package runtime

import "testing"

func TestVMV2Contract(t *testing.T) {
	if BytecodeVersion != 2 {
		t.Fatalf("BytecodeVersion = %d, want frozen VM v2", BytecodeVersion)
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
		{"_LIMIT", _LIMIT, 33},
	}
	for _, opcode := range opcodes {
		if opcode.got != opcode.want {
			t.Errorf("%s = %d, want frozen slot %d", opcode.name, opcode.got, opcode.want)
		}
	}

	const expectedInstructionSetChecksum = "10872c964d1c5c284b8ec4fd1acc429e31568151dc03d17a77d1da76774c7e91"
	if got := InstructionSetChecksum(); got != expectedInstructionSetChecksum {
		t.Fatalf(
			"instruction contract checksum = %q, want %q",
			got,
			expectedInstructionSetChecksum,
		)
	}
}
