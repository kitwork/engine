package ui

import (
	"strconv"
	"strings"

	"github.com/kitwork/engine/ui/stylesheet"
)

// Gradients in the compiler (decision of 12/10/2026, stylesheet/gradients.go). The contrast check reads
// a gradient as its eleven samples (stylesheet.Palette.GradientSamples), each a colour a reader may see
// behind the text or in it: text on a gradient background is measured against every sample, composited
// over what lies beneath it in light and in dark; gradient text is measured sample by sample against
// its ground. And the compile refuses a gradient the page would not draw as written: gradient text on
// an element that paints a background (the letters clip the background too), on an icon (a mask painted
// by its background), and a glass over a gradient background (a glass is made of a colour).

// painted is one colour a value paints in a mode, and where it lies along a gradient, as a message says
// it (" tại 40%"); "" for a colour.
type painted struct {
	rgba stylesheet.RGBA
	at   string
}

// paintsOf is a colour value painted in a mode: one colour, or a gradient's eleven samples, 0% to 100%
// along it; none for a value the palette cannot name.
func (c *compiler) paintsOf(v string, m stylesheet.Mode) []painted {
	palette := c.table.Palette()
	if samples, ok := palette.GradientSamples(strings.TrimSpace(v), m); ok {
		out := make([]painted, len(samples))
		for i, sample := range samples {
			out[i] = painted{rgba: sample, at: " tại " + strconv.Itoa(i*100/(len(samples)-1)) + "%"}
		}
		return out
	}
	if rgba, ok := palette.Paint(v, m); ok {
		return []painted{{rgba: rgba}}
	}
	return nil
}

// isGradient reports whether a value is a gradient the page's palette names (the site's or a standard
// one).
func (c *compiler) isGradient(v string) bool {
	_, ok := c.table.Palette().Gradient(strings.TrimSpace(v))
	return ok
}

// redeclareGround is how a contrast message says to change a ground in the router, after "đổi nền X
// (background, material)": a token is declared again in router.colors, a site's gradient changed in
// router.gradients; a standard gradient is changed only by writing another, so nothing more is said.
func (c *compiler) redeclareGround(ground string) string {
	if !c.isGradient(ground) {
		return " hay khai lại " + ground + " trong router.colors"
	}
	if base, _, _, stepped := stylesheet.SplitStep(ground); stepped {
		ground = base // a gradient written lighter or stronger (cirrus-80, hero+20) is changed where it is
	}
	if _, standard := stylesheet.StandardGradient(ground); standard {
		return ""
	}
	return " hay sửa " + ground + " trong router.gradients"
}

// paintsGradient reports whether an attribute may paint a gradient: one of its values is one.
func (c *compiler) paintsGradient(s *swaps, pi *paintIntent) bool {
	for _, v := range s.values(pi) {
		if c.isGradient(v) {
			return true
		}
	}
	return false
}

// backgroundLayer is what a background paints behind an element's content at a condition (layerOf). A
// colour is itself. A gradient is its samples, each over what the element paints beneath the image: a
// gradient class sets only background-image, so the colour of the background the cascade leaves at that
// condition shows through it (one written for the same condition or an earlier one), else the surface
// its kind has (a card's paper); over nothing of its own, a translucent sample is composited over what
// lies behind the element (backdrop).
func (c *compiler) backgroundLayer(f *paint, k cond, m stylesheet.Mode, s *swaps, w *paintIntent) ([]shade, bool) {
	plain := func(v string) string { return v }
	if !c.paintsGradient(s, w) {
		return c.shades(w, f, m, s, plain)
	}
	kept := f.written
	var colours []*paintIntent
	for _, pi := range kept {
		if pi.name == "background" && !c.paintsGradient(s, pi) && !s.dropped(pi) {
			colours = append(colours, pi)
		}
	}
	f.written = colours
	under := f.winner(k, "background")
	f.written = kept
	var beneath []shade
	var ok bool
	if under != nil {
		beneath, ok = c.shades(under, f, m, s, plain)
	} else {
		beneath, ok = c.surface(f, k, m, s)
	}
	if !ok {
		return nil, false // a colour from data beneath: what shows through cannot be known
	}
	values := s.values(w)
	var out []shade
	for i, v := range values {
		for _, p := range c.paintsOf(v, m) {
			top := shade{rgba: p.rgba, value: v, intent: w, owner: f, what: v + p.at, picks: w.choices(i)}
			if len(beneath) == 0 || top.rgba.Opaque() {
				out = append(out, top)
				continue
			}
			for _, b := range beneath {
				if !agree(top.picks, b.picks) {
					continue // never on screen together
				}
				over := top
				over.rgba = top.rgba.Over(b.rgba)
				over.what = top.what + " phủ trên " + b.what
				over.picks = append(append([]choice(nil), top.picks...), b.picks...)
				out = append(out, over)
			}
		}
	}
	return out, true
}

