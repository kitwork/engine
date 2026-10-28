package hydrate

import (
	"strings"
	"testing"
)

const marker = `<body data-kitwork-hydrate="v1.0.0">`

// A page that writes the grammar is a Kitwork page: it is checked, and jit/js Deliver gives it its
// JavaScript. The grammar is the eight grouped data-kitwork-* attributes the engine writes, and one
// hand-written attribute, data-kitwork-drag (a desktop window's title bar). The template pages'
// hand-written data-kit-* expressions bring nothing: the kernel stopped reading them on 26/10/2026,
// and this check on 27/10/2026. CONTROL: the same page in the grouped form is recognised.
func TestPagesWithTheGrammarAreRecognised(t *testing.T) {
	page := func(body string) string {
		return `<head><title>x</title></head><body>` + marker + body + `</section></body>`
	}
	if !UsesGrammar(page(`<b data-kitwork-bind="text: n * qty">0</b><button data-kitwork-event="click: n = n + 1">+</button>`)) {
		t.Error("a page with grouped bind and event directives must be recognised")
	}
	if !UsesGrammar(page(`<header data-kitwork-drag="true">x</header>`)) {
		t.Error("a page whose only directive is data-kitwork-drag needs the runtime for its drag file")
	}
	template := `<b data-kit-text="n * qty">0</b><button data-kit-click="n = n + 1">+</button>` +
		`<span data-kit-show="n > 3">ok</span><form data-kit-bind:disabled="sending"></form>` +
		`<input data-kit-model="name"><div data-kit-scope="n: 0"><p data-kit-for="i of items"></p></div>`
	if UsesGrammar(page(template)) {
		t.Error("a template page's data-kit-* expressions bring no runtime any more")
	}
	if found := authoringMistakes(page(`<b data-kit-text="n +">x</b>`)); len(found) != 0 {
		t.Errorf("a template page's data-kit-* expression is no longer checked, got %q", found)
	}
}

