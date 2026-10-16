package hydrate

import (
	"errors"
	"math"
)

// evalBudget caps how many IR nodes one Eval may visit — the same gas idea as the backend VM's
// MaxEnergy, built in from day one so no expression can run unbounded. The kernel's run counts the
// same nodes (evalRemaining).
const evalBudget = 10000

// callDepthLimit is how deep calls may nest, lambdas and native functions alike, as the kernel's call
// counts them (callDepth): f = (n) => n < 1 ? 0 : f(n - 1) runs f(63) and stops at f(64) on both ends
// (16/10/2026: the kernel stopped there and the server ran on to the budget).
const callDepthLimit = 64

var errBudget = errors.New("hydrate: evaluation budget exceeded")

// Lambda is a compiled function VALUE — a named IR tree with parameter names. It is pure data
// (serializable, inspectable); calling it means walking Body with the same budgeted walker. It is a
// function as JavaScript has one, as the kernel's lambda is (16/10/2026): one lambda keeps one
// identity, JSON leaves it out, and its text is Source, the lambda as written.
type Lambda struct {
	Params []string
	Body   any
	Source string
	// made is the scope the lambda was made in: where it runs when no expression calls it (its text
	// asked for after the evaluation), as the kernel's lambda falls back to the scope that made it.
	made *frame
	// text is its own toString, made with it, so that f.toString === f.toString as in the kernel
	// (16/10/2026: each read made a new one)
	text *boundFunc
}

// Func is a helper the host places in a scope (Kitwork UI's money, date…). Calling it by name from
// an expression runs it with the evaluated arguments; the client side has its own copy of each.
type Func func(args []any) any

// frame is a scope an expression reads: a lambda's parameters over the scope that calls it, down to
// the page scope, which has no frame under it. A name reads the nearest frame that has it; a write
// lands in the parameter it names, else in the page scope — the kernel's overlay of a lambda.
type frame struct {
	vars map[string]any
	up   *frame
}

func (f *frame) lookup(name string) (any, bool) {
	for ; f != nil; f = f.up {
		if f.up == nil && name == "$" {
			return nil, false // $ is the page scope itself, never a key of it
		}
		if v, ok := f.vars[name]; ok {
			return norm(v), true
		}
	}
	return nil, false
}

func (f *frame) assign(name string, v any) {
	for ; f.up != nil; f = f.up {
		if _, ok := f.vars[name]; ok {
			f.vars[name] = v
			return
		}
	}
	f.vars[name] = v
}

// machine is one evaluation: the budget it spends, the calls in progress, the page scope.
type machine struct {
	budget  int
	depth   int
	root    map[string]any
	top     *frame
	joining []any // the lists a join is inside of: one met again is "", as V8 writes a list in itself
	mutated []any // the list a method that changes its list made, for the call to write back
	// extras are the members a list holds beside its items: index, input and groups of a match
	extras map[uintptr]*Object
}

func newMachine(scope map[string]any) *machine {
	if scope == nil {
		scope = map[string]any{}
	}
	m := &machine{budget: evalBudget, root: scope}
	m.top = &frame{vars: scope}
	return m
}

// Eval walks a compiled IR node against scope — the Go twin of the composed client walker.
// One IR, two ends: the client walks it to render and react, the server walks the SAME data for
// truth (ctx.validate on submit, first-paint PreRender, go tests), and the two give JavaScript's
// answer (16/10/2026). Scope values follow JSON conventions: float64 numbers, string, bool, nil,
// []any, map[string]any. A result may also be Undefined, an *Object an expression made, a *Lambda;
// Plain makes plain data of it, Text the text the page shows.
func Eval(x any, scope map[string]any) (any, error) {
	m := newMachine(scope)
	return m.eval(m.top, x)
}

