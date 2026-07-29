package playground_test

import (
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	pg "github.com/kitwork/engine/utilities/playground"
	"github.com/kitwork/engine/value"
)

func TestFormatBytecodeAndConstants(t *testing.T) {
	program, err := runtime.NewProgram(
		[]byte{byte(runtime.PUSH), 0, 0, byte(runtime.HALT)},
		[]value.Value{value.New(42)},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	bc := &compiler.Bytecode{
		Program: program,
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
