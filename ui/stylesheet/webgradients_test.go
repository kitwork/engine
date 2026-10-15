package stylesheet

import (
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// decidedRenames are the twelve gradients of the WebGradients collection that take another name
// (decision W2 of 13/10/2026), the collection's name to the .ui name, written here apart from
// webgradients_gen.go so a changed name there shows.
var decidedRenames = map[string]string{
	"Young Passion": "coral-berry", "Lady Lips": "pink-blush", "Arielle's Smile": "arielles-smile",
	"Itmeo Branding": "spring-blue", "Dirty Beauty": "dusty-blue", "Teen Notebook": "periwinkle-pink",
	"Sweet Period": "indigo-gold", "Love Kiss": "red-peach", "Passionate Bed": "salmon-rose",
	"Vicious Stance": "deep-slate", "Light Blue": "aqua-sky", "Teen Party": "magenta-indigo",
}

// decidedLeftOut are the five gradients of the collection the engine leaves out (review of 13/10/2026),
// by name in .ui spelling, with the base colour the stylesheet draws them on: each that colour seen
// through translucent layers mixed by background-blend-mode, which one layer cannot draw.
var decidedLeftOut = map[string]string{
	"coup-de-grace": "#dcd9d4", "loon-crest": "#989898", "sharp-glass": "#c9ccd3", "chemic-aqua": "#cddcdc", "earl-gray": "#e4e4e1",
}

var marksBetweenWords = regexp.MustCompile(`[^a-z0-9]+`)

// uiSpelling is a name of the collection in .ui spelling, as W2 says it: lowercase, an apostrophe no
// letter, the words joined by -.
func uiSpelling(name string) string {
	return strings.Trim(marksBetweenWords.ReplaceAllString(strings.ReplaceAll(strings.ToLower(name), "'", ""), "-"), "-")
}

// handSpelling is a name of the collection as a hand writes it from the collection's site: lowercase,
// every mark between letters a - (Arielle's Smile is arielle-s-smile).
func handSpelling(name string) string {
	return strings.Trim(marksBetweenWords.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// uiName is the .ui name a gradient of the collection is built in under: its name in .ui spelling, or
// the decided new one.
func uiName(name string) string {
	if renamed, ok := decidedRenames[name]; ok {
		return renamed
	}
	return uiSpelling(name)
}

// sourceLayer is one image of a block of the collection's stylesheet, read here apart from
// webgradients_gen.go: linear or radial, what leads its stops as written (to right, 45deg, circle 248px
// at center; "" for none), its stops as written, and whether one is translucent (rgba).
type sourceLayer struct {
	kind, lead  string
	stops       []sourceStop
	translucent bool
}

type sourceStop struct {
	colour string // as written: #FF9A9E, lightgrey
	at     float64
}

var (
	gradientCall = regexp.MustCompile(`(linear|radial)-gradient\(`)
	writtenHex   = regexp.MustCompile(`#[0-9A-Fa-f]{6}\b`)
)

// layersOf are the images of a block's declarations, top first, and the base colour the background
// shorthand writes beside them, lowercase ("" when none).
func layersOf(t *testing.T, declarations string) (layers []sourceLayer, base string) {
	t.Helper()
	outside := declarations
	for rest := declarations; ; {
		at := gradientCall.FindStringSubmatchIndex(rest)
		if at == nil {
			break
		}
		depth, end := 0, -1
		for i := at[1] - 1; i < len(rest) && end < 0; i++ {
			switch rest[i] {
			case '(':
				depth++
			case ')':
				if depth--; depth == 0 {
					end = i
				}
			}
		}
		l := sourceLayer{kind: rest[at[2]:at[3]]}
		for k, part := range splitAtCommas(rest[at[1]:end]) {
			space := strings.LastIndexByte(part, ' ')
			colour, place := part[:max(space, 0)], part[space+1:]
			n, err := strconv.ParseFloat(strings.TrimSuffix(place, "%"), 64)
			if !strings.HasSuffix(place, "%") || err != nil || colour == "" || !(colour[0] == '#' || colour[0] >= 'a' && colour[0] <= 'z') {
				if k > 0 {
					t.Fatalf("%q: the stop %q", declarations, part)
				}
				l.lead = part // what leads the stops: to right, 45deg, circle 248px at center, 73% 147%
				continue
			}
			l.translucent = l.translucent || strings.HasPrefix(colour, "rgba(")
			l.stops = append(l.stops, sourceStop{colour: colour, at: n})
		}
		layers = append(layers, l)
		outside = strings.Replace(outside, rest[at[0]:end+1], "", 1)
		rest = rest[end+1:]
	}
	if hexes := writtenHex.FindAllString(outside, -1); len(hexes) > 0 {
		base = strings.ToLower(hexes[0])
	}
	return layers, base
}

// splitAtCommas is a list split at the commas outside brackets, each part trimmed.
func splitAtCommas(list string) []string {
	var out []string
	depth, from := 0, 0
	for i, c := range list {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(list[from:i]))
				from = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(list[from:]))
}

// stopHex is a stop's colour as written, as the engine's lowercase #rrggbb: a hex lowercase, a CSS name
// (lightgrey, white, black) its hex.
func stopHex(t *testing.T, colour string) string {
	t.Helper()
	if strings.HasPrefix(colour, "#") {
		return strings.ToLower(colour)
	}
	hex, ok := NamedColour(colour)
	if !ok {
		t.Fatalf("the stylesheet's colour %q is no colour", colour)
	}
	return hex
}

// builtSource are the blocks of the stylesheet the engine builds in, in order, each with its top layer:
// every block but the ones whose top layer is translucent.
func builtSource(t *testing.T) (blocks []sourceWebGradient, tops []sourceLayer) {
	for _, s := range sourceWebGradients {
		layers, _ := layersOf(t, s.declarations)
		if !layers[0].translucent {
			blocks, tops = append(blocks, s), append(tops, layers[0])
		}
	}
	return blocks, tops
}

// The WebGradients collection is built into every palette (decision W1 of 13/10/2026) as its stylesheet
// writes it: 175 of its 180 gradients, in its order, each under its own name in .ui spelling or, for the
// twelve renamed, the new name, and the collection's name of a renamed one resolves to the new one; the
// five whose top layer is translucent over a base colour are left out (review of 13/10/2026), as decided
// by hand above; built in beside the eighteen of the standard set and the twelve backdrops. Arielle's
// Smile's own name, written by hand, is arielles-smile with a hyphen more, no other name. CONTROL: a
// renamed one's own name and a left-out one's name are no gradient of the palette.
func TestWebGradientsAreTheCollection(t *testing.T) {
	blocks, _ := builtSource(t)
	if len(sourceWebGradients) != 180 || len(blocks) != 175 || len(WebGradients()) != 175 {
		t.Fatalf("the collection has %d gradients in its stylesheet, %d of one opaque top layer, %d built in: want 180, 175 and 175", len(sourceWebGradients), len(blocks), len(WebGradients()))
	}
	if len(builtinGradients) != 18+12+175 {
		t.Errorf("built into every site: %d gradients, want 18 standard, 12 backdrops and 175 of WebGradients", len(builtinGradients))
	}
	p := mustPalette(t)
	seen, left := 0, map[string]string{}
	for _, s := range sourceWebGradients {
		if layers, base := layersOf(t, s.declarations); layers[0].translucent {
			left[uiSpelling(s.name)] = base
			if _, ok := p.Gradient(uiSpelling(s.name)); ok {
				t.Errorf("CONTROL: %s %s, left out, is a gradient of the palette", s.index, s.name)
			}
		}
	}
	if fmt.Sprint(left) != fmt.Sprint(decidedLeftOut) || fmt.Sprint(webGradientsLeftOut) != fmt.Sprint(decidedLeftOut) {
		t.Errorf("left out: %v in the stylesheet, %v in the engine, want %v", left, webGradientsLeftOut, decidedLeftOut)
	}
	for i, s := range blocks {
		want := uiName(s.name)
		if _, ok := decidedRenames[s.name]; ok {
			seen++
		}
		if got := WebGradients()[i]; got != want {
			t.Errorf("%s %s: built in as %q, want %q", s.index, s.name, got, want)
		}
		if _, ok := p.Gradient(want); !ok {
			t.Errorf("%s %s: %q is no gradient of the palette", s.index, s.name, want)
		}
		own := handSpelling(s.name)
		if letters(own) == letters(want) {
			if renamed, _, ok := renamedWebGradient(own); ok {
				t.Errorf("%s %s: %q, the name with its letters written otherwise, resolves to %q as a renamed one", s.index, s.name, own, renamed)
			}
			continue // the name itself, however its letters are written
		}
		if renamed, original, ok := renamedWebGradient(own); !ok || renamed != want || original != own {
			t.Errorf("%s %s: %q resolves to %q (%v), want %q", s.index, s.name, own, renamed, ok, want)
		}
		if _, ok := p.Gradient(own); ok {
			t.Errorf("CONTROL: %s %s: its own name %q is a gradient of the palette, the new name %q is", s.index, s.name, own, want)
		}
	}
	if seen != 12 || len(webGradientRenames) != 12 {
		t.Errorf("twelve renamed: %d of the decided found in the stylesheet, %d in webGradientRenames", seen, len(webGradientRenames))
	}
	for original, name := range decidedRenames {
		if got := webGradientRenames[handSpelling(original)]; got != name {
			t.Errorf("webGradientRenames[%q] = %q, want %q", handSpelling(original), got, name)
		}
	}
}

// Every gradient built in is the top layer the collection's stylesheet writes (decisions W3 and W4 of
// 13/10/2026, read on the stylesheet in the review of 13/10/2026): linear or radial as written, every
// stop the stylesheet's colour as lowercase #rrggbb (lightgrey, white and black as their hex), the same
// in light and in dark, at the stylesheet's place, beyond 100 or after 0 as written; its samples the same
// in both modes. The stylesheet is read here apart from webgradients_gen.go (layersOf). A few are written
// out by hand too, among them the ones gradients.json cut short (spiky-naga, fabled-sunset), moved
// (burning-spring) or copied (deep-blue). CONTROL: a token (cirrus's pairs) differ by mode.
func TestWebGradientStopsAreTheSource(t *testing.T) {
	p := mustPalette(t)
	blocks, tops := builtSource(t)
	for i, s := range blocks {
		name, top := WebGradients()[i], tops[i]
		g, _ := p.Gradient(name)
		if g.Type != top.kind || len(g.Stops) != len(top.stops) {
			t.Errorf("%s: %s with %d stops, want %s with %d", name, g.Type, len(g.Stops), top.kind, len(top.stops))
			continue
		}
		for j, want := range top.stops {
			stop, hex := g.Stops[j], stopHex(t, want.colour)
			if stop.Color != "" || stop.Pair != (Color{Light: hex, Dark: hex}) || stop.At != want.at {
				t.Errorf("%s %s, stop %d: %+v, want %s (%s) in light and dark at %v", s.index, name, j+1, stop, hex, want.colour, want.at)
			}
			if fixed, ok := stop.fixed(); !ok || fixed != hex {
				t.Errorf("%s, stop %d: not written as the one colour %s (%q, %v)", name, j+1, hex, fixed, ok)
			}
		}
		light, _ := p.GradientSamples(name, LightMode)
		dark, _ := p.GradientSamples(name, DarkMode)
		for k := range light {
			if light[k] != dark[k] {
				t.Errorf("%s, sample %d: %s in light, %s in dark: one colour in both", name, k, light[k].Hex(), dark[k].Hex())
				break
			}
		}
	}
	for name, stops := range map[string][]Stop{
		"warm-flame":     {fixedStop("#ff9a9e", 0), fixedStop("#fad0c4", 99), fixedStop("#fad0c4", 100)},
		"slick-carbon":   {fixedStop("#323232", 0), fixedStop("#3f3f3f", 40), fixedStop("#1c1c1c", 150)},
		"elegance":       {fixedStop("#eadfdf", 59), fixedStop("#ece2df", 100)},
		"pink-blush":     {fixedStop("#ff9a9e", 0), fixedStop("#fecfef", 99), fixedStop("#fecfef", 100)},
		"deep-blue":      {fixedStop("#6a11cb", 0), fixedStop("#2575fc", 100)},
		"burning-spring": {fixedStop("#4fb576", 0), fixedStop("#44c489", 30), fixedStop("#28a9ae", 46), fixedStop("#28a2b7", 59), fixedStop("#4c7788", 71), fixedStop("#6c4f63", 86), fixedStop("#432c39", 100)},
		"fabled-sunset":  {fixedStop("#231557", 0), fixedStop("#44107a", 29), fixedStop("#ff1361", 67), fixedStop("#fff800", 100)},
		"above-the-sky":  {fixedStop("#d3d3d3", 0), fixedStop("#d3d3d3", 1), fixedStop("#e0e0e0", 26), fixedStop("#efefef", 48), fixedStop("#d9d9d9", 75), fixedStop("#bcbcbc", 100)},
		"spiky-naga": {fixedStop("#505285", 0), fixedStop("#585e92", 12), fixedStop("#65689f", 25), fixedStop("#7474b0", 37), fixedStop("#7e7ebb", 50),
			fixedStop("#8389c7", 62), fixedStop("#9795d4", 75), fixedStop("#a2a1dc", 87), fixedStop("#b5aee4", 100)},
		"indigo-gold": {fixedStop("#3f51b1", 0), fixedStop("#5a55ae", 13), fixedStop("#7b5fac", 25), fixedStop("#8f6aae", 38),
			fixedStop("#a86aa4", 50), fixedStop("#cc6b8e", 62), fixedStop("#f18271", 75), fixedStop("#f3a469", 87), fixedStop("#f7c978", 100)},
	} {
		g, _ := p.Gradient(name)
		if len(g.Stops) != len(stops) {
			t.Errorf("%s: %+v, want %+v", name, g.Stops, stops)
			continue
		}
		for j := range stops {
			if g.Stops[j] != stops[j] {
				t.Errorf("%s, stop %d: %+v, want %+v", name, j+1, g.Stops[j], stops[j])
			}
		}
	}
	light, _ := p.GradientSamples("cirrus", LightMode)
	dark, _ := p.GradientSamples("cirrus", DarkMode)
	if light[0] == dark[0] {
		t.Error("CONTROL: cirrus's first sample is one colour in both modes; its pairs differ")
	}
}

// directionAngles are the eight direction words at their CSS angles.
var directionAngles = map[string]float64{
	"to-top": 0, "to-top-end": 45, "to-end": 90, "to-bottom-end": 135,
	"to-bottom": 180, "to-bottom-start": 225, "to-start": 270, "to-top-start": 315,
}

// turnBetween is the smaller turn between two CSS angles, 0 to 180 degrees.
func turnBetween(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	return math.Min(d, 360-d)
}

// Each linear gradient keeps the stylesheet's direction (decision W3 of 13/10/2026, read on the
// stylesheet): a word as it is, to right to-end and to left to-start (both flip for right-to-left), an
// angle the nearest of the eight direction words, 0deg to-top, 90deg to-end, 180deg to-bottom, a negative
// angle the same turn less 360deg; a radial one is centred. Every lead the stylesheet writes is in the
// tables below, written by hand, each angle checked to be the nearest word; the largest rounding is 20deg
// (the -20deg gradients, to-top), within the 22.5deg any angle can be. Named: Night Fade to top, Malibu
// Beach and Star Wine to right (gradients.json wrote 0deg: from the bottom up), Above Clouds to left,
// Warm Flame 45deg to-top-end, Sunny Morning 120deg to-bottom-end, Kind Steel -20deg to-top, Sun Veggie
// -225deg to-bottom-end, Itmeo Branding 180deg to-bottom, Raccoon Back -180deg to-bottom.
func TestWebGradientDirectionsAreTheNearestWord(t *testing.T) {
	words := map[string]string{"to top": "to-top", "to right": "to-end", "to bottom": "to-bottom", "to left": "to-start"}
	nearest := map[float64]string{
		15: "to-top", 45: "to-top-end", 60: "to-top-end", 120: "to-bottom-end", 135: "to-bottom-end", 180: "to-bottom",
		-20: "to-top", -60: "to-top-start", -180: "to-bottom", -225: "to-bottom-end",
	}
	for angle, word := range nearest {
		for other, at := range directionAngles {
			if turnBetween(angle, at) < turnBetween(angle, directionAngles[word]) {
				t.Errorf("the table: %vdeg is nearer %s than %s", angle, other, word)
			}
		}
	}
	p := mustPalette(t)
	largest := 0.0
	blocks, tops := builtSource(t)
	for i, s := range blocks {
		g, _ := p.Gradient(WebGradients()[i])
		lead := tops[i].lead
		if tops[i].kind == "radial" {
			if g.Position != "center" || g.Direction != "" {
				t.Errorf("%s %s, radial %q: at %q, direction %q, want centred", s.index, s.name, lead, g.Position, g.Direction)
			}
			continue
		}
		word, ok := words[lead]
		if !ok {
			angle, err := strconv.ParseFloat(strings.TrimSuffix(lead, "deg"), 64)
			if word, ok = nearest[angle]; err != nil || !ok {
				t.Errorf("%s %s: the direction %q is not in the tables", s.index, s.name, lead)
				continue
			}
			largest = math.Max(largest, turnBetween(angle, directionAngles[word]))
		}
		if g.Direction != word {
			t.Errorf("%s %s at %q: %s, want %s", s.index, s.name, lead, g.Direction, word)
		}
	}
	if largest != 20 {
		t.Errorf("the largest rounding is %vdeg, the docs say 20deg", largest)
	}
	for name, word := range map[string]string{"night-fade": "to-top", "malibu-beach": "to-end", "star-wine": "to-end", "above-clouds": "to-start",
		"warm-flame": "to-top-end", "sunny-morning": "to-bottom-end", "kind-steel": "to-top", "sun-veggie": "to-bottom-end",
		"spring-blue": "to-bottom", "raccoon-back": "to-bottom"} {
		if g, _ := p.Gradient(name); g.Direction != word {
			t.Errorf("%s: %s, want %s", name, g.Direction, word)
		}
	}
	css := pageCSS(t, NewTable(p), Intent{Name: "background", Value: "warm-flame"}, Intent{Name: "background", Value: "spring-blue"},
		Intent{Name: "background", Value: "malibu-beach"}, Intent{Name: "background", Value: "arielles-smile"})
	for _, line := range []string{
		`.background-warm-flame{background-image:linear-gradient(to top right,#ff9a9e 0%,#fad0c4 99%,#fad0c4 100%)}`,
		`.background-warm-flame:where(:dir(rtl)){background-image:linear-gradient(to top left,#ff9a9e 0%,#fad0c4 99%,#fad0c4 100%)}`,
		`.background-spring-blue{background-image:linear-gradient(to bottom,#2af598 0%,#009efd 100%)}`,
		`.background-malibu-beach{background-image:linear-gradient(to right,#4facfe 0%,#00f2fe 100%)}`,
		`.background-malibu-beach:where(:dir(rtl)){background-image:linear-gradient(to left,#4facfe 0%,#00f2fe 100%)}`,
		`.background-arielles-smile{background-image:radial-gradient(at center,#16d9e3 0%,#30c7ec 47%,#46aef7 100%)}`,
	} {
		if !strings.Contains(css, "\n"+line+"\n") {
			t.Errorf("missing the line\n%s\nin\n%s", line, css)
		}
	}
	if strings.Contains(css, ".background-spring-blue:where(:dir(rtl))") {
		t.Errorf("to-bottom names no start or end: no right-to-left rule:\n%s", css)
	}
}

// Every gradient built in from the collection draws at least two colours, and no two draw alike but the
// three the stylesheet itself writes twice (Clean Mirror and Cochiti Lake, Party Bliss and River City,
// Colorful Peach and Royal Garden): gradients.json drew five as one colour and Deep Blue as a copy of
// Lilac Sky (review of 13/10/2026). The eleven the stylesheet writes in layers, over a base colour or
// radial are, as decided: five left out (decidedLeftOut), two whole (Above Clouds, Full Metal: an opaque
// top layer painted normally covers the rest), four drawn short of the stylesheet (Arielle's Smile,
// Elegance, Raccoon Back, Slick Carbon). CONTROL: the three pairs draw alike.
func TestWebGradientsDrawTwoColoursEach(t *testing.T) {
	p := mustPalette(t)
	repeats := map[string]string{"cochiti-lake": "clean-mirror", "river-city": "party-bliss", "royal-garden": "colorful-peach"}
	drawn := map[string]string{}
	for _, name := range WebGradients() {
		g, _ := p.Gradient(name)
		colours := map[Color]bool{}
		for _, s := range g.Stops {
			colours[s.Pair] = true
		}
		if len(colours) < 2 {
			t.Errorf("%s draws one colour: %+v", name, g.Stops)
		}
		key := fmt.Sprintf("%s %s %s %v", g.Type, g.Direction, g.Position, g.Stops)
		if other, ok := drawn[key]; ok && repeats[name] != other {
			t.Errorf("%s draws as %s does: %s", name, other, key)
		}
		drawn[key] = name
	}
	for later, earlier := range repeats {
		a, _ := p.Gradient(later)
		b, _ := p.Gradient(earlier)
		if a.name, b.name = "", ""; fmt.Sprint(a) != fmt.Sprint(b) {
			t.Errorf("CONTROL: %s and %s, written alike in the stylesheet, draw %+v and %+v", later, earlier, a, b)
		}
	}
	var layered []string
	for _, s := range sourceWebGradients {
		if layers, base := layersOf(t, s.declarations); len(layers) > 1 || layers[0].kind != "linear" || base != "" {
			layered = append(layered, uiName(s.name))
		}
	}
	want := "arielles-smile coup-de-grace loon-crest above-clouds sharp-glass raccoon-back elegance chemic-aqua full-metal slick-carbon earl-gray"
	if got := strings.Join(layered, " "); got != want {
		t.Errorf("the stylesheet's gradients in layers, over a base colour or radial:\n got %s\nwant %s", got, want)
	}
}

// The collection's name of a renamed gradient is a known word with a replacement (decision W2 of
// 13/10/2026): in background and color it is an error that suggests the new name, a step kept with it
// (-N, +N; the .N of before as its -N, operators of 13/10/2026), however its letters are written;
// light-blue is also the colour lightblue written with a hyphen, so it
// keeps the colour's fix and names the gradient too. router.gradients may not take it as a name of its
// own. Arielle's Smile's own name has the letters of its .ui name, so written by hand (arielle-s-smile,
// Arielles-Smile) it is the .ui name written otherwise and is told so, as warm-flame is. A left-out
// gradient's name is an error that says why, suggesting nothing; a site may declare a gradient of its own
// under it. CONTROL: border takes no gradient, so its message names none; the new name compiles.
func TestWebGradientRenamesAreErrors(t *testing.T) {
	p := mustPalette(t)
	table := NewTable(p)
	hero := Gradient{Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "ink", At: 0}, {Color: "brand", At: 100}}}
	for original, name := range decidedRenames {
		own := handSpelling(original)
		if letters(own) == letters(name) {
			continue // Arielle's Smile: below
		}
		fix := name
		if own == "light-blue" {
			fix = "lightblue"
		}
		for _, attribute := range []string{"background", "color"} {
			_, err := table.Class(nil, Intent{Name: attribute, Value: own})
			var problem *Problem
			if err == nil || !errors.As(err, &problem) || problem.Suggestion != fix ||
				!strings.Contains(problem.Message, "trong bộ WebGradients") || !strings.Contains(problem.Message, "tên là "+name+": viết "+name) {
				t.Errorf("%s=%q:\n got %v (suggestion %q)\nwant the new name %s, suggestion %s", attribute, own, err, suggestionOf(problem), name, fix)
			}
			if _, err := table.Class(nil, Intent{Name: attribute, Value: name}); err != nil {
				t.Errorf("CONTROL: %s=%q: %v", attribute, name, err)
			}
		}
		if _, err := table.Class(nil, Intent{Name: "border", Value: own}); err == nil || strings.Contains(err.Error(), "WebGradients") {
			t.Errorf("CONTROL: border=%q takes no gradient: %v", own, err)
		}
		err := CheckGradient(own, hero)
		want := `router.gradients: "` + own + `" là tên gốc của gradient ` + name + ` trong bộ WebGradients, có sẵn`
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("router.gradients({ %q: … }): %v, want …%s…", own, err, want)
		}
	}
	for in, want := range map[Intent][2]string{
		{Name: "background", Value: "lady-lips"}:       {`background="lady-lips": lady-lips là tên gốc trong bộ WebGradients, trong Kitwork gradient ấy tên là pink-blush: viết pink-blush`, "pink-blush"},
		{Name: "color", Value: "teen-party.40"}:        {`teen-party là tên gốc trong bộ WebGradients, trong Kitwork gradient ấy tên là magenta-indigo`, "magenta-indigo-60"},
		{Name: "color", Value: "teen-party-40"}:        {`color="teen-party-40": teen-party là tên gốc trong bộ WebGradients, trong Kitwork gradient ấy tên là magenta-indigo: viết magenta-indigo-40`, "magenta-indigo-40"},
		{Name: "background", Value: "teen-party+20"}:   {`background="teen-party+20": teen-party là tên gốc trong bộ WebGradients`, "magenta-indigo+20"},
		{Name: "background", Value: "love-kiss/50"}:    {`background="love-kiss/50": love-kiss là tên gốc`, "red-peach"},
		{Name: "background", Value: "LadyLips"}:        {`background="LadyLips": LadyLips là tên gốc trong bộ WebGradients, trong Kitwork gradient ấy tên là pink-blush`, "pink-blush"},
		{Name: "background", Value: "arielle-s-smile"}: {`background="arielle-s-smile": arielle-s-smile không phải tên gradient: tên là arielles-smile, chữ thường, gạch nối đúng như tên; viết arielles-smile`, "arielles-smile"},
		{Name: "background", Value: "Arielles-Smile"}:  {`background="Arielles-Smile": Arielles-Smile không phải tên gradient: tên là arielles-smile, chữ thường, gạch nối đúng như tên`, "arielles-smile"},
		{Name: "color", Value: "ariellessmile.40"}:     {`color="ariellessmile.40": ariellessmile không phải tên gradient: tên là arielles-smile`, "arielles-smile-60"},
		{Name: "color", Value: "ariellessmile-40"}:     {`color="ariellessmile-40": ariellessmile không phải tên gradient: tên là arielles-smile`, "arielles-smile-40"},
		{Name: "background", Value: "coup-de-grace"}:   {`background="coup-de-grace": coup-de-grace là gradient của bộ WebGradients mà Kitwork không có: bộ vẽ nó bằng màu #dcd9d4 dưới các lớp trong mờ trộn bằng background-blend-mode, còn gradient của Kitwork là một lớp màu; cần thì khai một gradient riêng tên coup-de-grace trong router.gradients`, ""},
		{Name: "color", Value: "Earl-Gray.40"}:         {`color="Earl-Gray.40": Earl-Gray là gradient của bộ WebGradients mà Kitwork không có: bộ vẽ nó bằng màu #e4e4e1`, ""},
		{Name: "background", Value: "light-blue"}: {`background="light-blue": light-blue không phải tên màu: tên là lightblue (#add8e6), chữ thường, gạch nối đúng như tên; viết lightblue; ` +
			`còn gradient tên gốc light-blue trong bộ WebGradients thì trong Kitwork tên là aqua-sky: viết aqua-sky`, "lightblue"},
		{Name: "background", Value: "warm-flam"}:  {`; gần nhất: warm-flame.`, ""},
		{Name: "background", Value: "zzzz-none"}:  {`; và 175 gradient của bộ WebGradients (warm-flame, night-fade, juicy-peach…);`, ""},
		{Name: "background", Value: "Warm-Flame"}: {`Warm-Flame không phải tên gradient: tên là warm-flame`, "warm-flame"},
	} {
		_, err := table.Class(nil, in)
		var problem *Problem
		if err == nil || !errors.As(err, &problem) || !strings.Contains(problem.Message, want[0]) || problem.Suggestion != want[1] {
			t.Errorf("%+v:\n got %v (suggestion %q)\nwant %q (suggestion %q)", in, err, suggestionOf(problem), want[0], want[1])
		}
	}
	if err := CheckGradient("arielle-s-smile", hero); err == nil || !strings.Contains(err.Error(), "là cách viết khác của gradient trong bộ WebGradients arielles-smile") {
		t.Errorf("router.gradients({ \"arielle-s-smile\": … }): %v, want arielles-smile written otherwise", err)
	}
	if _, err := table.Class(nil, Intent{Name: "border", Value: "light-blue"}); err == nil || !strings.Contains(err.Error(), "viết lightblue") || strings.Contains(err.Error(), "aqua-sky") {
		t.Errorf("CONTROL: border=\"light-blue\" is the colour lightblue written otherwise, no gradient: %v", err)
	}
	if _, err := table.Class(nil, Intent{Name: "border", Value: "coup-de-grace"}); err == nil || strings.Contains(err.Error(), "WebGradients") {
		t.Errorf("CONTROL: border=\"coup-de-grace\" takes no gradient: %v", err)
	}
	if got := strings.Join(p.Gradients(), " "); strings.Contains(got, "warm-flame") {
		t.Errorf("Palette.Gradients lists the site's, the standard set and the backdrops, not the 175: %s", got)
	}
	for name := range decidedLeftOut {
		if err := CheckGradient(name, hero); err != nil {
			t.Errorf("router.gradients({ %q: … }), a name the engine leaves free: %v", name, err)
		}
		own, err := p.WithGradients(map[string]Gradient{name: hero})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewTable(own).Class(nil, Intent{Name: "background", Value: name}); err != nil {
			t.Errorf("background=%q with the site's own %s: %v", name, name, err)
		}
	}
}

