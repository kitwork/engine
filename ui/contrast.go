package ui

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/kitwork/engine/ui/stylesheet"
)

// The contrast check (decision C, 12/10/2026): every piece of text a page can show, and every icon
// with an alt, is measured at compile time against what lies behind it, by WCAG 2.2 AA (4.5:1 for
// text, 3:1 for large text and for an icon that says something), in light and, when the palette has
// one, in dark, at every width and state the page writes. A pair that falls short is told at the
// attribute that decided it, with a colour that passes everywhere the pair occurs: a warning of the
// page by default (decision D: it renders as written, the author hears of it while developing), a
// compile error when the route's router.contrast is "strict" (Site.Contrast). By default the pairs that
// fall short because of one background — texts at many places on a page written background="white",
// which stays white in the dark — are one warning at that background (tellContrast); "strict" keeps
// each pair its own error, with its own suggestion. A fixed colour (white, black) under text that
// follows the mode is told the surface that follows it too, or the fixed colour for the text, never a
// token to declare: white and black are not tokens.
//
// The compiler knows each pair because it builds the page: an element's colour is the nearest colour
// written on it or around it, else the look reboot.css gives its kind (stylesheet.LookOf), else ink; its
// ground is the layers from it outward — backgrounds, glass, the surfaces of cards and fields — down to
// the first one nothing shows through, else the page's canvas. Layouts and components are measured
// where they are called. The tokens of a code element (<code language="…">) are spans in the engine's
// colour for their kind, measured on the code's ground and told at its language= with the engine's
// advice, each code its own (code.go); a kind the site's router.highlight colours is the author's colour,
// told at the call in router.work on the page's canvas, paper and panel (tellHighlight) and at
// language= on any other ground; a terminal's prompt is the text around its command at 70%,
// measured where it stands (terminal.go). The arithmetic (luminance, the ratio, a colour lighter or
// stronger as the browser paints it, an alpha composited) is the stylesheet's (stylesheet/contrast.go).
//
// What is not measured: an icon without alt (decoration), text inside a control written
// disabled="true" and every disabled: colour (WCAG's inactive components), placeholder text, an option
// in the list a select opens (the system draws it), text over what the page cannot know — the
// content under an element placed over its parent (place="fill" over a photo) when nothing solid
// lies between, a background from data ({{ }} that spells out no value).

// paint is one element as the contrast check reads it: what it writes that decides colour, ground and
// type, and what it is. Elements chain to the element around them, a layout's or a component's root to
// the element its call stands in.
type paint struct {
	parent    *paint
	node      *Node
	tag       string   // the .ui tag
	html      string   // the HTML element it becomes: what reboot.css keys a look by
	classes   []string // the engine's word for its tag where the element is shared (card, badge, hint…)
	inputType string
	heading   bool // a text that is a heading: size reads the heading scale
	page      bool
	pins      []pinAt // the pins it writes: pinned to an edge from the width one starts at
	places    []int   // the widths a place starts at, written on it or at its call (0 every screen)
	hidden    []bool  // the widths a show written "false" hides it at (nil: none)
	opener    bool    // what open: follows: an accordion, a button written expanded
	disabled  bool    // a control written disabled="true"
	written   []*paintIntent
	measured  bool // its text was measured once already
	// token is a kind of token of a code element, or several that look alike (code.go): a span whose
	// colour, fill and weight are the engine's, read from the palette's roles, or whose colour the site's
	// router.highlight sets (look.Source); nil for every other element
	token *codeToken
}

// pinAt is a pin from the width it starts at (0 every screen): on for top, bottom or a value from
// data, off for none.
type pinAt struct {
	width int
	on    bool
	attr  *Attr // the pin attribute written, for a message (lazyOnScreen)
}

// paintIntent is one attribute that decides colour, ground or type (color, background, material,
// size, weight), with its condition, and the values it may take: one written, each a {{ }} spells out,
// nil for a value from data. A pinned frame's background the engine gives it is one with no attr.
type paintIntent struct {
	name   string
	attr   *Attr
	node   *Node
	width  int      // 0 every screen, 1 small … 5 huge
	states []string // the state words of its prefix
	values []string
	// partial says a {{ }} may also yield a value from data besides the values it spells out: those
	// are measured, the unknown one is not, and a size or weight counts as the smallest
	partial bool
	// test is the test of a value that is one ternary ({{ selected ? 'brand' : 'paper' }}) and arms
	// the arm each value comes from: two values of one element that test the same show their arms
	// together, never a value of one arm with a value of the other
	test string
	arms map[string]int
}

// choice is one decision a value shown stands for: which value an attribute takes (intent, the place
// in its values), or which arm the test of an element's ternaries takes (node, test). Two shades that
// decided one of them differently are never on screen together.
type choice struct {
	intent *paintIntent
	node   *Node
	test   string
	arm    int
}

// choices are the decisions a value of an attribute stands for, by its place in the values.
func (pi *paintIntent) choices(i int) []choice {
	if pi == nil || i >= len(pi.values) {
		return nil
	}
	var out []choice
	if len(pi.values) > 1 {
		out = append(out, choice{intent: pi, arm: i})
	}
	if arm, ok := pi.arms[pi.values[i]]; ok && pi.test != "" {
		out = append(out, choice{node: pi.node, test: pi.test, arm: arm})
	}
	return out
}

// agree reports whether two sets of decisions can hold on one screen.
func agree(a, b []choice) bool {
	for _, x := range a {
		for _, y := range b {
			if x.intent == y.intent && x.node == y.node && x.test == y.test && x.arm != y.arm {
				return false
			}
		}
	}
	return true
}

func choicesKey(list []choice) string {
	var b strings.Builder
	for _, x := range list {
		fmt.Fprintf(&b, "|%p %p %s %d", x.intent, x.node, x.test, x.arm)
	}
	return b.String()
}

// interpolationLiterals is interpolationCandidates (skeleton.go) for the contrast check: the texts an
// interpolation renders from the literals its expressions spell out, and whether they are every text
// it can render. An arm that reads data leaves complete false and the arms spelled out still listed;
// nil when a part spells out nothing.
func interpolationLiterals(in *interpolation) ([]string, bool) {
	if in == nil {
		return nil, false
	}
	values, complete := []string{""}, true
	for _, p := range in.parts {
		if p.e == nil {
			for i := range values {
				values[i] += p.text
			}
			continue
		}
		literals, all := knownLiterals(p.e.ir)
		complete = complete && all
		var next []string
		for _, v := range values {
			for _, l := range literals {
				next = append(next, v+l)
			}
		}
		if len(next) == 0 || len(next) > maxCandidates {
			return nil, false
		}
		values = next
	}
	sort.Strings(values)
	return dedupe(values), complete
}

// knownLiterals is resultLiterals (skeleton.go) keeping the arms spelled out when another reads data:
// the literals, and whether they are every value the expression yields.
func knownLiterals(ir any) ([]string, bool) {
	node, ok := ir.([]any)
	if !ok || len(node) == 0 {
		return nil, false
	}
	op, _ := node[0].(string)
	switch {
	case op == "#" && len(node) >= 2:
		return []string{stringify(node[1])}, true
	case op == "?" && len(node) >= 4:
		return knownUnion(node[2], node[3])
	case (op == "||" || op == "??") && len(node) >= 3:
		return knownUnion(node[1], node[2])
	case op == "&&" && len(node) >= 3:
		return knownLiterals(node[2]) // a falsy left side renders no class: the class comes from the right
	}
	return nil, false
}

func knownUnion(a, b any) ([]string, bool) {
	left, l := knownLiterals(a)
	right, r := knownLiterals(b)
	return append(left, right...), l && r
}

// ternaryArms reads a value that is one ternary, {{ test ? a : b }}: its test, and the arm each value
// it spells out comes from (0 the first, 1 the second); "" and nil for any other value. A value both
// arms spell out is in neither.
func ternaryArms(in *interpolation) (string, map[string]int) {
	if in == nil || in.single() == nil {
		return "", nil
	}
	node, ok := in.single().ir.([]any)
	if !ok || len(node) < 4 || node[0] != "?" {
		return "", nil
	}
	arms, both := map[string]int{}, map[string]bool{}
	for arm, side := range node[2:4] {
		literals, _ := knownLiterals(side)
		for _, l := range literals {
			l = strings.TrimSpace(l)
			if seen, ok := arms[l]; ok && seen != arm {
				both[l] = true
			}
			arms[l] = arm
		}
	}
	for l := range both {
		delete(arms, l)
	}
	return fmt.Sprint(node[1]), arms
}

// paintNames are the attributes the check reads.
var paintNames = map[string]bool{"color": true, "background": true, "material": true, "size": true, "weight": true}

// paintIntentOf is a written attribute as the check reads it.
func paintIntentOf(n *Node, a *Attr, pre prefix, name string, values []string) *paintIntent {
	pi := &paintIntent{name: name, attr: a, node: n, width: narrowest(pre.widths), states: pre.states}
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			pi.values = append(pi.values, v)
		}
	}
	if values == nil {
		pi.values = nil
	}
	return pi
}

// narrowest is the width a list of width words holds from (widths are min-width).
func narrowest(words []string) int {
	at := 0
	for _, w := range words {
		if i := stylesheet.WidthIndex(w); i > 0 && (at == 0 || i < at) {
			at = i
		}
	}
	return at
}

// controlTags are the controls disabled="true" makes inactive.
var controlTags = map[string]bool{"button": true, "input": true, "select": true, "textarea": true, "option": true}

