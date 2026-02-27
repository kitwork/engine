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
}

func (v Value) Prototype(name string, fn Method)  { v.K.Prototype(name, fn) }
func (v Value) Method(name string) (Method, bool) { return v.K.Method(name) }

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

	attr := v.Get(name)
	if attr.K == Func {
		if fn, ok := attr.V.(Method); ok {
			return fn(v, args...)
		}
		return attr.Call(name, args...)
	}
	return attr
}

func (v Value) Call(name string, args ...Value) Value {
	if v.K != Func || v.V == nil {
		return Value{K: Invalid}
	}
	if fn, ok := v.V.(Method); ok {
		return fn(Value{K: Nil}, args...)
	}
	if goFn, ok := v.V.(func(...Value) Value); ok {
		return goFn(args...)
	}

	if fn, ok := v.V.(reflect.Value); ok {
		fnType := fn.Type()
		numIn := fnType.NumIn()
		isVariadic := fnType.IsVariadic()

		minArgs := numIn
		if isVariadic {
			minArgs = numIn - 1
		}

		if len(args) < minArgs {
			fmt.Printf("[Value Call] Panic Prevention: Too few arguments for function %s. Expected at least %d, got %d\n", name, minArgs, len(args))
			return Value{K: Nil}
		}

		goArgs := make([]reflect.Value, len(args))
		for i := 0; i < len(args); i++ {
			var targetType reflect.Type
			if isVariadic && i >= numIn-1 {
				targetType = fnType.In(numIn - 1).Elem()
			} else if i < numIn {
				targetType = fnType.In(i)
			} else {
				return Value{K: Invalid}
			}
			goArgs[i] = transformArg(args[i], targetType)
		}
		results := fn.Call(goArgs)
		if len(results) > 0 {
			return New(results[0].Interface())
		}
		return Value{K: Nil}
	}
	return Value{K: Invalid}
}

func transformArg(val Value, targetType reflect.Type) reflect.Value {
	if targetType == reflect.TypeOf(Value{}) {
		return reflect.ValueOf(val)
	}
	v := val.Interface()
	if v == nil {
		return reflect.Zero(targetType)
	}
	rv := reflect.ValueOf(v)
	if rv.Type().AssignableTo(targetType) {
		return rv
	}
	if rv.Type().ConvertibleTo(targetType) {
		return rv.Convert(targetType)
	}
	if val.K == Number {
		switch targetType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return reflect.ValueOf(int64(val.N)).Convert(targetType)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return reflect.ValueOf(uint64(val.N)).Convert(targetType)
		}
	}
	return reflect.Zero(targetType)
}
