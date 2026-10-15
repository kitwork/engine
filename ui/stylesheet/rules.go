package stylesheet

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Rules is one page's rule set: the rule of every class any branch of the page's HTML can carry,
// recorded by Table.Class and Table.Domain. A class exists only because its rule was recorded, so the
// page's files hold every class its HTML can carry. The zero value is ready to use; safe for
// concurrent use.
type Rules struct {
	mu      sync.Mutex
	rules   map[string]rule
	classes map[string]bool
	weights map[int]bool
	// moves are the properties each class with a state animates (its transition), and scrolled the
	// classes among them that hold while the page is scrolled: what Transition merges for one element
	moves    map[string][]string
	scrolled map[string]bool
	// materials says a class of the page is a material (material.go): its background classes then hand
	// their colour over, as written in --kitwork-background (a glass's fallback, none) and without its
	// /N in --kitwork-glass (the glass). A page without one has the bytes it always had.
	materials bool
	// gradientText says a class of the page paints its text with a gradient (gradients.go): every colour
	// class of color then gives the letters their fill back where it holds (Rules.CSS). A page without
	// one has the bytes it always had.
	gradientText bool
	// gradients are the classes that paint a gradient behind an element: an element that carries one and
	// a colour background for a later condition gets that colour's background-image:none (TakeBack)
	gradients map[string]bool
	// pairs are the custom properties of the page whose value differs by mode, by name: the { light,
	// dark } stops of its gradients, its stronger colours (X+N, --kitwork-strong-N-X) and the engine
	// colours it uses (--kitwork-badge-X, --kitwork-code-X, --kitwork-headline, --kitwork-inverse:
	// engine_colour.go); set on :root in light and again for the dark look, before the layers (Rules.CSS)
	pairs map[string]pairValue
	// ground is the gradient <page> paints as the page's backdrop ("" for none): what the document's
	// theme-color is made of (Backdrop)
	ground string
	// focus is the keyboard focus ring a page backdrop asks for, light then dark ("" keeps the
	// palette's): set on :root after colors.<hash>.css (Rules.CSS, Table.Backdrop)
	focus [2]string
}

// pairValue is one custom property of the page with a value per mode (a gradient's { light, dark }
// stop, a stronger colour, an engine colour): its light colour, and its dark one ("" when the palette has
// no dark look), each #rrggbb.
type pairValue struct{ name, light, dark string }

// rule is one CSS rule and its place in the fixed order.
type rule struct {
	layer int // 0 page, 1 show
	state int // 0 none, then open, active, scrolled, hover, focus, pressed, disabled
	width int // 0 none, then small … huge
	// origin is the width the class's own condition starts at; below width when the rule is a step a
	// narrower class carries to a wider screen (a heading's size growing from small)
	origin       int
	rank         int    // the attribute's place in the table
	value        string // the value as written
	class        string
	part         int // -1 the unconditional transition, 0 the class's own rule, 1… the scoped ones
	selector     string
	declarations string
	wrap         string // the at-rule the rule holds under inside its condition (scopedRule.wrap)
	handover     string // what a background class hands a material, on a page with one (Rules.CSS)
	// colour is the attribute of a colour class's own rule, background or color ("" for any other rule):
	// a colour background takes back the gradient of the element it shares (TakeBack), a colour of text
	// gives the letters their fill back on a page with gradient text (Rules.CSS)
	colour string
}

const (
	layerPage = iota
	layerShow
)

var layerNames = []string{"page", "show"}

