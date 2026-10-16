package hydrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strconv"

	"github.com/kitwork/engine/value"
)

// The values of an expression, as JavaScript has them (16/10/2026, the owner's decision: a .ui
// expression gives one answer on the server's first paint and in the browser's kernel, and that answer
// is JavaScript's, node being the judge). The kinds:
//
//	nil                    null
//	Undefined              undefined: a member nobody set, an item past the end, a missing argument
//	bool, float64, string  as in JavaScript; a string's lone half of a UTF-16 pair as jstext.go keeps it
//	[]any                  an array; one array read twice is one array ([x].includes(x))
//	map[string]any         an object the host gave (page data): its keys in JavaScript's order of
//	                       the sorted keys, the order literal() hands them to the browser in
//	*Object                an object an expression made ({ b: 1, a: 2 }, JSON.parse): its own order
//	*Lambda                a lambda, a function as the kernel's is: its text is its source
//	Func                   a helper the host put in the scope (money, date…)
//	globalRef              Math, JSON, String, parseInt … — the expression globals
//	nativeFunc             a function JavaScript builds in, read as a value: 'a'.slice, Math.max
//	*boundFunc             what bind made
//	*iterator              what keys(), values(), entries() and the iterator helpers make
//
// Before 16/10/2026 the server had one nil for null and undefined, compared lists and objects by
// value nowhere (l === l was false), printed a list or an object as "", and knew a dozen methods.

// undefinedType is the type of Undefined.
type undefinedType struct{}

// Undefined is JavaScript's undefined. It is no Go nil, which stays null: null === o.missing is false,
// as in JavaScript (it was true here, where the server had one value for both). Written as JSON it is
// null, as JSON.stringify writes undefined in an array.
var Undefined any = undefinedType{}

func (undefinedType) MarshalJSON() ([]byte, error) { return []byte("null"), nil }
func (undefinedType) String() string               { return "undefined" }

func isUndefined(v any) bool { _, ok := v.(undefinedType); return ok }

// nullish is JavaScript's == null: null or undefined.
func nullish(v any) bool { return v == nil || isUndefined(v) }

// Plain is v as plain Go data for a host (a check, a test, JSON): undefined is null, an object an
// expression made is a map, at any depth; anything else as it is.
func Plain(v any) any {
	return plain(v, 0)
}

func plain(v any, depth int) any {
	if depth > value.MaxDepth {
		return nil
	}
	switch t := v.(type) {
	case undefinedType:
		return nil
	case *Object:
		out := make(map[string]any, len(t.keys))
		for _, k := range t.keys {
			out[k] = plain(t.values[k], depth+1)
		}
		return out
	case []any:
		var out []any
		for i, item := range t {
			p := plain(item, depth+1)
			if out == nil && !sameShallow(p, item) {
				out = make([]any, len(t))
				copy(out, t[:i])
			}
			if out != nil {
				out[i] = p
			}
		}
		if out != nil {
			return out
		}
		return t
	case map[string]any:
		var out map[string]any
		for k, item := range t {
			p := plain(item, depth+1)
			if out == nil && !sameShallow(p, item) {
				out = make(map[string]any, len(t))
				for k2, v2 := range t {
					out[k2] = v2
				}
			}
			if out != nil {
				out[k] = p
			}
		}
		if out != nil {
			return out
		}
		return t
	}
	return v
}

// sameShallow reports that plain left v as it was.
func sameShallow(a, b any) bool {
	switch a.(type) {
	case []any, map[string]any:
		return sameRef(a, b)
	case nil:
		return b == nil
	}
	switch b.(type) {
	case undefinedType, *Object:
		return false
	}
	return true
}

// Object is an object an expression made: { b: 1, a: 2 }, JSON.parse('{"b":1}'). It keeps its keys in
// the order JavaScript gives them — the integer keys ascending, then the others as they came — so
// Object.keys and JSON.stringify answer as in the browser ({"b":1,"a":2}, where a Go map sorted them).
type Object struct {
	keys   []string
	values map[string]any
	// bare is an object with no prototype (a match's groups): no toString, no valueOf
	bare bool
}

func newObject(capacity int) *Object {
	return &Object{keys: make([]string, 0, capacity), values: make(map[string]any, capacity)}
}

