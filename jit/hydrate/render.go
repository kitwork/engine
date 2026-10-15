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

// DriveFile is Drive and the morph it drives, as a file of its own: only a site that moves without a
// reload (router.reload(false)) loads it, and it is the same file on every page of every such site.
//
// These two files are the whole client runtime; there is no composed one any more (03/10). Because
// the engine serves them and the Go compiler from one codebase, the two ends version together —
// grammar sync is by construction, not by discipline.
func DriveFile() string {
	return morphJS + "\n" + driveJS
}

// UsesGrammar reports whether a page really uses the grammar — a directive outside the <pre>/<code>
// blocks that only show one, written in either form: the data-kit-* an author writes, or the eight
// grouped data-kitwork-* attributes the engine writes. It is the test Check applies before reading a
// page, for a caller deciding whether the page needs the kernel at all.
func UsesGrammar(html string) bool {
	return hasDirectivePrefix(html) && hasAuthoredDirective(html)
}

// There is no root marker: an authored directive outside an example block is what makes a page a
// Kitwork page — so static pages (and demos that show data-* as example text) are never affected.
//
// PREFIX CONVENTION, restated 11/10/2026 for the grouped web output (KITWORK_UI.md §11).
//
//	data-kitwork-*  what the ENGINE writes, one prefix for both of its kinds:
//	                · the eight grouped DIRECTIVES the kernel reads — component, scope, bind, class,
//	                  style, event, if, for (GroupedDirectives, pairs.go) — "name: value; …" in one
//	                  attribute each;
//	                · TRANSPORT that is not grammar: data-kitwork-generated=* (what the engine or
//	                  Drive made), -layout, -current, -delay, -transition, -shell, -highlight and the
//	                  old -jit. These never make a page look like a Kitwork page.
//
//	data-kit-*      the hand-written GRAMMAR of template pages (data-kit-text, data-kit-bind:x,
//	                data-kit-click:prevent, …), read beside the grouped form until the shared
//	                clean-up; and what the kernel itself writes on a materialised row
//	                (data-kit-item, data-kit-key).
//
// (A precompiled IR directive — data-kitwork-text="[JSON]" — was a reserved wire mode the engine
// never emitted; it is gone (ideaship-final §9), and data-kitwork-text is not a grouped directive:
// text is a pair of data-kitwork-bind.) The root marker (data-kit-app / data-kit-hydrate) is gone
// entirely: a page is recognised by its grammar, and Drive is switched on by router.reload(false).

// directiveRe matches a hand-written EXPRESSION directive — data-kit-<name>="<expr>". The grouped
// data-kitwork-* attributes are verified by their own pass (grouped_check.go), pair by pair.
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
// sep is how the modifier is written where it was found: ':' in a data-kit-* attribute name, '.' in a
// data-kitwork-event pair.
func comboModifierError(modifier, event, sep string) error {
	if event != "keydown" && event != "keyup" {
		return fmt.Errorf("%s%s is a key combination, so it belongs on keydown/keyup, not %s", sep, modifier, event)
	}
	parts := strings.Split(modifier, "+")
	key := ""
	seen := map[string]bool{}
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("%s%s has an empty part — write it as mod+k", sep, modifier)
		}
		if comboModifierKeys[part] {
			if seen[part] {
				return fmt.Errorf("%s%s repeats %s", sep, modifier, part)
			}
			seen[part] = true
			continue
		}
		if key != "" {
			return fmt.Errorf("%s%s names two keys (%s and %s) — a combination presses one", sep, modifier, key, part)
		}
		key = part
	}
	if key == "" {
		return fmt.Errorf("%s%s names no key — write it as mod+k", sep, modifier)
	}
	if seen["mod"] && (seen["ctrl"] || seen["meta"]) {
		return fmt.Errorf("%s%s mixes mod with ctrl/meta — mod already means whichever one this platform uses", sep, modifier)
	}
	return nil
}

// styleAttrRe finds an authored style directive so its PROPERTY can be checked; the expression
// itself rides the ordinary directive verification. A data-kitwork-style pair goes through the same
// property check (grouped_check.go).
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
	return stylePropertyErrorIn("data-kit-style:"+property, property)
}

