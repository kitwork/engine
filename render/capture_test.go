package render

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// {{ capture name }} ... {{ end }} must render the region live AND bind `name`
// to that region's exact authored source, so a code panel can echo it escaped
// with zero drift. This is what makes the component catalog DRY.
func TestCaptureRendersLiveAndBindsEscapedSource(t *testing.T) {
	base := t.TempDir()
	shell := "<main>\n" +
		"{{ capture demo }}\n" +
		"    <div data-kit-component=\"switch@1.0.0\" data-kit-scope=\"checked: true\">\n" +
		"      <button data-kit-click=\"toggle()\">Toggle</button>\n" +
		"    </div>\n" +
		"{{ end }}\n" +
		"<pre>{{ demo }}</pre>\n" +
		"</main>"
	mkfile(t, base, "views/index.kitwork.html", shell)
	mkfile(t, base, "views/page.kitwork.html", "")
	out := newViews(base).Bind(value.New(map[string]any{})).String()

	// 1. The live demo is present as real, parseable markup (unescaped).
	if !strings.Contains(out, `<div data-kit-component="switch@1.0.0" data-kit-scope="checked: true">`) {
		t.Fatalf("live demo host missing from output:\n%s", out)
	}
	// 2. Exactly one live host — the demo renders once, not once per capture use.
	if got := strings.Count(out, `data-kit-component="switch@1.0.0"`); got != 1 {
		t.Fatalf("live host count = %d, want 1:\n%s", got, out)
	}
	// 3. The captured source is echoed HTML-escaped inside the <pre>.
	if !strings.Contains(out, `&lt;div data-kit-component=&#34;switch@1.0.0&#34;`) {
		t.Fatalf("escaped source missing from output:\n%s", out)
	}
	// 4. Dedented: the first source line starts at column 0, not the template's
	//    4-space nesting indent.
	if !strings.Contains(out, "<pre>&lt;div data-kit-component") {
		t.Fatalf("captured source was not dedented:\n%s", out)
	}
}

// Control: without a capture, the same {{ demo }} reference binds nothing, so no
// escaped source appears. This proves the capture block is what supplies it.
func TestCaptureControlWithoutBlockHasNoSource(t *testing.T) {
	base := t.TempDir()
	shell := "<main>\n" +
		"<div data-kit-component=\"switch@1.0.0\"></div>\n" +
		"<pre>{{ demo }}</pre>\n" +
		"</main>"
	mkfile(t, base, "views/index.kitwork.html", shell)
	mkfile(t, base, "views/page.kitwork.html", "")
	out := newViews(base).Bind(value.New(map[string]any{})).String()

	if strings.Contains(out, `&lt;div data-kit-component=&#34;switch`) {
		t.Fatalf("escaped source appeared without a capture block:\n%s", out)
	}
	if got := strings.Count(out, `data-kit-component="switch@1.0.0"`); got != 1 {
		t.Fatalf("live host count = %d, want 1:\n%s", got, out)
	}
}

// let must keep a multi-word string value intact (not truncate to the first
// token), so component pages can pass a lead paragraph to a recipe partial.
func TestLetKeepsMultiWordStringValue(t *testing.T) {
	base := t.TempDir()
	shell := `<main>{{ let lead = "A binary setting with several words." }}<p>{{ lead }}</p></main>`
	mkfile(t, base, "views/index.kitwork.html", shell)
	mkfile(t, base, "views/page.kitwork.html", "")
	out := newViews(base).Bind(value.New(map[string]any{})).String()
	if !strings.Contains(out, "<p>A binary setting with several words.</p>") {
		t.Fatalf("multi-word let value was truncated:\n%s", out)
	}
}

// {{ include }} was removed from the renderer in favor of the single @slot
// composition mechanism. A stray include must not inline a file (it is inert),
// while {{ layout }} and @slots keep working.
func TestIncludeDirectiveIsRemoved(t *testing.T) {
	base := t.TempDir()
	mkfile(t, base, "views/index.kitwork.html", `<main>A{{ include "../secret" }}B</main>`)
	mkfile(t, base, "views/page.kitwork.html", "")
	mkfile(t, base, "views/secret.html", `LEAKED`)
	out := newViews(base).Bind(value.New(map[string]any{})).String()
	if strings.Contains(out, "LEAKED") {
		t.Fatalf("include still inlined a file after removal:\n%s", out)
	}
}

// src.highlight() is an engine String method: it colors captured HTML source
// (data-kit-* in the brand class) and returns Raw HTML, so {{ src.highlight() }}
// needs no .html()/raw(). This also exercises method-call parsing in templates.
func TestHighlightStringMethodReturnsRawColoredSource(t *testing.T) {
	base := t.TempDir()
	shell := "<main>{{ capture src }}<a data-kit-click=\"go()\">x</a>{{ end }}<pre>{{ src.highlight() }}</pre></main>"
	mkfile(t, base, "views/index.kitwork.html", shell)
	mkfile(t, base, "views/page.kitwork.html", "")
	out := newViews(base).Bind(value.New(map[string]any{})).String()
	if !strings.Contains(out, `<span class="text-brand">data-kit-click</span>`) {
		t.Fatalf("data-kit-* directive not colored with the brand class:\n%s", out)
	}
	if strings.Contains(out, "&lt;span") {
		t.Fatalf("highlight output was escaped instead of Raw:\n%s", out)
	}
	// control: a plain string method that is not HTML stays escaped
	shell2 := `<main>{{ capture s }}<b>hi</b>{{ end }}<p>{{ s.upper() }}</p></main>`
	mkfile(t, base, "views/index.kitwork.html", shell2)
	out2 := newViews(base).Bind(value.New(map[string]any{})).String()
	if !strings.Contains(out2, "&lt;B&gt;HI&lt;/B&gt;") {
		t.Fatalf("upper() should stay escaped (not Raw):\n%s", out2)
	}
}
