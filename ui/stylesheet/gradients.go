package stylesheet

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Gradients (decision of 12/10/2026). background="glow" paints a gradient behind an element and
// color="headline" fills its text with one. A gradient is written one explicit way, the same in
// router.gradients and in the standard set:
//
//	router.gradients({
//	  hero: { type: "linear", direction: "to-bottom-end", stops: [ { color: "brand", at: 0 }, { color: "violet", at: 100 } ] },
//	  halo: { type: "radial", position: "top", stops: [ { color: "brand/40", at: 0 }, { color: "brand/0", at: 70 } ] }
//	});
//
// type is linear, radial or conic. A linear gradient has a direction (to-top … to-bottom-end), a radial
// or conic one a position (center, the default, top … bottom-end); start and end are the start and the
// end of a line, so they flip for right-to-left. Stops are { color, at }: at from 0 to 100, never going
// back; color a token (lighter -N, stronger +N, an alpha /N), white or black, or a named colour, exactly
// as color= takes it, or a { light, dark } pair of colours, exactly as router.colors writes a token
// (decision D2 of 13/10/2026: { color: { light: "#849cdd", dark: "#202f5e" }, at: 0 }). A page may
// write any gradient lighter or stronger (decision D3, its operators of 13/10/2026): background="cirrus-80"
// moves every stop 80% of the way to the mode's canvas, mixed in oklab as ink-30 is; background="cirrus+20"
// moves every stop 20% further from that canvas, as brand+15 is; the contrast check measures the moved
// stops. A gradient has no alpha: an alpha is a stop's. Gradient names share one namespace with
// colours: a gradient is never named like a token of the palette, a named colour, white, black,
// transparent or a standard gradient (nor a colour's or a standard gradient's letters written another
// way: slate-gray, fadetop), nor a keyword (none, auto, inherit…), nor ending in -<digits> (hero-2 would
// read as hero lighter by 2%), and a token is never named like a standard gradient. The site's
// gradients inherit down the folders as router.colors does, the nearest folder's winning per name.
//
// On the web a gradient background is background-image (a colour background a later condition writes
// takes it away again, background-image:none, on the element that has both: Rules.TakeBack); gradient
// text is the gradient clipped to the letters, background-clip:text with its -webkit- form and a
// transparent fill, over the first stop as a solid colour for a browser that cannot clip to text (or,
// for a gradient with a color-mix() stop, cannot read color-mix(): it would draw no image and no
// letters); in forced colours both give way to the system's colours. Neither animates. The stops are
// the colours the table writes (var(--token), a lighter colour's or an alpha's color-mix(), a stronger
// colour's custom property); a pair is a custom property of its own, --kitwork-gradient-<name>-<stop>,
// set on :root by the page's stylesheet in light and again under :root[data-theme=dark] for screens, as
// colors.<hash>.css sets a token (Rules.CSS). A page's -N is each stop's color-mix(in oklab, … (100-N)%,
// var(--canvas)); its +N is each stop's own custom property, --kitwork-gradient-<name>-strong-<N>-<stop>,
// the stronger colour worked out against each mode's canvas and set as a pair is (CSS has no function
// that moves a colour away from another), an alpha the stop writes kept around it. A gradient with a
// color-mix() stop is written a second time "in srgb", so the browser interpolates in sRGB, as the
// contrast check samples it, and not in Oklab, which CSS picks for a stop that is not a legacy colour.
//
// Native, for the record (not built): SwiftUI LinearGradient, RadialGradient and AngularGradient over
// Gradient(stops: [Gradient.Stop(color:location:)]), text through .foregroundStyle(gradient); Compose
// Brush.linearGradient, Brush.radialGradient and Brush.sweepGradient with colorStops, text through
// TextStyle(brush:).

// Gradient is one gradient as router.gradients writes it: Type linear, radial or conic; Direction for a
// linear one; Position for a radial or a conic one ("" is center); Stops in order.
type Gradient struct {
	Type, Direction, Position string
	Stops                     []Stop
	// Op and Amount are the step a page writes after a gradient's name (decision D3 of 13/10/2026, its
	// operators of the same day), Op 0 when it writes none: '-' lighter, cirrus-80, every stop moved
	// Amount% of the way to the mode's canvas in oklab exactly as a lighter colour is (ink-30), toward
	// white in light and toward the soft black in dark; '+' stronger, cirrus+20, every stop moved Amount%
	// further from that canvas exactly as a stronger colour is (brand+15). Palette.Gradient sets them;
	// router.gradients never does.
	Op     byte
	Amount int
	// name is the gradient's name in the palette: the custom properties its pairs are written in
	name string
}

// Stop is one colour of a gradient and where it stands along it, at from 0 to 100: Color, a colour
// exactly as color= takes it (a token, lighter or stronger, an alpha, white or black, a named colour),
// or Pair, its own colour in each mode, { light, dark }, as router.colors writes a token (decision D2
// of 13/10/2026; each "#rgb", "#rrggbb" or a named colour). A stop is one or the other.
type Stop struct {
	Color string
	At    float64
	Pair  Color
}

// paired reports whether the stop is a { light, dark } pair.
func (s Stop) paired() bool { return s.Pair != (Color{}) }

// fixed is the colour of a pair that is one colour in light and in dark (every stop of the WebGradients
// collection), lowercase #rrggbb: the table writes it as it is, as it writes a named colour, with no
// custom property to set per mode. ok is false for any other stop.
func (s Stop) fixed() (hex string, ok bool) {
	if !s.paired() {
		return "", false
	}
	light, problem := pairHex(s.Pair.Light)
	dark, otherProblem := pairHex(s.Pair.Dark)
	return light, problem == "" && otherProblem == "" && light == dark
}

// fixedStop is a stop of one colour, the same in light and in dark, at a place: a pair of one hex.
func fixedStop(hex string, at float64) Stop {
	return Stop{Pair: Color{Light: hex, Dark: hex}, At: at}
}

// gradientTypes are the kinds of gradient there are.
var gradientTypes = []string{"linear", "radial", "conic"}

// gradientDirections are the directions of a linear gradient, as CSS writes them from left to right and
// from right to left.
var gradientDirections = map[string][2]string{
	"to-top":          {"to top", "to top"},
	"to-bottom":       {"to bottom", "to bottom"},
	"to-start":        {"to left", "to right"},
	"to-end":          {"to right", "to left"},
	"to-top-start":    {"to top left", "to top right"},
	"to-top-end":      {"to top right", "to top left"},
	"to-bottom-start": {"to bottom left", "to bottom right"},
	"to-bottom-end":   {"to bottom right", "to bottom left"},
}

