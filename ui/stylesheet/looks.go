package stylesheet

import (
	"strconv"
	"strings"
)

// The looks reboot.css gives Kitwork's elements before the author writes anything, as far as contrast
// reads them: the colour of their text, the surface they paint behind it, the size and weight of their
// type. One table, written from reboot.css and checked against it (looks_test.go reads reboot.css and
// fails on a colour, background, font size or weight this table does not know, and on one it knows
// that reboot.css no longer sets), so the compiler's contrast walk never guesses what a card or a hint
// looks like.

// elementLook is one rule of reboot.css that sets a colour, a background, a font size or a weight.
type elementLook struct {
	selector string // the rule's selector, as reboot.css writes it compacted
	width    string // the width word its @media (min-width) starts at; "" for every screen
	// state is the state word its selector holds in (focus: a field with focus); "" for a rule that
	// holds at rest. LookOf reads the rules at rest, LookIn those and the rules of one state.
	state string
	// what it matches: HTML elements (an input of a type in skip is not one), or the classes the engine
	// writes for a .ui tag (card, badge, hint…)
	elements []string
	skip     []string
	classes  []string
	// text is the colour value its text takes ("inherit": the parent's, as without it); surface the
	// colour value painted behind it ("none": nothing, kitworkTint: the badge's fill, decided by its
	// colour, Table.BadgeTint); size the font size in px; weight the font weight
	text, surface string
	size          float64
	weight        int
	// unread, when set, says why contrast does not read the rule: what it paints is not text on a
	// background the compiler measures (a placeholder, a switch's track, the scrim behind a dialog…)
	unread string
}

// kitworkTint is the surface of a badge: background-color:var(--kitwork-tint), which the badge's colour
// class sets (Table.BadgeTint); without one, nothing.
const kitworkTint = "var(--kitwork-tint)"

// The browser's own type where reboot.css sets none: 16px, normal weight.
const (
	rootSize   = 16.0
	rootWeight = 400
)

