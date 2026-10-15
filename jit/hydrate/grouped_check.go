package hydrate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The dev-time check of the grouped attributes (KITWORK_UI.md §11), next to the data-kit-* one in
// render.go. The kernel is as quiet with these as with the hand-written form — a pair it cannot read
// does nothing — so the server says it here, pair by pair, naming the attribute, the pair and where in
// the value to look. Every value is compiled with the Go twin of the kernel's parser; the names are
// held to the rules the kernel applies when it writes:
//
//	bind   text, show, value, checked, aria-*, and plain attribute names — never on… (code), style
//	       (its own attribute), class or class.<name> (data-kitwork-class), srcdoc (a document),
//	       data-kit… (a directive)
//	class  the exact class name, by the splitter's name rule (letters, digits, '-', '.', '_', '+', '/',
//	       a flat (…) — never ':', so a variant class cannot be toggled; an opacity one can)
//	style  a CSS property or a custom property, as the kernel's styleName accepts
//	event  the .ui vocabulary (click, double-click, click-outside, focus-in, pointer-enter, …) and
//	       the kernel's modifier pipeline, written with '.'; a name it does not know is a warning —
//	       the kernel listens for a custom event of that name
//	scope  plain names
//	component, for, if  their own shapes

// groupedAttrRe finds one of the eight grouped attributes, and its value.
var groupedAttrRe = regexp.MustCompile(`(?:^|\s)(` + strings.Join(GroupedDirectives, "|") + `)="([^"]*)"`)

var (
	groupedScopeKeyRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	groupedBindAttrRe  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	groupedEventNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	componentBaseRe    = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	componentVersionRe = regexp.MustCompile(`^v?[0-9]+(?:\.[0-9]+)*$`)
	bareNameRe         = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
)

// reservedAliases are the kernel's own handles (kernel.js reservedAliases): a component cannot be one.
var reservedAliases = map[string]bool{"$this": true, "$el": true, "$host": true, "$root": true, "$event": true, "$element": true}

// groupedEventTypes is the .ui event vocabulary on the wire and the DOM event the kernel listens for.
// click-outside is click with the outside modifier; intersect-enter/-leave and ready are not DOM events
// and have rules of their own (groupedEventProblem).
var groupedEventTypes = map[string]string{
	"click": "click", "double-click": "dblclick", "context-menu": "contextmenu", "click-outside": "click",
	"keydown": "keydown", "keyup": "keyup", "input": "input", "change": "change", "submit": "submit",
	"focus-in": "focusin", "focus-out": "focusout",
	"pointer-down": "pointerdown", "pointer-up": "pointerup", "pointer-enter": "pointerenter", "pointer-leave": "pointerleave",
	"scroll": "scroll", "load": "load", "error": "error", "toggle": "toggle",
	"intersect-enter": "", "intersect-leave": "", "ready": "",
}

const groupedEventVocabulary = "click, double-click, context-menu, click-outside, keydown, keyup, input, change, submit, " +
	"focus-in, focus-out, pointer-down, pointer-up, pointer-enter, pointer-leave, scroll, load, error, toggle, " +
	"intersect-enter, intersect-leave, ready"

// characterAt is "at character N" for a byte offset into a value, counting characters from 1.
func characterAt(value string, offset int) string {
	if offset > len(value) {
		offset = len(value)
	}
	return fmt.Sprintf("at character %d", utf8.RuneCountInString(value[:offset])+1)
}

