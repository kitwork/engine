package work

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
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
	cache     map[string]*CachedResult

	databases map[string]*sql.DB
	dbMu      sync.Mutex
}

type cache struct {
	sync.RWMutex
	data map[string]*CachedResult
}

type CachedResult struct {
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

	// QUAN TRỌNG: Phải chạy VM để thực thi code trong app.js
	res := t.vm.Run()
	if res.K == value.Invalid {
		return fmt.Errorf("runtime error: %v", res.V)
	}

	return nil
}

func NewTenant(root string, domain string) *Tenant {

	tenant := &Tenant{
		config: &Config{
			root: root,
		},
		entity: &Entity{
			Identity: "test",
			Domain:   domain,
		},
		cache:     make(map[string]*CachedResult),
		MaxEnergy: 10000000,
	}

	if domain != "" {
		tenant.entity.Domain = domain
	}

	switch root {
	case "", "./", "../", "/", ".", "..":
		tenant.config.base = "."
		break
	}

	return tenant
}
