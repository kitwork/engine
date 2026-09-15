package css

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// themes.go — appearance modes derived from one set of tokens.
//
// router.css({ theme }) declares WHAT the tokens are (Tailwind's word: the config namespace).
// router.themes({ dark: true }) declares HOW they re-value under a mode (Kitwork's word). A mode is
// one block of variables under one selector — `.dark { --color-canvas: … }` — so `bg-canvas` never
// learns that themes exist; markup does not change, and no `bg-canvas-dark` utility is minted for
// a derived value. A rung the site spells out by hand (`paper: { dark: "#1e2030" }`) always wins.
//
// The token contract the derivation fills:
//
//	surfaces  canvas · panel · paper · line     one ladder, canvas < panel < paper, line above
//	text      ink (+ deep · soft · wash)         body, headings, secondary, hairline fills
//	accents   brand · success · warning · danger · info, and any family the site adds
//	          (+ deep · soft · wash · on)
//
// The dark formula is not a mirror. It is the hand-tuned dark of kitwork.io written as a rule, and
// the oracle test holds it to those values: surfaces take the HUE OF INK ("in the dark the page is
// made of ink"), not of canvas — a warm light canvas does not become a warm dark one; text lifts
// into the light band with more chroma than light text, and the two body rungs are contrast-guarded
// on the dark canvas; accents keep their hue, lift a little, lose a little chroma, and regrow their
// tinted fills as dark tints with the label rung re-measured on them.

// Theme is one appearance mode: its selector and the token values it re-declares.
type Theme struct {
	Selector string
	Colors   map[string]Color
}

// themeModes is the table of modes the engine can derive. dark is its first row; a second mode is
// a second row, not new machinery.
var themeModes = map[string]struct {
	surfaceL map[string]float64 // lightness per surface role
	surfaceC map[string]float64 // chroma per surface role, as a share of the recovered ink seed
	textL    map[string]float64 // lightness per text rung ("" = the body rung)
	textC    map[string]float64 // chroma per text rung, as a share of the recovered ink seed
	lift     float64            // accent lightness lift
	fade     float64            // accent chroma factor
	deepL    float64            // deep = accent L + deepL, before the readable guard
	deepC    float64            // deep chroma factor on the lifted accent
	softL    float64            // tinted fill lightness
	softC    float64            // tinted fill chroma factor on the lifted accent
	washL    float64
	washC    float64
	contrast float64 // AA for body text and the deep-on-soft badge
}{
	"dark": {
		surfaceL: map[string]float64{"canvas": 0.204, "panel": 0.222, "paper": 0.249, "line": 0.317},
		surfaceC: map[string]float64{"canvas": 0.30, "panel": 0.44, "paper": 0.55, "line": 0.67},
		textL:    map[string]float64{"": 0.846, "deep": 0.942, "soft": 0.700, "wash": 0.301},
		textC:    map[string]float64{"": 1.10, "deep": 0.36, "soft": 1.07, "wash": 0.53},
		lift:     0.085, fade: 0.76,
		deepL: -0.04, deepC: 1.20,
		softL: 0.275, softC: 0.26,
		washL: 0.218, washC: 0.15,
		contrast: 4.5,
	},
}

// themeSurfaces and themeText name the contract's neutral roles; every other site family with a
// DEFAULT is an accent. Tailwind's own families and the engine's legacy default names are neither.
var (
	themeSurfaces = []string{"canvas", "panel", "paper", "line"}
	themeText     = "ink"
	themeStatus   = map[string]bool{"brand": true, "success": true, "warning": true, "danger": true, "info": true}
	themeRungs    = []string{"deep", "soft", "wash", "on"}
)

// ThemeSelector is the parent selector a mode's block lives under: dark keeps Tailwind's darkMode
// selector (`.dark` unless the site changed it); any other mode is `[data-theme="<mode>"]` — an
// attribute, so a mode named like a utility (sepia, neutral) can never collide with one.
func ThemeSelector(cfg *Config, mode string) string {
	if mode == "dark" {
		if cfg != nil && cfg.DarkSelector != "" {
			return cfg.DarkSelector
		}
		return ".dark"
	}
	return `[data-theme="` + mode + `"]`
}

