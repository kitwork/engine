package hydrate

import (
	"math"
)

// callNative runs a function JavaScript builds in with this and args, as V8 does — the server's copy of
// every native the kernel reaches (16/10/2026). What depends on the visitor's machine (the time, the
// language, chance) gives Undefined: the kernel answers it in the browser.
func (m *machine) callNative(f *frame, fn nativeFunc, this any, args []any) (any, error) {
	switch fn.owner {
	case "String.prototype":
		return m.stringMethod(f, fn.name, this, args)
	case "Array.prototype":
		return m.arrayMethod(f, fn.name, this, args)
	case "Number.prototype":
		return m.numberMethod(f, fn.name, this, args)
	case "Boolean.prototype":
		b, ok := this.(bool)
		if !ok {
			return nil, typeError("Boolean.prototype." + fn.name + " requires that 'this' be a Boolean")
		}
		if fn.name == "toString" {
			return primitiveText(b), nil
		}
		return b, nil
	case "Object.prototype":
		return m.objectMethod(f, fn.name, this, args)
	case "Function.prototype":
		return m.functionMethod(f, fn.name, this, args)
	case "ArrayIterator.prototype", "Iterator.prototype", "IteratorHelper.prototype", "RegExpStringIterator.prototype":
		return m.iteratorMethod(f, fn.name, this, args)
	case "Promise.prototype":
		if _, ok := this.(*promise); !ok {
			return nil, typeError("Method Promise.prototype." + fn.name + " called on incompatible receiver")
		}
		return &promise{}, nil
	case "Math":
		return m.mathMethod(f, fn.name, args)
	case "JSON":
		return m.jsonMethod(f, fn.name, args)
	case "String":
		return m.stringStatic(f, fn.name, args)
	case "Number":
		return numberStatic(fn.name, arg(args, 0)), nil
	case "Array":
		return m.arrayStatic(f, fn.name, args)
	case "Date":
		return m.dateStatic(f, fn.name, args)
	case "Object":
		if fn.name == "keys" {
			return m.objectKeys(f, arg(args, 0))
		}
	}
	return nil, typeError(fn.name + " is not a function")
}

// arg is the i-th argument, undefined when the call gave none.
func arg(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return Undefined
}

// numberStatic is Number.isNaN, isFinite, isInteger and isSafeInteger: no number is made of the
// argument, so only a number can be true.
func numberStatic(name string, v any) any {
	n, ok := v.(float64)
	if !ok {
		return false
	}
	finite := !math.IsNaN(n) && !math.IsInf(n, 0)
	switch name {
	case "isNaN":
		return n != n
	case "isFinite":
		return finite
	case "isInteger":
		return finite && math.Trunc(n) == n
	case "isSafeInteger":
		return finite && math.Trunc(n) == n && math.Abs(n) <= 9007199254740991
	}
	return false
}

// objectKeys is Object.keys(v): the own enumerable keys, an error for null and undefined.
func (m *machine) objectKeys(f *frame, v any) (any, error) {
	if nullish(v) {
		return nil, typeError("Cannot convert undefined or null to object")
	}
	var keys []string
	switch t := v.(type) {
	case string:
		n := length16(t)
		keys = make([]string, n)
		for i := range keys {
			keys[i] = primitiveText(float64(i))
		}
	case []any:
		for i, it := range t {
			if !isHole(it) {
				keys = append(keys, primitiveText(float64(i)))
			}
		}
		if extra := m.extra(t); extra != nil {
			keys = append(keys, extra.Keys()...)
		}
	case map[string]any, *Object:
		keys = ownKeys(t)
	case globalRef:
		if t == "Object" {
			keys = []string{"keys"} // the kernel's Object is a frozen { keys }
		}
	}
	out := newList(len(keys))
	for i, k := range keys {
		out[i] = k
	}
	return out, nil
}

