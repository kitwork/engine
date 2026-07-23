package compiler

import (
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// The ONE case syntax can't catch: a counted loop whose bound grows inside the body
// (for (let i=0; i<arr.length; i++) { arr.push(i) }). MaxEnergy must terminate it — the gas
// backstop, not an infinite hang.
func TestForGrowingBoundKilledByEnergy(t *testing.T) {
	prog, err := parseProgram(`
		let arr = [1]
		for (let i = 0; i < arr.length; i++) { arr.push(i) }
		result = 1
	`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := NewCompiler("")
	if err := c.Compile(prog); err != nil {
		t.Fatalf("compile: %v", err)
	}
	bc := c.ByteCodeResult()
	vm := runtime.New(bc.Instructions, bc.Constants)
	vm.SourceMap = bc.SourceMap
	vm.MaxEnergy = 50_000 // low cap → must stop fast
	res := vm.Run()
	if res.K != value.Invalid {
		t.Fatalf("growing-bound loop should be killed by MaxEnergy, got %v", res.K)
	}
}
