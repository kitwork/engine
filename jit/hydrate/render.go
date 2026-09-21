package hydrate

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
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
// PREFIX CONVENTION: data-kit-* is the AUTHOR-written form — what a developer types, always
// SOURCE, and the only form the kernel reads for a directive or a boundary. data-kitwork-* is
// what the ENGINE puts on the wire: the data-kitwork-jit=* injected markers, the jitjs verbs
// (action/target/trigger/drag) and the kernel-owned overlays (data-kitwork-ui). A precompiled IR
// directive (data-kitwork-text="[JSON]") was a reserved wire mode the engine never emitted; the
// kernel no longer decodes it (ideaship-final §9), so the long prefix on a directive is inert. The
// app root is the one place the full prefix is also permitted as a branding anchor
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
// An event handler is data-kit-<event>[:modifier…] (ideaship-final §2–§4); its modifiers are
// checked by checkEventModifiers, since the kernel disables a handler it cannot make sense of.
var directiveRe = regexp.MustCompile(`data-kit-(text|show|if|validate|error|bind:[a-z][a-z0-9-]*|class|(?:click|dblclick|submit|input|change|keydown|keyup|pointerdown|pointerup|focusin|focusout)(?::[a-z]+(?:\([0-9]+\))?)*)="([^"]*)"`)

// The event family and the modifier pipeline of ideaship-final §4, as the kernel runs it: target
// → filter → prevent → stop → timing → once. The author may write them in any order; the server
// names a modifier the kernel would refuse, so the mistake is seen at render, not lost in silence.
var eventTypes = map[string]bool{"click": true, "dblclick": true, "submit": true, "input": true, "change": true, "keydown": true, "keyup": true, "pointerdown": true, "pointerup": true, "focusin": true, "focusout": true}
var outsideEventTypes = map[string]bool{"click": true, "dblclick": true, "pointerdown": true, "pointerup": true, "focusin": true}
var timingModifierRe = regexp.MustCompile(`^(debounce|throttle)\(([0-9]+)\)$`)

func checkEventModifiers(directive string) error {
	parts := strings.Split(directive, ":")
	event := parts[0]
	if !eventTypes[event] {
		return nil
	}
	seen := map[string]bool{}
	timing := 0
	for _, modifier := range parts[1:] {
		name := modifier
		if m := timingModifierRe.FindStringSubmatch(modifier); m != nil {
			name = m[1]
			timing++
			if n, err := strconv.Atoi(m[2]); err != nil || n < 1 || n > 60000 {
				return fmt.Errorf("%s(%s): the delay is 1–60000 milliseconds", m[1], m[2])
			}
		}
		if seen[name] {
			return fmt.Errorf("modifier :%s is repeated", name)
		}
		seen[name] = true
		switch name {
		case "window", "document", "outside", "escape", "enter", "prevent", "stop", "once", "debounce", "throttle":
		default:
			return fmt.Errorf("unknown modifier :%s (the pipeline is :window :document → :outside :escape :enter → :prevent → :stop → :debounce(n) :throttle(n) → :once)", name)
		}
	}
	if (seen["escape"] || seen["enter"]) && event != "keydown" && event != "keyup" {
		return fmt.Errorf(":escape and :enter filter a key, so they belong on keydown/keyup, not %s", event)
	}
	if seen["escape"] && seen["enter"] {
		return fmt.Errorf(":escape and :enter cannot both be the filter")
	}
	if seen["outside"] && !outsideEventTypes[event] {
		return fmt.Errorf(":outside applies to click, dblclick, pointerdown, pointerup and focusin, not %s", event)
	}
	if seen["window"] && seen["document"] {
		return fmt.Errorf(":window and :document name two targets; pick one")
	}
	if timing > 1 {
		return fmt.Errorf(":debounce and :throttle cannot both time one handler")
	}
	return nil
}

// presenceRe decides runtime INJECTION: authored data-kit-* forms, including the non-expression
// attributes — model is a plain scope key, live an SSE URL, scope/component a boundary — which need
// the runtime but must never be compile-verified; for's value is a spec, not an expression. A
// data-kitwork-* directive is not authored and not read, so it does not bring the runtime.
// (remember/api/live are NOT here: they are no longer core directives — each is a jit/js capability,
// and that channel injects the runtime for a page that uses one. Those assets are the ONLY place the
// remember/api/live modules ship.)
var presenceRe = regexp.MustCompile(`data-kit-(?:text|show|if|for|validate|error|bind:[a-z][a-z0-9-]*|seed(?::[a-z][a-z0-9-]*)?|class|model|scope|component|(?:click|dblclick|submit|input|change|keydown|keyup|pointerdown|pointerup|focusin|focusout)(?::[a-z]+(?:\([0-9]+\))?)*)="`)

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
// IR remains the engine's INTERNAL form (ctx.validate, go tests, analysis); it is not a wire
// mode — the kernel parses source, and only source. A page WITHOUT the marker (or with no
// directive) is returned byte-for-byte unchanged.
func Render(html string) string {
	if !strings.Contains(html, rootMarker) && !strings.Contains(html, rootMarkerShort) &&
		!strings.Contains(html, appMarker) && !strings.Contains(html, appMarkerShort) {
		return html
	}
	for _, m := range directiveRe.FindAllStringSubmatch(html, -1) {
		expression := authoredAttribute(m[2])
		if _, err := Compile(expression); err != nil {
			fmt.Printf("[hydrate] %v — in %s\n", err, m[0])
			continue
		}
		if err := checkEventModifiers(m[1]); err != nil {
			fmt.Printf("[hydrate] %v — in %s\n", err, m[0])
			continue
		}
		// data-kit-class carries an extra obligation the other directives do not: the CSS JIT emits
		// only classes it can SEE, and it reads them off this expression's literals. A name built by
		// concatenation is invisible to it, so the class would resolve at runtime to a rule that was
		// never generated — styling that silently disappears for some values and works for others,
		// which is far harder to diagnose than an error. Say so at render, next to the markup.
		if m[1] == "class" && HasConstructedClass(expression) {
			fmt.Printf("[hydrate] class names must be written out in full — the CSS JIT cannot emit a "+
				"name built with '+', so this rule is never generated. Use a conditional between "+
				"complete names (color === 'red' ? 'text-red' : 'text-blue') — in %s\n", m[0])
		}
	}
	// A seed's value is a state target, not an expression; name a malformed one at render, as the
	// kernel would only report it in the browser.
	for _, m := range seedAttrRe.FindAllStringSubmatch(html, -1) {
		if err := seedTargetError(strings.TrimSpace(authoredAttribute(m[2]))); err != nil {
			fmt.Printf("[hydrate] %v — in %s\n", err, m[0])
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