// No name of the collection takes a word of the one namespace colours and gradients share (W2 of
// 13/10/2026), however its letters are written: a named colour, white, black, transparent, a keyword, a
// role token of the palette (with its rungs), a gradient of the standard set or a backdrop, another of
// the collection, or another's own name before its rename. A token or a site gradient named like one is
// refused, the message saying whose name it is.
func TestWebGradientNamesStayOutOfTheNamespace(t *testing.T) {
	p, _ := NewPalette(true)
	byLetters := map[string]string{}
	for _, name := range append(append(StandardGradients(), Backdrops()...), WebGradients()...) {
		if other, ok := byLetters[letters(name)]; ok {
			t.Errorf("%s and %s are the same letters", other, name)
		}
		byLetters[letters(name)] = name
	}
	tokens := map[string]string{}
	for _, token := range p.Tokens() {
		tokens[letters(token)] = token
	}
	hero := Gradient{Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "ink", At: 0}, {Color: "brand", At: 100}}}
	for _, name := range WebGradients() {
		if !gradientName.MatchString(name) {
			t.Errorf("%s is no gradient's name: lowercase words joined by -", name)
		}
		if _, named := NamedColour(name); named || fixedColors[name] != "" || name == "transparent" || gradientKeywords[name] {
			t.Errorf("%s is a colour's name or a keyword", name)
		}
		if colour, _, ok := namedOtherwise(name); ok {
			t.Errorf("%s is the colour %s written another way", name, colour)
		}
		if token, ok := tokens[letters(name)]; ok || p.Has(name) {
			t.Errorf("%s is the token %s", name, token)
		}
		if renamed, _, ok := renamedWebGradient(name); ok && renamed != name {
			t.Errorf("%s is the collection's own name of %s", name, renamed)
		}
		if err := CheckGradient(name, hero); err == nil || !strings.Contains(err.Error(), "là tên một gradient trong bộ WebGradients của Kitwork, có sẵn") {
			t.Errorf("router.gradients({ %q: … }): %v", name, err)
		}
		if _, err := NewPalette(false, map[string]Color{name: {Light: "#123456"}}); err == nil || !strings.Contains(err.Error(), "đã là tên một gradient trong bộ WebGradients") {
			t.Errorf("router.colors({ %q: … }): %v", name, err)
		}
	}
	if _, err := NewPalette(false, map[string]Color{"accent": {Light: "#123456"}}); err != nil {
		t.Errorf("CONTROL: a token of the site's own: %v", err)
	}
}