var gradientDirectionWords = []string{"to-top", "to-bottom", "to-start", "to-end", "to-top-start", "to-top-end", "to-bottom-start", "to-bottom-end"}

// gradientPositions are where a radial or a conic gradient is centred, from left to right and from
// right to left.
var gradientPositions = map[string][2]string{
	"center":       {"center", "center"},
	"top":          {"top", "top"},
	"bottom":       {"bottom", "bottom"},
	"start":        {"left", "right"},
	"end":          {"right", "left"},
	"top-start":    {"top left", "top right"},
	"top-end":      {"top right", "top left"},
	"bottom-start": {"bottom left", "bottom right"},
	"bottom-end":   {"bottom right", "bottom left"},
}

var gradientPositionWords = []string{"center", "top", "bottom", "start", "end", "top-start", "top-end", "bottom-start", "bottom-end"}

// gradientKeywords are words that mean something else where a gradient's name stands — none (background
// has none: transparent is token/0), auto, CSS's keywords for a value, current for the text's colour —
// so no gradient is named by one: background="none" stays the error it is.
var gradientKeywords = map[string]bool{
	"none": true, "auto": true, "current": true, "currentcolor": true,
	"inherit": true, "initial": true, "unset": true, "revert": true, "revert-layer": true,
}

// physicalSides are the words of CSS a hand writes where .ui says start and end: what to write instead.
var physicalSides = strings.NewReplacer("left", "start", "right", "end")

// gradientName is a gradient's name: lowercase words joined by - (hero, card-glow). No dot: a dotted
// name is a token's (brand.on). Never ending in -<digits> either, which CheckGradient tells apart
// (stepNameError): in .ui, hero-2 is hero lighter by 2%.
var gradientName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// standardGradient is one gradient of the standard set.
type standardGradient struct {
	name string
	Gradient
}

func linear(direction string, stops ...Stop) Gradient {
	return Gradient{Type: "linear", Direction: direction, Stops: stops}
}

// standardGradients is the standard set, built into every site (decision of 12/10/2026, designed and
// measured in light and dark on three palettes). The first ten follow the theme, built on the palette's
// roles; the last eight are fixed, built on named colours, the same in light and dark.
var standardGradients = []standardGradient{
	// content dissolving into the page: under a sticky header, at the foot of a clipped list, at the
	// edges of a horizontal scroller (start and end flip for right-to-left)
	{"fade-top", linear("to-top", Stop{Color: "canvas/0", At: 0}, Stop{Color: "canvas", At: 100})},
	{"fade-bottom", linear("to-bottom", Stop{Color: "canvas/0", At: 0}, Stop{Color: "canvas", At: 100})},
	{"fade-start", linear("to-start", Stop{Color: "canvas/0", At: 0}, Stop{Color: "canvas", At: 100})},
	{"fade-end", linear("to-end", Stop{Color: "canvas/0", At: 0}, Stop{Color: "canvas", At: 100})},
	// a loading placeholder, its band in the middle more ink: darker in light, lighter in dark
	{"skeleton", linear("to-end", Stop{Color: "ink-88", At: 0}, Stop{Color: "ink-88", At: 35}, Stop{Color: "ink-82", At: 50}, Stop{Color: "ink-88", At: 65}, Stop{Color: "ink-88", At: 100})},
	// a divider that fades out at both ends, on a box one pixel tall
	{"hairline", linear("to-end", Stop{Color: "line/0", At: 0}, Stop{Color: "line", At: 50}, Stop{Color: "line/0", At: 100})},
	// a soft brand halo behind the top of a hero: the brand 85% of the way to the canvas, the tinted
	// fill ink-30 still reads on (operators of 13/10/2026)
	{"glow", Gradient{Type: "radial", Position: "top", Stops: []Stop{{Color: "brand-85", At: 0}, {Color: "brand-85/0", At: 70}}}},
	// a brand wash that settles into whatever is under it: a band, the highlighted card, under a chart
	{"tint", linear("to-bottom", Stop{Color: "brand-85", At: 0}, Stop{Color: "brand-85/0", At: 100})},
	// gradient headline text: the page's ink warming into the brand, an engine colour (engine_colour.go):
	// in light brand+40, 40% further from the canvas so the brand end reads as text on every palette
	// measured; in dark the smallest step from 0 that reads, the brand itself on those palettes
	{"headline", linear("to-end", Stop{Color: "ink", At: 0}, Stop{Color: HeadlineColour, At: 100})},
	// an inverse band that flips with the mode, under paper text; its brand start the same way
	{"inverse", linear("to-bottom-end", Stop{Color: InverseColour, At: 0}, Stop{Color: "ink", At: 100})},
	// scrims under white text over a photo, strongest at the edge they name
	{"scrim-top", linear("to-top", Stop{Color: "black/55", At: 0}, Stop{Color: "black/80", At: 100})},
	{"scrim-bottom", linear("to-bottom", Stop{Color: "black/55", At: 0}, Stop{Color: "black/80", At: 100})},
	// a dark band that stays dark in both modes, under white text
	{"night", linear("to-bottom-end", Stop{Color: "midnightblue", At: 0}, Stop{Color: "black", At: 100})},
	// gold with a pale sheen: the paid plan, a Pro badge; black text
	{"gilded", linear("to-bottom-end", Stop{Color: "metallic-gold", At: 0}, Stop{Color: "jasmine", At: 40}, Stop{Color: "darkgoldenrod", At: 100})},
	// deep blue into teal: a band under white text, or large headline text
	{"ocean", linear("to-bottom-end", Stop{Color: "royalblue", At: 0}, Stop{Color: "teal", At: 100})},
	// violet through orchid into pink: large headline text, or a band under black text
	{"nebula", linear("to-end", Stop{Color: "mediumslateblue", At: 0}, Stop{Color: "mediumorchid", At: 50}, Stop{Color: "razzmatazz", At: 100})},
	// crimson into burnt orange: a sale or launch band under white text, or large headline text
	{"magma", linear("to-end", Stop{Color: "crimson", At: 0}, Stop{Color: "burnt-orange", At: 100})},
	// a rainbow ring, pure decoration, seamless at any angle
	{"prism", Gradient{Type: "conic", Position: "center", Stops: []Stop{{Color: "red", At: 0}, {Color: "orange", At: 17}, {Color: "gold", At: 33}, {Color: "limegreen", At: 50}, {Color: "deepskyblue", At: 67}, {Color: "blueviolet", At: 83}, {Color: "red", At: 100}}}},
}

