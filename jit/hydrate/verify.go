package hydrate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The grammar is deliberately quiet at runtime: an expression that names something which is not
// there reads as 0, and a call on a non-function returns undefined, so a mistyped handler can never
// break a page a visitor is looking at. That is the right trade for the browser and the wrong one
// for the author — nothing is thrown, nothing is logged, the button simply does nothing.
//
// So the loud half belongs here, where the server holds the whole document and the same parser the
// kernel runs. Everything below is a mistake made in this repo, each of which cost an hour to find
// by hand:
//
//   · kit.theme.system() — on the service, missing from markup's grant (25/09); since 03/10 markup
//     has no kit at all, and any kit.<service> in an expression is named
//   · $element.nosuch.textContent — copy(undefined) reaching the clipboard (25/09)

// Markup never calls kit (03/10): a service reaches the page through the component that is its view,
// and the kernel gives expressions no kit at all. kit.theme.toggle() would then read as nothing and
// do nothing, in silence — so it is named here, with where the service is reached instead.
var kitReadRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_$.])kit\s*\??\.\s*([A-Za-z][A-Za-z0-9]*)`)

// kitViews is the component that is each service's view, for the services a page reaches for most.
var kitViews = map[string]string{
	"theme":      "the theme component",
	"progress":   "the progressbar component",
	"window":     "the titlebar component",
	"navigation": "the titlebar component",
	"clipboard":  "the clipboard component",
}

func checkKitReads(expression, where string, into *[]string) {
	for _, m := range kitReadRe.FindAllStringSubmatch(withoutStringLiterals(expression), -1) {
		via, known := kitViews[m[1]]
		if !known {
			via = "a component whose JavaScript calls kit." + m[1]
		}
		*into = append(*into, fmt.Sprintf(
			"markup does not call kit — kit.%s reads as nothing in an expression, so this does nothing. "+
				"Reach it through %s — in %s", m[1], via, where))
	}
}

// elementNameRe reads `$element.<name>`, and declaredElementRe reads every data-kit-element in the
// document. The check is document-wide rather than per boundary: the kernel resolves $element
// within the acting boundary, so a name declared in ANOTHER boundary still resolves to nothing —
// but a name declared nowhere at all is unambiguously a typo, and that is the case worth being
// certain about.
var (
	elementNameRe     = regexp.MustCompile(`\$element\??\.([A-Za-z_][A-Za-z0-9_]*)`)
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

// WithoutExamples is withoutExamples for jit/js, which scans a page for the components and native
// services it names: a docs page that printed data-kit-component="visible" downloaded visible until
// 02/10.
func WithoutExamples(html string) string { return withoutExamples(html) }

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