// set gives k a value; a key already there keeps its place, as in JavaScript.
func (o *Object) set(k string, v any) {
	if _, ok := o.values[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.values[k] = v
}

// Keys are the object's keys in JavaScript's order.
func (o *Object) Keys() []string { return jsKeyOrder(o.keys) }

// MarshalJSON writes the object with its keys in its own order, as JSON.stringify would (a value with
// no JSON form, such as undefined, as encoding/json writes it).
func (o *Object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.Keys() {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		v, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Get is the value of the object's own key k.
func (o *Object) Get(k string) (any, bool) {
	v, ok := o.values[k]
	return v, ok
}

// jsKeyOrder is keys in the order JavaScript enumerates an object's own keys: each array index (a
// canonical integer below 2^32 - 1) ascending, then the other keys in the order given.
func jsKeyOrder(keys []string) []string {
	var indexes, names []string
	for _, k := range keys {
		if _, ok := arrayIndex(k); ok {
			indexes = append(indexes, k)
		} else {
			names = append(names, k)
		}
	}
	if len(indexes) == 0 {
		return keys
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		a, _ := arrayIndex(indexes[i])
		b, _ := arrayIndex(indexes[j])
		return a < b
	})
	return append(indexes, names...)
}

// arrayIndex reads k as an array index: digits with no leading zero (or "0"), below 2^32 - 1.
func arrayIndex(k string) (uint32, bool) {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(k, 10, 64)
	if err != nil || n >= 1<<32-1 {
		return 0, false
	}
	return uint32(n), true
}

// ownKeys are the own enumerable keys of an object, in JavaScript's order: a host map's sorted, as
// literal() hands it to the browser.
func ownKeys(o any) []string {
	switch t := o.(type) {
	case *Object:
		return t.Keys()
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return jsKeyOrder(keys)
	}
	return nil
}

// ObjectKeys are the keys of an object an expression gives, in the order the browser has them: an
// object it made in its own order, a host map its keys sorted. nil for anything else.
func ObjectKeys(v any) []string { return ownKeys(v) }

// ownGet is the own key k of an object.
func ownGet(o any, k string) (any, bool) {
	switch t := o.(type) {
	case *Object:
		v, ok := t.values[k]
		return norm(v), ok
	case map[string]any:
		v, ok := t[k]
		return norm(v), ok
	}
	return nil, false
}

// norm is a value a host container holds, as an expression reads it: Go's other numbers as float64,
// and what JavaScript has no form for (a time, bytes, a struct) null, as literal() hands it on.
func norm(v any) any {
	switch t := v.(type) {
	case nil, bool, float64, string, []any, map[string]any, *Object, *Lambda, Func, globalRef, nativeFunc,
		*boundFunc, *iterator, undefinedType, *promise, *rawJSON:
		return v
	case int:
		return float64(t)
	case int8:
		return float64(t)
	case int16:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case uint:
		return float64(t)
	case uint8:
		return float64(t)
	case uint16:
		return float64(t)
	case uint32:
		return float64(t)
	case uint64:
		return float64(t)
	case float32:
		return float64(t)
	case func(args []any) any:
		return Func(t)
	}
	return nil
}

// newList is an array of n items, made so that it has an identity of its own even empty: a Go slice
// with no room shares its address with every other one.
func newList(n int) []any { return make([]any, n, max(n, 1)) }

// sameRef is JavaScript's identity of two values of a kind that has one (an array, an object, a
// function): the same array is the same backing items of the same length.
func sameRef(a, b any) bool {
	switch x := a.(type) {
	case []any:
		// a list with no room has no address of its own (every one decoded from JSON's [] shares one), so
		// two such lists are never one: an empty list the host gave is not equal even to itself
		y, ok := b.([]any)
		if !ok || len(x) != len(y) || cap(x) == 0 || cap(y) == 0 {
			return false
		}
		return reflect.ValueOf(x).Pointer() == reflect.ValueOf(y).Pointer()
	case map[string]any:
		y, ok := b.(map[string]any)
		return ok && reflect.ValueOf(x).Pointer() == reflect.ValueOf(y).Pointer()
	case Func:
		y, ok := b.(Func)
		return ok && reflect.ValueOf(x).Pointer() == reflect.ValueOf(y).Pointer()
	case *Object, *Lambda, *boundFunc, *iterator, globalRef, nativeFunc, *promise, *rawJSON:
		return a == b
	}
	return false
}

// isObject reports a value JavaScript calls an object: an array, an object, a function, a global.
func isObject(v any) bool {
	switch v.(type) {
	case []any, map[string]any, *Object, *Lambda, Func, globalRef, nativeFunc, *boundFunc, *iterator,
		*promise, *rawJSON:
		return true
	}
	return false
}

// callable reports a function: what typeof calls "function".
func callable(v any) bool {
	switch t := v.(type) {
	case *Lambda, Func, nativeFunc, *boundFunc:
		return true
	case globalRef:
		return t.callable()
	}
	return false
}

// truthy follows JS: false, 0, NaN, "", null and undefined are falsy; everything else truthy.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil, undefinedType:
		return false
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case string:
		return t != ""
	case int:
		return t != 0
	}
	return true
}