// ---- what a gradient written on an element would not draw as written ----

// writesGradient reports whether an attribute is written as a gradient: a value spelled out in the
// source, written plainly or as a literal a {{ }} may render (the arm of a ternary: {{ on ? 'headline'
// : 'ink' }} renders color-headline), never data alone (a {{ }} that spells out nothing takes the
// declared tokens only: no class makes it a gradient).
func (c *compiler) writesGradient(pi *paintIntent) bool {
	return c.gradientWritten(pi) != ""
}

// gradientWritten is the gradient an attribute is written as (writesGradient): its value, or the first
// literal of its {{ }} that is one; "" for none.
func (c *compiler) gradientWritten(pi *paintIntent) string {
	if pi.attr == nil {
		return ""
	}
	for _, v := range pi.values {
		if c.isGradient(v) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// takeBack hands the classes an element may carry — those written, and each a {{ }} may render — to
// the page's rules (stylesheet.Rules.TakeBack): a colour background written for a later condition
// takes back the gradient of this element, and of no other element with the same colour class.
func (c *compiler) takeBack(p *attrPlan) {
	classes := append([]string(nil), p.classes...)
	for _, d := range p.dynClasses {
		for _, class := range d.classes {
			classes = append(classes, class)
		}
	}
	c.rules.TakeBack(classes)
}

// glassy reports whether a material attribute may make the element glass: a glass written, or a value
// from data, which may be one.
func glassy(pi *paintIntent) bool {
	for _, v := range pi.values {
		if _, glass := stylesheet.GlassOf(v); glass {
			return true
		}
	}
	return false
}

// checkGradients refuses what a gradient written on an element would not draw as written (decision of
// 12/10/2026), at the attribute to change:
//   - gradient text (color="headline") on an element that paints a background — one written on it, at
//     any condition, a glass, the background the engine gives a frame pinned to an edge, or the surface
//     its kind has (a card's paper, a badge's fill): the text's gradient is clipped to the letters
//     (background-clip:text) and the background is clipped with it. The text goes in a <text> of its
//     own inside. A background and gradient text written for different conditions still meet: widths
//     are min-width and states hold together, and the clip stays wherever the gradient's class holds;
//   - gradient text on an icon: an icon is a mask painted by its background, so its colour cannot be a
//     gradient (the icon would vanish); background paints it with one;
//   - a glass over a gradient background, at a moment both win the cascade (gradientUnderGlass): a glass
//     is made of the frame's background colour, and the gradient would be drawn over it.
func (c *compiler) checkGradients(n *Node, el *element, p *attrPlan) error {
	var texts, grounds, glasses []*paintIntent
	for _, pi := range p.paints {
		switch pi.name {
		case "color":
			if c.writesGradient(pi) {
				texts = append(texts, pi)
			}
		case "background":
			grounds = append(grounds, pi)
		case "material":
			if glassy(pi) {
				glasses = append(glasses, pi)
			}
		}
	}
	for _, t := range texts {
		v, written := c.gradientWritten(t), strings.TrimSpace(t.attr.Value)
		text, lead := t.attr.Name+"=\""+written+"\"", ""
		if written != v {
			lead = " (nhánh " + v + ")" // a literal of a {{ }}: the arm that renders the gradient
		}
		if n.Tag == "icon" {
			return errAttr(n, t.attr, "%s%s trên <icon>: icon là một mặt nạ tô bằng nền của nó, chữ gradient không tô được icon (icon biến mất); tô icon bằng gradient là %s",
				text, lead, strings.TrimSuffix(t.attr.Name, "color")+"background=\""+written+"\"")
		}
		background, inside := "", "<"+n.Tag+">"
		switch {
		case len(grounds) > 0:
			g := grounds[0]
			written := g.attr.Name + "=\"" + strings.TrimSpace(g.attr.Value) + "\""
			background = written + " trên cùng phần tử"
			inside = "<" + n.Tag + " " + written + ">"
		case len(glasses) > 0:
			g := glasses[0]
			written := g.attr.Name + "=\"" + strings.TrimSpace(g.attr.Value) + "\""
			background = "kính (" + written + ") trên cùng phần tử"
			inside = "<" + n.Tag + " " + written + ">"
		case p.pinned && !writesAttribute(n, "background"):
			background = "nền engine cho khung ghim vào mép (pin, để nội dung cuộn bên dưới không lộ qua)"
		default:
			if surface := surfaceWord(n, el); surface != "" {
				background = surface
			}
		}
		if background == "" {
			continue
		}
		return errAttr(n, t.attr, "%s%s là chữ gradient: gradient được cắt theo nét chữ (background-clip:text), nên %s cũng bị cắt theo nét chữ, không còn là nền. "+
			"Để chữ gradient trên một <text> riêng bên trong: %s<text %s>…</text></%s>", text, lead, background, inside, text, n.Tag)
	}
	if glass, gradient := gradientUnderGlass(c, p.paints); glass != nil {
		g := gradient.attr.Name + "=\"" + strings.TrimSpace(gradient.attr.Value) + "\""
		said := g
		if v := c.gradientWritten(gradient); v != strings.TrimSpace(gradient.attr.Value) {
			said += " (nhánh " + v + ")"
		}
		return errAttr(n, glass.attr, "%s=\"%s\" đi cùng %s: kính làm từ màu nền của khung, gradient không làm kính được (trang sẽ vẽ gradient đè lên kính). "+
			"Bỏ material, hoặc đặt gradient trên một khung riêng bên trong khung kính: <box %s>…</box>", glass.attr.Name, strings.TrimSpace(glass.attr.Value), said, g)
	}
	return nil
}

// surfaceWord is the background an element's kind paints before anything is written, as a message says
// it ("nền paper của <card>", "nền của <badge>"); "" for a kind that paints none.
func surfaceWord(n *Node, el *element) string {
	var classes []string
	if sharedTag[n.Tag] {
		classes = []string{n.Tag}
	}
	inputType := ""
	if n.Tag == "input" {
		v, _ := n.Attr("type")
		inputType = strings.TrimSpace(v)
	}
	look := stylesheet.LookOf(el.html, classes, inputType, 0)
	switch {
	case look.Tinted:
		return "nền của <" + n.Tag + ">"
	case look.Surface != "" && look.Surface != "none":
		return "nền " + look.Surface + " của <" + n.Tag + ">"
	}
	return ""
}

// gradientUnderGlass is a glass and a gradient background of one element that win the cascade at the
// same moment — a width, and the states that hold then — so the gradient is drawn over the glass; nil
// when there is none. At a moment the material is the latest material class that holds, and the image
// the latest background class that holds (a colour class written for a condition takes the image away).
// Every combination of the states the element writes them for is a moment: states hold together.
func gradientUnderGlass(c *compiler, paints []*paintIntent) (glass, gradient *paintIntent) {
	var materials, backgrounds []*paintIntent
	var states []string
	for _, pi := range paints {
		switch pi.name {
		case "material":
			materials = append(materials, pi)
		case "background":
			backgrounds = append(backgrounds, pi)
		default:
			continue
		}
		for _, s := range pi.states {
			if indexOfWord(states, s) < 0 {
				states = append(states, s)
			}
		}
	}
	if len(materials) == 0 || len(backgrounds) == 0 || len(states) > 10 {
		return nil, nil
	}
	latest := func(list []*paintIntent, width int, held map[string]bool) *paintIntent {
		var best *paintIntent
		var bestKey [2]int
		for _, pi := range list {
			if pi.width > width {
				continue
			}
			state := 0
			if len(pi.states) > 0 {
				state = -1
				for _, s := range pi.states {
					if held[s] && stylesheet.StateIndex(s) > state {
						state = stylesheet.StateIndex(s)
					}
				}
				if state < 0 {
					continue // its state does not hold
				}
			}
			key := [2]int{state, pi.width}
			if best == nil || key[0] > bestKey[0] || key[0] == bestKey[0] && key[1] >= bestKey[1] {
				best, bestKey = pi, key
			}
		}
		return best
	}
	for width := 0; width <= len(stylesheet.Widths()); width++ {
		for set := 0; set < 1<<len(states); set++ {
			held := map[string]bool{}
			for i, s := range states {
				if set&(1<<i) != 0 {
					held[s] = true
				}
			}
			m, b := latest(materials, width, held), latest(backgrounds, width, held)
			if m != nil && b != nil && glassy(m) && c.writesGradient(b) {
				return m, b
			}
		}
	}
	return nil, nil
}

// ---- gradient text an element under it hides ----

// checkHiddenText checks the text an element shows (measure: every text of the page, the words of a
// field, a hint, a code's tokens) against gradient text around it. The letters inherit the gradient's
// transparent fill (-webkit-text-fill-color), and the gradient is clipped to them on the element that
// writes it: an element between that paints a solid ground of its own over it — a background, a glass,
// a pinned frame's, the surface of its kind (a card, a code block) — covers that gradient, and the text
// is not drawn at all; a neutral button's and a field's translucent ink (decision F1) covers nothing, the
// gradient shows through it (coverOf) (decision of 12/10/2026: what would not draw as written is
// an error). An element that writes a colour of its own at every condition (color="ink") gives its
// letters their fill back (its colour class, stylesheet.Rules.CSS) and ends the search. The first one
// found is the page's error (compile), at the gradient text to move into a <text> of its own.
func (c *compiler) checkHiddenText(f *paint) {
	if c.hiddenText != nil {
		return
	}
	var between []*paint // the elements from the text out to the gradient, the gradient's own not among them
	var gradient *paintIntent
	for fr := f; fr != nil && gradient == nil; fr = fr.parent {
		own := false
		for _, pi := range fr.written {
			switch {
			case pi.name != "color":
			case c.writesGradient(pi):
				gradient = pi
			case pi.width == 0 && len(pi.states) == 0:
				own = true
			}
		}
		if gradient == nil {
			if own {
				return // its letters take their own colour again: nothing around it reaches them
			}
			between = append(between, fr)
		}
	}
	if gradient == nil || len(between) == 0 {
		return
	}
	conds := conditionsOf(f)
	for _, fr := range between {
		if ground := c.coverOf(fr, conds); ground != "" {
			written := strings.TrimSpace(gradient.attr.Value)
			text, lead := gradient.attr.Name+"=\""+written+"\"", ""
			if v := c.gradientWritten(gradient); v != written {
				lead = " (nhánh " + v + ")"
			}
			c.hiddenText = errAttr(gradient.node, gradient.attr, "%s%s là chữ gradient: chữ bên trong thừa hưởng nét tô trong suốt của nó (-webkit-text-fill-color), "+
				"và <%s> bên trong có %s phủ lên gradient, nên chữ của <%s> không hiện. "+
				"Để chữ gradient trên một <text> riêng không bọc phần tử có nền (<text %s>…</text>), hoặc cho <%s> màu chữ riêng (color=\"ink\")",
				text, lead, fr.tag, ground, fr.tag, text, fr.tag)
			return
		}
	}
}

// coverOf is the ground an element paints over what lies behind it, as a message names it ("nền
// background=\"paper\"", "nền panel của <button>"): its layer at some condition and mode has a colour
// nothing shows through, or one from data that may be. "" for an element that paints nothing, or
// paints only see-through colours (the gradient's letters still show through them).
func (c *compiler) coverOf(f *paint, conds []cond) string {
	for _, k := range conds {
		for _, m := range c.table.Palette().Modes() {
			layer, known := c.layerOf(f, k, m, nil)
			for _, s := range layer {
				if s.rgba.Opaque() {
					return groundWord(f, s.intent)
				}
			}
			if !known {
				return groundWord(f, f.winner(k, "background", "material"))
			}
		}
	}
	return ""
}

// groundWord names the ground an element paints, from the attribute that paints it (nil: its kind's
// surface).
func groundWord(f *paint, pi *paintIntent) string {
	switch {
	case pi != nil && pi.attr != nil && pi.name == "material":
		return "kính (" + pi.attr.Name + "=\"" + strings.TrimSpace(pi.attr.Value) + "\")"
	case pi != nil && pi.attr != nil:
		return "nền " + pi.attr.Name + "=\"" + strings.TrimSpace(pi.attr.Value) + "\""
	case pi != nil && len(pi.values) > 0:
		return "nền " + pi.values[0] + " engine cho <" + f.tag + ">" // a neutral button's fill, a pinned frame's
	}
	if look := f.look(0); look.Surface != "" && look.Surface != "none" {
		return "nền " + look.Surface + " của <" + f.tag + ">"
	}
	return "nền của <" + f.tag + ">"
}