// groupedMistakes is the grouped half of authoringMistakes: one line per mistake, each ending with the
// attribute it is in.
func groupedMistakes(html string, declared map[string]bool) []string {
	if !strings.Contains(html, "data-kitwork-") {
		return nil
	}
	var found []string
	for _, m := range groupedAttrRe.FindAllStringSubmatch(html, -1) {
		attribute, value, where := m[1], authoredAttribute(m[2]), strings.TrimSpace(m[0])
		report := func(format string, args ...any) {
			found = append(found, fmt.Sprintf(format, args...)+" — in "+where)
		}
		// expression compiles one value and runs the reads every value gets; binding says whether it
		// runs on every paint, so a write in it is a mistake.
		expression := func(label, source string, at int, binding bool) {
			ir, err := Compile(source)
			if err != nil {
				report("%s: %v (the value starts %s)", label, err, characterAt(value, at))
				return
			}
			checkKitReads(source, where, &found)
			checkElementReads(source, where, declared, &found)
			if binding {
				checkGroupedBindingWrite(label, ir, where, &found)
			}
		}
		switch attribute {
		case "data-kitwork-component":
			checkGroupedComponent(value, report)
		case "data-kitwork-if":
			parts, err := splitTopLevel(value)
			if err != nil {
				report("data-kitwork-if: %v", err)
				continue
			}
			if len(parts) == 0 {
				report("data-kitwork-if is empty — it holds the one expression that decides whether the element is there")
				continue
			}
			if len(parts) > 1 {
				report("data-kitwork-if holds one expression; a second one starts %s", characterAt(value, parts[1].at))
				continue
			}
			expression("data-kitwork-if", parts[0].text, parts[0].at, true)
		case "data-kitwork-for":
			item, index, list, key, listAt, keyAt, err := forValueAt(value)
			if err != nil {
				report("data-kitwork-for: %v", err)
				continue
			}
			if index != "" && index == item {
				report("data-kitwork-for names the item and the index both %q", item)
				continue
			}
			expression("data-kitwork-for list", list, listAt, true)
			if key != "" {
				expression("data-kitwork-for key", key, keyAt, true)
			}
		default:
			pairs, err := pairsAt(value)
			if err != nil {
				report("%s: %v%s", attribute, err, groupedClassSplitHint(attribute, err))
				continue
			}
			seen := map[string]bool{}
			for _, p := range pairs {
				label := attribute + " " + p.Name
				if seen[p.Name] {
					report("%s names %s twice (the second %s) — give each name once", attribute, p.Name, characterAt(value, p.nameAt))
					continue
				}
				seen[p.Name] = true
				problem, fatal := "", true
				switch attribute {
				case "data-kitwork-scope":
					if !groupedScopeKeyRe.MatchString(p.Name) {
						problem = label + ": a scope key is a name — letters, digits and _ — since expressions read it by that name"
					}
				case "data-kitwork-bind":
					problem = groupedBindNameProblem(p.Pair)
				case "data-kitwork-class":
					problem, fatal = groupedClassProblem(p.Pair)
				case "data-kitwork-style":
					if err := stylePropertyErrorIn(label, p.Name); err != nil {
						problem = err.Error()
					}
				case "data-kitwork-event":
					problem, fatal = groupedEventProblem(p.Name)
				}
				if problem != "" {
					report("%s (%s)", problem, characterAt(value, p.nameAt))
					if fatal {
						continue
					}
				}
				// bind, class and style pairs run on every paint; scope runs once and event on its event
				binding := attribute == "data-kitwork-bind" || attribute == "data-kitwork-class" || attribute == "data-kitwork-style"
				expression(label, p.Value, p.valueAt, binding)
			}
		}
	}
	return found
}

func checkGroupedComponent(value string, report func(string, ...any)) {
	name, alias, err := ComponentValue(value)
	if err != nil {
		report("data-kitwork-component: %v", err)
		return
	}
	base, version, versioned := strings.Cut(name, "@")
	if !componentBaseRe.MatchString(base) {
		report("data-kitwork-component: %q is not a component name — lower case letters, digits and dashes, starting with a letter", base)
		return
	}
	if versioned && !componentVersionRe.MatchString(version) {
		report("data-kitwork-component: %q is not a version — write it as %s@1.0.0", version, base)
		return
	}
	if reservedAliases[alias] {
		report("data-kitwork-component: %s is the kernel's own handle, so it cannot name a component — pick another alias", alias)
	}
}

// groupedBindNameProblem says why a data-kitwork-bind pair will not be written, or "".
func groupedBindNameProblem(p Pair) string {
	name := p.Name
	label := "data-kitwork-bind " + name
	switch name {
	case "text", "show", "value", "checked":
		return ""
	case "class":
		return label + ": classes are toggled one by one in data-kitwork-class — active: open — so the CSS engine can see each name"
	case "style":
		return label + ": a CSS property is not a binding — write it in data-kitwork-style"
	case "srcdoc":
		return label + ": is refused — its value would be a whole document"
	}
	// class.<name> was a bind pair until data-kitwork-class took it (11/10/2026); the kernel no longer
	// toggles anything for it, so the pair is dead, not merely old.
	if class, ok := strings.CutPrefix(name, "class."); ok {
		if class == "" {
			return label + ": toggles no class — a class toggle is a pair of data-kitwork-class, its name the class itself"
		}
		return fmt.Sprintf("%s: a class toggle is no longer a bind pair, so nothing toggles %s — write it in data-kitwork-class=\"%s: %s\"", label, class, class, p.Value)
	}
	if strings.HasPrefix(name, "on") {
		return label + ": is refused — an on… attribute runs code; an event is a pair of data-kitwork-event"
	}
	if strings.HasPrefix(name, "data-kit") {
		return label + ": is refused — a binding cannot write a directive"
	}
	if !groupedBindAttrRe.MatchString(name) {
		return label + ": is not text, show, value, checked or an attribute name (lower case, digits and dashes)"
	}
	return ""
}

// groupedVariantValueRe is the value a variant class leaves behind when its first ':' is taken for
// the end of the pair's name: data-kitwork-class="hover:underline: on" reads as the name hover and
// the value "underline: on" — a class-like run, then a ':'.
var groupedVariantValueRe = regexp.MustCompile(`^([A-Za-z0-9_.+\-/\[\]#%()]+)` + jsSpaceClass + `*:`)

