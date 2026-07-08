package builtins

import "github.com/kitwork/engine/value"

func Array() value.Value {
	ctor := func(args ...value.Value) value.Value {
		if len(args) == 1 && args[0].K == value.Number {
			n := int(args[0].N)
			out := make([]value.Value, n)
			for i := 0; i < n; i++ {
				out[i] = value.Value{K: value.Nil}
			}
			return value.New(out)
		}
		return value.New(args)
	}

	props := map[string]value.Value{
		"isArray": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.FALSE
			}
			return value.ToBool(args[0].K == value.Array)
		}),
	}

	return value.NewFuncObject(ctor, props)
}
