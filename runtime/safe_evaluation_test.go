package runtime

import (
	"testing"

	"github.com/kitwork/engine/value"
)

func TestSafeEvaluationResultRescuesOnlyRuntimeErrors(t *testing.T) {
	runtimeFailure := diagnosticResult(&Diagnostic{
		Code:    DiagnosticRuntimeError,
		Message: "boom",
		IP:      -1,
	})
	rescued := safeEvaluationResult(runtimeFailure)
	if rescued.K == value.Invalid || rescued.Get("ok").Truthy() ||
		rescued.Get("error").Text() != "boom" {
		t.Fatalf("runtime failure was not rescued: %#v", rescued)
	}

	for _, code := range []DiagnosticCode{
		DiagnosticEnergyLimit,
		DiagnosticCancelled,
		DiagnosticStackOverflow,
		DiagnosticProgramMismatch,
		DiagnosticNoProgram,
		DiagnosticVMStopped,
		DiagnosticNativePanic,
	} {
		t.Run(string(code), func(t *testing.T) {
			failure := diagnosticResult(&Diagnostic{Code: code, Message: "fatal", IP: -1})
			got := safeEvaluationResult(failure)
			diagnostic, ok := DiagnosticFrom(got)
			if !ok || diagnostic.Code != code {
				t.Fatalf("safe evaluation changed %s into %#v", code, got)
			}
		})
	}
}

func TestSafeEvaluationResultWrapsSuccessAndRawInvalid(t *testing.T) {
	success := safeEvaluationResult(value.New(42))
	if !success.Get("ok").Truthy() || success.Get("value").Int() != 42 {
		t.Fatalf("safe success = %#v", success)
	}

	rescued := safeEvaluationResult(value.Value{K: value.Invalid, V: "raw failure"})
	if rescued.Get("ok").Truthy() || rescued.Get("error").Text() != "raw failure" {
		t.Fatalf("safe raw failure = %#v", rescued)
	}
}
