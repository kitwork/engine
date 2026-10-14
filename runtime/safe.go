package runtime

import (
	"strconv"

	"github.com/kitwork/engine/value"
)

// safe(f) is how a program reads a failure (14/10/2026). It calls f with no arguments, on this VM, in a
// frame of its own, on the same energy budget and under the same rules for what stops a run, and always
// gives a plain object:
//
//	{ value: <what f returned>, error: null }
//	{ value: null, error: { message, code, at } }      f failed, anywhere inside, however deep
//
// error.progress is there too for a PARTIAL failure. The frame gives the failure a floor: a failure in
// f unwinds f's frames, running their defers, down to it and no further (ExecuteLambda), so the program
// goes on after safe. What safe catches is a runtime error of the program, or of something the program
// asked for (a query, a body read, a call): fail, a call of what is not a function, an operation on
// kinds it does not take, a failure of the host. What it never catches is a limit the host puts on the
// program, and the engine's own integrity: ENERGY_LIMIT, EXECUTION_CANCELLED, STACK_OVERFLOW,
// NATIVE_PANIC, PROGRAM_MISMATCH, NO_PROGRAM, VM_STOPPED, UNKNOWN_OPCODE, TRUNCATED_BYTECODE pass
// through every safe and end the request. None of them is ever an error.code.

// callSafe runs the global safe with args, for the call at ip.
func (vm *VM) callSafe(ip int, args []value.Value) value.Value {
	if len(args) != 1 {
		// outside what safe protects: it does not catch its own misuse
		return value.InvalidFailure(value.InvalidArgumentCode, safeArgumentCountMessage(len(args)))
	}
	function := args[0]
	var result value.Value
	if lambda, ok := function.V.(*value.Lambda); ok && lambda != nil && function.K == value.Func {
		result = vm.ExecuteLambda(lambda, nil)
	} else {
		called, callable := vm.callHost(function, nil)
		if !callable {
			return value.InvalidFailure(
				value.InvalidArgumentCode,
				"safe nhận một hàm, không nhận "+value.KindName(function)+": viết safe(() => …)",
			)
		}
		result = called
	}
	return vm.safeResult(ip, result)
}

func safeArgumentCountMessage(count int) string {
	if count == 0 {
		return "safe nhận một hàm: viết safe(() => …)"
	}
	return "safe nhận đúng một hàm, không nhận " + strconv.Itoa(count) + " đối số: viết safe(() => …)"
}

// safeResult is the object safe gives for what its function returned, or the failure itself when it is
// one safe never catches.
func (vm *VM) safeResult(ip int, result value.Value) value.Value {
	if result.K != value.Invalid {
		return value.New(map[string]value.Value{
			"value": result,
			"error": {K: value.Nil},
		})
	}
	if diagnostic, ok := DiagnosticFrom(result); ok {
		if diagnostic.Code != DiagnosticRuntimeError {
			return result
		}
		return safeFailure(diagnostic.Message, diagnostic.CauseCode, diagnosticAt(diagnostic), diagnostic.Progress)
	}
	// A host function safe called directly failed without a frame of its own: the failure is at the
	// call of safe.
	failure, ok := value.FailureFrom(result)
	if !ok {
		failure = value.NewFailure(value.DefaultFailureCode, result.Text())
	}
	if failure.Code == value.CancelledCode || vm.cancelled() {
		return vm.diagnosticValue(DiagnosticCancelled, cancelledMessage(failure.Message), ip)
	}
	// as the interpreter does for a host call inside f: a host that wrapped a failure nothing catches
	// keeps it so, whether safe called it through a lambda or directly
	if code, never := uncatchableCode(failure.Code); never {
		return vm.diagnosticValue(code, failure.Message, ip)
	}
	return safeFailure(failure.Message, failure.Code, locationAt(vm.currentLocation(ip)), failure.Progress)
}

