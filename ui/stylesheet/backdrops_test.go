package stylesheet

import (
	"math"
	"strings"
	"testing"
)

// designedBackdrops are the twelve page backdrops as the owner chose them (decision D1 of 13/10/2026,
// the judge's final set): name, then each stop's light and dark at 0, 34 and 67, written here apart
// from backdrops.go so a changed colour there shows.
var designedBackdrops = []struct {
	name  string
	stops [3][2]string
}{
	{"cirrus", [3][2]string{{"#849cdd", "#202f5e"}, {"#a9bbe0", "#181948"}, {"#cdd9e2", "#0d0f15"}}},
	{"harbor", [3][2]string{{"#72acd5", "#00355a"}, {"#a1c3dc", "#001e4a"}, {"#cfdae2", "#0b1014"}}},
	{"still-water", [3][2]string{{"#6cb3c3", "#003a44"}, {"#9dc7d1", "#002627"}, {"#ccdbdf", "#091113"}}},
	{"sea-mist", [3][2]string{{"#71b8b5", "#003b3a"}, {"#9fcac8", "#002120"}, {"#ccdcdb", "#091111"}}},
	{"spring-rain", [3][2]string{{"#80b9a1", "#003d2b"}, {"#a8cbbb", "#002217"}, {"#cedcd5", "#0a110e"}}},
	{"rain-garden", [3][2]string{{"#97b78c", "#203b16"}, {"#b5c9af", "#082300"}, {"#d3dbd0", "#0d110c"}}},
	{"dune", [3][2]string{{"#ceb183", "#452d0a"}, {"#d6c4a8", "#2a1200"}, {"#ded7cc", "#130f0a"}}},
	{"golden-hour", [3][2]string{{"#d6a47e", "#4f2700"}, {"#dcbda6", "#311100"}, {"#e1d6cd", "#130e0a"}}},
	{"rose-haze", [3][2]string{{"#d7979e", "#541e28"}, {"#ddb6ba", "#380512"}, {"#e3d4d5", "#140d0e"}}},
	{"dusk", [3][2]string{{"#ca92b9", "#4c2041"}, {"#d5b3cb", "#310828"}, {"#e0d4dc", "#130d11"}}},
	{"evensong", [3][2]string{{"#b894ce", "#412452"}, {"#cab5d7", "#300f3a"}, {"#dcd5e0", "#110e13"}}},
	{"twilight", [3][2]string{{"#a097d9", "#33295a"}, {"#bcb7de", "#2d113d"}, {"#d8d6e3", "#0f0f14"}}},
}

// The page backdrops are the designed set: twelve names in their order round the hue wheel, each a
// linear ramp to the bottom end with a { light, dark } pair at 0, 34 and 67, exactly the chosen colours;
// each reads as a gradient under another name, and none is named like a colour, a token of the default
// palette or a standard gradient. They are every palette's gradients, and the standard set stays the
// eighteen.
func TestBackdropsAreTheDesignedSet(t *testing.T) {
	var names []string
	for _, d := range designedBackdrops {
		names = append(names, d.name)
	}
	if got, want := strings.Join(Backdrops(), " "), strings.Join(names, " "); got != want {
		t.Fatalf("the backdrops\n got %s\nwant %s", got, want)
	}
	p, _ := NewPalette(true)
	for _, d := range designedBackdrops {
		g, ok := p.Gradient(d.name)
		if !ok {
			t.Errorf("%s is no gradient of the palette", d.name)
			continue
		}
		if g.Type != "linear" || g.Direction != "to-bottom-end" || len(g.Stops) != 3 {
			t.Errorf("%s is %s %s with %d stops, want linear to-bottom-end with 3", d.name, g.Type, g.Direction, len(g.Stops))
			continue
		}
		for i, at := range []float64{0, 34, 67} {
			s := g.Stops[i]
			if s.At != at || s.Color != "" || s.Pair != (Color{Light: d.stops[i][0], Dark: d.stops[i][1]}) {
				t.Errorf("%s, stop %d: %+v, want { light: %s, dark: %s } at %v", d.name, i+1, s, d.stops[i][0], d.stops[i][1], at)
			}
		}
		if err := CheckGradient("my-"+d.name, g); err != nil {
			t.Errorf("%s does not read as a gradient: %v", d.name, err)
		}
		if _, named := NamedColour(d.name); named || p.Has(d.name) || fixedColors[d.name] != "" || indexOf(StandardGradients(), d.name) >= 0 {
			t.Errorf("%s is also a colour's or a standard gradient's name", d.name)
		}
		if _, _, ok := namedOtherwise(d.name); ok {
			t.Errorf("%s is a colour's name written another way", d.name)
		}
	}
	if len(StandardGradients()) != 18 {
		t.Errorf("the standard set is the eighteen: %v", StandardGradients())
	}
}