// A page may write any gradient of the collection lighter (decision W4 of 13/10/2026, as D3 for every
// gradient, written -N since its operators): warm-flame-60 moves each stop 60% of the way to the mode's
// canvas, mixed in oklab, so it goes quieter toward the canvas; the contrast check reads the mixed
// stops. On the web a stop of one colour is its hex, with no custom property per mode, and color= is
// gradient text over its first stop; as a page backdrop, <html> takes the first stop. CONTROL: the
// plain gradient's samples are not the mixed ones.
func TestWebGradientLighter(t *testing.T) {
	p, _ := NewPalette(true)
	for _, m := range p.Modes() {
		samples, ok := p.GradientSamples("warm-flame-60", m)
		plain, _ := p.GradientSamples("warm-flame", m)
		if !ok {
			t.Fatal("warm-flame-60 is no gradient of the palette")
		}
		canvas, _ := p.Paint("canvas", m)
		for _, at := range []struct {
			sample int
			hex    string
		}{{0, "#ff9a9e"}, {len(samples) - 1, "#fad0c4"}} {
			own, _ := ParseHex(at.hex)
			want := cssMix(own, canvas, 40)
			if got := samples[at.sample]; !nearColour(got, want) {
				t.Errorf("warm-flame-60 in %s, sample %d: %s, want %s (%s 40%% of it, the rest the canvas %s)", m.Word(), at.sample, got.Hex(), want.Hex(), at.hex, canvas.Hex())
			}
			if nearColour(plain[at.sample], want) {
				t.Errorf("CONTROL: warm-flame's own sample %d in %s is the mixed one", at.sample, m.Word())
			}
		}
	}
	table := NewTable(p)
	css := pageCSS(t, table, Intent{Name: "background", Value: "warm-flame-60"}, Intent{Name: "color", Value: "warm-flame"})
	stops := "color-mix(in oklab,#ff9a9e 40%,var(--canvas)) 0%,color-mix(in oklab,#fad0c4 40%,var(--canvas)) 99%,color-mix(in oklab,#fad0c4 40%,var(--canvas)) 100%"
	for _, line := range []string{
		`.background-warm-flame-60{background-image:linear-gradient(to top right,` + stops + `);background-image:linear-gradient(to top right in srgb,` + stops + `)}`,
		`.color-warm-flame{color:#ff9a9e}`,
	} {
		if !strings.Contains(css, "\n"+line+"\n") {
			t.Errorf("missing the line\n%s\nin\n%s", line, css)
		}
	}
	if strings.Contains(css, "--kitwork-gradient-") {
		t.Errorf("a stop of one colour sets no custom property:\n%s", css)
	}
	var r Rules
	if !table.Backdrop(&r, "warm-flame") {
		t.Fatal("warm-flame is a gradient of the palette")
	}
	if backdrop := string(r.CSS()); !strings.Contains(backdrop, ":where(html){background-color:#ff9a9e}") || r.Backdrop() != "warm-flame" || strings.Contains(backdrop, "--kitwork-gradient-") {
		t.Errorf("<page background=\"warm-flame\">: <html> on its first stop, no custom property (backdrop %q):\n%s", r.Backdrop(), backdrop)
	}
}

