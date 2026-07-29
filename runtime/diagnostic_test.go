package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestEnergyFailureReturnsStructuredDiagnostic(t *testing.T) {
	program, err := NewProgram(
		[]byte{byte(JUMP), 0, 0},
		nil,
		[]int32{4, 4, 4},
	)
	if err != nil {
		t.Fatalf("new program: %v", err)
	}
	vm := New(program)
	vm.MaxEnergy = 1

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no structured diagnostic: %#v", result)
	}
	if diagnostic.Code != DiagnosticEnergyLimit {
		t.Fatalf("diagnostic code = %q, want %q", diagnostic.Code, DiagnosticEnergyLimit)
	}
	if diagnostic.Function != "<main>" || diagnostic.Line != 4 || diagnostic.IP != 0 {
		t.Fatalf("top location = %#v", diagnostic)
	}
	if len(diagnostic.Stack) != 1 || diagnostic.Stack[0].Function != "<main>" {
		t.Fatalf("stack = %#v, want one main frame", diagnostic.Stack)
	}
}

func TestDirectLambdaDiagnosticDoesNotInventMainFrame(t *testing.T) {
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
		return value.Value{K: value.Invalid, V: "direct failure"}
	})

	result := vm.ExecuteLambda(
		&value.Lambda{
			Address:    0,
			Name:       "task",
			SourceLine: 12,
			Program:    program,
		},
		nil,
	)
	diagnostic, ok := DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no structured diagnostic: %#v", result)
	}
	if len(diagnostic.Stack) != 1 || diagnostic.Stack[0].Function != "task" {
		t.Fatalf("stack = %#v, want one task frame", diagnostic.Stack)
	}
	if diagnostic.Line != 12 {
		t.Fatalf("diagnostic line = %d, want lambda source line 12 fallback", diagnostic.Line)
	}
}

func TestProgramMismatchReturnsStructuredDiagnostic(t *testing.T) {
	owner := mustProgram(t, []byte{byte(RETURN)}, nil)
	vm := New(mustProgram(t, []byte{byte(RETURN)}, nil))

	result := vm.ExecuteLambda(&value.Lambda{
		Address: 0,
		Name:    "foreign",
		Program: owner,
	}, nil)
	diagnostic, ok := DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no structured diagnostic: %#v", result)
	}
	if diagnostic.Code != DiagnosticProgramMismatch {
		t.Fatalf("diagnostic code = %q, want %q", diagnostic.Code, DiagnosticProgramMismatch)
	}
	if !strings.Contains(result.Text(), "different program") {
		t.Fatalf("compatibility text = %q", result.Text())
	}
}

func TestCancellationReturnsStructuredDiagnostic(t *testing.T) {
	program := mustProgram(t, []byte{byte(JUMP), 0, 0}, nil)
	vm := New(program)
	vm.MaxEnergy = 1_000_000
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	vm.Context = ctx

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no structured diagnostic: %#v", result)
	}
	if diagnostic.Code != DiagnosticCancelled {
		t.Fatalf("diagnostic code = %q, want %q", diagnostic.Code, DiagnosticCancelled)
	}
	if diagnostic.Function != "<main>" || diagnostic.IP != 0 {
		t.Fatalf("top location = %#v", diagnostic)
	}
}