// noTextInput are the input types that show no text of their own. submit, reset and button show their
// value (or the browser's word) as a label: measured.
var noTextInput = map[string]bool{
	"checkbox": true, "radio": true, "switch": true, "range": true, "color": true, "file": true,
	"hidden": true, "image": true,
}

// paintOf is the element being compiled as the check reads it.
func (c *compiler) paintOf(n *Node, el *element, p *attrPlan, cx ctx, site *callSite) *paint {
	f := &paint{parent: cx.paint, node: n, tag: n.Tag, html: el.html, heading: el.intentTag() == "heading",
		page: n.Tag == "page", written: p.paints, places: p.places, pins: p.pins, hidden: p.hidden}
	if _, expanded := n.Attr("expanded"); n.Tag == "accordion" || n.Tag == "button" && expanded {
		f.opener = true
	}
	if sharedTag[n.Tag] {
		f.classes = []string{n.Tag}
	}
	if n.Tag == "input" {
		v, _ := n.Attr("type")
		f.inputType = strings.TrimSpace(v)
	}
	if site != nil {
		f.places = append(f.places, site.places...)
	}
	if v, ok := n.Attr("disabled"); ok && controlTags[n.Tag] && strings.TrimSpace(v) == "true" {
		f.disabled = true
	}
	return f
}

func (f *paint) root() *paint {
	r := f
	for r.parent != nil {
		r = r.parent
	}
	return r
}

func (f *paint) look(width int) stylesheet.Look {
	if f.token != nil {
		// the span's own rule (stylesheet.Table.Code): its colour wins over any colour around the code
		return stylesheet.Look{Text: f.token.look.Color, Surface: f.token.look.Background, Weight: f.token.look.Weight}
	}
	return stylesheet.LookOf(f.html, f.classes, f.inputType, width)
}

// placedAt reports whether the element floats over what lies around it at a width: written with place
// (inside its parent, over the parent's content, or outside it, over what follows), or a popover.
func (f *paint) placedAt(width int) bool {
	if f.tag == "popover" {
		return true
	}
	for _, w := range f.places {
		if w <= width {
			return true
		}
	}
	return false
}

// pinnedAt reports whether the element is pinned to an edge at a width: the pin written for the widest
// prefix not above it pins (medium:pin="top" is in the flow below medium; a wider pin="none" unpins).
func (f *paint) pinnedAt(width int) bool {
	on, at := false, -1
	for _, p := range f.pins {
		if p.width <= width && p.width >= at {
			on, at = p.on, p.width
		}
	}
	return on
}

// hiddenAt reports whether the element, or one around it, cannot render at a width: a show written
// "false" there.
func (f *paint) hiddenAt(width int) bool {
	for fr := f; fr != nil; fr = fr.parent {
		if fr.hidden != nil && fr.hidden[width] {
			return true
		}
	}
	return false
}

// cond is one condition a pair is measured under: a width, and a state with the elements it holds
// for (a hovered button holds hover for itself and every element around it).
type cond struct {
	width int
	state string
	in    map[*paint]bool
}

func (k cond) stateOf(f *paint) string {
	if k.in[f] {
		return k.state
	}
	return ""
}

// label is the condition as a prefix: medium+hover:, scrolled:, "" for the base.
func (k cond) label() string {
	var parts []string
	if k.width > 0 {
		parts = append(parts, stylesheet.Widths()[k.width-1])
	}
	if k.state != "" {
		parts = append(parts, k.state)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "+") + ":"
}

// winner is the attribute among names that the cascade applies at a condition: the latest state, then
// the widest width, then the table's order (material right after background), then the source's.
func (f *paint) winner(k cond, names ...string) *paintIntent {
	state := k.stateOf(f)
	var best *paintIntent
	var bestKey [3]int
	for _, pi := range f.written {
		if indexOfWord(names, pi.name) < 0 || pi.width > k.width {
			continue
		}
		s := 0
		if len(pi.states) > 0 {
			if state == "" || indexOfWord(pi.states, state) < 0 {
				continue
			}
			s = stylesheet.StateIndex(state)
		}
		rank := 0
		if pi.name == "material" {
			rank = 1
		}
		key := [3]int{s, pi.width, rank}
		if best == nil || key[0] > bestKey[0] || key[0] == bestKey[0] && (key[1] > bestKey[1] || key[1] == bestKey[1] && key[2] >= bestKey[2]) {
			best, bestKey = pi, key
		}
	}
	return best
}

func indexOfWord(list []string, word string) int {
	for i, w := range list {
		if w == word {
			return i
		}
	}
	return -1
}

// conditionsOf lists the conditions an element's text is measured under, base first: every width it
// can render at, then each state an element of its chain writes a colour, a ground or a type for, with
// the elements it can hold for together. A hover or a press holds for an element and every element
// around it, so the hovered ones are the outer ones from some point; open holds for what is inside the
// openers around it (openSets); focus and active hold for one element; scrolled for the page.
// disabled is never measured.
func conditionsOf(f *paint) []cond {
	writers := map[string][]*paint{}
	for fr := f; fr != nil; fr = fr.parent {
		seen := map[string]bool{}
		for _, pi := range fr.written {
			for _, s := range pi.states {
				if s != "disabled" && !seen[s] {
					seen[s] = true
					writers[s] = append(writers[s], fr)
				}
			}
		}
	}
	widths := len(stylesheet.Widths())
	var out []cond
	for w := 0; w <= widths; w++ {
		if !f.hiddenAt(w) {
			out = append(out, cond{width: w})
		}
	}
	for _, state := range []string{"open", "active", "scrolled", "hover", "focus", "pressed"} {
		list := writers[state]
		if len(list) == 0 {
			continue
		}
		var sets [][]*paint
		switch state {
		case "hover", "pressed":
			for j := range list {
				sets = append(sets, list[j:])
			}
		case "open":
			sets = openSets(f, list)
		case "scrolled":
			sets = append(sets, list)
		default:
			for j := range list {
				sets = append(sets, list[j:j+1])
			}
		}
		for w := 0; w <= widths; w++ {
			if f.hiddenAt(w) {
				continue
			}
			for _, set := range sets {
				in := map[*paint]bool{}
				for _, fr := range set {
					in[fr] = true
				}
				out = append(out, cond{width: w, state: state, in: in})
			}
		}
	}
	return out
}

// openSets are the writers of open: values that hold together. An open: class matches an element with
// an opener around it (an accordion's details, a button written expanded) and no closed one
// (values.go): opening the openers around it from the outermost down to some point holds for the
// writers from that point out to the outermost opener, never for an opener's own open: values when
// nothing around it opens, nor for what is outside the outermost one.
func openSets(f *paint, writers []*paint) [][]*paint {
	at := map[*paint]int{}
	var openers []int
	i := 0
	for fr := f; fr != nil; fr = fr.parent {
		at[fr] = i
		if fr.opener {
			openers = append(openers, i)
		}
		i++
	}
	if len(openers) == 0 {
		return nil
	}
	outermost := openers[len(openers)-1]
	var sets [][]*paint
	for j := range openers {
		from := 0 // every opener open: the writers inside the outermost one
		if j > 0 {
			from = openers[j-1] // the openers from the j-th out: what is inside the one just within, and it
		}
		var set []*paint
		for _, w := range writers {
			if p := at[w]; p >= from && p < outermost {
				set = append(set, w)
			}
		}
		if len(set) > 0 && (len(sets) == 0 || len(sets[len(sets)-1]) != len(set)) {
			sets = append(sets, set)
		}
	}
	return sets
}

// swaps are the colours a suggestion tries in place of the written ones: a value of an attribute, or
// the default colour of an element's text (nil: the page's ink).
type swaps struct {
	intents  map[*paintIntent]map[string]string
	defaults map[*paint]string
	// extra are colours written anew on an element for one condition (hover:color on a button)
	extra map[*paint][]*paintIntent
	// drop are backgrounds and materials left out, as if not written: what lies behind shows (heldPair:
	// whether a pair falls short because of its background)
	drop map[*paintIntent]bool
}

func (s *swaps) dropped(pi *paintIntent) bool {
	return s != nil && pi != nil && s.drop[pi]
}

func (s *swaps) values(pi *paintIntent) []string {
	if s == nil || s.intents[pi] == nil || pi.values == nil {
		return pi.values
	}
	out := make([]string, len(pi.values))
	for i, v := range pi.values {
		out[i] = v
		if to, ok := s.intents[pi][v]; ok {
			out[i] = to
		}
	}
	return out
}

func (s *swaps) text(owner *paint, value string) string {
	if s != nil {
		if to, ok := s.defaults[owner]; ok {
			return to
		}
	}
	return value
}

// shade is one colour a reader may see: painted in a mode, with the value it is written as and where.
type shade struct {
	rgba   stylesheet.RGBA
	value  string
	intent *paintIntent // the attribute it is written in; nil for a look of the stylesheet
	owner  *paint       // the element whose colour or look it is
	what   string       // how a message names it
	picks  []choice     // the decisions it stands for: a value of a {{ }}, an arm of a ternary
}

// ground is one backdrop a reader may see behind the text, opaque, with the nearest layer that paints.
type ground struct {
	rgba  stylesheet.RGBA
	top   shade
	what  string
	picks []choice // the decisions of every layer in it
}