// stylePropertyErrorIn is stylePropertyError for a property written in label — "data-kit-style:top"
// or "data-kitwork-style top" — so the message names what the author wrote.
func stylePropertyErrorIn(label, property string) error {
	if strings.HasPrefix(property, "--") {
		if !styleCustomPropertyRe.MatchString(property) {
			return fmt.Errorf("%s is not a custom property name (--name, letters, digits, dash, underscore)", label)
		}
		if styleReservedCustomRe.MatchString(property) {
			return fmt.Errorf("%s writes the engine's own namespace — pick a name of your own", label)
		}
		return nil
	}
	if !stylePlainPropertyRe.MatchString(property) {
		return fmt.Errorf("%s is not a CSS property name (lower case, digits and dashes)", label)
	}
	if styleBlockedProperty[property] {
		return fmt.Errorf("%s is refused: a value could escape the declaration through it", label)
	}
	return nil
}

var eventTypes = map[string]bool{"click": true, "dblclick": true, "submit": true, "input": true, "change": true, "keydown": true, "keyup": true, "pointerdown": true, "pointerup": true, "focusin": true, "focusout": true}
var outsideEventTypes = map[string]bool{"click": true, "dblclick": true, "pointerdown": true, "pointerup": true, "focusin": true}
var timingModifierRe = regexp.MustCompile(`^(debounce|throttle)\(([0-9]+)\)$`)

func checkEventModifiers(directive string) error {
	if directive == "intersect" || strings.HasPrefix(directive, "intersect:") {
		return checkIntersectModifiers(strings.Split(directive, ":")[1:], ":", "data-kit-intersect", "leave")
	}
	parts := strings.Split(directive, ":")
	if parts[0] == "event" && len(parts) > 1 {
		// data-kit-event:<name>: the name is the event, and the same modifiers apply.
		parts = parts[1:]
	} else if !eventTypes[parts[0]] {
		return nil
	}
	return checkModifierPipeline(parts[0], parts[1:], ":")
}

// checkModifierPipeline checks the modifiers of one handler of event (the DOM event the kernel
// listens for). sep is how they are written: ':' in a data-kit-* name, '.' in a data-kitwork-event
// pair — the rules are the kernel's, one pipeline for both.
func checkModifierPipeline(event string, modifiers []string, sep string) error {
	seen := map[string]bool{}
	timing := 0
	for _, modifier := range modifiers {
		name := modifier
		if m := timingModifierRe.FindStringSubmatch(modifier); m != nil {
			name = m[1]
			timing++
			if n, err := strconv.Atoi(m[2]); err != nil || n < 1 || n > 60000 {
				return fmt.Errorf("%s(%s): the delay is 1–60000 milliseconds", m[1], m[2])
			}
		}
		if seen[name] {
			return fmt.Errorf("modifier %s%s is repeated", sep, name)
		}
		seen[name] = true
		if combo := comboModifierRe.FindStringSubmatch(name); combo != nil {
			if err := comboModifierError(name, event, sep); err != nil {
				return err
			}
			seen["combo"] = true
			continue
		}
		switch name {
		case "window", "document", "outside", "self", "escape", "enter", "prevent", "stop", "once", "debounce", "throttle":
		default:
			return fmt.Errorf("unknown modifier %s%s (the pipeline is %s)", sep, name,
				strings.ReplaceAll(":window :document → :outside :escape :enter :self → :prevent → :stop → :debounce(n) :throttle(n) → :once", ":", sep))
		}
	}
	if seen["combo"] && (seen["escape"] || seen["enter"]) {
		return fmt.Errorf("a key combination is already the filter — %sescape and %senter cannot join it", sep, sep)
	}
	if (seen["escape"] || seen["enter"]) && event != "keydown" && event != "keyup" {
		return fmt.Errorf("%sescape and %senter filter a key, so they belong on keydown/keyup, not %s", sep, sep, event)
	}
	if seen["escape"] && seen["enter"] {
		return fmt.Errorf("%sescape and %senter cannot both be the filter", sep, sep)
	}
	if seen["outside"] && !outsideEventTypes[event] {
		return fmt.Errorf("%soutside applies to click, dblclick, pointerdown, pointerup and focusin, not %s", sep, event)
	}
	if seen["self"] && (seen["outside"] || seen["window"] || seen["document"]) {
		return fmt.Errorf("%sself keeps the handler to its own element; it cannot combine with %soutside, %swindow or %sdocument", sep, sep, sep, sep)
	}
	if seen["window"] && seen["document"] {
		return fmt.Errorf("%swindow and %sdocument name two targets; pick one", sep, sep)
	}
	if timing > 1 {
		return fmt.Errorf("%sdebounce and %sthrottle cannot both time one handler", sep, sep)
	}
	return nil
}

