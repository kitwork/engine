package runtime

import (
	"fmt"

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
// failures.
func (vm *VM) nativeValue(label string, call func() value.Value) (result value.Value) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = vm.nativePanic(label, recovered)
		}
	}()
	return call()
}

func (vm *VM) nativePanic(label string, recovered any) value.Value {
	return vm.diagnosticValue(
		DiagnosticNativePanic,
		fmt.Sprintf("Native panic in %s: %v", label, recovered),
		vm.currentInstruction(),
	)
}

// nativeMethod keeps the hot INVOKE path free of a per-call closure and label
// concatenation while preserving the same native-panic diagnostic boundary.
func (vm *VM) nativeMethod(target value.Value, method string, args []value.Value) (result value.Value) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = vm.nativePanic("method "+method, recovered)
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
