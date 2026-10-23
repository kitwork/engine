package work

import (
	envhelper "github.com/kitwork/engine/utilities/env"
	"github.com/kitwork/engine/value"
)

// EnvironmentEntry is one value of a .env file as written (utilities/env): its text and whether it was
// quoted.
type EnvironmentEntry = envhelper.Entry

// ParseDotEnv reads KEY=VALUE pairs from a .env file into a map.
func ParseDotEnv(path string) map[string]string {
	return envhelper.ParseDotEnv(path)
}

// ParseDotEnvEntries reads a .env file with whether each value was quoted, which keeps it a string.
func ParseDotEnvEntries(path string) map[string]EnvironmentEntry {
	return envhelper.ParseDotEnvEntries(path)
}

// envVars is environment of an app (import { environment } from "kitwork", docs/KITWORK_ENVIRONMENT.md), and
// the manifest's env: read by name, each value coerced (mục 4), an empty one absent.
type envVars struct {
	vars    map[string]EnvironmentEntry
	missing *[]string
}

// NewEnv is the environment object over vars, which it never changes.
func NewEnv(vars map[string]EnvironmentEntry) value.Value {
	return value.Value{K: value.Proxy, V: &envVars{vars: vars}}
}

// NewEnvWithMissing is NewEnv that records each key require() did not find (the manifest gathers them into
// one boot error).
func NewEnvWithMissing(vars map[string]EnvironmentEntry, missing *[]string) value.Value {
	return value.Value{K: value.Proxy, V: &envVars{vars: vars, missing: missing}}
}

// TextEntries are values of the operating system, or any other never quoted.
func TextEntries(vars map[string]string) map[string]EnvironmentEntry {
	entries := make(map[string]EnvironmentEntry, len(vars))
	for key, text := range vars {
		entries[key] = EnvironmentEntry{Text: text}
	}
	return entries
}

func (e *envVars) lookup(k string) (EnvironmentEntry, bool) {
	v, ok := e.vars[k]
	return v, ok && v.Present()
}

// has reports a key with a value: what environment.require finds.
func (e *envVars) has(k string) bool {
	_, ok := e.lookup(k)
	return ok
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
		// in an app the boot has checked every environment.require("…") already (environmentMissing):
		// only the manifest gets here with a key it lacks
		if e.missing != nil {
			*e.missing = append(*e.missing, k)
		}
		return value.Value{K: value.Nil}
	}
	// environment.API_KEY() calls a value, not a method: environment has only require, so it is the
	// not-a-function runtime error (14/10/2026), which never shows a string's text. Until then it gave null.
	if method != "" {
		return value.NotAFunction(method, e.OnGet(method))
	}
	return value.Value{K: value.Nil}
}

// Environment is environment of import { environment } from "kitwork" (docs/KITWORK_ENVIRONMENT.md, 22/10/2026;
// env until then): the app's .env, the site's laid over it key by key, and the keys application.work shares
// with the app. Never the host's .env nor the operating system's.
func (w *KitWork) Environment() value.Value {
	if w.tenant != nil {
		if generation := w.tenant.SiteGeneration(); generation != nil {
			if environment := generation.Environment(); environment.K == value.Proxy {
				return environment
			}
		}
		if w.tenant.env.K == value.Proxy {
			return w.tenant.env
		}
	}
	return NewEnv(nil)
}
