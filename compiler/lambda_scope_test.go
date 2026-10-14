package compiler

import (
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func lambdaScopeVM(t *testing.T, src string) *runtime.VM {
	t.Helper()
	prog, err := parseProgram(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := NewCompiler(src)
	if err := c.Compile(prog); err != nil {
		t.Fatalf("compile: %v", err)
	}
	bc, err := c.ByteCodeResult()
	if err != nil {
		t.Fatalf("program: %v", err)
	}
	vm := runtime.New(bc.Program)
	vm.MaxEnergy = 100_000_000
	if res := vm.Run(); res.K == value.Invalid {
		t.Fatalf("runtime error: %v", res.V)
	}
	return vm
}

// A Kitwork UI view hands the interface the names its body declared: const, var, and the functions
// it defines (which still reach the other names when called later).
func TestExecuteLambdaScopeReturnsDeclaredNames(t *testing.T) {
	vm := lambdaScopeVM(t, `
const view = (ctx) => {
  const product = { price: 29000 };
  var quantity = 2;
  function total() { return product.price * quantity; }
  if (ctx) { const inner = 1; }
};
`)
	view := vm.Vars["view"]
	if view.K != value.Func {
		t.Fatalf("view is %v, want a function", view.K)
	}
	result, locals := vm.ExecuteLambdaScope(view.V.(*value.Lambda), []value.Value{value.New(true)})
	if result.K == value.Invalid {
		t.Fatalf("view failed: %v", result.V)
	}
	if locals["product"].K != value.Map || locals["quantity"].N != 2 || locals["ctx"].K != value.Bool {
		t.Fatalf("missing declared names: %v", locals)
	}
	total, ok := locals["total"].V.(*value.Lambda)
	if !ok {
		t.Fatalf("total is %v, want a function", locals["total"].K)
	}

	// A later call reuses the frame slot: the handed-out names must survive it, and the function
	// must still read them.
	again := vm.ExecuteLambda(view.V.(*value.Lambda), []value.Value{value.New(false)})
	if again.K == value.Invalid {
		t.Fatalf("second run failed: %v", again.V)
	}
	if locals["quantity"].N != 2 || locals["product"].K != value.Map {
		t.Fatalf("handed-out names were wiped by the next call: %v", locals)
	}
	if got := vm.ExecuteLambda(total, nil); got.N != 58000 {
		t.Fatalf("total() = %v, want 58000", got.V)
	}
}

// A view that defines no function has no closure keeping its frame alive: the names must still be
// handed over, not recycled by the next call into the same frame slot.
func TestExecuteLambdaScopeSurvivesFrameReuse(t *testing.T) {
	vm := lambdaScopeVM(t, `const view = (n) => { const shown = n; };`)
	view := vm.Vars["view"].V.(*value.Lambda)
	_, first := vm.ExecuteLambdaScope(view, []value.Value{value.New(1)})
	vm.ExecuteLambda(view, []value.Value{value.New(2)})
	if first["shown"].N != 1 {
		t.Fatalf("first render's names were overwritten by the next call: shown = %v", first["shown"].V)
	}
}
