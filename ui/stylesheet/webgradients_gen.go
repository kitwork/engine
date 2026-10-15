//go:build ignore

// webgradients_gen writes webgradients.go, the WebGradients collection built into every site, and
// webgradients_source_test.go, the collection's stylesheet block by block as written, for the tests to
// read on their own, from the collection's webgradients.css:
//
//	go run webgradients_gen.go path/to/webgradients.css
//
// webgradients.css is
// https://raw.githubusercontent.com/itmeo/webgradients/0157829a0069756083abd3bfa81bef7117e14dfd/webgradients.css
// (github.com/itmeo/webgradients, MIT, Copyright (c) 2017 itmeo; the licence is WEBGRADIENTS_LICENSE),
// read 13/10/2026, sha256 472b0f48569a9fbbbbd7d84e75fa859a5add3e0a5be82953c1e12e7434ff1349 (any other
// bytes stop the generator): 180 blocks /*NNN Name*/ .class{…}, the stylesheet the collection's README
// gives to use and the one its site, webgradients.com, draws. Not the collection's gradients.json (sha256
// fc7d928dcba7cc75ffbb374ab02f0b3881d80add6dfd0584171d32efb81ec43e), read first and dropped the same day
// (review of 13/10/2026): it writes 0deg for every "to right" (25 gradients drawn from the bottom up),
// Deep Blue as a copy of Lilac Sky, only the first three stops of eight (Spiky Naga keeps 3 of 9, Fabled
// Sunset loses its yellow), Burning Spring's 86 as 80, and a layered gradient as one layer or one colour.
//
// The collection goes in as decisions W1–W4 of 13/10/2026 say, read on the stylesheet:
//   - each gradient keeps its name in .ui spelling: lowercase, the words joined by - (Warm Flame is
//     warm-flame, Arielle's Smile arielles-smile); twelve take another name (renames below), and the
//     name the collection gives them is a known word the page is told to replace;
//   - each is the top layer the stylesheet writes (the first, painted over the rest): for 169 of the
//     collection the whole gradient, linear;
//   - a linear one keeps its direction: to right is to-end, to left to-start, to top and to bottom
//     themselves; an angle becomes the nearest of the eight direction words (0deg is to-top, 90deg
//     to-end, 180deg to-bottom; a negative angle is the same turn less 360deg; an angle halfway between
//     two words takes the diagonal one): at most 22.5deg away, 20deg in this collection (the -20deg ones,
//     to-top);
//   - a radial one is centred where the stylesheet centres it and reaches the box's farthest corner, the
//     one size a gradient of Kitwork has;
//   - its stops are the stylesheet's colours at its places, as written (Warm Flame's 99 and 100, Cold
//     Evening's three at 100), lowercase #rrggbb (black, white and lightgrey as their hex), each the same
//     colour in light and in dark, like a named colour: a pair of one colour.
//
// Eleven are not one linear layer: Arielle's Smile is radial, ten are layers mixed by
// background-blend-mode. Two have an opaque top layer painted normally, which covers the rest, so it is
// the whole of them (Above Clouds, Full Metal); four keep their top layer and lose what the stylesheet
// draws besides (approximated below); five are a base colour seen through translucent layers, with no
// layer of their own colours: one layer cannot draw them, so the engine leaves them out (leftOut below)
// and a page that writes one is told so.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/format"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// sourceSHA256 is the sha256 of the webgradients.css the decisions were taken on.
const sourceSHA256 = "472b0f48569a9fbbbbd7d84e75fa859a5add3e0a5be82953c1e12e7434ff1349"