var elementLooks = []elementLook{
	{selector: ":where(html)", elements: []string{"html"}, text: "ink", surface: "canvas"},
	{selector: ":where(button,input,select,textarea)", elements: []string{"button", "input", "select", "textarea"}, text: "inherit"},
	{selector: ":where(button)", elements: []string{"button"}, surface: "none"},
	{selector: ":where(button:disabled,input:disabled,select:disabled,textarea:disabled,[aria-disabled=\"true\"])",
		unread: "a disabled control's words, dimmed: WCAG asks no contrast of it, and the check never measures one"},
	{selector: "::placeholder", unread: "placeholder text is out of scope (decision C)"},
	{selector: ":where(a)", elements: []string{"a"}, text: "inherit"},
	// font-size:1em: the size around it, as without the rule (size 0)
	{selector: ":where(code,kbd,samp,pre)", elements: []string{"code", "kbd", "samp", "pre"}},
	{selector: ":where(dialog,[popover])", elements: []string{"dialog"}, text: "inherit", surface: "none",
		unread: "Kitwork's popover is a div with the class popover, not the popover attribute; dialog is not built"},
	{selector: ":where(dialog)::backdrop", unread: "the scrim behind a dialog, which is not built"},
	{selector: ":where([class^=\"icon-\"],[class*=\" icon-\"],[class^=\"logo-\"],[class*=\" logo-\"])", unread: "forced colours: the system paints the icon"},
	{selector: ":where(a) :where([class^=\"icon-\"],[class*=\" icon-\"])", unread: "forced colours: the system paints the icon"},
	{selector: ":where(button) :where([class^=\"icon-\"],[class*=\" icon-\"])", unread: "forced colours: the system paints the icon"},
	{selector: ":where(button:disabled) :where([class^=\"icon-\"],[class*=\" icon-\"])", unread: "forced colours: the system paints the icon"},
	{selector: ":where(h1,h2,h3,h4,h5,h6)", elements: []string{"h1", "h2", "h3", "h4", "h5", "h6"}, size: 16, weight: 600},
	{selector: ":where(h1)", elements: []string{"h1"}, size: 30},
	{selector: ":where(h2)", elements: []string{"h2"}, size: 24},
	{selector: ":where(h3)", elements: []string{"h3"}, size: 18},
	{selector: ":where(h1)", width: "small", elements: []string{"h1"}, size: 36},
	// a field: a translucent fill of ink at rest, deeper with focus (no border: reboot.css); the contrast
	// walk composites it over what lies behind the field
	{selector: ":where(input:not([type=\"checkbox\"],[type=\"radio\"]),select,textarea)",
		elements: []string{"input", "select", "textarea"}, skip: []string{"checkbox", "radio", "switch"}, surface: "ink/5"},
	{selector: ":where(input:not([type=\"checkbox\"],[type=\"radio\"]):focus,select:focus,textarea:focus)", state: "focus",
		elements: []string{"input", "select", "textarea"}, skip: []string{"checkbox", "radio", "switch"}, surface: "ink/8"},
	{selector: ":where(.hint)", classes: []string{"hint"}, size: 14, text: "ink-30"},
	{selector: ":where(.error)", classes: []string{"error"}, size: 14, text: ErrorColour}, // danger+25 in light, per mode (engine_colour.go)
	{selector: ":where(input[role=\"switch\"])", unread: "a switch's track and knob: no text"},
	{selector: ":where(input[role=\"switch\"]:checked)", unread: "a switch's track and knob: no text"},
	{selector: ":where(.card)", classes: []string{"card"}, surface: "paper"},
	{selector: ":where(.badge)", classes: []string{"badge"}, size: 12, weight: 500, surface: kitworkTint},
	{selector: ":where(.popover)", classes: []string{"popover"}, surface: "paper"},
	// a quote stands on a faint translucent fill of ink (no rule on its edge)
	{selector: ":where(blockquote)", elements: []string{"blockquote"}, surface: "ink/4"},
	// a typing terminal (ui/terminal.go; the author draws the rest)
	{selector: "::highlight(kitwork-terminal-untyped)", unread: "what a terminal has not typed yet: transparent, not shown"},
	{selector: "::highlight(kitwork-terminal-caret)", unread: "the typing caret, a block over a character not typed yet: no text shown"},
}

// Look is how an element looks before the author writes anything, at one width, as far as contrast
// reads it. Text is the colour value of its text ("" or "inherit": the parent's); Surface the colour
// value painted behind it ("" or "none": nothing; Tinted: the badge's fill; one with an alpha, a field's
// ink/5, lets what is behind it show through); Size the font size in px
// (0: the parent's); Weight the font weight (0: the parent's).
type Look struct {
	Text, Surface string
	Tinted        bool
	Size          float64
	Weight        int
}

// LookOf is the look reboot.css gives an element at rest: its HTML element, the classes the engine
// writes for its .ui tag (card, badge, hint…), the type of an input ("" for any other element), at a
// width (0 every screen, 1 small … 5 huge). Later rules of the table win, as later rules of reboot.css do.
func LookOf(element string, classes []string, inputType string, width int) Look {
	return LookIn("", element, classes, inputType, width)
}

// LookIn is the look reboot.css gives an element in a state (focus): the rules at rest and the rules of
// that state, later ones winning as in reboot.css (every selector there weighs nothing). "" is LookOf.
func LookIn(state, element string, classes []string, inputType string, width int) Look {
	var out Look
	for _, l := range elementLooks {
		if l.unread != "" || l.state != "" && l.state != state || !l.matches(element, classes, inputType) {
			continue
		}
		if w := indexOf(widthWords, l.width) + 1; w > width {
			continue
		}
		if l.text != "" {
			out.Text = l.text
		}
		switch l.surface {
		case "":
		case kitworkTint:
			out.Surface, out.Tinted = "", true
		default:
			out.Surface, out.Tinted = l.surface, false
		}
		if l.size != 0 {
			out.Size = l.size
		}
		if l.weight != 0 {
			out.Weight = l.weight
		}
	}
	if out.Text == "inherit" {
		out.Text = ""
	}
	if out.Surface == "none" {
		out.Surface = ""
	}
	return out
}

