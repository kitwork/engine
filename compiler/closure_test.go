package compiler

import (
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// runResult compiles + runs a single-file script and returns its top-level `result`.
func runResult(t *testing.T, src string) value.Value {
	t.Helper()
	prog, err := parseProgram(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := NewCompiler(src)
	if err := c.Compile(prog); err != nil {
		t.Fatalf("compile: %v", err)
	}
	bc := c.ByteCodeResult()
	vm := runtime.New(bc.Instructions, bc.Constants)
	vm.SourceMap = bc.SourceMap
	vm.MaxEnergy = 100_000_000
	res := vm.Run()
	if res.K == value.Invalid {
		t.Fatalf("runtime error: %v", res.V)
	}
	return vm.Vars["result"]
}

func wantNum(t *testing.T, got value.Value, want float64, msg string) {
	t.Helper()
	if got.K != value.Number || got.N != want {
		t.Fatalf("%s: got %v (kind %v), want %v", msg, got.V, got.K, want)
	}
}

// The exact regression: a closure over a returned function's local must survive a
// LATER call that reuses the same frame slot. Pre-fix, g() wiped make()'s frame map
// (shared by reference) and f() returned nil.
func TestClosureSurvivesFrameReuse(t *testing.T) {
	got := runResult(t, `
const make = () => { const x = 42; return () => x; };
const f = make();
const g = () => 99;
g();
const result = f();
`)
	wantNum(t, got, 42, "closure lost its captured local after frame reuse")
}
