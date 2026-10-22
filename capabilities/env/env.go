package env

import (
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
	// env.API_KEY() calls a value, not a method: the not-a-function runtime error (14/10/2026), as
	// work's env answers it.
	if method != "" {
		return value.NotAFunction(method, e.OnGet(method))
	}
	return value.Value{K: value.Nil}
}

func init() {
	capabilities.DefaultRegistry.Register("env", func(scope capabilities.Scope) value.Value {
		// The app's own .env and nothing above it (docs/KITWORK_ENVIRONMENT.md §1, 22/10/2026): no
		// fallback to ../.env, which for apps/<identity>/ is apps/.env, a file every app would read.
		vars := envhelper.ParseDotEnv(scope.ResolvePath(".env"))
		return value.Value{K: value.Proxy, V: &envVars{vars: vars}}
	})
}
