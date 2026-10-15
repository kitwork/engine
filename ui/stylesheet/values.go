package stylesheet

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The scales and words a value of a presentation attribute is read against. They are .ui's own:
// the spacing scale keeps the shape of Tailwind v3's because it is handy, not to follow it, and the
// words are the ones the spec names (KITWORK_UI §7, §8).

// spacingSteps is the scale of gap and padding, and the numbers width, height and their min-/max-
// forms take: step N is N × 0.25rem. A class carries the step as written (gap-4), never pixels.
var spacingSteps = []string{
	"0", "0.5", "1", "1.5", "2", "2.5", "3", "3.5", "4", "5", "6", "7", "8", "9", "10", "11", "12",
	"14", "16", "20", "24", "28", "32", "36", "40", "44", "48", "52", "56", "60", "64", "72", "80", "96",
}

var spacingIndex = func() map[string]bool {
	m := make(map[string]bool, len(spacingSteps))
	for _, s := range spacingSteps {
		m[s] = true
	}
	return m
}()

// spacingWords names the scale for an error message.
const spacingWords = "0, 0.5, 1, 1.5, 2, 2.5, 3, 3.5, 4, 5, 6, 7, 8, 9, 10, 11, 12, 14, 16, 20, 24, 28, 32, 36, 40, 44, 48, 52, 56, 60, 64, 72, 80, 96 (bội của 0.25rem: 4 là 1rem)"

// remOf is a spacing step in rem: 4 → 1rem, 0.5 → .125rem, 0 → 0.
func remOf(step string) string {
	f, _ := strconv.ParseFloat(step, 64)
	if f == 0 {
		return "0"
	}
	return number(f*0.25) + "rem"
}

// number writes a CSS number the short way: 0.5 → .5, 2 → 2.
func number(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if strings.HasPrefix(s, "0.") {
		return s[1:]
	}
	return s
}

// nearestStep is the spacing step closest to a length in rem; on a tie the smaller step.
func nearestStep(rem float64) string {
	best, bestDistance := spacingSteps[0], math.Inf(1)
	for _, s := range spacingSteps {
		f, _ := strconv.ParseFloat(s, 64)
		if d := math.Abs(f*0.25 - rem); d < bestDistance-1e-9 {
			best, bestDistance = s, d
		}
	}
	return best
}

// widthWords are the width prefixes and the width scale, narrowest first: the pixels where each
// prefix starts (min-width) and how wide container="…" and max-width="…" let an element be.
var widthWords = []string{"small", "medium", "large", "wide", "huge"}

var widthPixels = map[string]string{
	"small": "640px", "medium": "768px", "large": "1024px", "wide": "1280px", "huge": "1536px",
}

// widthScaleWords names the width scale for an error message.
const widthScaleWords = "small (640px), medium (768px), large (1024px), wide (1280px) hoặc huge (1536px)"

// tailwindWidth are Tailwind's max-w-* words (2xl, 7xl, sm, prose, screen-lg…): not .ui words.
var tailwindWidth = map[string]bool{
	"xs": true, "sm": true, "md": true, "lg": true, "xl": true, "2xl": true, "3xl": true, "4xl": true,
	"5xl": true, "6xl": true, "7xl": true, "prose": true, "full": true, "none": true, "screen": true,
	"fit": true, "min": true, "max": true, "auto": true, "px": true,
}

// stateWords are the state prefixes in the fixed order of the stylesheet: a later state wins over an
// earlier one while both hold (a hovered button that is disabled looks disabled; a header the page
// has scrolled under still lights up under the pointer).
var stateWords = []string{"open", "active", "scrolled", "hover", "focus", "pressed", "disabled"}

// stateSelector is how each state reaches CSS. It sits inside :where(), so every page rule weighs
// exactly one class and only the order decides. pressed is CSS :active; Kitwork's active: is the link
// of the page being viewed (aria-current); open follows the nearest element that opens, so the chevron
// of a closed accordion nested in an open one stays closed; scrolled is the page scrolled away from
// its top, one flag on <html> (data-kitwork-scrolled) that scrolled.<hash>.js keeps (ScrolledScript).
var stateSelector = map[string]string{
	"open":     ":where(:is(details[open],[aria-expanded=true]) *):not(:where(details:not([open]),[aria-expanded=false]) *)",
	"active":   ":where([aria-current]:not([aria-current=false]))",
	"scrolled": ":where(:root[data-kitwork-scrolled] *)",
	"hover":    ":where(:hover)",
	"focus":    ":where(:focus-visible)",
	"pressed":  ":where(:active)",
	"disabled": ":where(:disabled,[aria-disabled=true])",
}

