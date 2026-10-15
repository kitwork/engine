package stylesheet

import (
	"strings"
	"testing"
)

// engineLight are the steps the engine wrote for both modes before the per-mode rule (13/10/2026), on
// the five palettes the code colours were measured on (codePalettes): light keeps them byte for byte.
var engineLight = map[string]map[string]string{
	"": { // every palette: the roles that do not depend on the accent
		CodeColour("brand"): "brand+35", CodeColour("success"): "success+35", CodeColour("warning"): "warning+55",
		CodeColour("info"): "info+25", CodeColour("danger"): "danger+25", CodeColour("inserted"): "success+45",
		CodeColour("deleted"): "danger+40", ErrorColour: "danger+25", HeadlineColour: "brand+40", InverseColour: "brand+40",
	},
	"Kit Design": {BadgeColour("brand"): "brand+15", BadgeColour("success"): "success+35", BadgeColour("warning"): "warning+55",
		BadgeColour("danger"): "danger+25", BadgeColour("info"): "info+20"},
	"kitwork": {BadgeColour("brand"): "brand+25", BadgeColour("success"): "success+35", BadgeColour("warning"): "warning+55",
		BadgeColour("danger"): "danger+30", BadgeColour("info"): "info+30"},
	"kitwork (canvas #ffeaec)": {BadgeColour("brand"): "brand+35", BadgeColour("success"): "success+45", BadgeColour("warning"): "warning+65",
		BadgeColour("danger"): "danger+40", BadgeColour("info"): "info+35"},
	"quancaphe": {BadgeColour("brand"): "brand+40", BadgeColour("success"): "success+40", BadgeColour("warning"): "warning+60",
		BadgeColour("danger"): "danger+30", BadgeColour("info"): "info+20"},
	"quancaphe/quan-ly": {BadgeColour("brand"): "brand+15", BadgeColour("success"): "success+40", BadgeColour("warning"): "warning+60",
		BadgeColour("danger"): "danger+30", BadgeColour("info"): "info+20"},
}

// roleOf is the role an engine colour is worked out by.
func roleOf(name string) engineRole {
	if r, ok := engineRoles[name]; ok {
		return r
	}
	return badgeRole(strings.TrimPrefix(name, "kitwork-badge-"))
}