// data-kit-validate is gone (22/09, B answer): 0 sites wrote one, required/pattern/type give the
// same live feedback natively, and the SERVER half stays (ctx.validate, work/validate.go) because a
// browser can be told anything. The kernel's submit gate still blocks a form holding
// data-state="invalid" — it only reads that state now, whoever wrote it.
func TestValidateIsNoLongerAClientDirective(t *testing.T) {
	const authored = `data-kit-validate="password.length >= 6"`
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

func TestRenderNoDirectivesIsNoop(t *testing.T) {
	in := `<head></head><body>` + marker + `<div class="card">hello</div></section></body>`
	if UsesGrammar(in) {
		t.Error("a page with no directive is not a Kitwork page")
	}
}

// A malformed expression is still shipped as written (visible, greppable) — the server logs the
// compile error at render time; the client runtime simply skips what it cannot parse.
func TestRenderMalformedKeptAndStillInjects(t *testing.T) {
	in := `<head></head><body>` + marker + `<b data-kitwork-bind="text: n +">x</b></section></body>`
	if !UsesGrammar(in) {
		t.Error("page uses a directive, so the runtime should be injected")
	}
}

// ONE GRAMMAR (KITWORK_UI.md §11): the EIGHT grouped data-kitwork-* attributes — component, scope, bind,
// class, style, event, if, for — are what the engine writes for a Kitwork UI page (data-kit-*, what a
// person wrote by hand on a template page, went with those pages). Each of the eight alone makes a page
// use the grammar. Every other data-kitwork-* name stays inert: the transport markers the engine hangs on
// nearly every page (generated, layout, current, delay, transition) and the retired per-directive
// spellings (data-kitwork-click, data-kitwork-text — now pairs inside event and bind).
func TestRenderStrictPrefixOrigin(t *testing.T) {
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
		`<main data-kitwork-layout="view.ui"></main>`,
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
// The verb system (data-kit-action / data-kit-target, kitwork.fire, the compat surface) is gone too
// (22/09): behaviour is a component or an expression, transport is Drive.
func TestRuntimeEmbedded(t *testing.T) {
	rt := composedRuntime()
	if strings.Contains(rt, "-ir") {
		t.Error("the -ir suffix form is retired")
	}
	for _, gone := range []string{"kitwork.fire", "kitwork.behavior", "kitwork.action ", "components.action(", "[data-kit-action]", "data-kit-trigger", "window.hydrate = "} {
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
		`data-kitwork-click`, `data-kitwork-alias`, `data-kitwork-seed`,
		`data-kitwork-generated`, `data-kitwork-layout`, `data-kitwork-current`, `data-kitwork-delay`, `data-kitwork-transition`,
	} {
		if strings.Contains(kernelJS, gone) {
			t.Errorf("the kernel reads %s — only the eight grouped data-kitwork-* attributes are directives", gone)
		}
	}
	// data-kitwork-item / data-kitwork-key are not an author's: they are the markers a live for writes
	// on each row it makes, and reads back to reconcile (data-kit-item / data-kit-key until 26/10/2026).
	for _, marker := range []string{`node.setAttribute("data-kitwork-item"`, `node.setAttribute("data-kitwork-key"`} {
		if !strings.Contains(kernelJS, marker) {
			t.Errorf("the kernel no longer marks the rows of a for: %s is missing", marker)
		}
	}
	// The hand-written data-kit-* directives of template pages left the client on 26/10/2026: no part
	// of the runtime selects or reads one (a comment may still name it).
	for _, gone := range []string{`[data-kit-`, `"data-kit-`, `'data-kit-`} {
		if strings.Contains(rt, gone) {
			t.Errorf("the runtime still reads a hand-written directive: found %s", gone)
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
		"kitwork.runtime", "kitwork.component", "kitwork.components",
		// the composed Drive module: navigation fetch header, morph primitive, head reconcile, history,
		// the two-way lock against the legacy standalone file, and the swap lifecycle events
		"X-Kitwork-Hydrate", "kitwork.morph", "mergeHead", "popstate", "kitwork.hydrate",
		"kitwork:before-swap", "kitwork:load",
		// nodes the client made for itself (the transition style) survive morph:
		// remembered in memory (clientOwned), not by an attribute
		"clientOwned", "kernelUI",
		// scopes: the boundary attribute, the resolver, and the page-scope opcode
		"data-kitwork-scope", "scopeFor", `"=$"`,
		// blueprint grammar: object/array/lambda/sequence/call ops + tools + boundary modes
		`"{}"`, `"[]"`, `"=>"`, `"call"`, "function lambda(x, s)", "tryArrowParams", "boundaryScope", "kitwork.run",
		// registered components: register fn, activation attr, blueprint registry, method this-bind
		"kitwork.component", "data-kitwork-component", "seedComponent", ".test(fn) ? undefined : s, fargs)",
		// a lambda is a function a native method calls back, walking its tree in the calling scope; every
		// call counts against one depth (16/10/2026: the lambda was an object wrapped at each native call)
		"function call(fn, self, args)", "function invokeLambda", "calling || s",
		// a component registered after boot still paints
		"scheduleRender",
		"kitwork.platform", "kitwork.bridge", `Object.defineProperty(kitwork, "isNative"`,
		"Bridge.prototype.receive", "BRIDGE_TIMEOUT", "kitwork.destroy", "removeEventListener",
		// kit services (Native Bridge RFC): exact namespace grants, bridge-first with web fallback
		"kitwork.service", `kitwork.service("appearance"`, `kitwork.service("clipboard"`,
		`native.call("clipboard.writeText"`, "navigator.clipboard",
		`kitwork.service("camera"`, `native.call("camera.capture"`, "readAsDataURL",
		`kitwork.service("navigation"`, `kitwork.service("window"`, `kitwork.service("capabilities"`,
		// data-kitwork-bind: each pair's value → its attribute or property (writeBinding)
		`[data-kitwork-bind]`, `function writeBinding`,
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
		"kitwork.streams =", "kitwork.sync =", "kitwork.syncApi =",
		"[data-kit-api]", "reconcileHooks", "onReconcile", "onDestroy",
		// data-kit-error went on 02/10 the same way: nothing used it.
		"[data-kit-error]", "handlingError",
		// kitwork.dialog, and kitwork.theme / toggleTheme (the light/dark service's names before kitwork.appearance),
		// went on 26/10/2026 with the template pages' client.
		`kitwork.service("dialog"`, `kitwork.service("theme"`, `compatibility("toggleTheme"`,
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