// stateTransition is how long a change a state brings takes to animate (a press's scale, a scrolled
// header's shadow): short enough to read as an answer, never as a wait. reboot.css's reduced-motion
// floor makes it instant for a reader who asked for less motion.
const stateTransition = ".15s"

// textSizes is the five-step text scale. Text bigger than huge is a heading, sized by its level.
var textSizes = map[string]string{
	"mini": ".75rem", "small": ".875rem", "medium": "1rem", "large": "1.125rem", "huge": "1.5rem",
}

var textSizeWords = []string{"mini", "small", "medium", "large", "huge"}

// headingStep is one step of the heading scale: its sizes from the narrowest screen, the width each
// starts at ("" for every screen), and how tight it is tracked.
type headingStep struct {
	sizes, widths []string
	tracking      string
}

// headingSteps is the heading scale (KITWORK_UI §7): the five words of the body scale, sized for a
// heading. A level has its default step in reboot.css (h1 medium, h2 small, h3 mini; h4–h6 the body
// size, set apart by weight); size on a heading picks another, so an h1 can be larger or smaller than
// its default and stay an h1. Big type is tracked tight; the line height stays the heading's 1.25
// (reboot.css), so Vietnamese stacked marks never touch.
var headingSteps = map[string]headingStep{
	"mini":   {sizes: []string{"1.125rem"}, widths: []string{""}, tracking: "-.02em"},
	"small":  {sizes: []string{"1.5rem"}, widths: []string{""}, tracking: "-.03em"},
	"medium": {sizes: []string{"1.875rem", "2.25rem"}, widths: []string{"", "small"}, tracking: "-.04em"},
	"large":  {sizes: []string{"2.25rem", "3rem"}, widths: []string{"", "small"}, tracking: "-.045em"},
	"huge":   {sizes: []string{"3rem", "3.75rem", "4.5rem"}, widths: []string{"", "small", "large"}, tracking: "-.05em"},
}

// headingSelector scopes a rule to the headings, weightless: the class alone weighs.
const headingSelector = ":where(h1,h2,h3,h4,h5,h6)"

// textSizeSpelledOut names the step an abbreviation or a Tailwind size meant.
var textSizeSpelledOut = map[string]string{
	"xs": "mini", "tiny": "mini", "sm": "small", "base": "medium", "md": "medium", "lg": "large",
	"xl": "huge", "2xl": "huge",
}

// weights are the font weights by name, lightest first (the names of Compose's FontWeight).
var weightWords = []string{"thin", "extralight", "light", "normal", "medium", "semibold", "bold", "extrabold", "black"}

var weights = map[string]int{
	"thin": 100, "extralight": 200, "light": 300, "normal": 400, "medium": 500,
	"semibold": 600, "bold": 700, "extrabold": 800, "black": 900,
}

// weightSpelledOut names the word an abbreviation, another system's name or a number meant.
var weightSpelledOut = map[string]string{
	"regular": "normal", "book": "normal", "heavy": "extrabold", "ultralight": "extralight",
	"hairline": "thin", "demibold": "semibold", "100": "thin", "200": "extralight", "300": "light",
	"400": "normal", "500": "medium", "600": "semibold", "700": "bold", "800": "extrabold", "900": "black",
}

// radii are the corner radii. A neutral button and an input sit on small.
var radiusWords = []string{"none", "small", "medium", "large", "full"}

var radii = map[string]string{
	"none": "0", "small": ".375rem", "medium": ".5rem", "large": ".75rem", "full": "999px",
}

// radiusReplaced names the word that replaces each of Tailwind's: small is its md (.375rem), medium
// its lg (.5rem), large its xl (.75rem); its bigger and smaller ones point at the nearest.
var radiusReplaced = map[string]string{
	"sm": "small", "md": "small", "lg": "medium", "xl": "large", "2xl": "large", "3xl": "large",
	"rounded": "small", "default": "small",
}

var shadowWords = []string{"small", "medium", "large"}

var shadows = map[string]string{
	"small":  "0 1px 2px 0 rgb(0 0 0/5%)",
	"medium": "0 4px 6px -1px rgb(0 0 0/10%),0 2px 4px -2px rgb(0 0 0/10%)",
	"large":  "0 10px 15px -3px rgb(0 0 0/10%),0 4px 6px -4px rgb(0 0 0/10%)",
}

var shadowReplaced = map[string]string{
	"sm": "small", "md": "medium", "lg": "large", "xl": "large", "2xl": "large",
}

// positionWords are the nine positions as (vertical, horizontal), the SwiftUI/Compose way: start is
// the middle of the leading edge, top the middle of the top edge. align reads them for the children of
// a container; place inside the parent reads the same words for the element itself.
var positionWords = []string{"top-start", "top", "top-end", "start", "center", "end", "bottom-start", "bottom", "bottom-end"}

