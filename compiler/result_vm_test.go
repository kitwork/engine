package compiler

import (
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// safe() is the single inline-error shape: an object with .value and .error. The array form it
// replaced is gone, so the destructuring spelling this test used to check goes with it.
func TestSafeMethodVMDispatch(t *testing.T) {
	got := runResult(t, `
		const u = { id: 7, name: "ann" }
		const r = u.safe()
		result = r.value.id
	`)
	wantNum(t, got, 7, ".safe() -> { value }, value read")
}

func TestSafeMethodPreservesEscapingClosure(t *testing.T) {
	got := runResult(t, `
		const base = 40
		const check = (() => base + 2).safe()
		const fn = check.value
		result = fn()
	`)
	wantNum(t, got, 42, ".safe() preserves a returned closure's scope")
}

func TestSafeMethodRescuesVMRuntimeError(t *testing.T) {
	bytecode, err := CompileSource(`
const check = failHost().safe()
const result = { ok: check.ok, error: check.error, value: check.value }
`)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Globals["failHost"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "boom"}
	})

	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("safe runtime result = %s", result.Text())
	}
	result := vm.Vars["result"].Interface()
	got, ok := result.(map[string]any)
	if !ok || got["ok"] != false || got["error"] != "boom" || got["value"] != nil {
		t.Fatalf("safe result = %#v", result)
	}
}

func TestSafeMethodPreservesAttachedRuntimeErrorCode(t *testing.T) {
	bytecode, err := CompileSource(`
const check = failHost().safe()
const result = { ok: check.ok, code: check.code, error: check.error }
`)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Globals["failHost"] = value.NewFunc(func(...value.Value) value.Value {
		return value.InvalidFailure(
			"KITDB_TRANSACTION_CONFLICT",
			"transaction conflict",
		)
	})

	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("safe runtime result = %s", result.Text())
	}
	result := vm.Vars["result"].Interface()
	got, ok := result.(map[string]any)
	if !ok || got["ok"] != false || got["code"] != "KITDB_TRANSACTION_CONFLICT" ||
		got["error"] != "transaction conflict" {
		t.Fatalf("safe result = %#v", result)
	}
}

func TestSafeMethodPreservesFailureCodeAcrossCallsAndCallbacks(t *testing.T) {
	fixtures := []struct {
		name   string
		source string
	}{
		{
			name: "nested call",
			source: `
const run = (task) => task()
const check = run(() => failHost()).safe()
const result = { ok: check.ok, code: check.code, error: check.error }
`,
		},
		{
			name: "collection callback",
			source: `
const checks = [1].map(() => failHost().safe())
const check = checks[0]
const result = { ok: check.ok, code: check.code, error: check.error }
`,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			bytecode, err := CompileSource(fixture.source)
			if err != nil {
				t.Fatal(err)
			}
			vm := runtime.New(bytecode.Program)
			vm.Globals["failHost"] = value.NewFunc(func(...value.Value) value.Value {
				return value.InvalidFailure("HOST_CONFLICT", "host conflict")
			})

			if result := vm.Run(); result.K == value.Invalid {
				t.Fatalf("safe runtime result = %s", result.Text())
			}
			result := vm.Vars["result"].Interface()
			got, ok := result.(map[string]any)
			if !ok || got["ok"] != false || got["code"] != "HOST_CONFLICT" ||
				got["error"] != "host conflict" {
				t.Fatalf("safe result = %#v", result)
			}
		})
	}
}

func TestSafeMethodDoesNotRescueNativePanic(t *testing.T) {
	bytecode, err := CompileSource(`const result = explode().safe()`)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Globals["explode"] = value.NewFunc(func(...value.Value) value.Value {
		panic("boom")
	})

	result := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok || diagnostic.Code != runtime.DiagnosticNativePanic {
		t.Fatalf("safe native panic = %#v, want %s", result, runtime.DiagnosticNativePanic)
	}
	if diagnostic.Function != "<safe>" {
		t.Fatalf("safe native panic function = %q, want <safe>", diagnostic.Function)
	}
}

func TestSafeMethodSurvivesArtifactRoundTrip(t *testing.T) {
	compiled, err := CompileSource(`const result = failHost().safe().error`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := compiled.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalBytecode(encoded, compiled.SourceFingerprint())
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.InspectProgram(restored.Program)
	if err != nil {
		t.Fatal(err)
	}
	foundSafeEntry := false
	for _, entry := range report.EntryPoints {
		if entry.Name == "<safe>" {
			foundSafeEntry = true
			break
		}
	}
	if !foundSafeEntry {
		t.Fatal("round-trip inspection omitted the protected safe evaluator")
	}

	vm := runtime.New(restored.Program)
	vm.Globals["failHost"] = value.NewFunc(func(...value.Value) value.Value {
		return value.InvalidFailure("ROUND_TRIP_ERROR", "round trip")
	})
	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("round-trip runtime result = %s", result.Text())
	}
	if got := vm.Vars["result"].Text(); got != "round trip" {
		t.Fatalf("round-trip safe error = %q", got)
	}
}
