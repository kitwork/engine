package render

import (
	"testing"

	"github.com/kitwork/engine/value"
)

// A template reads members through value.Get, so an object's own key wins over the method of the
// same name here too: {{ step.text }} was empty on the pages that list steps until 13/10/2026,
// because "text" named the conversion method. An object without the key keeps the method.
func TestTemplateReadsAnObjectsOwnKeyBeforeAMethod(t *testing.T) {
	data := value.New(map[string]any{
		"step":  map[string]any{"text": "Đăng ký", "keys": "khoá"},
		"steps": []any{map[string]any{"text": "một"}, map[string]any{"text": "hai"}},
		"plain": map[string]any{"a": 1},
	})
	got := engineRender(`[{{ step.text }}|{{ step.keys }}|{{ for s in steps }}{{ s.text }};{{ end }}|{{ plain.has("a") }}]`, data, "", "")
	if want := "[Đăng ký|khoá|một;hai;|true]"; got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
}

// A template calls a method through value.Invoke, so data never answers a call here either: a body
// carrying "has" and "len" keys still gets the builtins from {{ body.has(…) }} and {{ body.len() }}.
// And an own error is data on a plain object: {{ if $.error }} shows the message a handler bound with
// ctx.bind({ error }), where it read null and showed nothing before 13/10/2026.
func TestTemplateCallsTheBuiltinPastOwnDataAndReadsOwnError(t *testing.T) {
	data := value.New(map[string]any{
		"error": "Không lưu được",
		"body":  map[string]any{"has": true, "len": 7, "name": "a"},
	})
	got := engineRender(`[{{ body.has("token") }}|{{ body.has("name") }}|{{ body.len() }}|{{ body.has }}|{{ if $.error }}{{ $.error }}{{ end }}]`, data, "", "")
	if want := "[false|true|3|true|Không lưu được]"; got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
}