// renames are the gradients whose .ui name is not their own name in .ui spelling (decision W2 of
// 13/10/2026), in the collection's order: the name the collection gives, the .ui name, and why. Each new
// name says the gradient's colours in one or two plain words, as the collection's names do.
var renames = []struct{ original, name, why string }{
	{"Young Passion", "coral-berry", "a name awkward in a product's code: coral into raspberry"},
	{"Lady Lips", "pink-blush", "a name awkward in a product's code: salmon pink into a pale pink"},
	{"Arielle's Smile", "arielles-smile", "an apostrophe is no letter of a name: arielle-s-smile is arielles-smile"},
	{"Itmeo Branding", "spring-blue", "the collection's author's own brand: a spring green into a vivid blue"},
	{"Dirty Beauty", "dusty-blue", "a name awkward in a product's code: a dusty blue into a pale blue"},
	{"Teen Notebook", "periwinkle-pink", "a name awkward in a product's code: periwinkle into a pale pink"},
	{"Sweet Period", "indigo-gold", "a name awkward in a product's code: indigo through rose into gold"},
	{"Love Kiss", "red-peach", "a name awkward in a product's code: a vivid red into peach"},
	{"Passionate Bed", "salmon-rose", "a name awkward in a product's code: salmon pink into rose"},
	{"Vicious Stance", "deep-slate", "a name awkward in a product's code: two deep slate greys"},
	{"Light Blue", "aqua-sky", "light-blue is the colour lightblue written with a hyphen: mint through aqua into sky blue"},
	{"Teen Party", "magenta-indigo", "a name awkward in a product's code: magenta through purple into indigo"},
}

// approximated are the gradients the engine draws short of the stylesheet: an opaque top layer that does
// not cover the rest, or a radial one of a size of its own. What is lost goes above the gradient in
// webgradients.go.
var approximated = map[string]string{
	"Arielle's Smile": "here it reaches the box's farthest corner, not 248px",
	"Raccoon Back":    "the top of two layers; the translucent radial under it, mixed by screen, is not drawn",
	"Elegance":        "the top of two layers; here it reaches the box's farthest corner, not 73% 147%, and the translucent radial under it, mixed by screen, is not drawn",
	"Slick Carbon":    "the top of two layers; the translucent linear under it, mixed by multiply, is not drawn",
}

// leftOut are the gradients the engine does not carry: a base colour seen through translucent layers
// mixed by background-blend-mode, no layer of their own colours, so no gradient of one layer.
var leftOut = map[string]bool{"Coup de Grace": true, "Loon Crest": true, "Sharp Glass": true, "Chemic Aqua": true, "Earl Gray": true}

// repeats are the gradients the stylesheet writes twice under two names, the later to the earlier: both
// stay, as the collection has them.
var repeats = map[string]string{"Cochiti Lake": "Clean Mirror", "River City": "Party Bliss", "Royal Garden": "Colorful Peach"}

// keywordDirections are the directions the stylesheet writes in words, as direction words: to right is
// to-end, which flips for right-to-left as every start and end does.
var keywordDirections = map[string]string{
	"to top": "to-top", "to right": "to-end", "to bottom": "to-bottom", "to left": "to-start",
	"to top right": "to-top-end", "to right top": "to-top-end", "to bottom right": "to-bottom-end", "to right bottom": "to-bottom-end",
	"to bottom left": "to-bottom-start", "to left bottom": "to-bottom-start", "to top left": "to-top-start", "to left top": "to-top-start",
}

// namedHex are the colours the stylesheet writes by name, as their hex.
var namedHex = map[string]string{"black": "#000000", "white": "#ffffff", "lightgrey": "#d3d3d3"}

