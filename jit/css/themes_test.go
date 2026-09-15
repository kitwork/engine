package css

import (
	"math"
	"strings"
	"testing"
)

// kitworkLight is kitwork.io's token declaration with every hand-tuned dark rung removed — exactly
// what a site writes before it has a dark theme. The oracle below asks the derivation to land
// within tolerance of the twelve values Quốc tuned by hand for the real site (router.kitwork.js,
// 2026-09): if the rule cannot reproduce a human's dark, the rule is wrong, not the human.
func kitworkLight() Config {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["canvas"] = Hex("#ffeaec")
	cfg.Colors["paper"] = Hex("#ffffff")
	cfg.Colors["panel"] = Hex("#efeff1")
	cfg.Colors["line"] = Hex("#e5e3e5")
	for rung, hex := range Pigment("#242442", "#ffeaec") {
		cfg.Colors[rungKey("ink", rung)] = Hex(hex)
	}
	for rung, hex := range Palette("#F5225F") {
		cfg.Colors[rungKey("brand", rung)] = Hex(hex)
	}
	cfg.Colors["success"], cfg.Colors["success-soft"], cfg.Colors["success-deep"] = Hex("#0e9f6e"), Hex("#def7ec"), Hex("#046c4e")
	return cfg
}

func rungKey(family, rung string) string {
	if rung == "DEFAULT" {
		return family
	}
	return family + "-" + rung
}

var kitworkHandDark = map[string]string{
	"canvas": "#16161e", "panel": "#181a26", "paper": "#1e2030", "line": "#2c3145",
	"ink": "#c0caf5", "ink-deep": "#e8ebfa", "ink-soft": "#949cc4", "ink-wash": "#2a2d3d",
	"brand": "#ff6b6b", "brand-deep": "#ff4d4d", "brand-soft": "#3b1d22", "brand-wash": "#251518",
}

func TestDeriveDarkReproducesTheHandTunedDark(t *testing.T) {
	cfg := kitworkLight()
	if w := DeriveThemes(&cfg, "dark"); len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
	dark := cfg.Themes["dark"]
	// Lightness and chroma are what the rule claims to reproduce, so they are held tight. Hue is
	// looser because the hand-tuned dark brand is WARMED by taste — #ff6b6b / #ff4d4d sit 10–12°
	// toward orange from the light #f5225f — and the rule keeps a family's hue on purpose: a
	// derivation that rotated hues would be imitating one person's palette, not stating a rule.
	const dL, dC, dH = 0.04, 0.03, 15.0
	for token, want := range kitworkHandDark {
		got, ok := dark[token]
		if !ok {
			t.Errorf("%s: not derived", token)
			continue
		}
		g, w := colorOklch(got), colorOklch(Hex(want))
		hueOff := math.Abs(math.Mod(g.H-w.H+540, 360) - 180)
		if g.C < 0.02 && w.C < 0.02 {
			hueOff = 0 // hue is meaningless on near-greys
		}
		if math.Abs(g.L-w.L) > dL || math.Abs(g.C-w.C) > dC || hueOff > dH {
			t.Errorf("%-10s derived %s (L%.3f C%.3f H%.0f) vs hand %s (L%.3f C%.3f H%.0f)",
				token, got.HexString(), g.L, g.C, g.H, want, w.L, w.C, w.H)
		}
	}
	// The ladder must hold in the derived skin: canvas < panel < paper < line.
	l := func(token string) float64 { return colorOklch(dark[token]).L }
	if !(l("canvas") < l("panel") && l("panel") < l("paper") && l("paper") < l("line")) {
		t.Errorf("surface ladder broken: canvas %.3f panel %.3f paper %.3f line %.3f", l("canvas"), l("panel"), l("paper"), l("line"))
	}
	// Surfaces are made of ink, not of the warm canvas.
	if h := colorOklch(dark["paper"]).H; math.Abs(h-colorOklch(cfg.Colors["ink"]).H) > 10 {
		t.Errorf("dark paper hue %.0f should follow ink (%.0f), not canvas (%.0f)", h, colorOklch(cfg.Colors["ink"]).H, colorOklch(cfg.Colors["canvas"]).H)
	}
}

