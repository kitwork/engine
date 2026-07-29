package runtime

import (
	"errors"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestInstructionSpecsCoverExecutableOpcodes(t *testing.T) {
	executable := []Opcode{
		PUSH, POP, LOAD, STORE, GET, DUP, BUILTIN,
		ADD, SUB, MUL, DIV, MOD,
		AND, OR, NOT,
		COMPARE, JUMP, TRUE, FALSE, ITER, HALT,
		MAKE, SET, MERGE,
		CALL, INVOKE, RETURN, DEFER, SPAWN, COMMIT,
	}

	for _, op := range executable {
		spec, ok := LookupInstruction(op)
		if !ok {
			t.Fatalf("opcode %d has no instruction spec", op)
		}
		if spec.Name == "" {
			t.Fatalf("opcode %d has an empty name", op)
		}
		if spec.Energy == 0 {
			t.Fatalf("%s has zero energy", spec.Name)
		}
		if Table[op] != spec.Energy {
			t.Fatalf("%s energy table = %d, spec = %d", spec.Name, Table[op], spec.Energy)
		}
	}

	for _, op := range []Opcode{LAMBDA, YIELD, _RESERVED} {
		if spec, ok := LookupInstruction(op); ok {
			t.Fatalf("unsupported opcode %d unexpectedly resolves as %s", op, spec.Name)
		}
	}
}

func TestVerifyRejectsMalformedBytecode(t *testing.T) {
	tests := []struct {
		name      string
		code      []byte
		constants []value.Value
		want      VerifyCode
	}{
		{
			name: "unknown opcode",
			code: []byte{255},
			want: VerifyUnknownOpcode,
		},
		{
			name: "reserved opcode",
			code: []byte{byte(_RESERVED)},
			want: VerifyUnknownOpcode,
		},
		{
			name: "truncated operand",
			code: []byte{byte(PUSH), 0},
			want: VerifyTruncatedOperand,
		},
		{
			name: "constant out of bounds",
			code: []byte{byte(PUSH), 0, 0, byte(RETURN)},
			want: VerifyConstantOutOfBounds,
		},
		{
			name:      "load requires string constant",
			code:      []byte{byte(LOAD), 0, 0, byte(RETURN)},
			constants: []value.Value{value.New(42)},
			want:      VerifyInvalidConstant,
		},
		{
			name: "jump into operand",
			code: []byte{
				byte(PUSH), 0, 0,
				byte(JUMP), 0, 1,
				byte(RETURN),
			},
			constants: []value.Value{value.New(1)},
			want:      VerifyInvalidJump,
		},
		{
			name:      "invalid lambda address",
			code:      []byte{byte(RETURN)},
			constants: []value.Value{value.New(&value.Lambda{Address: 1})},
			want:      VerifyInvalidLambda,
		},
		{
			name: "stack underflow",
			code: []byte{byte(POP), byte(RETURN)},
			want: VerifyStackUnderflow,
		},
		{
			name: "stack mismatch",
			code: []byte{
				byte(PUSH), 0, 0,
				byte(FALSE), 0, 9,
				byte(PUSH), 0, 0,
				byte(RETURN),
			},
			constants: []value.Value{value.New(true)},
			want:      VerifyStackMismatch,
		},
		{
			name: "invalid comparison mode",
			code: []byte{
				byte(PUSH), 0, 0,
				byte(PUSH), 0, 0,
				byte(COMPARE), 6,
				byte(RETURN),
			},
			constants: []value.Value{value.New(1)},
			want:      VerifyInvalidOperand,
		},
		{
			name: "invalid collection kind",
			code: []byte{
				byte(MAKE), 2,
				byte(RETURN),
			},
			want: VerifyInvalidOperand,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Verify(tt.code, tt.constants)
			if err == nil {
				t.Fatalf("Verify() succeeded, want %s", tt.want)
			}
			var verifyErr *VerifyError
			if !errors.As(err, &verifyErr) {
				t.Fatalf("error type = %T, want *VerifyError", err)
			}
			if verifyErr.Code != tt.want {
				t.Fatalf("code = %s, want %s (%v)", verifyErr.Code, tt.want, err)
			}
		})
	}
}

func TestVerifyAcceptsStructuredControlFlow(t *testing.T) {
	code := []byte{
		byte(PUSH), 0, 0,
		byte(FALSE), 0, 9,
		byte(JUMP), 0, 9,
		byte(RETURN),
	}
	if err := Verify(code, []value.Value{value.New(true)}); err != nil {
		t.Fatal(err)
	}
}

func FuzzVerifyNeverPanics(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{byte(RETURN)})
	f.Add([]byte{byte(PUSH), 0})
	f.Add([]byte{255, 255, 255})

	constants := []value.Value{
		value.New("name"),
		value.New(42),
		value.New(&value.Lambda{Address: 0}),
	}
	f.Fuzz(func(t *testing.T, code []byte) {
		_ = Verify(code, constants)
	})
}
