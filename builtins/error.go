package builtins

import "github.com/kitwork/engine/value"

func Error() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		msg := "error"
		if len(args) > 0 {
			if s := args[0].Text(); s != "" {
				msg = s
			}
		}
		return value.Value{K: value.Invalid, V: msg}
	})
}