// Whatever the seed, the two body text rungs clear AA on the derived canvas, and the badge pair
// (deep on soft) does too — the guards, not the constants, are what make an unseen hue safe.
func TestDeriveDarkGuardsContrast(t *testing.T) {
	for _, seed := range []string{"#0e9f6e", "#635bff", "#c98a1e", "#808080"} {
		cfg := DefaultConfig
		cfg.Colors = map[string]Color{"canvas": Hex("#f6f9fc"), "paper": Hex("#ffffff")}
		for rung, hex := range Pigment(seed, "#f6f9fc") {
			cfg.Colors[rungKey("ink", rung)] = Hex(hex)
		}
		for rung, hex := range Palette(seed) {
			cfg.Colors[rungKey("brand", rung)] = Hex(hex)
		}
		DeriveThemes(&cfg, "dark")
		dark := cfg.Themes["dark"]
		rgb := func(c Color) (float64, float64, float64) { return float64(c.R), float64(c.G), float64(c.B) }
		cr, cg, cb := rgb(dark["canvas"])
		for _, token := range []string{"ink", "ink-soft"} {
			r, g, b := rgb(dark[token])
			if ratio := contrastRatio(r, g, b, cr, cg, cb); ratio < 4.5 {
				t.Errorf("seed %s: %s on dark canvas is %.2f:1", seed, token, ratio)
			}
		}
		sr, sg, sb := rgb(dark["brand-soft"])
		dr, dg, db := rgb(dark["brand-deep"])
		if ratio := contrastRatio(dr, dg, db, sr, sg, sb); ratio < 4.5 {
			t.Errorf("seed %s: brand-deep on brand-soft is %.2f:1", seed, ratio)
		}
		fr, fg, fb := rgb(dark["brand"])
		or, og, ob := rgb(dark["brand-on"])
		if ratio := contrastRatio(or, og, ob, fr, fg, fb); ratio < 4.5 {
			t.Errorf("seed %s: brand-on on brand is %.2f:1", seed, ratio)
		}
	}
}

// CONTROL 1: a rung the site wrote by hand is never overwritten — hand-tuning stays possible.
func TestDeriveDarkHandWins(t *testing.T) {
	cfg := kitworkLight()
	cfg.Colors["paper-dark"] = Hex("#101010")
	DeriveThemes(&cfg, "dark")
	if _, derived := cfg.Themes["dark"]["paper"]; derived {
		t.Fatal("paper has a hand-written dark rung; it must not be derived")
	}
	css := buildJITCSS([]string{"bg-paper"}, &cfg)
	block := css[strings.Index(css, ".dark {"):]
	block = block[:strings.Index(block, "}")]
	if !strings.Contains(block, "--color-paper: 16, 16, 16;") {
		t.Fatalf("the hand value must be the one emitted:\n%s", block)
	}
}

// CONTROL 2: Tailwind's families and the engine's legacy default names are not accents to derive.
func TestDeriveDarkSkipsForeignFamilies(t *testing.T) {
	cfg := kitworkLight()
	DeriveThemes(&cfg, "dark")
	for _, name := range []string{"blue", "gold", "lime", "primary", "white", "black", "gray"} {
		if _, ok := cfg.Themes["dark"][name]; ok {
			t.Errorf("%s must not be derived", name)
		}
	}
	// … while the status set and a site-declared family are.
	for _, name := range []string{"success", "success-soft", "success-deep", "brand-on"} {
		if _, ok := cfg.Themes["dark"][name]; !ok {
			t.Errorf("%s should be derived", name)
		}
	}
}

// CONTROL 3: a derived value lives in the .dark block only — no utility is minted from it.
func TestDeriveDarkMintsNoUtility(t *testing.T) {
	cfg := kitworkLight()
	DeriveThemes(&cfg, "dark")
	css := buildJITCSS([]string{"bg-canvas", "bg-canvas-dark"}, &cfg)
	if !strings.Contains(css, ".dark { --color-brand") && !strings.Contains(css, ".dark { --color-canvas") {
		t.Fatalf("no .dark block emitted:\n%s", css)
	}
	if strings.Contains(css, ".bg-canvas-dark") {
		t.Fatal("bg-canvas-dark must not exist for a derived value")
	}
	if !strings.Contains(css, ".bg-canvas {") {
		t.Fatal("bg-canvas must still resolve")
	}
}

// An unknown mode is a named warning, not a silent nothing; and a second mode gets an attribute
// selector so it can never collide with a utility name.
func TestDeriveThemesUnknownModeAndSelectors(t *testing.T) {
	cfg := kitworkLight()
	w := DeriveThemes(&cfg, "sepia")
	if len(w) != 1 || !strings.Contains(w[0], "sepia") {
		t.Fatalf("want one warning naming sepia, got %v", w)
	}
	if ThemeSelector(&cfg, "dark") != ".dark" || ThemeSelector(&cfg, "sepia") != `[data-theme="sepia"]` {
		t.Fatalf("selectors: %q %q", ThemeSelector(&cfg, "dark"), ThemeSelector(&cfg, "sepia"))
	}
	cfg.DarkSelector = `[data-theme="dark"]`
	if ThemeSelector(&cfg, "dark") != `[data-theme="dark"]` {
		t.Fatal("dark must honour darkMode's selector")
	}
}
