package runtime_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

var limitsSnapshotSink runtime.LimitsSnapshot

func TestRuntimeLimitsAreStableDetachedAndAllocationFree(t *testing.T) {
	want := runtime.LimitsSnapshot{
		MaxProgramBinaryBytes:     16 << 20,
		MaxBytecodeBytes:          1<<16 - 1,
		MaxConstants:              1 << 16,
		MaxVerifiedStackDepth:     1<<16 - 1,
		MaxCallDepth:              64,
		DefaultMaxEnergy:          10_000_000,
		CleanupEnergyReserve:      10_000,
		CancellationCheckInterval: 64,
	}
	got := runtime.Limits()
	if got != want {
		t.Fatalf("runtime limits = %+v, want %+v", got, want)
	}
	if got.MaxBytecodeBytes != runtime.MaxBytecodeSize ||
		got.MaxConstants != runtime.MaxConstants ||
		got.MaxVerifiedStackDepth != runtime.MaxStackDepth ||
		got.MaxProgramBinaryBytes != runtime.MaxProgramBinarySize {
		t.Fatalf("compatibility constants diverged from runtime limits: %+v", got)
	}
	if got.CancellationCheckInterval == 0 ||
		got.CancellationCheckInterval&(got.CancellationCheckInterval-1) != 0 {
		t.Fatalf("cancellation interval must remain a power of two: %d", got.CancellationCheckInterval)
	}

	got.MaxCallDepth = 1
	if runtime.Limits().MaxCallDepth != want.MaxCallDepth {
		t.Fatal("mutating a limit snapshot changed runtime policy")
	}
	if allocations := testing.AllocsPerRun(1_000, func() {
		limitsSnapshotSink = runtime.Limits()
	}); allocations != 0 {
		t.Fatalf("Limits allocated %.2f objects per snapshot", allocations)
	}
}

func TestRuntimeStructuralLimitBoundaries(t *testing.T) {
	limits := runtime.Limits()

	t.Run("Program binary bytes", func(t *testing.T) {
		data := make([]byte, limits.MaxProgramBinaryBytes+1)
		if _, err := runtime.UnmarshalProgram(data[:limits.MaxProgramBinaryBytes]); err == nil ||
			!strings.Contains(err.Error(), "invalid magic") {
			t.Fatalf("exact-limit decode error = %v, want envelope validation", err)
		}
		if _, err := runtime.UnmarshalProgram(data); err == nil ||
			!strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("over-limit decode error = %v, want size rejection", err)
		}
	})

	t.Run("Bytecode bytes", func(t *testing.T) {
		code := make([]byte, limits.MaxBytecodeBytes)
		code[0] = byte(runtime.YIELD)
		err := runtime.Verify(code, nil)
		var verifyError *runtime.VerifyError
		if !errors.As(err, &verifyError) || verifyError.Code != runtime.VerifyUnknownOpcode {
			t.Fatalf("exact-limit verify error = %v, want opcode validation", err)
		}

		err = runtime.Verify(append(code, 0), nil)
		if !errors.As(err, &verifyError) || verifyError.Code != runtime.VerifyProgramTooLarge {
			t.Fatalf("over-limit verify error = %v, want %s", err, runtime.VerifyProgramTooLarge)
		}
	})

	t.Run("Constants", func(t *testing.T) {
		constants := make([]value.Value, limits.MaxConstants+1)
		if err := runtime.Verify(nil, constants[:limits.MaxConstants]); err != nil {
			t.Fatalf("exact-limit constants were rejected: %v", err)
		}
		err := runtime.Verify(nil, constants)
		var verifyError *runtime.VerifyError
		if !errors.As(err, &verifyError) || verifyError.Code != runtime.VerifyConstantsTooLarge {
			t.Fatalf("over-limit verify error = %v, want %s", err, runtime.VerifyConstantsTooLarge)
		}
	})
}

func TestRuntimeEnergyBoundaryIsInclusive(t *testing.T) {
	program, err := runtime.NewProgram(
		[]byte{byte(runtime.PUSH), 0, 0, byte(runtime.RETURN)},
		[]value.Value{value.New(42)},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	budget := program.Profile().EncodedEnergy
	if budget == 0 {
		t.Fatal("energy boundary fixture has no encoded cost")
	}

	vm := runtime.New(program)
	vm.MaxEnergy = budget
	if result := vm.Run(); result.K == value.Invalid || result.Int() != 42 {
		t.Fatalf("exact energy budget failed: %#v", result)
	}
	if vm.Stats().Energy != budget {
		t.Fatalf("exact energy usage = %d, want %d", vm.Stats().Energy, budget)
	}

	vm.FastReset(program, nil)
	vm.MaxEnergy = budget - 1
	result := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok || diagnostic.Code != runtime.DiagnosticEnergyLimit {
		t.Fatalf("under-budget result = %#v, want %s", result, runtime.DiagnosticEnergyLimit)
	}
}

func TestRuntimeCallDepthCannotBeWidenedByFrameStorage(t *testing.T) {
	limits := runtime.Limits()
	bytecode, err := compiler.CompileSource(`
const recurse = (self) => self(self);
const result = recurse(recurse);
`)
	if err != nil {
		t.Fatal(err)
	}

	vm := runtime.New(bytecode.Program)
	if len(vm.Frames) != limits.MaxCallDepth {
		t.Fatalf("new VM frame storage = %d, want %d", len(vm.Frames), limits.MaxCallDepth)
	}
	vm.Frames = append(vm.Frames, make([]runtime.Frame, limits.MaxCallDepth)...)
	vm.MaxEnergy = limits.DefaultMaxEnergy
	result := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok || diagnostic.Code != runtime.DiagnosticStackOverflow {
		t.Fatalf("recursive result = %#v, want %s", result, runtime.DiagnosticStackOverflow)
	}
	if len(diagnostic.Stack) != limits.MaxCallDepth {
		t.Fatalf("diagnostic call depth = %d, want %d", len(diagnostic.Stack), limits.MaxCallDepth)
	}
	stats := vm.Stats()
	if stats.PeakFrameDepth != limits.MaxCallDepth || stats.StackDepth != 0 {
		t.Fatalf("recursive execution state = %+v", stats)
	}
}
