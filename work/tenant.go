package work

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/script"
	"github.com/kitwork/engine/value"
)

const kitwork = "kitwork"
const extension = "." + kitwork

type Tenant struct {
	config *Config
	entity *Entity

	bytecode  *compiler.Bytecode
	vm        *runtime.VM
	routes    *Routes
	MaxEnergy uint64

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
		t.config.base = filepath.Join(t.config.root, t.entity.Identity, t.entity.Domain)
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
	bytecode, err := script.Bytecode(t.AppFile())
	if err != nil {
		return err
	}

	t.bytecode = bytecode
	t.vm = runtime.New(bytecode.Instructions, bytecode.Constants)
	t.vm.MaxEnergy = t.MaxEnergy
	t.vm.SourceMap = bytecode.SourceMap
	t.routes = NewRoutes()

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
	identity := "test"
	if domain != "" {
		if dbIdentity, err := database.IdentitySystem(domain); err == nil && dbIdentity != "" {
			identity = dbIdentity
		} else {
			fmt.Println("Error identity system :", domain, " error : ", err)
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

	switch root {
	case "", "./", "../", "/", ".", "..":
		tenant.config.base = "."
		break
	}

	return tenant
}
