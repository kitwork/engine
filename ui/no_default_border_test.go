package ui

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// neutralClasses are the classes of a neutral button (compile.go, neutralSteps): its translucent fill
// of ink at rest, under the pointer, pressed and disabled, as an author's background would be written.
// Never a border.
const neutralClasses = "background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4"

// a class that draws an edge, prefixed or not: border-line, hover:border-brand…
var borderClass = regexp.MustCompile(`(^|[\s":])border-[a-z]`)

// A button its author writes nothing about the fill or the edge of is neutral (decision B1, 12/10/2026)
// and stands on translucent ink (decision F1, 13/10/2026): ink/6 at rest, ink/10 under the pointer,
// ink/14 pressed, ink/4 disabled, and no border. What the author writes about the fill or the edge is
// theirs: a border written, a background written in any state, border="none". CONTROLS: the steps are
// the classes of the page's stylesheet; the engine's hover step is measured as hover, over what lies
// behind the button (a colour that fails only on it is told "khi hover:").
func TestNeutralButtonHasAFillNotABorder(t *testing.T) {
	html, css := renderCSS(t, `<content><button>Gửi</button></content>`, nil)
	mustContain(t, html, `<button class="flex-row `+neutralClasses+`" type="button">Gửi</button>`)
	mustContain(t, css, `.background-ink\/6{background-color:color-mix(in srgb,var(--ink) 6%,transparent)}`,
		`.hover\:background-ink\/10:where(:hover){background-color:color-mix(in srgb,var(--ink) 10%,transparent)}`,
		`.pressed\:background-ink\/14:where(:active){background-color:color-mix(in srgb,var(--ink) 14%,transparent)}`,
		`.disabled\:background-ink\/4:where(:disabled,[aria-disabled=true]){background-color:color-mix(in srgb,var(--ink) 4%,transparent)}`)
	if borderClass.MatchString(html) || strings.Contains(css, "border") {
		t.Errorf("a neutral button draws a border:\n%s\n%s", html, css)
	}
	if want := []struct{ state, value string }{{"", "ink/6"}, {"hover", "ink/10"}, {"pressed", "ink/14"}, {"disabled", "ink/4"}}; !slices.Equal(neutralSteps, want) {
		t.Errorf("a neutral button rests on ink/6, ink/10 under the pointer, ink/14 pressed, ink/4 disabled: %v", neutralSteps)
	}

	// what the author writes about the fill or the edge is theirs
	for source, want := range map[string]string{
		`<button border="line">Viền</button>`:             `<button class="flex-row border-line" type="button">`,
		`<button background="brand">Gửi</button>`:         `<button class="flex-row background-brand" type="button">`,
		`<button hover:background="panel">Rê</button>`:    `<button class="flex-row hover:background-panel" type="button">`,
		`<button border="none">Trần</button>`:             `<button class="flex-row border-none" type="button">`,
		`<button medium:background="paper">Rộng</button>`: `<button class="flex-row medium:background-paper" type="button">`,
	} {
		html := render(t, `<content>`+source+`</content>`, nil)
		mustContain(t, html, want)
		mustNotContain(t, html, "background-ink/6 hover:background-ink/10")
	}

	// the hover step is measured where it shows: line on ink/10 over the canvas, told at the colour with
	// its condition
	if got := contrastCompile(t, nil, content(`<content><button hover:color="line">Gửi</button></content>`)); !strings.Contains(got, `trên nền ink/10 phủ trên canvas`) || !strings.Contains(got, `khi hover:`) {
		t.Errorf("CONTROL: the engine's hover step is measured, got %q", got)
	}
}

// lightness is a colour's OKLab lightness (Björn Ottosson's OKLab, CSS Color 4's oklab), 0 black to 1
// white: the scale a reader's eye tells two greys apart on.
func lightness(c stylesheet.RGBA) float64 {
	linear := func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	r, g, b := linear(c.R), linear(c.G), linear(c.B)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return 0.2104542553*l + 0.7936177850*m - 0.0040720468*s
}

// defaultFill is a fill Kitwork paints behind a control before its author writes anything.
type defaultFill struct{ what, value string }