// builtinGradients are every gradient built into every site: the standard set, the page backdrops
// (backdrops.go), then the WebGradients collection (webgradients.go). One namespace: no site gradient
// and no token takes one of their names.
var builtinGradients = append(append(append([]standardGradient(nil), standardGradients...), backdropGradients...), webGradientSet...)

// The WebGradients collection (decisions W1–W5 of 13/10/2026): the gradients of
// github.com/itmeo/webgradients (MIT, Copyright (c) 2017 itmeo; WEBGRADIENTS_LICENSE ships next to the
// data), built into every site like the standard set and needing no import: background="warm-flame",
// color="warm-flame", <page background="warm-flame">, warm-flame-60, the CSS written only for the
// gradients a page uses. webgradients.go is generated by webgradients_gen.go from the collection's
// stylesheet, webgradients.css, the one its site draws (its gradients.json loses directions, stops and
// layers). Each keeps its own name in .ui spelling (Warm Flame is warm-flame, Arielle's Smile
// arielles-smile) but twelve, renamed (webGradientRenames): light-blue would be the colour lightblue
// written with a hyphen, Itmeo Branding is the author's own brand, nine are awkward in a product's code;
// a page that writes the collection's name is told the new one (renamedGradient). Each is the top layer
// the stylesheet writes: a linear one's direction the stylesheet's word (to right is to-end) or its
// angle's nearest — at most 22.5deg away, 20deg in this collection — a radial one centred, reaching the
// box's farthest corner; its stops the stylesheet's colours at its places, each the same colour in light
// and dark like a named colour (fixedStop); -N makes it quieter toward the canvas and +N stronger, as
// for any gradient.
// Of the 180, 175 are built in, four of them drawn short of the stylesheet (Arielle's Smile and Elegance
// at the one radial size, the blended layers under Raccoon Back, Elegance and Slick Carbon not drawn);
// five are left out (webGradientsLeftOut), a base colour seen through translucent layers that one layer
// cannot draw, and a page that writes one is told so (renamedGradient).

// WebGradients lists the WebGradients collection by its .ui names, in the collection's order (001 to
// 180, the five left out skipped).
func WebGradients() []string {
	out := make([]string, len(webGradientSet))
	for i, s := range webGradientSet {
		out[i] = s.name
	}
	return out
}

// webGradientNames are the collection's .ui names, for builtinKind.
var webGradientNames = func() map[string]bool {
	out := make(map[string]bool, len(webGradientSet))
	for _, s := range webGradientSet {
		out[s.name] = true
	}
	return out
}()

// webGradientRenamedLetters is every renamed gradient's own name by its letters (letters): Lady Lips,
// LadyLips and lady-lips are all lady-lips.
var webGradientRenamedLetters = lettersOf(webGradientRenames)

// renamedWebGradient is the .ui name of a gradient of the WebGradients collection that a word names by
// the collection's own name, however its letters are written (lady-lips, LadyLips: pink-blush), and that
// own name as the collection's map keeps it. ok is false for any other word, and for one with the
// letters of the .ui name itself: Arielles-Smile and arielle-s-smile are arielles-smile written
// otherwise, which gradientMisused tells.
func renamedWebGradient(word string) (name, original string, ok bool) {
	original, ok = webGradientRenamedLetters[letters(word)]
	name = webGradientRenames[original]
	if !ok || letters(name) == letters(word) {
		return "", "", false
	}
	return name, original, true
}

// webGradientsLeftOutLetters is every left-out gradient's name by its letters (letters).
var webGradientsLeftOutLetters = lettersOf(webGradientsLeftOut)

var standardByName = func() map[string]Gradient {
	out := make(map[string]Gradient, len(builtinGradients))
	for _, s := range builtinGradients {
		out[s.name] = s.Gradient
	}
	return out
}()

// StandardGradients lists the standard set by name, in its designed order: the ten that follow the
// theme, then the eight fixed ones. The page backdrops are Backdrops.
func StandardGradients() []string {
	out := make([]string, len(standardGradients))
	for i, s := range standardGradients {
		out[i] = s.name
	}
	return out
}

// StandardGradient is a gradient built into every site, of the standard set or a page backdrop; ok is
// false for any other name.
func StandardGradient(name string) (Gradient, bool) {
	g, ok := standardByName[name]
	return g.clone(), ok
}

func (g Gradient) clone() Gradient {
	g.Stops = append([]Stop(nil), g.Stops...)
	return g
}

// ---- checking a gradient ----

