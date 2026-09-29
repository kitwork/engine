package hydrate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// The grammar is deliberately quiet at runtime: an expression that names something which is not
// there reads as 0, and a call on a non-function returns undefined, so a mistyped handler can never
// break a page a visitor is looking at. That is the right trade for the browser and the wrong one
// for the author — nothing is thrown, nothing is logged, the button simply does nothing.
//
// So the loud half belongs here, where the server holds the whole document, the same parser the
// kernel runs, and the list of everything each service grants. Everything below is a mistake made
// in this repo, each of which cost an hour to find by hand:
//
//   · kit.theme.system() — on the service, missing from its expression grant (25/09)
//   · $element.nosuch.textContent — copy(undefined) reaching the clipboard (25/09)
//   · kit.permissions.request(…) — a service markup was never given

// serviceGrantRe reads a grant out of the module that declares it:
//
//	kit.service("theme", theme, {
//	  expression: ["mode", "resolved", "set", "toggle", "system"]
//	});
//
// Reading the JS rather than keeping a Go copy means the two cannot drift: adding a member to the
// grant is the only edit, and this pass sees it on the next build.
//
// Each call is read on its OWN span — from `kit.service(` to the `);` that closes it. A single
// regex across the file cannot be trusted with that: the first version used a lazy `.*?` and still
// ran from `kit.service("host", host);`, which grants nothing, straight through to the NEXT grant
// it could find, so `window` was reported as granting nothing while it grants five members. A sweep
// over every page in the repo is what showed it — as three false alarms on a real titlebar.
var serviceGrantRe = regexp.MustCompile(`(?s)expression:\s*\[([^\]]*)\]`)

// serviceNameRe finds every service the kernel registers, granted or not — so a service that markup
// cannot reach at all can be named as exactly that, instead of blaming the member.
var serviceNameRe = regexp.MustCompile(`kit\.service\(\s*"([A-Za-z][A-Za-z0-9]*)"`)

var (
	grantsOnce  sync.Once
	grantedBy   map[string][]string // service → members markup may use
	knownServic map[string]bool     // every service the kernel registers
)

func loadGrants() {
	grantsOnce.Do(func() {
		grantedBy = map[string][]string{}
		knownServic = map[string]bool{}
		modules := webJS + "\n" + nativeJS + "\n" + storageJS
		for _, at := range serviceNameRe.FindAllStringSubmatchIndex(modules, -1) {
			name := modules[at[2]:at[3]]
			knownServic[name] = true
			// This call's own span: up to the `);` that closes it. A grant object holds no `);`.
			span := modules[at[0]:]
			if end := strings.Index(span, ");"); end >= 0 {
				span = span[:end]
			}
			grant := serviceGrantRe.FindStringSubmatch(span)
			if grant == nil {
				continue
			}
			members := []string{}
			for _, raw := range strings.Split(grant[1], ",") {
				if member := strings.Trim(strings.TrimSpace(raw), `"'`); member != "" {
					members = append(members, member)
				}
			}
			if len(members) > 0 {
				grantedBy[name] = members
			}
		}
	})
}

// kitReadRe finds `kit.<service>.<member>` in an expression. The kernel's own intrinsics —
// platform, isNative, mode, version — are read straight off kit and have no service.
var kitReadRe = regexp.MustCompile(`\bkit\.([A-Za-z][A-Za-z0-9]*)\.([A-Za-z][A-Za-z0-9]*)`)

var kitIntrinsics = map[string]bool{"platform": true, "isNative": true, "mode": true, "version": true}

func checkKitReads(expression, where string, into *[]string) {
	loadGrants()
	for _, m := range kitReadRe.FindAllStringSubmatch(expression, -1) {
		service, member := m[1], m[2]
		if kitIntrinsics[service] {
			continue
		}
		granted, hasGrant := grantedBy[service]
		if !hasGrant {
			if knownServic[service] {
				*into = append(*into, fmt.Sprintf(
					"kit.%s is not available to markup — the kernel registers it but grants nothing to "+
						"expressions, so kit.%s.%s reads as undefined and this does nothing. Call it from "+
						"component JavaScript instead — in %s", service, service, member, where))
			} else {
				*into = append(*into, fmt.Sprintf(
					"kit.%s is not a service the kernel registers, so kit.%s.%s reads as undefined and "+
						"this does nothing — in %s", service, service, member, where))
			}
			continue
		}
		found := false
		for _, name := range granted {
			if name == member {
				found = true
				break
			}
		}
		if !found {
			*into = append(*into, fmt.Sprintf(
				"kit.%s.%s is not granted to markup, so it reads as undefined and this does nothing. "+
					"kit.%s offers: %s — in %s",
				service, member, service, strings.Join(granted, ", "), where))
		}
	}
}

