package builtins

import "github.com/kitwork/engine/value"

// Fail is the global fail("…"): the program stops itself here. The failure's code is always FAIL
// (14/10/2026): an application never sets a code of its own, so the set of codes keeps its meaning (a
// result the program expects, out of stock or a wrong password, is data a function returns). Only the
// first argument counts, as text. safe(() => …) catches it like any runtime error, at the fail call.
//
// The global Error was another name for it until 14/10/2026 and is gone: JavaScript's Error("x") makes
// a value, and here the same words stopped the program where the value was made.
func Fail() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		msg := "error"
		if len(args) > 0 {
			if s := args[0].Text(); s != "" {
				msg = s
			}
		}
		return value.InvalidFailure(value.FailCode, msg)
	})
}

// Safe is the global safe(() => …) (value.SafeFunction, run by the VM: runtime/safe.go).
func Safe() value.Value {
	return value.NewSafeFunction()
}
