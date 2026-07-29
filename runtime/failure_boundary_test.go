package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

type callbackCommitter struct {
	handler *value.Lambda
	calls   int
}

func (c *callbackCommitter) Commit() value.CommitResult {
	c.calls++
	return value.CommitResult{Handler: c.handler}
}

type panicCommitter struct{}

func (*panicCommitter) Commit() value.CommitResult {
	panic("commit exploded")
}

func TestCommitCallbackFailurePropagates(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 9,
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
			byte(LOAD), 0, 1,
			byte(COMMIT),
			byte(RETURN),
		},
		[]value.Value{
			value.NewString("fail"),
			value.NewString("effect"),
		},
	)
	effect := &callbackCommitter{
		handler: &value.Lambda{
			Address: 3,
			Name:    "afterCommit",
			Program: program,
		},
	}
	vm := New(program)
	vm.Globals["effect"] = value.New(effect)
	vm.Globals["fail"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "commit callback failed"}
	})

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no diagnostic: %#v", result)
	}
	if effect.calls != 1 {
		t.Fatalf("commit calls = %d, want 1", effect.calls)
	}
	if diagnostic.Code != DiagnosticRuntimeError ||
		diagnostic.Function != "afterCommit" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	if !strings.Contains(result.Text(), "commit callback failed") {
		t.Fatalf("result = %q", result.Text())
	}
}

func TestCommitPanicBecomesDiagnostic(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(LOAD), 0, 0,
			byte(COMMIT),
			byte(RETURN),
		},
		[]value.Value{value.NewString("effect")},
	)
	vm := New(program)
	vm.Globals["effect"] = value.New(&panicCommitter{})

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no diagnostic: %#v", result)
	}
	if diagnostic.Code != DiagnosticNativePanic {
		t.Fatalf("diagnostic code = %q, want %q", diagnostic.Code, DiagnosticNativePanic)
	}
	if !strings.Contains(result.Text(), "Native panic in COMMIT: commit exploded") {
		t.Fatalf("result = %q", result.Text())
	}
}

func TestNativeFunctionPanicBecomesDiagnostic(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
		},
		[]value.Value{value.NewString("explode")},
	)
	vm := New(program)
	vm.Globals["explode"] = value.NewFunc(func(...value.Value) value.Value {
		panic("host exploded")
	})

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no diagnostic: %#v", result)
	}
	if diagnostic.Code != DiagnosticNativePanic {
		t.Fatalf("diagnostic code = %q, want %q", diagnostic.Code, DiagnosticNativePanic)
	}
	if len(diagnostic.Stack) != 1 || diagnostic.Stack[0].Function != "<main>" {
		t.Fatalf("stack = %#v", diagnostic.Stack)
	}
	if !strings.Contains(result.Text(), "Native panic in function call: host exploded") {
		t.Fatalf("result = %q", result.Text())
	}
}

func TestDefersRunLIFO(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 15,
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
			byte(LOAD), 0, 1,
			byte(CALL), 0,
			byte(RETURN),
			byte(PUSH), 0, 2,
			byte(DEFER),
			byte(PUSH), 0, 3,
			byte(DEFER),
			byte(PUSH), 0, 4,
			byte(RETURN),
		},
		[]value.Value{
			value.NewString("first"),
			value.NewString("second"),
			value.New(&value.Lambda{Address: 3, Name: "firstCleanup"}),
			value.New(&value.Lambda{Address: 9, Name: "secondCleanup"}),
			value.New(42),
		},
	)
	var order []string
	vm := New(program)
	vm.Globals["first"] = value.NewFunc(func(...value.Value) value.Value {
		order = append(order, "first")
		return value.Value{K: value.Nil}
	})
	vm.Globals["second"] = value.NewFunc(func(...value.Value) value.Value {
		order = append(order, "second")
		return value.Value{K: value.Nil}
	})

	result := vm.Run()
	if result.Int() != 42 {
		t.Fatalf("result = %v, want 42", result.Interface())
	}
	if want := []string{"second", "first"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("defer order = %#v, want %#v", order, want)
	}
}

func TestDeferFailureIsSuppressedByOriginalFailure(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 9,
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
			byte(PUSH), 0, 1,
			byte(DEFER),
			byte(LOAD), 0, 2,
			byte(CALL), 0,
			byte(RETURN),
		},
		[]value.Value{
			value.NewString("cleanupFail"),
			value.New(&value.Lambda{Address: 3, Name: "cleanup"}),
			value.NewString("bodyFail"),
		},
	)
	cleanupCalls := 0
	vm := New(program)
	vm.Globals["cleanupFail"] = value.NewFunc(func(...value.Value) value.Value {
		cleanupCalls++
		return value.Value{K: value.Invalid, V: "cleanup failed"}
	})
	vm.Globals["bodyFail"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "body failed"}
	})

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no diagnostic: %#v", result)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
	if diagnostic.Message != "body failed" {
		t.Fatalf("primary message = %q, want body failed", diagnostic.Message)
	}
	if len(diagnostic.Suppressed) != 1 ||
		diagnostic.Suppressed[0].Message != "cleanup failed" {
		t.Fatalf("suppressed diagnostics = %#v", diagnostic.Suppressed)
	}
	if !strings.Contains(result.Text(), "suppressed: cleanup failed") {
		t.Fatalf("formatted result = %q", result.Text())
	}
}

