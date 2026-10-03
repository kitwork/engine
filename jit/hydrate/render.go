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
// boot. It is identical for every tenant. Pages get it as files (KernelFile, DriveFile); the one
// composed string is for the browser test harnesses.
// Because the engine serves BOTH this runtime and the Go compiler from one codebase, the two ends
// version together — grammar sync is by construction, not by discipline.
// RuntimeParts is what a particular page needs. The runtime is not one indivisible blob: a site
// that never asked for Drive has no use for the navigator or the DOM morph it drives, and paying
// for them on every page is the same waste as shipping a component nobody named.
type RuntimeParts struct {
	// Modules is the page's own components, already composed by jit/js.
	Modules string
	// Drive ships the navigator and the DOM morph it drives — router.drive(). Measured 24/09 on the
	// MINIFIED asset a visitor actually receives: 18,218 bytes gzip with it, 15,407 without, so it
	// is 2,811 bytes on every page of a site that never asked. Nothing else calls kit.morph.
	Drive bool
	// Native ships the bridge and the 18 capability proxies that ride it. They only reach anything
	// inside a desktop or mobile shell: in an ordinary browser window there is no WebView handle, so
	// every call rejects with UNSUPPORTED. Measured 24/09 on the minified asset: 1,869 bytes gzip.
	Native bool
}

// Runtime is every part, which is what a test harness or the published package wants.
func Runtime() string {
	return RuntimeFor(RuntimeParts{Drive: true, Native: true})
}

// RuntimeFor composes exactly the parts a page needs, in the one order that works: bridge and
// kernel first, platform modules next, then the page's own modules, and boot LAST — a component
// that registers after the first paint has missed it (see boot.js).
func RuntimeFor(parts RuntimeParts) string {
	pieces := []string{kernelJS}
	if parts.Native {
		// The bridge loads BEFORE the kernel: the kernel reads kit.platform as it initialises.
		pieces = []string{bridgeJS, kernelJS, nativeJS}
	}
	pieces = append(pieces, storageJS, webJS)
	if parts.Drive {
		pieces = append(pieces, morphJS, driveJS)
	}
	if strings.TrimSpace(parts.Modules) != "" {
		pieces = append(pieces, parts.Modules)
	}
	return strings.Join(append(pieces, bootJS), "\n")
}

// KernelFile is the kernel as a file of its own — the first of the separate files a page loads
// (jit/js Deliver): the bridge when the app runs in a shell, the kernel, the platform modules and
// boot. Boot can close this file rather than the whole composition because it waits for
// DOMContentLoaded, which every deferred file after it — Drive, each component — precedes.
func KernelFile(native bool) string {
	pieces := []string{kernelJS}
	if native {
		pieces = []string{bridgeJS, kernelJS, nativeJS}
	}
	return strings.Join(append(pieces, storageJS, webJS, bootJS), "\n")
}

// DriveFile is Drive and the morph it drives, as a file of its own: only a site that asked for
// router.drive() loads it, and it is the same file on every page of every such site.
func DriveFile() string {
	return morphJS + "\n" + driveJS
}

// UsesGrammar reports whether a page really uses the grammar — an authored directive outside the
// <pre>/<code> blocks that only show one. It is the test Check applies before reading a page, for
// a caller deciding whether the page needs the kernel at all.
func UsesGrammar(html string) bool {
	return strings.Contains(html, authoredPrefix) && hasAuthoredDirective(html)
}

// RuntimePath is where the browser test harnesses serve the one-file composition (RuntimeFor) to
// the pages they build. The engine routes no such path any more (03/10): a page gets its
// JavaScript as one file per part, /kit/<part>.<hash>.js (jit/js delivery.go).
const RuntimePath = "/kit.js"

// There is no root marker: an authored directive outside an example block is what makes a page a
// Kitwork page — so static pages (and demos that show data-* as example text) are never affected.
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
// An event handler is data-kit-<event>[:modifier…] (ideaship-final §2–§4), or the long form
// data-kit-event:<name>[:modifier…] for any event by name — one a component emits, or a DOM event
// outside the short list (02/10). Its modifiers are checked by checkEventModifiers, since the kernel
// disables a handler it cannot make sense of.
var directiveRe = regexp.MustCompile(`data-kit-(text|show|if|bind:[a-z][a-z0-9-]*|style:[-a-zA-Z0-9_]+|class|(?:click|dblclick|submit|input|change|keydown|keyup|pointerdown|pointerup|focusin|focusout|event:[a-z][a-z0-9-]*|intersect)(?::[a-z0-9+]+(?:\([^)"]*\))?)*)="([^"]*)"`)

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
	if directive == "intersect" || strings.HasPrefix(directive, "intersect:") {
		return checkIntersectModifiers(strings.Split(directive, ":")[1:])
	}
	parts := strings.Split(directive, ":")
	if parts[0] == "event" && len(parts) > 1 {
		// data-kit-event:<name>: the name is the event, and the same modifiers apply.
		parts = parts[1:]
	} else if !eventTypes[parts[0]] {
		return nil
	}
	event := parts[0]
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

