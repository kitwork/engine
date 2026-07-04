package hydrate

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// evalBudget caps how many IR nodes one Eval may visit — the same gas idea as the backend VM's
// MaxEnergy, built in from day one so this walker can later serve capsule-style evaluation safely.
const evalBudget = 10000

var errBudget = errors.New("hydrate: evaluation budget exceeded")

// Lambda is a compiled function VALUE — a named IR tree with parameter names. It is pure data
// (serializable, inspectable); calling it means walking Body with the same budgeted walker.
type Lambda struct {
	Params []string
	Body   any
}

// Eval walks a compiled IR node against scope — the Go twin of the ~30-line client walker in
// runtime.js. One IR, two ends: the client walks it for instant feedback (e.g. validate as you
// type), the server walks the SAME data for truth (validate on submit, first-paint, go tests).
// Scope values follow JSON conventions: float64 numbers, string, bool, nil, []any, map[string]any.
func Eval(x any, scope map[string]any) (any, error) {
	budget := evalBudget
	return eval(x, scope, &budget)
}

func eval(x any, scope map[string]any, budget *int) (any, error) {
	*budget--
	if *budget < 0 {
		return nil, errBudget
	}
	arr, ok := x.([]any)
	if !ok || len(arr) == 0 {
		return x, nil
	}
	op, _ := arr[0].(string)
	switch op {
	case "#":
		return arr[1], nil
	case "$":
		name, _ := arr[1].(string)
		if name == "$" {
			return scope, nil // `$` = the page scope itself; on the server the scope IS the root
		}
		v, ok := scope[name]
		if !ok {
			return float64(0), nil // mirror the client Proxy: a missing key reads as 0
		}
		switch n := v.(type) { // normalize hand-built scopes to JSON's float64
		case int:
			return float64(n), nil
		case int64:
			return float64(n), nil
		}
		return v, nil
	case "=", "=$":
		// On the server the scope is flat (one map), so the lexical write and the page-scope
		// write land in the same place; the client walker distinguishes them across scopes.
		name, _ := arr[1].(string)
		if blockedKey(name) {
			return nil, nil
		}
		v, err := eval(arr[2], scope, budget)
		if err != nil {
			return nil, err
		}
		scope[name] = v
		return v, nil
	case "?":
		c, err := eval(arr[1], scope, budget)
		if err != nil {
			return nil, err
		}
		if truthy(c) {
			return eval(arr[2], scope, budget)
		}
		return eval(arr[3], scope, budget)
	case ".":
		o, err := eval(arr[1], scope, budget)
		if err != nil {
			return nil, err
		}
		name, _ := arr[2].(string)
		if blockedKey(name) {
			return nil, nil
		}
		return member(o, name), nil
	case "()":
		o, err := eval(arr[1], scope, budget)
		if err != nil {
			return nil, err
		}
		name, _ := arr[2].(string)
		if blockedKey(name) {
			return nil, nil
		}
		rawArgs, _ := arr[3].([]any)
		args := make([]any, len(rawArgs))
		for i, ra := range rawArgs {
			v, err := eval(ra, scope, budget)
			if err != nil {
				return nil, err
			}
			args[i] = v
		}
		return call(o, name, args), nil
	case "{}":
		pairs, _ := arr[1].([]any)
		m := make(map[string]any, len(pairs))
		for _, pr := range pairs {
			pa, ok := pr.([]any)
			if !ok || len(pa) != 2 {
				continue
			}
			k, _ := pa[0].(string)
			v, err := eval(pa[1], scope, budget)
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		return m, nil
	case "[]":
		items, _ := arr[1].([]any)
		out := make([]any, 0, len(items))
		for _, it := range items {
			v, err := eval(it, scope, budget)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case "=>":
		// A lambda VALUE: pure data (params + body IR). Nothing executes until it is called.
		rawParams, _ := arr[1].([]any)
		params := make([]string, 0, len(rawParams))
		for _, rp := range rawParams {
			if s, ok := rp.(string); ok {
				params = append(params, s)
			}
		}
		return &Lambda{Params: params, Body: arr[2]}, nil
	case ";":
		var v any
		for _, e := range arr[1:] {
			var err error
			v, err = eval(e, scope, budget)
			if err != nil {
				return nil, err
			}
		}
		return v, nil
	case "call":
		callee, err := eval(arr[1], scope, budget)
		if err != nil {
			return nil, err
		}
		rawArgs, _ := arr[2].([]any)
		args := make([]any, len(rawArgs))
		for i, ra := range rawArgs {
			v, err := eval(ra, scope, budget)
			if err != nil {
				return nil, err
			}
			args[i] = v
		}
		lam, ok := callee.(*Lambda)
		if !ok {
			return nil, nil // mirror the client: calling a non-lambda yields undefined
		}
		if len(lam.Params) == 0 {
			return eval(lam.Body, scope, budget)
		}
		// Params overlay the calling scope; writes to NON-param keys flow back out (lexical),
		// param bindings themselves stay local to the call.
		merged := make(map[string]any, len(scope)+len(lam.Params))
		for k, v := range scope {
			merged[k] = v
		}
		isParam := make(map[string]bool, len(lam.Params))
		for i, pn := range lam.Params {
			isParam[pn] = true
			if i < len(args) {
				merged[pn] = args[i]
			} else {
				merged[pn] = nil
			}
		}
		out, err := eval(lam.Body, merged, budget)
		if err != nil {
			return nil, err
		}
		for k, v := range merged {
			if !isParam[k] {
				scope[k] = v
			}
		}
		return out, nil
	case "u!":
		v, err := eval(arr[1], scope, budget)
		if err != nil {
			return nil, err
		}
		return !truthy(v), nil
	case "u-":
		v, err := eval(arr[1], scope, budget)
		if err != nil {
			return nil, err
		}
		return -num(v), nil
	}

	// Binary operators. Like the client walker, BOTH operands are evaluated (no short-circuit),
	// and &&/|| return an operand — JS value semantics.
	l, err := eval(arr[1], scope, budget)
	if err != nil {
		return nil, err
	}
	r, err := eval(arr[2], scope, budget)
	if err != nil {
		return nil, err
	}
	switch op {
	case "&&":
		if truthy(l) {
			return r, nil
		}
		return l, nil
	case "||":
		if truthy(l) {
			return l, nil
		}
		return r, nil
	case "+":
		if ls, ok := l.(string); ok {
			return ls + toStr(r), nil
		}
		if rs, ok := r.(string); ok {
			return toStr(l) + rs, nil
		}
		return num(l) + num(r), nil
	case "-":
		return num(l) - num(r), nil
	case "*":
		return num(l) * num(r), nil
	case "/":
		return num(l) / num(r), nil
	case "%":
		return math.Mod(num(l), num(r)), nil
	case ">":
		return num(l) > num(r), nil
	case "<":
		return num(l) < num(r), nil
	case ">=":
		return num(l) >= num(r), nil
	case "<=":
		return num(l) <= num(r), nil
	case "==":
		return looseEq(l, r), nil
	case "!=":
		return !looseEq(l, r), nil
	}
	return nil, errors.New("hydrate: unknown op '" + op + "'")
}

// Truthy reports whether an Eval result counts as true — exported so a host can turn a rule's
// result into a verdict with the exact same semantics the walkers use internally.
func Truthy(v any) bool { return truthy(v) }

// blockedKey mirrors the client walker: constructor/__proto__/prototype are the only member names
// that can reach code execution or prototype pollution, so member access, method calls and writes
// to them all resolve to nil. (The Go walker cannot reach a function constructor anyway, but the
// guard keeps the two ends byte-for-byte in agreement.)
func blockedKey(name string) bool {
	return name == "constructor" || name == "__proto__" || name == "prototype"
}

// truthy follows JS: false, 0, NaN, "", null/undefined are falsy; everything else truthy.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case string:
		return t != ""
	}
	return true
}

