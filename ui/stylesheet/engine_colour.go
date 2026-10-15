package stylesheet

import (
	"strconv"
	"strings"
)

// Engine colours (decision of 13/10/2026, "+N riêng cho từng chế độ"): a colour the engine chooses,
// never the author — the text of a <badge>, the colours of a code block, the words of <error>, the
// brand end of the standard headline and inverse gradients — is its token made stronger by the
// smallest multiple of 5 that reads 4.5:1 on its grounds in THAT mode. It starts from the role's step
// when the mode's canvas is lighter than the token (stronger darkens it: the step the engine wrote for
// both modes before, so a light look is byte for byte what it was), and from 0 when the canvas is
// darker (stronger lightens it, toward white: the accent itself reads, and keeps its hue, as the old
// .deep did in dark). When no step up to +100 reads, the role's step stays and the contrast check
// tells it. An X+N the author writes is one number for both modes still (operators.go).
//
// Each engine colour is a name, the custom property it is written in without its --: the tables (looks,
// codeLooks, the standard gradients, BadgeTint) hold the name, Paint paints it as its value in the mode
// asked, and the page's stylesheet (Rules.CSS) or colors.<hash>.css sets it per mode. No value of the
// markup can name one: kitwork- starts no token (checkToken) and no gradient's stop is read as one. One
// more name is read the same way without being the engine's choice: kitwork-highlight-<kind>, the
// { light, dark } pair router.highlight gives a kind (highlight.go), kept apart in Palette.highlight.

// The names of the engine colours that do not depend on an accent; a badge's is BadgeColour, a code
// colour's CodeColour.
const (
	ErrorColour    = "kitwork-error"    // the words of <error> (reboot.css :where(.error))
	HeadlineColour = "kitwork-headline" // the brand end of the standard headline gradient
	InverseColour  = "kitwork-inverse"  // the brand start of the standard inverse gradient
)

// CodeColour is the engine colour of a code role: kitwork-code-brand, kitwork-code-inserted.
func CodeColour(role string) string { return "kitwork-code-" + role }

// BadgeColour is the engine colour of a badge's text on its accent's tinted fill: kitwork-badge-warning.
func BadgeColour(accent string) string { return "kitwork-badge-" + accent }

// engineRole is how an engine colour is worked out: its token, the grounds it must read on (colour values
// of the palette), and the step it starts from where stronger darkens it.
type engineRole struct {
	token string
	on    []string
	step  int
}

var pageGrounds = []string{"canvas", "paper", "panel"}

// engineRoles are the engine colours that do not depend on which accent the author wrote, each with the
// step the engine wrote for both modes before (13/10/2026). A badge's text is one per accent that grows a
// label (engineColours).
var engineRoles = map[string]engineRole{
	CodeColour("brand"):    {"brand", pageGrounds, 35},
	CodeColour("success"):  {"success", pageGrounds, 35},
	CodeColour("warning"):  {"warning", pageGrounds, 55},
	CodeColour("info"):     {"info", pageGrounds, 25},
	CodeColour("danger"):   {"danger", pageGrounds, 25},
	CodeColour("inserted"): {"success", []string{"success-" + strconv.Itoa(TintStep)}, 45},
	CodeColour("deleted"):  {"danger", []string{"danger-" + strconv.Itoa(TintStep)}, 40},
	ErrorColour:            {"danger", pageGrounds, ErrorStep},
	// as text over the page's grounds and the glow behind a hero; inverse under paper, canvas and
	// brand-95 text, on the band itself
	HeadlineColour: {"brand", []string{"canvas", "paper", "panel", "brand-" + strconv.Itoa(TintStep)}, 40},
	InverseColour:  {"brand", []string{"paper", "canvas", "brand-" + strconv.Itoa(FaintStep)}, 40},
}

// badgeRole is the role of a badge's text: its accent on the accent's tinted fill (X-85), from
// StrongStep.
func badgeRole(accent string) engineRole {
	return engineRole{accent, []string{accent + "-" + strconv.Itoa(TintStep)}, StrongStep}
}

// floorIn is the step a role starts from in a mode: its step when that mode's canvas is lighter than the
// token (OKLab lightness), so stronger darkens it; 0 when the canvas is darker or as light, so stronger
// lightens it toward white and the token itself is tried first.
func (p *Palette) floorIn(r engineRole, m Mode) int {
	canvas, ok := p.token("canvas", m)
	token, known := p.token(r.token, m)
	if !ok || !known || toOklab(canvas).L <= toOklab(token).L {
		return 0
	}
	return r.step
}

