package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// Wide forms (bytecode 3, 14/10/2026): a jump in a program longer than 65,535 bytes carries a
// three-byte address, and a constant past index 65,535 a three-byte index. Each is its instruction
// with a wider operand: same stack effect, same energy, and the verifier refuses it where the
// two-byte form would do.

func wide(op Opcode, operand int) []byte {
	return []byte{byte(op), byte(operand >> 16), byte(operand >> 8), byte(operand)}
}

// wideJumpProgram jumps over `padding` unreachable HALTs with JUMP_WIDE, then branches with
// FALSE_WIDE (not taken), TRUE_WIDE (taken) and ITER_WIDE (an empty array, so it leaves), every
// address past 65,535, and returns 42. Constants: true, 42, "wrong", 0.
func wideJumpProgram(padding int) ([]byte, []value.Value) {
	constants := []value.Value{value.New(true), value.New(42), value.New("wrong"), value.New(0)}
	start := 4 + padding
	// start: PUSH true; FALSE_WIDE wrong; PUSH true; TRUE_WIDE array; HALT
	// array: MAKE 1; PUSH 0; ITER_WIDE answer; POP; POP; POP
	// answer: PUSH 42; RETURN
	// wrong: PUSH "wrong"; RETURN
	array := start + 3 + 4 + 3 + 4 + 1
	answer := array + 2 + 3 + 4 + 3
	wrong := answer + 3 + 1
	code := wide(JUMP_WIDE, start)
	for index := 0; index < padding; index++ {
		code = append(code, byte(HALT))
	}
	code = append(code, byte(PUSH), 0, 0)
	code = append(code, wide(FALSE_WIDE, wrong)...)
	code = append(code, byte(PUSH), 0, 0)
	code = append(code, wide(TRUE_WIDE, array)...)
	code = append(code, byte(HALT))
	code = append(code, byte(MAKE), 1, byte(PUSH), 0, 3)
	code = append(code, wide(ITER_WIDE, answer)...)
	code = append(code, byte(POP), byte(POP), byte(POP))
	code = append(code, byte(PUSH), 0, 1, byte(RETURN))
	code = append(code, byte(PUSH), 0, 2, byte(RETURN))
	return code, constants
}

func TestWideJumpsRunAndCostWhatTheirInstructionCosts(t *testing.T) {
	code, constants := wideJumpProgram(MaxNarrowOperand + 10)
	program := mustProgram(t, code, constants)
	vm := New(program)
	result := vm.Run()
	if result.K != value.Number || result.N != 42 {
		t.Fatalf("result = %s, want 42", result.Text())
	}
	// JUMP, PUSH, FALSE, PUSH, TRUE, MAKE, PUSH, ITER, PUSH, RETURN: each wide form costs as its
	// instruction does.
	want := Table[JUMP] + Table[PUSH] + Table[FALSE] + Table[PUSH] + Table[TRUE] +
		Table[MAKE] + Table[PUSH] + Table[ITER] + Table[PUSH] + Table[RETURN]
	if stats := vm.Stats(); stats.Energy != uint64(want) || stats.Instructions != 10 {
		t.Fatalf("energy %d over %d instructions, want %d over 10", stats.Energy, stats.Instructions, want)
	}

	profile := program.Profile()
	counted := map[string]int{}
	for _, opcode := range profile.Opcodes {
		counted[opcode.Name] = opcode.Count
	}
	if counted["JUMP_WIDE"] != 1 || counted["TRUE_WIDE"] != 1 || counted["FALSE_WIDE"] != 1 ||
		counted["ITER_WIDE"] != 1 || counted["JUMP"] != 0 || counted["ITER"] != 0 {
		t.Fatalf("the profile counts wide forms apart: %v", counted)
	}
}

func TestWideConstantIndexesReadPastTwoBytes(t *testing.T) {
	constants := make([]value.Value, MaxNarrowOperand+20)
	for index := range constants {
		constants[index] = value.New(index)
	}
	name := MaxNarrowOperand + 5
	constants[name] = value.New("answer")
	number := MaxNarrowOperand + 1
	var code []byte
	code = append(code, wide(PUSH_WIDE, number)...)
	code = append(code, wide(STORE_WIDE, name)...)
	code = append(code, byte(POP))
	code = append(code, wide(LOAD_WIDE, name)...)
	code = append(code, byte(RETURN))
	vm := New(mustProgram(t, code, constants))
	result := vm.Run()
	if result.K != value.Number || int(result.N) != number || int(vm.Vars["answer"].N) != number {
		t.Fatalf("result %s, answer %s, want %d", result.Text(), vm.Vars["answer"].Text(), number)
	}
	if stats := vm.Stats(); stats.Energy != uint64(Table[PUSH]+Table[STORE]+Table[POP]+Table[LOAD]+Table[RETURN]) {
		t.Fatalf("energy %d", stats.Energy)
	}
}

