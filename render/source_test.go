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

// source(version) reveals the real component: the kernel module /kit.js ships for the named
// component, exactly as authored — what shows is what runs.
func TestSourceReturnsTheKernelComponent(t *testing.T) {
	base := t.TempDir()
	shell := `<main><pre>{{ component.source(version) }}</pre></main>`
	mkfile(t, base, "views/index.kitwork.html", shell)
	mkfile(t, base, "views/page.kitwork.html", "")
	show := func(component, version string) string {
		return newViews(base).Bind(value.New(map[string]any{"component": component, "version": version})).String()
	}

	// The catalogue writes versions without their "v". A module registers its bare name only: no
	// version travels on the wire, the version only picks which source file is read.
	out := show("tree", "1.0.0")
	if !strings.Contains(out, `window.kit.component(&#34;tree&#34;`) {
		t.Fatalf("tree@1.0.0 source not shown:\n%s", out)
	}
	if strings.Contains(out, `kit.component(&#34;tree@`) {
		t.Fatalf("the module must register its bare name only, no version:\n%s", out)
	}
	// No version is the latest.
	if !strings.Contains(show("tree", ""), `window.kit.component(&#34;tree&#34;`) {
		t.Fatalf("an empty version must resolve to the latest module")
	}
	// The version still picks the file: clipboard 1.0.0 and 2.0.0 are two sources, both registering
	// "clipboard", and no version is the latest of them.
	first, second, latest := show("clipboard", "1.0.0"), show("clipboard", "2.0.0"), show("clipboard", "")
	for _, source := range []string{first, second} {
		if !strings.Contains(source, `window.kit.component(&#34;clipboard&#34;`) || strings.Contains(source, `kit.component(&#34;clipboard@`) {
			t.Fatalf("each clipboard source registers the bare name:\n%.300s", source)
		}
	}
	if first == second {
		t.Fatal("control: clipboard 1.0.0 and 2.0.0 must be different sources")
	}
	if latest != second {
		t.Fatal("no version must be the latest clipboard (2.0.0)")
	}
	// Controls: an unknown component, an unknown version and a path in the name all yield nothing.
	for _, miss := range [][2]string{{"does-not-exist", "1.0.0"}, {"tree", "9.9.9"}, {"../hydrate/kernel", ""}, {"tree", "../../x"}} {
		if strings.Contains(show(miss[0], miss[1]), "kit.component") {
			t.Fatalf("%s@%s must resolve to empty source", miss[0], miss[1])
		}
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