func (r *Rules) record(a attribute, rank int, c condition, value, class string, read reading) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rules == nil {
		r.rules = map[string]rule{}
		r.classes = map[string]bool{}
		r.weights = map[int]bool{}
		r.moves = map[string][]string{}
		r.scrolled = map[string]bool{}
		r.gradients = map[string]bool{}
	}
	r.addPairs(read.pairs)
	layer := layerPage
	if a.show {
		layer = layerShow
	}
	width := indexOf(widthWords, c.width) + 1
	selector := "." + escape(class)
	add := func(x rule) {
		key := strconv.Itoa(x.layer) + "|" + strconv.Itoa(x.state) + "|" + strconv.Itoa(x.width) + "|" + x.selector + "|" + x.wrap
		r.rules[key] = x
	}
	states := c.states
	if len(states) == 0 {
		states = []string{""}
	}
	colour := ""
	if !read.gradient && (a.name == "background" || a.name == "color") {
		colour = a.name
	}
	for _, s := range states {
		state := indexOf(stateWords, s) + 1
		add(rule{layer: layer, state: state, width: width, origin: width, rank: rank, value: value, class: class,
			selector: selector + stateSelector[s], declarations: read.declarations, handover: read.handover, colour: colour})
		for i, scoped := range read.scoped {
			at := width
			if w := indexOf(widthWords, scoped.width) + 1; w > at {
				at = w // a step that starts at a wider screen than the class's own condition
			}
			add(rule{layer: layer, state: state, width: at, origin: width, rank: rank, value: value, class: class, part: i + 1,
				selector: selector + scoped.selector + stateSelector[s], declarations: scoped.declarations, wrap: scoped.wrap})
		}
	}
	if a.name == "material" {
		r.materials = true
	}
	if read.gradient {
		if a.name == "background" {
			r.gradients[class] = true
		} else {
			r.gradientText = true
		}
	}
	if moves := movesOf(c, read); len(moves) > 0 {
		// outside the state, so the change animates both ways
		add(rule{layer: layer, rank: rank, value: value, class: class, part: -1, selector: selector, declarations: transitionOf(moves)})
		r.moves[class] = moves
	}
	if indexOf(c.states, "scrolled") >= 0 {
		// whether or not it animates anything (scrolled:align sets only variables), it reads the flag
		r.scrolled[class] = true
	}
	if read.weight != 0 {
		r.weights[read.weight] = true
	}
	r.classes[class] = true
}

// movesOf is what a change of state animates for one class: scale and rotate together for a class
// that sets either under a state (a press, an opening), and for a class that holds while the page is
// scrolled, exactly the properties it sets — its own rule's and its scoped rules' (a scrolled header's
// box-shadow and background-color, never all). A CSS variable is not one: it does not animate. Nothing
// for a class without a state.
func movesOf(c condition, read reading) []string {
	if len(c.states) == 0 || read.gradient {
		return nil // a gradient changes at once: no transition for it (gradients.go)
	}
	moves := append([]string(nil), read.transition...)
	if indexOf(c.states, "scrolled") < 0 {
		return moves
	}
	declarations := []string{read.declarations}
	for _, scoped := range read.scoped {
		declarations = append(declarations, scoped.declarations)
	}
	for _, block := range declarations {
		for _, declaration := range strings.Split(block, ";") {
			property, _, _ := strings.Cut(declaration, ":")
			property = strings.TrimSpace(property)
			if property != "" && !strings.HasPrefix(property, "--") && indexOf(moves, property) < 0 {
				moves = append(moves, property)
			}
		}
	}
	return moves
}

// transitionOf is the declaration that animates the properties, each over stateTransition.
func transitionOf(moves []string) string {
	parts := make([]string, len(moves))
	for i, property := range moves {
		parts[i] = property + " " + stateTransition
	}
	return "transition:" + strings.Join(parts, ",")
}

