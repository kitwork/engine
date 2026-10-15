package stylesheet

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// cssOklab is OKLab by CSS Color 4's own sample code, apart from the engine's (contrast.go goes from
// linear sRGB to LMS in one matrix, Ottosson's): linear sRGB to XYZ D65 with the spec's rational matrix,
// XYZ to LMS, the cube root, LMS to OKLab; and back through the inverse matrices, solved here.
type cssOklab struct{ l, a, b, alpha float64 }

var (
	cssRGBToXYZ = [3][3]float64{
		{506752.0 / 1228815, 87881.0 / 245763, 12673.0 / 70218},
		{87098.0 / 409605, 175762.0 / 245763, 12673.0 / 175545},
		{7918.0 / 409605, 87881.0 / 737289, 1001167.0 / 1053270},
	}
	cssXYZToLMS = [3][3]float64{
		{0.8190224379967030, 0.3619062600528904, -0.1288737815209879},
		{0.0329836539323885, 0.9292868615863434, 0.0361446663506424},
		{0.0481771893596242, 0.2642395317527308, 0.6335478284694309},
	}
	cssLMSToOklab = [3][3]float64{
		{0.2104542683093140, 0.7936177747023054, -0.0040720430116193},
		{1.9779985324311684, -2.4285922420485799, 0.4505937096174110},
		{0.0259040424655478, 0.7827717124575296, -0.8086757549230774},
	}
)

func times(m [3][3]float64, v [3]float64) [3]float64 {
	var out [3]float64
	for i := range out {
		out[i] = m[i][0]*v[0] + m[i][1]*v[1] + m[i][2]*v[2]
	}
	return out
}

func inverse(m [3][3]float64) [3][3]float64 {
	a, b, c := m[0][0], m[0][1], m[0][2]
	d, e, f := m[1][0], m[1][1], m[1][2]
	g, h, i := m[2][0], m[2][1], m[2][2]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	return [3][3]float64{
		{(e*i - f*h) / det, (c*h - b*i) / det, (b*f - c*e) / det},
		{(f*g - d*i) / det, (a*i - c*g) / det, (c*d - a*f) / det},
		{(d*h - e*g) / det, (b*g - a*h) / det, (a*e - b*d) / det},
	}
}

func toCSSOklab(c RGBA) cssOklab {
	linear := func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	lms := times(cssXYZToLMS, times(cssRGBToXYZ, [3]float64{linear(c.R), linear(c.G), linear(c.B)}))
	ok := times(cssLMSToOklab, [3]float64{math.Cbrt(lms[0]), math.Cbrt(lms[1]), math.Cbrt(lms[2])})
	return cssOklab{ok[0], ok[1], ok[2], c.A}
}

func fromCSSOklab(c cssOklab) RGBA {
	lms := times(inverse(cssLMSToOklab), [3]float64{c.l, c.a, c.b})
	rgb := times(inverse(cssRGBToXYZ), times(inverse(cssXYZToLMS), [3]float64{lms[0] * lms[0] * lms[0], lms[1] * lms[1] * lms[1], lms[2] * lms[2] * lms[2]}))
	gamma := func(v float64) float64 {
		if v <= 0.0031308 {
			return math.Max(0, 12.92*v)
		}
		return math.Min(1, 1.055*math.Pow(v, 1/2.4)-0.055)
	}
	return RGBA{R: gamma(rgb[0]), G: gamma(rgb[1]), B: gamma(rgb[2]), A: c.alpha}
}

// cssMix is color-mix(in oklab, x p%, y) as CSS Color 5 defines it: premultiplied by alpha, mixed,
// divided out.
func cssMix(x, y RGBA, percent float64) RGBA {
	f := percent / 100
	a, b := toCSSOklab(x), toCSSOklab(y)
	alpha := x.A*f + y.A*(1-f)
	mix := func(u, v float64) float64 { return (u*x.A*f + v*y.A*(1-f)) / alpha }
	return fromCSSOklab(cssOklab{mix(a.l, b.l), mix(a.a, b.a), mix(a.b, b.b), alpha})
}

// near reports whether two colours agree within a 255th per channel and in alpha.
func nearColour(a, b RGBA) bool {
	const step = 1.0 / 255
	return math.Abs(a.R-b.R) <= step && math.Abs(a.G-b.G) <= step && math.Abs(a.B-b.B) <= step && math.Abs(a.A-b.A) <= 1e-9
}