// groupedClassProblem says what is wrong with a data-kitwork-class pair, and whether the toggle is dead
// because of it (fatal) or merely suspect. The splitter has already held the name to its rule.
func groupedClassProblem(p Pair) (problem string, fatal bool) {
	label := "data-kitwork-class " + p.Name
	// A variant splits without complaint and leaves a value that cannot compile; say what it was. A
	// value that compiles is never second-guessed.
	if m := groupedVariantValueRe.FindStringSubmatch(p.Value); m != nil {
		if _, err := Compile(p.Value); err != nil {
			return fmt.Sprintf("%s: \"%s:%s\" is a variant class, and a toggled class holds no ':' — toggle a plain class and give it the variant in the stylesheet, or write the variant in class=", label, p.Name, m[1]), true
		}
	}
	// The old bind form copied over whole toggles a class literally named class.<name>.
	if class, ok := strings.CutPrefix(p.Name, "class."); ok && class != "" {
		return fmt.Sprintf("%s: toggles a class named %q — the name is the class itself here, so write %s: %s", label, p.Name, class, p.Value), false
	}
	return "", false
}

// groupedClassSplitHint is what a data-kitwork-class value that does not split may be missing: a name
// the splitter refuses there is usually an arbitrary value (w-[3px]) — a class a toggle cannot name.
// An opacity (bg-red-500/50) is one since 13/10/2026: '/' is a letter of a name.
func groupedClassSplitHint(attribute string, err error) string {
	var grouped *GroupedError
	if attribute != "data-kitwork-class" || !errors.As(err, &grouped) || !strings.Contains(grouped.Reason, "is not a name") {
		return ""
	}
	return " — a toggled class is a plain name: an arbitrary value (w-[3px]) cannot be toggled; write it in class=, or toggle a plain class the stylesheet gives it to"
}

// splitEventName cuts an event pair's name at the dots that are outside a modifier's parentheses:
// "input.debounce(300)" → input, debounce(300); "intersect-enter.margin(0.5rem)" keeps its argument.
func splitEventName(name string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '.':
			if depth == 0 {
				out = append(out, name[start:i])
				start = i + 1
			}
		}
	}
	return append(out, name[start:])
}

// groupedEventProblem says what is wrong with a data-kitwork-event name, and whether the handler is
// dead because of it (fatal) or merely suspect: a name outside the vocabulary is a warning, since the
// kernel then listens for a custom event of that name — one a component may emit.
func groupedEventProblem(name string) (problem string, fatal bool) {
	label := "data-kitwork-event " + name
	segments := splitEventName(name)
	event, modifiers := segments[0], segments[1:]
	for _, modifier := range modifiers {
		if modifier == "" {
			return label + ": has an empty modifier — modifiers follow one dot each: keydown.escape", true
		}
	}
	if !groupedEventNameRe.MatchString(event) {
		return fmt.Sprintf("%s: %q is not an event name (lower case, digits and dashes)", label, event), true
	}
	switch event {
	case "intersect-enter", "intersect-leave":
		if err := checkIntersectModifiers(modifiers, ".", event); err != nil {
			return label + ": " + err.Error(), true
		}
		return "", false
	case "ready":
		if len(modifiers) > 0 {
			return label + ": ready runs once, after the element's first paint — it takes no modifier", true
		}
		return "", false
	case "click-outside":
		for _, modifier := range modifiers {
			if modifier == "outside" {
				return label + ": click-outside already listens outside — .outside says it twice", true
			}
		}
		if err := checkModifierPipeline("click", append([]string{"outside"}, modifiers...), "."); err != nil {
			return label + ": " + err.Error(), true
		}
		return "", false
	}
	dom, known := groupedEventTypes[event]
	if !known {
		dom = event
	}
	if err := checkModifierPipeline(dom, modifiers, "."); err != nil {
		return label + ": " + err.Error(), true
	}
	if !known {
		return fmt.Sprintf("%s: %q is not an event the kernel knows (%s) — it listens for a custom event of that name, which only a component's context.emit fires", label, event, groupedEventVocabulary), false
	}
	return "", false
}

// checkGroupedBindingWrite is checkBindingWrites for a grouped binding: bind, class and style pairs,
// if, and a for's list and key all run on every paint.
func checkGroupedBindingWrite(label string, ir any, where string, into *[]string) {
	if name := firstWrite(ir); name != "" {
		*into = append(*into, fmt.Sprintf(
			"%s writes to %q, but a binding runs on every paint — it would change the state each time the "+
				"page is drawn. Move the write to data-kitwork-event, or give the value its start in "+
				"data-kitwork-scope — in %s", label, name, where))
	}
}
