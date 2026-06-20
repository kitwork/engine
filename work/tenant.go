package work

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

const kitwork = "kitwork"
const extension = "." + kitwork

// AppFileName is the entry filename every tenant must have (app.kitwork.js).
const AppFileName = "app" + extension + ".js"

type Tenant struct {
	config *Config
	entity *Entity

	bytecode  *compiler.Bytecode
	vm        *runtime.VM
	routes    *Routes
	MaxEnergy uint64

	env value.Value // env scoped của tenant này (đọc từ <path>/.env), lộ qua kitwork().env

	viewRender *Render // render mặc định cho ctx.view (đăng ký qua router.context({render}))

	// JIT CSS service mode (đăng ký qua router.jit()): phục vụ 1 stylesheet site-wide,
	// cached, tại jitRoute; jitInject = tự chèn <link> vào mỗi trang render. Set 1 lần
	// lúc boot khi app.kitwork.js chạy router.jit() — read-only khi phục vụ request.
	jitRoute  string
	jitInject bool

	cacheLock sync.RWMutex
	cache     map[string]*Responser

	databases map[string]*sql.DB
	dbMu      sync.Mutex

	// Rate Limiting fields
	rateLimiterMu    sync.Mutex
	currentLimiters  map[string]*RateLimiter
	previousLimiters map[string]*RateLimiter
	lastRotation     time.Time

	rateLimitEnabled  bool
	rateLimitRate     int
	rateLimitIpRate   int
	rateLimitUserRate int
	rateLimitPeriod   time.Duration
}

type Cache struct {
	sync.RWMutex
	data map[string]*Responser
}

type Responser struct {
	Response *Response
	ExpireAt time.Time
}

func (t *Tenant) resolve(paths ...string) string {
	if t.config.base == "" {
		switch t.config.root {
		case "", "./", "../", "/", ".", "..":
			t.config.base = "."
		default:
			if t.entity.Identity != "" {
				t.config.base = filepath.Join(t.config.root, t.entity.Identity, t.entity.Domain)
			} else {
				flatPath := filepath.Join(t.config.root, t.entity.Domain)
				testPath := filepath.Join(t.config.root, "test", t.entity.Domain)
				if _, err := os.Stat(filepath.Join(testPath, AppFileName)); err == nil {
					t.config.base = testPath
				} else {
					t.config.base = flatPath
				}
			}
		}
	}
	if len(paths) == 0 {
		return t.config.base
	}
	return filepath.Join(append([]string{t.config.base}, paths...)...)
}

func (t *Tenant) AppFile(filenames ...string) string {
	file := "app" + extension + ".js"
	if len(filenames) > 0 {
		file = filenames[0]
	}
	return t.resolve(file)
}