// errType is a TypeError, RangeError, SyntaxError or URIError JavaScript would throw: the expression
// fails, on the server as in the browser, where the binding keeps what the server painted.
type jsError struct {
	name, message string
}

func (e *jsError) Error() string { return "hydrate: " + e.name + ": " + e.message }

func typeError(message string) error   { return &jsError{"TypeError", message} }
func rangeError(message string) error  { return &jsError{"RangeError", message} }
func syntaxError(message string) error { return &jsError{"SyntaxError", message} }
func uriError(message string) error    { return &jsError{"URIError", message} }

var errDepth = errors.New("hydrate: call depth exceeded")

// ---- conversions, as ECMAScript's abstract operations ----

// primitive reports a value that is no object.
func primitive(v any) bool {
	switch v.(type) {
	case nil, undefinedType, bool, float64, string, int:
		return true
	}
	return false
}

// toPrimitive is ToPrimitive(v, hint): an object's valueOf or toString, as JavaScript calls them —
// a lambda an object holds as either runs ({ valueOf: () => 42 } + 1 is 43). hint is "string",
// "number" or "default".
func (m *machine) toPrimitive(f *frame, v any, hint string) (any, error) {
	if primitive(v) {
		return v, nil
	}
	// the kinds with no own valueOf or toString answer at once, as their prototype's would
	switch t := v.(type) {
	case []any:
		return m.joinList(f, t, ",")
	case *Lambda:
		return t.Source, nil
	case nativeFunc:
		return t.text(), nil
	case globalRef:
		return t.text(), nil
	case *boundFunc:
		return "function () { [native code] }", nil
	case Func:
		return "function () { [native code] }", nil
	case *iterator:
		return "[object " + t.tag + "]", nil
	case *promise:
		return "[object Promise]", nil
	case *rawJSON:
		// a raw JSON object has no valueOf or toString to call
		return nil, typeError("Cannot convert object to primitive value")
	case *Object:
		if t.bare {
			if _, ok := t.values["valueOf"]; !ok {
				if _, ok := t.values["toString"]; !ok {
					return nil, typeError("Cannot convert object to primitive value")
				}
			}
		}
	}
	order := [2]string{"valueOf", "toString"}
	if hint == "string" {
		order = [2]string{"toString", "valueOf"}
	}
	for _, name := range order {
		method, err := m.get(f, v, name)
		if err != nil {
			return nil, err
		}
		if !callable(method) {
			continue
		}
		out, err := m.callValue(f, method, v, nil)
		if err != nil {
			return nil, err
		}
		if primitive(out) {
			return out, nil
		}
	}
	return nil, typeError("Cannot convert object to primitive value")
}

// toNumber is ToNumber(v).
func (m *machine) toNumber(f *frame, v any) (float64, error) {
	p, err := m.toPrimitive(f, v, "number")
	if err != nil {
		return 0, err
	}
	return primitiveNumber(p), nil
}

// primitiveNumber is ToNumber of a primitive: undefined NaN, null 0, a string as Number reads it.
func primitiveNumber(p any) float64 {
	switch t := p.(type) {
	case nil:
		return 0
	case undefinedType:
		return math.NaN()
	case bool:
		if t {
			return 1
		}
		return 0
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		return value.StringToNumber(t)
	}
	return math.NaN()
}

// toString is ToString(v).
func (m *machine) toString(f *frame, v any) (string, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	p, err := m.toPrimitive(f, v, "string")
	if err != nil {
		return "", err
	}
	return primitiveText(p), nil
}

// primitiveText is ToString of a primitive.
func primitiveText(p any) string {
	switch t := p.(type) {
	case nil:
		return "null"
	case undefinedType:
		return "undefined"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return value.NumberText(t)
	case int:
		return strconv.Itoa(t)
	case string:
		return t
	}
	return ""
}

// toIntegerOrInfinity is ToIntegerOrInfinity(v).
func (m *machine) toIntegerOrInfinity(f *frame, v any) (float64, error) {
	n, err := m.toNumber(f, v)
	if err != nil {
		return 0, err
	}
	return value.ToIntegerOrInfinity(n), nil
}

// relativeIndex is a start or end of slice and its kind: from the end when negative, clamped into 0 to
// length. undefined is fallback.
func (m *machine) relativeIndex(f *frame, v any, length int, fallback int) (int, error) {
	if isUndefined(v) {
		return fallback, nil
	}
	n, err := m.toIntegerOrInfinity(f, v)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		n += float64(length)
		if n < 0 {
			n = 0
		}
	}
	if n > float64(length) {
		n = float64(length)
	}
	return int(n), nil
}

