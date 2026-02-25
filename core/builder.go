package core

import (
	"fmt"

	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

type MockProxy struct{}

func (m *MockProxy) OnGet(key string) value.Value {
	return value.Value{K: value.Proxy, V: m}
}
func (m *MockProxy) OnCompare(op string, other value.Value) value.Value {
	return value.NewBool(false)
}
func (m *MockProxy) OnInvoke(method string, args ...value.Value) value.Value {
	return value.Value{K: value.Proxy, V: m}
}

func createAppObj(e *Engine, tenantID, domain, sourcePath string) map[string]value.Value {
	appObj := make(map[string]value.Value)

	mockFn := value.NewFunc(func(args ...value.Value) value.Value {
		return value.Value{K: value.Proxy, V: &MockProxy{}}
	})

	appObj["entity"] = mockFn
	appObj["log"] = mockFn
	appObj["http"] = mockFn
	appObj["smtp"] = mockFn
	appObj["chrome"] = mockFn
	appObj["database"] = mockFn
	appObj["postgres"] = mockFn
	appObj["redis"] = mockFn

	appObj["render"] = value.NewFunc(func(args ...value.Value) value.Value {
		m := make(map[string]value.Value)
		m["layout"] = value.NewFunc(func(args ...value.Value) value.Value { return value.New(m) })
		m["bind"] = value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() })
		return value.New(m)
	})

	appObj["schedule"] = value.NewFunc(func(args ...value.Value) value.Value {
		m := make(map[string]value.Value)
		m["daily"] = value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() })
		m["every"] = value.NewFunc(func(args ...value.Value) value.Value { return value.NewNull() })
		return value.New(m)
	})

	appObj["include"] = value.NewFunc(func(args ...value.Value) value.Value {
		return value.NewNull()
	})

	appObj["router"] = value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(createRouterObj("", e, tenantID, domain, sourcePath))
	})

	appObj["get"] = createRouteBuilder("GET", "", e, tenantID, domain, sourcePath)
	appObj["post"] = createRouteBuilder("POST", "", e, tenantID, domain, sourcePath)
	appObj["put"] = createRouteBuilder("PUT", "", e, tenantID, domain, sourcePath)
	appObj["delete"] = createRouteBuilder("DELETE", "", e, tenantID, domain, sourcePath)

	return appObj
}

func createRouterObj(prefix string, e *Engine, tenantID, domain, sourcePath string) map[string]value.Value {
	r := make(map[string]value.Value)

	chain := func() value.Value { return value.New(r) }

	r["rateLimit"] = value.NewFunc(func(args ...value.Value) value.Value { return chain() })
	r["bodyLimit"] = value.NewFunc(func(args ...value.Value) value.Value { return chain() })

	baseFn := value.NewFunc(func(args ...value.Value) value.Value {
		p := ""
		if len(args) > 0 {
			p = args[0].Text()
		}
		return value.New(createRouterObj(prefix+p, e, tenantID, domain, sourcePath))
	})
	r["base"] = baseFn
	r["group"] = baseFn

	r["get"] = createRouteBuilder("GET", prefix, e, tenantID, domain, sourcePath)
	r["post"] = createRouteBuilder("POST", prefix, e, tenantID, domain, sourcePath)
	r["put"] = createRouteBuilder("PUT", prefix, e, tenantID, domain, sourcePath)
	r["delete"] = createRouteBuilder("DELETE", prefix, e, tenantID, domain, sourcePath)
	r["any"] = createRouteBuilder("ANY", prefix, e, tenantID, domain, sourcePath)

	return r
}

func createRouteBuilder(method, prefix string, e *Engine, tenantID, domain, sourcePath string) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) < 1 {
			return value.NewNull()
		}
		path := prefix + args[0].Text()
		name := fmt.Sprintf("rt_%s_%s_%s_%s", tenantID, domain, method, path)
		wTmp := work.New(name)
		wTmp.Entity = tenantID
		wTmp.Domain = domain
		wTmp.SourcePath = sourcePath
		rt := &work.Router{Work: *wTmp, Method: method, Path: path}

		e.RegistryMu.Lock()
		e.Routers = append(e.Routers, rt)
		e.RegistryMu.Unlock()

		b := make(map[string]value.Value)

		chain := func() value.Value { return value.New(b) }

		b["handle"] = value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) > 0 {
				rt.Done(args[0])
			}
			return chain()
		})
		b["folder"] = value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) > 0 {
				dir := args[0].Text()
				e.RegistryMu.Lock()
				e.Config.Assets = append(e.Config.Assets, Asset{Path: path, Dir: dir})
				e.RegistryMu.Unlock()
			}
			return chain()
		})
		b["file"] = b["folder"]
		b["redirect"] = value.NewFunc(func(args ...value.Value) value.Value { return chain() })
		b["forward"] = value.NewFunc(func(args ...value.Value) value.Value { return chain() })
		b["cache"] = value.NewFunc(func(args ...value.Value) value.Value { return chain() })
		b["fail"] = value.NewFunc(func(args ...value.Value) value.Value { return chain() })
		b["done"] = value.NewFunc(func(args ...value.Value) value.Value { return chain() })
		b["status"] = value.NewFunc(func(args ...value.Value) value.Value { return chain() })
		b["guard"] = b["handle"]

		return value.New(b)
	})
}
