package hydrate

import (
	"strings"
	"testing"
)

const marker = `<section data-kitwork-hydrate="v1.0.0">`

// THE WIRE SHIPS THE SOURCE: authored attributes ride unchanged; the engine verifies them and
// injects the runtime reference once.
func TestRenderKeepsSourceAndInjects(t *testing.T) {
	in := `<head><title>x</title></head><body>` + marker +
		`<b data-kit-text="n * qty">0</b>` +
		`<button data-kitwork-click="n = n + 1">+</button>` +
		`<span data-kit-show="n > 3">ok</span>` +
		`<form data-kit-validate="password.length >= 6"></form>` +
		`<input data-kit-model="name">` +
		`</section></body>`
	out := Render(in)

	// authored source attributes are the wire format — kept byte-for-byte
	for _, keep := range []string{
		`data-kit-text="n * qty"`,
		`data-kitwork-click="n = n + 1"`,
		`data-kit-show="n > 3"`,
		`data-kit-validate="password.length >= 6"`,
		`data-kit-model="name"`,
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("authored attribute must ride unchanged: %s\nout: %s", keep, out)
		}
	}
	// no IR is emitted by default (IR = internal/optional mode)
	if strings.Contains(out, "-ir='") {
		t.Errorf("default wire must not carry IR attributes\nout: %s", out)
	}
	// runtime injected once, inside <head>, pointing at the runtime route
	if n := strings.Count(out, injectTag); n != 1 {
		t.Errorf("expected runtime injected once, got %d", n)
	}
	if !strings.Contains(injectTag, RuntimePath) {
		t.Errorf("inject tag should reference %s", RuntimePath)
	}
	if strings.Index(out, injectTag) > strings.Index(out, "</head>") {
		t.Error("runtime should be injected before </head>")
	}
}

// The activation gate: a page WITHOUT the root marker is returned byte-for-byte unchanged, even if
// it contains directive-looking attributes (static pages, docs showing examples as text).
func TestRenderNoMarkerUntouched(t *testing.T) {
	in := `<body><b data-kit-text="n * qty">0</b><pre>data-kit-click="n = n + 1"</pre></body>`
	if out := Render(in); out != in {
		t.Errorf("no marker → must be untouched\n got: %s", out)
	}
}

func TestRenderNoDirectivesIsNoop(t *testing.T) {
	in := `<head></head><body>` + marker + `<div class="card">hello</div></section></body>`
	if out := Render(in); out != in {
		t.Errorf("marker but no directive should be unchanged\n got: %s", out)
	}
}

// A malformed expression is still shipped as authored (visible, greppable) — the server logs the
// compile error at render time; the client runtime simply skips what it cannot parse.
func TestRenderMalformedKeptAndStillInjects(t *testing.T) {
	in := `<head></head><body>` + marker + `<b data-kit-text="n +">x</b></section></body>`
	out := Render(in)
	if !strings.Contains(out, `data-kit-text="n +"`) {
		t.Error("malformed expression should ride unchanged")
	}
	if strings.Count(out, injectTag) != 1 {
		t.Error("page uses a directive, so the runtime should be injected")
	}
}

// live and model are not expressions — they must trigger runtime injection but never be
// compile-verified, and they ride the wire unchanged like everything else.
func TestRenderLiveAndModelInject(t *testing.T) {
	in := `<head></head><body>` + marker +
		`<div data-kit-live="/hydrate-engine/live"><b data-kit-model="x"></b></div>` +
		`</section></body>`
	out := Render(in)
	if !strings.Contains(out, `data-kit-live="/hydrate-engine/live"`) {
		t.Error("live attribute should ride unchanged")
	}
	if strings.Count(out, injectTag) != 1 {
		t.Error("a page with only live/model still needs the runtime injected")
	}
}

func TestRenderInjectsBeforeBodyWhenNoHead(t *testing.T) {
	in := `<body>` + marker + `<b data-kit-text="n">0</b></section></body>`
	out := Render(in)
	if strings.Index(out, injectTag) > strings.Index(out, "</body>") {
		t.Error("runtime should be injected before </body> when there is no head")
	}
}

// The runtime must ship BOTH halves: the tiny parser (source mode) and the IR walker (optional
// precompiled mode) — and never eval.
func TestRuntimeEmbedded(t *testing.T) {
	rt := Runtime()
	for _, want := range []string{"window.hydrate", "PREC", "function lex", "-ir", "EventSource", "MutationObserver"} {
		if !strings.Contains(rt, want) {
			t.Errorf("runtime.js missing %q", want)
		}
	}
	for _, forbid := range []string{"eval(", "new Function("} {
		if strings.Contains(rt, forbid) {
			t.Errorf("runtime.js must never use %q", forbid)
		}
	}
}
