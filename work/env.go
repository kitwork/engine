package work

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// ParseDotEnv reads KEY=VALUE pairs from a .env file into a map. A missing file
// is NOT an error (returns an empty map). Supports blank lines, `#` comments,
// an optional `export ` prefix, and surrounding "double"/'single' quotes.
//
// It does NOT touch the process environment — the result is a SCOPED map you
// hand to NewEnv. That scoping is what makes per-tenant isolation possible: a
// tenant only ever sees the map built from its OWN path.
func ParseDotEnv(path string) map[string]string {
	vars := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return vars
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key != "" {
			vars[key] = val
		}
	}
	return vars
}

// envVars exposes a fixed map as the `env` object (a value.Proxy):
//
//	env.KEY           → giá trị TỰ ÉP KIỂU (bool/number/string), nil nếu thiếu
//	env.require("K")  → như trên nhưng BẮT BUỘC; thiếu → ghi vào missing (fail-fast)
//
// Dùng default kiểu JS:  env.PORT || 8080,  env.HOST || "localhost".
// Là Proxy nên truy cập đi qua OnGet/OnInvoke, không đụng method ép-kiểu built-in
// trên value (int/float/string/json/len).
type envVars struct {
	vars    map[string]string
	missing *[]string // optional: env.require misses appended here (server boot)
}

// NewEnv builds a scoped `env` object over vars. The object can ONLY read vars —
// pass a tenant's own .env map for isolation, or the host map for the server.
func NewEnv(vars map[string]string) value.Value {
	return value.Value{K: value.Proxy, V: &envVars{vars: vars}}
}

// NewEnvWithMissing is NewEnv plus fail-fast tracking: env.require on a missing
// key appends it to *missing (caller checks after eval and refuses to boot).
func NewEnvWithMissing(vars map[string]string, missing *[]string) value.Value {
	return value.Value{K: value.Proxy, V: &envVars{vars: vars, missing: missing}}
}

func (e *envVars) lookup(k string) (string, bool) {
	v, ok := e.vars[k]
	return v, ok && v != ""
}

// OnGet xử lý env.KEY — TỰ ÉP KIỂU: "true"/"false"→bool, số→number, còn lại→string.
// Nhờ vậy chỉ cần `env.PORT || 8080`, `env.ALLOW_LOCAL || false` ở mọi nơi, không
// cần env.int/env.bool.
func (e *envVars) OnGet(key string) value.Value {
	if v, ok := e.lookup(key); ok {
		return coerceEnv(v)
	}
	return value.Value{K: value.Nil}
}

func (e *envVars) OnCompare(op string, other value.Value) value.Value {
	return value.Value{K: value.Nil}
}

// OnInvoke chỉ còn env.require("KEY") — thứ duy nhất KHÔNG diễn đạt được bằng
// env.KEY: bắt buộc phải có, thiếu là gom vào missing để fail-fast lúc boot.
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

// coerceEnv biến chuỗi env thành kiểu tự nhiên: bool > int > float > string.
func coerceEnv(v string) value.Value {
	t := strings.TrimSpace(v)
	switch strings.ToLower(t) {
	case "true":
		return value.New(true)
	case "false":
		return value.New(false)
	}
	if n, err := strconv.Atoi(t); err == nil {
		return value.New(n)
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return value.New(f)
	}
	return value.NewString(v)
}

// Env exposes the tenant's OWN scoped env to its app code: kitwork().env. It is
// built from <root>/<identity>/<domain>/.env — a tenant can never read another
// tenant's or the host's env (path isolation).
func (w *KitWork) Env() value.Value {
	if w.tenant != nil && w.tenant.env.K == value.Proxy {
		return w.tenant.env
	}
	return NewEnv(nil)
}