// Transition records what the classes of ONE element animate together. Each class with a state has
// its own transition rule (record), and CSS keeps one transition per element: an element whose
// scrolled: classes change several properties (scrolled:shadow="small" scrolled:background="paper")
// would animate only the property of the class written last in the stylesheet. So the element gets
// one more rule, for exactly its classes: every property they animate, nothing else. Its selector is
// the element's classes, the first one weighing and the others inside :where(), so it weighs one
// class like every page rule; it comes after every class's own transition rule, a larger set after a
// smaller (an element's own set is the largest that matches it, so it wins). Nothing is recorded when
// one class's own rule already says it all: a single class, or classes that animate the same
// properties (scale and rotate together, a shadow written for two widths).
func (r *Rules) Transition(classes []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var moving []string
	scrolled := false
	for _, class := range classes {
		if _, ok := r.moves[class]; ok && indexOf(moving, class) < 0 {
			moving = append(moving, class)
			scrolled = scrolled || r.scrolled[class]
		}
	}
	if len(moving) < 2 || !scrolled {
		return
	}
	sort.Strings(moving)
	var moves []string
	same := true
	for _, class := range moving {
		own := r.moves[class]
		if strings.Join(own, ",") != strings.Join(r.moves[moving[0]], ",") {
			same = false
		}
		for _, property := range own {
			if indexOf(moves, property) < 0 {
				moves = append(moves, property)
			}
		}
	}
	if same {
		return
	}
	selector := "." + escape(moving[0]) + ":where("
	for _, class := range moving[1:] {
		selector += "." + escape(class)
	}
	selector += ")"
	key := strconv.Itoa(layerPage) + "|0|0|" + selector
	r.rules[key] = rule{layer: layerPage, rank: len(table), value: strconv.Itoa(len(moving)), class: selector, part: -1,
		selector: selector, declarations: transitionOf(moves)}
}

// TakeBack records, for ONE element, that a colour background written for a later condition takes the
// element's gradient away, as a colour over a colour does: background="glow" medium:background="paper"
// is paper from medium (an unprefixed colour is never later than a gradient). For each colour
// background class of the element written with a condition, under each of its own conditions, a rule
// background-image:none for an element that also carries one of the element's gradient background
// classes: the colour's own selector with :where(.background-glow) after it, so it weighs one class
// like every page rule, right after the class's own rules. Only that element: the same colour class on
// another element keeps the image it has (a switch's thumb is reboot.css's background-image). Nothing
// for an element without a gradient background.
func (r *Rules) TakeBack(classes []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var gradients []string
	for _, class := range classes {
		if r.gradients[class] && indexOf(gradients, class) < 0 {
			gradients = append(gradients, class)
		}
	}
	if len(gradients) == 0 {
		return
	}
	sort.Strings(gradients)
	carries := ":where("
	for i, class := range gradients {
		if i > 0 {
			carries += ","
		}
		carries += "." + escape(class)
	}
	carries += ")"
	var back []rule
	for _, x := range r.rules {
		if x.colour == "background" && x.part == 0 && (x.state > 0 || x.width > 0) && indexOf(classes, x.class) >= 0 {
			x.selector += carries
			x.declarations, x.handover, x.colour, x.part = "background-image:none", "", "", takeBackPart
			back = append(back, x)
		}
	}
	for _, x := range back {
		r.rules[strconv.Itoa(x.layer)+"|"+strconv.Itoa(x.state)+"|"+strconv.Itoa(x.width)+"|"+x.selector+"|"+x.wrap] = x
	}
}

// takeBackPart places a colour's background-image:none (TakeBack) after the class's own rule and its
// scoped ones.
const takeBackPart = 1 << 10

// addPairs keeps the custom properties per mode (gradient pairs, stronger colours, engine colours) for
// the page's stylesheet, one value by name; r.mu is held.
func (r *Rules) addPairs(pairs []pairValue) {
	if len(pairs) == 0 {
		return
	}
	if r.pairs == nil {
		r.pairs = map[string]pairValue{}
	}
	for _, v := range pairs {
		r.pairs[v.name] = v
	}
}

// backdrop records a page's backdrop (Table.Backdrop): the rule of the layer, and, when <page> paints a
// gradient without a prefix, <html>'s ground (html, its declarations; "" for a gradient that fades to
// transparent) with that gradient's pairs, and the focus ring it asks for. Both rules come first in the
// page layer, before every class, the ground before the layer.
func (r *Rules) backdrop(ground, html string, pairs []pairValue, focus [2]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rules == nil {
		r.rules = map[string]rule{}
		r.classes = map[string]bool{}
		r.weights = map[int]bool{}
		r.moves = map[string][]string{}
		r.scrolled = map[string]bool{}
		r.gradients = map[string]bool{}
	}
	r.addPairs(pairs)
	r.focus = focus
	layer := ":where(." + BackdropClass + ")"
	r.rules[strconv.Itoa(layerPage)+"|0|0|"+layer+"|"] = rule{layer: layerPage, rank: -1, part: 1, selector: layer, declarations: backdropLayer}
	if html != "" {
		r.rules[strconv.Itoa(layerPage)+"|0|0|:where(html)|"] = rule{layer: layerPage, rank: -1, selector: ":where(html)", declarations: html}
		r.ground = ground
	}
}