func TestFailureUnwindsNestedFrames(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 25,
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
			byte(LOAD), 0, 1,
			byte(CALL), 0,
			byte(RETURN),
			byte(PUSH), 0, 2,
			byte(DEFER),
			byte(LOAD), 0, 3,
			byte(CALL), 0,
			byte(RETURN),
			byte(PUSH), 0, 4,
			byte(DEFER),
			byte(PUSH), 0, 5,
			byte(CALL), 0,
			byte(RETURN),
		},
		[]value.Value{
			value.NewString("innerCleanup"),
			value.NewString("outerCleanup"),
			value.New(&value.Lambda{Address: 3, Name: "innerCleanup"}),
			value.NewString("bodyFail"),
			value.New(&value.Lambda{Address: 9, Name: "outerCleanup"}),
			value.New(&value.Lambda{Address: 15, Name: "worker"}),
		},
	)
	var order []string
	vm := New(program)
	vm.Globals["innerCleanup"] = value.NewFunc(func(...value.Value) value.Value {
		order = append(order, "inner")
		return value.Value{K: value.Nil}
	})
	vm.Globals["outerCleanup"] = value.NewFunc(func(...value.Value) value.Value {
		order = append(order, "outer")
		return value.Value{K: value.Nil}
	})
	vm.Globals["bodyFail"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "worker failed"}
	})

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok || diagnostic.Message != "worker failed" {
		t.Fatalf("result = %#v", result)
	}
	if want := []string{"inner", "outer"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("defer order = %#v, want %#v", order, want)
	}
}

func TestEnergyFailureRunsDefers(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 9,
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
			byte(PUSH), 0, 1,
			byte(DEFER),
			byte(JUMP), 0, 13,
		},
		[]value.Value{
			value.NewString("cleanup"),
			value.New(&value.Lambda{Address: 3, Name: "cleanup"}),
		},
	)
	cleanupCalls := 0
	vm := New(program)
	vm.MaxEnergy = 30
	vm.Globals["cleanup"] = value.NewFunc(func(...value.Value) value.Value {
		cleanupCalls++
		return value.Value{K: value.Nil}
	})

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok || diagnostic.Code != DiagnosticEnergyLimit {
		t.Fatalf("energy result = %#v", result)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
	if vm.MaxEnergy != 30 {
		t.Fatalf("max energy = %d, want original limit 30", vm.MaxEnergy)
	}
}

func TestEnergyCleanupReserveStopsInfiniteDefer(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 6,
			byte(JUMP), 0, 3,
			byte(PUSH), 0, 0,
			byte(DEFER),
			byte(JUMP), 0, 10,
		},
		[]value.Value{
			value.New(&value.Lambda{Address: 3, Name: "loopingCleanup"}),
		},
	)
	vm := New(program)
	vm.MaxEnergy = 30

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok || diagnostic.Code != DiagnosticEnergyLimit {
		t.Fatalf("energy result = %#v", result)
	}
	if len(diagnostic.Suppressed) != 1 ||
		diagnostic.Suppressed[0].Code != DiagnosticEnergyLimit {
		t.Fatalf("suppressed diagnostics = %#v", diagnostic.Suppressed)
	}
	var largestInstructionCost uint64
	for _, cost := range Table {
		if uint64(cost) > largestInstructionCost {
			largestInstructionCost = uint64(cost)
		}
	}
	if vm.Energy > 30+cleanupEnergyReserve+2*largestInstructionCost {
		t.Fatalf("cleanup energy = %d, exceeded bounded reserve", vm.Energy)
	}
	if vm.MaxEnergy != 30 {
		t.Fatalf("max energy = %d, want original limit 30", vm.MaxEnergy)
	}
}

func TestCancellationRunsDefers(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 9,
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
			byte(PUSH), 0, 1,
			byte(DEFER),
			byte(JUMP), 0, 13,
		},
		[]value.Value{
			value.NewString("cleanup"),
			value.New(&value.Lambda{Address: 3, Name: "cleanup"}),
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cleanupCalls := 0
	vm := New(program)
	vm.Context = ctx
	vm.Globals["cleanup"] = value.NewFunc(func(...value.Value) value.Value {
		cleanupCalls++
		return value.Value{K: value.Nil}
	})

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok || diagnostic.Code != DiagnosticCancelled {
		t.Fatalf("cancel result = %#v", result)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
}

func TestHaltRunsDefers(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 9,
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
			byte(PUSH), 0, 1,
			byte(DEFER),
			byte(PUSH), 0, 2,
			byte(HALT),
		},
		[]value.Value{
			value.NewString("cleanup"),
			value.New(&value.Lambda{Address: 3, Name: "cleanup"}),
			value.New(42),
		},
	)
	cleanupCalls := 0
	vm := New(program)
	vm.Globals["cleanup"] = value.NewFunc(func(...value.Value) value.Value {
		cleanupCalls++
		return value.Value{K: value.Nil}
	})

	result := vm.Run()
	if result.Int() != 42 {
		t.Fatalf("result = %v, want 42", result.Interface())
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
}