// checkIntersectModifiers names a data-kit-intersect modifier the observer or the pipeline would not
// understand. :leave says when (not on screen), :once how often, :half / :full how much must show,
// :margin(x) where the screen's edge is — the event modifiers (:prevent, :outside…) mean nothing to
// an element entering the screen. sep and label say how it was written (data-kit-intersect:once, or
// intersect-enter.once in data-kitwork-event, where the name already says enter or leave and the
// allowed list leaves "leave" out).
func checkIntersectModifiers(modifiers []string, sep, label string, allowed ...string) error {
	allowed = append(allowed, "once", "half", "full", "margin")
	seen := map[string]bool{}
	for _, modifier := range modifiers {
		name := modifier
		if strings.HasPrefix(modifier, "margin(") {
			if !strings.HasSuffix(modifier, ")") || len(modifier) == len("margin()") {
				example := "-20%" // a pair's name has no '%' (pairs.go), so the grouped form's example is a length
				if sep == "." {
					example = "-2rem"
				}
				return fmt.Errorf("%s%s needs a CSS margin, like %smargin(%s)", sep, modifier, sep, example)
			}
			name = "margin"
		}
		if seen[name] {
			return fmt.Errorf("modifier %s%s is repeated", sep, name)
		}
		seen[name] = true
		known := false
		for _, a := range allowed {
			known = known || a == name
		}
		if !known {
			takes := make([]string, len(allowed))
			for i, a := range allowed {
				if a == "margin" {
					a = "margin(x)"
				}
				takes[i] = sep + a
			}
			return fmt.Errorf("unknown modifier %s%s for %s (it takes %s)", sep, modifier, label, strings.Join(takes, " "))
		}
	}
	if seen["half"] && seen["full"] {
		return fmt.Errorf("%shalf and %sfull name two thresholds; pick one", sep, sep)
	}
	return nil
}

// presenceRe decides runtime INJECTION: authored data-kit-* forms, including the non-expression
// attributes — model is a plain scope key, scope/component a boundary — which need the runtime but
// must never be compile-verified; for's value is a spec, not an expression. (data-kit-api, -live and
// -remember were removed on 02/10; they bring nothing.)
var presenceRe = regexp.MustCompile(`data-kit-(?:text|show|if|for|bind:[a-z][a-z0-9-]*|style:[-a-zA-Z0-9_]+|seed(?::[a-z][a-z0-9-]*)?|class|model|scope|component|(?:click|dblclick|submit|input|change|keydown|keyup|pointerdown|pointerup|focusin|focusout|event:[a-z][a-z0-9-]*|intersect)(?::[a-z0-9+]+(?:\([^)"]*\))?)*|drag)="`)

// groupedPresenceRe is presenceRe for the engine's form: exactly the eight grouped directives, never
// a transport marker — data-kitwork-generated sits on nearly every page, and matching the prefix would
// give every page the kernel.
var groupedPresenceRe = regexp.MustCompile(`(?:` + strings.Join(GroupedDirectives, "|") + `)="`)

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

// authoredPrefix is the one substring every hand-written directive shares — the cheap door, with the
// eight grouped names beside it (hasDirectivePrefix). "data-kit-" is not a substring of
// "data-kitwork-", so a page whose directives are all grouped needs the second look.
const authoredPrefix = "data-kit-"

