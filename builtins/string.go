package builtins

import "github.com/kitwork/engine/value"

func String() value.Value {
	ctor := func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.NewString("")
		}
		return value.NewString(args[0].Text())
	}

	props := map[string]value.Value{
		"fromCharCode": value.NewFunc(func(args ...value.Value) value.Value {
			runes := make([]rune, 0, len(args))
			for _, a := range args {
				if a.K == value.Number {
					runes = append(runes, rune(int(a.N)))
				}
			}
			return value.NewString(string(runes))
		}),
	}

	return value.NewFuncObject(ctor, props)
}