// textOf is the colour of the element's text at a condition, one shade per value it may take; false
// when a colour from data decides it. An icon is painted in its own background when it writes one,
// else in its text colour.
func (c *compiler) textOf(f *paint, k cond, m stylesheet.Mode, s *swaps, icon bool) ([]shade, bool) {
	palette := c.table.Palette()
	if icon {
		if w := f.winner(k, "background"); w != nil {
			return c.shades(w, f, m, s, func(v string) string { return attrWritten(w, v) })
		}
	}
	for fr := f; fr != nil; fr = fr.parent {
		if fr.token != nil && fr.token.look.Share > 0 && fr.parent != nil {
			// a share of the text around it (the terminal's prompt): that colour, at its share
			around, ok := c.textOf(fr.parent, k, m, s, false)
			if !ok {
				return nil, false
			}
			out := make([]shade, 0, len(around))
			for _, t := range around {
				t.rgba.A *= float64(fr.token.look.Share) / 100
				t.what = fr.token.what(t.value + " " + strconv.Itoa(fr.token.look.Share) + "%")
				out = append(out, t)
			}
			return out, len(out) > 0
		}
		if w := fr.winner(k, "color"); w != nil {
			values := s.values(w)
			if values == nil {
				return nil, false
			}
			badge := indexOfWord(fr.classes, "badge") >= 0
			var out []shade
			for i, v := range values {
				text, what := v, attrWritten(w, v)
				if badge {
					var family bool
					if _, text, family = c.table.BadgeTint(v); family {
						if e, ok := palette.Engine(text, m); ok {
							text = e // the engine colour as written in this mode: warning+55, warning
						}
						what += " (chữ " + text + " của <badge>)"
					}
				}
				for _, p := range c.paintsOf(text, m) { // gradient text: each sample its own (gradient.go)
					out = append(out, shade{rgba: p.rgba, value: v, intent: w, owner: fr, what: what + p.at, picks: w.choices(i)})
				}
			}
			return out, len(out) > 0
		}
		if look := fr.look(k.width); look.Text != "" {
			v := s.text(fr, look.Text)
			if e, ok := palette.Engine(v, m); ok {
				// an engine colour (the words of <error>, a code colour) as written in this mode: the
				// message says danger+25 or danger, the suggestions step from it, the advice names danger
				v = e
			}
			rgba, ok := palette.Paint(v, m)
			what := "chữ " + v + " mặc định của <" + fr.tag + ">"
			if fr.token != nil {
				what = fr.token.what(v)
			}
			return []shade{{rgba: rgba, value: v, owner: fr, what: what}}, ok
		}
	}
	v := s.text(nil, stylesheet.RootLook().Text)
	rgba, ok := palette.Paint(v, m)
	return []shade{{rgba: rgba, value: v, what: "chữ " + v + " mặc định của trang"}}, ok
}

// attrWritten is an attribute with one of its values, as the source writes it: hover:color="brand".
func attrWritten(pi *paintIntent, v string) string {
	if pi.attr == nil {
		return pi.name + "=\"" + v + "\""
	}
	return pi.attr.Name + "=\"" + v + "\""
}

// shades paints each value of an attribute.
func (c *compiler) shades(pi *paintIntent, owner *paint, m stylesheet.Mode, s *swaps, what func(string) string) ([]shade, bool) {
	values := s.values(pi)
	if values == nil {
		return nil, false
	}
	var out []shade
	for i, v := range values {
		for _, p := range c.paintsOf(v, m) { // a gradient: each sample its own (gradient.go)
			out = append(out, shade{rgba: p.rgba, value: v, intent: pi, owner: owner, what: what(v) + p.at, picks: pi.choices(i)})
		}
	}
	return out, len(out) > 0
}

// layerOf is what an element paints behind its content at a condition, one shade per value it may
// take (none: nothing); false when a background from data decides it.
func (c *compiler) layerOf(f *paint, k cond, m stylesheet.Mode, s *swaps) ([]shade, bool) {
	plain := func(v string) string { return v }
	w := f.winner(k, "background", "material")
	switch {
	case s.dropped(w):
		return nil, true // left out: the element paints nothing of its own here
	case w == nil:
		return c.surface(f, k, m, s)
	case w.name == "background":
		return c.backgroundLayer(f, k, m, s, w) // a colour, or a gradient over what is beneath it (gradient.go)
	}
	// a material: the background the cascade hands it at this condition, else paper (a card's and a
	// popover's too, reboot.css). Glass first, background second (decision of 13/10/2026): the glass is
	// that colour without its /N at the material's coverage, and where the browser cannot blur the
	// frame shows the background as written
	under := f.winner(k, "background")
	var out []shade
	for _, v := range w.values {
		glass, isGlass := stylesheet.GlassOf(v)
		if !isGlass { // none: the frame's own background, as without a material
			var layer []shade
			var ok bool
			if under != nil {
				layer, ok = c.shades(under, f, m, s, plain)
			} else {
				layer, ok = c.surface(f, k, m, s)
			}
			if !ok {
				return nil, false
			}
			out = append(out, layer...)
			continue
		}
		tints := []shade{{value: "paper", owner: f}}
		if under != nil {
			var ok bool
			if tints, ok = c.shades(under, f, m, s, plain); !ok {
				return nil, false
			}
		} else {
			tints[0].rgba, _ = c.table.Palette().Paint("paper", m)
		}
		for _, t := range tints {
			// the material decides how much of the tint shows: the error of a pair on glass the engine
			// coloured stands at it
			t.intent, t.what = w, "kính "+t.value+" ("+attrWritten(w, v)+")"
			// where the browser cannot blur the backdrop (the glass's one fallback since 13/10/2026):
			// the background as written, its /N the fallback's own, named the fallback with or without one
			solid := t
			solid.what = t.value + " (lối lùi của " + attrWritten(w, v) + " khi trình duyệt không nhoè được)"
			see := t
			if colour := stylesheet.GlassColour(t.value); colour != t.value {
				// the glass is the colour without its /N; the fallback is not the glass's colour
				see.value, see.what = colour, "kính "+colour+" ("+attrWritten(w, v)+")"
				see.rgba, _ = c.table.Palette().Paint(colour, m)
			}
			see.rgba.A *= float64(glass.Opacity) / 100
			out = append(out, see, solid)
		}
	}
	return out, true
}

// surface is the look of an element's kind behind its content: a card's paper, a field's ink/5, a
// badge's fill (decided by its colour).
func (c *compiler) surface(f *paint, k cond, m stylesheet.Mode, s *swaps) ([]shade, bool) {
	look := f.look(k.width)
	if look.Tinted {
		w := f.winner(k, "color")
		if w == nil {
			return nil, true
		}
		values := s.values(w)
		if values == nil {
			return nil, false
		}
		var out []shade
		for i, v := range values {
			fill, _, _ := c.table.BadgeTint(v)
			if rgba, ok := c.table.Palette().Paint(fill, m); ok {
				out = append(out, shade{rgba: rgba, value: fill, owner: f, what: fill + " của <badge>", picks: w.choices(i)})
			}
		}
		return out, true
	}
	if look.Surface == "" {
		return nil, true
	}
	rgba, ok := c.table.Palette().Paint(look.Surface, m)
	if !ok {
		return nil, true
	}
	return []shade{{rgba: rgba, value: look.Surface, owner: f, what: look.Surface}}, true
}

func opaque(layer []shade) bool {
	for _, s := range layer {
		if !s.rgba.Opaque() {
			return false
		}
	}
	return true
}

// backdrop is what lies behind content of start at a condition, each ground a reader may see; false
// when it cannot be known: a background from data, or an element over content the page does not know
// (place over a photo) with nothing solid between. A translucent layer over such content is measured
// over pure white and pure black. A pinned element sticks over the page while it scrolls under: what
// lies behind it is the page's own background, from the width its pin starts at. Each ground is
// painted as a screen shows it, 8 bits a channel.
func (c *compiler) backdrop(start *paint, k cond, m stylesheet.Mode, s *swaps) ([]ground, bool) {
	palette := c.table.Palette()
	root := start.root()
	var layers [][]shade
	closed := false
	for fr := start; fr != nil && !closed; {
		layer, known := c.layerOf(fr, k, m, s)
		if !known {
			return nil, false
		}
		if len(layer) > 0 {
			layers = append(layers, layer)
			if opaque(layer) {
				closed = true
				break
			}
		}
		if fr.placedAt(k.width) {
			if len(layers) == 0 {
				return nil, false
			}
			white, _ := stylesheet.ParseHex("#ffffff")
			black, _ := stylesheet.ParseHex("#000000")
			const unknown = "nội dung bên dưới (xét cả nền trắng và nền đen)"
			layers = append(layers, []shade{{rgba: white, value: "white", what: unknown}, {rgba: black, value: "black", what: unknown}})
			closed = true
			break
		}
		if fr.pinnedAt(k.width) && fr != root {
			fr = root
			continue
		}
		fr = fr.parent
	}
	if !closed {
		look := stylesheet.RootLook()
		rgba, _ := palette.Paint(look.Surface, m)
		layers = append(layers, []shade{{rgba: rgba, value: look.Surface, what: look.Surface}})
	}
	last := layers[len(layers)-1]
	grounds := make([]ground, 0, len(last))
	for _, s := range last {
		grounds = append(grounds, ground{rgba: s.rgba.Painted(), top: s, what: s.what, picks: s.picks})
	}
	for i := len(layers) - 2; i >= 0; i-- {
		var next []ground
		seen := map[string]bool{}
		for _, s := range layers[i] {
			for _, g := range grounds {
				out := ground{rgba: s.rgba.Painted(), top: s, what: s.what, picks: s.picks}
				if !s.rgba.Opaque() {
					if !agree(s.picks, g.picks) {
						continue // never on screen together
					}
					out.rgba = s.rgba.Over(g.rgba).Painted()
					out.what = s.what + " phủ trên " + g.what
					out.picks = append(append([]choice(nil), s.picks...), g.picks...)
				}
				key := out.rgba.Hex() + out.what + choicesKey(out.picks)
				if !seen[key] {
					seen[key] = true
					next = append(next, out)
				}
			}
		}
		grounds = next
	}
	return grounds, true
}