// defaultFills are read where they are decided: a neutral button's steps (compile.go) and the looks of a
// field, a field with focus and a quote (reboot.css, through its table of looks).
func defaultFills() []defaultFill {
	var out []defaultFill
	for _, step := range neutralSteps {
		out = append(out, defaultFill{what: strings.TrimSpace("<button> " + step.state), value: step.value})
	}
	return append(out,
		defaultFill{what: "<input>", value: stylesheet.LookOf("input", nil, "", 0).Surface},
		defaultFill{what: "<input> focus", value: stylesheet.LookIn("focus", "input", nil, "", 0).Surface},
		defaultFill{what: "<quote>", value: stylesheet.LookOf("blockquote", nil, "", 0).Surface})
}

// lift is how far a fill painted over a ground stands from that ground in OKLab lightness, in one mode,
// each colour as a screen shows it (8 bits a channel).
func lift(p *stylesheet.Palette, fill, ground string, m stylesheet.Mode) float64 {
	under, _ := p.Paint(ground, m)
	under = under.Painted()
	over, ok := p.Paint(fill, m)
	if !ok {
		return 0
	}
	return math.Abs(lightness(over.Over(under).Painted()) - lightness(under))
}

// fillGrounds are what a control may stand on: the page's canvas, a card's paper, the panel, and a
// darker ground, ink-80.
var fillGrounds = []string{"canvas", "paper", "panel", "ink-80"}

// unseen lists every fill that does not stand at least .02 in OKLab lightness from a ground it lands on.
func unseen(p *stylesheet.Palette, fills []defaultFill) []string {
	var out []string
	for _, m := range p.Modes() {
		for _, ground := range fillGrounds {
			for _, f := range fills {
				if d := lift(p, f.value, ground, m); d < .02 {
					out = append(out, fmt.Sprintf("%s %s on %s (chế độ %s): ΔL %.4f", f.what, f.value, ground, m.Word(), d))
				}
			}
		}
	}
	return out
}

// Kitwork's own fills are translucent ink (decision F1, 13/10/2026), so a control shows on any ground
// it lands on: every fill of a neutral button, a field and a quote stands at least .02 in OKLab
// lightness from the canvas, a card's paper, the panel and ink-80, in light and in dark. A button
// deepens with each step under the pointer (at least .015 a step), a field with focus; a disabled
// button is fainter than at rest and its words dimmed to half their colour. CONTROL: the solid fills
// these replaced (panel, ink-90, ink-80, paper) vanish on a ground of their own colour, and the check
// finds each.
func TestTranslucentFillsShowOnEveryGround(t *testing.T) {
	palette := darkPalette(t)
	fills := defaultFills()
	if problems := unseen(palette, fills); len(problems) > 0 {
		t.Errorf("a default fill vanishes on a ground it may stand on:\n%s", strings.Join(problems, "\n"))
	}

	value := map[string]string{}
	for _, f := range fills {
		value[f.what] = f.value
	}
	for _, m := range palette.Modes() {
		for _, ground := range fillGrounds {
			deeper := func(before, after string, at float64) {
				if a, b := lift(palette, value[before], ground, m), lift(palette, value[after], ground, m); b-a < at {
					t.Errorf("%s (%s) is not deeper than %s (%s) on %s (chế độ %s): ΔL %.4f then %.4f", after, value[after], before, value[before], ground, m.Word(), a, b)
				}
			}
			deeper("<button>", "<button> hover", .015)
			deeper("<button> hover", "<button> pressed", .015)
			deeper("<input>", "<input> focus", .015)
			deeper("<button> disabled", "<button>", .005) // disabled: fainter than at rest

			// a disabled button's words: reboot.css paints them at half their colour, so they stand at most
			// half as far from their fill as an enabled button's (contrast ratio), in every mode
			under, _ := palette.Paint(ground, m)
			ink, _ := palette.Paint("ink", m)
			rest, _ := palette.Paint(value["<button>"], m)
			off, _ := palette.Paint(value["<button> disabled"], m)
			restGround, offGround := rest.Over(under.Painted()).Painted(), off.Over(under.Painted()).Painted()
			dim := ink
			dim.A = .5
			enabled, disabled := stylesheet.Ratio(ink.Over(restGround).Painted(), restGround), stylesheet.Ratio(dim.Over(offGround).Painted(), offGround)
			if disabled > enabled/2 {
				t.Errorf("a disabled button's words on %s (chế độ %s) stand %.2f:1, an enabled one's %.2f:1: not dimmed enough to read as disabled", ground, m.Word(), disabled, enabled)
			}
		}
	}
	mustContain(t, rebootCSS(), `:where(button:disabled,input:disabled,select:disabled,textarea:disabled,[aria-disabled="true"]){color:color-mix(in oklab,currentColor 50%,transparent)}`)

	// THE EXCEPTION, as compile.go (neutralSteps), reboot.css and KITWORK_UI say it: on a band painted ink
	// itself every fill is ink over ink and shows nothing, in light and in dark; its author writes a
	// background or a border there. A fill that shows on ink after all is a change of design: say so there.
	for _, m := range palette.Modes() {
		for _, f := range fills {
			if d := lift(palette, f.value, "ink", m); d >= .02 {
				t.Errorf("%s %s stands ΔL %.4f from a band painted ink (chế độ %s): the comments and KITWORK_UI say the fills do not show there", f.what, f.value, d, m.Word())
			}
		}
	}

	// CONTROL: the solid fills of before vanish on their own colour
	solid := []defaultFill{{"<button>", "panel"}, {"<button> hover", "ink-90"}, {"<button> pressed", "ink-80"}, {"<input> focus", "paper"}}
	problems := strings.Join(unseen(palette, solid), "\n")
	for _, want := range []string{"<button> panel on panel (chế độ sáng)", "<button> pressed ink-80 on ink-80 (chế độ tối)", "<input> focus paper on paper (chế độ sáng)"} {
		if !strings.Contains(problems, want) {
			t.Errorf("CONTROL: the check does not find %q in:\n%s", want, problems)
		}
	}
}