func (t *Tenant) Run() error {
	bytecode, err := compiler.CompileFile(t.AppFile())
	if err != nil {
		return err
	}

	t.bytecode = bytecode
	t.vm = runtime.New(bytecode.Instructions, bytecode.Constants)
	t.vm.MaxEnergy = t.MaxEnergy
	t.vm.SourceMap = bytecode.SourceMap
	t.routes = NewRoutes()

	// env scoped THEO PATH của tenant: chỉ đọc <root>/<identity>/<domain>/.env →
	// tenant không bao giờ thấy env của host hay tenant khác. Lộ qua kitwork().env.
	t.env = NewEnv(ParseDotEnv(t.resolve(".env")))

	// TỐI ƯU: Đăng ký kitwork vào Builtin Index 0, trả về Struct KitWork
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(t.Kitwork(args...))
	})
	t.vm.Builtins = []value.Value{kitworkFunc}

	// Giữ lại trong Globals
	t.vm.Globals[kitwork] = kitworkFunc

	// Inject console global helper
	consoleLog := value.NewFunc(func(args ...value.Value) value.Value {
		var sb strings.Builder
		for i, arg := range args {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(arg.Text())
		}
		fmt.Println("[console.log]", sb.String())
		return value.Value{K: value.Nil}
	})
	consoleObj := value.New(map[string]value.Value{
		"log": consoleLog,
	})
	t.vm.Globals["console"] = consoleObj

	// Inject JSON global helper
	jsonStringify := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Nil}
		}
		bytes, err := json.Marshal(args[0])
		if err != nil {
			return value.Value{K: value.Invalid, V: err.Error()}
		}
		return value.NewString(string(bytes))
	})
	jsonParse := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Nil}
		}
		var val value.Value
		err := json.Unmarshal([]byte(args[0].Text()), &val)
		if err != nil {
			return value.Value{K: value.Invalid, V: err.Error()}
		}
		return val
	})
	jsonObj := value.New(map[string]value.Value{
		"stringify": jsonStringify,
		"parse":     jsonParse,
	})
	t.vm.Globals["JSON"] = jsonObj

	// Inject JS-compatible globals: Math, Date (Date.now, new Date(), ...)
	injectJSCompat(t.vm.Globals)

	// Inject parseFloat global helper
	parseFloatFunc := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.New(0.0)
		}
		s := args[0].Text()
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return value.New(0.0)
		}
		return value.New(f)
	})
	t.vm.Globals["parseFloat"] = parseFloatFunc

	// Inject parseInt global helper
	parseIntFunc := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.New(0)
		}
		s := args[0].Text()
		base := 10
		if len(args) > 1 && args[1].K == value.Number {
			base = int(args[1].N)
		}
		if base < 2 || base > 36 {
			base = 10
		}
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			return value.New(0)
		}
		if base == 16 && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
			s = s[2:]
		}
		i, err := strconv.ParseInt(s, base, 64)
		if err == nil {
			return value.New(i)
		}
		var prefix strings.Builder
		for idx, ch := range s {
			if idx == 0 && (ch == '+' || ch == '-') {
				prefix.WriteRune(ch)
				continue
			}
			isValid := false
			if ch >= '0' && ch <= '9' {
				isValid = int(ch-'0') < base
			} else if ch >= 'a' && ch <= 'z' {
				isValid = int(ch-'a'+10) < base
			} else if ch >= 'A' && ch <= 'Z' {
				isValid = int(ch-'A'+10) < base
			}
			if !isValid {
				break
			}
			prefix.WriteRune(ch)
		}
		parsedStr := prefix.String()
		if parsedStr == "" || parsedStr == "+" || parsedStr == "-" {
			return value.New(0)
		}
		i, err = strconv.ParseInt(parsedStr, base, 64)
		if err != nil {
			return value.New(0)
		}
		return value.New(i)
	})
	t.vm.Globals["parseInt"] = parseIntFunc

	// QUAN TRỌNG: Phải chạy VM để thực thi code trong app.js
	res := t.vm.Run()
	if res.K == value.Invalid {
		return fmt.Errorf("runtime error: %v", res.V)
	}

	return nil
}

func NewTenant(root string, domain string) *Tenant {
	var identity string
	if domain != "" {
		if dbIdentity, err := database.IdentitySystem(domain); err == nil && dbIdentity != "" {
			identity = dbIdentity
		}
	}

	tenant := &Tenant{
		config: &Config{
			root: root,
		},
		entity: &Entity{
			Identity: identity,
			Domain:   domain,
		},
		cache:             make(map[string]*Responser),
		databases:         make(map[string]*sql.DB),
		currentLimiters:   make(map[string]*RateLimiter),
		previousLimiters:  make(map[string]*RateLimiter),
		lastRotation:      time.Now(),
		rateLimitEnabled:  RateLimitEnabled,
		rateLimitRate:     DefaultTenantRate,
		rateLimitIpRate:   DefaultTenantIpRate,
		rateLimitUserRate: DefaultTenantUserRate,
		rateLimitPeriod:   RateLimitPeriod,
	}

	return tenant
}