func (l elementLook) matches(element string, classes []string, inputType string) bool {
	for _, e := range l.elements {
		if e == element {
			return indexOf(l.skip, inputType) < 0
		}
	}
	for _, c := range l.classes {
		if indexOf(classes, c) >= 0 {
			return true
		}
	}
	return false
}

// RootLook is the page's ground (html): its text colour, its surface and the browser's type.
func RootLook() Look {
	l := LookOf("html", nil, "", 0)
	l.Size, l.Weight = rootSize, rootWeight
	return l
}

// FontSize is the font size in px a size value sets on an element at a width (0 every screen, 1
// small … 5 huge), its class starting at from (0 unprefixed). On a heading it is a step of the heading
// scale (readTextSize): unprefixed it grows with the screen, prefixed it is the step's largest size from
// that width on. ok is false for a word that is not a size.
func FontSize(heading bool, value string, from, width int) (float64, bool) {
	rem, ok := textSizes[strings.TrimSpace(value)]
	if !ok {
		return 0, false
	}
	if heading {
		step := headingSteps[strings.TrimSpace(value)]
		if from > 0 {
			return pixels(step.sizes[len(step.sizes)-1]), true
		}
		size := step.sizes[0]
		for i, w := range step.widths {
			if indexOf(widthWords, w)+1 <= width {
				size = step.sizes[i]
			}
		}
		return pixels(size), true
	}
	return pixels(rem), true
}

// FontWeight is the weight a weight value sets.
func FontWeight(value string) (int, bool) {
	w, ok := weights[strings.TrimSpace(value)]
	return w, ok
}

// pixels reads a length of the scale in px: "1.5rem" → 24, ".75rem" → 12.
func pixels(length string) float64 {
	if rem, ok := strings.CutSuffix(length, "rem"); ok {
		f, _ := strconv.ParseFloat(rem, 64)
		return f * rootSize
	}
	f, _ := strconv.ParseFloat(strings.TrimSuffix(length, "px"), 64)
	return f
}

// WidthIndex is a width word's place, 1 small … 5 huge; 0 for "" (every screen) and for a word that is
// not a width.
func WidthIndex(word string) int { return indexOf(widthWords, word) + 1 }

// StateIndex is a state word's place in the fixed order, 1 open … 7 disabled; 0 for none.
func StateIndex(word string) int { return indexOf(stateWords, word) + 1 }

// Widths are the width words, narrowest first.
func Widths() []string { return append([]string(nil), widthWords...) }

// BadgeTint is the pill a badge's colour makes (readColour): a plain accent that grows a family (.on) is
// its fill made lighter, X-85 (TintStep), under its text, the engine colour kitwork-badge-X
// (BadgeColour): X made stronger as far as it must to read 4.5:1 on that fill in each mode, from 15
// (StrongStep) where stronger darkens and from 0 where it lightens, so a pale accent (warning) takes more
// than a deep one and dark keeps the accent's hue (engine_colour.go). text is that name: Paint and
// Palette.Engine read it per mode (family true). Anything else (ink, a surface, an operator, an alpha, a
// fixed colour) is the quiet panel under the colour as written.
func (t *Table) BadgeTint(value string) (fill, text string, family bool) {
	c, ok := SplitColour(value)
	if ok && c.Plain() && accent(c.Base) && !c.Fixed() && t.palette.Has(c.Base+".on") {
		return c.Base + "-" + strconv.Itoa(TintStep), BadgeColour(c.Base), true
	}
	return "panel", strings.TrimSpace(value), false
}

// Materials are material's values, none first, then the glasses thinnest first: what a material from
// data may be.
func Materials() []string { return append([]string(nil), materialWords...) }