// elementNameRe reads `$element.<name>`, and declaredElementRe reads every data-kit-element in the
// document. The check is document-wide rather than per boundary: the kernel resolves $element
// within the acting boundary, so a name declared in ANOTHER boundary still resolves to nothing —
// but a name declared nowhere at all is unambiguously a typo, and that is the case worth being
// certain about.
var (
	elementNameRe     = regexp.MustCompile(`\$element\.([A-Za-z_][A-Za-z0-9_]*)`)
	declaredElementRe = regexp.MustCompile(`data-kit-element="([A-Za-z_][A-Za-z0-9_]*)"`)
)

func checkElementReads(expression, where string, declared map[string]bool, into *[]string) {
	// nil means the HTML was a fragment: a name declared in a sibling slot is invisible from here,
	// and saying "nothing declares it" would be a guess dressed as a finding.
	if declared == nil {
		return
	}
	for _, m := range elementNameRe.FindAllStringSubmatch(expression, -1) {
		if declared[m[1]] {
			continue
		}
		names := make([]string, 0, len(declared))
		for name := range declared {
			names = append(names, name)
		}
		sort.Strings(names)
		offer := "the page declares none"
		if len(names) > 0 {
			offer = "the page declares: " + strings.Join(names, ", ")
		}
		*into = append(*into, fmt.Sprintf(
			"$element.%s names an element nothing declares with data-kit-element, so it reads as "+
				"nothing and whatever this expression does with it is lost — %s — in %s",
			m[1], offer, where))
	}
}

// exampleContentRe finds the INSIDE of a <pre> or <code> block. Documentation prints the grammar
// there — kitjs.org's installation page shows `data-kit-click="count++"` as an example — and an
// example is text, not a directive. Only the content goes: the element's own attributes stay, so a
// live <pre data-kit-text="…"> is still judged.
var exampleContentRe = regexp.MustCompile(`(?s)(<(?:pre|code)\b[^>]*>).*?(</(?:pre|code)>)`)

func withoutExamples(html string) string {
	if !strings.Contains(html, "<pre") && !strings.Contains(html, "<code") {
		return html
	}
	return exampleContentRe.ReplaceAllString(html, "$1$2")
}

func declaredElements(html string) map[string]bool {
	if !wholeDocument(html) {
		return nil
	}
	declared := map[string]bool{}
	for _, m := range declaredElementRe.FindAllStringSubmatch(html, -1) {
		declared[m[1]] = true
	}
	return declared
}

// A binding runs on every paint. data-kit-text, show, if, class, bind:<name> and style:<property>
// answer the question "what should this look like now?", and one that also WRITES state changes the
// data each time the page is drawn — which draws again. The standalone KitJS put it as a rule of the
// grammar: a binding is read-only; an action (an event handler) may assign or apply ++/--. The
// kernel has no action/binding distinction at runtime to hang that on, so the rule lives here.
//
// A write inside a lambda counts too. It is tempting to say `() => count++` only DEFINES an action,
// but in a binding a lambda is there to be called — items.map((x) => seen++) runs the write once
// per item, on every paint. The first version of this check skipped lambda bodies and missed that.
func bindingDirective(kind string) bool {
	switch kind {
	case "text", "show", "if", "class":
		return true
	}
	return strings.HasPrefix(kind, "bind:") || strings.HasPrefix(kind, "style:")
}

func firstWrite(node any) string {
	parts, ok := node.([]any)
	if !ok || len(parts) == 0 {
		return ""
	}
	switch op, _ := parts[0].(string); op {
	case "=", "=$", "++", "--", "++$", "--$":
		name, _ := parts[1].(string)
		return name
	}
	for _, child := range parts[1:] {
		if name := firstWrite(child); name != "" {
			return name
		}
		if list, ok := child.([]any); ok && !isNode(list) {
			for _, item := range list {
				if name := firstWrite(item); name != "" {
					return name
				}
			}
		}
	}
	return ""
}

func checkBindingWrites(kind string, ir any, where string, into *[]string) {
	if !bindingDirective(kind) {
		return
	}
	if name := firstWrite(ir); name != "" {
		*into = append(*into, fmt.Sprintf(
			"data-kit-%s writes to %q, but a binding runs on every paint — it would change the state "+
				"each time the page is drawn. Move the write to an event (data-kit-click) or give the "+
				"value its start in data-kit-scope — in %s", kind, name, where))
	}
}
