package ui

import "testing"

// {{ x = $ }} puts the page's scope into itself. A component's input written from it (literal, the
// data-kitwork-scope the kernel seeds the instance from) writes the scope once, null where it comes
// back. Before 14/10/2026 it was written again to literal's depth bound, and each key back to itself
// doubled the text at every level: two keys made a 16 MB page in a second, three some 15 GB.
func TestLiteralOfAScopeThatHoldsItself(t *testing.T) {
	scope := map[string]any{"a": 1.0}
	scope["x"] = scope
	scope["y"] = scope
	if got := literal(scope); got != "{'a': 1, 'x': null, 'y': null}" {
		t.Errorf("literal of a scope in itself = %.200s", got)
	}
	list := []any{1.0, nil}
	list[1] = list
	if got := literal(list); got != "[1, null]" {
		t.Errorf("literal of a list in itself = %.200s", got)
	}
	// the same object twice is not in itself
	shared := map[string]any{"n": 1.0}
	if got := literal(map[string]any{"p": shared, "q": []any{shared, shared}}); got != "{'p': {'n': 1}, 'q': [{'n': 1}, {'n': 1}]}" {
		t.Errorf("literal of a shared object = %s", got)
	}

	// the page. JSON.stringify of a value that holds itself fails, as in JavaScript, and a text of more
	// than one part is one expression (16/10/2026), so the whole text is empty; the page is served
	page := `<content><text>{{ x = $ }}</text><text>{{ b = $ }}</text><text>[{{ JSON.stringify(b) }}]</text>` +
		`<box component="dropdown" open="{{ x }}"><button click="toggle()">x</button></box></content>`
	html := render(t, page, map[string]any{"a": 1.0})
	mustContain(t, html, `&#39;a&#39;: 1, &#39;b&#39;: null, `, `&#39;x&#39;: null}"`, `<p></p>`)
	if len(html) > 4096 {
		t.Fatalf("the page is %d bytes", len(html))
	}
}