// objectMethod is a method of Object.prototype.
func (m *machine) objectMethod(f *frame, name string, this any, args []any) (any, error) {
	switch name {
	case "toString":
		return "[object " + classOf(this) + "]", nil
	case "toLocaleString":
		method, err := m.get(f, this, "toString")
		if err != nil {
			return nil, err
		}
		if !callable(method) {
			return nil, typeError("toString is not a function")
		}
		return m.callValue(f, method, this, nil)
	case "valueOf":
		if nullish(this) {
			return nil, typeError("Cannot convert undefined or null to object")
		}
		return this, nil
	case "hasOwnProperty", "propertyIsEnumerable":
		key, err := m.propertyKey(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		if nullish(this) {
			return nil, typeError("Cannot convert undefined or null to object")
		}
		if name == "hasOwnProperty" {
			return m.hasOwn(this, key), nil
		}
		return m.enumerable(this, key), nil
	case "isPrototypeOf":
		// nothing an expression reaches is the prototype of another
		v := arg(args, 0)
		if !isObject(v) {
			return false, nil
		}
		if nullish(this) {
			return nil, typeError("Cannot convert undefined or null to object")
		}
		return false, nil
	}
	return nil, typeError(name + " is not a function")
}

// propertyKey is ToPropertyKey(v): the text a key is.
func (m *machine) propertyKey(f *frame, v any) (string, error) {
	p, err := m.toPrimitive(f, v, "string")
	if err != nil {
		return "", err
	}
	return primitiveText(p), nil
}

// functionMethod is call, apply, bind or toString of a function.
func (m *machine) functionMethod(f *frame, name string, this any, args []any) (any, error) {
	if name == "toString" {
		switch t := this.(type) {
		case *Lambda:
			// the kernel's lambda is a bound function, whose own text is a bound function's (16/10/2026: the
			// kernel printed its wrapper's source here); String(f) and f.toString() give the source
			return "function () { [native code] }", nil
		case nativeFunc:
			return t.text(), nil
		case globalRef:
			if t.callable() {
				return t.text(), nil
			}
		case *boundFunc, Func:
			return "function () { [native code] }", nil
		}
		return nil, typeError("Function.prototype.toString requires that 'this' be a Function")
	}
	if !callable(this) {
		return nil, typeError("Function.prototype." + name + " called on a value that is not a function")
	}
	switch name {
	case "call":
		rest := []any{}
		if len(args) > 1 {
			rest = args[1:]
		}
		return m.callValue(f, this, arg(args, 0), rest)
	case "apply":
		list := arg(args, 1)
		var spread []any
		if !nullish(list) {
			items, err := m.listFromArrayLike(f, list)
			if err != nil {
				return nil, err
			}
			spread = items
		}
		return m.callValue(f, this, arg(args, 0), spread)
	case "bind":
		bound := &boundFunc{target: this, this: arg(args, 0)}
		if len(args) > 1 {
			bound.args = append([]any{}, args[1:]...)
		}
		return bound, nil
	}
	return nil, typeError(name + " is not a function")
}

// listFromArrayLike is CreateListFromArrayLike: the items of a list, or of an object with a length.
func (m *machine) listFromArrayLike(f *frame, v any) ([]any, error) {
	if !isObject(v) {
		return nil, typeError("CreateListFromArrayLike called on non-object")
	}
	if list, ok := v.([]any); ok {
		out := make([]any, len(list))
		for i, it := range list {
			out[i] = item(it)
		}
		return out, nil
	}
	view, err := m.arrayLike(f, v)
	if err != nil {
		return nil, err
	}
	for i, it := range view {
		view[i] = item(it)
	}
	return view, nil
}

// promise is what Array.fromAsync makes: a promise the browser settles after the expression. Its
// callbacks never run while an expression runs, on either end.
type promise struct{}

// rawJSON is what JSON.rawJSON makes: text JSON.stringify writes as it is.
type rawJSON struct {
	text string
}
