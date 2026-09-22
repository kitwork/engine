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

// The event family — data-kit-<event>[:modifier…] (ideaship-final §2–§4) — is verified and injected
// like any expression directive: the expression compiles, the modifiers are the pipeline's, the
// source rides unchanged, and a page whose ONLY directive is one of them still ships the runtime.
// The old dedicated directives (data-kit-away / data-kit-escape) and companions (data-kit-guard)
// are no longer names the server knows: their jobs are :outside, :escape:window, :prevent.
// data-kit-seed brings the runtime and has its target checked at render: a key, a dotted path, or
// list[] — the client's rule, said on the server.
func TestRenderSeedIsInjectedAndItsTargetChecked(t *testing.T) {
	for _, ok := range []string{"title", "user.email", "tags[]", "a.b.c"} {
		if err := seedTargetError(ok); err != nil {
			t.Errorf("seedTargetError(%q) = %v, want nil", ok, err)
		}
	}
	for target, reason := range map[string]string{"items[1]": "must be", "a b": "must be", "$title": "must be", "user.__proto__": "blocked"} {
		if err := seedTargetError(target); err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("seedTargetError(%q) = %v, want an error naming %q", target, err, reason)
		}
	}
	for _, authored := range []string{`data-kit-seed="title"`, `data-kit-seed:value="user.email"`} {
		if !presenceRe.MatchString(authored) {
			t.Errorf("%s must bring the runtime", authored)
		}
		if directiveRe.MatchString(authored) {
			t.Errorf("%s is a state target, not an expression to verify", authored)
		}
	}
	in := `<head></head><body>` + marker + `<h1 data-kit-seed="title">x</h1></section></body>`
	if out := Render(in); strings.Count(out, injectTag) != 1 {
		t.Error("a page whose only directive is a seed still needs the runtime")
	}
}

func TestRenderEventFamilyIsVerifiedAndInjected(t *testing.T) {
	for _, authored := range []string{
		`data-kit-click="open = false"`,
		`data-kit-click:outside="open = false"`,
		`data-kit-keydown:escape:window:prevent="open = false"`,
		`data-kit-input:debounce(250)="query = query.trim()"`,
		`data-kit-submit:prevent:once="saved = true"`,
		`data-kit-pointerdown:throttle(100):stop="drag = true"`,
		`data-kit-click:self="pick = true"`,
		`data-kit-error="failed = $error.message"`,
	} {
		m := directiveRe.FindStringSubmatch(authored)
		if m == nil {
			t.Errorf("%s must be verified as an authored expression directive", authored)
			continue
		}
		if err := checkEventModifiers(m[1]); err != nil {
			t.Errorf("%s: modifiers should be accepted, got %v", authored, err)
		}
	}
	for _, gone := range []string{`data-kit-away="open = false"`, `data-kit-escape="open = false"`} {
		if directiveRe.MatchString(gone) || presenceRe.MatchString(gone) {
			t.Errorf("%s is not a directive any more (its job is a modifier)", gone)
		}
	}
	// A modifier the kernel would disable is named at render.
	for directive, reason := range map[string]string{
		"click:mystery":                  "unknown modifier",
		"click:escape":                   "belong on keydown/keyup",
		"keydown:escape:enter":           "cannot both",
		"input:outside":                  ":outside applies to",
		"click:prevent:prevent":          "repeated",
		"click:debounce(10):throttle(5)": "cannot both time",
		"click:debounce(0)":              "1–60000",
		"click:window:document":          "pick one",
		"click:self:outside":             "cannot combine",
		"click:self:window":              "cannot combine",
	} {
		err := checkEventModifiers(directive)
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("checkEventModifiers(%q) = %v, want an error naming %q", directive, err, reason)
		}
	}
	// injection: a page whose only directive is a modified handler still ships the runtime, and the
	// authored attribute rides unchanged.
	in := `<head></head><body>` + marker +
		`<div data-kit-click:outside="open = false"><a data-kit-keydown:escape:window="open = false">x</a></div>` +
		`</section></body>`
	out := Render(in)
	for _, keep := range []string{
		`data-kit-click:outside="open = false"`,
		`data-kit-keydown:escape:window="open = false"`,
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("authored attribute must ride unchanged: %s", keep)
		}
	}
	if strings.Count(out, injectTag) != 1 {
		t.Error("a page whose only directive is a modified event handler still needs the runtime injected")
	}
}