// Stops at one place make a sudden change there (CSS Images 3), so at its start a gradient shows the
// last of the stops written at its first place: Star Wine writes #b8cbb8 twice at 0, then #b465da at 0,
// and starts purple; Magic Lake writes #d5dee7 then #ffafbd at 0. The contrast check's first sample,
// the gradient text's fallback colour and a page backdrop's ground are that colour. CONTROL: a gradient
// whose first stop stands alone (Elegance, first at 59; night-fade) starts with it.
func TestWebGradientStartsWithTheStopItShows(t *testing.T) {
	p, _ := NewPalette(true)
	for name, want := range map[string]string{"star-wine": "#b465da", "magic-lake": "#ffafbd", "old-hat": "#b8cbb8", "elegance": "#eadfdf", "night-fade": "#a18cd1"} {
		g, _ := p.Gradient(name)
		for _, m := range p.Modes() {
			samples, _ := p.GradientSamples(name, m)
			ground, _ := p.GradientGround(name, m)
			if samples[0].Hex() != want || ground.Hex() != want {
				t.Errorf("%s in %s: first sample %s, ground %s, want %s (stops %+v)", name, m.Word(), samples[0].Hex(), ground.Hex(), want, g.Stops)
			}
		}
		css := pageCSS(t, NewTable(p), Intent{Name: "color", Value: name})
		if !strings.Contains(css, "\n.color-"+name+"{color:"+want+"}\n") {
			t.Errorf("color=%q falls back to %s:\n%s", name, want, css)
		}
	}
	if g, _ := p.Gradient("star-wine"); g.Stops[0].Pair.Light != "#b8cbb8" || g.first() != 2 {
		t.Errorf("CONTROL: star-wine writes #b8cbb8 first and shows its third stop first: %+v, first %d", g.Stops, g.first())
	}
}

