package stylesheet

import (
	"sort"
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/highlight"
)

// The palettes code is checked on: Kit Design's default (its dark look derived), the kitwork example's
// router.colors word for word, the same with the canvas it had on 12/10/2026 (the copy the gradient
// tests keep), and the theme of the quancaphe conformance fixture with its branch that writes its own
// brand — the five the code colours were measured on (operators of 13/10/2026).
func codePalettes(t *testing.T) map[string]*Palette {
	t.Helper()
	// docs/examples/kitwork/kitwork.localhost/router.work
	kitwork := map[string]Color{
		"brand": {Light: "#f5225f", Dark: "#ff6b6b"}, "canvas": {Light: "#f6f9fc", Dark: "#16161e"},
		"paper": {Light: "#ffffff", Dark: "#1e2030"}, "panel": {Light: "#eff3f8", Dark: "#181a26"},
		"line": {Light: "#e6ebf1", Dark: "#2c3145"}, "ink": {Light: "#242442", Dark: "#c0caf5"},
		"success": {Light: "#0e9f6e"}, "warning": {Light: "#c98a1e"}, "danger": {Light: "#ef4444"}, "info": {Light: "#3b82f6"},
	}
	// the same before 13/10/2026: a pink canvas, panel and line (designPalettes keeps it)
	kitworkPink := map[string]Color{
		"brand": {Light: "#f5225f", Dark: "#ff6b6b"}, "canvas": {Light: "#ffeaec", Dark: "#16161e"},
		"paper": {Light: "#ffffff", Dark: "#1e2030"}, "panel": {Light: "#f7eff1", Dark: "#181a26"},
		"line": {Light: "#e5e3e5", Dark: "#2c3145"}, "ink": {Light: "#242442", Dark: "#c0caf5"},
		"success": {Light: "#0e9f6e"}, "warning": {Light: "#c98a1e"}, "danger": {Light: "#ef4444"}, "info": {Light: "#3b82f6"},
	}
	// the quancaphe conformance fixture (engine/conformance/testdata/ui/quancaphe/cases.json theme), and
	// its quan-ly branch
	quancaphe := map[string]Color{
		"brand": {Light: "#c8742c"}, "canvas": {Light: "#f6f3ee", Dark: "#141110"},
		"panel": {Light: "#efe9e1", Dark: "#1a1613"}, "paper": {Light: "#ffffff", Dark: "#1f1a17"},
		"ink": {Light: "#2b211b", Dark: "#f3ece6"}, "line": {Light: "#e6ded5", Dark: "#3a302a"},
		"success": {Light: "#2f9e63"}, "warning": {Light: "#c98a1e"}, "danger": {Light: "#e5484d"},
	}
	out := map[string]*Palette{}
	for name, chain := range map[string][]map[string]Color{
		"Kit Design":               nil,
		"kitwork":                  {kitwork},
		"kitwork (canvas #ffeaec)": {kitworkPink},
		"quancaphe":                {quancaphe},
		"quancaphe/quan-ly":        {quancaphe, {"brand": {Light: "#2563eb"}}},
	} {
		p, err := NewPalette(true, chain...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = p
	}
	return out
}

// Every kind jit/highlight has gets a look and a class, but plain (bare text, the code's own colour);
// a name that is no kind gets none. CONTROL: a kind the table forgot fails here, not as a span
// nobody coloured.
func TestCodeEveryKindHasALook(t *testing.T) {
	var kinds []string
	for _, kind := range highlight.Kinds() {
		name := kind.String()
		look, ok := CodeLookOf(name)
		if kind == highlight.Plain {
			if ok {
				t.Errorf("plain is bare text in the code's own colour, it has no look: got %+v", look)
			}
			continue
		}
		kinds = append(kinds, name)
		if !ok || look.Color == "" && look.Share == 0 { // the prompt is a share of the text around it
			t.Errorf("kind %s has no colour", name)
		}
		if got := CodeClass(name); got != "code-"+name {
			t.Errorf("class of %s is %q, want code-%s", name, got, name)
		}
	}
	sort.Strings(kinds)
	if got := strings.Join(CodeKinds(), " "); got != strings.Join(kinds, " ") {
		t.Errorf("CodeKinds is not every kind but plain:\n got  %s\n want %s", got, strings.Join(kinds, " "))
	}
	if _, ok := CodeLookOf("regex"); ok {
		t.Error("regex is no kind (regular-expression is)")
	}
	if _, err := NewTable(nil).Code(&Rules{}, "regex"); err == nil || !strings.Contains(err.Error(), "regex") {
		t.Errorf("a kind with no look is refused, got %v", err)
	}
}

// Table.Code records one rule a kind, in the page layer, through the palette's roles (operators of
// 13/10/2026): an accent as its engine colour's custom property (kitwork-code-brand, engine_colour.go),
// set on :root for the page in light and again for the dark look (Rules.CSS) — on Kit Design brand+35
// #4500de in light as before, brand itself #7d83fd in dark —, ink lighter mixed toward canvas exactly as
// color="ink-30" is, a fill, italic, bold (whose weight the fonts file ships) and the prompt left out of
// a selection. Only what is asked for is recorded, the custom properties too; an author's brand+35 is
// another property. CONTROL: an empty rule set has no code rule.
func TestCodeRulesAreThePalettesRoles(t *testing.T) {
	palette, _ := NewPalette(true)
	table := NewTable(palette)
	rules := &Rules{}
	for _, kind := range []string{"keyword", "comment", "inserted", "heading", "prompt"} {
		if _, err := table.Code(rules, kind); err != nil {
			t.Fatal(err)
		}
	}
	css := string(rules.CSS())
	hex := func(value string, m Mode) string {
		c, ok := palette.Paint(value, m)
		if !ok {
			t.Fatalf("the palette cannot paint %s", value)
		}
		return c.Hex()
	}
	for _, want := range []string{
		"@layer page{\n",
		".code-keyword{color:var(--kitwork-code-brand)}",
		".code-comment{color:color-mix(in oklab,var(--ink) 70%,var(--canvas));font-style:italic}",
		".code-inserted{color:var(--kitwork-code-inserted);background-color:color-mix(in oklab,var(--success) 15%,var(--canvas))}",
		".code-heading{color:var(--kitwork-code-brand);font-weight:700}",
		".code-prompt{color:color-mix(in oklab,currentColor 70%,transparent);-webkit-user-select:none;user-select:none}",
		// the engine colours of the page, each its own in each mode: light as the steps were, dark the
		// accent itself
		":root{--kitwork-code-brand:#4500de;--kitwork-code-inserted:#006a3a}\n",
		"@media screen{:root[data-theme=dark]{--kitwork-code-brand:#7d83fd;--kitwork-code-inserted:#69b485}}\n",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %s in\n%s", want, css)
		}
	}
	if hex(CodeColour("brand"), LightMode) != hex("brand+35", LightMode) || hex(CodeColour("brand"), DarkMode) != hex("brand", DarkMode) ||
		hex(CodeColour("inserted"), DarkMode) != hex("success", DarkMode) {
		t.Errorf("Paint and the page disagree: kitwork-code-brand is %s / %s", hex(CodeColour("brand"), LightMode), hex(CodeColour("brand"), DarkMode))
	}
	if strings.Contains(css, ".code-string") || strings.Contains(css, ".code-deleted") || strings.Contains(css, "-danger") || strings.Contains(css, "-warning") ||
		strings.Contains(css, "--kitwork-strong-") {
		t.Errorf("only the kinds asked for are recorded, and their own custom properties:\n%s", css)
	}
	if got := rules.Weights(); len(got) != 1 || got[0] != 700 {
		t.Errorf("a bold heading ships the weight 700, got %v", got)
	}
	// a lighter colour is written as color= writes it: the same bytes (an accent is the engine's own
	// property, which color= cannot name)
	for kind, value := range map[string]string{"comment": "ink-30"} {
		written, err := table.Class(&Rules{}, Intent{Name: "color", Value: value})
		if err != nil || written != "color-"+value {
			t.Fatalf("color=%q: %q, %v", value, written, err)
		}
		colour, _, _, _, _ := table.colour(Intent{Name: "color", Value: value}, value)
		if !strings.Contains(css, ".code-"+kind+"{color:"+colour) {
			t.Errorf("%s is written as color=%q writes it (%s):\n%s", kind, value, colour, css)
		}
	}
	if css := (&Rules{}).CSS(); len(css) != 0 {
		t.Errorf("CONTROL: no rule recorded, no CSS, got %s", css)
	}
}

