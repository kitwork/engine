package work

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// The names a .ui page reads are the view's and its file's as the source wrote them (14/10/2026, block
// scope): a const of a block, at the top of the file or in the view, and the object a loop makes for
// its closures are no names of the page; the view's own title wins over the file's.
func TestViewScopeHasNoBlockVariables(t *testing.T) {
	bytecode, err := compiler.CompileSource(`
const title = "file";
if (title) { const hidden = 1; }
const handlers = [];
for (const n of [1, 2]) { handlers.push(() => n); }
const view = (ctx) => {
  const title = "view";
  if (ctx) { const inner = 2; }
};`)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatal(returned.Text())
	}
	view := vm.Vars["view"].V.(*value.Lambda)
	result, locals := vm.ExecuteLambdaScope(view, []value.Value{value.New(true)})
	if result.IsInvalid() {
		t.Fatal(result.Text())
	}
	scope, err := viewScope(vm, view, locals)
	if err != nil {
		t.Fatal(err)
	}
	if scope["title"] != "view" {
		t.Fatalf("title = %v, want the view's", scope["title"])
	}
	if handlers, ok := scope["handlers"].([]any); !ok || len(handlers) != 2 {
		t.Fatalf("handlers = %#v", scope["handlers"])
	}
	for name := range scope {
		if strings.ContainsAny(name, "@#") || name == "hidden" || name == "inner" || name == "n" {
			t.Fatalf("the page reads %q: %v", name, scope)
		}
	}
}
