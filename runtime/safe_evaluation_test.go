package runtime

import (
	"testing"

	"github.com/kitwork/engine/value"
)

// safeResult is what safe(() => x) gives for what its function returned (safe.go): a runtime error
// becomes { value: null, error: { message, code, at } }; every failure the host puts on a program, and
// the engine's integrity, passes through unchanged.
func TestSafeResultRescuesOnlyRuntimeErrors(t *testing.T) {
	var vm *VM
	runtimeFailure := diagnosticResult(&Diagnostic{
		Code:      DiagnosticRuntimeError,
		CauseCode: value.FailCode,
		Message:   "boom",
		IP:        -1,
		File:      "router.kitwork.js",
		Line:      4,
		Column:    7,
	})
	rescued := vm.safeResult(-1, runtimeFailure)
	failure := rescued.Get("error")
	if rescued.K != value.Map || rescued.Get("value").K != value.Nil ||
		failure.Get("message").Text() != "boom" || failure.Get("code").Text() != value.FailCode ||
		failure.Get("at").Text() != "router.kitwork.js:4:7" {
		t.Fatalf("runtime failure was not rescued: %s", rescued.Text())
	}
	if _, has := failure.Map()["progress"]; has {
		t.Fatalf("a failure that is not PARTIAL carries progress: %s", failure.Text())
	}

	for _, code := range []DiagnosticCode{
		DiagnosticEnergyLimit,
		DiagnosticCancelled,
		DiagnosticStackOverflow,
		DiagnosticProgramMismatch,
		DiagnosticNoProgram,
		DiagnosticVMStopped,
		DiagnosticNativePanic,
		DiagnosticUnknownOpcode,
		DiagnosticTruncatedBytecode,
	} {
		t.Run(string(code), func(t *testing.T) {
			failure := diagnosticResult(&Diagnostic{Code: code, Message: "fatal", IP: -1})
			got := vm.safeResult(-1, failure)
			diagnostic, ok := DiagnosticFrom(got)
			if !ok || diagnostic.Code != code {
				t.Fatalf("safe changed %s into %s", code, got.Text())
			}
		})
	}
}

func TestSafeResultOfSuccessIsAPlainObject(t *testing.T) {
	var vm *VM
	success := vm.safeResult(-1, value.New(42))
	if success.K != value.Map || success.Get("value").Int() != 42 || success.Get("error").K != value.Nil {
		t.Fatalf("safe success = %s", success.Text())
	}
	if len(success.Map()) != 2 {
		t.Fatalf("safe success has keys beyond value and error: %s", success.Text())
	}
	// a function that returns null is a success too: error, not value, says whether it failed
	empty := vm.safeResult(-1, value.Value{K: value.Nil})
	if empty.Get("value").K != value.Nil || empty.Get("error").K != value.Nil {
		t.Fatalf("safe of null = %s", empty.Text())
	}
}

// A code outside the closed set reads as ERROR; a vendor's old code is not passed on.
func TestSafeResultClosesTheCode(t *testing.T) {
	var vm *VM
	for cause, want := range map[string]string{
		"KITDB_TRANSACTION_CONFLICT": value.DefaultFailureCode,
		value.ConflictCode:           value.ConflictCode,
		"":                           value.DefaultFailureCode,
		value.NotAFunctionCode:       value.NotAFunctionCode,
	} {
		failure := diagnosticResult(&Diagnostic{Code: DiagnosticRuntimeError, CauseCode: cause, Message: "x", IP: -1})
		if got := vm.safeResult(-1, failure).Get("error").Get("code").Text(); got != want {
			t.Errorf("cause %q: code = %q, want %q", cause, got, want)
		}
	}
}

// PARTIAL hands its progress on as error.progress: what was written before the bulk write stopped.
func TestSafeResultCarriesProgressForPartial(t *testing.T) {
	var vm *VM
	progress := value.New(map[string]value.Value{"inserted": value.New(2), "next": value.New(2)})
	failure := diagnosticResult(&Diagnostic{
		Code: DiagnosticRuntimeError, CauseCode: value.PartialCode, Progress: progress, Message: "stopped", IP: -1,
	})
	got := vm.safeResult(-1, failure).Get("error")
	if got.Get("code").Text() != value.PartialCode || got.Get("progress").Get("next").Int() != 2 {
		t.Fatalf("PARTIAL = %s, want its progress", got.Text())
	}
}

// A diagnostic leads its printed text with where it is; its message never holds the place.
func TestDiagnosticPrintsItsPlaceBeforeItsMessage(t *testing.T) {
	diagnostic := &Diagnostic{Code: DiagnosticRuntimeError, Message: "o.x không phải hàm (là số): không gọi được",
		File: "router.kitwork.js", Line: 12, Column: 5}
	if got := diagnostic.Error(); got != "router.kitwork.js:12:5: o.x không phải hàm (là số): không gọi được" {
		t.Fatalf("Error() = %q", got)
	}
	// control: a diagnostic of a source with no name keeps the line after the message
	nameless := &Diagnostic{Code: DiagnosticRuntimeError, Message: "boom", Line: 3, Column: 1}
	if got := nameless.Error(); got != "boom (at line 3)" {
		t.Fatalf("Error() = %q", got)
	}
}
