package runtime

import "testing"

// TestVMContract freezes the instruction contract: bytecode 3 (14/10/2026) is bytecode 2's 33 slots,
// unchanged, and seven wide forms after the reserved one.
func TestVMContract(t *testing.T) {
	if BytecodeVersion != 3 {
		t.Fatalf("BytecodeVersion = %d, want frozen bytecode 3", BytecodeVersion)
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
		{"_LIMIT", _LIMIT, 40},
	}
	for _, opcode := range opcodes {
		if opcode.got != opcode.want {
			t.Errorf("%s = %d, want frozen slot %d", opcode.name, opcode.got, opcode.want)
		}
	}

	const expectedInstructionSetChecksum = "f98baae0b65716b886b0786a06a9316ae7baf4ff7ab93b8cd87149a344df1678"
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

	// Each wide form is its instruction with a three-byte operand.
	for op := PUSH_WIDE; op < _LIMIT; op++ {
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