// Every kind reads on the code's ground (reboot.css paints nothing behind code, so a block shows the
// canvas, a card's paper or a panel around it) at WCAG 2.2 AA for normal text, 4.5:1, in light and in
// dark, on the five palettes the steps were measured on (codePalettes). inserted and deleted are read on
// their own fill. Measured as the pixel a screen paints, as the compiler's check does. Each accent is an
// engine colour (engine_colour.go) and its step is the least that reads, per mode: in light the role's
// step, which five less falls short of on one of the palettes, so the code is no stronger than it must
// be; in dark the least from 0 on each palette (the accent itself on these five). CONTROL: brand itself
// (not brand+35) falls short somewhere in light, so the check is not vacuous; and the roles checked are
// the seven code colours, so a list that finds none fails.
func TestCodeColoursReadOnEveryGround(t *testing.T) {
	const need = NormalTextRatio
	ratio := func(p *Palette, colour, ground string, m Mode) float64 {
		g, ok := p.Paint(ground, m)
		if !ok {
			t.Fatalf("the palette cannot paint %s", ground)
		}
		x, ok := p.Paint(colour, m)
		if !ok {
			t.Fatalf("the palette cannot paint %s", colour)
		}
		return Ratio(x.Over(g.Painted()).Painted(), g.Painted())
	}
	palettes := codePalettes(t)
	worst, at := 21.0, ""
	for name, p := range palettes {
		for _, kind := range CodeKinds() {
			look, _ := CodeLookOf(kind)
			if look.Share > 0 {
				continue // the text around it, muted: measured where it stands (ui/contrast.go)
			}
			grounds := []string{"canvas", "paper", "panel"}
			if look.Background != "" {
				grounds = []string{look.Background}
			}
			for _, m := range []Mode{LightMode, DarkMode} {
				for _, ground := range grounds {
					r := ratio(p, look.Color, ground, m)
					if r < need {
						t.Errorf("%s: %s (%s) on %s in %s mode is %.2f:1, short of %.1f:1", name, kind, look.Color, ground, m.Word(), r, need)
					}
					if r < worst {
						worst, at = r, name+" "+kind+" on "+ground+" "+m.Word()
					}
				}
			}
		}
	}
	t.Logf("worst pair: %s, %.2f:1", at, worst)
	// each engine colour of the code is the least step that reads, per mode
	checked := map[string]bool{}
	for _, kind := range CodeKinds() {
		look, _ := CodeLookOf(kind)
		role, ok := engineRoles[look.Color]
		if !ok || checked[look.Color] {
			continue // ink-30 and the prompt are no engine colour
		}
		checked[look.Color] = true
		less := spell(role.token, role.step-5)
		short := false
		for name, p := range palettes {
			if light, _ := p.Engine(look.Color, LightMode); light != spell(role.token, role.step) {
				t.Errorf("%s: %s is %s in light, want %s as it was", name, look.Color, light, spell(role.token, role.step))
			}
			short = short || !p.readsIn(less, role.on, need, LightMode)
			dark, _ := p.Engine(look.Color, DarkMode)
			c, _ := SplitColour(dark)
			if !p.readsIn(dark, role.on, need, DarkMode) || c.Amount > 0 && p.readsIn(spell(role.token, c.Amount-5), role.on, need, DarkMode) {
				t.Errorf("%s: %s is %s in dark, not the least step from 0 that reads", name, look.Color, dark)
			}
		}
		if !short {
			t.Errorf("%s starts from %s in light, but %s reads on every palette: the step is not the least that reads", look.Color, spell(role.token, role.step), less)
		}
	}
	if len(checked) != 7 {
		t.Errorf("CONTROL: %d code colours checked, want the 7 engine colours of the code (%v)", len(checked), checked)
	}
	short := false
	for _, p := range palettes {
		if ratio(p, "brand", "canvas", LightMode) < need {
			short = true
		}
	}
	if !short {
		t.Error("CONTROL: brand itself falls short on some canvas in light, which is why the code takes brand+35 there")
	}
}
