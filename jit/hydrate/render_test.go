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

// data-kit-away (a click OUTSIDE) and data-kit-escape (the Escape key) are ordinary expression
// directives — the server twin must treat them exactly like data-kit-click: verify the expression,
// keep the source on the wire, and inject the runtime even on a page whose ONLY directive is one of
// them (the client dispatch cannot run without /kit.js). This is the server half of the event-
// modifier feature; if the render.go name lists ever drop them, this goes red.
func TestRenderAwayAndEscapeAreDirectives(t *testing.T) {
	// verify: an away/escape expression is recognised as authored source (so a typo is caught + logged).
	if !directiveRe.MatchString(`data-kit-away="open = false"`) {
		t.Error("data-kit-away must be verified as an authored expression directive")
	}
	if !directiveRe.MatchString(`data-kit-escape="open = false"`) {
		t.Error("data-kit-escape must be verified as an authored expression directive")
	}
	// injection: a page whose only directive is away (with a debounce/guard companion) still ships the
	// runtime, and the authored attributes ride unchanged.
	in := `<head></head><body>` + marker +
		`<div data-kit-away="open = false" data-kit-guard="prevent"><a data-kit-escape="open = false">x</a></div>` +
		`</section></body>`
	out := Render(in)
	for _, keep := range []string{
		`data-kit-away="open = false"`,
		`data-kit-escape="open = false"`,
		`data-kit-guard="prevent"`,
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("authored attribute must ride unchanged: %s", keep)
		}
	}
	if strings.Count(out, injectTag) != 1 {
		t.Error("a page whose only directive is away/escape still needs the runtime injected")
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

// A combined jit/js asset already carries the kernel, so Hydrate must not add a bare reference.
func TestRenderSkipsWhenRuntimeAssetExists(t *testing.T) {
	in := `<head><script data-kitwork-jit="runtime" src="/kit.js?components=dialog" defer></script></head><body>` + marker +
		`<b data-kit-text="n">0</b></section></body>`
	out := Render(in)
	if strings.Count(out, `src="/kit.js`) != 1 {
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
		"window.hydrate", "PREC", "function lex", "MutationObserver",
		// the unified kernel surfaces: boot guard, behavior registry, verb compat, delegated action
		"kit.runtime", "kit.behavior", "kit.components", "data-kitwork-action",
		// the composed Drive module: navigation fetch header, morph primitive, head reconcile, history,
		// the two-way lock against the legacy standalone file, and the swap lifecycle events
		"X-Kitwork-Hydrate", "kit.morph", "mergeHead", "popstate", "kit.hydrate",
		"kitwork:before-swap", "kitwork:load",
		// kernel overlays (progress bar, announcer) survive morph via the data-kitwork-ui marker
		"data-kitwork-ui", "kernelUI",
		// scopes: the boundary attribute, the resolver, and the page-scope opcode
		"data-kitwork-scope", "scopeFor", `"=$"`,
		// blueprint grammar: object/array/lambda/sequence/call ops + tools + boundary modes
		`"{}"`, `"[]"`, `"=>"`, `"call"`, "__kitLambda", "tryArrowParams", "boundaryScope", "kit.run",
		// registered components: register fn, activation attr, blueprint registry, method this-bind
		"kit.component", "data-kitwork-component", "seedComponent", "fn.apply(s, fargs)",
		// the capability seam remember (and later api/live) installs through, now that it is out of core
		"pageScope", "scheduleRender",
		"kit.platform", "kit.bridge", "kit.isNative",
		"Bridge.prototype.receive", "BRIDGE_TIMEOUT", "kit.destroy", "removeEventListener",
		// $app capabilities (Native Bridge RFC v2): bridge-first with web fallback
		"kit.clipboard", `native.call("clipboard.write"`, "navigator.clipboard",
		"kit.camera", `native.call("camera.capture"`, "readAsDataURL",
		// data-kit-bind: object expression → attributes (grammar-safe registry directive)
		`selector("bind")`,
		// an api element is still a core SCOPE boundary (the fetch that fills it is now the capability)
		"data-kitwork-api", "data-kit-api",
		// the reconcile/destroy lifecycle capabilities (api/live/remember) install through, out of core
		"reconcileHooks", "onReconcile", "onDestroy",
		// component init() lifecycle hook stays in core
		"function runInit", "st.scope.init",
		// sandbox: the blocklist that seals the Function-constructor / prototype-pollution escape
		"function blockedKey", "constructor",
	} {
		if !strings.Contains(rt, want) {
			t.Errorf("composed runtime missing %q", want)
		}
	}
	for _, forbid := range []string{"eval(", "new Function("} {
		if strings.Contains(rt, forbid) {
			t.Errorf("composed runtime must never use %q", forbid)
		}
	}
	// remember/api/live were lifted out of the always-shipped core into the jit/js capability channel:
	// their IMPLEMENTATIONS must be GONE from the core bytes (only pointer comments remain). If any of
	// them creeps back into the kernel, this catches it — that is the whole point of the extraction.
	for _, forbid := range []string{
		"registerRememberedKey", "function loadRemembered", "rememberStoragePrefix", "kitwork:remember:",
		"function syncApi", "function syncLive", "function liveTarget", "new EventSource",
		"kit.streams =", "kit.sync =", "kit.syncApi =",
	} {
		if strings.Contains(rt, forbid) {
			t.Errorf("capability implementation must not be in the core runtime: found %q", forbid)
		}
	}
}

func TestRuntimeCompositionOrder(t *testing.T) {
	runtime := Runtime()
	markers := []string{
		"Kitwork native bridge adapter",
		"Kitwork hydrate kernel",
		"Native-only capabilities",
		"Storage capability",
		"Browser-backed capabilities",
		"Optional remote component loader",
		"DOM morph module",
		"Compatibility surface",
		"Optional Kitwork Drive module",
		"Final composition step",
	}
	last := -1
	for _, marker := range markers {
		index := strings.Index(runtime, marker)
		if index < 0 {
			t.Fatalf("runtime module marker missing: %q", marker)
		}
		if index <= last {
			t.Fatalf("runtime module out of order: %q", marker)
		}
		last = index
	}
}