// size and weight are the type of the element's text at a condition: written on it or around it, else
// its kind's look, else the browser's. A value from data counts as the smallest, also one a {{ }} may
// yield besides those it spells out.
func (c *compiler) size(f *paint, k cond) float64 {
	for fr := f; fr != nil; fr = fr.parent {
		if w := fr.winner(k, "size"); w != nil {
			if w.partial {
				return 0
			}
			smallest := math.Inf(1)
			for _, v := range w.values {
				if px, ok := stylesheet.FontSize(fr.heading, v, w.width, k.width); ok && px < smallest {
					smallest = px
				}
			}
			if math.IsInf(smallest, 1) {
				return 0
			}
			return smallest
		}
		if look := fr.look(k.width); look.Size != 0 {
			return look.Size
		}
	}
	return stylesheet.RootLook().Size
}

func (c *compiler) weight(f *paint, k cond) int {
	for fr := f; fr != nil; fr = fr.parent {
		if w := fr.winner(k, "weight"); w != nil {
			if w.partial {
				return 0
			}
			lightest := 0
			for i, v := range w.values {
				if n, ok := stylesheet.FontWeight(v); ok && (i == 0 || n < lightest) {
					lightest = n
				}
			}
			return lightest
		}
		if look := fr.look(k.width); look.Weight != 0 {
			return look.Weight
		}
	}
	return stylesheet.RootLook().Weight
}

// pairing is a text measured at one condition in one mode: its worst pair.
type pairing struct {
	skip        bool
	ratio, need float64
	large       bool
	text        shade
	ground      ground
}

func (c *compiler) pairAt(f *paint, k cond, m stylesheet.Mode, s *swaps, icon bool) pairing {
	texts, ok := c.textOf(f, k, m, s, icon)
	if !ok {
		return pairing{skip: true}
	}
	start := f
	if icon {
		if f.placedAt(k.width) {
			return pairing{skip: true} // placed over what the page cannot know, nothing of its own between
		}
		start = f.parent // an icon is painted in its colour; behind it is what is behind its parent's content
	}
	if start == nil {
		return pairing{skip: true}
	}
	grounds, ok := c.backdrop(start, k, m, s)
	if !ok || len(grounds) == 0 {
		return pairing{skip: true}
	}
	p := pairing{ratio: math.Inf(1), need: stylesheet.NonTextRatio}
	if !icon {
		size, weight := c.size(f, k), c.weight(f, k)
		p.large = stylesheet.LargeText(size, weight)
		p.need = stylesheet.NormalTextRatio
		if p.large {
			p.need = stylesheet.LargeTextRatio
		}
	}
	told, measured := false, false
	for _, t := range texts {
		for _, g := range grounds {
			if !agree(t.picks, g.picks) {
				continue // a value of one arm on the ground of the other: never on screen
			}
			if toldAtRouter(f, g) {
				told = true
				continue // the call of router.highlight says this pair, once a route (tellHighlight)
			}
			measured = true
			// the text as painted over its ground, 8 bits a channel: the pixel a reader sees
			if r := stylesheet.Ratio(t.rgba.Over(g.rgba).Painted(), g.rgba); r < p.ratio {
				p.ratio, p.text, p.ground = r, t, g
			}
		}
	}
	if told && !measured {
		return pairing{skip: true} // every ground was the call's to tell
	}
	return p
}

// toldAtRouter reports whether a code token's pair on a ground is the call of router.highlight's to tell
// (stylesheet.Palette.HighlightWarning): the token's colour is the author's, and the ground is the page's
// canvas, paper or panel as they are, solid (no glass of them, which lets what is behind show), or the
// kind's own fill (inserted, deleted). Each other ground is measured here, at language=.
func toldAtRouter(f *paint, g ground) bool {
	if f.token == nil || f.token.look.Source == nil || !g.top.rgba.Opaque() {
		return false
	}
	switch v := g.top.value; {
	case v == "canvas", v == "paper", v == "panel":
		return true
	case f.token.look.Background != "" && v == f.token.look.Background:
		return true
	}
	return false
}

// measure checks the text of an element (or an icon with alt) under every condition and mode, and
// records the first pair that falls short as a compile error, with a colour that passes.
func (c *compiler) measure(f *paint, icon bool) {
	if f == nil || !icon && f.measured {
		return
	}
	if !icon {
		f.measured = true
		c.checkHiddenText(f) // gradient text around it that an element between hides (gradient.go)
	}
	if f.tag == "option" {
		return // its select shows the chosen one; the list it opens is the system's
	}
	for fr := f; fr != nil; fr = fr.parent {
		if fr.disabled {
			return
		}
	}
	conds := conditionsOf(f)
	modes := c.table.Palette().Modes()
	for _, k := range conds {
		for _, m := range modes {
			p := c.pairAt(f, k, m, nil, icon)
			if p.skip || p.ratio >= p.need {
				continue
			}
			c.reportContrast(f, k, m, p, conds, icon)
			return
		}
	}
}

// passes reports whether a swap makes the element's pair pass under every condition and mode, and its
// worst ratio in each mode.
func (c *compiler) passes(f *paint, conds []cond, s *swaps, icon bool) (bool, map[stylesheet.Mode]float64) {
	return c.passesIn(f, conds, s, icon, c.table.Palette().Modes()...)
}

// passesIn is passes in the modes given only.
func (c *compiler) passesIn(f *paint, conds []cond, s *swaps, icon bool, modes ...stylesheet.Mode) (bool, map[stylesheet.Mode]float64) {
	for fr, extra := range s.extra {
		kept := fr.written
		fr.written = append(append([]*paintIntent(nil), kept...), extra...)
		defer func() { fr.written = kept }()
	}
	worst := map[stylesheet.Mode]float64{}
	for _, k := range conds {
		for _, m := range modes {
			p := c.pairAt(f, k, m, s, icon)
			if p.skip {
				continue
			}
			if p.ratio < p.need {
				return false, nil
			}
			if r, ok := worst[m]; !ok || p.ratio < r {
				worst[m] = p.ratio
			}
		}
	}
	return true, worst
}

// ratioText writes a ratio with two decimals, never rounded up to the ratio it misses (4.499 is 4.49):
// stylesheet.RatioText, which the line of router.highlight writes too.
func ratioText(r, need float64) string { return stylesheet.RatioText(r, need) }

// colourTry is one colour tried in place of the one that falls short: as the author writes it, the
// element it is written on when that is not the one measured, and the swap that tries it. value is the
// text colour it tries ("" for a background made solid), and with the same swap for another text colour
// (overshootNote).
type colourTry struct {
	write string
	on    *paint
	swap  *swaps
	value string
	with  func(v string) *swaps
}

// colours lists, in order, the text colours to try (decision C, with the operators of 13/10/2026): a
// label on a fill F takes F.on; a colour with an alpha tries itself solid first; white or black the
// other; a lighter X-N steps back toward X by fives (ink-30: ink-25 … ink-5, then ink); a stronger X+N
// steps further by fives (brand+15: brand+20 … brand+100); a token written plain, ink among them, the
// smallest stronger step that reads (brand: brand+5 … brand+100). Then ink, and last paper, the other
// end of the palette from ink: text on a dark fill.
func (c *compiler) colours(p pairing) []string {
	palette := c.table.Palette()
	var out []string
	add := func(v string) {
		if v == p.text.value || indexOfWord(out, v) >= 0 {
			return
		}
		if _, ok := palette.Paint(v, stylesheet.LightMode); ok { // a colour this palette paints
			out = append(out, v)
		}
	}
	if fill, ok := stylesheet.SplitColour(p.ground.top.value); ok && fill.Plain() && !fill.Fixed() && p.ground.top.rgba.Opaque() &&
		palette.Has(fill.Base+".on") {
		add(fill.Base + ".on") // a label on a fill
	}
	if v, ok := stylesheet.SplitColour(p.text.value); ok {
		if v.Translucent {
			v.Alpha, v.Translucent = 100, false
			add(v.String())
		}
		switch {
		case v.Fixed():
			if v.Base == "white" {
				add("black")
			} else {
				add("white")
			}
		case v.Op == '-': // back toward the colour itself: the multiples of 5 below N, then X
			for n := (v.Amount - 1) / 5 * 5; n >= 5; n -= 5 {
				add(v.Base + "-" + strconv.Itoa(n))
			}
			add(v.Base)
		case v.Op == '+': // further from canvas: the multiples of 5 above N
			for n := (v.Amount/5 + 1) * 5; n <= 100; n += 5 {
				add(v.Base + "+" + strconv.Itoa(n))
			}
		default: // the token as written: its smallest stronger step that reads
			for n := 5; n <= 100; n += 5 {
				add(v.Base + "+" + strconv.Itoa(n))
			}
		}
	}
	add("ink")
	add("paper")
	return out
}

