package hydrate

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed bridge.js
var bridgeJS string

//go:embed kernel.js
var kernelJS string

//go:embed morph.js
var morphJS string

//go:embed modules/native.js
var nativeJS string

//go:embed modules/storage.js
var storageJS string

//go:embed modules/web.js
var webJS string

//go:embed modules/component-loader.js
var componentLoaderJS string

//go:embed compat.js
var compatJS string

//go:embed drive.js
var driveJS string

//go:embed boot.js
var bootJS string

// Runtime returns the ordered client composition: bridge, kernel, modules, Morph, compatibility,
// Drive, then boot. It is identical for every tenant and served at one cacheable RuntimePath.
// Because the engine serves BOTH this runtime and the Go compiler from one codebase, the two ends
// version together — grammar sync is by construction, not by discipline.
func Runtime() string {
	return strings.Join([]string{
		bridgeJS,
		kernelJS,
		nativeJS,
		storageJS,
		webJS,
		componentLoaderJS,
		morphJS,
		compatJS,
		driveJS,
		bootJS,
	}, "\n")
}

// RuntimePath is the built-in, always-on route the runtime is served at, and what Render points
// pages at. It is NOT a per-page JIT artifact (unlike /jitcss, /jiticons, /jitfonts, which are
// scanned + emitted only-used) — it is the ONE static client kernel, identical bytes for every
// tenant, so it lives under a clean, cacheable public name instead of the /jit* namespace. Same
// file the @kitwork/kitjs package ships (see cmd/kitjs-dist). Distinct from the verb runtime's
// /jitjs (jit/js) — this is the expression engine.
const RuntimePath = "/kit.js"

// The root marker is how a page opts into hydrate. Only when it is present does Render touch the
// page — so static pages (and demos that show data-* as example text) are never affected.
//
// PREFIX CONVENTION (strict for expression directives): data-kit-* is the AUTHOR-written form —
// what a developer types, always SOURCE. data-kitwork-* is what the ENGINE emits: the
// data-kitwork-jit=* injected markers, and — on a directive (text/show/click/validate) — the
// precompiled IR (JSON). The prefix alone tells origin AND encoding; the old -ir suffix form and
// the data-kitwork-* source alias are gone. Non-expression attributes (model/live/scope/…) keep the
// long form as a deprecated read-alias in the kernel (they have no IR form, so nothing collides).
// The app root is the one place the full prefix is also permitted as a branding anchor
// (`<html data-kitwork-app>`), though `data-kit-app` is equally fine and is what the reference
// tenant uses.
const (
	rootMarker      = "data-kitwork-hydrate"
	rootMarkerShort = "data-kit-hydrate"
	appMarker       = "data-kitwork-app"
	appMarkerShort  = "data-kit-app"
)

// directiveRe matches an authored EXPRESSION directive — data-kit-<name>="<expr>" ONLY. The long
// prefix is engine-emitted IR, never authored source, so it must not be compile-verified here.
// Expressions use single-quoted string literals, so the value never contains a double quote.
var directiveRe = regexp.MustCompile(`data-kit-(text|show|click|validate|bind|class)="([^"]*)"`)

// presenceRe decides runtime INJECTION: authored data-kit-* forms (including the non-expression
// attributes — model is a plain scope key, live an SSE URL, scope/component a boundary — which need
// the runtime but must never be compile-verified), plus engine-emitted IR directives
// (data-kitwork-text|show|click|validate), which equally need the walker.
// (IR JSON contains double quotes, so an emitted IR attribute is single-quoted — accept both.)
var presenceRe = regexp.MustCompile(`data-kit-(?:text|show|click|validate|bind|class|model|live|scope|component|remember|api)="|data-kitwork-(?:text|show|click|validate|bind|class)=['"]`)

// The value is "runtime" (not "hydrate"): this IS the client runtime — the code calls itself
// kitwork.runtime, and it runs directives + reactivity + navigation, not just hydration. The
// data-kitwork-jit attribute stays (the namespace mergeHead/morph scans for engine-injected assets).
const injectTag = `<script data-kitwork-jit="runtime" src="` + RuntimePath + `" defer></script>`

// Render is the server pass for hydrate pages. THE WIRE SHIPS THE SOURCE: authored data-kit-*
// attributes ride to the client unchanged (readable DOM, smaller wire) and the client runtime
// parses them there — no eval, same grammar. What the server does here:
//
//  1. VERIFY — every expression is compiled with the Go compiler at render time, so a typo is
//     caught and logged on the server instead of failing silently in the browser.
//  2. DELIVER — inject the <script src="/kit.js"> reference once, only when the page actually
//     uses a directive.
//
// IR remains the engine's INTERNAL form (ctx.validate, go tests, analysis) and a RESERVED wire
// mode: if the engine ever emits a precompiled directive it is data-kitwork-<name>="[IR JSON]" —
// the prefix alone marks it (the kernel JSON.parses the long form; the -ir suffix is gone). Render
// does not emit it today. A page WITHOUT the marker (or with no directive) is returned
// byte-for-byte unchanged.
func Render(html string) string {
	if !strings.Contains(html, rootMarker) && !strings.Contains(html, rootMarkerShort) &&
		!strings.Contains(html, appMarker) && !strings.Contains(html, appMarkerShort) {
		return html
	}
	for _, m := range directiveRe.FindAllStringSubmatch(html, -1) {
		if _, err := Compile(m[2]); err != nil {
			fmt.Printf("[hydrate] %v — in %s\n", err, m[0])
			continue
		}
		// data-kit-class carries an extra obligation the other directives do not: the CSS JIT emits
		// only classes it can SEE, and it reads them off this expression's literals. A name built by
		// concatenation is invisible to it, so the class would resolve at runtime to a rule that was
		// never generated — styling that silently disappears for some values and works for others,
		// which is far harder to diagnose than an error. Say so at render, next to the markup.
		if m[1] == "class" && HasConstructedClass(m[2]) {
			fmt.Printf("[hydrate] class names must be written out in full — the CSS JIT cannot emit a "+
				"name built with '+', so this rule is never generated. Use a conditional between "+
				"complete names (color === 'red' ? 'text-red' : 'text-blue') — in %s\n", m[0])
		}
	}
	if !presenceRe.MatchString(html) {
		return html
	}
	// The jit/js pass runs earlier and points its only-used action/component set at this same
	// runtime route. When that combined asset is present, a second bare reference is duplication.
	if strings.Contains(html, `<script data-kitwork-jit="runtime" src="`+RuntimePath) {
		return html
	}
	if i := strings.LastIndex(html, "</head>"); i >= 0 {
		return html[:i] + injectTag + html[i:]
	}
	if i := strings.LastIndex(html, "</body>"); i >= 0 {
		return html[:i] + injectTag + html[i:]
	}
	return html + injectTag
}
