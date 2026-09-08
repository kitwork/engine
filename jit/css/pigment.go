package css

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Colour derivation in OKLCH.
//
// Palette() and Pigment() grow a full token family from ONE hue so a site declares its identity
// once instead of hand-typing eight hexes that drift apart. OKLCH is used rather than HSL because
// its L is perceptual: one fixed set of lightness targets reads evenly across EVERY hue, so no
// per-hue hand-tuning is needed and hue stays put when lightness changes (HSL needs a
// Bezold-Brücke fudge to stop indigo collapsing into navy when darkened).
//
// Both return plain name→hex maps, so the existing theme parser flattens them exactly like a
// hand-written object (DEFAULT loses its suffix) and a site can still override any rung by
// spreading: brand: { ...palette("#635bff"), deep: "#4320b3" }.

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

// oklchHex renders an OKLCH colour as #rrggbb, backing chroma off until it fits inside sRGB.
// Clipping each channel instead would shift the hue and read as muddy.
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

// parseHexRGB accepts #rgb / #rrggbb (with or without the hash). ok is false for anything else.
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

// ---- WCAG contrast, so generated text is legible whatever hue a site picks ----

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

// readableHex nudges L until the colour clears target contrast on bg — darker on a light
// background, lighter on a dark one — so body text stays legible under any brand hue or theme.
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

const defaultCanvas = "#f6f9fc"

// Palette grows an accent family from one hue: DEFAULT (the input, never altered) plus deep for
// hover/active, soft for tinted fills such as badges, and wash for a faint zone background.
// It suits any single-hue family — brand as well as the status colours.
func Palette(hex string) map[string]string {
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		return nil
	}
	c := rgbToOklch(r, g, b)

	// `on` is not a fifth rung of the ladder — the ladder is lightness steps of one hue, while this
	// is the FOREGROUND paired with the fill (Material's on-<colour> convention). It exists because
	// nothing else can supply it: a label on a solid fill needs a binary choice made by measured
	// contrast, which no alpha or lightness step can express. Without it, a site picking a light
	// hue (amber, cyan, lime) gets an unreadable button label, since white fails on a light fill.
	// White is the conventional label and is kept whenever it genuinely clears AA. Only when the
	// fill is too light for it does the rung switch to a dark label — and that label is darkened
	// until it ACTUALLY clears AA, because a fixed lightness stalls at 4.3-4.4 on mid-tone hues
	// (violet, ocean). A label that merely almost passes is the silent failure this rung prevents.
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

	soft := oklchHex(oklch{L: 0.90, C: math.Min(c.C*0.34, 0.055), H: c.H})

	// `deep` is darkened until it is READABLE ON `soft`, because the house badge recipe pairs the two
	// (`bg-<x>-soft text-<x>-deep`). A fixed "11% darker than the seed" holds for a mid-tone identity
	// hue but collapses elsewhere: amber lands at 3.63:1, and a seed that is already a pale tint —
	// sage #def7ec — gives 1.18:1, an unreadable badge. Where the plain step already clears AA
	// (blurple, emerald) the guard returns it untouched, so nothing shifts for the common case.
	sr, sg, sb, _ := parseHexRGB(soft)
	deep := readableHex(oklch{L: math.Max(0.30, c.L-0.11), C: math.Min(c.C*1.05, 0.33), H: c.H}, sr, sg, sb, 4.5)

	return map[string]string{
		"DEFAULT": hex,
		"deep":    deep,
		"soft":    soft,
		"wash":    oklchHex(oklch{L: 0.955, C: math.Min(c.C*0.20, 0.028), H: c.H}),
		"on":      on,
	}
}

// Pigment grows the text ramp: brand-tinted neutrals for headings (deep), body (DEFAULT),
// secondary copy (soft) and hairlines or faint fills (wash). The two rungs used as body text are
// contrast-guarded against canvas, so they clear WCAG AA whatever hue is seeded.
//
// Chroma scales from the seed with NO floor: a near-grey seed yields near-grey text, because a
// site that deliberately picks a desaturated identity should not get tinted prose.
func Pigment(hex, canvas string) map[string]string {
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		return nil
	}
	if canvas == "" {
		canvas = defaultCanvas
	}
	br, bg, bb, ok := parseHexRGB(canvas)
	if !ok {
		br, bg, bb, _ = parseHexRGB(defaultCanvas)
	}
	c := rgbToOklch(r, g, b)
	return map[string]string{
		"deep":    oklchHex(oklch{L: 0.20, C: math.Min(c.C*0.22, 0.05), H: c.H}),
		"DEFAULT": readableHex(oklch{L: 0.27, C: math.Min(c.C*0.20, 0.045), H: c.H}, br, bg, bb, 4.5),
		"soft":    readableHex(oklch{L: 0.48, C: math.Min(c.C*0.14, 0.03), H: c.H}, br, bg, bb, 4.5),
		"wash":    oklchHex(oklch{L: 0.90, C: math.Min(c.C*0.06, 0.015), H: c.H}),
	}
}
