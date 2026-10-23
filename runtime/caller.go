package runtime

import (
	"github.com/kitwork/engine/value"
)

// A host object is built once and used by every request: a module imported at the top of a file
// (import * as routine from "kitwork/routine") is made by the VM that loads the file, before any request.
// A method of such an object that needs the VM calling it (the request's context it runs under, where the
// call is written, whether a file is still loading) is handed it here: INVOKE hands it to an object that
// is CallerMethods, CALL to a value.CallerFunction (a method read off the object by name), and so do a
// call of an object's key that holds one ({ all: routine.wait }.all(fn)) and an array's callback. Nothing
// else changes for any other object, so the seam costs INVOKE one check of the target's kind.

// CallerMethods is a host object whose methods are handed the VM that calls them. InvokeFrom answers
// method with args as the object's OnInvoke would, and handled is false for a method it leaves to it.
type CallerMethods interface {
	InvokeFrom(caller *VM, method string, args []value.Value) (result value.Value, handled bool)
}

// CallSite is where the call the VM runs now is written: the instruction of the host call in progress,
// in its current frame.
func (vm *VM) CallSite() SourceLocation {
	if vm == nil {
		return SourceLocation{}
	}
	return vm.currentLocation(vm.currentInstruction())
}

// WrapFailure is the failure of something a host call ran on the program's behalf, cause, told as the
// call's own: a runtime error at the call (the place of the host call in progress on vm; none when vm is
// nil), with message, cause's code and cause itself, which safe gives as error.cause. A host that ran
// several things reports the one that failed this way (routine.wait). A cause nothing catches (a limit
// of the host, the request over) is given back as it is: it ends the run, wrapped or not.
func WrapFailure(vm *VM, message string, cause value.Value) value.Value {
	inner, ok := DiagnosticFrom(cause)
	if ok && inner.Code != DiagnosticRuntimeError {
		return cause
	}
	if !ok {
		failure, has := value.FailureFrom(cause)
		if !has {
			return cause // not a failure: nothing to wrap
		}
		if failure.Code == value.CancelledCode {
			return cause
		}
		if _, never := uncatchableCode(failure.Code); never {
			return cause
		}
		inner = &Diagnostic{Code: DiagnosticRuntimeError, CauseCode: failure.Code, Message: failure.Message, IP: -1}
	}
	diagnostic := &Diagnostic{Code: DiagnosticRuntimeError, Message: message, IP: -1}
	if vm != nil {
		diagnostic = vm.buildDiagnostic(DiagnosticRuntimeError, message, vm.currentInstruction())
	}
	diagnostic.CauseCode = inner.CauseCode
	diagnostic.Cause = inner
	return diagnosticResult(diagnostic)
}
