package stylesheet

import (
	"strings"
	"testing"
)

// A page backdrop that fades to transparent (glow, tint, the scrims, one of them lighter or stronger)
// leaves <html> on the canvas reboot.css gives it, and the browser's bar to the browser: the fixed
// layer's see-through part shows the canvas, as the contrast check composites it (ui, backdrop over
// RootLook), not the first stop as one flat colour over the whole screen. The layer is recorded all
// the same. CONTROL: a backdrop every stop of which paints solid (cirrus, cirrus lighter or stronger, a
// site gradient of pairs) keeps <html>'s ground and the theme-color it is made of.
func TestBackdropThatFadesKeepsTheCanvas(t *testing.T) {
	p, _ := NewPalette(true)
	for _, name := range []string{"glow", "tint", "scrim-top", "scrim-bottom", "glow-50", "glow+20", "fade-top"} {
		var r Rules
		if !NewTable(p).Backdrop(&r, name) {
			t.Fatalf("%s is a gradient of the palette", name)
		}
		css := string(r.CSS())
		if !strings.Contains(css, ":where(.page-backdrop){position:fixed") || strings.Contains(css, ":where(html)") || r.Backdrop() != "" {
			t.Errorf("%s fades to transparent: the layer, no ground for <html>, no theme-color (backdrop %q):\n%s", name, r.Backdrop(), css)
		}
	}

	solid, err := p.WithGradients(map[string]Gradient{"morning": {Type: "linear", Direction: "to-bottom", Stops: []Stop{
		{Pair: Color{Light: "#849cdd", Dark: "#202f5e"}, At: 0}, {Color: "canvas", At: 100}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cirrus", "cirrus-80", "cirrus+20", "dusk", "morning"} {
		var r Rules
		NewTable(solid).Backdrop(&r, name)
		if css := string(r.CSS()); !strings.Contains(css, ":where(html){background-color:") || r.Backdrop() != name {
			t.Errorf("CONTROL: %s paints solid everywhere: <html> takes its first stop, the theme-color too (backdrop %q):\n%s", name, r.Backdrop(), css)
		}
	}
}

// kitworkExample is the palette of the kitwork example site (docs/examples/kitwork, router.work): its
// brand, #f5225f, reaches 3:1 on its canvas and its paper, so its focus ring is the brand.
func kitworkExample(t *testing.T) *Palette {
	t.Helper()
	p, err := NewPalette(true, map[string]Color{
		"brand": {Light: "#f5225f", Dark: "#ff6b6b"}, "canvas": {Light: "#ffeaec", Dark: "#16161e"},
		"paper": {Light: "#ffffff", Dark: "#1e2030"}, "panel": {Light: "#f7eff1", Dark: "#181a26"},
		"line": {Light: "#e5e3e5", Dark: "#2c3145"}, "ink": {Light: "#242442", Dark: "#c0caf5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The keyboard focus ring reaches 3:1 (WCAG 1.4.11) on every point of a page backdrop, in light and in
// dark, on Kit Design's palette and on the kitwork example's: the ring a page resolves to — its own
// page.css's --kitwork-focus where it writes one, else the palette's (colors.css) — measured against
// each of the eleven samples of the backdrop over the canvas, as the contrast check reads them. The
// page writes a ring only where the palette's falls short: never in dark here, where the brand reads.
// CONTROL: the palette's ring alone, the brand, falls short on cirrus in light (#f5225f on #849cdd,
// 1.47:1; Kit Design's brand 1.74:1), so a page that wrote nothing would fail the check.
func TestBackdropFocusRingReads(t *testing.T) {
	for _, palette := range []struct {
		name string
		p    *Palette
	}{{"Kit Design", mustPalette(t)}, {"kitwork", kitworkExample(t)}} {
		p := palette.p
		if p.focus != [2]string{"brand", "brand"} || !strings.Contains(string(Colors(p).CSS), "--kitwork-focus:var(--brand)") {
			t.Fatalf("%s: the palette's own ring is the brand in both modes, got %v", palette.name, p.focus)
		}
		for _, name := range append(Backdrops(), "glow", "tint") {
			var r Rules
			NewTable(p).Backdrop(&r, name)
			css := string(r.CSS())
			ring := map[Mode]string{LightMode: p.focus[0], DarkMode: p.focus[1]}
			if strings.Contains(css, ":root{--kitwork-focus:var(--ink)}\n") {
				ring[LightMode] = "ink"
			}
			if strings.Contains(css, "@media screen{:root[data-theme=dark]{--kitwork-focus:var(--ink)}}\n") {
				ring[DarkMode] = "ink"
			}
			for _, m := range p.Modes() {
				samples, _ := p.GradientSamples(name, m)
				canvas, _ := p.Paint("canvas", m)
				colour, _ := p.Paint(ring[m], m)
				for i, s := range samples {
					if r := Ratio(colour.Painted(), s.Over(canvas.Painted()).Painted()); r < NonTextRatio {
						t.Errorf("%s, %s in %s: the focus ring %s stands %.2f:1 from the backdrop at %d%%, under 3:1", palette.name, name, m.Word(), ring[m], r, i*10)
						break
					}
				}
			}
			if ring[DarkMode] != "brand" {
				t.Errorf("%s, %s: the brand reads in dark, the page writes no ring for it:\n%s", palette.name, name, css)
			}
		}
		samples, _ := p.GradientSamples("cirrus", LightMode)
		brand, _ := p.Paint("brand", LightMode)
		if r := Ratio(brand.Painted(), samples[0].Painted()); r >= NonTextRatio {
			t.Errorf("CONTROL: %s's brand on cirrus's corner in light is %.2f:1, the check needs a ring that falls short", palette.name, r)
		}
	}
}

func mustPalette(t *testing.T) *Palette {
	t.Helper()
	p, err := NewPalette(true)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A message about a name taken by a gradient built into every site says which kind it is: a page
// backdrop is a "nền trang", one of the standard eighteen a "gradient chuẩn", in router.gradients (the
// name itself and its letters written another way) and in router.colors alike. CONTROL: glow, fade-top
// and a token named glow are still the standard set's.
func TestBuiltinNamesSayTheirKind(t *testing.T) {
	message := func(err error) string {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	token := func(name string) error {
		_, err := NewPalette(false, map[string]Color{name: {Light: "#123456"}})
		return err
	}
	for _, c := range []struct{ got, want string }{
		{message(CheckGradient("dusk", Gradient{})), `router.gradients: "dusk" là tên một nền trang của Kitwork, có sẵn: viết background="dusk" hay color="dusk" là có. Gradient riêng đặt tên khác`},
		{message(CheckGradient("stillwater", Gradient{})), `router.gradients: "stillwater" là cách viết khác của nền trang still-water: background="stillwater" là lỗi gợi still-water, không là gradient riêng; đặt tên khác`},
		{message(token("cirrus")), `router.colors: "cirrus" đã là tên một nền trang (background="cirrus"): tên màu và tên gradient chung một chỗ, đặt tên token theo vai trò khác`},
		{message(CheckGradient("glow", Gradient{})), `router.gradients: "glow" là tên một gradient chuẩn của Kitwork, có sẵn: viết background="glow" hay color="glow" là có. Gradient riêng đặt tên khác`},
		{message(CheckGradient("fadetop", Gradient{})), `router.gradients: "fadetop" là cách viết khác của gradient chuẩn fade-top: background="fadetop" là lỗi gợi fade-top, không là gradient riêng; đặt tên khác`},
		{message(token("glow")), `router.colors: "glow" đã là tên một gradient chuẩn (background="glow"): tên màu và tên gradient chung một chỗ, đặt tên token theo vai trò khác`},
	} {
		if c.got != c.want {
			t.Errorf("\n got  %s\n want %s", c.got, c.want)
		}
	}
}
