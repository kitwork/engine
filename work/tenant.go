package work

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/script"
	"github.com/kitwork/engine/value"
)

type Tenant struct {
	config   *Config
	entity   *Entity
	bytecode *compiler.Bytecode
	vm       *runtime.Runtime
	routes   []*Router

	cacheLock sync.RWMutex
	cache     map[string]*CachedResult
}

type CachedResult struct {
	Response *Response
	ExpireAt time.Time
}

func (t *Tenant) joinPath(paths ...string) string {
	// filepath.Join đã tự động gọi Clean() và xử lý dấu gạch chéo thông minh
	base := filepath.Join(t.config.source, t.entity.Identity, t.entity.Domain)
	return filepath.Join(append([]string{base}, paths...)...)
}

func (t *Tenant) appfile(filenames ...string) string {
	file := "app.js"
	if len(filenames) > 0 {
		file = filenames[0]
	}
	return t.joinPath(file)
}

func NewTenant(source string, domain string) (*Tenant, error) {

	tenant := &Tenant{
		config: &Config{
			source: source,
		},
		entity: &Entity{
			Identity: "test",
			Domain:   domain,
		},
		cache: make(map[string]*CachedResult),
	}

	bytecode, err := script.Bytecode(tenant.appfile())
	if err != nil {
		return nil, err
	}

	tenant.bytecode = bytecode
	tenant.vm = runtime.New(bytecode.Instructions, bytecode.Constants)
	tenant.routes = make([]*Router, 0)

	// TỐI ƯU: Đăng ký kitwork vào Builtin Index 0, trả về Struct KitWork
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(tenant.Config(args...))
	})
	tenant.vm.Builtins = []value.Value{kitworkFunc}

	// Giữ lại trong Globals
	tenant.vm.Globals["kitwork"] = kitworkFunc

	// QUAN TRỌNG: Phải chạy VM để thực thi code trong app.js
	tenant.vm.Run()
	return tenant, nil
}
