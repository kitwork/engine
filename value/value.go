package value

import (
	"fmt"
	"reflect"
)

var (
	NULL  = NewNull()
	TRUE  = NewBool(true)
	FALSE = NewBool(false)
)

type Value struct {
	N float64
	V any
	K Kind

	IsError bool
	// ErrorVal contains a Failure for application errors or a runtime-owned
	// diagnostic for terminal VM failures. New host code should use
	// InvalidFailure (or PartialFailure) instead of assigning this field directly.
	ErrorVal any
	// Raw marks a string as ALREADY-safe trusted HTML: templates emit it verbatim without escaping,
	// so an engine-produced value (e.g. serialized+escaped JSON-LD) needs no raw() in the template.
	// Only the engine sets this — the JS subset has no way to mark data trusted, so it adds no XSS
	// surface. It does not propagate: any operation on the value produces a fresh, non-Raw result.
	Raw bool
}

func (v Value) Prototype(name string, fn Method)  { v.K.Prototype(name, fn) }
func (v Value) Method(name string) (Method, bool) { return v.K.Method(name) }

// Invoke calls the method `name` of v: v.name(args…), the call after a dot. A method of null is null,
// as every member of null is (null.trim() is null, the receiver chain never fails). A name that is
// neither a function v owns, nor a builtin of its kind, nor a Go method of a host value is not a
// function: calling it is the NotAFunction failure (14/10/2026), not the value read.
func (v Value) Invoke(name string, args ...Value) Value {
	if v.K == Nil {
		return v
	}

	// Nếu là Proxy, ưu tiên hỏi Handler
	if v.K == Proxy {
		if handler, ok := v.V.(ProxyHandler); ok {
			return handler.OnInvoke(name, args...)
		}
	}

	// An own key that holds data never answers a call to a builtin method: the call goes to the
	// builtin, as on an object without that key (CallsOwn). Reading the key (Get) still gives the
	// data. The builtin is found first (a switch), so the object's keys are looked up once.
	if v.K == Map {
		if builtin, ok := v.K.Method(name); ok {
			if own, has := v.Map()[name]; !has || own.K != Func {
				return builtin(v, args...)
			}
		}
	}

	// fmt.Printf("[Value] Invoking method: %s on kind: %s\n", name, v.K.String())
	attr := v.Get(name)
	if attr.K == Func {
		if fn, ok := attr.V.(Method); ok {
			return fn(v, args...)
		}
		return attr.Call(name, args...)
	}
	// A Go method without arguments is a getter that Get has already called: attr is its result. A
	// failure passes on as it is. Anything else is data, or nothing, and data never answers a call:
	// { x: 3 }.x() is an error, as in JavaScript; until 14/10/2026 it gave 3.
	if attr.K == Invalid || v.hasGoMethod(name) {
		return attr
	}
	return NotAFunction(name, attr)
}

func (v Value) Call(name string, args ...Value) Value {
	if v.K != Func || v.V == nil {
		return NotAFunction(name, v)
	}
	if _, ok := v.V.(SafeFunction); ok {
		// safe runs its function on the VM that called it: a host has no VM to lend it
		return InvalidFailure(InvalidArgumentCode, "safe chạy hàm của nó trên chính chương trình gọi nó: gọi thẳng safe(() => …)")
	}
	if fn, ok := v.V.(Method); ok {
		return fn(Value{K: Nil}, args...)
	}
	if goFn, ok := v.V.(func(...Value) Value); ok {
		return goFn(args...)
	}
	if fo, ok := v.V.(*FuncObject); ok {
		return fo.Fn(args...)
	}

	if fn, ok := v.V.(reflect.Value); ok {
		if !fn.IsValid() || fn.Kind() != reflect.Func {
			return invalidCall(name, "host value is not callable")
		}
		fnType := fn.Type()
		numIn := fnType.NumIn()
		isVariadic := fnType.IsVariadic()

		minArgs := numIn
		if isVariadic {
			minArgs = numIn - 1
		}

		if len(args) < minArgs {
			return invalidCall(
				name,
				fmt.Sprintf("expected at least %d arguments, got %d", minArgs, len(args)),
			)
		}
		if !isVariadic && len(args) > numIn {
			return invalidCall(
				name,
				fmt.Sprintf("expected %d arguments, got %d", numIn, len(args)),
			)
		}

		goArgs := make([]reflect.Value, len(args))
		for i := 0; i < len(args); i++ {
			var targetType reflect.Type
			if isVariadic && i >= numIn-1 {
				targetType = fnType.In(numIn - 1).Elem()
			} else if i < numIn {
				targetType = fnType.In(i)
			} else {
				return invalidCall(name, "argument is outside the host function signature")
			}
			transformed, compatible := transformArg(args[i], targetType)
			if !compatible {
				return invalidCall(
					name,
					fmt.Sprintf("argument %d cannot convert to %s", i+1, targetType),
				)
			}
			goArgs[i] = transformed
		}
		results := fn.Call(goArgs)
		return reflectedCallResult(name, results)
	}
	return NotAFunction(name, v)
}

// invalidCall is a host function called with arguments it cannot take.
func invalidCall(name, detail string) Value {
	if name == "" {
		name = "<anonymous>"
	}
	return InvalidFailure(InvalidArgumentCode, fmt.Sprintf("call %s: %s", name, detail))
}

func transformArg(val Value, targetType reflect.Type) (reflect.Value, bool) {
	if targetType == reflect.TypeOf(Value{}) {
		return reflect.ValueOf(val), true
	}
	v := val.Interface()
	if v == nil {
		return reflect.Zero(targetType), true
	}
	rv := reflect.ValueOf(v)
	if rv.Type().AssignableTo(targetType) {
		return rv, true
	}
	if rv.Type().ConvertibleTo(targetType) {
		return rv.Convert(targetType), true
	}
	if val.K == Number {
		switch targetType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return reflect.ValueOf(int64(val.N)).Convert(targetType), true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return reflect.ValueOf(uint64(val.N)).Convert(targetType), true
		}
	}
	return reflect.Value{}, false
}

func reflectedCallResult(name string, results []reflect.Value) Value {
	if len(results) == 0 {
		return Value{K: Nil}
	}

	last := results[len(results)-1]
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	// Only an explicit `error` result is an error channel. Host API objects may
	// themselves implement Error() while still being ordinary fluent values.
	if last.IsValid() && last.Type() == errorType {
		isNil := false
		switch last.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			isNil = last.IsNil()
		}
		if !isNil {
			return FailureOfError("call "+nameOrAnonymous(name), last.Interface().(error))
		}
		if len(results) == 1 {
			return Value{K: Nil}
		}
	}
	return New(results[0].Interface())
}

// TenantCache enables runtime/VM to query the in-memory cache directly without import cycles.
type TenantCache interface {
	GetCache(key string) (Value, bool)
	SetCache(key string, val Value, ttl Value)
	DeleteCache(key string)
	ClearCache()
}
