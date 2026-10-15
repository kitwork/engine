package stylesheet

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// designPalettes are the three palettes the standard set was designed and measured on (decision of
// 12/10/2026): Kit Design's default, the kitwork example's router.colors as it was then (its canvas
// #ffeaec; the example's own is #f6f9fc since, codePalettes keeps both) and the theme of the quancaphe
// conformance fixture (engine/conformance/testdata/ui/quancaphe/cases.json), each with a dark look.
func designPalettes(t *testing.T) map[string]*Palette {
	t.Helper()
	chains := map[string]map[string]Color{
		"default": {},
		"kitwork": {
			"brand": {Light: "#f5225f", Dark: "#ff6b6b"}, "canvas": {Light: "#ffeaec", Dark: "#16161e"},
			"paper": {Light: "#ffffff", Dark: "#1e2030"}, "panel": {Light: "#f7eff1", Dark: "#181a26"},
			"line": {Light: "#e5e3e5", Dark: "#2c3145"}, "ink": {Light: "#242442", Dark: "#c0caf5"},
			"success": {Light: "#0e9f6e"}, "warning": {Light: "#c98a1e"}, "danger": {Light: "#ef4444"}, "info": {Light: "#3b82f6"},
		},
		"quancaphe": {
			"brand": {Light: "#c8742c"}, "canvas": {Light: "#f6f3ee", Dark: "#141110"},
			"panel": {Light: "#efe9e1", Dark: "#1a1613"}, "paper": {Light: "#ffffff", Dark: "#1f1a17"},
			"ink": {Light: "#2b211b", Dark: "#f3ece6"}, "line": {Light: "#e6ded5", Dark: "#3a302a"},
			"success": {Light: "#2f9e63"}, "warning": {Light: "#c98a1e"}, "danger": {Light: "#e5484d"},
		},
	}
	out := map[string]*Palette{}
	for name, chain := range chains {
		p, err := NewPalette(true, chain)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = p
	}
	return out
}

// worstOn is the lowest ratio of a text colour over a gradient's samples composited on each ground, in
// one mode, every pixel painted as a screen shows it (8 bits a channel), as the compiler measures.
func worstOn(t *testing.T, p *Palette, gradient, text string, grounds []string, m Mode) float64 {
	t.Helper()
	samples, ok := p.GradientSamples(gradient, m)
	if !ok {
		t.Fatalf("%s: no samples", gradient)
	}
	ink, ok := p.Paint(text, m)
	if !ok {
		t.Fatalf("%s: cannot paint", text)
	}
	worst := math.Inf(1)
	for _, ground := range grounds {
		below, ok := p.Paint(ground, m)
		if !ok {
			below, _ = ParseHex(ground) // a photo the page cannot know: pure white, pure black
		}
		for _, s := range samples {
			bg := s.Over(below.Painted()).Painted()
			if r := Ratio(ink.Over(bg).Painted(), bg); r < worst {
				worst = r
			}
		}
	}
	return worst
}

// worstAsText is the lowest ratio of a gradient's samples as text on each ground, in one mode.
func worstAsText(t *testing.T, p *Palette, gradient string, grounds []string, m Mode) float64 {
	t.Helper()
	samples, _ := p.GradientSamples(gradient, m)
	worst := math.Inf(1)
	for _, ground := range grounds {
		below, _ := p.Paint(ground, m)
		bg := below.Painted()
		for _, s := range samples {
			if r := Ratio(s.Over(bg).Painted(), bg); r < worst {
				worst = r
			}
		}
	}
	return worst
}

