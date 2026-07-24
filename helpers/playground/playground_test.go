package playground_test

import (
	"testing"

	"github.com/kitwork/engine/compiler"
	pg "github.com/kitwork/engine/helpers/playground"
	"github.com/kitwork/engine/value"
)

func TestFormatBytecodeAndConstants(t *testing.T) {
	bc := &compiler.Bytecode{
		Instructions: []byte{0x00, 0x00, 0x00, 0x13},
		Constants:    []value.Value{value.New(42)},
	}

	ops := pg.FormatBytecode(bc)
	if len(ops) < 2 {
		t.Fatalf("Expected at least 2 disassembly ops, got %d", len(ops))
	}

	consts := pg.FormatConstants(bc)
	if len(consts) != 1 {
		t.Fatalf("Expected 1 constant, got %d", len(consts))
	}
	if consts[0] != "[0] 42 (number)" {
		t.Errorf("Unexpected constant format: %s", consts[0])
	}
}
