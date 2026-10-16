package hydrate

import (
	"math"
)

// What a member read finds on each kind of value, as JavaScript's [[Get]] does: a value's own keys
// first, then what its prototype lends it — String.prototype for a string, Array.prototype for a list,
// Object.prototype under all of them. A method read without a call is that function (nativeFunc),
// 'abc'.slice is truthy and prints "function slice() { [native code] }" on both ends (16/10/2026: the
// server read null for every method).

// memberKind is what a native member holds.
type memberKind uint8

const (
	memberFunction memberKind = iota + 1
	memberNumber
	memberString
	memberAccessor
)

// nativeMember is one member of a prototype or global, from jsnatives.go.
type nativeMember struct {
	kind   memberKind
	length int
	name   string
	bits   uint64
	text   string
}

// nativeFunc is a function JavaScript builds in, read as a value: 'abc'.slice is
// nativeFunc{"String.prototype", "slice"}. Two reads of one are one function ('a'.slice === 'b'.slice).
type nativeFunc struct {
	owner string
	name  string
}

func (n nativeFunc) member() nativeMember {
	if n.owner == "Object" && n.name == "keys" {
		// the kernel's Object is a frozen { keys } holding JavaScript's own Object.keys, which the
		// generated tables leave out (16/10/2026: its length read 0 and its name "")
		return objectKeysMember
	}
	return nativeMembers[n.owner][n.name]
}

var objectKeysMember = nativeMember{kind: memberFunction, length: 1, name: "keys"}

// text is the function's source as V8 writes a built-in one. Firefox and Safari write the same words
// over three lines.
func (n nativeFunc) text() string {
	return "function " + n.member().name + "() { [native code] }"
}

// boundFunc is what bind makes: target called with this and the leading args.
type boundFunc struct {
	target any
	this   any
	args   []any
}

// prototypeOf is the chain a member read walks after a value's own keys.
func prototypeOf(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{"String.prototype", "Object.prototype"}
	case float64, int:
		return []string{"Number.prototype", "Object.prototype"}
	case bool:
		return []string{"Boolean.prototype", "Object.prototype"}
	case []any:
		return []string{"Array.prototype", "Object.prototype"}
	case *Lambda, nativeFunc, *boundFunc, Func:
		return []string{"Function.prototype", "Object.prototype"}
	case globalRef:
		if t.callable() {
			return []string{string(t), "Function.prototype", "Object.prototype"}
		}
		return []string{string(t), "Object.prototype"}
	case *iterator:
		switch t.tag {
		case "Array Iterator":
			return []string{"ArrayIterator.prototype", "Iterator.prototype", "Object.prototype"}
		case "RegExp String Iterator":
			return []string{"RegExpStringIterator.prototype", "Iterator.prototype", "Object.prototype"}
		}
		return []string{"IteratorHelper.prototype", "Iterator.prototype", "Object.prototype"}
	case *Object:
		if t.bare {
			return nil
		}
	case *promise:
		return []string{"Promise.prototype", "Object.prototype"}
	case *rawJSON:
		return nil // JSON.rawJSON's object has no prototype
	}
	return []string{"Object.prototype"}
}

// fromPrototype is a member the chain lends: a function as a nativeFunc, a constant its value.
func fromPrototype(owner, key string) (any, bool) {
	member, ok := nativeMembers[owner][key]
	if !ok {
		return nil, false
	}
	switch member.kind {
	case memberFunction:
		if owner == "Number" && (key == "parseInt" || key == "parseFloat") {
			return globalRef(key), true // Number.parseInt is the global parseInt, one function
		}
		return nativeFunc{owner, member.name}, true
	case memberNumber:
		return math.Float64frombits(member.bits), true
	case memberString:
		return member.text, true
	}
	return nil, true // an accessor: the caller decides
}

// get is v[key] for any value: its own key, else its prototype chain; Undefined when none has it.
func (m *machine) get(f *frame, v any, key string) (any, error) {
	switch t := v.(type) {
	case nil, undefinedType:
		return Undefined, nil
	case string:
		if key == "length" {
			return float64(length16(t)), nil
		}
		if i, ok := arrayIndex(key); ok {
			u := units(t)
			if int(i) < len(u) {
				return unitString(u[i]), nil
			}
			return Undefined, nil
		}
	case []any:
		if key == "length" {
			return float64(len(t)), nil
		}
		if i, ok := arrayIndex(key); ok {
			if int(i) < len(t) {
				return item(t[i]), nil
			}
			return Undefined, nil
		}
		if extra := m.extra(t); extra != nil {
			if v, ok := extra.Get(key); ok {
				return v, nil
			}
		}
	case map[string]any, *Object:
		if own, ok := ownGet(t, key); ok {
			return own, nil
		}
	case *Lambda:
		// the kernel's lambda is a strict function with own length (its parameters), name "" and a toString
		// that gives its source (a bound String.prototype.valueOf, so that it too has no source of its own)
		switch key {
		case "length":
			return float64(len(t.Params)), nil
		case "name":
			return "", nil
		case "toString":
			if t.text != nil {
				return t.text, nil
			}
			return &boundFunc{target: nativeFunc{"String.prototype", "valueOf"}, this: t.Source}, nil
		}
	case nativeFunc:
		switch key {
		case "length":
			return float64(t.member().length), nil
		case "name":
			return t.member().name, nil
		}
	case *boundFunc:
		switch key {
		case "length":
			n, _ := m.functionLength(t.target)
			return math.Max(0, n-float64(len(t.args))), nil
		case "name":
			name, _ := m.functionName(t.target)
			return "bound " + name, nil
		}
	case Func:
		switch key {
		case "length":
			return float64(0), nil
		case "name":
			return "", nil
		}
	case globalRef:
		if own, ok := t.own(key); ok {
			return own, nil
		}
	case *rawJSON:
		if key == "rawJSON" {
			return t.text, nil
		}
	}
	for _, owner := range prototypeOf(v) {
		if member, ok := fromPrototype(owner, key); ok {
			if member == nil {
				// Function.prototype's caller and arguments: a strict function's throw, as V8's do
				return nil, typeError("'caller', 'callee', and 'arguments' properties may not be accessed on strict mode functions or the arguments objects for calls to them")
			}
			return member, nil
		}
	}
	return Undefined, nil
}

