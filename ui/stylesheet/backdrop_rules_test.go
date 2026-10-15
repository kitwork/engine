package stylesheet

import (
	"strings"
	"testing"
)

// Table.Backdrop records what a page whose <page> paints a gradient needs besides its classes (decision
// D4 of 13/10/2026), first in the page layer, before every class: <html>'s ground in the gradient's
// first stop (the pair's custom property; lighter, the mixed one) and the layer's rule — fixed to
// the screen, inset 0, at least the large viewport tall, under everything, taking no pointer. Rules
// remembers the gradient for the document's theme-color, and Palette.GradientGround paints the same
// first stop, solid, per mode (an alpha dropped: fade-top's canvas/0 is the canvas). A ground that is
// no gradient (a colour written beside a gradient for a width) records the layer alone. CONTROL: a page
// with no backdrop has neither rule.
func TestBackdropRules(t *testing.T) {
	p, _ := NewPalette(true)
	table := NewTable(p)
	var r Rules
	if !table.Backdrop(&r, " cirrus ") {
		t.Fatal("cirrus is a gradient of the palette")
	}
	classes(t, table, &r, Intent{Name: "background", Value: "cirrus"})
	css := string(r.CSS())
	want := "@layer page{\n:where(html){background-color:var(--kitwork-gradient-cirrus-1)}\n" +
		":where(.page-backdrop){position:fixed;inset:0;min-block-size:100lvh;z-index:-1;pointer-events:none}\n.background-cirrus{"
	if !strings.Contains(css, want) {
		t.Errorf("the ground and the layer come first in the page layer:\nwant %s\nin\n%s", want, css)
	}
	if r.Backdrop() != "cirrus" {
		t.Errorf("the page's backdrop is %q, want cirrus", r.Backdrop())
	}

	var lighter Rules
	table.Backdrop(&lighter, "harbor-30")
	if css := string(lighter.CSS()); !strings.Contains(css, ":where(html){background-color:color-mix(in oklab,var(--kitwork-gradient-harbor-1) 70%,var(--canvas))}") ||
		!strings.Contains(css, "--kitwork-gradient-harbor-1:#72acd5") || lighter.Backdrop() != "harbor-30" {
		t.Errorf("a lighter backdrop's ground is its mixed first stop, its pair set:\n%s", css)
	}

	var coloured Rules
	if table.Backdrop(&coloured, "paper") {
		t.Error("paper is no gradient")
	}
	if css := string(coloured.CSS()); !strings.Contains(css, ":where(.page-backdrop){position:fixed") || strings.Contains(css, ":where(html)") || coloured.Backdrop() != "" {
		t.Errorf("a ground that is a colour records the layer alone:\n%s", css)
	}

	for _, c := range []struct {
		name string
		m    Mode
		hex  string
	}{{"cirrus", LightMode, "#849cdd"}, {"cirrus", DarkMode, "#202f5e"}, {"dusk", DarkMode, "#4c2041"}, {"fade-top", LightMode, "#f6f9fc"}} {
		ground, ok := p.GradientGround(c.name, c.m)
		if !ok || ground.Hex() != c.hex || ground.A != 1 {
			t.Errorf("%s's ground in %s is %s at %.2f, want %s, solid", c.name, c.m.Word(), ground.Hex(), ground.A, c.hex)
		}
	}
	if _, ok := p.GradientGround("paper", LightMode); ok {
		t.Error("paper has no gradient ground")
	}

	var plain Rules
	classes(t, table, &plain, Intent{Name: "background", Value: "cirrus"})
	if css := string(plain.CSS()); strings.Contains(css, "page-backdrop") || strings.Contains(css, ":where(html)") || plain.Backdrop() != "" {
		t.Errorf("control: a gradient on a box records no backdrop:\n%s", css)
	}
}
