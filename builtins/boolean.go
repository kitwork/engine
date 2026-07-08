package builtins

import "github.com/kitwork/engine/value"

func Boolean() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.FALSE
		}
		return value.ToBool(args[0].Truthy())
	})
}