// CheckGradient checks a gradient router.gradients declares, as far as it can be read without the
// palette: its name, its type, its direction or position, its stops and where they stand. Whether each
// stop's colour is one the route's palette names, and whether the name is a token of it, is
// Palette.WithGradients's. The message says, in the author's language, what is written and what to
// write.
func CheckGradient(name string, g Gradient) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("router.gradients: %q "+format, append([]any{name}, args...)...)
	}
	switch {
	case !gradientName.MatchString(name):
		return fail("không phải tên gradient: chữ thường, các từ nối bằng - (hero, card-glow), không có dấu . (tên có dấu . là của token: brand.on)")
	case stepNameError("router.gradients", name) != nil:
		return stepNameError("router.gradients", name) // hero-2 would read as hero lighter by 2
	case standardByName[name].Type != "":
		return fail("là tên một %s của Kitwork, có sẵn: viết background=%q hay color=%q là có. Gradient riêng đặt tên khác", builtinKind(name), name, name)
	case fixedColors[name] != "":
		return fail("là màu cố định %s: tên gradient và tên màu chung một chỗ, đặt tên khác", name)
	case name == "transparent":
		return fail("không đặt được cho gradient: trong suốt là token/0 (paper/0); đặt tên khác")
	case gradientKeywords[name]:
		return fail("là một từ khoá, không phải tên: background=%q hay color=%q không bao giờ là một gradient; đặt tên khác", name, name)
	}
	if renamed, _, ok := renamedWebGradient(name); ok && namedColours[name] == "" { // lightblue is the colour
		return fail("là tên gốc của gradient %s trong bộ WebGradients, có sẵn: background=%q là lỗi gợi %s, không là gradient riêng; đặt tên khác", renamed, name, renamed)
	}
	for _, s := range builtinGradients {
		if letters(s.name) == letters(name) {
			return fail("là cách viết khác của %s %s: background=%q là lỗi gợi %s, không là gradient riêng; đặt tên khác", builtinKind(s.name), s.name, name, s.name)
		}
	}
	if hex, ok := NamedColour(name); ok {
		return fail("đã là tên một màu (%s): tên gradient và tên màu chung một chỗ, đặt tên khác", hex)
	}
	if colour, _, ok := namedOtherwise(name); ok {
		return fail("là cách viết khác của màu %s (%s): tên gradient và tên màu chung một chỗ, đặt tên khác", colour, namedColours[colour])
	}
	switch g.Type {
	case "":
		return fail("thiếu type: linear, radial hoặc conic")
	case "linear", "radial", "conic":
	default:
		return fail("có type %q: type là linear, radial hoặc conic", g.Type)
	}
	if g.Type == "linear" {
		if g.Position != "" {
			return fail("là linear: không có position, hướng của nó là direction (%s)", strings.Join(gradientDirectionWords, ", "))
		}
		if g.Direction == "" {
			return fail("là linear nên cần direction: %s", strings.Join(gradientDirectionWords, ", "))
		}
		if _, ok := gradientDirections[g.Direction]; !ok {
			return fail("có direction %q: direction là %s%s", g.Direction, strings.Join(gradientDirectionWords, ", "), sideHint(g.Direction, gradientDirections))
		}
	} else {
		if g.Direction != "" {
			return fail("là %s: không có direction, tâm của nó là position (%s)", g.Type, strings.Join(gradientPositionWords, ", "))
		}
		if _, ok := gradientPositions[g.Position]; !ok && g.Position != "" {
			return fail("có position %q: position là %s%s", g.Position, strings.Join(gradientPositionWords, ", "), sideHint(g.Position, gradientPositions))
		}
	}
	if len(g.Stops) < 2 {
		return fail("cần ít nhất hai điểm dừng: stops: [ { color: \"brand\", at: 0 }, { color: \"brand+15\", at: 100 } ]")
	}
	for i, s := range g.Stops {
		place := "điểm dừng " + strconv.Itoa(i+1)
		switch {
		case s.paired() && strings.TrimSpace(s.Color) != "":
			return fail("%s có cả color %q và một cặp { light, dark }: một điểm dừng là một màu hoặc một cặp", place, s.Color)
		case s.paired():
			if problem := checkPair(s.Pair); problem != "" {
				return fail("%s: %s", place, problem)
			}
		case strings.TrimSpace(s.Color) == "":
			return fail("%s thiếu color: mỗi điểm dừng là { color, at }", place)
		}
		if s.At < 0 || s.At > 100 {
			return fail("%s có at %s: at là vị trí trên gradient, từ 0 đến 100", place, number(s.At))
		}
		if i > 0 && s.At < g.Stops[i-1].At {
			return fail("%s có at %s, nhỏ hơn at %s của điểm dừng trước: các điểm dừng viết theo thứ tự at tăng dần", place, number(s.At), number(g.Stops[i-1].At))
		}
	}
	return nil
}

// builtinKind is how a message names a gradient built into every site: a page backdrop is a "nền
// trang" (Backdrops), one of the WebGradients collection a "gradient trong bộ WebGradients"
// (WebGradients), any other a "gradient chuẩn" (StandardGradients).
func builtinKind(name string) string {
	for _, s := range backdropGradients {
		if s.name == name {
			return "nền trang"
		}
	}
	if webGradientNames[name] {
		return "gradient trong bộ WebGradients"
	}
	return "gradient chuẩn"
}

// checkPair checks a stop's { light, dark } pair as router.colors checks a token's: both written, each
// "#rgb", "#rrggbb" or a named colour. "" when it reads; else what is wrong, in the author's words.
func checkPair(pair Color) string {
	for _, side := range []struct{ mode, value string }{{"light", pair.Light}, {"dark", pair.Dark}} {
		if strings.TrimSpace(side.value) == "" {
			return "cặp màu thiếu " + side.mode + ": color { light, dark } viết cả hai, như một token của router.colors ({ light: \"#849cdd\", dark: \"#202f5e\" })"
		}
		if _, problem := pairHex(side.value); problem != "" {
			return side.mode + " là " + strconv.Quote(side.value) + ": " + problem
		}
	}
	return ""
}

// pairHex reads one side of a stop's pair as lowercase #rrggbb: "#rgb", "#rrggbb", or a named colour as
// its value. problem says, in the author's words, why a value does not read.
func pairHex(v string) (hex, problem string) {
	h := strings.TrimSpace(v)
	if named, ok := NamedColour(h); ok {
		return named, ""
	}
	if _, why, ok := namedOtherwise(h); ok {
		return "", why
	}
	if !hexColor.MatchString(h) {
		return "", "một màu của cặp là \"#rgb\", \"#rrggbb\" hay một màu có tên (tomato, cerulean…); token (brand, ink-30) thì viết color: \"brand\""
	}
	h = strings.ToLower(h)
	if len(h) == 4 {
		h = string([]byte{'#', h[1], h[1], h[2], h[2], h[3], h[3]})
	}
	return h, ""
}

// sideHint is what to write for a direction or a position written with left or right: start and end,
// which flip for right-to-left. "" when the word is not that.
func sideHint(word string, words map[string][2]string) string {
	if spelled := physicalSides.Replace(word); spelled != word {
		if _, ok := words[spelled]; ok {
			return "; trái phải viết start và end, tự lật khi viết từ phải sang trái: " + spelled
		}
	}
	return ""
}

// ---- the palette's gradients ----

// WithGradients is the palette with a site's gradients: chain holds the router.gradients() of each
// folder from the site root down to the route, nearest last, and the nearest folder's gradient wins per
// name. Each is checked (CheckGradient), its name must not be a token of the palette, and every stop's
// colour must be one the palette names (a token, lighter or stronger, an alpha, white or black, a named
// colour).
// The standard set needs no declaring: every palette has it.
func (p *Palette) WithGradients(chain ...map[string]Gradient) (*Palette, error) {
	merged := map[string]Gradient{}
	for _, layer := range chain {
		for name, g := range layer {
			merged[name] = g.clone()
		}
	}
	if len(merged) == 0 && len(p.gradients) == 0 {
		return p, nil
	}
	q := *p
	q.gradients = merged
	table := NewTable(&q)
	for _, name := range sortedKeys(merged) {
		g := merged[name]
		if err := CheckGradient(name, g); err != nil {
			return nil, err
		}
		if p.Has(name) {
			return nil, fmt.Errorf("router.gradients: %q đã là token màu của bảng màu: tên gradient và tên màu chung một chỗ, đặt tên khác (%s-gradient…)", name, name)
		}
		for i, s := range g.Stops {
			if s.paired() {
				continue // its own colours, read by CheckGradient: no palette names them
			}
			v := strings.TrimSpace(s.Color)
			if base, _, _ := strings.Cut(v, "/"); q.isGradient(base) || q.isGradient(v) {
				return nil, fmt.Errorf("router.gradients: %q, điểm dừng %d: %q là một gradient; điểm dừng là một màu (token, nhạt hơn token-N, đậm hơn token+N, độ hiện token/N, white, black hay màu có tên)", name, i+1, v)
			}
			if _, _, _, _, problem := table.colour(Intent{Tag: stopTag, Name: "color", Value: v}, v); problem != nil {
				return nil, fmt.Errorf("router.gradients: %q, điểm dừng %d: %s", name, i+1, problem.Message)
			}
		}
	}
	return &q, nil
}