// strictEquals is JavaScript's ===.
func strictEquals(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case undefinedType:
		return isUndefined(b)
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	}
	return sameRef(a, b)
}

// sameValueZero is === except that NaN is NaN, as includes compares.
func sameValueZero(a, b any) bool {
	if x, ok := a.(float64); ok && x != x {
		y, ok := b.(float64)
		return ok && y != y
	}
	return strictEquals(a, b)
}

// looseEquals is JavaScript's ==.
func (m *machine) looseEquals(f *frame, a, b any) (bool, error) {
	for {
		if kindOf(a) == kindOf(b) {
			return strictEquals(a, b), nil
		}
		if nullish(a) && nullish(b) {
			return true, nil
		}
		switch x := a.(type) {
		case float64:
			if y, ok := b.(string); ok {
				return x == value.StringToNumber(y), nil
			}
		case string:
			if y, ok := b.(float64); ok {
				return value.StringToNumber(x) == y, nil
			}
		case bool:
			a = primitiveNumber(x)
			continue
		}
		if y, ok := b.(bool); ok {
			b = primitiveNumber(y)
			continue
		}
		if isObject(a) && (kindOf(b) == "string" || kindOf(b) == "number") {
			p, err := m.toPrimitive(f, a, "default")
			if err != nil {
				return false, err
			}
			a = p
			continue
		}
		if isObject(b) && (kindOf(a) == "string" || kindOf(a) == "number") {
			p, err := m.toPrimitive(f, b, "default")
			if err != nil {
				return false, err
			}
			b = p
			continue
		}
		return false, nil
	}
}

// kindOf is the ECMAScript type of v: "undefined", "null", "boolean", "number", "string" or "object".
func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case undefinedType:
		return "undefined"
	case bool:
		return "boolean"
	case float64, int:
		return "number"
	case string:
		return "string"
	}
	return "object"
}

// lessThan is IsLessThan(a, b) of two primitives: true, false, or undefined (a NaN) as nil.
func lessThan(a, b any) (bool, bool) {
	as, aText := a.(string)
	bs, bText := b.(string)
	if aText && bText {
		return compare16(as, bs) < 0, true
	}
	x, y := primitiveNumber(a), primitiveNumber(b)
	if x != x || y != y {
		return false, false
	}
	return x < y, true
}

// relational is JavaScript's < > <= >= of two values: each made a primitive with the hint number, the
// left first; two texts compare by code units, anything else as numbers.
func (m *machine) relational(f *frame, op string, l, r any) (bool, error) {
	pl, err := m.toPrimitive(f, l, "number")
	if err != nil {
		return false, err
	}
	pr, err := m.toPrimitive(f, r, "number")
	if err != nil {
		return false, err
	}
	switch op {
	case "<":
		lt, ok := lessThan(pl, pr)
		return ok && lt, nil
	case ">":
		lt, ok := lessThan(pr, pl)
		return ok && lt, nil
	case "<=":
		lt, ok := lessThan(pr, pl)
		return ok && !lt, nil
	}
	lt, ok := lessThan(pl, pr)
	return ok && !lt, nil
}

// plus is JavaScript's + of two values the kernel's walk has already read (a value that is not there
// as "" beside a string, as 0 otherwise): both made primitives, a text joins, numbers add.
func (m *machine) plus(f *frame, l, r any) (any, error) {
	pl, err := m.toPrimitive(f, l, "default")
	if err != nil {
		return nil, err
	}
	pr, err := m.toPrimitive(f, r, "default")
	if err != nil {
		return nil, err
	}
	_, ls := pl.(string)
	_, rs := pr.(string)
	if ls || rs {
		return concat16(primitiveText(pl), primitiveText(pr)), nil
	}
	return primitiveNumber(pl) + primitiveNumber(pr), nil
}

// Text is what the browser's kernel writes of a value into a page's text or attribute: null and
// undefined nothing, anything else its JavaScript text (String(v)) — a list its items joined by
// commas, an object "[object Object]", a lambda its source. A value whose text runs a lambda that
// fails, or makes none, is nothing, as the kernel leaves what the server wrote.
func Text(v any) string {
	if nullish(v) {
		return ""
	}
	if s, ok := v.(string); ok {
		return pageText(s)
	}
	// no frame calls it: a lambda that gives its text runs in the scope that made it, as the kernel runs one
	// outside any expression (16/10/2026: it ran in an empty scope, so an attribute whose value was
	// { toString: () => open ? 'yes' : 'no' } read open as 0 here and printed "no")
	m := newMachine(nil)
	s, err := m.toString(nil, v)
	if err != nil {
		return ""
	}
	return pageText(s)
}
