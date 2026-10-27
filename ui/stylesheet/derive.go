package stylesheet

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Colour derivation in OKLCH: the label an accent grows on its fill (onLabel), the dark value of every
// token the site did not write one for (deriveDark) and the contrast the focus ring is picked with
// (contrastHex). These were jit/css's (Palette's on, DeriveThemes' dark row, Contrast) until
// 27/10/2026; they moved here with their formulas and constants unchanged, so every colour a palette
// makes is the same, and a .ui page no longer reaches the template pages' CSS engine. OKLCH, not HSL:
// its L is perceptual, so one set of lightness targets reads evenly across every hue.

// ---- sRGB <-> OKLCH (Björn Ottosson's OKLab matrices) ----

func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func linearToSrgb(c float64) float64 {
	if c <= 0.0031308 {
		return 12.92 * c
	}
	return 1.055*math.Pow(c, 1.0/2.4) - 0.055
}

type oklch struct{ L, C, H float64 }

func rgbToOklch(r, g, b float64) oklch {
	lr, lg, lb := srgbToLinear(r/255), srgbToLinear(g/255), srgbToLinear(b/255)
	l := 0.4122214708*lr + 0.5363325363*lg + 0.0514459929*lb
	m := 0.2119034982*lr + 0.6806995451*lg + 0.1073969566*lb
	s := 0.0883024619*lr + 0.2817188376*lg + 0.6299787005*lb
	l_, m_, s_ := math.Cbrt(l), math.Cbrt(m), math.Cbrt(s)
	L := 0.2104542553*l_ + 0.7936177850*m_ - 0.0040720468*s_
	A := 1.9779984951*l_ - 2.4285922050*m_ + 0.4505937099*s_
	B := 0.0259040371*l_ + 0.7827717662*m_ - 0.8086757660*s_
	H := math.Atan2(B, A) * 180 / math.Pi
	if H < 0 {
		H += 360
	}
	return oklch{L: L, C: math.Hypot(A, B), H: H}
}

func oklchToRGB(c oklch) (float64, float64, float64) {
	hr := c.H * math.Pi / 180
	A, B := math.Cos(hr)*c.C, math.Sin(hr)*c.C
	l_ := c.L + 0.3963377774*A + 0.2158037573*B
	m_ := c.L - 0.1055613458*A - 0.0638541728*B
	s_ := c.L - 0.0894841775*A - 1.2914855480*B
	l, m, s := l_*l_*l_, m_*m_*m_, s_*s_*s_
	return linearToSrgb(4.0767416621*l-3.3077115913*m+0.2309699292*s) * 255,
		linearToSrgb(-1.2684380046*l+2.6097574011*m-0.3413193965*s) * 255,
		linearToSrgb(-0.0041960863*l-0.7034186147*m+1.7076147010*s) * 255
}

// oklchHex writes an OKLCH colour as #rrggbb, backing chroma off until it fits inside sRGB: clipping
// each channel instead would shift the hue.
func oklchHex(c oklch) string {
	step := c.C / 24
	for chroma := c.C; chroma > 0; chroma -= step {
		r, g, b := oklchToRGB(oklch{L: c.L, C: chroma, H: c.H})
		if inSRGB(r) && inSRGB(g) && inSRGB(b) {
			return rgbHex(r, g, b)
		}
	}
	r, g, b := oklchToRGB(oklch{L: c.L, C: 0, H: c.H})
	return rgbHex(r, g, b)
}

func inSRGB(v float64) bool { return v >= -0.5 && v <= 255.5 }

func rgbHex(r, g, b float64) string {
	clamp := func(v float64) int {
		i := int(math.Round(v))
		if i < 0 {
			return 0
		}
		if i > 255 {
			return 255
		}
		return i
	}
	return fmt.Sprintf("#%02x%02x%02x", clamp(r), clamp(g), clamp(b))
}

// parseHexRGB reads #rgb or #rrggbb, the hash optional; ok is false for anything else.
func parseHexRGB(hex string) (r, g, b float64, ok bool) {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff), true
}

// rgb is a colour as three bytes, as a token holds it between the steps of the dark formula.
type rgb struct{ R, G, B int }