// CanDeriveTheme reports whether the engine has a formula for the mode.
func CanDeriveTheme(mode string) bool { _, ok := themeModes[mode]; return ok }

// DeriveThemes fills cfg.Themes[mode] for each requested mode with the tokens the site did not
// spell out by hand, and returns one warning per mode it cannot derive. It is idempotent.
func DeriveThemes(cfg *Config, modes ...string) []string {
	if cfg == nil {
		return nil
	}
	var warnings []string
	for _, mode := range modes {
		rule, ok := themeModes[mode]
		if !ok {
			warnings = append(warnings, "router.themes(): "+mode+" is not a mode this engine can derive (dark is) — ignored")
			continue
		}
		if cfg.Themes == nil {
			cfg.Themes = map[string]map[string]Color{}
		}
		derived := map[string]Color{}
		cfg.Themes[mode] = derived
		colors := cfg.Colors
		if colors == nil {
			colors = Colors
		}
		hand := func(token string) (Color, bool) { c, ok := colors[token+"-"+mode]; return c, ok }
		set := func(token, hex string) {
			if _, byHand := hand(token); byHand {
				return
			}
			derived[token] = Hex(hex)
		}
		value := func(token string) (oklch, bool) {
			if c, ok := hand(token); ok {
				return colorOklch(c), true
			}
			if c, ok := derived[token]; ok {
				return colorOklch(c), true
			}
			return oklch{}, false
		}

		// ---- the ink seed: hue from the body text, chroma recovered from pigment's reduction ----
		seed := oklch{L: 0.27, C: 0, H: 280}
		if ink, ok := colors[themeText]; ok {
			c := colorOklch(ink)
			seed = oklch{L: c.L, C: math.Min(c.C*5, 0.08), H: c.H}
		}

		// ---- surfaces: the ink hue, one ladder ----
		for _, role := range themeSurfaces {
			if _, declared := colors[role]; !declared {
				continue
			}
			set(role, oklchHex(oklch{L: rule.surfaceL[role], C: math.Min(seed.C*rule.surfaceC[role], 0.045), H: seed.H}))
		}
		canvas, hasCanvas := value("canvas")
		var canvasR, canvasG, canvasB float64
		if hasCanvas {
			canvasR, canvasG, canvasB, _ = parseHexRGB(oklchHex(canvas))
		} else {
			canvasR, canvasG, canvasB, _ = parseHexRGB(oklchHex(oklch{L: rule.surfaceL["canvas"], C: 0, H: seed.H}))
		}

		// ---- text: lifted into the light band, body rungs readable on the dark canvas ----
		if _, declared := colors[themeText]; declared {
			for rung, l := range rule.textL {
				token := themeText
				if rung != "" {
					token += "-" + rung
					if _, declared := colors[token]; !declared {
						continue
					}
				}
				c := oklch{L: l, C: seed.C * rule.textC[rung], H: seed.H}
				hex := oklchHex(c)
				if rung == "" || rung == "soft" {
					hex = readableHex(c, canvasR, canvasG, canvasB, rule.contrast)
				}
				set(token, hex)
			}
		}

		// ---- accents: keep the hue, lift, fade; regrow the tinted fills as dark tints ----
		for _, family := range accentFamilies(colors) {
			base := colorOklch(colors[family])
			lifted := oklch{L: math.Min(base.L+rule.lift, 0.90), C: base.C * rule.fade, H: base.H}
			set(family, oklchHex(lifted))
			soft := oklch{L: rule.softL, C: lifted.C * rule.softC, H: base.H}
			if _, declared := colors[family+"-soft"]; declared {
				set(family+"-soft", oklchHex(soft))
			}
			if _, declared := colors[family+"-wash"]; declared {
				set(family+"-wash", oklchHex(oklch{L: rule.washL, C: lifted.C * rule.washC, H: base.H}))
			}
			if _, declared := colors[family+"-deep"]; declared {
				sr, sg, sb, _ := parseHexRGB(oklchHex(soft))
				if s, ok := value(family + "-soft"); ok {
					sr, sg, sb, _ = parseHexRGB(oklchHex(s))
				}
				deep := oklch{L: lifted.L + rule.deepL, C: math.Min(lifted.C*rule.deepC, 0.33), H: base.H}
				set(family+"-deep", readableHex(deep, sr, sg, sb, rule.contrast))
			}
			if _, declared := colors[family+"-on"]; declared {
				fill := lifted
				if f, ok := value(family); ok {
					fill = f
				}
				fr, fg, fb, _ := parseHexRGB(oklchHex(fill))
				set(family+"-on", labelOn(fill, fr, fg, fb, rule.contrast))
			}
		}
	}
	return warnings
}