// Every engine colour takes its +N per mode (decision of 13/10/2026). On the five palettes measured:
// light is the step the engine wrote before, for every colour (Kit Design's hexes pinned: C-1 of the
// plan); each mode's value reads 4.5:1 on the role's grounds and is the least multiple of 5 from that
// mode's floor that does; dark is the accent itself on all five, its hue kept (lightness under 0.95,
// chroma at least 30% of the token's). CONTROL: the palette has every role and a badge text for each
// accent that grows a label, fifteen in all, so a role dropped from engineColours fails here; and the
// one step of before, used in dark, loses its hue (warning+55 is #ffffff there).
func TestEngineColoursPerMode(t *testing.T) {
	kitLight := map[string]string{
		BadgeColour("brand"): "#5537fc", BadgeColour("success"): "#007642", BadgeColour("warning"): "#8c5c00",
		BadgeColour("danger"): "#ce0029", BadgeColour("info"): "#0064b2", CodeColour("brand"): "#4500de",
		CodeColour("success"): "#007642", CodeColour("warning"): "#8c5c00", CodeColour("info"): "#005fa9",
		CodeColour("danger"): "#ce0029", CodeColour("inserted"): "#006a3a", CodeColour("deleted"): "#b30022",
		ErrorColour: "#ce0029", HeadlineColour: "#4000d1", InverseColour: "#4000d1",
	}
	for name, p := range codePalettes(t) {
		accents := 0
		for _, token := range p.Tokens() {
			if accent(token) && p.Has(token+".on") {
				accents++
			}
		}
		if len(p.engine) != len(engineRoles)+accents || len(p.engine) != 15 {
			t.Errorf("%s: %d engine colours, want the %d roles and a badge for each of %d accents", name, len(p.engine), len(engineRoles), accents)
		}
		for engine := range p.engine {
			role := roleOf(engine)
			want := engineLight[""][engine]
			if want == "" {
				want = engineLight[name][engine]
			}
			light, _ := p.Engine(engine, LightMode)
			if light != want {
				t.Errorf("%s: %s is %s in light, want %s as before", name, engine, light, want)
			}
			if name == "Kit Design" {
				if painted, _ := p.Paint(engine, LightMode); painted.Hex() != kitLight[engine] {
					t.Errorf("Kit Design: %s is %s in light, want %s as before", engine, painted.Hex(), kitLight[engine])
				}
			}
			for _, m := range p.Modes() {
				value, _ := p.Engine(engine, m)
				if !p.readsIn(value, role.on, NormalTextRatio, m) {
					t.Errorf("%s: %s %s %s does not read on %v", name, engine, m.Word(), value, role.on)
				}
				c, _ := SplitColour(value)
				if floor := p.floorIn(role, m); c.Amount < floor || c.Amount > floor && p.readsIn(spell(role.token, c.Amount-5), role.on, NormalTextRatio, m) {
					t.Errorf("%s: %s %s %s is not the smallest step from %d that reads", name, engine, m.Word(), value, floor)
				}
			}
			dark, _ := p.Engine(engine, DarkMode)
			painted, _ := p.Paint(dark, DarkMode)
			base, _ := p.Paint(role.token, DarkMode)
			l, chroma := OklabLightnessChroma(painted)
			_, baseChroma := OklabLightnessChroma(base)
			if l >= 0.95 || chroma < 0.3*baseChroma {
				t.Errorf("%s: %s dark %s %s has lost its hue: lightness %.3f, chroma %.0f%% of %s", name, engine, dark, painted.Hex(), l, 100*chroma/baseChroma, role.token)
			}
			if dark != role.token {
				t.Errorf("%s: %s is %s in dark, want %s itself, as measured on these palettes", name, engine, dark, role.token)
			}
		}
	}
	// CONTROL: before, warning+55 was the code's warning in dark too: white, no hue
	kit, _ := NewPalette(true)
	if white, _ := kit.Paint("warning+55", DarkMode); white.Hex() != "#ffffff" {
		t.Errorf("CONTROL: warning+55 in dark on Kit Design is %s, the plan measured #ffffff", white.Hex())
	}
}

// Every role's light step is the least that reads on one of the palettes measured: five less falls
// short on at least one, in light. inverse is the exception: it keeps the step it shared with headline
// (40), though 35 reads on all five, so the two ends of the brand stay one colour in light.
func TestEngineLightStepsAreTheLeast(t *testing.T) {
	palettes := codePalettes(t)
	for name, role := range engineRoles {
		less := spell(role.token, role.step-5)
		short := false
		for _, p := range palettes {
			short = short || !p.readsIn(less, role.on, NormalTextRatio, LightMode)
		}
		if name == InverseColour {
			if short {
				t.Errorf("%s: %s falls short somewhere: the inverse exception is no longer needed", name, less)
			}
			continue
		}
		if !short {
			t.Errorf("%s starts from %d in light, but %s reads on every palette", name, role.step, less)
		}
	}
}