// tries are the colours to try, each in place of the colour that falls short and, when it falls short
// only under a condition, written for that condition on the element that holds it (a button's label
// on its hover fill: hover:color on the button); then a translucent background as its token solid.
func (c *compiler) tries(f *paint, k cond, p pairing) []colourTry {
	var out []colourTry
	prefix := k.label()
	name := "color"
	if p.text.intent != nil && p.text.intent.attr != nil {
		name = p.text.intent.attr.Name
	}
	// in place of the colour that falls short, where it is written (or the default it is)
	instead := func(v string) *swaps {
		s := &swaps{intents: map[*paintIntent]map[string]string{}, defaults: map[*paint]string{}}
		if p.text.intent != nil {
			s.intents[p.text.intent] = map[string]string{p.text.value: v}
		} else {
			s.defaults[p.text.owner] = v
		}
		return s
	}
	for _, v := range c.colours(p) {
		out = append(out, colourTry{write: name + "=\"" + v + "\"", swap: instead(v), value: v, with: instead})
		if prefix == "" || p.text.intent != nil && p.text.intent.attr != nil && strings.HasPrefix(p.text.intent.attr.Name, prefix) {
			continue
		}
		on := f
		var states []string
		if k.state != "" {
			states = []string{k.state}
			for fr := f; fr != nil; fr = fr.parent {
				if k.in[fr] {
					on = fr // the nearest element the state holds for
					break
				}
			}
		}
		// written for the condition on the element it holds for
		under := func(v string) *swaps {
			extra := &paintIntent{name: "color", node: on.node, width: k.width, states: states, values: []string{v}}
			return &swaps{extra: map[*paint][]*paintIntent{on: {extra}}}
		}
		out = append(out, colourTry{write: prefix + "color=\"" + v + "\"", on: on, swap: under(v), value: v, with: under})
	}
	if top := p.ground.top; top.intent != nil && top.intent.attr != nil && top.intent.name == "background" {
		if v, ok := stylesheet.SplitColour(top.value); ok && v.Translucent {
			v.Alpha, v.Translucent = 100, false
			s := &swaps{intents: map[*paintIntent]map[string]string{top.intent: {top.value: v.String()}}}
			out = append(out, colourTry{write: top.intent.attr.Name + "=\"" + v.String() + "\"", on: top.owner, swap: s})
		}
	}
	return out
}

// reportContrast tells of a pair that falls short — a warning of the page, or under "strict" a compile
// error of it (contrastError): where the pair is decided, the
// pair as written, its ratio, the mode and the condition, what it needs, and the first colour that
// passes everywhere the element's pair occurs.
func (c *compiler) reportContrast(f *paint, k cond, m stylesheet.Mode, p pairing, conds []cond, icon bool) {
	need := "chữ thường cần 4.5:1 (chữ lớn 3:1)"
	switch {
	case icon:
		need = "icon có alt cần 3:1 như mọi thứ không phải chữ mà vẫn nói một điều"
	case p.large:
		need = "chữ lớn cần 3:1"
	}
	what := p.text.what
	if icon {
		what = "icon màu " + what
	}
	message := what + " trên nền " + p.ground.what + " chỉ " + ratioText(p.ratio, p.need) + ":1 ở chế độ " + m.Word()
	if label := k.label(); label != "" {
		message += " khi " + label
	}
	message += "; " + need + "."

	// a token of a code element is coloured by the engine from the palette's roles, or by the site's
	// router.highlight: no attribute the author writes on the code changes it, so no colour to write is
	// suggested here (codeToken.advice says where it changes)
	codeText := f.token != nil
	var tries []colourTry
	if !codeText {
		tries = c.tries(f, k, p)
	}
	use, note := "", ""
	for _, t := range tries {
		ok, worst := c.passes(f, conds, t.swap, icon)
		if !ok {
			continue
		}
		use = t.write
		if t.on != nil && t.on != f {
			use += " trên <" + t.on.tag + "> (" + t.on.node.File + ":" + strconv.Itoa(t.on.node.Line) + ":" + strconv.Itoa(t.on.node.Column) + ")"
		}
		use += c.ratiosText(worst)
		owner := p.text.owner
		if t.on != nil {
			owner = t.on
		}
		note = c.overshootNote(f, owner, conds, icon, t)
		break
	}
	token := func(v string) string {
		if cv, ok := stylesheet.SplitColour(v); ok {
			return cv.Base
		}
		return v
	}
	fixed := func(v string) bool {
		cv, ok := stylesheet.SplitColour(v)
		return ok && cv.Fixed()
	}
	rung, ground := token(p.text.value), token(p.ground.top.value)
	// the ground's top is a see-through fill of the text's own colour (a neutral button's ink/14, a
	// field's ink/8 under ink text): the fill moves with the text, but the ground it makes is mostly what
	// lies behind it, so the colour to declare anew is the text's own, on the ground as it is shown
	// (ink/8 phủ trên canvas), never the other end of the palette
	own := rung == ground && !p.ground.top.rgba.Opaque()
	shown := ground
	if own {
		shown = p.ground.what
	}
	if colours := c.colours(p); len(colours) > 0 && p.text.intent != nil && !own {
		rung = token(colours[0]) // the rung the first suggestion names
	}
	// a rung worth declaring anew: one router.colors takes (not white or black), and not the ground's
	// own, which a new value would move too (unless that ground is the rung's own see-through fill)
	declarable := (rung != ground || own) && !fixed(rung)
	redeclare := ""
	if !fixed(ground) {
		redeclare = c.redeclareGround(ground) // a token in router.colors, a gradient in router.gradients (gradient.go)
	}
	surface := ""
	if use == "" && !codeText {
		surface = c.fixedSurfaceAdvice(f, conds, p, icon)
	}
	all, alone := true, true
	if use == "" && surface == "" && !codeText {
		all, alone = c.readable(f, conds, p, icon)
	}
	switch {
	case codeText:
		message += f.token.advice(p.text.value, p.ground.top.value)
	case surface != "":
		// a fixed colour (white, black) under text that follows the mode: change the surface or fix the
		// text, never a token to declare (white and black are not tokens)
		message += surface
	case !all && alone:
		// black reads on some of its grounds and white on the others: no one colour, declared or not
		message += " Không một màu chữ nào, kể cả trắng hay đen, đọc được trên mọi nền của nó ở mọi điều kiện: viết màu chữ riêng cho từng điều kiện (như hover:color), hoặc đổi nền " + ground + " (background, material)" + redeclare + "."
	case !all:
		message += " Không màu chữ nào, kể cả trắng hay đen, đọc được trên nền này: đổi nền " + ground + " (background, material)" + redeclare + "."
	case p.text.intent == nil && !icon && use != "" && declarable:
		message += " Chữ này engine tô theo router.colors: viết " + use + ", hoặc khai lại " + rung + " trong router.colors cho đủ tương phản." + note
	case p.text.intent == nil && !icon && use != "":
		message += " Chữ này engine tô theo router.colors: viết " + use + "." + note
	case p.text.intent == nil && !icon && declarable && c.isGradient(ground):
		message += " Chữ này engine tô theo router.colors: khai lại " + rung + " trong router.colors bằng một cặp { light: \"#…\", dark: \"#…\" } đủ tương phản trên nền " + ground + redeclare + "."
	case p.text.intent == nil && !icon && own:
		message += " Chữ này engine tô theo router.colors: khai lại " + rung + " trong router.colors bằng một cặp { light: \"#…\", dark: \"#…\" } đủ tương phản trên nền " + shown + "."
	case p.text.intent == nil && !icon && declarable:
		message += " Chữ này engine tô theo router.colors: khai lại " + rung + " hoặc " + ground + " trong router.colors bằng một cặp { light: \"#…\", dark: \"#…\" } đủ tương phản."
	case use != "":
		message += " Dùng " + use + "." + note
	default:
		if !declarable { // the other end of the palette from the ground
			rung = "paper"
			if ground == "paper" {
				rung = "ink"
			}
		}
		declare := "khai "
		if own {
			declare = "khai lại "
		}
		message += " Không màu gợi ý nào đạt ở mọi chế độ và điều kiện: " + declare + rung + " trong router.colors bằng một cặp { light: \"#…\", dark: \"#…\" } đủ tương phản trên nền " + shown + "."
	}

	node, line, column := f.node, f.node.Line, f.node.Column
	switch {
	case codeText && f.token.language.attr != nil && f.token.language.attr.Line > 0:
		// told at the attribute that names the language: what chose these colours
		node, line, column = f.token.language.node, f.token.language.attr.Line, f.token.language.attr.Column
	case codeText:
	case p.text.intent != nil && p.text.intent.attr != nil:
		node, line, column = p.text.intent.node, p.text.intent.attr.Line, p.text.intent.attr.Column
	case p.ground.top.intent != nil && p.ground.top.intent.attr != nil:
		node, line, column = p.ground.top.intent.node, p.ground.top.intent.attr.Line, p.ground.top.intent.attr.Column
	}
	if !c.site.strictContrast() {
		// the default (router.contrast "warning"): the page renders as written, the author hears of
		// the pair while developing (warning.go), once — held until the compile ends, where the pairs
		// that fall short on one background are told together (tellContrast)
		c.hold(&heldPair{told: Warning{File: node.File, Line: line, Column: column, Message: message},
			f: f, k: k, m: m, p: p, conds: conds, icon: icon})
		return
	}
	err := &Error{File: node.File, Line: line, Column: column, Message: message}
	key := err.Error()
	if c.contrastSeen == nil {
		c.contrastSeen = map[string]bool{}
	}
	if !c.contrastSeen[key] {
		c.contrastSeen[key] = true
		c.contrast = append(c.contrast, err)
	}
}

