package work

import (
	"path/filepath"

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
	}

	bytecode, err := script.Bytecode(tenant.appfile())
	if err != nil {
		return nil, err
	}

	tenant.bytecode = bytecode
	tenant.vm = runtime.New(bytecode.Instructions, bytecode.Constants)
	tenant.routes = make([]*Router, 0)

	// TỐI ƯU SIÊU CẤP: Đăng ký kitwork vào Builtin Index 0
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		kw := tenant.Config(args...)
		res := make(map[string]value.Value)
		res["router"] = value.New(kw.Router())
		res["log"] = value.New(kw.Log())
		res["http"] = value.New(kw.HTTP())
		// res["render"] = value.New(kw.Render()) // Giả sử có Render()
		return value.New(res)
	})
	tenant.vm.Builtins = []value.Value{kitworkFunc}

	// Giữ lại trong Globals cho các trường hợp đặc biệt
	tenant.vm.Globals["kitwork"] = kitworkFunc

	// Hỗ trợ HTTP Fetch
	httpObj := tenant.Config().HTTP()
	tenant.vm.Globals["http"] = value.New(httpObj)
	tenant.vm.Globals["fetch"] = value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.NewNil()
		}
		return value.New(httpObj.Fetch(args[0], args[1:]...))
	})

	tenant.vm.Run()
	return tenant, nil
}