// The floor is keyed by the canvas, not by the mode's name (decision of 13/10/2026): a palette whose
// only look has a dark canvas starts every engine colour from 0, so its code keeps its hues; a light
// look from the role's step. When no step up to +100 reads, the floor stays and the contrast check tells
// it: a site info so light that nothing reads on its panel keeps info+25 in light, not info+100.
// CONTROL: from the role's step, the dark canvas's warning is a near-white #ffe8c9.
func TestEngineFloorFollowsTheCanvas(t *testing.T) {
	night, err := NewPalette(false, map[string]Color{
		"canvas": {Light: "#16161e"}, "paper": {Light: "#1e2030"}, "panel": {Light: "#181a26"},
		"ink": {Light: "#c0caf5"}, "line": {Light: "#2c3145"}, "brand": {Light: "#ff6b6b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// the least step from 0: warning and brand themselves, danger +10 (under the role's 25)
	for name, want := range map[string]string{CodeColour("warning"): "warning", CodeColour("brand"): "brand", ErrorColour: "danger+10"} {
		if got, _ := night.Engine(name, LightMode); got != want {
			t.Errorf("a light look on a dark canvas: %s is %s, want %s", name, got, want)
		}
		if got, _ := night.Engine(name, DarkMode); got != want {
			t.Errorf("no dark look: %s in dark is its light value %s, got %s", name, want, got)
		}
	}
	if from := spell("warning", engineRoles[CodeColour("warning")].step); !night.readsIn(from, pageGrounds, NormalTextRatio, LightMode) {
		t.Errorf("CONTROL: %s does not read on the dark canvas", from)
	} else if c, _ := night.Paint(from, LightMode); c.Hex() != "#ffe8c9" {
		t.Errorf("CONTROL: %s on the dark canvas is %s, measured #ffe8c9", from, c.Hex())
	}

	pale, err := NewPalette(true, map[string]Color{"info": {Light: "#a8c7fa", Dark: "#a8c7fa"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := pale.Engine(CodeColour("info"), LightMode); got != "info+25" {
		t.Errorf("nothing reads on the panel in light: kitwork-code-info is %s, want the floor info+25 kept", got)
	}
	if pale.readsIn("info+100", pageGrounds, NormalTextRatio, LightMode) {
		t.Error("CONTROL: info+100 reads on canvas, paper and panel: the floor is not what is kept")
	}
	if got, _ := pale.Engine(CodeColour("info"), DarkMode); got != "info" {
		t.Errorf("info reads in dark: kitwork-code-info is %s there, want info", got)
	}
}

// An engine colour and the author's X+N with the same light step are two custom properties: the
// engine's takes its value per mode, the author's keeps one step in both. On Kit Design a badge of
// warning and color="warning+55" share #8c5c00 in light; in dark the badge is warning #daa965, the
// author's warning+55 #ffffff still (the grammar is untouched: CONTROL).
func TestEngineAndAuthorStrongAreApart(t *testing.T) {
	kit, _ := NewPalette(true)
	table := NewTable(kit)
	r := &Rules{}
	for _, in := range []Intent{{Tag: "badge", Name: "color", Value: "warning"}, {Name: "color", Value: "warning+55"}} {
		if _, err := table.Class(r, in); err != nil {
			t.Fatal(err)
		}
	}
	css := string(r.CSS())
	for _, want := range []string{
		":root{--kitwork-badge-warning:#8c5c00;--kitwork-strong-55-warning:#8c5c00}\n",
		"@media screen{:root[data-theme=dark]{--kitwork-badge-warning:#daa965;--kitwork-strong-55-warning:#ffffff}}\n",
		"color:var(--kitwork-badge-warning)}",
		".color-warning\\+55{color:var(--kitwork-strong-55-warning)}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %q in\n%s", want, css)
		}
	}
}

// An engine colour's name is no colour an author may write: color="kitwork-error" and its forms are
// refused, a router.gradients stop kitwork-headline is refused, and router.colors cannot declare
// kitwork.error (its variable would be --kitwork-error). CONTROL: Paint paints the name, so the refusal
// is the table's, not a colour it cannot paint.
func TestEngineColoursAreNotAuthorWords(t *testing.T) {
	kit, _ := NewPalette(true)
	table := NewTable(kit)
	for _, v := range []string{"kitwork-error", "kitwork-badge-warning", "kitwork-code-brand+10", "kitwork-headline/50", "kitwork-inverse-20"} {
		if _, err := table.Class(&Rules{}, Intent{Name: "color", Value: v}); err == nil {
			t.Errorf("color=%q compiles; an engine colour is not the author's", v)
		}
	}
	if _, err := kit.WithGradients(map[string]Gradient{"hero": {Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "kitwork-headline", At: 0}, {Color: "brand", At: 100}}}}); err == nil {
		t.Error("a router.gradients stop takes kitwork-headline")
	}
	if _, err := NewPalette(false, map[string]Color{"kitwork.error": {Light: "#123456"}}); err == nil {
		t.Error("router.colors declares kitwork.error, the variable --kitwork-error")
	}
	if _, ok := kit.Paint(ErrorColour, DarkMode); !ok {
		t.Error("CONTROL: Paint does not paint kitwork-error")
	}
	if _, ok := kit.Engine("danger+25", LightMode); ok {
		t.Error("an author's danger+25 is no engine colour")
	}
}
