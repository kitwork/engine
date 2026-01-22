package engine

import (
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/vm"
)

func TestCompilerAndVM(t *testing.T) {
	source := `
		let price = 100;
		let tax = 0.1;
		let total = price * (1 + tax);
		total;
	`

	l := compiler.NewLexer(source)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()

	if len(p.Errors()) != 0 {
		t.Fatalf("Parser errors: %v", p.Errors())
	}

	c := compiler.NewCompiler()
	err := c.Compile(prog)
	if err != nil {
		t.Fatalf("Compiler error: %v", err)
	}

	bc := c.ByteCodeResult()
	machine := vm.NewVM(bc.Instructions, bc.Constants)
	result := machine.Run()

	expected := 110.0
	if result.Float() < expected-0.01 || result.Float() > expected+0.01 {
		t.Errorf("Expected result around %v, got %v", expected, result.Float())
	}
}