// readableIn is the smallest step that makes a token's text read on every ground in one mode: n, a
// multiple of 5 from floor (rounded up to one) to 100, such that token+n (the token itself for 0) reads
// ratio:1 on each colour value of on, each painted as a screen shows it. When even token+100 does not, ok
// is false and n is the floor: the role's own step stays, and the contrast check tells it.
func (p *Palette) readableIn(token string, on []string, ratio float64, floor int, m Mode) (int, bool) {
	start := (max(floor, 0) + 4) / 5 * 5
	for n := start; n <= 100; n += 5 {
		if p.readsIn(spell(token, n), on, ratio, m) {
			return n, true
		}
	}
	return start, false
}

// readsIn reports whether a colour value reads ratio:1 on every ground of on in one mode.
func (p *Palette) readsIn(text string, on []string, ratio float64, m Mode) bool {
	fg, ok := p.Paint(text, m)
	if !ok {
		return false
	}
	for _, ground := range on {
		bg, ok := p.Paint(ground, m)
		if !ok || Ratio(fg.Painted(), bg.Painted()) < ratio {
			return false
		}
	}
	return true
}

// spell writes token+n as an author would, the token alone for 0.
func spell(token string, n int) string {
	if n == 0 {
		return token
	}
	return token + "+" + strconv.Itoa(n)
}

// engineColours works out every engine colour of the palette, per mode, as the colour value it is in
// that mode (warning+55 in light, warning in dark): the fixed roles, and a badge's text for each accent
// that grows a label. A role whose token the palette lacks has none. Run while p.engine is still nil, so
// Paint reads plain colour values only.
func (p *Palette) engineColours() map[string][2]string {
	out := map[string][2]string{}
	add := func(name string, r engineRole) {
		if !p.Has(r.token) {
			return
		}
		var v [2]string
		for _, m := range p.Modes() {
			n, _ := p.readableIn(r.token, r.on, NormalTextRatio, p.floorIn(r, m), m)
			v[m] = spell(r.token, n)
		}
		out[name] = v
	}
	for name, r := range engineRoles {
		add(name, r)
	}
	for _, name := range p.names {
		if accent(name) && p.Has(name+".on") {
			add(BadgeColour(name), badgeRole(name))
		}
	}
	return out
}

// Engine is an engine colour's value in a mode, as an author would write it (warning+55 in light,
// warning in dark); its light value in dark when the palette has no dark look. A kind's pair of
// router.highlight (kitwork-highlight-function) is its side of that mode, #rrggbb (highlight.go). ok is
// false for any value that is neither.
func (p *Palette) Engine(name string, m Mode) (string, bool) {
	name = strings.TrimSpace(name)
	if kind, ok := strings.CutPrefix(name, highlightPrefix); ok {
		return p.highlightSide(kind, m)
	}
	v, ok := p.engine[name]
	if !ok {
		return "", false
	}
	if m == DarkMode && v[DarkMode] != "" {
		return v[DarkMode], true
	}
	return v[LightMode], true
}

// enginePair is an engine colour's custom property (--kitwork-error) with its colour in light, and in
// dark when the palette has a dark look: what the page's stylesheet sets (Rules.CSS), and
// colors.<hash>.css for the words of <error>.
func (p *Palette) enginePair(name string) (pairValue, bool) {
	v, ok := p.engine[name]
	if !ok {
		return pairValue{}, false
	}
	out := pairValue{name: "--" + name}
	if c, ok := p.Paint(v[LightMode], LightMode); ok {
		out.light = c.Hex()
	}
	if p.hasDark {
		if c, ok := p.Paint(v[DarkMode], DarkMode); ok {
			out.dark = c.Hex()
		}
	}
	return out, true
}

// engineColour is the CSS of an engine colour, var(--name), and its custom property per mode for the
// page's stylesheet; ok is false for a value that is no engine colour of the palette.
func (t *Table) engineColour(name string) (string, []pairValue, bool) {
	v, ok := t.palette.enginePair(strings.TrimSpace(name))
	if !ok {
		return "", nil, false
	}
	return "var(" + v.name + ")", []pairValue{v}, true
}