// A quote is set apart by a faint translucent fill of ink, ink/4, not by a rule on its edge: no border,
// its edge 1px and transparent (a contrast theme draws it), its words measured on that fill over what
// lies behind it. CONTROL: in an ink section the page's ink is told on ink/4 over the ink; in a box (no
// fill of its own), on the ink alone; with the section's words paper, the quote reads.
func TestQuoteStandsOnAFaintFill(t *testing.T) {
	html := render(t, `<content><quote><text>Câu</text></quote></content>`, nil)
	mustContain(t, html, `<blockquote class="flex-column"><p>Câu</p></blockquote>`)
	mustContain(t, rebootCSS(), `:where(blockquote){gap:0.5rem;padding:1rem;border:1px solid transparent;border-radius:0.5rem;background:color-mix(in srgb,var(--ink) 4%,transparent)}`)
	mustNotContain(t, rebootCSS(), "border-inline-start")
	contrastPasses(t, nil, content(`<content><quote><text>Câu</text></quote><section background="ink" color="paper"><quote><text>Câu</text></quote></section></content>`))
	if got := contrastCompile(t, nil, content(`<content><section background="ink"><quote><text>Câu</text></quote></section></content>`)); !strings.Contains(got, "trên nền ink/4 phủ trên ink chỉ 1.00:1") {
		t.Errorf("a quote's words are measured on its fill over the section, got %q", got)
	}
	if got := contrastCompile(t, nil, content(`<content><section background="ink"><box><text>Câu</text></box></section></content>`)); !strings.Contains(got, "trên nền ink chỉ 1.00:1") {
		t.Errorf("CONTROL: words in a box on ink are measured on ink, got %q", got)
	}
}