// checkIntersectModifiers names a data-kit-intersect modifier the observer or the pipeline would not
// understand. :leave says when (not on screen), :once how often, :half / :full how much must show,
// :margin(x) where the screen's edge is — the event modifiers (:prevent, :outside…) mean nothing to
// an element entering the screen.
func checkIntersectModifiers(modifiers []string) error {
	seen := map[string]bool{}
	for _, modifier := range modifiers {
		name := modifier
		if strings.HasPrefix(modifier, "margin(") {
			if !strings.HasSuffix(modifier, ")") || len(modifier) == len("margin()") {
				return fmt.Errorf(":%s needs a CSS margin, like :margin(-20%%)", modifier)
			}
			name = "margin"
		}
		if seen[name] {
			return fmt.Errorf("modifier :%s is repeated", name)
		}
		seen[name] = true
		switch name {
		case "leave", "once", "half", "full", "margin":
		default:
			return fmt.Errorf("unknown modifier :%s for data-kit-intersect (it takes :leave :once :half :full :margin(x))", modifier)
		}
	}
	if seen["half"] && seen["full"] {
		return fmt.Errorf(":half and :full name two thresholds; pick one")
	}
	return nil
}

// presenceRe decides runtime INJECTION: authored data-kit-* forms, including the non-expression
// attributes — model is a plain scope key, scope/component a boundary — which need the runtime but
// must never be compile-verified; for's value is a spec, not an expression. A data-kitwork-*
// directive is not authored and not read, so it does not bring the runtime. (data-kit-api, -live and
// -remember were removed on 02/10; they bring nothing.)
var presenceRe = regexp.MustCompile(`data-kit-(?:text|show|if|for|bind:[a-z][a-z0-9-]*|style:[-a-zA-Z0-9_]+|seed(?::[a-z][a-z0-9-]*)?|class|model|scope|component|(?:click|dblclick|submit|input|change|keydown|keyup|pointerdown|pointerup|focusin|focusout|event:[a-z][a-z0-9-]*|intersect)(?::[a-z0-9+]+(?:\([^)"]*\))?)*|drag)="`)

// Check is the server pass for hydrate pages. THE WIRE SHIPS THE SOURCE: authored data-kit-*
// attributes ride to the client unchanged (readable DOM, smaller wire) and the client runtime
// parses them there — no eval, same grammar. What the server does here is VERIFY: every
// expression is compiled with the Go compiler at render time, so a typo is caught and printed on
// the server instead of failing silently in the browser. It never changes the page: giving it its
// JavaScript is jit/js Deliver's job, which runs first. (Check was Render until 03/10, when its
// fallback <script src="/kit.js"> went with the route — Deliver had always answered first.)
//
// IR remains the engine's INTERNAL form (ctx.validate, go tests, analysis); it is not a wire
// mode — the kernel parses source, and only source.

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
	for _, line := range authoringMistakes(html) {
		fmt.Printf("[hydrate] %s\n", line)
	}
}

// authoringMistakes is the same pass without the printing, so a test can read what an author
// would be told rather than capturing stdout — and so the engine can decide where a report goes.
func authoringMistakes(html string) []string {
	var found []string
	html = withoutExamples(html)
	declared := declaredElements(html)
	for _, m := range directiveRe.FindAllStringSubmatch(html, -1) {
		expression := authoredAttribute(m[2])
		ir, err := Compile(expression)
		if err != nil {
			found = append(found, fmt.Sprintf("%v — in %s", err, m[0]))
			continue
		}
		if err := checkEventModifiers(m[1]); err != nil {
			found = append(found, fmt.Sprintf("%v — in %s", err, m[0]))
			continue
		}
		// Two reads the kernel resolves to nothing without a word: a service member markup was
		// never granted, and an element name nothing declares.
		checkKitReads(expression, m[0], &found)
		checkElementReads(expression, m[0], declared, &found)
		checkBindingWrites(m[1], ir, m[0], &found)
		// data-kit-class carries an extra obligation the other directives do not: the CSS JIT emits
		// only classes it can SEE, and it reads them off this expression's literals. A name built by
		// concatenation is invisible to it, so the class would resolve at runtime to a rule that was
		// never generated — styling that silently disappears for some values and works for others,
		// which is far harder to diagnose than an error. Say so at render, next to the markup.
		if m[1] == "class" && HasConstructedClass(expression) {
			found = append(found, fmt.Sprintf("class names must be written out in full — the CSS JIT cannot emit a "+
				"name built with '+', so this rule is never generated. Use a conditional between "+
				"complete names (color === 'red' ? 'text-red' : 'text-blue') — in %s", m[0]))
		}
	}
	// A style directive names a CSS property in the attribute. The kernel refuses to write one it
	// cannot vouch for and says nothing further, so say it HERE, next to the markup, where the
	// author can see which property was refused and why.
	for _, m := range styleAttrRe.FindAllStringSubmatch(html, -1) {
		if err := stylePropertyError(m[1]); err != nil {
			found = append(found, fmt.Sprintf("%v — in %s", err, m[0]))
		}
	}
	// The costliest mistake an authored page can make is a name nothing declares: the kernel reads
	// it as 0 and the markup looks right. The tree and the expressions are both here, so say it.
	found = append(found, undeclaredWarnings(html)...)
	// A seed's value is a state target, not an expression; name a malformed one at render, as the
	// kernel would only report it in the browser.
	for _, m := range seedAttrRe.FindAllStringSubmatch(html, -1) {
		if err := seedTargetError(strings.TrimSpace(authoredAttribute(m[2]))); err != nil {
			found = append(found, fmt.Sprintf("%v — in %s", err, m[0]))
		}
	}
	return found
}

func Check(html string) {
	// Is this a Kitwork page? The markup says so by ITSELF — an authored directive is the evidence,
	// and no marker attribute is required or read. The cheap string test comes first so an ordinary
	// static page costs one scan for a substring and nothing else.
	if !Diagnostics || !strings.Contains(html, authoredPrefix) || !hasAuthoredDirective(html) {
		return
	}
	// Authoring mistakes are reported for the person writing the page, so they are printed where a
	// person is watching. In production nobody reads the server log for this, and the scan is not free.
	reportAuthoringMistakes(html)
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