// safeFailure is { value: null, error: { message, code, at } }, with error.progress for PARTIAL.
func safeFailure(message, code, at string, progress value.Value) value.Value {
	code = value.ClosedCode(code)
	failure := map[string]value.Value{
		"message": value.NewString(message),
		"code":    value.NewString(code),
		"at":      {K: value.Nil},
	}
	if at != "" {
		failure["at"] = value.NewString(at)
	}
	if code == value.PartialCode && progress.K != value.Invalid {
		failure["progress"] = progress
	}
	return value.New(map[string]value.Value{
		"value": {K: value.Nil},
		"error": value.New(failure),
	})
}

// FailureObject is the error object of a failure, the shape safe gives as error, for a host that
// hands a failure to the program another way (a router's .error() handler): { message, code, at },
// error.progress for PARTIAL. code is the diagnostic's own for a failure safe never catches
// (ENERGY_LIMIT, EXECUTION_CANCELLED…), since it is the end of the run that is being described.
func FailureObject(failure value.Value) value.Value {
	if diagnostic, ok := DiagnosticFrom(failure); ok {
		if diagnostic.Code != DiagnosticRuntimeError {
			object := safeFailure(diagnostic.Message, "", diagnosticAt(diagnostic), value.Value{}).Get("error")
			object.Map()["code"] = value.NewString(string(diagnostic.Code))
			return object
		}
		return safeFailure(diagnostic.Message, diagnostic.CauseCode, diagnosticAt(diagnostic), diagnostic.Progress).Get("error")
	}
	attached, ok := value.FailureFrom(failure)
	if !ok {
		attached = value.NewFailure(value.DefaultFailureCode, failure.Text())
	}
	return safeFailure(attached.Message, attached.Code, "", attached.Progress).Get("error")
}

// Failure is the failure value of this diagnostic, for a host that holds it as a Go error
// (Tenant.Execute) and hands it on: the program reads of it what it reads of the run itself, its
// message, code and place (safe, FailureObject), never a message led by the place and the stack.
func (d *Diagnostic) Failure() value.Value { return diagnosticResult(d) }

// diagnosticAt is "file:line:column" of where a failure happened, the innermost frame at the failing
// instruction ("line:column" for a source with no name); "" when the bytecode does not tell.
func diagnosticAt(diagnostic *Diagnostic) string {
	return locationAt(SourceLocation{File: diagnostic.File, Line: diagnostic.Line, Column: diagnostic.Column})
}

func locationAt(location SourceLocation) string {
	if location.Line <= 0 {
		return ""
	}
	place := strconv.Itoa(int(location.Line))
	if location.Column > 0 {
		place += ":" + strconv.Itoa(int(location.Column))
	}
	if location.File != "" {
		place = location.File + ":" + place
	}
	return place
}

// uncatchableCode reports whether code, the code of a failure a host returned, is one of a failure
// nothing catches (a host that read a nested run's diagnostic as a Go error and wrapped it), and which.
func uncatchableCode(code string) (DiagnosticCode, bool) {
	switch DiagnosticCode(code) {
	case DiagnosticEnergyLimit, DiagnosticCancelled, DiagnosticStackOverflow, DiagnosticNativePanic,
		DiagnosticProgramMismatch, DiagnosticNoProgram, DiagnosticVMStopped, DiagnosticUnknownOpcode,
		DiagnosticTruncatedBytecode:
		return DiagnosticCode(code), true
	}
	return "", false
}

// FailureCode is the code of the failure a host makes of this diagnostic when it reads it as a Go
// error (value.FailureOfError): a runtime error's cause, the diagnostic's own code for any other, so a
// failure nothing catches stays one (uncatchableCode).
func (d *Diagnostic) FailureCode() string {
	if d.Code == DiagnosticRuntimeError {
		return d.CauseCode
	}
	return string(d.Code)
}

// cancelled reports whether the request this VM runs for is over: a host call that failed then failed
// because of it, whatever it said.
func (vm *VM) cancelled() bool {
	return vm.Context != nil && vm.Context.Err() != nil
}

func cancelledMessage(message string) string {
	return "Execution Cancelled: " + message
}
