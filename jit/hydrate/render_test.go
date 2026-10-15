package hydrate

import (
	"strings"
	"testing"
)

const marker = `<body data-kitwork-hydrate="v1.0.0">`

// A page that writes the grammar is a Kitwork page: it is checked, and jit/js Deliver gives it its
// JavaScript. (Rendering used to inject /kit.js here; the route went on 03/10.)
func TestPagesWithTheGrammarAreRecognised(t *testing.T) {
	in := `<head><title>x</title></head><body>` + marker +
		`<b data-kit-text="n * qty">0</b>` +
		`<button data-kit-click="n = n + 1">+</button>` +
		`<span data-kit-show="n > 3">ok</span>` +
		`<form data-kit-bind:disabled="sending"></form>` +
		`<input data-kit-model="name">` +
		`</section></body>`
	if !UsesGrammar(in) {
		t.Error("a page with text, click, show, bind and model directives must be recognised")
	}
}

// The event family — data-kit-<event>[:modifier…] (ideaship-final §2–§4) — is verified and injected
// like any expression directive: the expression compiles, the modifiers are the pipeline's, the
// source rides unchanged, and a page whose ONLY directive is one of them still ships the runtime.
// The old dedicated directives (data-kit-away / data-kit-escape) and companions (data-kit-guard)
// are no longer names the server knows: their jobs are :outside, :escape:window, :prevent.
// data-kit-seed brings the runtime and has its target checked at render: a key, a dotted path, or
// list[] — the client's rule, said on the server.
// data-kit-validate is gone (22/09, B answer): 0 sites wrote one, required/pattern/type give the
// same live feedback natively, and the SERVER half stays (ctx.validate, work/validate.go) because a
// browser can be told anything. The kernel's submit gate still blocks a form holding
// data-state="invalid" — it only reads that state now, whoever wrote it.
func TestValidateIsNoLongerAClientDirective(t *testing.T) {
	const authored = `data-kit-validate="password.length >= 6"`
	if directiveRe.MatchString(authored) {
		t.Error("data-kit-validate must not be verified as an expression directive any more")
	}
	if presenceRe.MatchString(authored) {
		t.Error("data-kit-validate must not bring the runtime on its own any more")
	}
	in := `<head></head><body>` + marker + `<form ` + authored + `></form></section></body>`
	if UsesGrammar(in) {
		t.Error("a page whose only data-kit-* attribute is the retired validate needs no runtime")
	}
	if strings.Contains(composedRuntime(), `selector("validate")`) || strings.Contains(composedRuntime(), `"data-kit-validate"`) {
		t.Error("the kernel must not carry the validate pass any more")
	}
	if !strings.Contains(composedRuntime(), `[data-state="invalid"]`) {
		t.Error("the submit gate must stay: it reads data-state, whoever wrote it")
	}
}

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
	if !UsesGrammar(in) {
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
	for _, gone := range []string{`data-kit-away="open = false"`, `data-kit-escape="open = false"`, `data-kit-error="failed = $error.message"`} {
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
	if !UsesGrammar(in) {
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
		if !UsesGrammar(in) {
			t.Errorf("a page whose only directive is for/if must inject the runtime: %s", only)
		}
	}
}

func TestRenderNoDirectivesIsNoop(t *testing.T) {
	in := `<head></head><body>` + marker + `<div class="card">hello</div></section></body>`
	if UsesGrammar(in) {
		t.Error("a page with no directive is not a Kitwork page")
	}
}

// A malformed expression is still shipped as authored (visible, greppable) — the server logs the
// compile error at render time; the client runtime simply skips what it cannot parse.
func TestRenderMalformedKeptAndStillInjects(t *testing.T) {
	in := `<head></head><body>` + marker + `<b data-kit-text="n +">x</b></section></body>`
	if !UsesGrammar(in) {
		t.Error("page uses a directive, so the runtime should be injected")
	}
}

// model is not an expression — it must trigger runtime injection but never be compile-verified, and
// it rides the wire unchanged like everything else.
func TestRenderModelInjects(t *testing.T) {
	in := `<head></head><body>` + marker +
		`<div><b data-kit-model="x"></b></div>` +
		`</section></body>`
	if !UsesGrammar(in) {
		t.Error("a page with only model still needs the runtime injected")
	}
}

// TWO SPELLINGS, ONE GRAMMAR (11/10/2026, KITWORK_UI.md §11): data-kit-* is what a person writes by
// hand, and the EIGHT grouped data-kitwork-* attributes — component, scope, bind, class, style, event,
// if, for — are what the engine writes for a Kitwork UI page. Each of the eight alone makes a page use
// the grammar. Every other data-kitwork-* name stays inert: the transport markers the engine hangs on
// nearly every page (generated, layout, current, delay, transition) and the retired per-directive
// spellings (data-kitwork-click, data-kitwork-text — now pairs inside event and bind).
func TestRenderStrictPrefixOrigin(t *testing.T) {
	if directiveRe.MatchString(`data-kitwork-click="n = n + 1"`) {
		t.Error("data-kitwork-click must NOT be matched as authored source: a grouped click is a pair of data-kitwork-event")
	}
	if !directiveRe.MatchString(`data-kit-click="n = n + 1"`) {
		t.Error("data-kit-click must be matched as authored source")
	}
	page := func(body string) string { return `<head></head><body>` + marker + body + `</section></body>` }
	for _, grouped := range []string{
		`<div data-kitwork-component="counter: $counter"></div>`,
		`<div data-kitwork-scope="n: 0"></div>`,
		`<b data-kitwork-bind="text: n">0</b>`,
		`<li data-kitwork-class="done: n == 100">x</li>`,
		`<div data-kitwork-style="width: n + '%'"></div>`,
		`<button data-kitwork-event="click: n = n + 1">+</button>`,
		`<p data-kitwork-if="n">x</p>`,
		`<li data-kitwork-for="row of rows; key: row.id">x</li>`,
	} {
		if !UsesGrammar(page(grouped)) {
			t.Errorf("a page whose only directive is %s uses the grammar and needs the runtime", grouped)
		}
	}
	for _, inert := range []string{
		`<button data-kitwork-click='["=","n",["+",["$","n"],["#",1]]]'>+</button>`,
		`<b data-kitwork-text="n">0</b>`,
		`<style data-kitwork-generated="css"></style>`,
		`<main data-kitwork-layout="page.ui"></main>`,
		`<a href="/" data-kitwork-current="page">x</a>`,
		`<div data-kitwork-delay="0"></div>`,
		`<div data-kitwork-transition="fade"></div>`,
	} {
		if UsesGrammar(page(inert)) {
			t.Errorf("%s is not one of the eight grouped directives and must not bring the runtime", inert)
		}
	}
}

// The runtime ships the tiny parser and the walker — and never eval. It reads two spellings of the
// same directives: data-kit-* by hand, and the eight grouped data-kitwork-* attributes the engine
// writes (KITWORK_UI.md §11), each found by its own selector. It never decodes a precompiled IR, and
// reads no other data-kitwork-* name: not the transport markers, not the retired per-directive
// spellings that the grouped form folded into bind / event / for / component. A class is its own
// grouped attribute (data-kitwork-class), so the bind reader no longer special-cases class.<name>.
// The verb system (data-kit-action / data-kit-target, kit.fire, the compat surface) is gone too
// (22/09): behaviour is a component or an expression, transport is Drive.
func TestRuntimeEmbedded(t *testing.T) {
	rt := composedRuntime()
	if strings.Contains(rt, "-ir") {
		t.Error("the -ir suffix form is retired")
	}
	for _, gone := range []string{"kit.fire", "kit.behavior", "kit.action ", "components.action(", "[data-kit-action]", "data-kit-trigger", "window.hydrate = "} {
		if strings.Contains(rt, gone) {
			t.Errorf("the runtime still carries the verb system: %q", gone)
		}
	}
	// the eight grouped attributes, the fixed-name ones by their selectors
	for _, read := range []string{
		`[data-kitwork-component]`, `[data-kitwork-scope]`, `[data-kitwork-bind]`, `[data-kitwork-event]`,
		`[data-kitwork-if]`, `[data-kitwork-for]`, `eachPair("bind"`, `eachPair("class"`, `eachPair("style"`,
		`grouped(b, "scope")`, `grouped(el, "event")`, `"data-kitwork-component"`, `"data-kitwork-if"`,
	} {
		if !strings.Contains(kernelJS, read) {
			t.Errorf("the kernel does not read the grouped form: %s is missing", read)
		}
	}
	for _, gone := range []string{
		`[data-kitwork-model]`, `data-kitwork-debounce`, `data-kitwork-text`, `data-kitwork-show`,
		`data-kitwork-click`, `data-kitwork-key`, `data-kitwork-alias`, `data-kitwork-seed`, `data-kitwork-item`,
		`data-kitwork-generated`, `data-kitwork-layout`, `data-kitwork-current`, `data-kitwork-delay`, `data-kitwork-transition`,
	} {
		if strings.Contains(kernelJS, gone) {
			t.Errorf("the kernel reads %s — only the eight grouped data-kitwork-* attributes are directives", gone)
		}
	}
	// class.<name> left data-kitwork-bind for data-kitwork-class: the bind reader must not keep a
	// special case for it.
	if strings.Contains(kernelJS, `indexOf("class.")`) {
		t.Error(`the kernel still special-cases class.<name> in data-kitwork-bind — a class is data-kitwork-class now`)
	}
	for _, want := range []string{
		"PREC", "function lex", "MutationObserver",
		// the unified kernel surfaces: boot guard, component registry
		"kit.runtime", "kit.component", "kit.components",
		// the composed Drive module: navigation fetch header, morph primitive, head reconcile, history,
		// the two-way lock against the legacy standalone file, and the swap lifecycle events
		"X-Kitwork-Hydrate", "kit.morph", "mergeHead", "popstate", "kit.hydrate",
		"kitwork:before-swap", "kitwork:load",
		// nodes the client made for itself (the transition style) survive morph:
		// remembered in memory (clientOwned), not by an attribute
		"clientOwned", "kernelUI",
		// scopes: the boundary attribute, the resolver, and the page-scope opcode
		"data-kit-scope", "scopeFor", `"=$"`,
		// blueprint grammar: object/array/lambda/sequence/call ops + tools + boundary modes
		`"{}"`, `"[]"`, `"=>"`, `"call"`, "function KitLambda", "tryArrowParams", "boundaryScope", "kit.run",
		// registered components: register fn, activation attr, blueprint registry, method this-bind
		"kit.component", "data-kit-component", "seedComponent", "fn.apply(s, callbacks(fargs, s))",
		// a lambda handed to a native method becomes a callback that walks its tree in the caller scope
		"function callbacks", "function invokeLambda",
		// a component registered after boot still paints
		"scheduleRender",
		"kit.platform", "kit.bridge", `Object.defineProperty(kit, "isNative"`,
		"Bridge.prototype.receive", "BRIDGE_TIMEOUT", "kit.destroy", "removeEventListener",
		// kit services (Native Bridge RFC): exact namespace grants, bridge-first with web fallback
		"kit.service", `kit.service("theme"`, `kit.service("clipboard"`,
		`native.call("clipboard.writeText"`, "navigator.clipboard",
		`kit.service("camera"`, `native.call("camera.capture"`, "readAsDataURL",
		`kit.service("navigation"`, `kit.service("window"`, `kit.service("capabilities"`,
		// data-kit-bind: object expression → attributes (grammar-safe registry directive)
		`data-kit-bind:`, `function writeBinding`,
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
	// remember/api/live were removed on 02/10, with the hooks they plugged into: none of it may come
	// back into the kernel by the side door.
	for _, forbid := range []string{
		"registerRememberedKey", "function loadRemembered", "rememberStoragePrefix", "kitwork:remember:",
		"function syncApi", "function syncLive", "function liveTarget", "new EventSource",
		"kit.streams =", "kit.sync =", "kit.syncApi =",
		"[data-kit-api]", "reconcileHooks", "onReconcile", "onDestroy",
		// data-kit-error went on 02/10 the same way: nothing used it.
		"[data-kit-error]", "handlingError",
	} {
		if strings.Contains(rt, forbid) {
			t.Errorf("a removed capability is back in the core runtime: found %q", forbid)
		}
	}
}

func TestRuntimeCompositionOrder(t *testing.T) {
	runtime := composedRuntime()
	markers := []string{
		"Kitwork native bridge adapter",
		"Kitwork hydrate kernel",
		"Native-only capabilities",
		"Origin-scoped storage service",
		"Browser-backed platform services",
		"DOM morph module",
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
