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

// Eval walks a compiled IR node against scope — the Go twin of the composed client walker.
// One IR, two ends: the client walks it to render and react, the server walks the SAME data for
// truth (ctx.validate on submit, first-paint PreRender, go tests).
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
			if expressionGlobals[name] {
				return globalRef(name), nil // a global comes after every name the page declares
			}
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
	case "++", "--", "++$", "--$":
		// The server scope is flat, so the page-scope form writes where the lexical one does.
		name, _ := arr[1].(string)
		if blockedKey(name) {
			return nil, nil
		}
		old := float64(0) // a missing key reads as 0, like the client Proxy
		if v, ok := scope[name]; ok {
			old = num(v)
		}
		next := old + 1
		if op[:2] == "--" {
			next = old - 1
		}
		scope[name] = next
		if prefix, _ := arr[2].(bool); prefix {
			return next, nil
		}
		return old, nil
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
	case "idx":
		o, err := eval(arr[1], scope, budget)
		if err != nil {
			return nil, err
		}
		key, err := eval(arr[2], scope, budget)
		if err != nil {
			return nil, err
		}
		return index(o, key), nil
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
		if list, ok := o.([]any); ok {
			if handled, v, err := callArrayWithLambda(list, name, args, scope, budget); handled {
				return v, err
			}
		}
		if g, ok := o.(globalRef); ok {
			return callGlobalMethod(g, name, args), nil
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
		if g, ok := callee.(globalRef); ok {
			return callGlobal(g, args), nil
		}
		lam, ok := callee.(*Lambda)
		if !ok {
			return nil, nil // mirror the client: calling a non-lambda yields undefined
		}
		return invokeLambda(lam, args, scope, budget)
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

	// Logical operators short-circuit and return an operand, matching JavaScript and the client.
	l, err := eval(arr[1], scope, budget)
	if err != nil {
		return nil, err
	}
	if op == "&&" {
		if !truthy(l) {
			return l, nil
		}
		return eval(arr[2], scope, budget)
	}
	if op == "||" {
		if truthy(l) {
			return l, nil
		}
		return eval(arr[2], scope, budget)
	}
	if op == "??" {
		if l != nil {
			return l, nil
		}
		return eval(arr[2], scope, budget)
	}
	r, err := eval(arr[2], scope, budget)
	if err != nil {
		return nil, err
	}
	switch op {
	case "+":
		if ls, ok := l.(string); ok {
			return ls + toStr(r), nil
		}
		if rs, ok := r.(string); ok {
			return toStr(l) + rs, nil
		}
		return arith(l) + arith(r), nil
	case "-":
		return arith(l) - arith(r), nil
	case "*":
		return arith(l) * arith(r), nil
	case "/":
		return arith(l) / arith(r), nil
	case "%":
		return math.Mod(arith(l), arith(r)), nil
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
	case "===":
		return strictEq(l, r), nil
	case "!==":
		return !strictEq(l, r), nil
	}
	return nil, errors.New("hydrate: unknown op '" + op + "'")
}

// Truthy reports whether an Eval result counts as true — exported so a host can turn a rule's
// result into a verdict with the exact same semantics the walkers use internally.
func Truthy(v any) bool { return truthy(v) }

// blockedKey mirrors the client walker. The DOM-global names matter only in the browser, but using
// one denylist keeps server verdicts and client feedback consistent.
func blockedKey(name string) bool {
	switch name {
	case "constructor", "__proto__", "prototype",
		"__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__",
		"ownerDocument", "defaultView", "contentWindow",
		"window", "parent", "top", "self", "globalThis":
		return true
	default:
		return false
	}
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
// arith is a number for arithmetic. A value that is not there — null, or a member nobody set — is 0,
// as an undeclared name is: a missing cart.count + 1 is 1, not NaN (03/10). In a string it is ""
// (toStr), so a missing cart.name never prints "undefined". The kernel's walk does the same.
func arith(v any) float64 {
	if v == nil {
		return 0
	}
	return num(v)
}

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
// strictEq is JavaScript's ===: equal only when the kinds match and the values match — no
// coercion, so 1 === '1' is false where 1 == '1' is true. Numbers compare by value whatever Go
// type carried them (a JSON scope decodes to float64, a literal may be int).
func strictEq(l, r any) bool {
	switch lv := l.(type) {
	case nil:
		return r == nil
	case string:
		rv, ok := r.(string)
		return ok && lv == rv
	case bool:
		rv, ok := r.(bool)
		return ok && lv == rv
	}
	if isNumber(l) && isNumber(r) {
		return num(l) == num(r)
	}
	return false
}

func isNumber(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	}
	return false
}

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
// index mirrors the client's `o[key]`: a number indexes a list or a string (a rune, as JS reads
// a UTF-16 unit — the same for the BMP text these pages carry); any other key reads a map member
// by its string form, blocked names reading as nil like they do after a dot.
func index(o any, key any) any {
	if o == nil || key == nil {
		return nil
	}
	if f, ok := key.(float64); ok {
		i := int(f)
		if float64(i) != f || i < 0 {
			return nil
		}
		switch t := o.(type) {
		case []any:
			if i < len(t) {
				return t[i]
			}
			return nil
		case string:
			runes := []rune(t)
			if i < len(runes) {
				return string(runes[i])
			}
			return nil
		}
		return member(o, strconv.Itoa(i))
	}
	name := toStr(key)
	if blockedKey(name) {
		return nil
	}
	return member(o, name)
}

