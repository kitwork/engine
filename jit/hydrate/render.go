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

//go:embed drive.js
var driveJS string

//go:embed boot.js
var bootJS string

// Runtime returns the ordered client composition: bridge, kernel, modules, Morph, Drive, then
// boot. It is identical for every tenant and served at one cacheable RuntimePath.
// Because the engine serves BOTH this runtime and the Go compiler from one codebase, the two ends
// version together — grammar sync is by construction, not by discipline.
func Runtime() string {
	return RuntimeWith("")
}

// RuntimeWith is the runtime plus the page's own modules — components and capabilities — and the
// modules go in BEFORE the boot shim, on purpose.
//
// boot.js starts the kernel, and the kernel's first pass is a real paint: it evaluates every
// expression on the page. A component that registers after that pass misses it, so for one frame
// each of its bindings is read against a boundary with no scope — and a name nothing declares
// reads as 0. The page shows "0" where a label belongs, and an `src` bound that way makes the
// browser fetch a URL named "0" before the correction arrives. Serving the modules first costs
// nothing and makes boot.js's own promise — "all modules are registered before the kernel starts"
// — true.
func RuntimeWith(modules string) string {
	parts := []string{
		bridgeJS,
		kernelJS,
		nativeJS,
		storageJS,
		webJS,
		morphJS,
		driveJS,
	}
	if strings.TrimSpace(modules) != "" {
		parts = append(parts, modules)
	}
	return strings.Join(append(parts, bootJS), "\n")
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
// PREFIX CONVENTION, restated 24/09 when the root marker went away.
//
//	data-kit-*      the GRAMMAR. What an author writes — and also what the engine writes when it
//	                emits a piece of the grammar itself, as data-kit-item does on a materialised
//	                row. The test is what the attribute MEANS, not who typed it: an item is a
//	                boundary, so it reads like one, next to the data-kit-for and data-kit-key it
//	                belongs with.
//
//	data-kitwork-*  TRANSPORT. Things the engine hangs on a page that are not part of the grammar:
//	                data-kitwork-jit=* asset markers, data-kitwork-drive (the site's answer to
//	                router.drive()), data-kitwork-ui overlays, data-kitwork-layout. An author never
//	                writes one, and the kernel never reads one as a directive.
//
// A precompiled IR directive (data-kitwork-text="[JSON]") was a reserved wire mode the engine never
// emitted; the kernel no longer decodes it (ideaship-final §9), so a long prefix on a directive is
// inert. The root marker (data-kit-app / data-kit-hydrate) is gone entirely: a page is recognised by
// its grammar, and Drive is switched on by router.drive().

// directiveRe matches an authored EXPRESSION directive — data-kit-<name>="<expr>" ONLY. The long
// prefix is engine-emitted IR, never authored source, so it must not be compile-verified here.
// Expressions use single-quoted string literals, so the value never contains a double quote.
// An event handler is data-kit-<event>[:modifier…] (ideaship-final §2–§4); its modifiers are
// checked by checkEventModifiers, since the kernel disables a handler it cannot make sense of.
var directiveRe = regexp.MustCompile(`data-kit-(text|show|if|error|bind:[a-z][a-z0-9-]*|style:[-a-zA-Z0-9_]+|class|(?:click|dblclick|submit|input|change|keydown|keyup|pointerdown|pointerup|focusin|focusout)(?::[a-z0-9+]+(?:\([0-9]+\))?)*)="([^"]*)"`)

// The event family and the modifier pipeline of ideaship-final §4, as the kernel runs it: target
// → filter → prevent → stop → timing → once. The author may write them in any order; the server
// names a modifier the kernel would refuse, so the mistake is seen at render, not lost in silence.
// comboModifierRe matches a key-combination modifier — `mod+k`, `mod+shift+p`, `alt+enter`. The
// `+` is what tells it apart from an ordinary modifier like :prevent.
var comboModifierRe = regexp.MustCompile(`^[a-z0-9]+(?:\+[a-z0-9]+)+$`)

// comboModifierKeys are the modifier words a combination may name. "mod" is Control on Windows and
// Linux and Command on macOS — one grammar for both, since a shortcut means the same thing to the
// person pressing it.
var comboModifierKeys = map[string]bool{"mod": true, "ctrl": true, "meta": true, "shift": true, "alt": true}

// comboModifierError says why a key combination will not run, or nil when it will. The kernel
// refuses a handler it cannot make sense of rather than misfiring, so the mistake is named here,
// next to the markup.
func comboModifierError(modifier, event string) error {
	if event != "keydown" && event != "keyup" {
		return fmt.Errorf(":%s is a key combination, so it belongs on keydown/keyup, not %s", modifier, event)
	}
	parts := strings.Split(modifier, "+")
	key := ""
	seen := map[string]bool{}
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf(":%s has an empty part — write it as mod+k", modifier)
		}
		if comboModifierKeys[part] {
			if seen[part] {
				return fmt.Errorf(":%s repeats %s", modifier, part)
			}
			seen[part] = true
			continue
		}
		if key != "" {
			return fmt.Errorf(":%s names two keys (%s and %s) — a combination presses one", modifier, key, part)
		}
		key = part
	}
	if key == "" {
		return fmt.Errorf(":%s names no key — write it as mod+k", modifier)
	}
	if seen["mod"] && (seen["ctrl"] || seen["meta"]) {
		return fmt.Errorf(":%s mixes mod with ctrl/meta — mod already means whichever one this platform uses", modifier)
	}
	return nil
}