// The collection ships with its licence (decision W5 of 13/10/2026): WEBGRADIENTS_LICENSE next to the
// data holds the MIT License with the collection's copyright line, exactly as its LICENSE file states
// it, and names the stylesheet the data is made from by the sha256 the generator checks it by; the
// package docs and the generated data point to it.
func TestWebGradientsLicence(t *testing.T) {
	licence, err := os.ReadFile("WEBGRADIENTS_LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	generator, err := os.ReadFile("webgradients_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	sum := regexp.MustCompile(`const sourceSHA256 = "([0-9a-f]{64})"`).FindSubmatch(generator)
	if sum == nil {
		t.Fatal("webgradients_gen.go names no sourceSHA256")
	}
	for _, want := range []string{
		"MIT License\n\nCopyright (c) 2017 itmeo\n\nPermission is hereby granted, free of charge, to any person obtaining a copy\n",
		"The above copyright notice and this permission notice shall be included in all\ncopies or substantial portions of the Software.\n",
		"THE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND",
		"https://github.com/itmeo/webgradients",
		"webgradients.css",
		string(sum[1]),
	} {
		if !strings.Contains(string(licence), want) {
			t.Errorf("WEBGRADIENTS_LICENSE lacks %q", want)
		}
	}
	for _, file := range []string{"stylesheet.go", "webgradients.go", "gradients.go"} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), "WEBGRADIENTS_LICENSE") || !strings.Contains(string(source), "Copyright (c) 2017") {
			t.Errorf("%s does not point to WEBGRADIENTS_LICENSE with the copyright", file)
		}
	}
	if source, _ := os.ReadFile("webgradients_source_test.go"); !strings.Contains(string(source), string(sum[1])) {
		t.Error("webgradients_source_test.go is not made from the stylesheet the generator names")
	}
}