// readable reports whether a colour could read where the failing text of a pair stands, one value a
// mode (router.colors declares a { light, dark } pair): all, when in each mode black or white reads
// on every ground that text has across the conditions; alone, when at each condition by itself one of
// them does (a colour written for each condition could read). When neither holds, no colour written
// or declared reads there: the ground has to change.
func (c *compiler) readable(f *paint, conds []cond, p pairing, icon bool) (all, alone bool) {
	all, alone = true, true
	for _, m := range c.table.Palette().Modes() {
		reads := map[string][]bool{}
		for _, extreme := range []string{"black", "white"} {
			s := &swaps{intents: map[*paintIntent]map[string]string{}, defaults: map[*paint]string{}}
			if p.text.intent != nil {
				every := map[string]string{}
				for _, v := range p.text.intent.values {
					every[v] = extreme
				}
				s.intents[p.text.intent] = every
			} else {
				s.defaults[p.text.owner] = extreme
			}
			for _, k := range conds {
				q := c.pairAt(f, k, m, s, icon)
				// another colour there (hover:color), or nothing to measure: not this text's ground
				passes := q.skip || q.text.intent != p.text.intent || q.text.owner != p.text.owner || q.ratio >= q.need
				reads[extreme] = append(reads[extreme], passes)
			}
		}
		every := func(list []bool) bool {
			for _, ok := range list {
				if !ok {
					return false
				}
			}
			return true
		}
		if !every(reads["black"]) && !every(reads["white"]) {
			all = false
		}
		for i := range conds {
			if !reads["black"][i] && !reads["white"][i] {
				alone = false
			}
		}
	}
	return all, alone
}

// Errors are several problems of one page, in the order it was compiled: the contrast check reports
// every pair that falls short at once, one a line.
type Errors []*Error

func (e Errors) Error() string {
	lines := make([]string, len(e))
	for i, err := range e {
		lines[i] = err.Error()
	}
	return strings.Join(lines, "\n")
}

// Unwrap gives each problem to errors.Is and errors.As.
func (e Errors) Unwrap() []error {
	out := make([]error, len(e))
	for i, err := range e {
		out[i] = err
	}
	return out
}

// contrastError ends the check when the page's compile does: by default it tells the pairs held
// (tellContrast) and answers nil; under "strict" a page whose pairs fall short answers the one problem,
// or all of them.
func (c *compiler) contrastError() error {
	c.tellContrast()
	switch len(c.contrast) {
	case 0:
		return nil
	case 1:
		return c.contrast[0]
	}
	return Errors(append([]*Error(nil), c.contrast...))
}

// ---- the pairs of one background, told once ----

// heldPair is a pair short of contrast that the default mode holds until the compile ends: the pair as
// told alone (the line "strict" makes its compile error, word for word), and what measured it.
type heldPair struct {
	told  Warning
	f     *paint
	k     cond
	m     stylesheet.Mode
	p     pairing
	conds []cond
	icon  bool
	// group is the background the pair falls short on, when that background is why (groupKey): where
	// it is written, its value, the mode and the condition; "" when the pair is its text's own
	group string
}

// hold keeps a pair for the compile's end, in the page's list of warnings where it was found, each once
// a group.
func (c *compiler) hold(h *heldPair) {
	h.group = c.groupKey(h)
	key := h.told.String() + "\x00" + h.group
	if c.contrastSeen == nil {
		c.contrastSeen = map[string]bool{}
	}
	if c.contrastSeen[key] {
		return
	}
	c.contrastSeen[key] = true
	w := h.told
	w.held = h
	c.out.warnings = append(c.out.warnings, w)
}

// groupKey is the group of a pair: the background its ground's top layer is written in — on the page,
// a region, a layout, the content — when that background is why the pair falls short, the text reading
// at the same condition and mode once the background is left out (what lies behind it shows); "" when
// the pair is its text's own: a colour short on the page's canvas, on a card's paper, or short on its
// background and on what lies behind it alike. It is told alone.
func (c *compiler) groupKey(h *heldPair) string {
	if h.f.token != nil {
		return "" // a token of a code element: its advice is its own (codeToken.advice: the engine's, or router.highlight's), told at its code
	}
	top := h.p.ground.top
	source := writtenSource(top)
	if source == nil {
		return ""
	}
	without := &swaps{drop: map[*paintIntent]bool{top.intent: true, source: true}}
	if q := c.pairAt(h.f, h.k, h.m, without, h.icon); q.skip || q.ratio < q.need {
		return ""
	}
	return attrPlace(source.node, source.attr) + "\x00" + top.value + "\x00" + h.m.Word() + "\x00" + h.k.label()
}

// writtenSource is the attribute a ground's top layer is written in: its own, or, for the background
// the engine gives a frame pinned to an edge (the page's, so the content scrolling under it does not
// show through), <page background>. nil when nothing written decides it: the page's canvas, a card's
// paper, a frame pinned on a page that writes no background.
func writtenSource(top shade) *paintIntent {
	switch pi := top.intent; {
	case pi == nil:
		return nil
	case pi.attr != nil:
		return pi
	case pi.name != "background" || top.owner == nil:
		return nil
	}
	root := top.owner.root()
	if !root.page {
		return nil
	}
	for _, w := range root.written {
		if w.name == "background" && w.attr != nil && w.attr.Name == "background" {
			return w
		}
	}
	return nil
}

// surfaceSwap is a ground's top layer in another colour: the attribute it is written in and, for a
// pinned frame's, the background the engine gave it from the page.
func surfaceSwap(s *swaps, top shade, source *paintIntent, v string) {
	s.intents[top.intent] = map[string]string{top.value: v}
	if source != top.intent {
		every := map[string]string{}
		for _, written := range source.values {
			every[written] = v
		}
		s.intents[source] = every
	}
}

// attrPlace is where an attribute is written, file:line:col.
func attrPlace(n *Node, a *Attr) string {
	return n.File + ":" + strconv.Itoa(a.Line) + ":" + strconv.Itoa(a.Column)
}

// pairGroup is the pairs of one group, and the places they are told at alone, each once, in the order
// found.
type pairGroup struct {
	pairs  []*heldPair
	places []Warning
	seen   map[string]bool
	told   bool
}

func (g *pairGroup) add(h *heldPair) {
	g.pairs = append(g.pairs, h)
	at := h.told.File + ":" + strconv.Itoa(h.told.Line) + ":" + strconv.Itoa(h.told.Column)
	if g.seen == nil {
		g.seen = map[string]bool{}
	}
	if !g.seen[at] {
		g.seen[at] = true
		g.places = append(g.places, h.told)
	}
}

// tellContrast tells the pairs held, where they were found among the page's warnings, each line once:
// the pairs of one group told at two places or more as one line at their background (groupLine), where
// the first of them was found; every other pair as itself. Under "strict" nothing is held: each pair is
// its own error, as it always was.
func (c *compiler) tellContrast() {
	groups := map[string]*pairGroup{}
	held := false
	for _, w := range c.out.warnings {
		if w.held == nil {
			continue
		}
		held = true
		if key := w.held.group; key != "" {
			if groups[key] == nil {
				groups[key] = &pairGroup{}
			}
			groups[key].add(w.held)
		}
	}
	if !held {
		return
	}
	out := make([]Warning, 0, len(c.out.warnings))
	for _, w := range c.out.warnings {
		h := w.held
		if h == nil {
			out = append(out, w)
			continue
		}
		told := h.told
		if g := groups[h.group]; g != nil && len(g.places) > 1 {
			if g.told {
				continue
			}
			g.told = true
			told = c.groupLine(g)
		}
		if c.out.warned.first(told.String()) {
			out = append(out, told)
		}
	}
	c.out.warnings = out
}

// groupLine is the one line of a group, at its background: the background as written (a fixed colour
// says it keeps its colour while the mode changes), the mode and condition the pairs fall short in, at
// how many places, each text colour with its worst ratio, the first places, and what reads there for
// every pair of it. Each pair's own line, with its own suggestion, is the error "strict" gives.
func (c *compiler) groupLine(g *pairGroup) Warning {
	first := g.pairs[0]
	top := first.p.ground.top
	source := writtenSource(top)
	message := attrWritten(source, top.value)
	if top.intent.name == "material" {
		message = top.what // kính paper (material="glass")
	}
	if word := fixedWord(top); word != "" && len(c.table.Palette().Modes()) > 1 {
		message += " giữ màu " + word
	}
	message += " ở chế độ " + first.m.Word()
	if label := first.k.label(); label != "" {
		message += " khi " + label
	}
	a := source.attr
	line := Warning{File: source.node.File, Line: a.Line, Column: a.Column, Message: message}
	line.head = line.String() // what every page that shares the background says the same
	line.Message += "; chữ ở " + strconv.Itoa(len(g.places)) + " chỗ trên nó khó đọc (" + groupRatios(g) + "; " + groupNeed(g) + "): " +
		placesText(g.places) + ". " + c.groupAdvice(g) + ` router.contrast("strict") nêu từng chỗ với gợi ý riêng.`
	return line
}

