package hydrate

import (
	"math"
)

// iterator is what keys(), values() and entries() of a list make, and what the iterator helpers
// (map, filter, take …) make of one: next() gives { value, done } one at a time, as V8's does, and a
// helper runs its function only as far as it is read.
type iterator struct {
	tag  string // "Array Iterator" or "Iterator Helper"
	next func(m *machine, f *frame) (any, bool, error)
	done bool
}

// step is the iterator's next value, or done.
func (it *iterator) step(m *machine, f *frame) (any, bool, error) {
	if it.done {
		return Undefined, true, nil
	}
	v, done, err := it.next(m, f)
	if err != nil {
		it.done = true
		return nil, true, err
	}
	if done {
		it.done = true
		return Undefined, true, nil
	}
	return v, false, nil
}

// arrayIterator is list.keys(), values() or entries().
func arrayIterator(kind string, list []any) *iterator {
	i := 0
	return &iterator{tag: "Array Iterator", next: func(*machine, *frame) (any, bool, error) {
		if i >= len(list) {
			return nil, true, nil
		}
		at := i
		i++
		switch kind {
		case "keys":
			return float64(at), false, nil
		case "entries":
			pair := newList(2)
			pair[0], pair[1] = float64(at), item(list[at])
			return pair, false, nil
		}
		return item(list[at]), false, nil
	}}
}

// result is the { value, done } next() gives.
func result(v any, done bool) *Object {
	out := newObject(2)
	out.set("value", v)
	out.set("done", done)
	return out
}

func init() {
	// the members V8 gives an iterator helper and a promise, which the generated table does not hold
	nativeMembers["IteratorHelper.prototype"] = map[string]nativeMember{
		"next":   {kind: memberFunction, length: 0, name: "next"},
		"return": {kind: memberFunction, length: 0, name: "return"},
	}
	nativeMembers["RegExpStringIterator.prototype"] = map[string]nativeMember{
		"next": {kind: memberFunction, length: 0, name: "next"},
	}
	nativeMembers["Promise.prototype"] = map[string]nativeMember{
		"then":    {kind: memberFunction, length: 2, name: "then"},
		"catch":   {kind: memberFunction, length: 1, name: "catch"},
		"finally": {kind: memberFunction, length: 1, name: "finally"},
	}
}

// iteratorMethod is next and return of an iterator, and the helpers of Iterator.prototype.
func (m *machine) iteratorMethod(f *frame, name string, this any, args []any) (any, error) {
	it, ok := this.(*iterator)
	if !ok {
		return nil, typeError("Iterator.prototype." + name + " called on what is no iterator")
	}
	switch name {
	case "next":
		v, done, err := it.step(m, f)
		if err != nil {
			return nil, err
		}
		return result(v, done), nil
	case "return":
		it.done = true
		return result(Undefined, true), nil
	case "toArray":
		out := newList(0)
		for {
			v, done, err := it.step(m, f)
			if err != nil {
				return nil, err
			}
			if done {
				return out, nil
			}
			out = append(out, v)
			if len(out) > serverListLimit {
				return nil, errTooMany
			}
		}
	case "take", "drop":
		limit, err := m.toNumber(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		if limit != limit {
			return nil, rangeError(primitiveText(arg(args, 0)) + " must be positive")
		}
		n := math.Trunc(limit)
		if n < 0 {
			return nil, rangeError(primitiveText(arg(args, 0)) + " must be positive")
		}
		count := 0.0
		return &iterator{tag: "Iterator Helper", next: func(m *machine, f *frame) (any, bool, error) {
			if name == "take" {
				if count >= n {
					it.done = true
					return nil, true, nil
				}
				count++
				v, done, err := it.step(m, f)
				return v, done, err
			}
			for count < n {
				count++
				if _, done, err := it.step(m, f); done || err != nil {
					return nil, true, err
				}
			}
			v, done, err := it.step(m, f)
			return v, done, err
		}}, nil
	}
	fn, err := m.callback(f, arg(args, 0))
	if err != nil {
		return nil, err
	}
	counter := 0.0
	switch name {
	case "map", "filter", "flatMap":
		var inner *iterator
		return &iterator{tag: "Iterator Helper", next: func(m *machine, f *frame) (any, bool, error) {
			for {
				if inner != nil {
					v, done, err := inner.step(m, f)
					if err != nil {
						return nil, true, err
					}
					if !done {
						return v, false, nil
					}
					inner = nil
				}
				v, done, err := it.step(m, f)
				if err != nil || done {
					return nil, true, err
				}
				out, err := m.callValue(f, fn, Undefined, []any{v, counter})
				counter++
				if err != nil {
					return nil, true, err
				}
				switch name {
				case "map":
					return out, false, nil
				case "filter":
					if truthy(out) {
						return v, false, nil
					}
				case "flatMap":
					switch t := out.(type) {
					case []any:
						inner = arrayIterator("values", t)
					case *iterator:
						inner = t
					default:
						return nil, true, typeError(primitiveText(out) + " is not an object")
					}
				}
			}
		}}, nil
	case "reduce":
		var acc any
		hasAcc := len(args) >= 2
		if hasAcc {
			acc = args[1]
		}
		for {
			v, done, err := it.step(m, f)
			if err != nil {
				return nil, err
			}
			if done {
				if !hasAcc {
					return nil, typeError("Reduce of empty iterator with no initial value")
				}
				return acc, nil
			}
			if !hasAcc {
				acc, hasAcc = v, true
				counter++
				continue
			}
			acc, err = m.callValue(f, fn, Undefined, []any{acc, v, counter})
			counter++
			if err != nil {
				return nil, err
			}
		}
	case "forEach", "some", "every", "find":
		for {
			v, done, err := it.step(m, f)
			if err != nil {
				return nil, err
			}
			if done {
				switch name {
				case "some":
					return false, nil
				case "every":
					return true, nil
				}
				return Undefined, nil
			}
			out, err := m.callValue(f, fn, Undefined, []any{v, counter})
			counter++
			if err != nil {
				return nil, err
			}
			switch name {
			case "some":
				if truthy(out) {
					it.done = true
					return true, nil
				}
			case "every":
				if !truthy(out) {
					it.done = true
					return false, nil
				}
			case "find":
				if truthy(out) {
					it.done = true
					return v, nil
				}
			}
		}
	}
	return nil, typeError("Iterator.prototype." + name + " is not a function")
}
