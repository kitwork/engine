package ui

import (
	"testing"

	"github.com/kitwork/engine/jit/hydrate"
)

// The first paint writes a value as the browser's kernel then writes it, which is JavaScript's
// String(v) (16/10/2026, the owner's decision that a .ui expression answers alike on both ends): a list
// its items joined by commas, an object "[object Object]", a lambda its source, null the word "null"
// when String makes it. Before, a list or an object printed nothing here and "1,2" in the browser, and
// the text changed as the page hydrated. CONTROL: a value that is not there still prints nothing, a
// number and a string as before.
func TestPageTextIsJavaScriptsString(t *testing.T) {
	html := render(t, `<content><text>[{{ [1, 2] }}]</text><text>[{{ { a: 1 } }}]</text><text>[{{ l }}]</text>`+
		`<text>[{{ (x) => x * 2 }}]</text><text>[{{ String(null) }}]</text><text>[{{ o.missing }}]</text>`+
		`<text>[{{ 2.5 }}]</text><text>[{{ 'chữ' }}]</text></content>`,
		map[string]any{"l": []any{1.0, []any{2.0, 3.0}}, "o": map[string]any{}})
	mustContain(t, html, "<p>[1,2]</p>", "<p>[[object Object]]</p>", "<p>[1,2,3]</p>", "<p>[(x) =&gt; x * 2]</p>",
		"<p>[null]</p>", "<p>[]</p>", "<p>[2.5]</p>", "<p>[chữ]</p>")
	// an attribute: one expression its value's text (stringify), text around it the interpolation's
	html = render(t, `<content><image value="/a.png?v={{ [1, 2] }}" alt="{{ { a: 1 } }}" /><text alt="{{ ['p', 'q'] }}">x</text></content>`, nil)
	mustContain(t, html, `src="/a.png?v=1,2"`, `alt="[object Object]"`, `aria-label="p,q"`)
}

// What the server hands the browser as a literal (a component's input, data-kitwork-scope) keeps an
// object's keys in the order the expression made them, so Object.keys reads alike on both ends, and a
// string the browser could not read back from the page's UTF-8 — a lone half of a UTF-16 pair, a carriage
// return the HTML parser would make a line feed — goes as an escape. CONTROL: a host map is written with
// its keys sorted, as before, and plain text as itself.
func TestLiteralKeepsAnObjectsOrderAndEscapesWhatHTMLWouldChange(t *testing.T) {
	eval := func(src string) any {
		t.Helper()
		ir, err := hydrate.Compile(src)
		if err != nil {
			t.Fatal(err)
		}
		v, err := hydrate.Eval(ir, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, row := range []struct {
		value any
		want  string
	}{
		{eval("{ b: 1, a: [2, { d: 3, c: 4 }] }"), "{'b': 1, 'a': [2, {'d': 3, 'c': 4}]}"},
		{eval("{ b: 1, '2': 2, '10': 3 }"), "{'2': 2, '10': 3, 'b': 1}"},
		{eval("'😀'[0] + '|' + 'a\\rb' + '\\0'"), `'\uD83D|a\rb\0'`},
		{eval("o.missing"), "[][0]"},
		{map[string]any{"b": 1.0, "a": 2.0}, "{'a': 2, 'b': 1}"},
		{"chữ 'x'", `'chữ \'x\''`},
	} {
		if got := literal(row.value); got != row.want {
			t.Errorf("literal(%#v) = %s, want %s", row.value, got, row.want)
		}
	}
}