// styleAttrRe finds an authored style directive so its PROPERTY can be checked; the expression
// itself rides the ordinary directive verification.
var styleAttrRe = regexp.MustCompile(`data-kit-style:([-a-zA-Z0-9_]+)="[^"]*"`)

// styleBlockedProperty holds the property names a value can escape through: three can carry script
// in some engines, and css-text would let one binding rewrite the whole declaration.
var styleBlockedProperty = map[string]bool{"css-text": true, "csstext": true, "behavior": true, "-moz-binding": true}

var stylePlainPropertyRe = regexp.MustCompile(`^-?[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var styleCustomPropertyRe = regexp.MustCompile(`^--[A-Za-z_][A-Za-z0-9_-]*$`)
var styleReservedCustomRe = regexp.MustCompile(`(?i)^--(?:kit|kitwork)-`)

// stylePropertyError reports why the kernel will not write this property, or nil when it will.
// Custom properties are allowed — a Kitwork site's colours live in them — except the engine's own
// --kit-/--kitwork- namespace, which the runtime writes and must be able to trust.
func stylePropertyError(property string) error {
	if strings.HasPrefix(property, "--") {
		if !styleCustomPropertyRe.MatchString(property) {
			return fmt.Errorf("data-kit-style:%s is not a custom property name (--name, letters, digits, dash, underscore)", property)
		}
		if styleReservedCustomRe.MatchString(property) {
			return fmt.Errorf("data-kit-style:%s writes the engine's own namespace — pick a name of your own", property)
		}
		return nil
	}
	if !stylePlainPropertyRe.MatchString(property) {
		return fmt.Errorf("data-kit-style:%s is not a CSS property name (lower case, digits and dashes)", property)
	}
	if styleBlockedProperty[property] {
		return fmt.Errorf("data-kit-style:%s is refused: a value could escape the declaration through it", property)
	}
	return nil
}

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
		if combo := comboModifierRe.FindStringSubmatch(name); combo != nil {
			if err := comboModifierError(name, event); err != nil {
				return err
			}
			seen["combo"] = true
			continue
		}
		switch name {
		case "window", "document", "outside", "self", "escape", "enter", "prevent", "stop", "once", "debounce", "throttle":
		default:
			return fmt.Errorf("unknown modifier :%s (the pipeline is :window :document → :outside :escape :enter :self → :prevent → :stop → :debounce(n) :throttle(n) → :once)", name)
		}
	}
	if seen["combo"] && (seen["escape"] || seen["enter"]) {
		return fmt.Errorf("a key combination is already the filter — :escape and :enter cannot join it")
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
	if seen["self"] && (seen["outside"] || seen["window"] || seen["document"]) {
		return fmt.Errorf(":self keeps the handler to its own element; it cannot combine with :outside, :window or :document")
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
var presenceRe = regexp.MustCompile(`data-kit-(?:text|show|if|for|error|bind:[a-z][a-z0-9-]*|style:[-a-zA-Z0-9_]+|seed(?::[a-z][a-z0-9-]*)?|class|model|scope|component|(?:click|dblclick|submit|input|change|keydown|keyup|pointerdown|pointerup|focusin|focusout)(?::[a-z0-9+]+(?:\([0-9]+\))?)*)="`)

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

// authoredPrefix is the one substring every authored directive shares — the cheap door.
const authoredPrefix = "data-kit-"

// Diagnostics decides whether authoring mistakes are printed. The engine sets it from AllowLocal at
// boot: on while someone is developing, off in production, where the same scan would cost every
// response and nobody would read the result.
var Diagnostics = true

// hasAuthoredDirective reports whether the page really uses the grammar. A documentation page that
// PRINTS `data-kit-text="…"` inside a <pre> or <code> is showing an example, not using one, so those
// blocks are removed before the second look. Measured across this repo, three files depend on that
// distinction — and 157 pages depend on the engine recognising them without a marker.
func hasAuthoredDirective(html string) bool {
	if !presenceRe.MatchString(html) {
		return false
	}
	if !strings.Contains(html, "<pre") && !strings.Contains(html, "<code") {
		return true
	}
	return presenceRe.MatchString(exampleBlockRe.ReplaceAllString(html, ""))
}

var exampleBlockRe = regexp.MustCompile(`(?s)<(pre|code)[^>]*>.*?</(pre|code)>`)

// reportAuthoringMistakes prints everything the server can see and the browser would only whisper.
func reportAuthoringMistakes(html string) {
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
	// A style directive names a CSS property in the attribute. The kernel refuses to write one it
	// cannot vouch for and says nothing further, so say it HERE, next to the markup, where the
	// author can see which property was refused and why.
	for _, m := range styleAttrRe.FindAllStringSubmatch(html, -1) {
		if err := stylePropertyError(m[1]); err != nil {
			fmt.Printf("[hydrate] %v — in %s\n", err, m[0])
		}
	}
	// The costliest mistake an authored page can make is a name nothing declares: the kernel reads
	// it as 0 and the markup looks right. The tree and the expressions are both here, so say it.
	reportUndeclaredNames(html)
	// A seed's value is a state target, not an expression; name a malformed one at render, as the
	// kernel would only report it in the browser.
	for _, m := range seedAttrRe.FindAllStringSubmatch(html, -1) {
		if err := seedTargetError(strings.TrimSpace(authoredAttribute(m[2]))); err != nil {
			fmt.Printf("[hydrate] %v — in %s\n", err, m[0])
		}
	}
}

func Render(html string) string {
	// Is this a Kitwork page? The markup says so by ITSELF — an authored directive is the evidence,
	// and no marker attribute is required or read. The cheap string test comes first so an ordinary
	// static page costs one scan for a substring and nothing else.
	if !strings.Contains(html, authoredPrefix) || !hasAuthoredDirective(html) {
		return html
	}
	// Authoring mistakes are reported for the person writing the page, so they are printed where a
	// person is watching. In production nobody reads the server log for this, and the scan is not free.
	if Diagnostics {
		reportAuthoringMistakes(html)
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

// MarkDrive flags the runtime script tag so the client knows this site asked for Drive
// (router.drive()). The flag rides on the ENGINE's own tag, not on anything the author wrote: it is
// a transport detail, so it wears the data-kitwork-* prefix and never appears in a template.
//
// Before this, Drive switched itself on wherever a root marker appeared — an attribute authors wrote
// to mean something else entirely.
func MarkDrive(html string) string {
	const tag = `<script data-kitwork-jit="runtime"`
	i := strings.Index(html, tag)
	if i < 0 || strings.Contains(html, driveFlag) {
		return html
	}
	at := i + len(tag)
	return html[:at] + " " + driveFlag + html[at:]
}

// driveFlag is what MarkDrive writes and what drive.js looks for.
const driveFlag = "data-kitwork-drive"
