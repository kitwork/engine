package value

// CallerFunction is a function of the host that is handed the VM calling it: a *runtime.VM, which this
// package cannot name, or nil when no VM calls it (a host calling it through Call). A method of a module
// read off it by name (import { go, wait } from "kitwork/routine") is one: called alone it still knows the
// request it runs for, as routine.go(fn) through the module does (runtime.CallerMethods).
type CallerFunction struct {
	Call func(caller any, args []Value) Value
}

// NewCallerFunction is a function value whose host is handed the VM calling it.
func NewCallerFunction(call func(caller any, args []Value) Value) Value {
	return Value{K: Func, V: &CallerFunction{Call: call}}
}