// A page may write any gradient lighter (decision D3 of 13/10/2026, its operators of the same day):
// background="cirrus-80" moves every stop 80% of the way to the mode's canvas, mixed in oklab, the same
// maths as a lighter colour (ink-30): lighter toward the canvas in light, toward the soft black in dark.
// It is the shade .N of before with N the complement, byte for byte: cirrus-70 writes what cirrus.30
// wrote and its samples are the ones cirrus.30 had (both written down from the engine before the
// change, Kit Design's palette). The contrast check reads the mixed stops: each sample agrees within a
// 255th with an independent mix (CSS Color 4's sample code, through XYZ) of the stop's colour of the
// mode and that mode's canvas — a pair (cirrus), tokens (headline: ink into brand+40) and a stop with an
// alpha (fade-top-50: canvas/0 mixed half with the canvas is the canvas at half its alpha, CSS's
// premultiplied mix). On the web each stop is color-mix(in oklab, …, var(--canvas)), the image written
// again in sRGB after it, as any gradient with a color-mix() stop; color="cirrus-80" is gradient text
// whose fallback colour is the mixed first stop, under the condition that the browser reads
// color-mix(); a prefix holds as for any value; -0 is the gradient itself and writes no mix. CONTROL:
// the plain gradient's samples are not the mixed ones.
func TestGradientLighterIsTheOldShade(t *testing.T) {
	p, _ := NewPalette(true)
	for _, c := range []struct {
		name, base string
		percent    float64 // of the stop's own colour, the rest the canvas
	}{{"cirrus-80", "cirrus", 20}, {"cirrus-30", "cirrus", 70}, {"headline-50", "headline", 50}, {"fade-top-50", "fade-top", 50}, {"cirrus-100", "cirrus", 0}} {
		g, _ := p.Gradient(c.base)
		for _, m := range p.Modes() {
			samples, ok := p.GradientSamples(c.name, m)
			if !ok {
				t.Fatalf("%s: no samples", c.name)
			}
			canvas, _ := p.Paint("canvas", m)
			for _, at := range []struct {
				sample int
				stop   Stop
			}{{0, g.Stops[0]}, {len(samples) - 1, g.Stops[len(g.Stops)-1]}} {
				colour := at.stop.Color
				if at.stop.paired() {
					colour = at.stop.Pair.Light
					if m == DarkMode {
						colour = at.stop.Pair.Dark
					}
				}
				own, ok := p.Paint(colour, m)
				if !ok {
					own, _ = ParseHex(colour)
				}
				want := cssMix(own, canvas, c.percent)
				if got := samples[at.sample]; !nearColour(got, want) {
					t.Errorf("%s in %s, sample %d: %s at %.3f, want %s at %.3f (%s mixed %v%% with the canvas %s)",
						c.name, m.Word(), at.sample, got.Hex(), got.A, want.Hex(), want.A, colour, c.percent, canvas.Hex())
				}
				if plain, _ := p.GradientSamples(c.base, m); c.base != "fade-top" && nearColour(plain[at.sample], want) {
					t.Errorf("control: %s's own sample %d is the mixed one", c.base, at.sample)
				}
			}
		}
	}

	// cirrus-70 is cirrus.30 of before: its samples and its stylesheet, the class's name aside
	before := map[Mode]string{
		LightMode: "#d3ddf4 #d6e0f4 #dae2f4 #dde5f4 #e0e8f4 #e4ebf4 #e7edf4 #eaeff4 #eaeff4 #eaeff4 #eaeff4",
		DarkMode:  "#141f33 #131d31 #121b30 #121a2e #11182a #101726 #0f1621 #0f151e #0f151e #0f151e #0f151e",
	}
	for m, want := range before {
		samples, _ := p.GradientSamples("cirrus-70", m)
		if got := strings.Join(sampleHexes(samples), " "); got != want {
			t.Errorf("cirrus-70 in %s: %s, want cirrus.30's %s", m.Word(), got, want)
		}
	}
	const pairs = ":root{--kitwork-gradient-cirrus-1:#849cdd;--kitwork-gradient-cirrus-2:#a9bbe0;--kitwork-gradient-cirrus-3:#cdd9e2}\n" +
		"@media screen{:root[data-theme=dark]{--kitwork-gradient-cirrus-1:#202f5e;--kitwork-gradient-cirrus-2:#181948;--kitwork-gradient-cirrus-3:#0d0f15}}\n"
	const oldStops = "color-mix(in oklab,var(--kitwork-gradient-cirrus-1) 30%,var(--canvas)) 0%,color-mix(in oklab,var(--kitwork-gradient-cirrus-2) 30%,var(--canvas)) 34%," +
		"color-mix(in oklab,var(--kitwork-gradient-cirrus-3) 30%,var(--canvas)) 67%"
	oldBackground := LayerStatement + "\n" + pairs + "@layer page{\n" +
		`.background-cirrus\.30{background-image:linear-gradient(to bottom right,` + oldStops + `);background-image:linear-gradient(to bottom right in srgb,` + oldStops + ")}\n" +
		`.background-cirrus\.30:where(:dir(rtl)){background-image:linear-gradient(to bottom left,` + oldStops + `);background-image:linear-gradient(to bottom left in srgb,` + oldStops + ")}\n" +
		`@media (forced-colors:active){.background-cirrus\.30{background-image:none}}` + "\n}\n"
	oldText := LayerStatement + "\n" + pairs + "@layer page{\n" +
		`.color-cirrus\.30{color:color-mix(in oklab,var(--kitwork-gradient-cirrus-1) 30%,var(--canvas))}` + "\n" +
		clipsToTextMixed + `{.color-cirrus\.30{background-image:linear-gradient(to bottom right,` + oldStops + `);background-image:linear-gradient(to bottom right in srgb,` + oldStops +
		");-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent}}\n" +
		clipsToTextMixed + `{.color-cirrus\.30:where(:dir(rtl)){background-image:linear-gradient(to bottom left,` + oldStops + `);background-image:linear-gradient(to bottom left in srgb,` + oldStops + ")}}\n" +
		`@media (forced-colors:active){.color-cirrus\.30{background-image:none;-webkit-text-fill-color:currentcolor}}` + "\n}\n"
	table := NewTable(p)
	for attribute, old := range map[string]string{"background": oldBackground, "color": oldText} {
		css := pageCSS(t, table, Intent{Name: attribute, Value: "cirrus-70"})
		if want := strings.ReplaceAll(old, `-cirrus\.30`, "-cirrus-70"); css != want {
			t.Errorf("%s=\"cirrus-70\" is not what %s=\"cirrus.30\" wrote:\n got %s\nwant %s", attribute, attribute, css, want)
		}
	}

	css := pageCSS(t, table, Intent{Name: "background", Value: "cirrus-80"}, Intent{Name: "color", Value: "cirrus-80"},
		Intent{Width: "medium", Name: "background", Value: "harbor-30"}, Intent{Name: "background", Value: "cirrus-0"})
	mix := func(i int) string {
		return "color-mix(in oklab,var(--kitwork-gradient-cirrus-" + string(rune('0'+i)) + ") 20%,var(--canvas))"
	}
	stops := mix(1) + " 0%," + mix(2) + " 34%," + mix(3) + " 67%"
	for _, line := range []string{
		`.background-cirrus-80{background-image:linear-gradient(to bottom right,` + stops + `);background-image:linear-gradient(to bottom right in srgb,` + stops + `)}`,
		`.color-cirrus-80{color:` + mix(1) + `}`,
		clipsToTextMixed + `{.color-cirrus-80{background-image:linear-gradient(to bottom right,` + stops + `);`,
		`.medium\:background-harbor-30{background-image:linear-gradient(to bottom right,color-mix(in oklab,var(--kitwork-gradient-harbor-1) 70%,var(--canvas)) 0%,`,
		`.background-cirrus-0{background-image:linear-gradient(to bottom right,var(--kitwork-gradient-cirrus-1) 0%,var(--kitwork-gradient-cirrus-2) 34%,var(--kitwork-gradient-cirrus-3) 67%)}`,
	} {
		if !strings.Contains(css, "\n"+line) {
			t.Errorf("missing\n%s\nin\n%s", line, css)
		}
	}
	if !strings.Contains(css, "--kitwork-gradient-harbor-1:#72acd5") {
		t.Errorf("a lighter gradient's pairs are set like the gradient's own:\n%s", css)
	}
	if g, ok := p.Gradient("cirrus-80"); !ok || g.Op != '-' || g.Amount != 80 {
		t.Errorf("cirrus-80 is cirrus lighter by 80: %+v", g)
	}
	for _, refused := range []string{"cirrus-101", "cirrus-07", "cirrus--5", "cirrus-2.5", "cirrus.20", "cirrus-20+10", "cirrus/50"} {
		if _, ok := p.Gradient(refused); ok {
			t.Errorf("%s is no gradient", refused)
		}
	}
}