// groupRatios are the text colours of a group, each with its worst ratio, in the order found: ink
// 1.61:1, ink-30 3.22:1.
func groupRatios(g *pairGroup) string {
	const shown = 5
	type low struct{ ratio, need float64 }
	lows := map[string]low{}
	var order []string
	for _, h := range g.pairs {
		v := h.p.text.value
		l, seen := lows[v]
		if !seen {
			order = append(order, v)
		}
		if !seen || h.p.ratio < l.ratio {
			lows[v] = low{h.p.ratio, h.p.need}
		}
	}
	var parts []string
	for i, v := range order {
		if i == shown {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, v+" "+ratioText(lows[v].ratio, lows[v].need)+":1")
	}
	return strings.Join(parts, ", ")
}

// groupNeed is what the texts of a group need.
func groupNeed(g *pairGroup) string {
	var normal, large, icon bool
	for _, h := range g.pairs {
		switch {
		case h.icon:
			icon = true
		case h.p.large:
			large = true
		default:
			normal = true
		}
	}
	var parts []string
	switch {
	case normal:
		parts = append(parts, "chữ thường cần 4.5:1, chữ lớn 3:1")
	case large:
		parts = append(parts, "chữ lớn cần 3:1")
	}
	if icon {
		parts = append(parts, "icon có alt cần 3:1")
	}
	return strings.Join(parts, ", ")
}