// Backdrop is the gradient <page> paints as the page's backdrop, as written (cirrus, cirrus-70); "" when
// it paints none (Table.Backdrop).
func (r *Rules) Backdrop() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ground
}

// Scrolled reports whether a class of the page holds while the page is scrolled (scrolled:): the page
// needs scrolled.<hash>.js, which keeps the flag those classes read.
func (r *Rules) Scrolled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.scrolled) > 0
}

// escape writes a class as a CSS selector: the characters a .ui class keeps from its source and CSS
// reads as syntax — : + , . / % — are escaped. The HTML carries the class as written.
func escape(class string) string {
	var b strings.Builder
	for i := 0; i < len(class); i++ {
		switch c := class[i]; c {
		case ':', '+', ',', '.', '/', '%':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ordered is the rules in the fixed order: the page layer, then the show layer; inside each the
// unprefixed rules, then the widths ascending; then each state, open < active < scrolled < hover <
// focus < pressed < disabled, widths ascending inside it. Within one condition a step a narrower
// class carries to this width comes first (size="medium" grows at small, small:size="large" is
// written for small: the written one wins there), then the table's order decides (a side of padding
// after padding), then the value; an element's merged transition (Transition) after every class's
// own. Every page rule weighs one class, so this order is the whole contract: a state beats a width
// while it holds, disabled beats hover, hover beats scrolled.
func (r *Rules) ordered() []rule {
	r.mu.Lock()
	list := make([]rule, 0, len(r.rules))
	for _, x := range r.rules {
		list = append(list, x)
	}
	r.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch {
		case a.layer != b.layer:
			return a.layer < b.layer
		case a.state != b.state:
			return a.state < b.state
		case a.width != b.width:
			return a.width < b.width
		case a.origin != b.origin:
			return a.origin < b.origin
		case a.rank != b.rank:
			return a.rank < b.rank
		case a.value != b.value:
			return lessValue(a.value, b.value)
		case a.class != b.class:
			return a.class < b.class
		}
		return a.part < b.part
	})
	return list
}

// lessValue orders numbers by size (gap-2 before gap-10) and words as text, numbers first.
func lessValue(a, b string) bool {
	fa, errA := strconv.ParseFloat(a, 64)
	fb, errB := strconv.ParseFloat(b, 64)
	switch {
	case errA == nil && errB == nil && fa != fb:
		return fa < fb
	case errA == nil && errB != nil:
		return true
	case errA != nil && errB == nil:
		return false
	}
	return a < b
}

// media is the condition a rule's block holds under: a width is min-width; hover holds only where a
// pointer can hover, so a phone does not keep the colour after a tap.
func media(state, width int) string {
	var parts []string
	if width > 0 {
		parts = append(parts, "(min-width:"+widthPixels[widthWords[width-1]]+")")
	}
	if state > 0 && stateWords[state-1] == "hover" {
		parts = append(parts, "(hover:hover)")
	}
	return strings.Join(parts, " and ")
}

// CSS is the page's stylesheet: the layer statement, then @layer page{…} and @layer show{…} in the
// fixed order, one rule a line; a rule that holds under an at-rule of its own inside its condition
// (a glass's fallback) is that at-rule around it, on the same line. On a page with a material each
// background class also hands its colour over, as --kitwork-background and --kitwork-glass. The
// generator writes it compact itself, so development and production serve the same bytes. Empty when
// the page records no rule: an empty page.css gets no link.
func (r *Rules) CSS() []byte {
	list := r.ordered()
	if len(list) == 0 {
		return nil
	}
	r.mu.Lock()
	materials := r.materials
	gradientText := r.gradientText
	pairs := make([]pairValue, 0, len(r.pairs))
	for _, name := range sortedKeys(r.pairs) {
		pairs = append(pairs, r.pairs[name])
	}
	focus := r.focus
	r.mu.Unlock()
	var b strings.Builder
	b.WriteString(LayerStatement + "\n")
	writePairs(&b, pairs)
	writeFocus(&b, focus)
	for layer, name := range layerNames {
		started := false
		open := ""
		for _, x := range list {
			if x.layer != layer {
				continue
			}
			if !started {
				b.WriteString("@layer " + name + "{\n")
				started = true
			}
			if m := media(x.state, x.width); m != open {
				if open != "" {
					b.WriteString("}\n")
				}
				if m != "" {
					b.WriteString("@media " + m + "{\n")
				}
				open = m
			}
			declarations := x.declarations
			if materials && x.handover != "" {
				declarations += ";" + x.handover
			}
			if gradientText && x.colour == "color" {
				// the letters take their colour again, inside gradient text too (the fill is inherited)
				declarations += ";-webkit-text-fill-color:currentcolor"
			}
			if x.wrap != "" {
				b.WriteString(x.wrap + "{" + x.selector + "{" + declarations + "}}\n")
				continue
			}
			b.WriteString(x.selector + "{" + declarations + "}\n")
		}
		if open != "" {
			b.WriteString("}\n")
		}
		if started {
			b.WriteString("}\n")
		}
	}
	return []byte(b.String())
}

// writePairs writes the page's custom properties per mode (pairValue) as colors.<hash>.css writes
// the tokens: on :root in light, then the dark values under :root[data-theme=dark] for screens only, so
// print stays light. Custom properties only, so unlayered, right after the layer statement. Nothing for
// a page without a pair.
func writePairs(b *strings.Builder, pairs []pairValue) {
	if len(pairs) == 0 {
		return
	}
	b.WriteString(":root{")
	dark := false
	for i, v := range pairs {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(v.name + ":" + v.light)
		dark = dark || v.dark != ""
	}
	b.WriteString("}\n")
	if !dark {
		return
	}
	b.WriteString("@media screen{:root[data-theme=dark]{")
	first := true
	for _, v := range pairs {
		if v.dark == "" {
			continue
		}
		if !first {
			b.WriteByte(';')
		}
		first = false
		b.WriteString(v.name + ":" + v.dark)
	}
	b.WriteString("}}\n")
}

// writeFocus writes the keyboard focus ring a page backdrop asks for (Table.Backdrop) as colors.<hash>.css
// writes the palette's: --kitwork-focus on :root for light, under :root[data-theme=dark] for screens
// for dark. Unlayered and after colors.<hash>.css in the head, so each wins over the palette's ring of
// its mode and leaves the other mode's alone. Nothing for a mode whose ring reads.
func writeFocus(b *strings.Builder, focus [2]string) {
	if focus[0] != "" {
		b.WriteString(":root{--kitwork-focus:var(--" + focus[0] + ")}\n")
	}
	if focus[1] != "" {
		b.WriteString("@media screen{:root[data-theme=dark]{--kitwork-focus:var(--" + focus[1] + ")}}\n")
	}
}

// File is the page's stylesheet as the engine serves it, page.<hash>.css. Name is "" when the page
// records no rule: no file, no link.
func (r *Rules) File() File {
	css := r.CSS()
	if len(css) == 0 {
		return File{Role: "page"}
	}
	return newFile("page", css)
}

// Classes lists every class recorded, sorted.
func (r *Rules) Classes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sortedKeys(r.classes)
}

// Has reports whether a class was recorded: its rule is in the page's stylesheet.
func (r *Rules) Has(class string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.classes[class]
}

// Weights lists the font weights the page's classes set (weight="semibold" → 600), ascending, so
// fonts.<hash>.css can ship the weights the page uses; the weights of the elements before anything is
// written are ElementWeights.
func (r *Rules) Weights() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int, 0, len(r.weights))
	for w := range r.weights {
		out = append(out, w)
	}
	sort.Ints(out)
	return out
}