// A page may write any gradient stronger (decision D5 of the operators, 13/10/2026): background="cirrus+20"
// moves every stop 20% further from the mode's canvas, as brand+15 moves a colour (Stronger): darker in
// light, lighter in dark. CSS has no function for it, so each stop is a custom property of its own,
// --kitwork-gradient-cirrus-strong-20-<stop>, set on :root for the page in light and again for the dark
// look, the image naming it (no color-mix(), so no second image in sRGB); a stop's alpha stays around it
// (halo+20: brand/40 is still 40%). The contrast check reads what the page sets: every sample moves away
// from the canvas, and the first stop is what gradient text falls back to and what a backdrop paints
// <html> with (GradientGround), the same colour as the property. A stop that is an engine colour
// (headline's brand end, engine_colour.go) is moved from its value in each mode — brand+40 in light,
// brand in dark on Kit Design —, and the page sets no property it does not read. +0 is the gradient
// itself. CONTROL: a palette with no dark look sets the light values only.
func TestGradientStronger(t *testing.T) {
	p, _ := NewPalette(true)
	table := NewTable(p)
	css := pageCSS(t, table, Intent{Name: "background", Value: "cirrus+20"}, Intent{Name: "color", Value: "cirrus+20"})
	property := func(i int) string { return "--kitwork-gradient-cirrus-strong-20-" + string(rune('0'+i)) }
	stops := "var(" + property(1) + ") 0%,var(" + property(2) + ") 34%,var(" + property(3) + ") 67%"
	for _, line := range []string{
		`.background-cirrus\+20{background-image:linear-gradient(to bottom right,` + stops + `)}`,
		`.background-cirrus\+20:where(:dir(rtl)){background-image:linear-gradient(to bottom left,` + stops + `)}`,
		`.color-cirrus\+20{color:var(` + property(1) + `)}`,
		clipsToText + `{.color-cirrus\+20{background-image:linear-gradient(to bottom right,` + stops + `);`,
	} {
		if !strings.Contains(css, "\n"+line) {
			t.Errorf("missing\n%s\nin\n%s", line, css)
		}
	}
	values := map[Mode]map[string]string{LightMode: {}, DarkMode: {}}
	for _, block := range []struct {
		m      Mode
		prefix string
	}{{LightMode, "\n:root{"}, {DarkMode, "\n@media screen{:root[data-theme=dark]{"}} {
		start := strings.Index(css, block.prefix)
		if start < 0 {
			t.Fatalf("no custom properties for %s:\n%s", block.m.Word(), css)
		}
		body := css[start+len(block.prefix):]
		body = body[:strings.IndexByte(body, '}')]
		for _, declaration := range strings.Split(body, ";") {
			name, value, _ := strings.Cut(declaration, ":")
			values[block.m][name] = value
		}
	}
	for _, m := range p.Modes() {
		samples, _ := p.GradientSamples("cirrus+20", m)
		plain, _ := p.GradientSamples("cirrus", m)
		canvas, _ := p.Paint("canvas", m)
		far := func(c RGBA) float64 { return math.Abs(toOklab(c.Painted()).L - toOklab(canvas).L) }
		for i := range samples {
			if far(samples[i]) <= far(plain[i]) {
				t.Errorf("cirrus+20 in %s at %d%%: %s is no further from the canvas %s than cirrus's %s", m.Word(), i*10, samples[i].Hex(), canvas.Hex(), plain[i].Hex())
			}
		}
		for i, at := range map[int]int{1: 0, 3: len(samples) - 1} {
			if got := values[m][property(i)]; got != samples[at].Hex() {
				t.Errorf("%s in %s is %s, the contrast check reads %s", property(i), m.Word(), got, samples[at].Hex())
			}
		}
		ground, ok := p.GradientGround("cirrus+20", m)
		if !ok || ground.Hex() != values[m][property(1)] || ground.A != 1 {
			t.Errorf("cirrus+20's ground in %s is %s, its first stop's property %s", m.Word(), ground.Hex(), values[m][property(1)])
		}
	}
	if values[LightMode][property(1)] == values[DarkMode][property(1)] {
		t.Errorf("a stronger stop follows the mode: %s is %s in both", property(1), values[LightMode][property(1)])
	}

	// an alpha stays the stop's: halo+20 keeps brand/40 at 40% and brand/0 clear
	site, err := p.WithGradients(map[string]Gradient{"halo": {Type: "radial", Position: "top", Stops: []Stop{{Color: "brand/40", At: 0}, {Color: "brand/0", At: 70}}}})
	if err != nil {
		t.Fatal(err)
	}
	halo := pageCSS(t, NewTable(site), Intent{Name: "background", Value: "halo+20"})
	haloStops := "color-mix(in srgb,var(--kitwork-gradient-halo-strong-20-1) 40%,transparent) 0%,color-mix(in srgb,var(--kitwork-gradient-halo-strong-20-2) 0%,transparent) 70%"
	if !strings.Contains(halo, `.background-halo\+20{background-image:radial-gradient(at top,`+haloStops+`);background-image:radial-gradient(at top in srgb,`+haloStops+`)}`) {
		t.Errorf("halo+20 keeps each stop's alpha around its property:\n%s", halo)
	}
	for _, m := range site.Modes() {
		samples, _ := site.GradientSamples("halo+20", m)
		brand, _ := site.Paint("brand", m)
		if samples[0].A != 0.4 || samples[10].A != 0 || samples[0].Hex() == brand.Hex() {
			t.Errorf("halo+20 in %s: %s at %.2f, want brand moved, at 0.40", m.Word(), samples[0].Hex(), samples[0].A)
		}
	}

	// a stop that is stronger already is moved from there, and only the stop's own property is set
	headline := pageCSS(t, table, Intent{Name: "color", Value: "headline+20"})
	if !strings.Contains(headline, ".color-headline\\+20{color:var(--kitwork-gradient-headline-strong-20-1)}") ||
		strings.Contains(headline, "--kitwork-strong-") || strings.Contains(headline, "--kitwork-headline") {
		t.Errorf("headline+20 reads its own properties only:\n%s", headline)
	}
	for _, m := range p.Modes() {
		strongest, _ := p.GradientSamples("headline+20", m)
		strong, _ := p.GradientSamples("headline", m)
		if strongest[10].Hex() == strong[10].Hex() {
			t.Errorf("headline+20's brand end in %s is the headline's own: %s", m.Word(), strong[10].Hex())
		}
		// the end moves from the engine colour's value in that mode: brand+40 in light, brand in dark
		end, _ := p.Engine(HeadlineColour, m)
		from, _ := p.Paint(end, m)
		canvas, _ := p.Paint("canvas", m)
		if want := Stronger(from, canvas, 20).Hex(); strongest[10].Hex() != want {
			t.Errorf("headline+20's end in %s is %s, want %s moved 20 further: %s", m.Word(), strongest[10].Hex(), end, want)
		}
	}
	if end, _ := p.Engine(HeadlineColour, DarkMode); end != "brand" {
		t.Errorf("CONTROL: headline's end in dark on Kit Design is %s, want brand", end)
	}

	// a page backdrop written stronger: <html> on its first stop's property, set per mode
	var r Rules
	table.Backdrop(&r, "cirrus+20")
	if backdrop := string(r.CSS()); !strings.Contains(backdrop, ":where(html){background-color:var("+property(1)+")}") ||
		!strings.Contains(backdrop, property(1)+":"+values[LightMode][property(1)]) || r.Backdrop() != "cirrus+20" {
		t.Errorf("<page background=\"cirrus+20\">: <html> on the first stop's property, the property set:\n%s", backdrop)
	}

	if same := pageCSS(t, table, Intent{Name: "background", Value: "cirrus+0"}); !strings.Contains(same, `.background-cirrus\+0{background-image:linear-gradient(to bottom right,var(--kitwork-gradient-cirrus-1) 0%,`) {
		t.Errorf("cirrus+0 is cirrus:\n%s", same)
	}
	if g, ok := p.Gradient("cirrus+20"); !ok || g.Op != '+' || g.Amount != 20 {
		t.Errorf("cirrus+20 is cirrus stronger by 20: %+v", g)
	}
	light, _ := NewPalette(false)
	if css := pageCSS(t, NewTable(light), Intent{Name: "background", Value: "cirrus+20"}); strings.Contains(css, "data-theme=dark") || !strings.Contains(css, ":root{"+property(1)+":") {
		t.Errorf("CONTROL: a palette with no dark look sets the light values only:\n%s", css)
	}
}