var (
	block     = regexp.MustCompile(`(?s)/\*(\d{3}) ([^*]+)\*/\s*\.([a-z0-9_]+)\{(.*?)\}`)
	stopText  = regexp.MustCompile(`^(#[0-9a-fA-F]{6}|#[0-9a-fA-F]{3}|[a-z]+|rgba\([^)]*\))\s+(-?\d+(?:\.\d+)?)%$`)
	degrees   = regexp.MustCompile(`^(-?\d+(?:\.\d+)?)deg$`)
	hexColour = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}){1,2}$`)
	uiName    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	notLetter = regexp.MustCompile(`[^a-z0-9]+`)
)

// spell is a name of the collection in .ui spelling: lowercase, an apostrophe dropped, every other run
// of marks between the words one -.
func spell(name string) string {
	lower := strings.NewReplacer("'", "", "’", "").Replace(strings.ToLower(name))
	return strings.Trim(notLetter.ReplaceAllString(lower, "-"), "-")
}

// mechanical is a name with every run of marks one -, the apostrophe too (arielle-s-smile): how a hand
// may write a name the collection spells with one.
func mechanical(name string) string {
	return strings.Trim(notLetter.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// directionWords are the eight directions of a linear gradient, clockwise from to-top, 45deg apart.
var directionWords = [8]string{"to-top", "to-top-end", "to-end", "to-bottom-end", "to-bottom", "to-bottom-start", "to-start", "to-top-start"}

// direction is the direction word nearest a CSS angle: the angle taken round to 0–360deg, then the word
// of the nearest multiple of 45deg; halfway between two, the diagonal (the odd step).
func direction(deg float64) string {
	d := math.Mod(deg, 360)
	if d < 0 {
		d += 360
	}
	q := d / 45
	step := int(math.Floor(q))
	switch f := q - math.Floor(q); {
	case f > 0.5:
		step++
	case f == 0.5 && step%2 == 0:
		step++
	}
	return directionWords[step%8]
}

// letters is a name without its hyphens: two names of the collection never share them.
func letters(name string) string { return strings.ReplaceAll(name, "-", "") }

// stop is one stop of a layer: its colour, lowercase #rrggbb ("" for a translucent rgba()), and its place.
type stop struct {
	colour string
	at     float64
}

// layer is one image of a block: linear or radial, what leads its stops as written (to right, 45deg,
// circle 248px at center; "" for none), its stops, and whether a stop is translucent.
type layer struct {
	kind, lead  string
	stops       []stop
	translucent bool
}

// gradient is one block of the stylesheet.
type gradient struct {
	index, name, class string
	declarations       string   // as written, its lines joined by a space
	layers             []layer  // top first
	blend              []string // background-blend-mode as written, one per layer; none is normal
	base               string   // the colour the background shorthand writes under the layers, or ""
}

func main() {
	if len(os.Args) != 2 {
		fail("usage: go run webgradients_gen.go webgradients.css")
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err.Error())
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != sourceSHA256 {
		fail(fmt.Sprintf("%s has sha256 %x, not the %s the decisions were taken on: read what changed, decide, then set sourceSHA256", os.Args[1], sum, sourceSHA256))
	}
	all := read(string(raw))
	renamed := map[string]int{} // the collection's name → its place in renames
	for i, r := range renames {
		if _, twice := renamed[r.original]; twice {
			fail(r.original + " is renamed twice")
		}
		renamed[r.original] = i
	}
	names := make([]string, len(all))
	taken := map[string]string{} // a name's letters → the name
	for i, g := range all {
		name := spell(g.name)
		if r, ok := renamed[g.name]; ok {
			name = renames[r].name
			delete(renamed, g.name)
		}
		if !uiName.MatchString(name) {
			fail(fmt.Sprintf("%s: %q is no .ui name", g.name, name))
		}
		if other, ok := taken[letters(name)]; ok {
			fail(fmt.Sprintf("%s: %q has the letters of %q", g.name, name, other))
		}
		taken[letters(name)] = name
		names[i] = name
	}
	if len(renamed) != 0 {
		fail(fmt.Sprintf("renames name gradients the collection does not have: %v", renamed))
	}
	for _, r := range renames {
		if other, ok := taken[letters(spell(r.original))]; ok && other != r.name {
			fail(fmt.Sprintf("%s: its own name %s is the gradient %s's", r.original, spell(r.original), other))
		}
	}

	var b bytes.Buffer
	b.WriteString(`// Code generated by webgradients_gen.go from webgradients.css; DO NOT EDIT.