// The standard set reads as it was designed, measured by the engine's own sampling (eleven samples,
// interpolated in sRGB with premultiplied alpha, composited over the ground, 8 bits a channel) on the
// three palettes it was designed on, in light and in dark: each gradient with the text it was made for,
// at the ratio WCAG 2.2 AA asks (4.5:1, large text 3:1). The scrims are measured over a photo the page
// cannot know, pure white and pure black, as the contrast check does for content under a placed element.
// The operators of 13/10/2026 rebuilt the four on the brand: glow and tint on brand-85 (TintStep, the
// tinted fill ink-30 still reads on), headline and inverse on the brand made stronger, an engine colour
// per mode (engine_colour.go: brand+40 in light, the least that reads as text and under paper on all
// three; the least from 0 in dark, brand itself there), which the glow and tint carry too. CONTROL: the
// same measurement fails what the design rejected — white on
// gilded, the fixed nebula as normal-size text, ink-30 on the owner's first glow (brand/40 to brand/0),
// headline ending in brand+15 (the old deep's nearest), glow on brand-83 under ink-30 — so it can tell
// the two apart.
func TestStandardGradientsReadAsDesigned(t *testing.T) {
	pages := []string{"canvas", "paper"}
	type pair struct {
		gradient, text string
		grounds        []string
		need           float64
		asText         bool
	}
	pairs := []pair{
		{"scrim-top", "white", []string{"#ffffff", "#000000"}, 4.5, false},
		{"scrim-bottom", "white", []string{"#ffffff", "#000000"}, 4.5, false},
		{"night", "white", pages, 4.5, false},
		{"gilded", "black", pages, 4.5, false},
		{"ocean", "white", pages, 4.5, false},
		{"ocean", "black", pages, 3, false},
		{"nebula", "black", pages, 4.5, false},
		{"nebula", "white", pages, 3, false},
		{"magma", "white", pages, 4.5, false},
		{"magma", "black", pages, 3, false},
		{"ocean", "", []string{"canvas", "paper", "panel"}, 3, true},
		{"nebula", "", []string{"canvas", "paper", "panel"}, 3, true},
		{"magma", "", []string{"canvas", "paper", "panel"}, 3, true},
		{"headline", "", []string{"canvas", "paper", "panel", "brand-85"}, 4.5, true},
		{"inverse", "paper", pages, 4.5, false},
		{"inverse", "canvas", pages, 4.5, false},
		{"inverse", "brand-95", pages, 4.5, false},
	}
	for _, g := range []string{"fade-top", "fade-bottom", "fade-start", "fade-end"} {
		pairs = append(pairs, pair{g, "ink", []string{"canvas", "paper", "panel"}, 4.5, false}, pair{g, "ink-30", pages, 4.5, false})
	}
	for _, g := range []string{"glow", "tint"} {
		pairs = append(pairs, pair{g, "ink", pages, 4.5, false}, pair{g, "ink-30", pages, 4.5, false}, pair{g, HeadlineColour, pages, 4.5, false})
	}
	palettes := designPalettes(t)
	for _, x := range pairs {
		lowest, at := math.Inf(1), ""
		for name, p := range palettes {
			for _, m := range p.Modes() {
				r := 0.0
				if x.asText {
					r = worstAsText(t, p, x.gradient, x.grounds, m)
				} else {
					r = worstOn(t, p, x.gradient, x.text, x.grounds, m)
				}
				if r < lowest {
					lowest, at = r, name+" "+m.Word()
				}
			}
		}
		what := x.text + " on " + x.gradient
		if x.asText {
			what = x.gradient + " as text"
		}
		if lowest < x.need {
			t.Errorf("%s: %.3f:1 (%s), want at least %.1f:1", what, lowest, at, x.need)
		}
		t.Logf("%-28s worst %.2f:1 (%s)", what, lowest, at)
	}
	// controls: what the design rejected falls short in the same measurement
	p := palettes["default"]
	if r := worstOn(t, p, "gilded", "white", pages, LightMode); r >= 4.5 {
		t.Errorf("control: white on gilded is %.2f:1, the design measured 1.33", r)
	}
	if r := worstAsText(t, p, "nebula", []string{"canvas"}, LightMode); r >= 4.5 {
		t.Errorf("control: nebula as normal-size text is %.2f:1, large only", r)
	}
	firstGlow, err := p.WithGradients(map[string]Gradient{"halo": {Type: "radial", Position: "top", Stops: []Stop{{Color: "brand/40", At: 0}, {Color: "brand/0", At: 70}}}})
	if err != nil {
		t.Fatal(err)
	}
	if r := worstOn(t, firstGlow, "halo", "ink-30", pages, DarkMode); r >= 4.5 {
		t.Errorf("control: ink-30 on brand/40 is %.2f:1 in dark, the design measured it short", r)
	}
	// the steps are the least that read: headline ending in brand+15, and a glow of brand-83, fall short
	weaker, err := palettes["quancaphe"].WithGradients(map[string]Gradient{
		"headline15": {Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "ink", At: 0}, {Color: "brand+15", At: 100}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := worstAsText(t, weaker, "headline15", []string{"canvas"}, LightMode); r >= 4.5 {
		t.Errorf("control: a headline ending in brand+15 is %.2f:1 on quancaphe's canvas in light, the design measured it short", r)
	}
	paler, err := palettes["kitwork"].WithGradients(map[string]Gradient{
		"glow83": {Type: "radial", Position: "top", Stops: []Stop{{Color: "brand-83", At: 0}, {Color: "brand-83/0", At: 70}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := worstOn(t, paler, "glow83", "ink-30", pages, DarkMode); r >= 4.5 {
		t.Errorf("control: ink-30 on a glow of brand-83 is %.2f:1 on kitwork in dark, the design measured it short", r)
	}
}

// The standard set is the designed one: eighteen names in their order, none a named colour, a fixed
// colour or a token of the default palette, every stop a colour the default palette names or one of the
// engine's own (the brand end of headline and inverse, engine_colour.go), which no author may write.
func TestStandardGradientsAreTheDesignedSet(t *testing.T) {
	want := "fade-top fade-bottom fade-start fade-end skeleton hairline glow tint headline inverse " +
		"scrim-top scrim-bottom night gilded ocean nebula magma prism"
	if got := strings.Join(StandardGradients(), " "); got != want {
		t.Fatalf("the standard set\n got %s\nwant %s", got, want)
	}
	p, _ := NewPalette(true)
	table := NewTable(p)
	for _, name := range StandardGradients() {
		g, _ := StandardGradient(name)
		if err := CheckGradient("my-"+name, g); err != nil {
			t.Errorf("%s does not read as a gradient: %v", name, err)
		}
		if _, named := NamedColour(name); named || p.Has(name) || fixedColors[name] != "" {
			t.Errorf("%s is also a colour's name", name)
		}
		for _, s := range g.Stops {
			if _, _, engine := table.engineColour(s.Color); engine {
				if _, _, _, _, problem := table.colour(Intent{Tag: stopTag, Name: "color"}, s.Color); problem == nil {
					t.Errorf("%s: stop %s is an engine colour, yet an author may write it", name, s.Color)
				}
				continue
			}
			if _, _, _, _, problem := table.colour(Intent{Tag: stopTag, Name: "color"}, s.Color); problem != nil {
				t.Errorf("%s: stop %s: %s", name, s.Color, problem.Message)
			}
		}
	}
	// StandardGradient hands a copy: changing it changes nothing
	g, _ := StandardGradient("glow")
	g.Stops[0].Color = "danger"
	if again, _ := StandardGradient("glow"); again.Stops[0].Color != "brand-85" {
		t.Error("StandardGradient handed out the set's own stops")
	}
}

// pageCSS records intents into one page and returns its stylesheet.
func pageCSS(t *testing.T, table *Table, intents ...Intent) string {
	t.Helper()
	var r Rules
	classes(t, table, &r, intents...)
	return string(r.CSS())
}

// Every standard gradient compiles to CSS as a background and as text, its class the attribute as
// written: background-image with the gradient of its type, its stops the colours the table writes
// (var(--token), a lighter colour's or an alpha's color-mix, a stronger colour's custom property, set on
// :root per mode, a named colour's hex) at their place; a gradient with a color-mix() stop written a
// second time "in srgb" after the first (the browser interpolates in sRGB, as the contrast check samples
// it); forced colours take the image away. A few whole lines pin the form: glow on brand-85, headline
// into the engine colour --kitwork-headline, Kit Design's brand+40 #4000d1 in light as before and its
// brand #7d83fd in dark (it was brand+40 #c2c8ff there, a pale end). CONTROL: a colour background on the
// same page is still background-color.
func TestEveryStandardGradientCompilesToCSS(t *testing.T) {
	p, _ := NewPalette(true)
	table := NewTable(p)
	for _, name := range StandardGradients() {
		g, _ := StandardGradient(name)
		css := pageCSS(t, table, Intent{Name: "background", Value: name}, Intent{Name: "color", Value: name})
		image := g.Type + "-gradient("
		if !strings.Contains(css, ".background-"+name+"{background-image:"+image) {
			t.Errorf("%s: no background image of its type:\n%s", name, css)
		}
		if !strings.Contains(css, "@media (forced-colors:active){.background-"+name+"{background-image:none}}") {
			t.Errorf("%s: forced colours keep the gradient:\n%s", name, css)
		}
		for _, s := range g.Stops {
			stop, custom, engine := table.engineColour(s.Color)
			if !engine {
				stop, _, _, custom, _ = table.colour(Intent{Name: "color"}, s.Color)
			}
			if !strings.Contains(css, stop+" "+number(s.At)+"%") {
				t.Errorf("%s: stop %s at %v is not %q:\n%s", name, s.Color, s.At, stop, css)
			}
			for _, v := range custom {
				if !strings.Contains(css, v.name+":"+v.light) || !strings.Contains(css, v.name+":"+v.dark) {
					t.Errorf("%s: stop %s reads %s, which the page does not set:\n%s", name, s.Color, v.name, css)
				}
			}
		}
		mixed := strings.Contains(css, "color-mix(")
		if twice := strings.Contains(css, " in srgb,"); twice != mixed {
			t.Errorf("%s: written in srgb %v, has a color-mix stop %v:\n%s", name, twice, mixed, css)
		}
		if strings.Contains(css, "transition") {
			t.Errorf("%s: a gradient animates nothing:\n%s", name, css)
		}
	}
	css := pageCSS(t, table, Intent{Name: "background", Value: "glow"}, Intent{Name: "background", Value: "prism"},
		Intent{Name: "background", Value: "night"}, Intent{Name: "background", Value: "paper"}, Intent{Name: "background", Value: "headline"})
	const tinted = "color-mix(in oklab,var(--brand) 15%,var(--canvas))"
	for _, line := range []string{
		`.background-glow{background-image:radial-gradient(at top,` + tinted + ` 0%,color-mix(in srgb,` + tinted + ` 0%,transparent) 70%);` +
			`background-image:radial-gradient(at top in srgb,` + tinted + ` 0%,color-mix(in srgb,` + tinted + ` 0%,transparent) 70%)}`,
		`.background-headline{background-image:linear-gradient(to right,var(--ink) 0%,var(--kitwork-headline) 100%)}`,
		`:root{--kitwork-headline:#4000d1}`,
		`@media screen{:root[data-theme=dark]{--kitwork-headline:#7d83fd}}`,
		`.background-prism{background-image:conic-gradient(at center,#ff0000 0%,#ffa500 17%,#ffd700 33%,#32cd32 50%,#00bfff 67%,#8a2be2 83%,#ff0000 100%)}`,
		`.background-night{background-image:linear-gradient(to bottom right,#191970 0%,#000 100%)}`,
		`.background-paper{background-color:var(--paper)}`,
	} {
		if !strings.Contains(css, "\n"+line+"\n") {
			t.Errorf("missing the line\n%s\nin\n%s", line, css)
		}
	}
}

// start and end flip for right-to-left: a gradient whose direction or position names one is written
// again for :dir(rtl), inside :where() so the rule weighs one class like every page rule, right after
// the class's own; one that names neither (to-bottom, top, center) has no such rule.
func TestGradientFlipsForRightToLeft(t *testing.T) {
	p, _ := NewPalette(false)
	table := NewTable(p)
	css := pageCSS(t, table, Intent{Name: "background", Value: "fade-end"}, Intent{Name: "background", Value: "night"},
		Intent{Name: "background", Value: "fade-top"}, Intent{Name: "background", Value: "glow"})
	fadeEnd := `.background-fade-end{background-image:linear-gradient(to right,color-mix(in srgb,var(--canvas) 0%,transparent) 0%,var(--canvas) 100%);` +
		`background-image:linear-gradient(to right in srgb,color-mix(in srgb,var(--canvas) 0%,transparent) 0%,var(--canvas) 100%)}` + "\n" +
		`.background-fade-end:where(:dir(rtl)){background-image:linear-gradient(to left,color-mix(in srgb,var(--canvas) 0%,transparent) 0%,var(--canvas) 100%);` +
		`background-image:linear-gradient(to left in srgb,color-mix(in srgb,var(--canvas) 0%,transparent) 0%,var(--canvas) 100%)}` + "\n"
	if !strings.Contains(css, fadeEnd) {
		t.Errorf("fade-end and its right-to-left rule, one after the other:\n%s", css)
	}
	if !strings.Contains(css, `.background-night:where(:dir(rtl)){background-image:linear-gradient(to bottom left,#191970 0%,#000 100%)}`) {
		t.Errorf("to-bottom-end is to bottom left from right to left:\n%s", css)
	}
	for _, still := range []string{"fade-top", "glow"} {
		if strings.Contains(css, ".background-"+still+":where(:dir(rtl))") {
			t.Errorf("%s names no start or end: no right-to-left rule:\n%s", still, css)
		}
	}
	// a position with start or end: a site's radial gradient at the top start
	q, err := p.WithGradients(map[string]Gradient{"corner": {Type: "radial", Position: "top-start", Stops: []Stop{{Color: "brand", At: 0}, {Color: "brand/0", At: 60}}}})
	if err != nil {
		t.Fatal(err)
	}
	css = pageCSS(t, NewTable(q), Intent{Name: "background", Value: "corner"})
	if !strings.Contains(css, ".background-corner{background-image:radial-gradient(at top left,") ||
		!strings.Contains(css, ".background-corner:where(:dir(rtl)){background-image:radial-gradient(at top right,") {
		t.Errorf("top-start is top left, and top right from right to left:\n%s", css)
	}
}

// color="headline" is gradient text: the first stop as a solid colour where the browser cannot clip to
// the text; where it can (either spelling), the gradient behind the letters, clipped to them with the
// -webkit- form first, and the fill transparent; flipped for right-to-left inside the same @supports;
// and in forced colours the system's text colour again, so the text never disappears. A first stop
// with an alpha falls back to its colour solid (line/0 is line). Prefixed, the rules hold under the
// prefix's condition.
func TestGradientTextCSS(t *testing.T) {
	p, _ := NewPalette(false)
	table := NewTable(p)
	css := pageCSS(t, table, Intent{Name: "color", Value: "headline"})
	want := strings.Join([]string{
		LayerStatement,
		`:root{--kitwork-headline:#4000d1}`, // brand+40 in light: this palette has no dark look
		"@layer page{",
		`.color-headline{color:var(--ink)}`,
		`@supports (background-clip:text) or (-webkit-background-clip:text){.color-headline{background-image:linear-gradient(to right,var(--ink) 0%,var(--kitwork-headline) 100%);-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent}}`,
		`@supports (background-clip:text) or (-webkit-background-clip:text){.color-headline:where(:dir(rtl)){background-image:linear-gradient(to left,var(--ink) 0%,var(--kitwork-headline) 100%)}}`,
		`@media (forced-colors:active){.color-headline{background-image:none;-webkit-text-fill-color:currentcolor}}`,
		"}", "",
	}, "\n")
	if css != want {
		t.Errorf("gradient text\n got %s\nwant %s", css, want)
	}
	if css := pageCSS(t, table, Intent{Name: "color", Value: "hairline"}); !strings.Contains(css, ".color-hairline{color:var(--line)}") {
		t.Errorf("the fallback of a first stop line/0 is line, solid:\n%s", css)
	}
	css = pageCSS(t, table, Intent{State: "hover", Name: "color", Value: "magma"})
	if !strings.Contains(css, "@media (hover:hover){\n.hover\\:color-magma:where(:hover){color:#dc143c}\n"+
		"@supports (background-clip:text) or (-webkit-background-clip:text){.hover\\:color-magma:where(:hover){background-image:linear-gradient(to right,#dc143c 0%,#be5103 100%)") {
		t.Errorf("hover:color=\"magma\" holds under hover:\n%s", css)
	}
}

// On an element with a gradient background, a colour background written for a condition takes the
// image away (background-image:none, Rules.TakeBack), so medium:background="paper" over
// background="glow" is paper, as a colour over a colour is; an unprefixed colour, never later than a
// gradient, has none. Only on that element: the rule is the colour's selector with the element's
// gradient classes in :where() after it, so the same colour class on another element of the page keeps
// the image it has (a switch's thumb is reboot.css's background-image: review of 12/10/2026, the switch
// lost it when the whole page's colour classes took images away). On a page with gradient text, every
// colour class gives the letters their fill back (inherited from gradient text around them otherwise). A
// gradient never animates: scrolled:background="glow" has no transition. CONTROL: a page with no
// gradient has the bytes it always had.
func TestColourClassesTakeAGradientBack(t *testing.T) {
	p, _ := NewPalette(false)
	table := NewTable(p)
	colours := []Intent{{Name: "background", Value: "paper"}, {Width: "medium", Name: "background", Value: "paper"},
		{State: "hover", Name: "background", Value: "brand"}, {Name: "color", Value: "ink"}, {State: "hover", Name: "color", Value: "brand+15"}}
	plain := pageCSS(t, table, colours...)
	for _, line := range []string{".background-paper{background-color:var(--paper)}", ".medium\\:background-paper{background-color:var(--paper)}",
		".hover\\:background-brand:where(:hover){background-color:var(--brand)}", ".color-ink{color:var(--ink)}",
		".hover\\:color-brand\\+15:where(:hover){color:var(--kitwork-strong-15-brand)}"} {
		if !strings.Contains(plain, "\n"+line+"\n") {
			t.Errorf("control: a page without a gradient keeps\n%s\nin\n%s", line, plain)
		}
	}
	// a page with a gradient background: its colour classes keep their bytes (the switch beside the glow)
	background := pageCSS(t, table, append([]Intent{{Name: "background", Value: "glow"}}, colours...)...)
	for _, line := range []string{".background-paper{background-color:var(--paper)}", ".medium\\:background-paper{background-color:var(--paper)}",
		".hover\\:background-brand:where(:hover){background-color:var(--brand)}", ".color-ink{color:var(--ink)}"} {
		if !strings.Contains(background, "\n"+line+"\n") {
			t.Errorf("a page with a gradient background keeps the colour class\n%s\nin\n%s", line, background)
		}
	}
	if strings.Count(background, "background-image:none") != 1 { // glow's own, in forced colours
		t.Errorf("no element carries both: nothing takes the gradient back:\n%s", background)
	}
	// one element writes background="glow" medium:background="paper" hover:background="tint", another
	// hover:background="brand": the first takes its gradients back under medium, the second keeps its image
	var r Rules
	element := strings.Fields(classes(t, table, &r, Intent{Name: "background", Value: "glow"}, Intent{Width: "medium", Name: "background", Value: "paper"},
		Intent{State: "hover", Name: "background", Value: "tint"}, Intent{Name: "background", Value: "paper"}))
	other := strings.Fields(classes(t, table, &r, Intent{State: "hover", Name: "background", Value: "brand"}))
	r.TakeBack(element)
	r.TakeBack(other)
	css := string(r.CSS())
	for _, line := range []string{
		".medium\\:background-paper{background-color:var(--paper)}\n.medium\\:background-paper:where(.background-glow,.hover\\:background-tint){background-image:none}",
		".background-paper{background-color:var(--paper)}",
		".hover\\:background-brand:where(:hover){background-color:var(--brand)}",
	} {
		if !strings.Contains(css, "\n"+line+"\n") {
			t.Errorf("the element that carries a gradient takes it back, no other:\n%s\nin\n%s", line, css)
		}
	}
	if strings.Count(css, "{background-image:none}") != 3 { // the one taken back, the two forced-colours rules
		t.Errorf("only medium:background-paper of the gradient's element takes it back:\n%s", css)
	}
	text := pageCSS(t, table, append([]Intent{{Name: "color", Value: "headline"}}, colours...)...)
	for _, line := range []string{".color-ink{color:var(--ink);-webkit-text-fill-color:currentcolor}",
		".hover\\:color-brand\\+15:where(:hover){color:var(--kitwork-strong-15-brand);-webkit-text-fill-color:currentcolor}",
		".medium\\:background-paper{background-color:var(--paper)}"} {
		if !strings.Contains(text, "\n"+line+"\n") {
			t.Errorf("a page with gradient text has\n%s\nin\n%s", line, text)
		}
	}
	scrolled := pageCSS(t, table, Intent{State: "scrolled", Name: "background", Value: "glow"}, Intent{State: "scrolled", Name: "color", Value: "headline"})
	if strings.Contains(scrolled, "transition") {
		t.Errorf("a gradient changes at once, no transition:\n%s", scrolled)
	}
	if moving := pageCSS(t, table, Intent{State: "scrolled", Name: "background", Value: "paper"}); !strings.Contains(moving, "transition:background-color .15s") {
		t.Errorf("control: a scrolled colour still fades:\n%s", moving)
	}
}

// A gradient written where it does not go, or as a colour, is refused with what to write: in border
// (a gradient is a background or text), with an alpha (the alpha is a stop's; a lighter gradient is
// written -N, a stronger +N: decision D3 of 13/10/2026 and its operators) or a step that is no whole
// number from 0 to 100, in capitals or without its hyphens. A typo near a gradient's name names it. A
// value from data never becomes a gradient: the domain of background and color is the declared tokens.
// The spellings of before are TestOldGradientFormsAreErrors's.
func TestGradientMisusedIsAnError(t *testing.T) {
	p, _ := NewPalette(false)
	table := NewTable(p)
	for in, want := range map[Intent][2]string{
		{Name: "border", Value: "glow"}:          {`border nhận none hoặc một màu. border="glow": glow là một gradient; gradient là nền (background="glow") hoặc màu chữ (color="glow"), border nhận một màu`, ""},
		{Name: "background", Value: "glow/50"}:   {`background="glow/50": glow là một gradient, không có độ hiện /N: viết background="glow"; gradient nhạt hơn là glow-N (glow-50: mỗi điểm dừng đi 50% về phía canvas), đậm hơn là glow+N; độ hiện là của từng điểm dừng trong router.gradients (brand/40)`, "glow"},
		{Name: "color", Value: "headline-30/50"}: {`color="headline-30/50": headline là một gradient, không có độ hiện /N: viết color="headline-30"`, "headline-30"},
		{Name: "color", Value: "headline-170"}:   {`color="headline-170": số sau dấu - của gradient headline là số nguyên từ 0 đến 100 (headline-20: mỗi điểm dừng đi 20% về phía canvas)`, ""},
		{Name: "background", Value: "cirrus-07"}: {`background="cirrus-07": số sau dấu - của gradient cirrus là số nguyên từ 0 đến 100`, ""},
		{Name: "color", Value: "headline+170"}:   {`color="headline+170": số sau dấu + của gradient headline là số nguyên từ 0 đến 100 (headline+20: mỗi điểm dừng ra xa canvas thêm 20%)`, ""},
		{Name: "background", Value: "Fade-Top"}:  {`background="Fade-Top": Fade-Top không phải tên gradient: tên là fade-top, chữ thường, gạch nối đúng như tên; viết fade-top`, "fade-top"},
		{Name: "background", Value: "scrimtop"}:  {`scrimtop không phải tên gradient: tên là scrim-top`, "scrim-top"},
		{Name: "background", Value: "glwo"}:      {`background="glwo": "glwo" không có trong bảng màu của trang; gần nhất: glow.`, ""},
		{Name: "color", Value: "headlin"}:        {`; gần nhất: headline.`, ""},
		{Name: "background", Value: "zzzz-none"}: {`; gradient: cirrus, dune, dusk, evensong, fade-bottom, fade-end, fade-start, fade-top, gilded, glow, golden-hour, hairline, harbor, headline, inverse, magma, nebula, night, ocean, prism, rain-garden, rose-haze, scrim-bottom, scrim-top, sea-mist, skeleton, spring-rain, still-water, tint, twilight;`, ""},
	} {
		_, err := table.Class(nil, in)
		var problem *Problem
		if err == nil || !errors.As(err, &problem) || !strings.Contains(problem.Message, want[0]) || problem.Suggestion != want[1] {
			t.Errorf("%+v:\n got %v (suggestion %q)\nwant %q (suggestion %q)", in, err, suggestionOf(problem), want[0], want[1])
		}
	}
	// border's own message keeps gradients out of its list
	if _, err := table.Class(nil, Intent{Name: "border", Value: "glwo"}); err == nil || strings.Contains(err.Error(), "gradient") || strings.Contains(err.Error(), "gần nhất: glow") {
		t.Errorf("border takes no gradient, so its message names none: %v", err)
	}
	domain, err := table.Domain(nil, Intent{Name: "background", Value: "{{ x }}"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := domain["glow"]; ok || domain["paper"] != "background-paper" {
		t.Errorf("a background from data takes the declared tokens, never a gradient: %v", domain)
	}
}

func suggestionOf(p *Problem) string {
	if p == nil {
		return ""
	}
	return p.Suggestion
}

// router.gradients is checked as far as it can be read without the palette (CheckGradient), each problem
// in the author's language with what to write: a missing or unknown type, a linear one without its
// direction (left and right are start and end), a direction on a radial one, a position on a linear
// one, fewer than two stops, a stop without a colour, at outside 0 to 100 or going back, a name that is
// not one or is already a colour's or a standard gradient's. CONTROL: the owner's two examples read.
func TestCheckGradientRefuses(t *testing.T) {
	ok := Gradient{Type: "linear", Direction: "to-bottom-end", Stops: []Stop{{Color: "brand", At: 0}, {Color: "violet", At: 100}}}
	if err := CheckGradient("hero", ok); err != nil {
		t.Fatalf("control: the owner's hero reads: %v", err)
	}
	if err := CheckGradient("halo", Gradient{Type: "radial", Position: "top", Stops: []Stop{{Color: "brand/40", At: 0}, {Color: "brand/0", At: 70}}}); err != nil {
		t.Fatalf("control: the owner's glow, renamed, reads: %v", err)
	}
	with := func(change func(*Gradient)) Gradient {
		g := ok.clone()
		change(&g)
		return g
	}
	for _, c := range []struct {
		name string
		g    Gradient
		want string
	}{
		{"hero", with(func(g *Gradient) { g.Type = "" }), `router.gradients: "hero" thiếu type: linear, radial hoặc conic`},
		{"hero", with(func(g *Gradient) { g.Type = "lineal" }), `router.gradients: "hero" có type "lineal": type là linear, radial hoặc conic`},
		{"hero", with(func(g *Gradient) { g.Direction = "" }), `router.gradients: "hero" là linear nên cần direction: to-top, to-bottom, to-start, to-end, to-top-start, to-top-end, to-bottom-start, to-bottom-end`},
		{"hero", with(func(g *Gradient) { g.Direction = "to-bottom-right" }), `có direction "to-bottom-right": direction là to-top, to-bottom, to-start, to-end, to-top-start, to-top-end, to-bottom-start, to-bottom-end; trái phải viết start và end, tự lật khi viết từ phải sang trái: to-bottom-end`},
		{"hero", with(func(g *Gradient) { g.Direction = "45deg" }), `có direction "45deg": direction là to-top`},
		{"hero", with(func(g *Gradient) { g.Position = "top" }), `"hero" là linear: không có position, hướng của nó là direction`},
		{"hero", with(func(g *Gradient) { g.Type, g.Direction, g.Position = "radial", "to-top", "" }), `"hero" là radial: không có direction, tâm của nó là position (center, top, bottom, start, end, top-start, top-end, bottom-start, bottom-end)`},
		{"hero", with(func(g *Gradient) { g.Type, g.Direction, g.Position = "conic", "", "left" }), `có position "left": position là center, top, bottom, start, end, top-start, top-end, bottom-start, bottom-end; trái phải viết start và end, tự lật khi viết từ phải sang trái: start`},
		{"hero", with(func(g *Gradient) { g.Stops = g.Stops[:1] }), `"hero" cần ít nhất hai điểm dừng`},
		{"hero", with(func(g *Gradient) { g.Stops[1].Color = " " }), `"hero" điểm dừng 2 thiếu color: mỗi điểm dừng là { color, at }`},
		{"hero", with(func(g *Gradient) { g.Stops[1].At = 120 }), `"hero" điểm dừng 2 có at 120: at là vị trí trên gradient, từ 0 đến 100`},
		{"hero", with(func(g *Gradient) { g.Stops[0].At = -5 }), `"hero" điểm dừng 1 có at -5: at là vị trí`},
		{"hero", with(func(g *Gradient) {
			g.Stops = []Stop{{Color: "brand", At: 0}, {Color: "ink", At: 60}, {Color: "paper", At: 40}}
		}), `router.gradients: "hero" điểm dừng 3 có at 40, nhỏ hơn at 60 của điểm dừng trước: các điểm dừng viết theo thứ tự at tăng dần`},
		{"glow", ok, `router.gradients: "glow" là tên một gradient chuẩn của Kitwork, có sẵn: viết background="glow" hay color="glow" là có. Gradient riêng đặt tên khác`},
		{"tomato", ok, `router.gradients: "tomato" đã là tên một màu (#ff6347): tên gradient và tên màu chung một chỗ, đặt tên khác`},
		{"slate-gray", ok, `router.gradients: "slate-gray" là cách viết khác của màu slategray (#708090)`},
		{"white", ok, `router.gradients: "white" là màu cố định white`},
		{"transparent", ok, `router.gradients: "transparent" không đặt được cho gradient`},
		{"Hero", ok, `router.gradients: "Hero" không phải tên gradient: chữ thường, các từ nối bằng - (hero, card-glow)`},
		{"hero.soft", ok, `router.gradients: "hero.soft" không phải tên gradient`},
	} {
		err := CheckGradient(c.name, c.g)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %+v:\n got %v\nwant %q", c.name, c.g, err, c.want)
		}
	}
	// two stops at one place are a hard edge, not going back
	if err := CheckGradient("stripe", Gradient{Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "brand", At: 0}, {Color: "brand", At: 50}, {Color: "ink", At: 50}, {Color: "ink", At: 100}}}); err != nil {
		t.Errorf("two stops at one place are a hard edge: %v", err)
	}
}

// The palette checks a site's gradients against itself (WithGradients): every stop's colour one it
// names, never a gradient; the name never one of its tokens. The nearest folder wins per name, and the
// standard set is every palette's. A token named like a standard gradient is refused by router.colors.
func TestPaletteWithGradients(t *testing.T) {
	p := examplePalette(t, map[string]Color{"chart": {Light: "#6b5d52"}})
	hero := Gradient{Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "brand", At: 0}, {Color: "tomato/50", At: 100}}}
	for _, c := range []struct {
		chain map[string]Gradient
		want  string
	}{
		{map[string]Gradient{"hero": {Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "brand", At: 0}, {Color: "brnad", At: 100}}}},
			`router.gradients: "hero", điểm dừng 2: color="brnad": "brnad" không có trong bảng màu của trang; gần nhất: brand.`},
		{map[string]Gradient{"hero": {Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "glow", At: 0}, {Color: "brand", At: 100}}}},
			`router.gradients: "hero", điểm dừng 1: "glow" là một gradient; điểm dừng là một màu`},
		{map[string]Gradient{"hero": {Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "brand", At: 0}, {Color: "navy-blue/40", At: 100}}}},
			`router.gradients: "hero", điểm dừng 2: color="navy-blue/40": navy-blue không phải tên màu: đó là tên Figma đặt cho navy`},
		{map[string]Gradient{"chart": hero}, `router.gradients: "chart" đã là token màu của bảng màu: tên gradient và tên màu chung một chỗ`},
		{map[string]Gradient{"canvas": hero}, `router.gradients: "canvas" đã là token màu của bảng màu`},
		{map[string]Gradient{"hero": {Type: "linear", Stops: hero.Stops}}, `router.gradients: "hero" là linear nên cần direction`},
	} {
		if _, err := p.WithGradients(c.chain); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v:\n got %v\nwant %q", c.chain, err, c.want)
		}
	}
	_, err := p.WithGradients(map[string]Gradient{"hero": {Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "brand", At: 0}, {Color: "glwo", At: 100}}}})
	if err == nil || strings.Contains(err.Error(), "gradient:") || strings.Contains(err.Error(), "gần nhất: glow") {
		t.Errorf("a stop's message names no gradient, a stop is a colour: %v", err)
	}
	site, err := p.WithGradients(map[string]Gradient{"hero": hero, "band": hero}, map[string]Gradient{"hero": {Type: "radial", Stops: hero.Stops}})
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := site.Gradient("hero"); g.Type != "radial" {
		t.Errorf("the nearest folder's hero wins: %+v", g)
	}
	if g, _ := site.Gradient("band"); g.Type != "linear" {
		t.Errorf("a gradient the nearest folder does not write stays the site's: %+v", g)
	}
	if _, ok := site.Gradient("glow"); !ok {
		t.Error("the standard set is every palette's")
	}
	if _, ok := p.Gradient("hero"); ok {
		t.Error("WithGradients changed the palette it was asked of")
	}
	if got := strings.Join(site.Gradients(), " "); !strings.Contains(got, "band cirrus dune dusk evensong fade-bottom") || !strings.Contains(got, "headline hero inverse") {
		t.Errorf("the gradients of the palette, sorted: %s", got)
	}
	_, err = NewPalette(false, map[string]Color{"glow": {Light: "#ffffff"}})
	if err == nil || err.Error() != `router.colors: "glow" đã là tên một gradient chuẩn (background="glow"): tên màu và tên gradient chung một chỗ, đặt tên token theo vai trò khác` {
		t.Errorf("a token named like a standard gradient: %v", err)
	}
	if _, err := NewPalette(false, map[string]Color{"glow-soft": {Light: "#ffffff"}}); err != nil {
		t.Errorf("control: glow-soft is no gradient's name: %v", err)
	}
}

