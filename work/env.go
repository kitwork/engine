package work

import (
	envhelper "github.com/kitwork/engine/utilities/env"
	"github.com/kitwork/engine/value"
)

// ParseDotEnv reads KEY=VALUE pairs from a .env file into a map.
func ParseDotEnv(path string) map[string]string {
	return envhelper.ParseDotEnv(path)
}

type envVars struct {
	vars    map[string]string
	missing *[]string
}

func NewEnv(vars map[string]string) value.Value {
	return value.Value{K: value.Proxy, V: &envVars{vars: vars}}
}

func NewEnvWithMissing(vars map[string]string, missing *[]string) value.Value {
	return value.Value{K: value.Proxy, V: &envVars{vars: vars, missing: missing}}
}

func (e *envVars) lookup(k string) (string, bool) {
	v, ok := e.vars[k]
	return v, ok && v != ""
}

func (e *envVars) OnGet(key string) value.Value {
	if v, ok := e.lookup(key); ok {
		return coerceEnv(v)
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
			return coerceEnv(v)
		}
		if e.missing != nil {
			*e.missing = append(*e.missing, k)
		}
		return value.Value{K: value.Nil}
	}
	return value.Value{K: value.Nil}
}

func coerceEnv(v string) value.Value {
	return envhelper.CoerceValue(v)
}

func (w *KitWork) Env() value.Value {
	if w.tenant != nil && w.tenant.env.K == value.Proxy {
		return w.tenant.env
	}
	return NewEnv(nil)
}