// EvalText evaluates x and gives the text a page writes of it, as the kernel's text binding does:
// null and undefined nothing, anything else String(v), its lone halves of a pair as U+FFFD. An object
// whose toString is a lambda runs it in the scope that made it, as the browser runs a lambda no
// expression calls: no frame calls it here (16/10/2026: it ran in the page scope, so a toString made
// inside a call, ((p) => ({ toString: () => p }))(5), printed 0 here and 5 in the browser).
func EvalText(x any, scope map[string]any) (string, error) {
	m := newMachine(scope)
	v, err := m.eval(m.top, x)
	if err != nil {
		return "", err
	}
	if nullish(v) {
		return "", nil
	}
	m.budget = evalBudget // the kernel's String(v) runs outside the expression, on a budget of its own
	s, err := m.toString(nil, v)
	if err != nil {
		return "", err
	}
	return pageText(s), nil
}

func (m *machine) eval(f *frame, x any) (any, error) {
	m.budget--
	if m.budget < 0 {
		return nil, errBudget
	}
	arr, ok := x.([]any)
	if !ok || len(arr) == 0 {
		return norm(x), nil
	}
	op, _ := arr[0].(string)
	switch op {
	case "#":
		return norm(arr[1]), nil
	case "$":
		name, _ := arr[1].(string)
		return m.readName(f, name), nil
	case "=", "=$":
		// The lexical write lands in the parameter it names, else in the page scope; $.name = v always
		// in the page scope.
		name, _ := arr[1].(string)
		if blockedKey(name) {
			return Undefined, nil
		}
		v, err := m.eval(f, arr[2])
		if err != nil {
			return nil, err
		}
		if op == "=$" {
			m.root[name] = v
		} else {
			f.assign(name, v)
		}
		return v, nil
	case "++", "--", "++$", "--$":
		name, _ := arr[1].(string)
		if blockedKey(name) {
			return Undefined, nil
		}
		// the name read as the kernel reads it: $.name off the page object, a bare name through the
		// scope (a global, else 0); then Number of it, a value that is not there 0
		var was any
		if len(op) == 3 {
			v, err := m.get(f, m.root, name)
			if err != nil {
				return nil, err
			}
			was = v
		} else {
			was = m.readName(f, name)
		}
		old, err := m.toNumber(f, present(was))
		if err != nil {
			return nil, err
		}
		next := old + 1
		if op[:2] == "--" {
			next = old - 1
		}
		if len(op) == 3 {
			m.root[name] = next
		} else {
			f.assign(name, next)
		}
		if prefix, _ := arr[2].(bool); prefix {
			return next, nil
		}
		return old, nil
	case "?":
		c, err := m.eval(f, arr[1])
		if err != nil {
			return nil, err
		}
		if truthy(c) {
			return m.eval(f, arr[2])
		}
		return m.eval(f, arr[3])
	case ".":
		o, err := m.eval(f, arr[1])
		if err != nil {
			return nil, err
		}
		name, _ := arr[2].(string)
		if nullish(o) || blockedKey(name) {
			return Undefined, nil
		}
		return m.get(f, o, name)
	case "idx":
		o, err := m.eval(f, arr[1])
		if err != nil {
			return nil, err
		}
		key, err := m.eval(f, arr[2])
		if err != nil {
			return nil, err
		}
		if nullish(o) {
			return Undefined, nil
		}
		// a key is its text, as JavaScript makes it: keys[null] reads keys["null"] (16/10/2026, both ends)
		name, err := m.propertyKey(f, key)
		if err != nil {
			return nil, err
		}
		if blockedKey(name) {
			return Undefined, nil
		}
		return m.get(f, o, name)
	case "()":
		o, place, err := m.receiver(f, arr[1])
		if err != nil {
			return nil, err
		}
		name, _ := arr[2].(string)
		if computed, ok := arr[2].([]any); ok {
			// o[k](…): the key is read after what it is a member of, before the arguments, as in the kernel
			key, err := m.eval(f, computed)
			if err != nil {
				return nil, err
			}
			if nullish(o) {
				return Undefined, nil
			}
			if name, err = m.propertyKey(f, key); err != nil {
				return nil, err
			}
		}
		if nullish(o) || blockedKey(name) {
			return Undefined, nil
		}
		args, err := m.evalArgs(f, arr[3])
		if err != nil {
			return nil, err
		}
		if g, ok := o.(globalRef); ok && (g == "Math" || g == "Number") {
			presentAll(args) // as the kernel hands Math and Number undefined as null
		}
		member, err := m.get(f, o, name)
		if err != nil {
			return nil, err
		}
		if !callable(member) {
			return Undefined, nil
		}
		m.mutated = nil
		out, err := m.call(f, member, o, args)
		if changed := m.mutated; changed != nil {
			m.mutated = nil
			if place != nil && err == nil {
				place(changed)
			}
		}
		return out, err
	case "{}":
		pairs, _ := arr[1].([]any)
		obj := newObject(len(pairs))
		for _, pr := range pairs {
			pa, ok := pr.([]any)
			if !ok || len(pa) != 2 {
				continue
			}
			k, _ := pa[0].(string)
			if k == "__proto__" {
				continue // it would set the prototype the data chose, not a key: dropped, as in the kernel
			}
			v, err := m.eval(f, pa[1])
			if err != nil {
				return nil, err
			}
			obj.set(k, v)
		}
		return obj, nil
	case "[]":
		items, _ := arr[1].([]any)
		out := newList(len(items))
		for i, it := range items {
			v, err := m.eval(f, it)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case "=>":
		rawParams, _ := arr[1].([]any)
		params := make([]string, 0, len(rawParams))
		for _, rp := range rawParams {
			if s, ok := rp.(string); ok {
				params = append(params, s)
			}
		}
		source := ""
		if len(arr) > 3 {
			source, _ = arr[3].(string)
		}
		text := &boundFunc{target: nativeFunc{"String.prototype", "valueOf"}, this: source}
		return &Lambda{Params: params, Body: arr[2], Source: source, made: f, text: text}, nil
	case ";":
		var v any = Undefined
		for _, e := range arr[1:] {
			var err error
			v, err = m.eval(f, e)
			if err != nil {
				return nil, err
			}
		}
		return v, nil
	case "call":
		callee, err := m.eval(f, arr[1])
		if err != nil {
			return nil, err
		}
		args, err := m.evalArgs(f, arr[2])
		if err != nil {
			return nil, err
		}
		if g, ok := callee.(globalRef); ok {
			switch g {
			case "Number", "isNaN", "isFinite", "parseInt", "parseFloat":
				presentAll(args)
			}
		}
		if !callable(callee) {
			return Undefined, nil // calling what is no function gives undefined, as in the kernel
		}
		// a bare call hands no this, as in JavaScript and the kernel: f = l.push; f(3) fails, as push of
		// nothing does (16/10/2026: both ends handed it the scope, and the kernel's push wrote into it)
		return m.call(f, callee, Undefined, args)
	case "u!":
		v, err := m.eval(f, arr[1])
		if err != nil {
			return nil, err
		}
		return !truthy(v), nil
	case "u-":
		v, err := m.eval(f, arr[1])
		if err != nil {
			return nil, err
		}
		n, err := m.toNumber(f, v)
		if err != nil {
			return nil, err
		}
		return -n, nil
	case "u+":
		// +x is Number(x), as in JavaScript and the VM (16/10/2026, the owner's E2); a value that is not
		// there is 0, as the kernel hands Number undefined as null
		v, err := m.eval(f, arr[1])
		if err != nil {
			return nil, err
		}
		return m.toNumber(f, present(v))
	}

	// Logical operators short-circuit and return an operand, matching JavaScript and the client.
	l, err := m.eval(f, arr[1])
	if err != nil {
		return nil, err
	}
	switch op {
	case "&&":
		if !truthy(l) {
			return l, nil
		}
		return m.eval(f, arr[2])
	case "||":
		if truthy(l) {
			return l, nil
		}
		return m.eval(f, arr[2])
	case "??":
		if !nullish(l) {
			return l, nil
		}
		return m.eval(f, arr[2])
	}
	r, err := m.eval(f, arr[2])
	if err != nil {
		return nil, err
	}
	switch op {
	case "+", "-", "*", "/", "%":
		// A value that is not there (null, undefined) is "" beside a string and 0 in arithmetic, as in
		// the kernel's walk: a missing cart.name never prints "undefined", a missing cart.count + 1 is 1.
		if op == "+" {
			_, ls := l.(string)
			_, rs := r.(string)
			if ls || rs {
				if nullish(l) {
					l = ""
				}
				if nullish(r) {
					r = ""
				}
				return m.plus(f, l, r)
			}
		}
		if nullish(l) {
			l = float64(0)
		}
		if nullish(r) {
			r = float64(0)
		}
		if op == "+" {
			return m.plus(f, l, r)
		}
		a, err := m.toNumber(f, l)
		if err != nil {
			return nil, err
		}
		b, err := m.toNumber(f, r)
		if err != nil {
			return nil, err
		}
		switch op {
		case "-":
			return a - b, nil
		case "*":
			return a * b, nil
		case "/":
			return a / b, nil
		}
		return math.Mod(a, b), nil
	case ">", "<", ">=", "<=":
		return m.relational(f, op, l, r)
	case "==", "!=":
		eq, err := m.looseEquals(f, l, r)
		if err != nil {
			return nil, err
		}
		return eq == (op == "=="), nil
	case "===":
		return strictEquals(l, r), nil
	case "!==":
		return !strictEquals(l, r), nil
	}
	return nil, errors.New("hydrate: unknown op '" + op + "'")
}

// receiver evaluates the value a method is called on, and where it was read from when that is a place a
// changed list can be written back to: a name, $.name, $['name'], or a member or an item of what one of
// those holds, at any depth (cart.items, nested.a.b, x[0]). A list a method changes (push, sort …) is a
// new Go slice; the place gets it, so the next read of the same place sees the change, as every reader
// of the one array does in JavaScript. An object the expression made and the page scope take it in
// place; a host's map or a list is copied with the change and the copy goes where that one was read
// from, so data the host handed in is never written (16/10/2026: only a name, $.name and a member of an
// object the expression made took it, and cart.items.push(1) left cart.items as it was). Another name,
// a lambda's parameter, or an item that held the same list keeps the old one on the server: a Go slice
// does not grow in place (docs/KITWORK_UI.md lists where).
func (m *machine) receiver(f *frame, x any) (any, func(any), error) {
	node, _ := x.([]any)
	if len(node) == 2 && node[0] == "$" {
		name, _ := node[1].(string)
		if name != "$" {
			m.budget--
			if m.budget < 0 {
				return nil, nil, errBudget
			}
			return m.readName(f, name), func(v any) { f.assign(name, v) }, nil
		}
	}
	if len(node) == 3 && (node[0] == "." || node[0] == "idx") {
		// read as eval reads a member or an item, in the same order and on the same budget
		m.budget--
		if m.budget < 0 {
			return nil, nil, errBudget
		}
		holder, write, err := m.receiver(f, node[1])
		if err != nil {
			return nil, nil, err
		}
		key, _ := node[2].(string)
		if node[0] == "idx" {
			k, err := m.eval(f, node[2])
			if err != nil {
				return nil, nil, err
			}
			if nullish(holder) {
				return Undefined, nil, nil
			}
			if key, err = m.propertyKey(f, k); err != nil {
				return nil, nil, err
			}
		}
		if nullish(holder) || blockedKey(key) {
			return Undefined, nil, nil
		}
		v, err := m.get(f, holder, key)
		if err != nil {
			return nil, nil, err
		}
		return v, m.writeInto(holder, key, write), nil
	}
	v, err := m.eval(f, x)
	return v, nil, err
}

// writeInto is how a changed value reaches key of holder, which was read from the place write fills
// (nil when it was read from none): in place into an object the expression made and into the page
// scope, else into a copy of the host's map or of the list, which goes to that place.
func (m *machine) writeInto(holder any, key string, write func(any)) func(any) {
	switch t := holder.(type) {
	case *Object:
		return func(changed any) { t.set(key, changed) }
	case map[string]any:
		if sameRef(t, m.root) {
			return func(changed any) { m.root[key] = changed }
		}
		if write == nil {
			return nil
		}
		return func(changed any) {
			copied := make(map[string]any, len(t))
			for k, v := range t {
				copied[k] = v
			}
			copied[key] = changed
			write(copied)
		}
	case []any:
		i, ok := arrayIndex(key)
		if !ok || int64(i) >= int64(len(t)) || write == nil {
			return nil
		}
		return func(changed any) {
			copied := make([]any, len(t))
			copy(copied, t)
			copied[i] = changed
			write(copied)
		}
	}
	return nil
}

func (m *machine) evalArgs(f *frame, raw any) ([]any, error) {
	list, _ := raw.([]any)
	args := make([]any, len(list))
	for i, ra := range list {
		v, err := m.eval(f, ra)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	return args, nil
}

// present is v with undefined as null, as the kernel hands it to Number, isNaN, isFinite, parseInt,
// parseFloat, Math and the methods of Number (16/10/2026, the owner's E2).
func present(v any) any {
	if isUndefined(v) {
		return nil
	}
	return v
}

func presentAll(args []any) {
	for i, a := range args {
		args[i] = present(a)
	}
}

// readName is a bare name as the kernel's scope reads it: the nearest scope that has it, else an
// expression global (a name the page declares comes first), else 0 — a name nothing declares reads
// as 0 on both ends.
func (m *machine) readName(f *frame, name string) any {
	if name == "$" {
		for g := f; g != nil && g.up != nil; g = g.up {
			if v, ok := g.vars["$"]; ok {
				return norm(v) // a parameter named $
			}
		}
		return m.root
	}
	if v, ok := f.lookup(name); ok {
		return v
	}
	if g, ok := lookupGlobal(name); ok {
		return g
	}
	return float64(0)
}

// call is a call an expression makes, of a lambda or a native function, counted against the depth
// calls may nest to, as the kernel's call counts it.
func (m *machine) call(f *frame, fn, this any, args []any) (any, error) {
	if m.depth >= callDepthLimit {
		return nil, errDepth
	}
	m.depth++
	defer func() { m.depth-- }()
	return m.callValue(f, fn, this, args)
}

// callValue calls fn with this and args, from the scope f (nil when no expression calls it).
func (m *machine) callValue(f *frame, fn, this any, args []any) (any, error) {
	switch t := fn.(type) {
	case *Lambda:
		return m.invoke(f, t, args)
	case Func:
		plainArgs := make([]any, len(args))
		for i, a := range args {
			plainArgs[i] = Plain(a)
		}
		return norm(t(plainArgs)), nil
	case globalRef:
		return m.callGlobal(f, t, args)
	case nativeFunc:
		return m.callNative(f, t, this, args)
	case *boundFunc:
		all := append(append(make([]any, 0, len(t.args)+len(args)), t.args...), args...)
		return m.callValue(f, t.target, t.this, all)
	}
	text, _ := m.describe(f, fn)
	return nil, typeError(text + " is not a function")
}

// invoke runs a lambda: its parameters over the scope that calls it (f), else over the scope that made
// it. Writes to a parameter stay in the call; any other write lands where the name lives.
func (m *machine) invoke(f *frame, lam *Lambda, args []any) (any, error) {
	if f == nil {
		f = lam.made
		if f == nil {
			f = m.top
		}
	}
	if len(lam.Params) == 0 {
		return m.eval(f, lam.Body)
	}
	vars := make(map[string]any, len(lam.Params))
	for i, p := range lam.Params {
		if i < len(args) {
			vars[p] = args[i]
		} else {
			vars[p] = Undefined
		}
	}
	return m.eval(&frame{vars: vars, up: f}, lam.Body)
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

// toStr renders a value the way a page prints it, as JavaScript's String does: a value that is not
// there "", a list its items joined, an object "[object Object]" (Text).
func toStr(v any) string { return Text(v) }

// num is Number(v) of a value that needs no lambda to become a number: a host's helper reads its
// arguments with it.
func num(v any) float64 {
	m := newMachine(nil)
	n, err := m.toNumber(m.top, v)
	if err != nil {
		return math.NaN()
	}
	return n
}