package stylesheet

// webGradientSet is the WebGradients collection (github.com/itmeo/webgradients, MIT, Copyright (c) 2017
// itmeo: WEBGRADIENTS_LICENSE) as its stylesheet, webgradients.css, writes it, built into every site like
// the standard set (decision W1 of 13/10/2026), in the collection's order but the five left out
// (webGradientsLeftOut): each its name in .ui spelling and the top layer the stylesheet writes, a linear
// one's direction the nearest direction word, a radial one centred, its stops the stylesheet's colours at
// its places, each the same colour in light and in dark. Above each, the collection's number and name
// and what leads the layer in the stylesheet; above the four drawn short of it, what is lost.
var webGradientSet = []standardGradient{
`)
	kept, keys := 0, map[string]string{} // a drawn gradient → the collection's name
	var outs bytes.Buffer
	for i, g := range all {
		top := g.layers[0]
		if top.translucent {
			if !leftOut[g.name] || g.base == "" {
				fail(fmt.Sprintf("%s %s: its top layer is translucent over %q: decide, then list it in leftOut", g.index, g.name, g.base))
			}
			fmt.Fprintf(&outs, "%q: %q, // %s %s\n", names[i], g.base, g.index, g.name)
			delete(leftOut, g.name)
			continue
		}
		if leftOut[g.name] {
			fail(g.name + " is in leftOut, but its top layer is opaque")
		}
		blend := "normal"
		if len(g.blend) > 0 {
			blend = g.blend[0]
		}
		whole := top.kind == "linear" && (blend == "normal" || len(g.layers) == 1 && g.base == "") // nothing under it shows
		lost, listed := approximated[g.name]
		if whole == listed {
			fail(fmt.Sprintf("%s %s (layers: %d, the top %s, painted %s): approximated lists a gradient exactly when its top layer is not the whole of it", g.index, g.name, len(g.layers), top.kind, blend))
		}
		delete(approximated, g.name)
		colours := map[string]bool{}
		var stops []string
		for j, s := range top.stops {
			if s.at < 0 || (j > 0 && s.at < top.stops[j-1].at) {
				fail(fmt.Sprintf("%s, stop %d: at %v goes back", g.name, j+1, s.at))
			}
			colours[s.colour] = true
			stops = append(stops, fmt.Sprintf("fixedStop(%q, %s)", s.colour, number(s.at)))
		}
		if len(colours) < 2 {
			fail(fmt.Sprintf("%s %s: one colour, no gradient", g.index, g.name))
		}
		var drawn string
		if top.kind == "linear" {
			drawn = fmt.Sprintf("linear(%q, %s)", linearDirection(g.name, top.lead), strings.Join(stops, ", "))
		} else {
			drawn = fmt.Sprintf("Gradient{Type: \"radial\", Position: %q, Stops: []Stop{%s}}", radialPosition(g.name, top.lead), strings.Join(stops, ", "))
		}
		if other, ok := keys[drawn]; ok && repeats[g.name] != other {
			fail(fmt.Sprintf("%s draws as %s does: list it in repeats if the stylesheet writes both alike", g.name, other))
		} else if ok {
			delete(repeats, g.name)
		}
		keys[drawn] = g.name
		comment := fmt.Sprintf("// %s %s: %s", g.index, g.name, top.lead)
		if lost != "" {
			comment += "; " + lost
		}
		fmt.Fprintf(&b, "%s\n{%q, %s},\n", comment, names[i], drawn)
		kept++
	}
	for _, rest := range []map[string]string{approximated, repeats} {
		if len(rest) != 0 {
			fail(fmt.Sprintf("listed but not met: %v", rest))
		}
	}
	if len(leftOut) != 0 {
		fail(fmt.Sprintf("leftOut lists what the stylesheet does not have: %v", leftOut))
	}
	b.WriteString(`}