// num coerces to number the JS way: bools → 0/1, numeric strings parse, "" → 0, nil → NaN
// (nil stands for undefined — a MISSING scope key never reaches here, "$" already returns 0).
func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	return math.NaN()
}

// toStr renders a value the way JS string-concat would ("1" not "1.000000").
func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

// looseEq is JS == for the value kinds IR carries: same-type compares directly; mixed kinds fall
// back to numeric coercion (so "" == 0, "6" == 6 — the JS results).
func looseEq(l, r any) bool {
	if ls, ok := l.(string); ok {
		if rs, ok := r.(string); ok {
			return ls == rs
		}
	}
	if lb, ok := l.(bool); ok {
		if rb, ok := r.(bool); ok {
			return lb == rb
		}
	}
	if l == nil || r == nil {
		return l == nil && r == nil
	}
	ln, rn := num(l), num(r)
	if math.IsNaN(ln) || math.IsNaN(rn) {
		return false
	}
	return ln == rn
}

// member resolves property access. `.length` counts runes — matches JS for all typical text
// (differs only on surrogate pairs like emoji, where JS counts UTF-16 units).
func member(o any, name string) any {
	switch t := o.(type) {
	case string:
		if name == "length" {
			return float64(utf8.RuneCountInString(t))
		}
	case []any:
		if name == "length" {
			return float64(len(t))
		}
	case map[string]any:
		return t[name]
	}
	return nil
}

// call implements the small method set the client gets natively from JS. Unknown methods return
// nil (the client walker likewise returns undefined) — same verdict on both ends.
func call(o any, name string, args []any) any {
	switch t := o.(type) {
	case string:
		switch name {
		case "includes":
			if len(args) == 1 {
				return strings.Contains(t, toStr(args[0]))
			}
		case "startsWith":
			if len(args) == 1 {
				return strings.HasPrefix(t, toStr(args[0]))
			}
		case "endsWith":
			if len(args) == 1 {
				return strings.HasSuffix(t, toStr(args[0]))
			}
		case "trim":
			return strings.TrimSpace(t)
		case "toLowerCase":
			return strings.ToLower(t)
		case "toUpperCase":
			return strings.ToUpper(t)
		}
	case float64:
		if name == "toFixed" {
			d := 0
			if len(args) == 1 {
				d = int(num(args[0]))
			}
			return strconv.FormatFloat(t, 'f', d, 64)
		}
	}
	return nil
}