// The backdrops read as they were designed, measured by the engine's own sampling (eleven samples, each
// stop the pair's colour of the mode, 8 bits a channel) on the three palettes they were designed on, in
// light and in dark: ink on every sample at least 4.5:1, and on a glass card over every sample (paper
// at 75%, composited in sRGB as the browser draws glass where it cannot blur) ink and ink-30 at least
// 4.5:1 too. CONTROL: ink-30 straight on a backdrop falls short in light on every one of them — the
// design keeps secondary text on glass — so the measurement tells the two apart.
func TestBackdropsReadAsDesigned(t *testing.T) {
	palettes := designPalettes(t)
	glassOver := func(p *Palette, sample RGBA, m Mode) RGBA {
		paper, _ := p.Paint("paper/75", m)
		return paper.Over(sample.Painted()).Painted()
	}
	for _, name := range Backdrops() {
		ink, glassInk, glassInk70 := math.Inf(1), math.Inf(1), math.Inf(1)
		bare := math.Inf(1)
		for _, p := range palettes {
			for _, m := range p.Modes() {
				samples, ok := p.GradientSamples(name, m)
				if !ok {
					t.Fatalf("%s: no samples", name)
				}
				text, _ := p.Paint("ink", m)
				secondary, _ := p.Paint("ink-30", m)
				for _, s := range samples {
					ground := s.Painted()
					ink = math.Min(ink, Ratio(text.Painted(), ground))
					glass := glassOver(p, s, m)
					glassInk = math.Min(glassInk, Ratio(text.Painted(), glass))
					glassInk70 = math.Min(glassInk70, Ratio(secondary.Painted(), glass))
					if m == LightMode {
						bare = math.Min(bare, Ratio(secondary.Painted(), ground))
					}
				}
			}
		}
		if ink < 4.5 || glassInk < 4.5 || glassInk70 < 4.5 {
			t.Errorf("%s: ink %.2f:1, on glass ink %.2f:1 and ink-30 %.2f:1, want each at least 4.5:1", name, ink, glassInk, glassInk70)
		}
		if bare >= 4.5 {
			t.Errorf("control: ink-30 straight on %s is %.2f:1 in light, the design measured it short", name, bare)
		}
		t.Logf("%-12s ink %.2f:1, glass ink %.2f:1, glass ink-30 %.2f:1; ink-30 bare %.2f:1", name, ink, glassInk, glassInk70, bare)
	}
}