// rgbOf reads a #rgb or #rrggbb the palette already checked.
func rgbOf(h string) rgb {
	h = strings.TrimPrefix(h, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	val, _ := strconv.ParseUint(h, 16, 32)
	return rgb{int(val >> 16), int((val >> 8) & 0xFF), int(val & 0xFF)}
}

func (c rgb) hex() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func (c rgb) oklch() oklch { return rgbToOklch(float64(c.R), float64(c.G), float64(c.B)) }

// ---- WCAG contrast ----

func relLuminance(r, g, b float64) float64 {
	f := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*f(r) + 0.7152*f(g) + 0.0722*f(b)
}

func contrastRatio(r1, g1, b1, r2, g2, b2 float64) float64 {
	a, b := relLuminance(r1, g1, b1)+0.05, relLuminance(r2, g2, b2)+0.05
	if a > b {
		return a / b
	}
	return b / a
}

// contrastHex is WCAG 2's contrast ratio of two colours written #rgb or #rrggbb, from 1 to 21; 0 when
// either is not a colour it can read, so a check against a minimum fails.
func contrastHex(a, b string) float64 {
	r1, g1, b1, ok1 := parseHexRGB(a)
	r2, g2, b2, ok2 := parseHexRGB(b)
	if !ok1 || !ok2 {
		return 0
	}
	return contrastRatio(r1, g1, b1, r2, g2, b2)
}

// readableHex moves L until the colour clears target contrast on the background: darker on a light
// one, lighter on a dark one.
func readableHex(c oklch, bgR, bgG, bgB, target float64) string {
	step := 0.03
	if relLuminance(bgR, bgG, bgB) <= 0.35 {
		step = -0.03 // dark background: move the text lighter instead
	}
	l := c.L
	for i := 0; i < 24; i++ {
		hex := oklchHex(oklch{L: l, C: c.C, H: c.H})
		r, g, b, _ := parseHexRGB(hex)
		if contrastRatio(r, g, b, bgR, bgG, bgB) >= target {
			return hex
		}
		l -= step
		if l <= 0.04 || l >= 0.98 {
			break
		}
	}
	return oklchHex(oklch{L: math.Min(0.98, math.Max(0.04, l)), C: c.C, H: c.H})
}

// onLabel is the label an accent grows on its solid fill, its on: white when white clears AA (4.5:1) on
// the hue, else a dark label in the hue, darkened until it actually clears AA (a fixed lightness stalls
// at 4.3 on mid-tone hues). "" for a colour it cannot read.
func onLabel(hex string) string {
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		return ""
	}
	c := rgbToOklch(r, g, b)
	on := "#ffffff"
	if contrastRatio(255, 255, 255, r, g, b) < 4.5 {
		darkChroma := math.Min(c.C*0.22, 0.05)
		for l := 0.20; l >= 0.04; l -= 0.02 {
			on = oklchHex(oklch{L: l, C: darkChroma, H: c.H})
			dr, dg, db, _ := parseHexRGB(on)
			if contrastRatio(dr, dg, db, r, g, b) >= 4.5 {
				break
			}
		}
	}
	return on
}

// labelOn is onLabel's rule for an arbitrary fill: white when it clears the target, else a label
// darkened in the fill's hue until it does.
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

// The dark formula. It is not a mirror but the hand-tuned dark of kitwork.io written as a rule:
// surfaces take the hue of ink ("in the dark the page is made of ink"); text lifts into the light band,
// its body rungs readable on the dark canvas; accents keep their hue, lift a little, lose a little
// chroma, and regrow their tinted fills as dark tints with the label re-measured on them.
var (
	darkSurfaceL = map[string]float64{"canvas": 0.204, "panel": 0.222, "paper": 0.249, "line": 0.317}
	darkSurfaceC = map[string]float64{"canvas": 0.30, "panel": 0.44, "paper": 0.55, "line": 0.67}
	darkTextL    = map[string]float64{"": 0.846, "deep": 0.942, "soft": 0.700, "wash": 0.301}
	darkTextC    = map[string]float64{"": 1.10, "deep": 0.36, "soft": 1.07, "wash": 0.53}
)

const (
	darkLift     = 0.085 // accent lightness lift
	darkFade     = 0.76  // accent chroma factor
	darkDeepL    = -0.04 // deep = accent L + darkDeepL, before the readable guard
	darkDeepC    = 1.20  // deep chroma factor on the lifted accent
	darkSoftL    = 0.275 // tinted fill lightness
	darkSoftC    = 0.26  // tinted fill chroma factor on the lifted accent
	darkWashL    = 0.218
	darkWashC    = 0.15
	darkContrast = 4.5 // AA for body text and the label
)