var positions = map[string][2]string{
	"top-start": {"top", "start"}, "top": {"top", "center"}, "top-end": {"top", "end"},
	"start": {"center", "start"}, "center": {"center", "center"}, "end": {"center", "end"},
	"bottom-start": {"bottom", "start"}, "bottom": {"bottom", "center"}, "bottom-end": {"bottom", "end"},
}

// flexValue is a screen direction as a flex alignment value: top and start are the beginning of
// their axis, which follows the writing direction, so start is right in Arabic.
var flexValue = map[string]string{
	"top": "flex-start", "start": "flex-start", "center": "center", "bottom": "flex-end", "end": "flex-end",
}

// placeWords are place's values: inside the parent (the nine positions and fill) and outside it,
// floating over what follows (below, above). Every place sets all insets, margins and translate, so
// a written place replaces popover's default whole. Centred places use physical left:50% with
// translate, where direction does not matter; the start/end ones are logical, so right-to-left works.
var placeWords = []string{
	"top-start", "top", "top-end", "start", "center", "end", "bottom-start", "bottom", "bottom-end", "fill",
	"below-start", "below", "below-end", "above-start", "above", "above-end",
}

var places = map[string]string{
	"fill":         "position:absolute;inset:0;margin:0;translate:none",
	"top-start":    "position:absolute;inset-block:0 auto;inset-inline:0 auto;margin:0;translate:none",
	"top":          "position:absolute;inset:0 auto auto 50%;margin:0;translate:-50% 0",
	"top-end":      "position:absolute;inset-block:0 auto;inset-inline:auto 0;margin:0;translate:none",
	"start":        "position:absolute;inset-block:50% auto;inset-inline:0 auto;margin:0;translate:0 -50%",
	"center":       "position:absolute;inset:50% auto auto 50%;margin:0;translate:-50% -50%",
	"end":          "position:absolute;inset-block:50% auto;inset-inline:auto 0;margin:0;translate:0 -50%",
	"bottom-start": "position:absolute;inset-block:auto 0;inset-inline:0 auto;margin:0;translate:none",
	"bottom":       "position:absolute;inset:auto auto 0 50%;margin:0;translate:-50% 0",
	"bottom-end":   "position:absolute;inset-block:auto 0;inset-inline:auto 0;margin:0;translate:none",
	"below-start":  "position:absolute;inset-block:100% auto;inset-inline:0 auto;margin:.5rem 0 0;translate:none;z-index:50",
	"below":        "position:absolute;inset:100% auto auto 50%;margin:.5rem 0 0;translate:-50% 0;z-index:50",
	"below-end":    "position:absolute;inset-block:100% auto;inset-inline:auto 0;margin:.5rem 0 0;translate:none;z-index:50",
	"above-start":  "position:absolute;inset-block:auto 100%;inset-inline:0 auto;margin:0 0 .5rem;translate:none;z-index:50",
	"above":        "position:absolute;inset:auto auto 100% 50%;margin:0 0 .5rem;translate:-50% 0;z-index:50",
	"above-end":    "position:absolute;inset-block:auto 100%;inset-inline:auto 0;margin:0 0 .5rem;translate:none;z-index:50",
}

// unitSize is a length written with its unit: a number and one unit of a closed set. No calc(), no
// var(): a size the scale cannot say is written as it is, nothing more.
var unitSize = regexp.MustCompile(`^(\d+(?:\.\d+)?)(px|rem|em|%|ch|vw|vh|dvh)$`)

const unitWords = "px, rem, em, %, ch, vw, vh, dvh"

// ratio is aspect-ratio's width/height, whole numbers.
var ratio = regexp.MustCompile(`^([1-9]\d{0,3})/([1-9]\d{0,3})$`)

// ratioDomain are the ratios a value from data may take.
var ratioDomain = []string{"square", "auto", "16/9", "4/3", "3/2", "21/9", "9/16", "3/4", "2/3"}

// whole reads a whole number written plainly (no sign, no leading zero) within [low, high].
func whole(value string, low, high int) (int, bool) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < low || n > high {
		return 0, false
	}
	return n, true
}

// integer reads a whole number written plainly with an optional minus sign: no plus, no leading zero,
// no "-0". rotate and scale read it, where a value below their range still means something.
func integer(value string) (int, bool) {
	digits := strings.TrimPrefix(value, "-")
	n, ok := whole(digits, 0, math.MaxInt32)
	if !ok || (digits != value && n == 0) {
		return 0, false
	}
	if digits != value {
		return -n, true
	}
	return n, true
}

// wholeRange lists low…high as written.
func wholeRange(low, high int) []string {
	out := make([]string, 0, high-low+1)
	for n := low; n <= high; n++ {
		out = append(out, strconv.Itoa(n))
	}
	return out
}