// A field (input, select, textarea) stands on a translucent fill of ink, ink/5, with no border, and the
// fill deepens to ink/8 with focus, under the focus ring (reboot.css). What is typed is measured on both,
// over what lies behind the field: an ink that reads on the fill at rest (4.60:1 on a white canvas) and
// not on the deeper one (4.44:1) is told with focus only. A background the author writes wins in every
// state: on it the deeper fill never shows. CONTROL: the same field with Kit Design's ink is quiet.
func TestFieldsStandOnATranslucentFill(t *testing.T) {
	html := render(t, `<content><form><field><label>Tên</label><input></input></field><field><label>Chọn</label><select><option value="a">A</option></select></field><field><label>Lời</label><textarea></textarea></field></form></content>`, nil)
	if borderClass.MatchString(html) {
		t.Errorf("a field gets a border class:\n%s", html)
	}
	reboot := rebootCSS()
	mustContain(t, reboot,
		`:where(input:not([type="checkbox"],[type="radio"]),select,textarea){min-inline-size:0;padding:0.5rem 0.75rem;border:1px solid transparent;border-radius:0.375rem;background:color-mix(in srgb,var(--ink) 5%,transparent)}`,
		`:where(input:not([type="checkbox"],[type="radio"]):focus,select:focus,textarea:focus){background:color-mix(in srgb,var(--ink) 8%,transparent)}`,
		`:where(:focus-visible){outline:2px solid var(--kitwork-focus,var(--brand));outline-offset:2px}`)

	grey, err := stylesheet.NewPalette(false, map[string]stylesheet.Color{"canvas": {Light: "#ffffff"}, "ink": {Light: "#717171"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`<input></input>`, `<textarea></textarea>`, `<select><option value="a">A</option></select>`} {
		got := contrastCompile(t, grey, content(`<content><field><label>Tên</label>`+field+`</field></content>`))
		if !strings.Contains(got, "trên nền ink/8 phủ trên canvas chỉ 4.44:1") || !strings.Contains(got, "khi focus:") || strings.Count(got, "\n") != 0 {
			t.Errorf("%s: what is typed is measured on the deeper fill a field takes with focus, got %q", field, got)
		}
	}
	// a background written wins in every state: the deeper fill never shows behind what is typed
	contrastPasses(t, grey, content(`<content><field><label>Tên</label><input background="canvas"></input></field></content>`))
	// CONTROL: with Kit Design's ink nothing is told
	contrastPasses(t, nil, content(`<content><field><label>Tên</label><input></input></field></content>`))
}

// border="line" still draws where the author writes it: a box, a button, a field. The reset leaves
// every border solid, so a width and a colour are all a class says.
func TestAuthorBorderStillDraws(t *testing.T) {
	html, css := renderCSS(t, `<content><box border="line"><text>a</text></box><button border="line">b</button><input border="line"></input></content>`, nil)
	mustContain(t, html, `<div class="flex-column border-line">`, `<button class="flex-row border-line" type="button">`, `<input class="border-line"`)
	mustContain(t, css, `.border-line{border-width:1px;border-color:var(--line)}`)
	mustContain(t, rebootCSS(), `*,::before,::after{box-sizing:border-box;margin:0;padding:0;border:0 solid}`)
}

// No element gets a border nobody wrote: every element of the catalog, written bare, compiles to no
// border class and a page stylesheet with no border in it (reboot.css is TestNoElementHasABorderOfItsOwn).
func TestNoElementGetsABorderNobodyWrote(t *testing.T) {
	html, css := renderCSS(t, `<content>
<section><box><card><grid><text>a</text></grid></card></box></section>
<list><item><text>x</text></item></list>
<text heading="2">H</text>
<code>let x</code>
<quote><text>Câu</text></quote>
<time value="2026-10-12">hôm nay</time>
<badge>Mới</badge>
<link value="/">Trang</link>
<button>Gửi</button>
<icon value="coffee"></icon>
<image value="/a.png" alt="A"></image>
<form><field><label>Tên</label><input></input><hint>h</hint><error /></field>
<field><label>Chọn</label><select><option value="a">A</option></select></field>
<field><label>Lời</label><textarea></textarea></field>
<input type="checkbox"></input><input type="switch"></input></form>
<accordions><accordion><text>Q</text><text>A</text></accordion></accordions>
</content>`, nil)
	if borderClass.MatchString(html) {
		t.Errorf("an element written bare gets a border class:\n%s", html)
	}
	if strings.Contains(css, "border") {
		t.Errorf("the page's stylesheet draws a border nobody wrote:\n%s", css)
	}
	for _, tag := range []string{"<section", "<div class=\"card", "<ul", "<h2", "<code", "<blockquote", "<time", "<span class=\"badge", "<a ", "<button", "<i ", "<img", "<form", "<input", "<select", "<textarea", "<details"} {
		mustContain(t, html, tag) // each element really is on the page
	}
}