// darkSurfaces names the formula's surfaces, in the order it derives them; ink is its text.
var darkSurfaces = []string{"canvas", "panel", "paper", "line"}

// deriveDark is the dark value of every token of colors the formula reaches and no <token>-dark in colors
// already gives. colors holds each token's light value under the formula's role names — canvas, panel,
// paper, line, ink, and each accent family under a name with no dash, its rungs as <family>-<rung> —
// and the dark values written by hand as <token>-dark. A name with a dash that is no rung of a family
// gets nothing.
func deriveDark(colors map[string]rgb) map[string]rgb {
	derived := map[string]rgb{}
	hand := func(token string) (rgb, bool) { c, ok := colors[token+"-dark"]; return c, ok }
	set := func(token, hex string) {
		if _, byHand := hand(token); byHand {
			return
		}
		derived[token] = rgbOf(hex)
	}
	value := func(token string) (oklch, bool) {
		if c, ok := hand(token); ok {
			return c.oklch(), true
		}
		if c, ok := derived[token]; ok {
			return c.oklch(), true
		}
		return oklch{}, false
	}

	// the ink seed: hue from the body text, chroma recovered from the text ramp's reduction
	seed := oklch{L: 0.27, C: 0, H: 280}
	if ink, ok := colors["ink"]; ok {
		c := ink.oklch()
		seed = oklch{L: c.L, C: math.Min(c.C*5, 0.08), H: c.H}
	}

	// surfaces: the ink hue, one ladder
	for _, role := range darkSurfaces {
		if _, declared := colors[role]; !declared {
			continue
		}
		set(role, oklchHex(oklch{L: darkSurfaceL[role], C: math.Min(seed.C*darkSurfaceC[role], 0.045), H: seed.H}))
	}
	canvas, hasCanvas := value("canvas")
	var canvasR, canvasG, canvasB float64
	if hasCanvas {
		canvasR, canvasG, canvasB, _ = parseHexRGB(oklchHex(canvas))
	} else {
		canvasR, canvasG, canvasB, _ = parseHexRGB(oklchHex(oklch{L: darkSurfaceL["canvas"], C: 0, H: seed.H}))
	}

	// text: lifted into the light band, the body rungs readable on the dark canvas
	if _, declared := colors["ink"]; declared {
		for rung, l := range darkTextL {
			token := "ink"
			if rung != "" {
				token += "-" + rung
				if _, declared := colors[token]; !declared {
					continue
				}
			}
			c := oklch{L: l, C: seed.C * darkTextC[rung], H: seed.H}
			hex := oklchHex(c)
			if rung == "" || rung == "soft" {
				hex = readableHex(c, canvasR, canvasG, canvasB, darkContrast)
			}
			set(token, hex)
		}
	}

	// accents: keep the hue, lift, fade; regrow the tinted fills as dark tints
	for family := range colors {
		if family == "ink" || strings.Contains(family, "-") || isDarkSurface(family) {
			continue
		}
		base := colors[family].oklch()
		lifted := oklch{L: math.Min(base.L+darkLift, 0.90), C: base.C * darkFade, H: base.H}
		set(family, oklchHex(lifted))
		soft := oklch{L: darkSoftL, C: lifted.C * darkSoftC, H: base.H}
		if _, declared := colors[family+"-soft"]; declared {
			set(family+"-soft", oklchHex(soft))
		}
		if _, declared := colors[family+"-wash"]; declared {
			set(family+"-wash", oklchHex(oklch{L: darkWashL, C: lifted.C * darkWashC, H: base.H}))
		}
		if _, declared := colors[family+"-deep"]; declared {
			sr, sg, sb, _ := parseHexRGB(oklchHex(soft))
			if s, ok := value(family + "-soft"); ok {
				sr, sg, sb, _ = parseHexRGB(oklchHex(s))
			}
			deep := oklch{L: lifted.L + darkDeepL, C: math.Min(lifted.C*darkDeepC, 0.33), H: base.H}
			set(family+"-deep", readableHex(deep, sr, sg, sb, darkContrast))
		}
		if _, declared := colors[family+"-on"]; declared {
			fill := lifted
			if f, ok := value(family); ok {
				fill = f
			}
			fr, fg, fb, _ := parseHexRGB(oklchHex(fill))
			set(family+"-on", labelOn(fill, fr, fg, fb, darkContrast))
		}
	}
	return derived
}

func isDarkSurface(name string) bool {
	for _, s := range darkSurfaces {
		if name == s {
			return true
		}
	}
	return false
}