// stopTag marks a colour read as a gradient's stop, not an attribute: its message names no gradient.
const stopTag = "router.gradients"

// colourOnly reports whether a colour is read where only a colour goes, outside any attribute: a
// gradient's stop, or a kind's colour of router.highlight (highlightTag). Its message names no gradient,
// suggests none and counts none among the words near a typo.
func colourOnly(in Intent) bool { return in.Tag == stopTag || in.Tag == highlightTag }

// Gradient is a gradient the palette names: the site's own (WithGradients), else one built into every
// site (the standard set, a page backdrop, the WebGradients collection), as a page writes it: its name,
// or its name with a step from 0 to 100, lighter -N (cirrus-80) or stronger +N (cirrus+20):
// Gradient.Op and Gradient.Amount. A gradient's name never ends in -<digits> (CheckGradient), so the
// step is read off the end as a colour's is (SplitStep). ok is false for any other word, and for a step
// outside 0–100 or an alpha (the table tells the author: gradientMisused).
func (p *Palette) Gradient(name string) (Gradient, bool) {
	base, op, amount, stepped := SplitStep(name)
	g, ok := p.gradients[base]
	if ok {
		g = g.clone()
	} else if g, ok = StandardGradient(base); !ok {
		return Gradient{}, false
	}
	g.name = base
	if stepped {
		n, ok := whole(amount, 0, 100)
		if !ok {
			return Gradient{}, false
		}
		g.Op, g.Amount = op, n
	}
	return g, true
}

func (p *Palette) isGradient(name string) bool {
	_, ok := p.Gradient(name)
	return ok
}

