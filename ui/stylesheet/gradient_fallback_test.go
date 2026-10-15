package stylesheet

import (
	"strings"
	"testing"
)

// Gradient text whose stops hold a color-mix() (lighter, an alpha) is clipped to the letters only where
// the browser reads color-mix() too: the @supports asks for both. A browser that clips to text but has no
// color-mix() (Chrome and Edge 99–110, Safari 15.4–16.1) would drop both images and keep the transparent
// fill, the text gone (review of 12/10/2026); with the condition it keeps the solid colour before it. The
// right-to-left rule holds under the same condition. CONTROL: a gradient of plain colours (headline,
// magma) keeps the condition it had, since any browser that clips reads its images; a background is
// never wrapped (without its image the element keeps its colour).
func TestGradientTextWithAMixedStopNeedsColorMix(t *testing.T) {
	const mixedCondition = "@supports ((background-clip:text) or (-webkit-background-clip:text)) and (color:color-mix(in srgb,red,red))"
	p, _ := NewPalette(false)
	site, err := p.WithGradients(map[string]Gradient{
		"sitetext": {Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "brand-30", At: 0}, {Color: "ink-10", At: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	table := NewTable(site)
	css := pageCSS(t, table, Intent{Name: "color", Value: "sitetext"})
	for _, line := range []string{
		`.color-sitetext{color:color-mix(in oklab,var(--brand) 70%,var(--canvas))}`,
		mixedCondition + `{.color-sitetext{background-image:linear-gradient(to right,`,
		mixedCondition + `{.color-sitetext:where(:dir(rtl)){background-image:linear-gradient(to left,`,
		`@media (forced-colors:active){.color-sitetext{background-image:none;-webkit-text-fill-color:currentcolor}}`,
	} {
		if !strings.Contains(css, "\n"+line) {
			t.Errorf("gradient text with a lighter stop has\n%s\nin\n%s", line, css)
		}
	}
	if strings.Count(css, "@supports (background-clip:text) or") != 0 {
		t.Errorf("no rule of gradient text with a color-mix() stop holds where color-mix() is not read:\n%s", css)
	}
	for _, name := range []string{"glow", "tint", "skeleton", "hairline", "fade-end"} {
		css := pageCSS(t, table, Intent{Name: "color", Value: name})
		if !strings.Contains(css, mixedCondition+"{.color-"+name+"{background-image:") || strings.Contains(css, "@supports (background-clip:text) or") {
			t.Errorf("%s as text: its stops are color-mix(), its rule needs color-mix() read:\n%s", name, css)
		}
	}
	for _, name := range []string{"headline", "magma"} {
		css := pageCSS(t, table, Intent{Name: "color", Value: name})
		if !strings.Contains(css, "\n@supports (background-clip:text) or (-webkit-background-clip:text){.color-"+name+"{") || strings.Contains(css, "color-mix") {
			t.Errorf("control: %s has no color-mix() stop, its condition is the clip alone:\n%s", name, css)
		}
	}
	if css := pageCSS(t, table, Intent{Name: "background", Value: "glow"}); strings.Contains(css, "@supports") {
		t.Errorf("control: a gradient background is never wrapped:\n%s", css)
	}
}
