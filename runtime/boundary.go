package runtime

import (
	"github.com/kitwork/engine/value"
)

func (vm *VM) currentInstruction() int {
	if vm == nil || vm.FrameIdx < 0 || vm.FrameIdx >= len(vm.Frames) {
		return -1
	}
	return vm.Frames[vm.FrameIdx].LastIP
}

// nativeValue contains a panic only around a host-owned call. Interpreter
// panics remain visible as engine bugs rather than being mislabeled as tenant
// failures. A host call may run a nested VM (nesting.go): the VM shows it what
// is left before the call and takes back what it spent after.
func (vm *VM) nativeValue(label string, call func() value.Value) (result value.Value) {
	vm.showHost()
	defer func() {
		if recovered := recover(); recovered != nil {
			result = vm.nativePanic(label, recovered)
		}
		if vm.nested.returned.Load() != nil {
			result = vm.takeBack(result)
		}
	}()
	return call()
}

func (vm *VM) nativePanic(label string, recovered any) value.Value {
	return vm.diagnosticValue(
		DiagnosticNativePanic,
		nativePanicMessage(label, recovered),
		vm.currentInstruction(),
	)
}

// nativeProperty reads a property through the host. It is the hottest host
// boundary, so it does not show the VM's budget (a nested VM started by a read
// sees it as of the latest host call or checkpoint), but it still takes back
// what such a VM handed back.
func (vm *VM) nativeProperty(target value.Value, key string) (result value.Value) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = vm.nativePanic("property access", recovered)
		}
		if vm.nested.returned.Load() != nil {
			result = vm.takeBack(result)
		}
	}()
	return target.Get(key)
}

// nativeMethod keeps the hot INVOKE path free of a per-call closure and label
// concatenation while preserving the same native-panic diagnostic boundary.
func (vm *VM) nativeMethod(target value.Value, method string, args []value.Value) (result value.Value) {
	vm.showHost()
	mark := vm.passLambdas(args)
	defer func() {
		if recovered := recover(); recovered != nil {
			result = vm.nativePanic("method "+method, recovered)
		}
		vm.withdrawLambdas(mark)
		if vm.nested.returned.Load() != nil {
			result = vm.takeBack(result)
		}
	}()
	return target.Invoke(method, args...)
}

func (vm *VM) nativeStandardMethod(
	target value.Value,
	method string,
	standard value.Method,
	args []value.Value,
) (result value.Value) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = vm.nativePanic("method "+method, recovered)
		}
	}()
	return standard(target, args...)
}

func (vm *VM) nativeAction(label string, call func()) value.Value {
	return vm.nativeValue(label, func() value.Value {
		call()
		return value.Value{K: value.Nil}
	})
}
