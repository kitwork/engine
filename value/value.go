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

// Value is the one slot of the VM: three fields, 32 bytes, under the line Go keeps a struct in
// registers (at most 4 words and 4 fields; past it every push, pop and store goes through memory
// with bulk write barriers). What a value is is K; what it holds is N for the immediate kinds
// (Number, Bool as 1/0, Time, Duration) and V for the rest. A failure is K == Invalid with what failed
// in V: a *Failure for an application failure, the run's *runtime.Diagnostic for the VM's own; the
// zero Value (K == Invalid, V == nil) is nothing, never a failure (Failed). A String the engine made
// already-safe HTML holds a TrustedHTML in V (Trusted): the trust is the payload's type, so no
// operation carries it on. Nothing else rides on a Value: until 25/10/2026 IsError, ErrorVal and Raw
// were three more fields saying what K and V already said, and the struct was 56 bytes.
type Value struct {
	N float64
	V any
	K Kind
}

func (v Value) Prototype(name string, fn Method)  { v.K.Prototype(name, fn) }
func (v Value) Method(name string) (Method, bool) { return v.K.Method(name) }

// Failed reports whether v is a failure: K == Invalid carrying what failed. The zero Value is not one.
func (v Value) Failed() bool { return v.K == Invalid && v.V != nil }

// Failure is the application failure v carries, by pointer: read it, never write it, since every copy
// of v shares it (FailureFrom gives a copy). false for a run's diagnostic, for the zero Value and for
// every other kind.
func (v Value) Failure() (*Failure, bool) {
	failure, ok := v.V.(*Failure)
	return failure, ok && failure != nil && v.K == Invalid
}

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
	attr := v.member(name, false)
	if attr.K == Func {
		if fn, ok := attr.V.(Method); ok {
			return fn(v, args...)
		}
		return attr.Call(name, args...)
	}
	// A Go method without arguments is a getter that Get has already called: attr is its result. A
	// failure passes on as it is. Anything else is data, or nothing, and data never answers a call:
	// { x: 3 }.x() is an error, as in JavaScript; until 14/10/2026 it gave 3.
	// A getter whose result is a callable proxy is called with the arguments, as obj.f(args) in
	// JavaScript reads f and calls it (15/10/2026, for ctx.database("ledger"), which was removed on
	// 22/10/2026). Without arguments the proxy itself answers, as any getter's result does below.
	if attr.K == Proxy && len(args) > 0 {
		if handler, ok := attr.V.(ProxyHandler); ok {
			return handler.OnInvoke("", args...)
		}
	}
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
	if fn, ok := v.V.(*CallerFunction); ok && fn.Call != nil {
		// a host calling it lends no VM
		return fn.Call(nil, args)
	}
	if bound, ok := v.V.(*BoundMethod); ok && bound != nil {
		return bound.Function.Call(name, args...)
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
