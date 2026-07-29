package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

func TestRootAndLambdaShareCommitSemantics(t *testing.T) {
	code := []byte{
		byte(LOAD), 0, 0,
		byte(COMMIT),
		byte(RETURN),
	}
	program := mustProgram(t, code, []value.Value{value.NewString("effect")})

	rootEffect := &countingCommitter{}
	root := New(program)
	root.Globals["effect"] = value.New(rootEffect)
	rootResult := root.Run()

	lambdaEffect := &countingCommitter{}
	lambdaVM := New(program)
	lambdaVM.Globals["effect"] = value.New(lambdaEffect)
	lambdaResult := lambdaVM.ExecuteLambda(
		&value.Lambda{Address: 0, Program: program},
		nil,
	)

	if rootEffect.commits != 1 || lambdaEffect.commits != 1 {
		t.Fatalf("commit counts: root=%d lambda=%d", rootEffect.commits, lambdaEffect.commits)
	}
	if rootResult.V != rootEffect || lambdaResult.V != lambdaEffect {
		t.Fatalf("commit changed values: root=%T lambda=%T", rootResult.V, lambdaResult.V)
	}
	if lambdaVM.FrameIdx != 0 {
		t.Fatalf("lambda returned with frame index %d, want caller frame 0", lambdaVM.FrameIdx)
	}
}

func TestExecuteLambdaRestoresCallerOnHalt(t *testing.T) {
	program := mustProgram(t,
		[]byte{byte(PUSH), 0, 0, byte(HALT)},
		[]value.Value{value.New(42)},
	)
	vm := New(program)

	result := vm.ExecuteLambda(&value.Lambda{Address: 0, Program: program}, nil)
	if result.Int() != 42 {
		t.Fatalf("HALT result = %v, want 42", result.Interface())
	}
	if vm.FrameIdx != 0 {
		t.Fatalf("HALT left frame index %d, want caller frame 0", vm.FrameIdx)
	}
}

func TestExecuteLambdaRestoresCallerOnError(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
		},
		[]value.Value{value.NewString("fail")},
	)
	vm := New(program)
	vm.Globals["fail"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "expected failure"}
	})

	result := vm.ExecuteLambda(&value.Lambda{Address: 0, Program: program}, nil)
	if result.K != value.Invalid || !strings.Contains(result.Text(), "expected failure") {
		t.Fatalf("error result = %#v", result)
	}
	if vm.FrameIdx != 0 {
		t.Fatalf("error left frame index %d, want caller frame 0", vm.FrameIdx)
	}
}

func TestExecuteLambdaCancellationRestoresCaller(t *testing.T) {
	program := mustProgram(t, []byte{byte(JUMP), 0, 0}, nil)
	vm := New(program)
	vm.MaxEnergy = 1_000_000_000

	ctx, cancel := context.WithCancel(context.Background())
	vm.Context = ctx
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	result := vm.ExecuteLambda(&value.Lambda{Address: 0, Program: program}, nil)
	if result.K != value.Invalid || !strings.Contains(result.Text(), "Cancelled") {
		t.Fatalf("cancellation result = %#v", result)
	}
	if vm.FrameIdx != 0 {
		t.Fatalf("cancellation left frame index %d, want caller frame 0", vm.FrameIdx)
	}
}

func TestRootAndLambdaShareFuncObjectCalls(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(LOAD), 0, 0,
			byte(PUSH), 0, 1,
			byte(CALL), 1,
			byte(RETURN),
		},
		[]value.Value{value.NewString("factory"), value.New(21)},
	)
	factory := value.NewFuncObject(
		func(args ...value.Value) value.Value {
			return value.New(args[0].N * 2)
		},
		nil,
	)

	root := New(program)
	root.Globals["factory"] = factory
	rootResult := root.Run()

	lambdaVM := New(program)
	lambdaVM.Globals["factory"] = factory
	lambdaResult := lambdaVM.ExecuteLambda(
		&value.Lambda{Address: 0, Program: program},
		nil,
	)

	if rootResult.Int() != 42 || lambdaResult.Int() != 42 {
		t.Fatalf("FuncObject results: root=%v lambda=%v", rootResult.Interface(), lambdaResult.Interface())
	}
}

func TestRunRestoresRootAfterNestedError(t *testing.T) {
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 9,
			byte(LOAD), 0, 0,
			byte(CALL), 0,
			byte(RETURN),
			byte(PUSH), 0, 1,
			byte(CALL), 0,
			byte(RETURN),
		},
		[]value.Value{
			value.NewString("fail"),
			value.New(&value.Lambda{Address: 3}),
		},
	)
	vm := New(program)
	vm.Globals["fail"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "nested failure"}
	})

	result := vm.Run()
	if result.K != value.Invalid || !strings.Contains(result.Text(), "nested failure") {
		t.Fatalf("nested error result = %#v", result)
	}
	if vm.FrameIdx != 0 {
		t.Fatalf("nested error left frame index %d, want root frame 0", vm.FrameIdx)
	}
}