func TestVerifyRefusesAWideFormTheNarrowOneWouldDo(t *testing.T) {
	expect := func(t *testing.T, code []byte, constants []value.Value, want VerifyCode) {
		t.Helper()
		err := Verify(code, constants)
		var verifyError *VerifyError
		if want == "" {
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			return
		}
		if !errors.As(err, &verifyError) || verifyError.Code != want {
			t.Fatalf("verify error = %v, want %s", err, want)
		}
	}

	constants := make([]value.Value, MaxNarrowOperand+2)
	for index := range constants {
		constants[index] = value.New("name")
	}
	t.Run("a wide index at 65,535 is refused, at 65,536 it is read", func(t *testing.T) {
		for _, op := range []Opcode{PUSH_WIDE, LOAD_WIDE, STORE_WIDE} {
			prefix := []byte(nil)
			if op == STORE_WIDE {
				prefix = []byte{byte(PUSH), 0, 0}
			}
			at := func(index int) []byte {
				return append(append(append([]byte(nil), prefix...), wide(op, index)...), byte(RETURN))
			}
			expect(t, at(MaxNarrowOperand), constants, VerifyInvalidOperand)
			expect(t, at(MaxNarrowOperand+1), constants, "")
			expect(t, at(MaxNarrowOperand+2), constants, VerifyConstantOutOfBounds)
		}
	})

	// A jump to a HALT at target, in a program of length bytes. Written in two bytes the jump is one byte
	// shorter, so the HALT it goes to is at target - 1.
	jumpTo := func(op Opcode, length, target int) []byte {
		var code []byte
		switch op {
		case TRUE_WIDE, FALSE_WIDE:
			code = []byte{byte(PUSH), 0, 0}
		case ITER_WIDE:
			code = []byte{byte(MAKE), 1, byte(PUSH), 0, 0}
		}
		code = append(code, wide(op, target)...)
		if op == ITER_WIDE {
			// the iteration that goes on holds three values more: drop them
			code = append(code, byte(POP), byte(POP), byte(POP))
		}
		for len(code) < length-1 {
			code = append(code, byte(HALT))
		}
		return append(code, byte(RETURN))
	}

	t.Run("a wide jump in a program of 65,535 bytes is refused", func(t *testing.T) {
		for _, op := range []Opcode{JUMP_WIDE, TRUE_WIDE, FALSE_WIDE, ITER_WIDE} {
			expect(t, jumpTo(op, MaxNarrowOperand, MaxNarrowOperand-2), constants, VerifyInvalidOperand)
		}
	})

	// 15/10/2026: the length alone is not enough, as the compiler writes jumps wide only when the program
	// with two-byte jumps has an address two bytes cannot hold. CONTROL: one byte further, it is read.
	refusedFor := func(t *testing.T, code []byte, reason string) {
		t.Helper()
		var verifyError *VerifyError
		if err := Verify(code, constants); !errors.As(err, &verifyError) || verifyError.Code != VerifyInvalidOperand ||
			!strings.Contains(verifyError.Detail, reason) {
			t.Fatalf("verify error = %v, want %s: %s", err, VerifyInvalidOperand, reason)
		}
	}
	t.Run("a wide jump is read only where its two-byte program reaches past 65,535", func(t *testing.T) {
		for _, op := range []Opcode{JUMP_WIDE, TRUE_WIDE, FALSE_WIDE, ITER_WIDE} {
			long := MaxNarrowOperand + 10
			refusedFor(t, jumpTo(op, long, 20), "every address fits two bytes")
			refusedFor(t, jumpTo(op, long, MaxNarrowOperand+1), "every address fits two bytes")
			expect(t, jumpTo(op, long, MaxNarrowOperand+2), constants, "")
		}
	})

	// 15/10/2026: the compiler writes every jump of a program in one width. CONTROL: both wide, it is read.
	t.Run("two-byte and three-byte jumps in one program are refused", func(t *testing.T) {
		long := MaxNarrowOperand + 10
		mixed := append([]byte{byte(JUMP), 0, 3}, jumpTo(JUMP_WIDE, long-3, MaxNarrowOperand+5-3)...)
		copy(mixed[3:7], wide(JUMP_WIDE, MaxNarrowOperand+5))
		refusedFor(t, mixed, "two-byte and three-byte jumps")
		both := append(wide(JUMP_WIDE, 4), mixed[3:]...)
		copy(both[4:8], wide(JUMP_WIDE, MaxNarrowOperand+6))
		expect(t, both, constants, "")
	})

	t.Run("a wide jump into an operand or past the end is refused", func(t *testing.T) {
		code, padded := wideJumpProgram(MaxNarrowOperand + 10)
		inside := append([]byte(nil), code...)
		copy(inside[:4], wide(JUMP_WIDE, 2))
		expect(t, inside, padded, VerifyInvalidJump)
		past := append([]byte(nil), code...)
		copy(past[:4], wide(JUMP_WIDE, len(code)+1))
		expect(t, past, padded, VerifyInvalidJump)
		expect(t, wide(JUMP_WIDE, 0)[:3], nil, VerifyTruncatedOperand)
		expect(t, wide(PUSH_WIDE, 0)[:2], nil, VerifyTruncatedOperand)
	})

	t.Run("LOAD_WIDE and STORE_WIDE need a name", func(t *testing.T) {
		numbers := make([]value.Value, MaxNarrowOperand+2)
		for index := range numbers {
			numbers[index] = value.New(index)
		}
		expect(t, append(wide(LOAD_WIDE, MaxNarrowOperand+1), byte(RETURN)), numbers, VerifyInvalidConstant)
	})
}

func TestInspectShowsWideFormsWithTheirTargets(t *testing.T) {
	code, constants := wideJumpProgram(MaxNarrowOperand + 10)
	report, err := InspectProgram(mustProgram(t, code, constants))
	if err != nil {
		t.Fatal(err)
	}
	first := report.Instructions[0]
	start := 4 + MaxNarrowOperand + 10
	if first.Opcode != JUMP_WIDE || first.Name != "JUMP_WIDE" || first.NextIP != 4 ||
		len(first.Operands) != 1 || first.Operands[0] != uint32(start) ||
		first.Target == nil || *first.Target != start || first.TargetStack == nil || *first.TargetStack != 0 {
		t.Fatalf("first instruction = %+v", first)
	}
	last := report.Instructions[len(report.Instructions)-2]
	if last.Opcode != PUSH || last.Constant == nil || *last.Constant != 2 {
		t.Fatalf("the program's last PUSH = %+v", last)
	}
}