// data-kit-if and data-kit-for are structural directives shipped in the client kernel; the server
// twin must know them too. `if`'s value is an expression → verified like text/show; `for`'s value is
// a spec ("item, i of items"), not a compilable expression → NOT verified, but both must still inject
// the runtime (a page whose only directive is for/if cannot hydrate without /kit.js). Regression here
// = a for/if-only page silently ships dead markup.
func TestRenderIfAndForInject(t *testing.T) {
	// if is a verified expression directive; for is not (its value is a spec).
	if !directiveRe.MatchString(`data-kit-if="open"`) {
		t.Error("data-kit-if must be verified as an expression directive")
	}
	if directiveRe.MatchString(`data-kit-for="item, i of items"`) {
		t.Error("data-kit-for value is a spec, not a compilable expression — must NOT be in directiveRe")
	}
	// both inject, even alone.
	for _, only := range []string{
		`<li data-kit-for="item, i of items"><b data-kit-text="item.name"></b></li>`,
		`<div data-kit-if="open">panel</div>`,
	} {
		in := `<head></head><body>` + marker + only + `</section></body>`
		if strings.Count(Render(in), injectTag) != 1 {
			t.Errorf("a page whose only directive is for/if must inject the runtime: %s", only)
		}
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

// STRICT PREFIX = ORIGIN: data-kit-* is the only authored source form. The long prefix on a
// directive is not source and, since the kernel stopped decoding IR (ideaship-final §9), not
// anything: it is neither verified nor a reason to ship the runtime.
func TestRenderStrictPrefixOrigin(t *testing.T) {
	if directiveRe.MatchString(`data-kitwork-click="n = n + 1"`) {
		t.Error("data-kitwork-click must NOT be matched as authored source")
	}
	if !directiveRe.MatchString(`data-kit-click="n = n + 1"`) {
		t.Error("data-kit-click must be matched as authored source")
	}
	for _, long := range []string{
		`data-kitwork-click='["=","n",["+",["$","n"],["#",1]]]'`,
		`data-kitwork-text="n"`,
		`data-kitwork-scope="cart"`,
	} {
		if presenceRe.MatchString(long) {
			t.Errorf("%s is inert and must not bring the runtime", long)
		}
	}
	in := `<head></head><body>` + marker +
		`<button data-kitwork-click='["=","n",["+",["$","n"],["#",1]]]'>+</button>` +
		`</section></body>`
	if out := Render(in); strings.Contains(out, injectTag) {
		t.Error("a page whose only directive wears the long prefix has no directive; nothing to hydrate")
	}
}

func TestRenderInjectsBeforeBodyWhenNoHead(t *testing.T) {
	in := `<body>` + marker + `<b data-kit-text="n">0</b></section></body>`
	out := Render(in)
	if strings.Index(out, injectTag) > strings.Index(out, "</body>") {
		t.Error("runtime should be injected before </body> when there is no head")
	}
}

// The runtime ships the tiny parser (data-kit-* source) and the walker — and never eval. It does
// not decode a precompiled IR any more: no data-kitwork-<directive> read remains in the kernel.
func TestRuntimeEmbedded(t *testing.T) {
	rt := Runtime()
	if strings.Contains(rt, "-ir") {
		t.Error("the -ir suffix form is retired")
	}
	for _, gone := range []string{
		`"data-kitwork-" + name`, `[data-kitwork-scope]`, `[data-kitwork-component]`, `[data-kitwork-for]`,
		`[data-kitwork-if]`, `[data-kitwork-model]`, `data-kitwork-debounce`,
	} {
		if strings.Contains(kernelJS, gone) {
			t.Errorf("the kernel still reads %s — the long prefix on a directive/boundary is inert now", gone)
		}
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
		"data-kit-scope", "scopeFor", `"=$"`,
		// blueprint grammar: object/array/lambda/sequence/call ops + tools + boundary modes
		`"{}"`, `"[]"`, `"=>"`, `"call"`, "__kitLambda", "tryArrowParams", "boundaryScope", "kit.run",
		// registered components: register fn, activation attr, blueprint registry, method this-bind
		"kit.component", "data-kit-component", "seedComponent", "fn.apply(s, fargs)",
		// the capability seam remember (and later api/live) installs through, now that it is out of core
		"pageScope", "scheduleRender",
		"kit.platform", "kit.bridge", `Object.defineProperty(kit, "isNative"`,
		"Bridge.prototype.receive", "BRIDGE_TIMEOUT", "kit.destroy", "removeEventListener",
		// kit services (Native Bridge RFC): exact namespace grants, bridge-first with web fallback
		"kit.service", `kit.service("theme"`, `kit.service("clipboard"`,
		`native.call("clipboard.writeText"`, "navigator.clipboard",
		`kit.service("camera"`, `native.call("camera.capture"`, "readAsDataURL",
		`kit.service("navigation"`, `kit.service("window"`, `kit.service("capabilities"`,
		// data-kit-bind: object expression → attributes (grammar-safe registry directive)
		`data-kit-bind:`, `function writeBinding`,
		// an api element is still a core SCOPE boundary (the fetch that fills it is now the capability)
		"data-kit-api",
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
		"Origin-scoped storage service",
		"Browser-backed platform services",
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