// The spellings the operators replaced (13/10/2026) are known words with a replacement, in background
// and color: the .N of before is -(100-N) (cirrus.30 is cirrus-70, a renamed gradient of the
// WebGradients collection carrying it: lady-lips.40 is pink-blush-60); a gradient takes no alpha, its
// step kept (glow-30/50 is glow-30); a step is a whole number from 0 to 100 after - or +. Each message
// says what to write. CONTROL: every suggestion reads.
func TestOldGradientFormsAreErrors(t *testing.T) {
	p, _ := NewPalette(true)
	table := NewTable(p)
	const lighter = "gradient nhạt hơn là glow-N (glow-50: mỗi điểm dừng đi 50% về phía canvas), đậm hơn là glow+N; độ hiện là của từng điểm dừng trong router.gradients (brand/40)"
	for in, want := range map[Intent][2]string{
		{Name: "background", Value: "cirrus.30"}:    {`background="cirrus.30": cách viết .N đã bỏ (13/10/2026): gradient nhạt hơn giờ là cirrus-70, mỗi điểm dừng đi 70% về phía canvas, đúng cirrus.30 cũ`, "cirrus-70"},
		{Name: "color", Value: "headline.70/50"}:    {`color="headline.70/50": cách viết .N đã bỏ (13/10/2026): gradient nhạt hơn giờ là headline-30`, "headline-30"},
		{Name: "background", Value: "lady-lips.40"}: {`lady-lips là tên gốc trong bộ WebGradients, trong Kitwork gradient ấy tên là pink-blush`, "pink-blush-60"},
		{Name: "background", Value: "lady-lips-40"}: {`background="lady-lips-40": lady-lips là tên gốc trong bộ WebGradients, trong Kitwork gradient ấy tên là pink-blush: viết pink-blush-40`, "pink-blush-40"},
		{Name: "background", Value: "glow/50"}:      {`background="glow/50": glow là một gradient, không có độ hiện /N: viết background="glow"; ` + lighter, "glow"},
		{Name: "background", Value: "glow-30/50"}:   {`background="glow-30/50": glow là một gradient, không có độ hiện /N: viết background="glow-30"; ` + lighter, "glow-30"},
		{Name: "color", Value: "glow+20/50"}:        {`color="glow+20/50": glow là một gradient, không có độ hiện /N: viết color="glow+20"`, "glow+20"},
		{Name: "background", Value: "glow-150"}:     {`background="glow-150": số sau dấu - của gradient glow là số nguyên từ 0 đến 100 (glow-20: mỗi điểm dừng đi 20% về phía canvas)`, ""},
		{Name: "background", Value: "cirrus-07"}:    {`background="cirrus-07": số sau dấu - của gradient cirrus là số nguyên từ 0 đến 100`, ""},
		{Name: "background", Value: "glow+150"}:     {`background="glow+150": số sau dấu + của gradient glow là số nguyên từ 0 đến 100 (glow+20: mỗi điểm dừng ra xa canvas thêm 20%)`, ""},
		{Name: "color", Value: "glow+"}:             {`color="glow+": số sau dấu + của gradient glow là số nguyên từ 0 đến 100`, ""},
		{Name: "background", Value: "Glow-30"}:      {`background="Glow-30": Glow không phải tên gradient: tên là glow`, "glow-30"},
		{Name: "border", Value: "glow-30"}:          {`border="glow-30": glow là một gradient; gradient là nền (background="glow") hoặc màu chữ (color="glow"), border nhận một màu`, ""},
	} {
		_, err := table.Class(nil, in)
		var problem *Problem
		if err == nil || !errors.As(err, &problem) || !strings.Contains(problem.Message, want[0]) || problem.Suggestion != want[1] {
			t.Errorf("%+v:\n got %v (suggestion %q)\nwant %q (suggestion %q)", in, err, suggestionOf(problem), want[0], want[1])
			continue
		}
		if problem.Suggestion != "" {
			if _, err := table.Class(nil, Intent{Name: in.Name, Value: problem.Suggestion}); err != nil {
				t.Errorf("CONTROL: %s=%q, the replacement of %q, does not read: %v", in.Name, problem.Suggestion, in.Value, err)
			}
		}
	}
}
