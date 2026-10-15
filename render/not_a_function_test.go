package render

import (
	"testing"

	"github.com/kitwork/engine/value"
)

type templateHost struct{}

func (templateHost) Greet(who string) string { return "chào " + who }

// A .kitwork.html template calls a method through value.Invoke, as the VM does after a dot, so a name
// the value has no function for is the same runtime error there (14/10/2026), and a template prints a
// runtime error where the value would go, as it prints every other: the host method called without its
// argument below is the control, an error of the kind a template already printed. The message names
// the call by the path the template wrote; a template has no place in a source to give. A method of
// a value that is not there is still null, as before, and a builtin still answers past data of its
// name.
func TestTemplatePrintsACallOfANonFunctionAsARuntimeError(t *testing.T) {
	data := value.New(map[string]any{
		"item": map[string]any{"n": 3, "name": "ab", "has": true},
		"host": value.New(templateHost{}),
	})
	got := engineRender(`[{{ item.n() }}|{{ item.name.upper() }}|{{ item.name.nope() }}|{{ host.greet() }}|{{ item.has("n") }}|{{ host.greet("Quốc") }}|{{ missing.x() }}]`, data, "", "")
	want := "[item.n không phải hàm (là số): không gọi được|AB|item.name.nope không phải hàm (là null): không gọi được|call greet: expected at least 1 arguments, got 0|true|chào Quốc|null]"
	if got != want {
		t.Fatalf("rendered\n%q\nwant\n%q", got, want)
	}
}

// A call further down a chain gets the failed call as its receiver and passes it on as it is, so the
// message names the call that failed once, by its own path. Until this was fixed every outer call put
// its path in front again: "item.n().upper().item.n().item.n không phải hàm …". A chain that does not
// fail is the control.
func TestTemplateNamesTheFailedCallOnceInAChain(t *testing.T) {
	data := value.New(map[string]any{"item": map[string]any{"n": 3, "name": "ab"}})
	got := engineRender(`[{{ item.n().upper() }}|{{ item.n().upper().trim() }}|{{ item.nope().upper() }}|{{ item.name.upper().trim() }}]`, data, "", "")
	want := "[item.n không phải hàm (là số): không gọi được|item.n không phải hàm (là số): không gọi được|item.nope không phải hàm (là null): không gọi được|AB]"
	if got != want {
		t.Fatalf("rendered\n%q\nwant\n%q", got, want)
	}
}
