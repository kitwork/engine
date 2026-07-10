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
		`<button data-kit-click="n = n + 1">+</button>` +
		`<span data-kit-show="n > 3">ok</span>` +
		`<form data-kit-validate="password.length >= 6"></form>` +
		`<input data-kit-model="name">` +
		`</section></body>`
	out := Render(in)

	// authored source attributes are the wire format — kept byte-for-byte
	for _, keep := range []string{
		`data-kit-text="n * qty"`,
		`data-kit-click="n = n + 1"`,
		`data-kit-show="n > 3"`,
		`data-kit-validate="password.length >= 6"`,
		`data-kit-model="name"`,
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("authored attribute must ride unchanged: %s\nout: %s", keep, out)
		}
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

// When the jit/js pass already inlined its verb bundle (whose core IS this same kernel), the
// hydrate pass must not add a second reference.
func TestRenderSkipsWhenKernelInlined(t *testing.T) {
	in := `<head><script data-kitwork-jit="js">/*kernel+verbs*/</script></head><body>` + marker +
		`<b data-kit-text="n">0</b></section></body>`
	out := Render(in)
	if strings.Contains(out, injectTag) {
		t.Error("kernel already inlined by jit/js — no /kit.js reference should be added")
	}
	if out != in {
		t.Errorf("page should be unchanged\n got: %s", out)
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

// STRICT PREFIX = ORIGIN: data-kit-* is the only authored source form; data-kitwork-* on a
// directive is engine-emitted IR — never compile-verified as source, but it still needs the
// runtime injected (the walker runs it).
func TestRenderStrictPrefixOrigin(t *testing.T) {
	// The verify regex must not treat the long prefix as source…
	if directiveRe.MatchString(`data-kitwork-click="n = n + 1"`) {
		t.Error("data-kitwork-click must NOT be matched as authored source")
	}
	if !directiveRe.MatchString(`data-kit-click="n = n + 1"`) {
		t.Error("data-kit-click must be matched as authored source")
	}
	// …and a page carrying ONLY a precompiled IR directive still gets the runtime.
	in := `<head></head><body>` + marker +
		`<button data-kitwork-click='["=","n",["+",["$","n"],["#",1]]]'>+</button>` +
		`</section></body>`
	out := Render(in)
	if strings.Count(out, injectTag) != 1 {
		t.Error("an IR-only page still needs the runtime injected")
	}
}

func TestRenderInjectsBeforeBodyWhenNoHead(t *testing.T) {
	in := `<body>` + marker + `<b data-kit-text="n">0</b></section></body>`
	out := Render(in)
	if strings.Index(out, injectTag) > strings.Index(out, "</body>") {
		t.Error("runtime should be injected before </body> when there is no head")
	}
}

// The runtime must ship BOTH halves: the tiny parser (data-kit-* source) and the IR walker
// (data-kitwork-* precompiled JSON — prefix IS the encoding, no -ir suffix) — and never eval.
func TestRuntimeEmbedded(t *testing.T) {
	rt := Runtime()
	if strings.Contains(rt, "-ir") {
		t.Error("the -ir suffix form is retired — the long prefix alone marks engine-emitted IR")
	}
	for _, want := range []string{
		"window.hydrate", "PREC", "function lex", "EventSource", "MutationObserver",
		// the unified kernel surfaces: boot guard, behavior registry, verb compat, delegated action
		"kitwork.runtime", "kitwork.behavior", "kitwork.components", "data-kitwork-action",
		// the absorbed drive: navigation fetch header, morph primitive, head reconcile, history,
		// the two-way lock against the legacy standalone file, and the swap lifecycle events
		"X-Kitwork-Hydrate", "kitwork.morph", "mergeHead", "popstate", "kitwork.hydrate",
		"kitwork:before-swap", "kitwork:load",
		// kernel overlays (progress bar, announcer) survive morph via the data-kitwork-ui marker
		"data-kitwork-ui", "kernelUI",
		// scopes: the boundary attribute, the resolver, and the page-scope opcode
		"data-kitwork-scope", "scopeFor", `"=$"`,
		// blueprint grammar: object/array/lambda/sequence/call ops + tools + boundary modes
		`"{}"`, `"[]"`, `"=>"`, `"call"`, "__kitLambda", "tryArrowParams", "boundaryScope", "kitwork.run",
		// registered components: register fn, activation attr, blueprint registry, method this-bind
		"kitwork.component", "data-kitwork-component", "seedComponent", "fn.apply(s, fargs)",
		// remember: persisted $ keys — register fn, declaration attr, storage key, load/persist
		"kitwork.remember", "data-kit-remember", "registerRememberedKey", "loadRemembered",
		"kitwork.platform", "kitwork.bridge", "kitwork.isNative",
		// $app capabilities (Native Bridge RFC v2): bridge-first with web fallback
		"kitwork.clipboard", `bridge.call("clipboard.write"`, "navigator.clipboard",
		"kitwork.camera", `bridge.call("camera.capture"`, "readAsDataURL",
		// data-kit-bind: object expression → attributes (grammar-safe registry directive)
		`selector("bind")`,
		// api: async JSON source — sync fn, activation attr, fetch + state→CSS lifecycle
		"kitwork.syncApi", "data-kit-api", `el.setAttribute("data-state", "loading")`,
		// live per-scope + component init() lifecycle hook
		"liveTarget", "function runInit", "st.scope.init",
		// sandbox: the blocklist that seals the Function-constructor / prototype-pollution escape
		"function blockedKey", "constructor",
	} {
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
