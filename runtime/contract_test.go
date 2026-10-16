package runtime

import "testing"

// TestVMContract freezes the instruction contract: bytecode 3 (14/10/2026) is bytecode 2's 33 slots,
// unchanged, and seven wide forms after the reserved one; bytecode 4 (15/10/2026) is bytecode 3's 40
// slots, unchanged, and STEP.
func TestVMContract(t *testing.T) {
	if BytecodeVersion != 4 {
		t.Fatalf("BytecodeVersion = %d, want frozen bytecode 4", BytecodeVersion)
	}
	if ProgramEncodingVersion != 1 {
		t.Fatalf("ProgramEncodingVersion = %d, want 1", ProgramEncodingVersion)
	}

	opcodes := []struct {
		name string
		got  Opcode
		want Opcode
	}{
		{"PUSH", PUSH, 0},
		{"POP", POP, 1},
		{"LOAD", LOAD, 2},
		{"STORE", STORE, 3},
		{"GET", GET, 4},
		{"DUP", DUP, 5},
		{"BUILTIN", BUILTIN, 6},
		{"ADD", ADD, 7},
		{"SUB", SUB, 8},
		{"MUL", MUL, 9},
		{"DIV", DIV, 10},
		{"AND", AND, 11},
		{"OR", OR, 12},
		{"NOT", NOT, 13},
		{"COMPARE", COMPARE, 14},
		{"JUMP", JUMP, 15},
		{"TRUE", TRUE, 16},
		{"FALSE", FALSE, 17},
		{"ITER", ITER, 18},
		{"HALT", HALT, 19},
		{"YIELD", YIELD, 20},
		{"MAKE", MAKE, 21},
		{"SET", SET, 22},
		{"MERGE", MERGE, 23},
		{"CALL", CALL, 24},
		{"INVOKE", INVOKE, 25},
		{"LAMBDA", LAMBDA, 26},
		{"RETURN", RETURN, 27},
		{"DEFER", DEFER, 28},
		{"SPAWN", SPAWN, 29},
		{"MOD", MOD, 30},
		{"COMMIT", COMMIT, 31},
		{"_RESERVED", _RESERVED, 32},
		{"PUSH_WIDE", PUSH_WIDE, 33},
		{"LOAD_WIDE", LOAD_WIDE, 34},
		{"STORE_WIDE", STORE_WIDE, 35},
		{"JUMP_WIDE", JUMP_WIDE, 36},
		{"TRUE_WIDE", TRUE_WIDE, 37},
		{"FALSE_WIDE", FALSE_WIDE, 38},
		{"ITER_WIDE", ITER_WIDE, 39},
		{"STEP", STEP, 40},
		{"_LIMIT", _LIMIT, 41},
	}
	for _, opcode := range opcodes {
		if opcode.got != opcode.want {
			t.Errorf("%s = %d, want frozen slot %d", opcode.name, opcode.got, opcode.want)
		}
	}

	const expectedInstructionSetChecksum = "ac0cab56f20c203825f2564a935756a8a3ce5110d86499a97fcaffc49f51b9da"
	if got := InstructionSetChecksum(); got != expectedInstructionSetChecksum {
		t.Errorf(
			"instruction contract checksum = %q, want %q",
			got,
			expectedInstructionSetChecksum,
		)
	}

	// The slots bytecode 2 had hash, with version 2, to bytecode 2's checksum: no name, operand width,
	// stack effect or energy of them changed.
	const bytecode2InstructionSetChecksum = "10872c964d1c5c284b8ec4fd1acc429e31568151dc03d17a77d1da76774c7e91"
	if got := instructionSetChecksum(2, int(PUSH_WIDE)); got != bytecode2InstructionSetChecksum {
		t.Errorf("bytecode 2's slots hash to %q, want bytecode 2's checksum %q", got, bytecode2InstructionSetChecksum)
	}

	// The slots bytecode 3 had hash, with version 3, to bytecode 3's checksum: STEP changed none of them.
	const bytecode3InstructionSetChecksum = "f98baae0b65716b886b0786a06a9316ae7baf4ff7ab93b8cd87149a344df1678"
	if got := instructionSetChecksum(3, int(STEP)); got != bytecode3InstructionSetChecksum {
		t.Errorf("bytecode 3's slots hash to %q, want bytecode 3's checksum %q", got, bytecode3InstructionSetChecksum)
	}

	// STEP takes the value it steps and gives the number, with a one-byte step, and costs what the
	// PUSH 1; ADD of x = x + 1 cost, so an update's energy did not move.
	step, ok := LookupInstruction(STEP)
	push, _ := LookupInstruction(PUSH)
	add, _ := LookupInstruction(ADD)
	if !ok || step.Name != "STEP" || len(step.OperandWidths) != 1 || step.OperandWidths[0] != 1 ||
		step.StackIn != 1 || step.StackOut != 1 || step.Energy != push.Energy+add.Energy {
		t.Fatalf("STEP = %+v, want one one-byte operand, 1 in, 1 out and the energy of PUSH and ADD", step)
	}
	if StepNumber != 0 || StepIncrement != 1 || StepDecrement != 2 {
		t.Fatalf("STEP's operands = %d, %d, %d, want frozen 0, 1, 2", StepNumber, StepIncrement, StepDecrement)
	}

	// Each wide form is its instruction with a three-byte operand.
	for op := PUSH_WIDE; op <= ITER_WIDE; op++ {
		wide, ok := LookupInstruction(op)
		narrow, narrowOK := LookupInstruction(op.Narrow())
		if !ok || !narrowOK || op.Narrow() == op || op.Narrow().Wide() != op || !op.IsWide() {
			t.Fatalf("opcode %d is not the wide form of an instruction", op)
		}
		if wide.Name != narrow.Name+"_WIDE" || len(wide.OperandWidths) != 1 || wide.OperandWidths[0] != 3 ||
			len(narrow.OperandWidths) != 1 || narrow.OperandWidths[0] != 2 ||
			wide.StackIn != narrow.StackIn || wide.StackOut != narrow.StackOut || wide.Energy != narrow.Energy {
			t.Fatalf("%s is not %s with a three-byte operand: %+v, %+v", wide.Name, narrow.Name, wide, narrow)
		}
	}
}