// Gradients lists the gradients the palette names but the WebGradients collection: the site's, the
// standard set and the page backdrops, sorted. Every palette names the collection too (WebGradients):
// 175 names, too many for a message to list.
func (p *Palette) Gradients() []string {
	out := append(StandardGradients(), Backdrops()...)
	for name := range p.gradients {
		if standardByName[name].Type == "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// GradientSamples is a gradient painted in one mode at the eleven points the contrast check reads,
// 0%, 10% … 100% along it (along the line, the ray or the turn alike): each painted between the two
// stops around it as a browser interpolates, in sRGB with premultiplied alpha, its alpha straight;
// before the first stop the first, after the last the last. Each stop is painted as paintStop paints
// it: a colour as Paint does, a pair its colour of the mode, and with the page's -N or +N each moved
// toward or away from that mode's canvas. ok is false for a name that is no gradient of the palette,
// or a stop it cannot paint.
func (p *Palette) GradientSamples(name string, m Mode) ([]RGBA, bool) {
	g, ok := p.Gradient(name)
	if !ok {
		return nil, false
	}
	type painted struct {
		c  RGBA
		at float64
	}
	stops := make([]painted, 0, len(g.Stops))
	for _, s := range g.Stops {
		c, ok := p.paintStop(g, s, m)
		if !ok {
			return nil, false
		}
		stops = append(stops, painted{c: c, at: s.At / 100})
	}
	out := make([]RGBA, 0, GradientSampleCount)
	for i := 0; i < GradientSampleCount; i++ {
		t := float64(i) / float64(GradientSampleCount-1)
		sample := stops[len(stops)-1].c
		if t <= stops[0].at {
			sample = stops[0].c
			if t == stops[0].at {
				sample = stops[g.first()].c // at the first place, the last stop there shows (Gradient.first)
			}
		} else {
			for j := 0; j+1 < len(stops); j++ {
				from, to := stops[j], stops[j+1]
				if t < from.at || t > to.at {
					continue
				}
				f := 0.0
				if to.at > from.at {
					f = (t - from.at) / (to.at - from.at)
				} else {
					f = 1 // a hard stop: the later colour from it on
				}
				sample = premultipliedMix(from.c, to.c, f)
				break
			}
		}
		out = append(out, sample)
	}
	return out, true
}

// GradientSampleCount is how many points along a gradient the contrast check reads: every tenth.
const GradientSampleCount = 11

// paintStop is one stop of a gradient as the browser paints it in a mode: a colour as Paint paints it,
// a pair its colour of that mode; then, when the page wrote the gradient lighter (cirrus-80), that
// colour moved N% of the way to the mode's canvas, mixed in oklab with premultiplied alpha exactly as
// color-mix(in oklab, X (100-N)%, var(--canvas)) is; when it wrote it stronger (cirrus+20), the colour
// moved N% further from that canvas (Stronger) and rounded to the #rrggbb its custom property holds, its
// alpha kept. A step of 0 is the stop itself, and the table writes nothing for it.
func (p *Palette) paintStop(g Gradient, s Stop, m Mode) (RGBA, bool) {
	var c RGBA
	if s.paired() {
		side := s.Pair.Light
		if m == DarkMode {
			side = s.Pair.Dark
		}
		hex, problem := pairHex(side)
		if problem != "" {
			return RGBA{}, false
		}
		c, _ = ParseHex(hex)
	} else {
		var ok bool
		if c, ok = p.Paint(s.Color, m); !ok {
			return RGBA{}, false
		}
	}
	if g.Amount == 0 {
		return c, true
	}
	canvas, _ := p.token("canvas", m)
	switch g.Op {
	case '-':
		c = mixOklabOver(c, canvas, float64(100-g.Amount))
	case '+':
		alpha := c.A
		c, _ = ParseHex(Stronger(c, canvas, g.Amount).Hex()) // what the custom property holds (stopCSS)
		c.A = alpha
	}
	return c, true
}

// GradientGround is the colour a gradient's first stop paints in a mode (the one it shows first,
// Gradient.first), solid (its alpha dropped, the page's step kept): what the table writes as the colour
// of gradient text where the browser cannot clip to the text, and, for a page backdrop, the ground of
// <html> and the browser's bar (theme-color). ok is false for a value that is no gradient of the
// palette.
func (p *Palette) GradientGround(name string, m Mode) (RGBA, bool) {
	g, ok := p.Gradient(strings.TrimSpace(name))
	if !ok {
		return RGBA{}, false
	}
	return p.paintStop(g, solidStop(g.Stops[g.first()]), m)
}

// first is the place in Stops of the stop a gradient shows first: the last of the stops written at the
// first stop's place. Stops at one place make a sudden change there, from the first written to the last
// (CSS Images 3), so at the start of a gradient only the last shows: Star Wine of the WebGradients
// collection writes #b8cbb8 twice at 0 before #b465da, also at 0, and starts purple.
func (g Gradient) first() int {
	i := 0
	for i+1 < len(g.Stops) && g.Stops[i+1].At == g.Stops[0].At {
		i++
	}
	return i
}

// solidStop is a stop with its alpha dropped: paper/0 is paper; a pair is opaque already.
func solidStop(s Stop) Stop {
	if s.paired() {
		return s
	}
	if c, ok := SplitColour(strings.TrimSpace(s.Color)); ok {
		c.Alpha, c.Translucent = 100, false
		s.Color = c.String()
	}
	return s
}

// mixOklabOver is color-mix(in oklab, x p%, y) for y opaque (a canvas) and x of any alpha, as CSS Color 5
// mixes: each colour premultiplied by its alpha, interpolated in OKLab, the alpha interpolated too and
// divided out again. For x opaque it is MixOklab.
func mixOklabOver(x, y RGBA, percent float64) RGBA {
	if x.A >= 1 {
		return MixOklab(x, y, percent)
	}
	f := percent / 100
	alpha := x.A*f + y.A*(1-f)
	if alpha <= 0 {
		return RGBA{}
	}
	a, b := toOklab(x), toOklab(y)
	mix := func(u, v float64) float64 { return (u*x.A*f + v*y.A*(1-f)) / alpha }
	out := fromOklab(oklab{L: mix(a.L, b.L), A: mix(a.A, b.A), B: mix(a.B, b.B)})
	out.A = alpha
	return out
}

// premultipliedMix is the colour a fraction f of the way from a to b, interpolated in sRGB with
// premultiplied alpha (a transparent end lends no colour of its own), alpha straight in the result.
func premultipliedMix(a, b RGBA, f float64) RGBA {
	alpha := a.A + (b.A-a.A)*f
	if alpha <= 0 {
		return RGBA{R: b.R, G: b.G, B: b.B}
	}
	mix := func(x, y float64) float64 { return (x*a.A + (y*b.A-x*a.A)*f) / alpha }
	return RGBA{R: clip(mix(a.R, b.R)), G: clip(mix(a.G, b.G)), B: clip(mix(a.B, b.B)), A: alpha}
}

// ---- the table: background and color take a gradient ----

// The conditions a gradient's rules hold under besides the class's own: right-to-left (start and end
// flip), a browser that can clip a background to the text (gradient text), and forced colours, where
// the system's colours replace the gradient. Gradient text with a color-mix() stop (lighter, an alpha)
// also needs a browser that reads color-mix() (clipsToTextMixed): one that clips to the text without it
// (Chrome and Edge 99–110, Safari 15.4–16.1, through -webkit-) drops both images and would keep the
// transparent fill, the text gone; without the condition it keeps the first stop's solid colour.
const (
	rightToLeft      = ":where(:dir(rtl))"
	clipsToText      = "@supports (background-clip:text) or (-webkit-background-clip:text)"
	clipsToTextMixed = "@supports ((background-clip:text) or (-webkit-background-clip:text)) and (color:color-mix(in srgb,red,red))"
	forcedColors     = "@media (forced-colors:active)"
)

// gradientImages is a gradient as background-image declarations, left to right and right to left (""
// when nothing in it flips): one declaration, or, when a stop is a color-mix() (lighter, an alpha:
// mixed), the same image again "in srgb" after it, so a browser that reads the interpolation space
// interpolates in sRGB and one that does not keeps the first.
func (t *Table) gradientImages(g Gradient) (ltr, rtl string, mixed bool) {
	var stops []string
	for i, s := range g.Stops {
		css, _ := t.stopCSS(g, i, s)
		mixed = mixed || strings.Contains(css, "color-mix(")
		stops = append(stops, css+" "+number(s.At)+"%")
	}
	write := func(lead string) string {
		image := g.Type + "-gradient(" + lead + "," + strings.Join(stops, ",") + ")"
		out := "background-image:" + image
		if mixed {
			out += ";background-image:" + g.Type + "-gradient(" + lead + " in srgb," + strings.Join(stops, ",") + ")"
		}
		return out
	}
	var sides [2]string
	if g.Type == "linear" {
		sides = gradientDirections[g.Direction]
	} else {
		position := g.Position
		if position == "" {
			position = "center"
		}
		at := gradientPositions[position]
		sides = [2]string{"at " + at[0], "at " + at[1]}
	}
	ltr = write(sides[0])
	if sides[1] != sides[0] {
		rtl = write(sides[1])
	}
	return ltr, rtl, mixed
}

// solidFirstStop is a gradient's first stop (the one it shows first, Gradient.first) as a solid colour
// (its alpha dropped, the page's step kept): the colour of gradient text where the browser cannot clip
// to the text, and the colour its icons and underline take. The custom properties it reads are among
// the gradient's own (pairsOf).
func (t *Table) solidFirstStop(g Gradient) string {
	i := g.first()
	css, _ := t.stopCSS(g, i, solidStop(g.Stops[i]))
	return css
}

// stopCSS is the CSS colour of the stop at place i of a gradient (s, that stop or a form of it), and the
// custom properties it reads, for the page's stylesheet to set (Rules.CSS): the colour the table writes
// for a colour (var(--token), a lighter colour's or an alpha's color-mix(), a stronger colour's custom
// property, a named colour's hex), an engine colour's custom property (the brand end of headline and
// inverse, engine_colour.go), the custom property of its place for a pair (pairVariable), the hex
// of a pair of one colour (Stop.fixed); when the page wrote the gradient lighter (cirrus-80), that colour
// mixed toward the canvas as a lighter colour is; when it wrote it stronger (cirrus+20), the stop's own
// custom property per mode (strongerStop), its alpha around it.
func (t *Table) stopCSS(g Gradient, i int, s Stop) (string, []pairValue) {
	if g.Op == '+' && g.Amount != 0 {
		return t.strongerStop(g, i, s)
	}
	var css string
	var custom []pairValue
	if hex, ok := s.fixed(); ok {
		css = hex // one colour in both modes: written as it is (Stop.fixed)
	} else if s.paired() {
		v := pairValue{name: pairVariable(g.name, i)}
		v.light, _ = pairHex(s.Pair.Light)
		if t.palette.hasDark {
			v.dark, _ = pairHex(s.Pair.Dark)
		}
		css, custom = "var("+v.name+")", []pairValue{v}
	} else if engine, engineCustom, ok := t.engineColour(s.Color); ok {
		css, custom = engine, engineCustom // the standard set's brand end: its custom property per mode
	} else {
		v := strings.TrimSpace(s.Color)
		css, _, _, custom, _ = t.colour(Intent{Tag: stopTag, Name: "color", Value: v}, v)
	}
	if g.Op == '-' && g.Amount != 0 {
		css = "color-mix(in oklab," + css + " " + strconv.Itoa(100-g.Amount) + "%,var(--canvas))"
	}
	return css, custom
}

// strongerStop is the stop at place i of a gradient the page wrote stronger (cirrus+20): its custom
// property (strongerVariable), set to the stop's colour moved further from the canvas of each mode
// (paintStop: light, and dark when the palette has a dark look), with the stop's alpha around it as an
// alpha is written (brand/40 stays at 40%). CSS cannot move a colour away from another, so the colour
// is worked out here, per mode, as a { light, dark } pair is set.
func (t *Table) strongerStop(g Gradient, i int, s Stop) (string, []pairValue) {
	v := pairValue{name: strongerVariable(g.name, g.Amount, i)}
	light, _ := t.palette.paintStop(g, s, LightMode)
	v.light = light.Hex()
	if t.palette.hasDark {
		dark, _ := t.palette.paintStop(g, s, DarkMode)
		v.dark = dark.Hex()
	}
	css := "var(" + v.name + ")"
	if alpha := stopAlpha(s); alpha < 100 {
		css = "color-mix(in srgb," + css + " " + strconv.Itoa(alpha) + "%,transparent)"
	}
	return css, []pairValue{v}
}

// stopAlpha is the alpha a stop writes, from 0 to 100: a colour's /N, 100 for a pair and a colour
// without one.
func stopAlpha(s Stop) int {
	if s.paired() {
		return 100
	}
	if c, ok := SplitColour(strings.TrimSpace(s.Color)); ok && c.Translucent {
		return c.Alpha
	}
	return 100
}

// pairVariable is the custom property a gradient's pair at place i is written in: cirrus's first stop is
// --kitwork-gradient-cirrus-1. A gradient's name is lowercase words joined by -, so it is one as it is.
func pairVariable(name string, i int) string {
	return "--kitwork-gradient-" + name + "-" + strconv.Itoa(i+1)
}

// strongerVariable is the custom property the stop at place i of a gradient written n stronger is set
// in: cirrus+20's first stop is --kitwork-gradient-cirrus-strong-20-1. No gradient's name ends in
// -<digits> (CheckGradient), so no pair's property (pairVariable) is named like one: a gradient named
// cirrus-strong-20 is refused.
func strongerVariable(name string, n, i int) string {
	return "--kitwork-gradient-" + name + "-strong-" + strconv.Itoa(n) + "-" + strconv.Itoa(i+1)
}

// pairsOf are the custom properties a gradient's stops read, for the page's stylesheet to set
// (Rules.CSS): each pair's light colour, and its dark one when the palette has a dark look; a stronger
// colour's (brand+15) and an engine colour's (kitwork-headline, the table's own); every stop's own when
// the page wrote the gradient stronger. A
// pair of one colour has none: its hex is written in the gradient.
func (t *Table) pairsOf(g Gradient) []pairValue {
	var out []pairValue
	for i, s := range g.Stops {
		_, custom := t.stopCSS(g, i, s)
		out = append(out, custom...)
	}
	return out
}

// readBackground reads background: a gradient of the palette is a background image (its own rule, the
// image flipped for right-to-left, none in forced colours); anything else a colour (a gradient written
// as no gradient is, the colour reading refuses with what to write: gradientMisused, oldColour).
func readBackground(t *Table, in Intent, v string) (reading, *Problem) {
	g, ok := t.palette.Gradient(v)
	if !ok {
		return readColour("background-color")(t, in, v)
	}
	ltr, rtl, _ := t.gradientImages(g)
	read := reading{declarations: ltr, gradient: true, pairs: t.pairsOf(g)}
	if rtl != "" {
		read.scoped = append(read.scoped, scopedRule{selector: rightToLeft, declarations: rtl})
	}
	read.scoped = append(read.scoped, scopedRule{wrap: forcedColors, declarations: "background-image:none"})
	return read, nil
}

// readTextColour reads color: a gradient of the palette is gradient text — the colour of its first
// stop, solid, then, where the browser clips a background to the text (and reads color-mix() when a
// stop is one), the gradient behind the letters and their fill transparent (flipped for right-to-left),
// and in forced colours the system's text colour again so the text never disappears; anything else a
// colour (a gradient written as no gradient is, the colour reading refuses with what to write).
func readTextColour(t *Table, in Intent, v string) (reading, *Problem) {
	g, ok := t.palette.Gradient(v)
	if !ok {
		return readColour("color")(t, in, v)
	}
	ltr, rtl, mixed := t.gradientImages(g)
	clips := clipsToText
	if mixed {
		clips = clipsToTextMixed
	}
	read := reading{declarations: "color:" + t.solidFirstStop(g), gradient: true, pairs: t.pairsOf(g), scoped: []scopedRule{
		{wrap: clips, declarations: ltr + ";-webkit-background-clip:text;background-clip:text;-webkit-text-fill-color:transparent"},
	}}
	if rtl != "" {
		read.scoped = append(read.scoped, scopedRule{selector: rightToLeft, wrap: clips, declarations: rtl})
	}
	read.scoped = append(read.scoped, scopedRule{wrap: forcedColors, declarations: "background-image:none;-webkit-text-fill-color:currentcolor"})
	return read, nil
}

// gradientMisused refuses a gradient written where it does not go, or written as a colour: in border
// (a gradient is a background or text), with an alpha (glow/50: an alpha is a stop's, in
// router.gradients; operators of 13/10/2026), with a step that is no whole number from 0 to 100
// (glow-150, glow+), or in capitals or without its hyphens (Glow, fadetop). base is the value's word as
// the colour reading took it, its step and alpha taken off (gradientWord takes off any left). The .N
// of before and two steps are the colour reading's to refuse (oldColour, readOperators). nil for any
// other word.
func (t *Table) gradientMisused(in Intent, v, base string) *Problem {
	if colourOnly(in) {
		return nil
	}
	name := gradientWord(base)
	takes := in.Name == "background" || in.Name == "color"
	if !t.palette.isGradient(name) {
		if g := t.gradientSpelled(name); g != "" && takes {
			p := problem(in, in.Name+"=\""+v+"\": "+name+" không phải tên gradient: tên là "+g+", chữ thường, gạch nối đúng như tên; viết "+g)
			p.Suggestion = g + newStep(strings.TrimPrefix(v, name))
			return p
		}
		return nil
	}
	if !takes {
		return problem(in, in.Name+"=\""+v+"\": "+name+" là một gradient; gradient là nền (background=\""+name+"\") hoặc màu chữ (color=\""+name+"\"), "+in.Name+" nhận một màu")
	}
	say := func(suggestion, message string) *Problem {
		p := problem(in, in.Name+"=\""+v+"\": "+message)
		p.Suggestion = suggestion
		return p
	}
	left, _, translucent := strings.Cut(strings.TrimSpace(v), "/")
	if _, op, amount, stepped := SplitStep(left); stepped {
		if _, ok := whole(amount, 0, 100); !ok {
			return gradientAmountProblem(in, v, name, op)
		}
	}
	if !translucent {
		return nil // the gradient itself, which Palette.Gradient reads
	}
	return say(left, name+" là một gradient, không có độ hiện /N: viết "+in.Name+"=\""+left+"\"; gradient nhạt hơn là "+name+"-N ("+
		name+"-50: mỗi điểm dừng đi 50% về phía canvas), đậm hơn là "+name+"+N; độ hiện là của từng điểm dừng trong router.gradients (brand/40)")
}

// gradientAmountProblem refuses a gradient's step whose amount is no whole number from 0 to 100:
// glow-150, glow-30.5 (operators.go, stepAmountProblem).
func gradientAmountProblem(in Intent, v, name string, op byte) *Problem {
	if op == '+' {
		return problem(in, in.Name+"=\""+v+"\": số sau dấu + của gradient "+name+" là số nguyên từ 0 đến 100 ("+name+"+20: mỗi điểm dừng ra xa canvas thêm 20%)")
	}
	return problem(in, in.Name+"=\""+v+"\": số sau dấu - của gradient "+name+" là số nguyên từ 0 đến 100 ("+name+"-20: mỗi điểm dừng đi 20% về phía canvas)")
}

// gradientWord is the name a value of background or color starts with: its alpha, its step (-N, +N)
// and the .N of before taken off. glow-30/50 and glow.70 are glow.
func gradientWord(v string) string {
	left, _, _ := strings.Cut(strings.TrimSpace(v), "/")
	if i := strings.LastIndexByte(left, '.'); i >= 0 && isDigits(left[i+1:]) {
		return left[:i]
	}
	name, _, _, _ := SplitStep(left)
	return name
}

// newStep is what follows a gradient's name as it is written now: the .N of before is -(100-N) (.40 is
// -60, .100 nothing, .0 -100; operators of 13/10/2026); an alpha is dropped, a gradient has none; a step
// -N or +N stays as written.
func newStep(after string) string {
	step, _, _ := strings.Cut(after, "/")
	if digits, ok := strings.CutPrefix(step, "."); ok {
		n, ok := whole(digits, 0, 100)
		switch {
		case !ok:
			return "" // no step to keep: the name alone, which reads
		case n == 100:
			return ""
		}
		return "-" + strconv.Itoa(100-n)
	}
	return step
}

// gradientSpelled is the gradient a word names in other letters (Glow, fadetop, ariellessmile: glow,
// fade-top, arielles-smile), the palette's or one of the WebGradients collection; "" for none.
func (t *Table) gradientSpelled(word string) string {
	for _, g := range append(t.palette.Gradients(), WebGradients()...) {
		if letters(g) == letters(word) {
			return g
		}
	}
	return ""
}

// colourNames are the words a colour value near a typo may be meant as: the palette's tokens, and in
// background and color its gradients too, the WebGradients collection among them.
func (t *Table) colourNames(in Intent) []string {
	if !colourOnly(in) && (in.Name == "background" || in.Name == "color") {
		return append(append(t.palette.Tokens(), t.palette.Gradients()...), WebGradients()...)
	}
	return t.palette.Tokens()
}

// gradientWords is what an unknown colour's message says of gradients, in background and color: the
// gradients the page may name, and how many the WebGradients collection adds. "" elsewhere.
func (t *Table) gradientWords(in Intent) string {
	if colourOnly(in) || (in.Name != "background" && in.Name != "color") {
		return ""
	}
	return "; gradient: " + strings.Join(t.palette.Gradients(), ", ") +
		"; và " + strconv.Itoa(len(webGradientSet)) + " gradient của bộ WebGradients (warm-flame, night-fade, juicy-peach…)"
}

// renamedGradient refuses, in background and color, the name the WebGradients collection gives a
// gradient that has another name here (webGradientRenames), however its letters are written, with the
// name to write: lady-lips is pink-blush, lady-lips-40 pink-blush-40 (the .N of before, lady-lips.40,
// is oldColour's to tell: pink-blush-60). light-blue is also the colour lightblue written with a
// hyphen: its message says both, and the fix stays the colour, which a hand writing light-blue means
// far more often. A gradient of the collection the engine leaves out (webGradientsLeftOut:
// coup-de-grace) is refused with why, and no name to write. base is the value's word as the colour
// reading took it; its step and alpha, if it still has them, are taken off here (gradientWord). nil for
// any other word, and in any other attribute.
func (t *Table) renamedGradient(in Intent, v, base string) *Problem {
	if colourOnly(in) || (in.Name != "background" && in.Name != "color") {
		return nil
	}
	word := gradientWord(base)
	if name, ok := webGradientsLeftOutLetters[letters(word)]; ok {
		return problem(in, in.Name+"=\""+v+"\": "+word+" là gradient của bộ WebGradients mà Kitwork không có: bộ vẽ nó bằng màu "+
			webGradientsLeftOut[name]+" dưới các lớp trong mờ trộn bằng background-blend-mode, còn gradient của Kitwork là một lớp màu; cần thì khai một gradient riêng tên "+name+" trong router.gradients")
	}
	renamed, original, ok := renamedWebGradient(word)
	if !ok {
		return nil
	}
	if colour, why, ok := namedOtherwise(word); ok {
		p := problem(in, in.Name+"=\""+v+"\": "+why+"; còn gradient tên gốc "+original+" trong bộ WebGradients thì trong Kitwork tên là "+renamed+": viết "+renamed)
		p.Suggestion = colour + strings.TrimPrefix(v, word)
		return p
	}
	step := newStep(strings.TrimPrefix(strings.TrimSpace(v), word)) // a gradient has no alpha: its step goes with it
	p := problem(in, in.Name+"=\""+v+"\": "+word+" là tên gốc trong bộ WebGradients, trong Kitwork gradient ấy tên là "+renamed+": viết "+renamed+step)
	p.Suggestion = renamed + step
	return p
}
