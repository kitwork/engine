package env

import (
	"path/filepath"

	"github.com/kitwork/engine/capabilities"
	envhelper "github.com/kitwork/engine/utilities/env"
	"github.com/kitwork/engine/value"
)

type envVars struct {
	vars map[string]string
}

func (e *envVars) lookup(k string) (string, bool) {
	v, ok := e.vars[k]
	return v, ok && v != ""
}

func (e *envVars) OnGet(key string) value.Value {
	if v, ok := e.lookup(key); ok {
		return envhelper.CoerceValue(v)
	}
	return value.Value{K: value.Nil}
}

func (e *envVars) OnCompare(op string, other value.Value) value.Value {
	return value.Value{K: value.Nil}
}

func (e *envVars) OnInvoke(method string, args ...value.Value) value.Value {
	if method == "require" {
		if len(args) == 0 {
			return value.Value{K: value.Nil}
		}
		k := args[0].Text()
		if v, ok := e.lookup(k); ok {
			return envhelper.CoerceValue(v)
		}
		return value.Value{K: value.Nil}
	}
	return value.Value{K: value.Nil}
}

func init() {
	capabilities.DefaultRegistry.Register("env", func(scope capabilities.Scope) value.Value {
		envPath := scope.ResolvePath(".env")
		vars := envhelper.ParseDotEnv(envPath)
		if len(vars) == 0 {
			vars = envhelper.ParseDotEnv(filepath.Join(scope.ResolvePath(), "..", ".env"))
		}
		return value.Value{K: value.Proxy, V: &envVars{vars: vars}}
	})
}