// A gradient's stop may be a { light, dark } pair (decision D2 of 13/10/2026), painted per mode: cirrus
// is #849cdd at its corner in light and #202f5e in dark, its far end #cdd9e2 and #0d0f15. On the web each
// pair is a custom property of its own, set on :root by the page's stylesheet in light and again under
// :root[data-theme=dark] for screens (as colors.css sets a token), and the image names it. A site's
// gradient may mix pairs and tokens, and a pair's side may be a named colour (tomato is #ff6347) or
// #rgb. A palette with no dark look writes no dark block. A pair is checked as router.colors checks a
// token: both sides written, each a colour. CONTROL: the same gradient's token stop is still var(--canvas).
func TestGradientPairsPerMode(t *testing.T) {
	p, _ := NewPalette(true)
	for _, c := range []struct {
		m           Mode
		first, last string
	}{{LightMode, "#849cdd", "#cdd9e2"}, {DarkMode, "#202f5e", "#0d0f15"}} {
		samples, ok := p.GradientSamples("cirrus", c.m)
		if !ok || samples[0].Hex() != c.first || samples[len(samples)-1].Hex() != c.last || samples[8].Hex() != c.last {
			t.Errorf("cirrus in %s: %v, want %s at 0 and %s from 67%% on", c.m.Word(), sampleHexes(samples), c.first, c.last)
		}
	}
	css := pageCSS(t, NewTable(p), Intent{Name: "background", Value: "cirrus"}, Intent{Name: "color", Value: "cirrus"})
	for _, line := range []string{
		":root{--kitwork-gradient-cirrus-1:#849cdd;--kitwork-gradient-cirrus-2:#a9bbe0;--kitwork-gradient-cirrus-3:#cdd9e2}",
		"@media screen{:root[data-theme=dark]{--kitwork-gradient-cirrus-1:#202f5e;--kitwork-gradient-cirrus-2:#181948;--kitwork-gradient-cirrus-3:#0d0f15}}",
		".background-cirrus{background-image:linear-gradient(to bottom right,var(--kitwork-gradient-cirrus-1) 0%,var(--kitwork-gradient-cirrus-2) 34%,var(--kitwork-gradient-cirrus-3) 67%)}",
		".color-cirrus{color:var(--kitwork-gradient-cirrus-1)}",
	} {
		if !strings.Contains(css, "\n"+line+"\n") {
			t.Errorf("missing\n%s\nin\n%s", line, css)
		}
	}
	if !strings.HasPrefix(css, LayerStatement+"\n:root{") {
		t.Errorf("the pairs come right after the layer statement, unlayered:\n%s", css)
	}
	light, _ := NewPalette(false)
	if css := pageCSS(t, NewTable(light), Intent{Name: "background", Value: "cirrus"}); strings.Contains(css, "data-theme=dark") || !strings.Contains(css, "--kitwork-gradient-cirrus-1:#849cdd") {
		t.Errorf("a palette with no dark look sets the light pairs only:\n%s", css)
	}

	site, err := p.WithGradients(map[string]Gradient{"sky": {Type: "linear", Direction: "to-bottom", Stops: []Stop{
		{Pair: Color{Light: "#123", Dark: "tomato"}, At: 0}, {Color: "canvas", At: 100}}}})
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := site.GradientSamples("sky", LightMode); l[0].Hex() != "#112233" {
		t.Errorf("sky's pair in light: %s, want #112233", l[0].Hex())
	}
	if d, _ := site.GradientSamples("sky", DarkMode); d[0].Hex() != "#ff6347" {
		t.Errorf("sky's pair in dark is the named tomato: %s, want #ff6347", d[0].Hex())
	}
	sky := pageCSS(t, NewTable(site), Intent{Name: "background", Value: "sky"})
	for _, want := range []string{"--kitwork-gradient-sky-1:#112233", "{--kitwork-gradient-sky-1:#ff6347}}",
		".background-sky{background-image:linear-gradient(to bottom,var(--kitwork-gradient-sky-1) 0%,var(--canvas) 100%)}"} {
		if !strings.Contains(sky, want) {
			t.Errorf("missing %q in\n%s", want, sky)
		}
	}
	if strings.Contains(sky, "--kitwork-gradient-sky-2") {
		t.Errorf("a token stop is no pair:\n%s", sky)
	}

	ramp := func(s Stop) Gradient {
		return Gradient{Type: "linear", Direction: "to-end", Stops: []Stop{s, {Color: "canvas", At: 100}}}
	}
	for stop, want := range map[Stop]string{
		{Pair: Color{Light: "#849cdd"}}:                                  `router.gradients: "hero" điểm dừng 1: cặp màu thiếu dark: color { light, dark } viết cả hai`,
		{Pair: Color{Dark: "#202f5e"}}:                                   `router.gradients: "hero" điểm dừng 1: cặp màu thiếu light`,
		{Pair: Color{Light: "#12", Dark: "#202f5e"}}:                     `router.gradients: "hero" điểm dừng 1: light là "#12": một màu của cặp là "#rgb", "#rrggbb" hay một màu có tên`,
		{Pair: Color{Light: "#849cdd", Dark: "brand"}}:                   `điểm dừng 1: dark là "brand": một màu của cặp là "#rgb", "#rrggbb" hay một màu có tên (tomato, cerulean…); token (brand, ink-30) thì viết color: "brand"`,
		{Pair: Color{Light: "#849cdd", Dark: "slate-gray"}}:              `điểm dừng 1: dark là "slate-gray": slate-gray không phải tên màu: tên là slategray`,
		{Color: "brand", Pair: Color{Light: "#849cdd", Dark: "#202f5e"}}: `điểm dừng 1 có cả color "brand" và một cặp { light, dark }`,
	} {
		if err := CheckGradient("hero", ramp(stop)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v:\n got  %v\n want %s", stop, err, want)
		}
	}
	if err := CheckGradient("hero", ramp(Stop{Pair: Color{Light: "#849CDD", Dark: "navy"}})); err != nil {
		t.Errorf("control: a pair of a hex in capitals and a named colour reads: %v", err)
	}
}

func sampleHexes(samples []RGBA) []string {
	out := make([]string, len(samples))
	for i, s := range samples {
		out[i] = s.Hex()
	}
	return out
}
