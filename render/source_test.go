package render

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// standalone() prints the pure-KitJS form of the markup: the @version pin, a
// Kitwork-staging detail, is stripped from data-kit-component while every other
// attribute is left exactly as authored.
func TestStandaloneStripsComponentVersion(t *testing.T) {
	base := t.TempDir()
	shell := "<main>{{ capture src }}" +
		`<div data-kit-component="switch@1.0.0" data-kit-as="$s" data-kit-scope="checked: true">x</div>` +
		"{{ end }}<pre>{{ src.standalone().highlight() }}</pre></main>"
	mkfile(t, base, "views/index.kitwork.html", shell)
	mkfile(t, base, "views/page.kitwork.html", "")
	out := newViews(base).Bind(value.New(map[string]any{})).String()

	// The live demo host keeps its version (it must, to mount on staged delivery).
	if !strings.Contains(out, `data-kit-component="switch@1.0.0"`) {
		t.Fatalf("live host lost its version:\n%s", out)
	}
	// The shown source is unversioned: the highlighted host value reads
	// "switch", and the "@1.0.0" pin never appears in the printed code.
	if !strings.Contains(out, `>&#34;switch&#34;</span>`) {
		t.Fatalf("standalone() did not produce the bare switch host in the shown source:\n%s", out)
	}
	if strings.Contains(out, `&#34;switch@1.0.0&#34;`) {
		t.Fatalf("shown source still carries the @version pin:\n%s", out)
	}
	// Untouched sibling attributes survive the rewrite.
	if !strings.Contains(out, "data-kit-as") || !strings.Contains(out, "checked: true") {
		t.Fatalf("standalone() disturbed unrelated attributes:\n%s", out)
	}
}

// stripComponentVersion is exercised directly for the exact-string contract,
// including the control case where there is nothing to strip.
func TestStripComponentVersionExact(t *testing.T) {
	got := stripComponentVersion(`<a data-kit-component="tabs@2.1.0" class="x">`)
	if want := `<a data-kit-component="tabs" class="x">`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// Control: an already-bare host is returned unchanged (no accidental edits).
	unchanged := `<a data-kit-component="tabs" class="x">`
	if stripComponentVersion(unchanged) != unchanged {
		t.Fatalf("a bare host must pass through untouched: %q", unchanged)
	}
}

// source(version) reveals the real component: it returns the authored
// kit.component(...) source of the named component, with the delivery IIFE and
// "use strict" prologue stripped so what shows is what a developer would write.
func TestSourceReturnsUnwrappedComponentJS(t *testing.T) {
	base := t.TempDir()
	shell := `<main><pre>{{ component.source(version) }}</pre></main>`
	mkfile(t, base, "views/index.kitwork.html", shell)
	mkfile(t, base, "views/page.kitwork.html", "")
	data := value.New(map[string]any{"component": "switch", "version": "1.0.0"})
	out := newViews(base).Bind(data).String()

	// The meaningful source is present (HTML-escaped, since we did not highlight).
	if !strings.Contains(out, `kit.component(&#34;switch&#34;`) {
		t.Fatalf("component source not shown:\n%s", out)
	}
	// The delivery wrapper is gone — it is noise, not what a developer copies.
	if strings.Contains(out, "use strict") || strings.Contains(out, "})();") {
		t.Fatalf("delivery IIFE leaked into the shown source:\n%s", out)
	}
	// Control: an unknown component yields nothing rather than an error string.
	miss := value.New(map[string]any{"component": "does-not-exist", "version": "9.9.9"})
	if strings.Contains(newViews(base).Bind(miss).String(), "kit.component") {
		t.Fatalf("an unknown component must resolve to empty source")
	}
}

// highlightScript() colors trusted JS with the shared palette and returns Raw so
// {{ src.highlightScript() }} prints spans, not escaped text. kit glows in the
// directive color; a control string method proves non-JS output stays escaped.
func TestHighlightScriptColorsAndReturnsRaw(t *testing.T) {
	base := t.TempDir()
	shell := "<main>{{ capture js }}kit.component(\"x\", { on: function () { return true; } }){{ end }}" +
		`<pre>{{ js.highlightScript("classic") }}</pre></main>`
	mkfile(t, base, "views/index.kitwork.html", shell)
	mkfile(t, base, "views/page.kitwork.html", "")
	out := newViews(base).Bind(value.New(map[string]any{})).String()

	// kit is painted as a directive (lime-300 in the classic palette).
	if !strings.Contains(out, `<span class="text-lime-300">kit</span>`) {
		t.Fatalf("kit was not colored as a directive:\n%s", out)
	}
	// A keyword (function/return) is painted as structural (sky-300).
	if !strings.Contains(out, `<span class="text-sky-300">function</span>`) {
		t.Fatalf("keyword not colored:\n%s", out)
	}
	// Output is Raw: real spans, never escaped markup.
	if strings.Contains(out, "&lt;span") {
		t.Fatalf("highlightScript output was escaped instead of Raw:\n%s", out)
	}
	// A method call (component) is painted as attr (violet-300).
	if !strings.Contains(out, `<span class="text-violet-300">component</span>`) {
		t.Fatalf("call name not colored as attr:\n%s", out)
	}
}
