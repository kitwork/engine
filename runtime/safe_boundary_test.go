package runtime_test

import (
	"context"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// runBoundary runs `const check = safe(() => host()); after = 1` with host answering failure, on a VM
// whose context is ctx, and gives what the run ended with and the VM.
func runBoundary(t *testing.T, ctx context.Context, failure value.Value) (value.Value, *runtime.VM) {
	t.Helper()
	return runBoundaryOf(t, ctx, failure, "safe(() => host())")
}

// runBoundaryOf is runBoundary with the call of safe written as call: safe(() => host()) or safe(host).
func runBoundaryOf(t *testing.T, ctx context.Context, failure value.Value, call string) (value.Value, *runtime.VM) {
	t.Helper()
	bytecode, err := compiler.CompileSource("var after = 0;\nconst check = " + call + ";\nafter = 1;\nvar result = check.error.code;")
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Context = ctx
	vm.Globals["safe"] = value.NewSafeFunction()
	vm.Globals["host"] = value.NewFunc(func(...value.Value) value.Value { return failure })
	return vm.Run(), vm
}

// A host that wraps a failure nothing catches (it read a nested run's diagnostic as a Go error) keeps
// it so: the run ends with that code, safe or not. A host call that failed because the request is over
// is the cancellation, whatever it said. Any other failure of the host is caught.
func TestSafeBoundaryHoldsThroughTheHost(t *testing.T) {
	cases := []struct {
		label   string
		ctx     context.Context
		failure value.Value
		ends    runtime.DiagnosticCode
	}{
		{"energy the host wrapped", context.Background(), value.InvalidFailure(string(runtime.DiagnosticEnergyLimit), "Energy Limit Exceeded"), runtime.DiagnosticEnergyLimit},
		{"a panic the host wrapped", context.Background(), value.InvalidFailure(string(runtime.DiagnosticNativePanic), "Native panic"), runtime.DiagnosticNativePanic},
		{"the cancellation a host reports", context.Background(), value.InvalidFailure(value.CancelledCode, "query: context canceled"), runtime.DiagnosticCancelled},
		{"a stack overflow the host wrapped", context.Background(), value.InvalidFailure(string(runtime.DiagnosticStackOverflow), "Stack overflow"), runtime.DiagnosticStackOverflow},
		{"an integrity failure the host wrapped", context.Background(), value.InvalidFailure(string(runtime.DiagnosticProgramMismatch), "program mismatch"), runtime.DiagnosticProgramMismatch},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases = append(cases, struct {
		label   string
		ctx     context.Context
		failure value.Value
		ends    runtime.DiagnosticCode
	}{"any failure once the request is over", cancelled, value.InvalidFailure(value.DatabaseCode, "driver: bad connection"), runtime.DiagnosticCancelled})

	// safe(host) hands the host function to safe itself: the host fails with no frame of its own, and
	// the same rule holds
	for _, call := range []string{"safe(() => host())", "safe(host)"} {
		for _, c := range cases {
			returned, vm := runBoundaryOf(t, c.ctx, c.failure, call)
			diagnostic, ok := runtime.DiagnosticFrom(returned)
			if !ok || diagnostic.Code != c.ends {
				t.Errorf("%s, %s: run = %s, want it ended by %s", call, c.label, returned.Text(), c.ends)
				continue
			}
			if vm.Vars["after"].N != 0 {
				t.Errorf("%s, %s: safe caught it and the run went on", call, c.label)
			}
		}

		// control: the same failure of the host while the request goes on is caught, with its code
		returned, vm := runBoundaryOf(t, context.Background(), value.InvalidFailure(value.DatabaseCode, "driver: bad connection"), call)
		if returned.IsInvalid() || vm.Vars["after"].N != 1 || vm.Vars["result"].Text() != value.DatabaseCode {
			t.Fatalf("%s, control: run = %s, result %s", call, returned.Text(), vm.Vars["result"].Text())
		}
	}
}
