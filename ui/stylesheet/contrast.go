package stylesheet

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Contrast (decision C, 12/10/2026): every colour a .ui page shows text in is measured at compile time
// against what lies behind it, by WCAG 2.2 AA, and a pair that falls short is a compile error. This
// file is the arithmetic, kept beside the palette it reads: a colour value as the browser paints it in
// one mode, alpha compositing, relative luminance and the ratio, and the large-text rule read from the
// stylesheet's own scale. The walk that decides which pair an element shows is the compiler's (ui).

// RGBA is a colour as the browser paints it: sRGB channels from 0 to 1, gamma-encoded, and straight
// (not premultiplied) alpha.
type RGBA struct{ R, G, B, A float64 }

// Opaque reports whether nothing shows through the colour.
func (c RGBA) Opaque() bool { return c.A >= 1 }

// Hex is the colour as #rrggbb, its alpha dropped: channels rounded to 8 bits as a screen shows them.
func (c RGBA) Hex() string {
	return fmt.Sprintf("#%02x%02x%02x", byte8(c.R), byte8(c.G), byte8(c.B))
}

// Painted is the colour as a screen shows it: each channel rounded to 8 bits, as Hex writes it, the
// alpha kept. WCAG's luminance is of these values: a pair measured in floats can pass at 4.51:1 while
// the pixel painted is 4.49:1.
func (c RGBA) Painted() RGBA {
	return RGBA{R: float64(byte8(c.R)) / 255, G: float64(byte8(c.G)) / 255, B: float64(byte8(c.B)) / 255, A: c.A}
}

func byte8(v float64) int {
	i := int(math.Round(clip(v) * 255))
	return i
}

func clip(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// ParseHex reads "#rgb" or "#rrggbb" as an opaque colour.
func ParseHex(hex string) (RGBA, bool) {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return RGBA{}, false
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return RGBA{}, false
	}
	return RGBA{R: float64(n>>16&0xff) / 255, G: float64(n>>8&0xff) / 255, B: float64(n&0xff) / 255, A: 1}, true
}

// Over is c painted over below, as a browser composites: source-over in sRGB with straight alpha.
func (c RGBA) Over(below RGBA) RGBA {
	a := c.A + below.A*(1-c.A)
	if a <= 0 {
		return RGBA{}
	}
	mix := func(top, bottom float64) float64 { return (top*c.A + bottom*below.A*(1-c.A)) / a }
	return RGBA{R: mix(c.R, below.R), G: mix(c.G, below.G), B: mix(c.B, below.B), A: a}
}