// accentFamilies lists the site's accent families: the status set, plus every family the site
// declared itself — never a rung, never a mode value, never Tailwind's or the engine's default names.
func accentFamilies(colors map[string]Color) []string {
	var out []string
	for name := range colors {
		if name == themeText || strings.Contains(name, "-") {
			continue
		}
		if _, isPalette := TwPalette[name]; isPalette {
			continue
		}
		isSurface := false
		for _, s := range themeSurfaces {
			if name == s {
				isSurface = true
			}
		}
		if isSurface {
			continue
		}
		if _, legacy := DefaultConfig.Colors[name]; legacy && !themeStatus[name] {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// labelOn is palette()'s `on` rule for an arbitrary fill: white when it clears the target, else a
// label darkened in the fill's hue until it actually does.
func labelOn(fill oklch, r, g, b, target float64) string {
	if contrastRatio(255, 255, 255, r, g, b) >= target {
		return "#ffffff"
	}
	chroma := math.Min(fill.C*0.22, 0.05)
	on := "#000000"
	for l := 0.20; l >= 0.04; l -= 0.02 {
		on = oklchHex(oklch{L: l, C: chroma, H: fill.H})
		dr, dg, db, _ := parseHexRGB(on)
		if contrastRatio(dr, dg, db, r, g, b) >= target {
			break
		}
	}
	return on
}

func colorOklch(c Color) oklch { return rgbToOklch(float64(c.R), float64(c.G), float64(c.B)) }

// themeBlocks renders every mode's variable block: the hand-written `<token>-<mode>` rungs and the
// derived values, hand first so it wins. Empty when the site has neither.
func themeBlocks(cfg *Config, tokenColors map[string]Color, tokenKeys []string) string {
	modes := map[string]bool{}
	for _, k := range tokenKeys {
		if strings.HasSuffix(k, "-dark") {
			if _, ok := tokenColors[strings.TrimSuffix(k, "-dark")]; ok {
				modes["dark"] = true
			}
		}
	}
	if cfg != nil {
		for mode, colors := range cfg.Themes {
			if len(colors) > 0 {
				modes[mode] = true
			}
		}
	}
	if len(modes) == 0 {
		return ""
	}
	names := make([]string, 0, len(modes))
	for mode := range modes {
		names = append(names, mode)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, mode := range names {
		values := map[string]Color{}
		if cfg != nil {
			for token, c := range cfg.Themes[mode] {
				values[token] = c
			}
		}
		for _, k := range tokenKeys { // hand-written rungs overwrite derived ones
			if base, ok := strings.CutSuffix(k, "-"+mode); ok {
				if _, declared := tokenColors[base]; declared {
					values[base] = tokenColors[k]
				}
			}
		}
		tokens := make([]string, 0, len(values))
		for token := range values {
			tokens = append(tokens, token)
		}
		sort.Strings(tokens)
		b.WriteString(ThemeSelector(cfg, mode) + " {")
		for _, token := range tokens {
			b.WriteString(fmt.Sprintf(" --color-%s: %s;", token, values[token].String()))
		}
		b.WriteString(" }\n")
	}
	return b.String()
}