// hole is a place in a list no item fills: Array(3) has three. It reads as undefined; the methods that
// skip a hole skip it, as JavaScript's do.
type holeType struct{}

var hole any = holeType{}

func isHole(v any) bool { _, ok := v.(holeType); return ok }

// item is a list's item as a read gives it: a hole is undefined.
func item(v any) any {
	if isHole(v) {
		return Undefined
	}
	return norm(v)
}

// hasOwn is Object.prototype.hasOwnProperty of v for key.
func (m *machine) hasOwn(v any, key string) bool {
	switch t := v.(type) {
	case string:
		if key == "length" {
			return true
		}
		i, ok := arrayIndex(key)
		return ok && int(i) < length16(t)
	case []any:
		if key == "length" {
			return true
		}
		if extra := m.extra(t); extra != nil {
			if _, ok := extra.Get(key); ok {
				return true
			}
		}
		i, ok := arrayIndex(key)
		return ok && int(i) < len(t) && !isHole(t[i])
	case map[string]any, *Object:
		_, ok := ownGet(t, key)
		return ok
	case *Lambda:
		// no prototype, as an arrow function has none and the kernel's bound lambda neither (16/10/2026)
		return key == "length" || key == "name" || key == "toString"
	case nativeFunc, *boundFunc, Func:
		return key == "length" || key == "name"
	case globalRef:
		_, ok := t.own(key)
		if !ok && t.callable() {
			ok = key == "length" || key == "name" || key == "prototype" && t != "parseInt" && t != "parseFloat" &&
				t != "isNaN" && t != "isFinite" && t != "encodeURIComponent" && t != "decodeURIComponent"
		}
		return ok
	case *rawJSON:
		return key == "rawJSON"
	}
	return false
}

// enumerable is Object.prototype.propertyIsEnumerable of v for key: an own key a for-in would list.
func (m *machine) enumerable(v any, key string) bool {
	switch t := v.(type) {
	case string:
		i, ok := arrayIndex(key)
		return ok && int(i) < length16(t)
	case []any:
		if extra := m.extra(t); extra != nil {
			if _, ok := extra.Get(key); ok {
				return true
			}
		}
		i, ok := arrayIndex(key)
		return ok && int(i) < len(t) && !isHole(t[i])
	case map[string]any, *Object:
		_, ok := ownGet(t, key)
		return ok
	}
	return false
}

// functionLength and functionName are a function's length and name.
func (m *machine) functionLength(fn any) (float64, bool) {
	v, err := m.get(nil, fn, "length")
	if err != nil {
		return 0, false
	}
	n, ok := v.(float64)
	return n, ok
}

func (m *machine) functionName(fn any) (string, bool) {
	v, err := m.get(nil, fn, "name")
	if err != nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// describe is v for an error message, as V8 names what is not a function.
func (m *machine) describe(f *frame, v any) (string, error) {
	switch t := v.(type) {
	case string:
		return "\"" + t + "\"", nil
	case []any:
		return "object", nil
	}
	if primitive(v) {
		return primitiveText(v), nil
	}
	return "object", nil
}

// classOf is the tag Object.prototype.toString gives v: "[object " + tag + "]".
func classOf(v any) string {
	switch t := v.(type) {
	case nil:
		return "Null"
	case undefinedType:
		return "Undefined"
	case string:
		return "String"
	case float64, int:
		return "Number"
	case bool:
		return "Boolean"
	case []any:
		return "Array"
	case *Lambda, nativeFunc, *boundFunc, Func:
		return "Function"
	case globalRef:
		switch t {
		case "Math":
			return "Math"
		case "JSON":
			return "JSON"
		case "Object":
			return "Object"
		}
		return "Function"
	case *iterator:
		return t.tag
	case *promise:
		return "Promise"
	}
	return "Object"
}