// invokeLambda runs a lambda VALUE with its arguments. Params overlay the calling scope; writes to
// NON-param keys flow back out (lexical), param bindings themselves stay local to the call. It is
// the one way a lambda runs — a bare inc() and a callback inside filter() go through here alike.
func invokeLambda(lam *Lambda, args []any, scope map[string]any, budget *int) (any, error) {
	if len(lam.Params) == 0 {
		return eval(lam.Body, scope, budget)
	}
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
}

// callArrayWithLambda is the half of the array method set that takes a callback: map, filter,
// find, some, every — the list the standalone KitJS 1.0.0 always offered. The callback receives
// (item, index, array) as JavaScript passes them, and every step of it counts against the ONE
// budget the whole expression shares, so a callback over a long list cannot escape the limit.
// A non-lambda argument yields undefined, as calling a non-function does everywhere else here.
func callArrayWithLambda(list []any, name string, args []any, scope map[string]any, budget *int) (bool, any, error) {
	switch name {
	case "map", "filter", "find", "some", "every":
	default:
		return false, nil, nil
	}
	if len(args) < 1 {
		return true, nil, nil
	}
	lam, ok := args[0].(*Lambda)
	if !ok {
		return true, nil, nil
	}
	var mapped, kept []any
	for i, item := range list {
		v, err := invokeLambda(lam, []any{item, float64(i), list}, scope, budget)
		if err != nil {
			return true, nil, err
		}
		switch name {
		case "map":
			mapped = append(mapped, v)
		case "filter":
			if truthy(v) {
				kept = append(kept, item)
			}
		case "find":
			if truthy(v) {
				return true, item, nil
			}
		case "some":
			if truthy(v) {
				return true, true, nil
			}
		case "every":
			if !truthy(v) {
				return true, false, nil
			}
		}
	}
	switch name {
	case "map":
		if mapped == nil {
			mapped = []any{}
		}
		return true, mapped, nil
	case "filter":
		if kept == nil {
			kept = []any{}
		}
		return true, kept, nil
	case "find":
		return true, nil, nil
	case "some":
		return true, false, nil
	}
	return true, true, nil // every, over a list with nothing that failed
}

// sliceIndex resolves a slice bound the JavaScript way: negative counts from the end, and both
// ends are clamped into the list.
func sliceIndex(v any, length int, fallback int) int {
	if v == nil {
		return fallback
	}
	n := int(num(v))
	if n < 0 {
		n += length
		if n < 0 {
			n = 0
		}
	}
	if n > length {
		n = length
	}
	return n
}

func member(o any, name string) any {
	switch t := o.(type) {
	case globalRef:
		return globalMember(t, name)
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
		case "split":
			// JavaScript's split: no separator gives the whole string as one piece, "" gives the
			// characters, anything else splits on it. It came with string escapes (30/09): until
			// then value.split('\n') could not even name a newline.
			if len(args) == 0 || args[0] == nil {
				return []any{t}
			}
			sep := toStr(args[0])
			var parts []string
			if sep == "" {
				for _, r := range t {
					parts = append(parts, string(r))
				}
			} else {
				parts = strings.Split(t, sep)
			}
			out := make([]any, len(parts))
			for i, part := range parts {
				out[i] = part
			}
			return out
		case "trim":
			return strings.TrimSpace(t)
		case "toLowerCase":
			return strings.ToLower(t)
		case "toUpperCase":
			return strings.ToUpper(t)
		}
	case []any:
		switch name {
		case "includes":
			if len(args) >= 1 {
				for _, item := range t {
					if strictEq(item, args[0]) {
						return true
					}
				}
				return false
			}
		case "indexOf":
			if len(args) >= 1 {
				for i, item := range t {
					if strictEq(item, args[0]) {
						return float64(i)
					}
				}
				return float64(-1)
			}
		case "join":
			sep := ","
			if len(args) >= 1 && args[0] != nil {
				sep = toStr(args[0])
			}
			parts := make([]string, len(t))
			for i, item := range t {
				if item != nil { // null and undefined join as empty, as in JavaScript
					parts[i] = toStr(item)
				}
			}
			return strings.Join(parts, sep)
		case "slice":
			var from, to any
			if len(args) >= 1 {
				from = args[0]
			}
			if len(args) >= 2 {
				to = args[1]
			}
			start, end := sliceIndex(from, len(t), 0), sliceIndex(to, len(t), len(t))
			if start >= end {
				return []any{}
			}
			return append([]any{}, t[start:end]...)
		case "concat":
			// JavaScript's concat: an array argument is spread one level, anything else is one
			// item. It is how a list GROWS in the grammar — tags = tags.concat([draft]) — without a
			// push that would mutate in place and leave the old array painted (30/09).
			out := append([]any{}, t...)
			for _, arg := range args {
				if items, ok := arg.([]any); ok {
					out = append(out, items...)
				} else {
					out = append(out, arg)
				}
			}
			return out
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