// webGradientRenames are the twelve gradients whose .ui name is not the collection's (decision W2 of
// 13/10/2026): the collection's name as a hand writes it, lowercase with its words joined by -, to the
// name to write. A page that writes the first is told the second.
var webGradientRenames = map[string]string{
`)
	for _, r := range renames {
		fmt.Fprintf(&b, "%q: %q, // %s: %s\n", mechanical(r.original), r.name, r.original, r.why)
	}
	b.WriteString(`}

// webGradientsLeftOut are the gradients of the collection the engine does not carry (review of
// 13/10/2026): each a base colour seen through translucent layers mixed by background-blend-mode, with no
// layer of its own colours, which one layer cannot draw. By name in .ui spelling, the base colour. A page
// that writes one is told so (renamedGradient).
var webGradientsLeftOut = map[string]string{
`)
	b.Write(outs.Bytes())
	b.WriteString("}\n")
	if kept != 175 {
		fail(fmt.Sprintf("%d gradients drawn, want 175", kept))
	}
	write("webgradients.go", b.Bytes())

	b.Reset()
	fmt.Fprintf(&b, `// Code generated by webgradients_gen.go from webgradients.css; DO NOT EDIT.

package stylesheet

// sourceWebGradients is webgradients.css as the WebGradients collection publishes it
// (github.com/itmeo/webgradients, sha256 %s):
// each block's number, name and declarations as written, their lines joined by a space, for the tests to
// read on their own.
var sourceWebGradients = []sourceWebGradient{
`, sourceSHA256)
	for _, g := range all {
		fmt.Fprintf(&b, "{%q, %q, %q},\n", g.index, g.name, g.declarations)
	}
	b.WriteString(`}

type sourceWebGradient struct{ index, name, declarations string }
`)
	write("webgradients_source_test.go", b.Bytes())
}

// read is the stylesheet's blocks, in order, each read: every one numbered in turn, its class its name in
// .ui spelling with _ for -, its declarations background-image or background with an optional
// background-blend-mode, nothing outside the blocks.
func read(css string) []gradient {
	if rest := strings.TrimSpace(block.ReplaceAllString(css, "")); rest != "" {
		fail(fmt.Sprintf("text outside the blocks: %.80q", rest))
	}
	var all []gradient
	for i, m := range block.FindAllStringSubmatch(css, -1) {
		g := gradient{index: m[1], name: m[2], class: m[3]}
		if want := fmt.Sprintf("%03d", i+1); g.index != want {
			fail(fmt.Sprintf("block %d has the number %q, want %q", i+1, g.index, want))
		}
		if want := strings.ReplaceAll(spell(g.name), "-", "_"); g.class != want {
			fail(fmt.Sprintf("%s %s: the class %q, want %q", g.index, g.name, g.class, want))
		}
		var lines []string
		for _, line := range strings.Split(m[4], "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		g.declarations = strings.Join(lines, " ")
		image, shorthand := "", false
		for _, d := range strings.Split(m[4], ";") {
			if d = strings.TrimSpace(d); d == "" {
				continue
			}
			property, value, _ := strings.Cut(d, ":")
			switch property = strings.TrimSpace(property); property {
			case "background-image", "background":
				if image != "" {
					fail(g.name + ": two backgrounds")
				}
				image, shorthand = strings.TrimSpace(value), property == "background"
			case "background-blend-mode":
				g.blend = splitTop(value)
			default:
				fail(fmt.Sprintf("%s: the declaration %q", g.name, d))
			}
		}
		for _, text := range splitTop(image) {
			g.layers = append(g.layers, readLayer(&g, text, shorthand))
		}
		if len(g.layers) == 0 {
			fail(g.name + ": no layer")
		}
		all = append(all, g)
	}
	if len(all) != 180 {
		fail(fmt.Sprintf("webgradients.css has %d gradients, want 180", len(all)))
	}
	return all
}

// readLayer reads one layer: a linear-gradient() or a radial-gradient(), in the background shorthand
// with the base colour written beside it.
func readLayer(g *gradient, text string, shorthand bool) layer {
	open := strings.Index(text, "-gradient(")
	if open < 0 {
		fail(fmt.Sprintf("%s: the layer %q", g.name, text))
	}
	start := strings.LastIndexByte(text[:open], ' ') + 1
	l := layer{kind: text[start:open]}
	if l.kind != "linear" && l.kind != "radial" {
		fail(fmt.Sprintf("%s: a %s gradient", g.name, l.kind))
	}
	depth, end := 0, -1
	for i := open + len("-gradient"); i < len(text) && end < 0; i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				end = i
			}
		}
	}
	if end < 0 {
		fail(fmt.Sprintf("%s: the layer %q is not closed", g.name, text))
	}
	if outside := strings.TrimSpace(text[:start] + text[end+1:]); outside != "" {
		if !shorthand || !hexColour.MatchString(outside) || g.base != "" {
			fail(fmt.Sprintf("%s: %q beside the layer", g.name, outside))
		}
		g.base = hexOf(outside)
	}
	args := splitTop(text[open+len("-gradient(") : end])
	if !stopText.MatchString(args[0]) {
		l.lead, args = args[0], args[1:]
	}
	for _, a := range args {
		m := stopText.FindStringSubmatch(a)
		if m == nil {
			fail(fmt.Sprintf("%s: the stop %q", g.name, a))
		}
		s := stop{}
		s.at, _ = strconv.ParseFloat(m[2], 64)
		switch c := m[1]; {
		case strings.HasPrefix(c, "rgba("):
			parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(c, "rgba("), ")"), ",")
			if alpha, err := strconv.ParseFloat(strings.TrimSpace(parts[len(parts)-1]), 64); err != nil || len(parts) != 4 || alpha >= 1 {
				fail(fmt.Sprintf("%s: the colour %q", g.name, c))
			}
			l.translucent = true
		case strings.HasPrefix(c, "#"):
			s.colour = hexOf(c)
		default:
			if s.colour = namedHex[c]; s.colour == "" {
				fail(fmt.Sprintf("%s: the colour %q has no hex in namedHex", g.name, c))
			}
		}
		l.stops = append(l.stops, s)
	}
	if len(l.stops) < 2 {
		fail(g.name + ": fewer than two stops")
	}
	return l
}

// linearDirection is the direction word of a linear layer's lead: a keyword as it is, an angle the
// nearest word, none to bottom (CSS's own).
func linearDirection(name, lead string) string {
	if lead == "" {
		return "to-bottom"
	}
	if word, ok := keywordDirections[lead]; ok {
		return word
	}
	m := degrees.FindStringSubmatch(lead)
	if m == nil {
		fail(fmt.Sprintf("%s: the direction %q", name, lead))
	}
	deg, _ := strconv.ParseFloat(m[1], 64)
	return direction(deg)
}

// radialPosition is where a radial layer is centred: at center, or written with no at (a size only).
func radialPosition(name, lead string) string {
	if _, at, ok := strings.Cut(" "+lead, " at "); ok && at != "center" {
		fail(fmt.Sprintf("%s: the radial at %q", name, at))
	}
	return "center"
}

// splitTop is a list split at the commas outside brackets, each part trimmed.
func splitTop(list string) []string {
	var out []string
	depth, from := 0, 0
	for i := 0; i < len(list); i++ {
		switch list[i] {
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

// hexOf is a #rgb or #rrggbb colour as lowercase #rrggbb.
func hexOf(c string) string {
	c = strings.ToLower(c)
	if len(c) == 4 {
		c = string([]byte{'#', c[1], c[1], c[2], c[2], c[3], c[3]})
	}
	return c
}

func number(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func write(file string, source []byte) {
	formatted, err := format.Source(source)
	if err != nil {
		fail(file + ": " + err.Error())
	}
	if err := os.WriteFile(file, formatted, 0o644); err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "webgradients_gen:", message)
	os.Exit(1)
}