// Eleven samples, every tenth of the gradient, each between the two stops around it in sRGB with
// premultiplied alpha: scrim-bottom is black from 55% to 80% opaque, evenly; fade-bottom keeps the
// canvas's own colour while it grows opaque; white/0 to black is black at half opacity halfway, not
// grey (the transparent end lends no colour of its own); glow is transparent from its last stop at 70%
// on; a hard stop takes the later colour after it; a token's samples follow the mode, a named colour's
// do not.
func TestGradientSamples(t *testing.T) {
	p, _ := NewPalette(true)
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	scrim, _ := p.GradientSamples("scrim-bottom", LightMode)
	if len(scrim) != GradientSampleCount || GradientSampleCount != 11 {
		t.Fatalf("eleven samples, got %d", len(scrim))
	}
	for i, s := range scrim {
		if want := 0.55 + 0.25*float64(i)/10; s.R != 0 || s.G != 0 || s.B != 0 || !near(s.A, want) {
			t.Errorf("scrim-bottom at %d%%: %+v, want black at %.3f", i*10, s, want)
		}
	}
	canvas, _ := p.Paint("canvas", LightMode)
	fade, _ := p.GradientSamples("fade-bottom", LightMode)
	for i, s := range fade {
		if !near(s.R, canvas.R) || !near(s.G, canvas.G) || !near(s.B, canvas.B) || !near(s.A, float64(i)/10) {
			t.Errorf("fade-bottom at %d%%: %+v, want the canvas at %.1f", i*10, s, float64(i)/10)
		}
	}
	glow, _ := p.GradientSamples("glow", LightMode)
	if glow[0].A != 1 || glow[7].A != 0 || glow[10].A != 0 || !near(glow[3].A, 1-3.0/7) {
		t.Errorf("glow: opaque at 0%%, %.3f at 30%%, transparent from 70%%: %+v", glow[3].A, glow)
	}
	q, _ := p.WithGradients(map[string]Gradient{
		"stripe": {Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "white", At: 0}, {Color: "white", At: 55}, {Color: "black", At: 55}, {Color: "black", At: 100}}},
		"smoke":  {Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "white/0", At: 0}, {Color: "black", At: 100}}},
	})
	stripe, _ := q.GradientSamples("stripe", LightMode)
	if stripe[5].R != 1 || stripe[6].R != 0 {
		t.Errorf("a hard stop at 55%%: white at 50%%, black at 60%%: %+v %+v", stripe[5], stripe[6])
	}
	// premultiplied: the transparent white lends no white, so halfway is black at half, not grey at half
	smoke, _ := q.GradientSamples("smoke", LightMode)
	if half := smoke[5]; half.R != 0 || half.G != 0 || half.B != 0 || !near(half.A, 0.5) {
		t.Errorf("white/0 to black halfway: %+v, want black at 0.5", half)
	}
	light, _ := p.GradientSamples("headline", LightMode)
	dark, _ := p.GradientSamples("headline", DarkMode)
	if light[0] == dark[0] {
		t.Error("headline follows the mode: its ink is the dark ink in dark")
	}
	nightLight, _ := p.GradientSamples("night", LightMode)
	nightDark, _ := p.GradientSamples("night", DarkMode)
	for i := range nightLight {
		if nightLight[i] != nightDark[i] {
			t.Errorf("night is fixed: the same at %d%% in both modes", i*10)
		}
	}
	if _, ok := p.GradientSamples("paper", LightMode); ok {
		t.Error("a colour has no samples")
	}
}