// hasDirectivePrefix is the cheap door: a substring test per form, before any regex runs.
func hasDirectivePrefix(html string) bool {
	if strings.Contains(html, authoredPrefix) {
		return true
	}
	if !strings.Contains(html, "data-kitwork-") {
		return false
	}
	for _, directive := range GroupedDirectives {
		if strings.Contains(html, directive+`="`) {
			return true
		}
	}
	return false
}

// Diagnostics decides whether authoring mistakes are printed. The engine sets it from AllowLocal at
// boot: on while someone is developing, off in production, where the same scan would cost every
// response and nobody would read the result.
var Diagnostics = true

// hasAuthoredDirective reports whether the page really uses the grammar. A documentation page that
// PRINTS `data-kit-text="…"` inside a <pre> or <code> is showing an example, not using one, so those
// blocks are removed before the second look. Measured across this repo, three files depend on that
// distinction — and 157 pages depend on the engine recognising them without a marker.
func hasAuthoredDirective(html string) bool {
	present := func(html string) bool { return presenceRe.MatchString(html) || groupedPresenceRe.MatchString(html) }
	if !present(html) {
		return false
	}
	if !strings.Contains(html, "<pre") && !strings.Contains(html, "<code") {
		return true
	}
	return present(exampleBlockRe.ReplaceAllString(html, ""))
}

var exampleBlockRe = regexp.MustCompile(`(?s)<(pre|code)[^>]*>.*?</(pre|code)>`)

// reportAuthoringMistakes prints everything the server can see and the browser would only whisper.
func reportAuthoringMistakes(html string, components ComponentNames) {
	for _, line := range authoringMistakesWith(html, components) {
		fmt.Printf("[hydrate] %s\n", line)
	}
}

// authoringMistakes is the same pass without the printing, so a test can read what an author
// would be told rather than capturing stdout — and so the engine can decide where a report goes.
func authoringMistakes(html string) []string { return authoringMistakesWith(html, nil) }

func authoringMistakesWith(html string, components ComponentNames) []string {
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
	// The grouped form the engine writes — data-kitwork-component, -scope, -bind, -class, -style,
	// -event, -if and -for — is read pair by pair and held to the same rules (grouped_check.go).
	found = append(found, groupedMistakes(html, declared)...)
	// The costliest mistake an authored page can make is a name nothing declares: the kernel reads
	// it as 0 and the markup looks right. The tree and the expressions are both here, so say it.
	found = append(found, undeclaredWarningsWith(html, components)...)
	// A seed's value is a state target, not an expression; name a malformed one at render, as the
	// kernel would only report it in the browser.
	for _, m := range seedAttrRe.FindAllStringSubmatch(html, -1) {
		if err := seedTargetError(strings.TrimSpace(authoredAttribute(m[2]))); err != nil {
			found = append(found, fmt.Sprintf("%v — in %s", err, m[0]))
		}
	}
	return found
}

// ComponentNames answers, for a component — a data-kit-component value, or the name a
// data-kitwork-component carries before its ": $alias" (the version stays: "theme@1.0.0") — the
// names that component's scope holds,
// or ok=false when they cannot be known for certain — which keeps that component opaque to the
// checks, as every component was before 03/10. jit/js provides it, per site (ComponentNames there).
type ComponentNames func(component string) (names []string, ok bool)

func Check(html string, components ComponentNames) {
	// Is this a Kitwork page? The markup says so by ITSELF — an authored directive is the evidence,
	// and no marker attribute is required or read. The cheap string test comes first so an ordinary
	// static page costs one scan for a substring and nothing else.
	if !Diagnostics || !hasDirectivePrefix(html) || !hasAuthoredDirective(html) {
		return
	}
	// Authoring mistakes are reported for the person writing the page, so they are printed where a
	// person is watching. In production nobody reads the server log for this, and the scan is not free.
	reportAuthoringMistakes(html, components)
}

// (MarkDrive is gone, 10/10/2026. It flagged the kernel's tag data-kitwork-drive so drive.js could
// tell whether the site asked for Drive; that was needed while Drive rode inside one runtime file every
// page loaded. Drive is a file of its own now, delivered only to a site that asked for it, so the file
// being on the page is the answer.)