// placesText writes the first places of a group, file:line:col — a place in the file of the one before
// it as line:col — then how many more: page.ui:1:26, content.ui:5:12, 9:4, 32:8 và 33 chỗ khác.
func placesText(places []Warning) string {
	const shown = 4
	var b strings.Builder
	file := ""
	for i, w := range places {
		if i == shown {
			b.WriteString(" và " + strconv.Itoa(len(places)-shown) + " chỗ khác")
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		if i == 0 || w.File != file {
			file = w.File
			b.WriteString(file + ":")
		}
		b.WriteString(strconv.Itoa(w.Line) + ":" + strconv.Itoa(w.Column))
	}
	return b.String()
}

// groupAdvice is what reads on a group's background for every pair of it, under every condition and in
// every mode each is measured in. On a fixed colour (white, black): the surface that follows the mode as
// the text does (canvas, paper), or the other fixed colour for the text on it. On any other ground: the
// first text colour suggested that reads for all of them, with the note a pair's own line has when that
// X+N goes far past one mode's need (groupOvershootNote), else change the ground.
func (c *compiler) groupAdvice(g *pairGroup) string {
	first := g.pairs[0]
	top := first.p.ground.top
	if word := fixedWord(top); word != "" {
		source := writtenSource(top)
		var parts []string
		for _, v := range surfacesFor(source) {
			s := &swaps{intents: map[*paintIntent]map[string]string{}}
			for _, h := range g.pairs {
				t := h.p.ground.top
				surfaceSwap(s, t, writtenSource(t), v)
			}
			if worst, ok := c.groupPasses(g, s); ok {
				parts = append(parts, "viết "+source.attr.Name+"=\""+v+"\""+c.ratiosText(worst))
				break
			}
		}
		other := otherFixed(top.value)
		if worst, ok := c.groupPasses(g, groupText(g, other)); ok {
			parts = append(parts, "giữ nền "+word+" và viết "+groupTextName(g)+"=\""+other+"\" cho chữ trên nó"+c.ratiosText(worst))
		}
		if len(parts) == 0 {
			return "Đổi nền " + top.value + " (background, material) hoặc màu chữ trên nó."
		}
		advice := strings.Join(parts, ", hoặc ")
		return strings.ToUpper(advice[:1]) + advice[1:] + "."
	}
	for _, v := range c.colours(first.p) {
		if worst, ok := c.groupPasses(g, groupText(g, v)); ok {
			write := groupTextName(g) + "=\"" + v + "\" cho chữ trên nó"
			return "Dùng " + write + c.ratiosText(worst) + "." + c.groupOvershootNote(g, v, write)
		}
	}
	ground := top.value
	cv, ok := stylesheet.SplitColour(ground)
	if ok {
		ground = cv.Base
	}
	advice := "Không màu chữ gợi ý nào đọc được cho mọi chữ trên nó: đổi nền " + ground + " (background, material)"
	if ok && !cv.Fixed() {
		advice += c.redeclareGround(ground) // a token in router.colors, a gradient in router.gradients (gradient.go)
	}
	return advice + "."
}

// groupTextName is the attribute a group's texts write their colour in, as written (medium:color),
// when they all write it the same; else color.
func groupTextName(g *pairGroup) string {
	name := ""
	for i, h := range g.pairs {
		this := "color"
		if t := h.p.text.intent; t != nil && t.attr != nil {
			this = t.attr.Name
		}
		if i > 0 && this != name {
			return "color"
		}
		name = this
	}
	return name
}

// groupText is a group's texts each in one colour, in place of the colour that falls short.
func groupText(g *pairGroup, v string) *swaps {
	s := &swaps{intents: map[*paintIntent]map[string]string{}, defaults: map[*paint]string{}}
	for _, h := range g.pairs {
		if t := h.p.text; t.intent != nil {
			if s.intents[t.intent] == nil {
				s.intents[t.intent] = map[string]string{}
			}
			s.intents[t.intent][t.value] = v
		} else {
			s.defaults[t.owner] = v
		}
	}
	return s
}

// groupPasses reports whether a swap makes every pair of a group pass under every condition and mode,
// and the worst ratio among them in each mode.
func (c *compiler) groupPasses(g *pairGroup, s *swaps) (map[stylesheet.Mode]float64, bool) {
	worst := map[stylesheet.Mode]float64{}
	for _, h := range g.pairs {
		ok, each := c.passes(h.f, h.conds, s, h.icon)
		if !ok {
			return nil, false
		}
		for m, r := range each {
			if w, seen := worst[m]; !seen || r < w {
				worst[m] = r
			}
		}
	}
	return worst, true
}

// ratiosText is a suggestion's worst ratio in each mode, " (21.00:1 sáng, 21.00:1 tối)"; "" for none.
func (c *compiler) ratiosText(worst map[stylesheet.Mode]float64) string {
	var ratios []string
	for _, mode := range c.table.Palette().Modes() {
		if r, measured := worst[mode]; measured {
			ratios = append(ratios, ratioText(r, 0)+":1 "+mode.Word())
		}
	}
	if len(ratios) == 0 {
		return ""
	}
	return " (" + strings.Join(ratios, ", ") + ")"
}

// overshootSteps is how far one X+N may go past what a mode needs before the suggestion says so: 20, four
// steps of 5 (decision of 13/10/2026, option B).
const overshootSteps = 20

// overshoots reports whether a suggestion X+amount goes far enough past a mode's need (X+need) for the
// note: overshootSteps or more.
func overshoots(amount, need int) bool {
	return amount-need >= overshootSteps
}

// overshootNote is the note after a suggestion of one element's pair (reportContrast), each mode's step
// measured with the suggestion's own swap in that mode alone. owner is the element the suggestion is
// written on: a badge there starts the steps from X+5 (overshoot).
func (c *compiler) overshootNote(f, owner *paint, conds []cond, icon bool, t colourTry) string {
	if t.with == nil {
		return ""
	}
	badge := owner != nil && indexOfWord(owner.classes, "badge") >= 0
	return c.overshoot(t.value, t.write, badge, func(v string, m stylesheet.Mode) (bool, float64) {
		passes, worst := c.passesIn(f, conds, t.with(v), icon, m)
		return passes, worst[m]
	})
}

// groupOvershootNote is the note after a group line's suggestion (groupAdvice): each mode's step the
// least that reads for every pair of the group in that mode alone, its ratio the worst of them; write is
// the suggestion as the line writes it (color="brand+35" cho chữ trên nó).
func (c *compiler) groupOvershootNote(g *pairGroup, value, write string) string {
	badge := false
	for _, h := range g.pairs {
		if owner := h.p.text.owner; owner != nil && indexOfWord(owner.classes, "badge") >= 0 {
			badge = true
		}
	}
	return c.overshoot(value, write, badge, func(v string, m stylesheet.Mode) (bool, float64) {
		s := groupText(g, v)
		worst, measured := 0.0, false
		for _, h := range g.pairs {
			passes, each := c.passesIn(h.f, h.conds, s, h.icon, m)
			if !passes {
				return false, 0
			}
			if r, ok := each[m]; ok && (!measured || r < worst) {
				worst, measured = r, true
			}
		}
		return true, worst
	})
}

// overshoot is the note said after a suggestion X+N (one step for both modes: the grammar) that goes
// overshootSteps or more past the smallest step that reads in one mode (reads, in that mode alone): what
// X+N is in that mode (its hex, and "gần trắng", "gần đen", "chỉ còn N% sắc của X" when its lightness or
// chroma says so: overshootLook), the step that mode needs, then a role of the site with the smallest step
// of each mode as its pair: router.colors({ "X.text": { light, dark } }), and the attribute as the
// suggestion writes it with that role. What the role is for is said only as far as the colours show it
// (decision of 13/10/2026, option B, worded by what is measured):
//   - "Muốn giữ sắc ở cả hai chế độ" when X+N has lost its hue in a mode told (overshootLook says so)
//     and the role keeps it in both (overshootLook says nothing of either of its colours);
//   - else "Muốn mỗi chế độ chỉ lấy bậc nó cần": X+N there is only stronger than that mode needs (light's
//     X+N only darker), or the role itself is pale or dark in a mode, which is then said with the step
//     that mode needs ("ở chế độ tối danger.text vẫn gần trắng: tối cần tới danger+35").
//
// On a badge (badge), X alone is the badge's own pill (its text the engine's X+n, BadgeTint), not X, so
// the steps tried start from X+5 there. "" for any other suggestion: an operator other than +, an alpha,
// one mode only, or no mode that far below.
func (c *compiler) overshoot(value, write string, badge bool, reads func(v string, m stylesheet.Mode) (bool, float64)) string {
	v, ok := stylesheet.SplitColour(value)
	palette := c.table.Palette()
	if !ok || v.Op != '+' || v.Translucent || v.Fixed() || len(palette.Modes()) < 2 {
		return ""
	}
	start := 0
	if _, _, pill := c.table.BadgeTint(v.Base); pill && badge {
		start = 5
	}
	need := map[stylesheet.Mode]int{}
	ratio := map[stylesheet.Mode]float64{}
	for _, m := range palette.Modes() {
		need[m] = v.Amount
		for n := start; n < v.Amount; n += 5 {
			if passes, worst := reads(stepOf(v.Base, n), m); passes {
				need[m], ratio[m] = n, worst
				break
			}
		}
	}
	var over []string
	lost := false // X+N has lost its hue in a mode told (overshootLook says so there)
	for _, m := range palette.Modes() {
		if !overshoots(v.Amount, need[m]) {
			continue
		}
		painted, _ := palette.Paint(value, m)
		is := painted.Hex()
		if look := overshootLook(palette, v.Base, painted, m); look != "" {
			is += ", " + look
			lost = true
		}
		over = append(over, "ở chế độ "+m.Word()+" nó là "+is+", mà "+m.Word()+" chỉ cần "+stepOf(v.Base, need[m])+" ("+ratioText(ratio[m], 0)+":1)")
	}
	if len(over) == 0 {
		return ""
	}
	role := v.Base + ".text"
	pair := map[stylesheet.Mode]stylesheet.RGBA{}
	var pale []string // the role's own colour in a mode, when overshootLook says something of it: even the role
	for _, m := range palette.Modes() {
		pair[m], _ = palette.Paint(stepOf(v.Base, need[m]), m)
		if look := overshootLook(palette, v.Base, pair[m], m); look != "" {
			pale = append(pale, "ở chế độ "+m.Word()+" "+role+" vẫn "+look+": "+m.Word()+" cần tới "+stepOf(v.Base, need[m]))
		}
	}
	why := "Muốn giữ sắc ở cả hai chế độ"
	if !lost || len(pale) > 0 {
		why = "Muốn mỗi chế độ chỉ lấy bậc nó cần"
	}
	note := " " + value + " là một bậc cho cả hai chế độ: " + strings.Join(over, "; ") + ". " + why +
		" thì khai một vai trò riêng: router.colors({ \"" + role + "\": { light: \"" + pair[stylesheet.LightMode].Hex() +
		"\", dark: \"" + pair[stylesheet.DarkMode].Hex() + "\" } }) rồi viết " + strings.Replace(write, "\""+value+"\"", "\""+role+"\"", 1)
	if palette.Has(role) {
		note += " (" + role + " đã có trong bảng màu: đặt tên khác)"
	}
	if len(pale) > 0 {
		note += "; " + strings.Join(pale, "; ")
	}
	return note + "."
}

// stepOf writes X+n as the author would, X alone for 0.
func stepOf(base string, n int) string {
	if n == 0 {
		return base
	}
	return base + "+" + strconv.Itoa(n)
}

// overshootLook says what a colour that went past what its mode needs looks like there, when it shows:
// "trắng hẳn" or "đen hẳn" for #ffffff and #000000; else "gần trắng" at an OKLab lightness of 0.85 or
// more, "gần đen" at 0.25 or less, and "chỉ còn N% sắc của X" when its chroma is under 40% of its token's
// in that mode; joined by commas, "" when none holds.
func overshootLook(palette *stylesheet.Palette, base string, c stylesheet.RGBA, m stylesheet.Mode) string {
	switch c.Hex() {
	case "#ffffff":
		return "trắng hẳn"
	case "#000000":
		return "đen hẳn"
	}
	var words []string
	lightness, chroma := stylesheet.OklabLightnessChroma(c)
	switch {
	case lightness >= 0.85:
		words = append(words, "gần trắng")
	case lightness <= 0.25:
		words = append(words, "gần đen")
	}
	if token, ok := palette.Paint(base, m); ok {
		if _, own := stylesheet.OklabLightnessChroma(token); own > 0 && chroma < 0.4*own {
			words = append(words, "chỉ còn "+strconv.Itoa(int(math.Round(100*chroma/own)))+"% sắc của "+base)
		}
	}
	return strings.Join(words, ", ")
}

// fixedNames are the fixed colours as a message says them.
var fixedNames = map[string]string{"white": "trắng", "black": "đen"}

// fixedWord is the word of the fixed colour a ground's top layer is written in, solid, as a background
// (white: trắng, black: đen): the same in every mode while the text follows the mode. "" for any other
// ground: a token, a translucent white over something, the stand-ins for content the page cannot know.
func fixedWord(top shade) string {
	if writtenSource(top) == nil || top.intent.name != "background" {
		return ""
	}
	v, ok := stylesheet.SplitColour(top.value)
	if !ok || !v.Fixed() || !v.Plain() {
		return ""
	}
	if word := fixedNames[v.Base]; word != "" {
		return word
	}
	return v.Base
}

// fixedText reports whether a text colour is a fixed one (white, black): the same in every mode.
func fixedText(value string) bool {
	v, ok := stylesheet.SplitColour(value)
	return ok && v.Fixed()
}

// otherFixed is the fixed colour that reads on a fixed one: black on white, white on black.
func otherFixed(value string) string {
	if v, ok := stylesheet.SplitColour(value); ok && v.Base == "black" {
		return "white"
	}
	return "black"
}

// surfacesFor are the surfaces that follow the mode, in the order to try in place of a fixed
// background: the page's canvas first on <page>, a raised paper first anywhere else.
func surfacesFor(source *paintIntent) []string {
	if source.node != nil && source.node.Tag == "page" {
		return []string{"canvas", "paper"}
	}
	return []string{"paper", "canvas"}
}

// fixedSurfaceAdvice is the advice for a pair whose ground is a fixed colour (white, black) written as a
// background, under text that follows the mode, when no text colour suggested passes: the surface that
// follows the mode too (surfacesFor), or the other fixed colour for the text, each only when it passes
// everywhere the element's pair occurs, with its ratios — never a token to declare. "" for any other
// pair: its own advice stands.
func (c *compiler) fixedSurfaceAdvice(f *paint, conds []cond, p pairing, icon bool) string {
	top := p.ground.top
	word := fixedWord(top)
	if word == "" || fixedText(p.text.value) {
		return ""
	}
	source := writtenSource(top)
	var parts []string
	for _, v := range surfacesFor(source) {
		s := &swaps{intents: map[*paintIntent]map[string]string{}}
		surfaceSwap(s, top, source, v)
		if ok, worst := c.passes(f, conds, s, icon); ok {
			write := "viết " + source.attr.Name + "=\"" + v + "\""
			if p.text.intent != nil && p.text.intent.attr != nil || top.intent.attr == nil {
				// the line stands at the text's colour, or at the element pinned over the page
				write += " ở " + attrPlace(source.node, source.attr)
			}
			parts = append(parts, write+c.ratiosText(worst))
			break
		}
	}
	other := otherFixed(top.value)
	s := &swaps{intents: map[*paintIntent]map[string]string{}, defaults: map[*paint]string{}}
	name := "color"
	if p.text.intent != nil {
		s.intents[p.text.intent] = map[string]string{p.text.value: other}
		if p.text.intent.attr != nil {
			name = p.text.intent.attr.Name
		}
	} else {
		s.defaults[p.text.owner] = other
	}
	if ok, worst := c.passes(f, conds, s, icon); ok {
		parts = append(parts, "giữ nền "+word+" và viết "+name+"=\""+other+"\""+c.ratiosText(worst))
	}
	message := " Nền " + top.value + " là màu cố định"
	if len(c.table.Palette().Modes()) > 1 {
		message += ", không đổi theo chế độ như chữ"
	}
	if len(parts) == 0 {
		return message + ": đổi nền " + top.value + " (background, material)."
	}
	return message + ": " + strings.Join(parts, ", hoặc ") + "."
}

// measureOwn measures what an element shows besides its text children: the words a hint and an error
// show (written, from {{ }}, or a validation's), the text typed in a field or chosen in a select, an
// icon with alt.
func (c *compiler) measureOwn(n *Node, f *paint) {
	switch {
	case n.Tag == "hint", n.Tag == "error", n.Tag == "textarea", n.Tag == "select":
		c.measure(f, false)
	case n.Tag == "input" && !noTextInput[f.inputType]:
		c.measure(f, false)
	case n.Tag == "icon":
		if alt, ok := n.Attr("alt"); ok && strings.TrimSpace(alt) != "" {
			c.measure(f, true)
		}
	}
}

// placeWidth is the width a place written at a call starts at, from its prefix (0 every screen).
func (c *compiler) placeWidth(chain string) int {
	if chain == "" {
		return 0
	}
	pre, _ := parsePrefix(chain, c.site.target())
	return narrowest(pre.widths)
}
