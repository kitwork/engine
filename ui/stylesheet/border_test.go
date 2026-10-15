package stylesheet

import (
	"strings"
	"testing"
)

// edgeOf says what one declaration of reboot.css draws as an edge around an element, "" for nothing:
// a border of no width or of a transparent colour draws nothing a screen shows (the 1px transparent
// edge of a button and a field keeps every box the same and is what a contrast theme draws), a
// border's colour is an edge whatever rule gives the width, a width alone is drawn in the text's
// colour (the reset leaves the style solid), and an inset shadow is an edge drawn another way. The
// corners, a table's collapse and spacing, and the focus ring (an outline, a state) are not edges.
func edgeOf(property, value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "!important"))
	words := strings.Fields(value)
	invisible := func() bool {
		for _, w := range words {
			if w == "transparent" || w == "none" || w == "hidden" {
				return true
			}
		}
		return len(words) > 0 && (words[0] == "0" || words[0] == "0px")
	}
	switch {
	case property == "box-shadow":
		if strings.Contains(value, "inset") {
			return property + ":" + value
		}
		return ""
	case !strings.HasPrefix(property, "border"):
		return ""
	case strings.HasSuffix(property, "-radius"), property == "border-collapse", property == "border-spacing",
		strings.HasSuffix(property, "-style"):
		return ""
	case strings.HasSuffix(property, "-width"), strings.HasSuffix(property, "-color"),
		strings.HasPrefix(property, "border-image"):
		if invisible() {
			return ""
		}
		return property + ":" + value
	}
	// the shorthands: border, border-top, border-block-end, border-inline-start…
	if invisible() {
		return ""
	}
	return property + ":" + value
}

// defaultEdges lists every edge reboot.css, compacted, draws on an element before the author writes
// anything: selector { property:value }.
func defaultEdges(css string) []string {
	var out []string
	for _, rule := range styleRules(css, "") {
		for _, declaration := range strings.Split(rule.body, ";") {
			property, value, _ := strings.Cut(declaration, ":")
			if edge := edgeOf(strings.TrimSpace(property), value); edge != "" {
				out = append(out, rule.selector+" { "+edge+" }")
			}
		}
	}
	return out
}

// No element has a border of its own (decision B1, 12/10/2026): an author who wants one writes
// border="line". Every rule of reboot.css is read, and none draws an edge; the button's and the field's
// 1px edge stays, transparent. The table of looks reads colours, surfaces and type, never an edge, so
// reboot.css is where one could come back. CONTROLS: the quote's rule on its start edge, the field's
// and the popover's hairline, a colour on a button's edge, a width alone and an inset shadow, each put
// back, are each found.
func TestNoElementHasABorderOfItsOwn(t *testing.T) {
	css := string(compact(rebootSource))
	if edges := defaultEdges(css); len(edges) > 0 {
		t.Errorf("reboot.css draws a border nobody wrote:\n%s", strings.Join(edges, "\n"))
	}
	for _, kept := range []string{
		":where(button){--kitwork-horizontal:center;padding:0.5rem 0.75rem;border:1px solid transparent;",
		"border:1px solid transparent;border-radius:0.375rem;background:color-mix(in srgb,var(--ink) 5%,transparent)}",
		// a contrast theme drops the popover's shadow and repaints the quote's panel: the edge it draws
		// in the system's colour is all that still sets them apart (review of 12/10/2026)
		"min-inline-size:12rem;background:var(--paper);border:1px solid transparent;",
		":where(blockquote){gap:0.5rem;padding:1rem;border:1px solid transparent;",
	} {
		if !strings.Contains(css, kept) {
			t.Errorf("the transparent 1px edge of a button, a field, a popover or a quote is gone (a contrast theme draws it): no %q", kept)
		}
	}
	for name, changed := range map[string]string{
		"the quote's rule":          strings.Replace(css, ":where(blockquote){", ":where(blockquote){border-inline-start:4px solid var(--line);", 1),
		"the field's hairline":      strings.Replace(css, "border:1px solid transparent;border-radius:0.375rem;background:color-mix(", "border:1px solid var(--line);border-radius:0.375rem;background:color-mix(", 1),
		"the popover's hairline":    strings.Replace(css, ":where(.popover){", ":where(.popover){border:1px solid var(--line);", 1),
		"a colour on a button edge": strings.Replace(css, ":where(textarea){resize:vertical}", ":where(textarea){resize:vertical}:where(button){border-color:var(--line)}", 1),
		"a width alone":             strings.Replace(css, ":where(.card){", ":where(.card){border-width:1px;", 1),
		"an inset shadow":           strings.Replace(css, ":where(.card){", ":where(.card){box-shadow:inset 0 0 0 1px var(--line);", 1),
	} {
		if changed == css {
			t.Fatalf("CONTROL %s: the replacement did not apply", name)
		}
		if len(defaultEdges(changed)) <= len(defaultEdges(css)) {
			t.Errorf("CONTROL %s: the check finds nothing", name)
		}
	}
}