// Luminance is WCAG 2.2's relative luminance of an opaque colour (its alpha is not read: composite it
// first).
func Luminance(c RGBA) float64 {
	linear := func(v float64) float64 {
		v = clip(v)
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// Ratio is WCAG 2.2's contrast ratio of two opaque colours, from 1 to 21, whichever is lighter.
func Ratio(a, b RGBA) float64 {
	x, y := Luminance(a)+0.05, Luminance(b)+0.05
	if x < y {
		x, y = y, x
	}
	return x / y
}

// The ratios WCAG 2.2 AA asks for (1.4.3, 1.4.11).
const (
	NormalTextRatio = 4.5 // text
	LargeTextRatio  = 3.0 // large text: LargeText
	NonTextRatio    = 3.0 // what is not text but says something: an icon with alt
)

// LargeText reports whether text is large by WCAG: at least 18pt (24px), or at least 14pt (18.66px)
// and bold (700 or heavier).
func LargeText(px float64, weight int) bool {
	return px >= 24 || px >= 18.66 && weight >= 700
}

// ---- oklab, for the operators: X-N is color-mix(in oklab, X (100-N)%, var(--canvas)), X+N moves
// away from the canvas in it (operators.go) ----

type oklab struct{ L, A, B float64 }

func toLinear(v float64) float64 {
	sign := 1.0
	if v < 0 {
		sign, v = -1, -v
	}
	if v <= 0.04045 {
		return sign * v / 12.92
	}
	return sign * math.Pow((v+0.055)/1.055, 2.4)
}

func fromLinear(v float64) float64 {
	sign := 1.0
	if v < 0 {
		sign, v = -1, -v
	}
	if v <= 0.0031308 {
		return sign * 12.92 * v
	}
	return sign * (1.055*math.Pow(v, 1/2.4) - 0.055)
}

// toOklab is Björn Ottosson's OKLab of an sRGB colour, the space CSS Color 4 names oklab.
func toOklab(c RGBA) oklab {
	r, g, b := toLinear(c.R), toLinear(c.G), toLinear(c.B)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	return oklab{
		L: 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		A: 1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		B: 0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

// fromOklab is the sRGB colour of an OKLab one, each channel clipped into sRGB as a browser paints a
// colour outside it.
func fromOklab(c oklab) RGBA {
	l := c.L + 0.3963377774*c.A + 0.2158037573*c.B
	m := c.L - 0.1055613458*c.A - 0.0638541728*c.B
	s := c.L - 0.0894841775*c.A - 1.2914855480*c.B
	l, m, s = l*l*l, m*m*m, s*s*s
	return RGBA{
		R: clip(fromLinear(4.0767416621*l - 3.3077115913*m + 0.2309699292*s)),
		G: clip(fromLinear(-1.2684380046*l + 2.6097574011*m - 0.3413193965*s)),
		B: clip(fromLinear(-0.0041960863*l - 0.7034186147*m + 1.7076147010*s)),
		A: 1,
	}
}

// OklabLightnessChroma is a colour's OKLab lightness, from 0 (black) to 1 (white), and its chroma, how far
// it is from the grey of that lightness: what says a colour went near white or lost its hue.
func OklabLightnessChroma(c RGBA) (lightness, chroma float64) {
	o := toOklab(c)
	return o.L, math.Hypot(o.A, o.B)
}

// MixOklab is color-mix(in oklab, x p%, y) of two opaque colours, p from 0 to 100: interpolated in
// OKLab, then back in sRGB, clipped.
func MixOklab(x, y RGBA, percent float64) RGBA {
	p := percent / 100
	a, b := toOklab(x), toOklab(y)
	return fromOklab(oklab{L: a.L*p + b.L*(1-p), A: a.A*p + b.A*(1-p), B: a.B*p + b.B*(1-p)})
}

// ---- a colour value of the table, painted ----

// Mode is one look of a palette.
type Mode int

const (
	LightMode Mode = iota
	DarkMode
)

// Word is the mode as a message says it: sáng, tối.
func (m Mode) Word() string {
	if m == DarkMode {
		return "tối"
	}
	return "sáng"
}

// Modes are the looks a palette has: light always, dark too when the chain has a dark look.
func (p *Palette) Modes() []Mode {
	if p.hasDark {
		return []Mode{LightMode, DarkMode}
	}
	return []Mode{LightMode}
}

// token is a token's value in a mode: its dark value, else the light one (a token the dark look does
// not re-value keeps it, as colors.<hash>.css writes only the tokens it re-values).
func (p *Palette) token(name string, m Mode) (RGBA, bool) {
	hex, ok := p.light[name]
	if !ok {
		return RGBA{}, false
	}
	if night, re := p.dark[name]; m == DarkMode && re {
		hex = night
	}
	return ParseHex(hex)
}

// Paint is a colour value as the browser paints it in one mode: the token's value in that mode (a
// fixed colour the same in both), made lighter (X-N, Lighter) or stronger (X+N, Stronger, rounded to
// the 8 bits of its custom property) against that mode's canvas exactly as the table writes it, an
// alpha as the colour's alpha
// (color-mix(in srgb, X N%, transparent) is X at N%). An engine colour (kitwork-error) is painted as its
// value in that mode (Palette.Engine). A "#rgb" or "#rrggbb" is that colour, opaque: the side of a kind's
// pair of router.highlight, which Engine gives as its hex (and the contrast check names the text by);
// the markup never reaches one, the table refuses # before anything is painted. ok is false for a value
// the palette cannot name, and for -N or +N on a fixed colour, which the table refuses.
func (p *Palette) Paint(value string, m Mode) (RGBA, bool) {
	if v, ok := p.Engine(value, m); ok {
		value = v
	}
	if hex := strings.TrimSpace(value); hexColor.MatchString(hex) {
		return ParseHex(hex)
	}
	c, ok := SplitColour(value)
	if !ok {
		return RGBA{}, false
	}
	var base RGBA
	if fixed, isFixed := fixedColour(c.Base); isFixed {
		if c.Op != 0 {
			return RGBA{}, false
		}
		base, _ = ParseHex(fixed)
	} else if base, ok = p.token(c.Base, m); !ok {
		return RGBA{}, false
	}
	if c.Op != 0 {
		canvas, _ := p.token("canvas", m)
		if c.Op == '-' {
			base = Lighter(base, canvas, c.Amount)
		} else {
			// the page paints the #rrggbb its custom property holds (strongValue), so an alpha
			// composites that, not the unrounded colour
			base = Stronger(base, canvas, c.Amount).Painted()
		}
	}
	base.A = float64(c.Alpha) / 100
	return base, true
}
