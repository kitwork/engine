package stylesheet

import (
	"math"
	"strconv"
	"testing"
)

func colourOf(t *testing.T, h string) RGBA {
	t.Helper()
	c, ok := ParseHex(h)
	if !ok {
		t.Fatalf("not a colour: %s", h)
	}
	return c
}

// WCAG 2.2's ratio for pairs everyone knows: black on white is 21:1, a colour on itself 1:1, and
// #777777 on white 4.48:1, short of 4.5:1 — the comparison is exact, so it fails; #767676, the
// lightest grey that reads on white, passes (control). The ratio does not care which is the text.
func TestWCAGRatio(t *testing.T) {
	white, black := colourOf(t, "#fff"), colourOf(t, "#000000")
	if r := Ratio(black, white); r != 21 {
		t.Errorf("black on white = %v, want 21", r)
	}
	if r := Ratio(white, white); r != 1 {
		t.Errorf("white on white = %v, want 1", r)
	}
	grey := colourOf(t, "#777777")
	r := Ratio(grey, white)
	if math.Abs(r-4.4781) > 0.0001 {
		t.Errorf("#777777 on white = %.4f, want 4.4781", r)
	}
	if r >= NormalTextRatio {
		t.Errorf("#777777 on white (%.4f) must fall short of %.1f", r, NormalTextRatio)
	}
	if r := Ratio(colourOf(t, "#767676"), white); r < NormalTextRatio {
		t.Errorf("CONTROL: #767676 on white = %.4f, want at least 4.5", r)
	}
	if Ratio(grey, white) != Ratio(white, grey) {
		t.Error("the ratio depends on which colour is the text")
	}
	// the threshold of WCAG 2.2 (0.04045): the darkest channels are linear
	if l := Luminance(RGBA{R: 0.04, G: 0.04, B: 0.04, A: 1}); math.Abs(l-0.04/12.92) > 1e-12 {
		t.Errorf("Luminance of a channel under 0.04045 = %v, want %v", l, 0.04/12.92)
	}
}

// Over is source-over in sRGB with straight alpha, as a browser paints a translucent colour.
func TestAlphaCompositing(t *testing.T) {
	white, black := colourOf(t, "#ffffff"), colourOf(t, "#000000")
	half := black
	half.A = 0.5
	if got := half.Over(white); got.Hex() != "#808080" || got.A != 1 {
		t.Errorf("black/50 over white = %s alpha %v, want #808080 alpha 1", got.Hex(), got.A)
	}
	red := RGBA{R: 1, A: 0.5}
	if got := red.Over(RGBA{B: 1, A: 1}); got != (RGBA{R: 0.5, B: 0.5, A: 1}) {
		t.Errorf("red/50 over blue = %+v", got)
	}
	// two translucent layers: alpha adds up, colour weighs by it
	under := RGBA{B: 1, A: 0.5}
	got := red.Over(under)
	if math.Abs(got.A-0.75) > 1e-12 || math.Abs(got.R-2.0/3) > 1e-12 || math.Abs(got.B-1.0/3) > 1e-12 {
		t.Errorf("red/50 over blue/50 = %+v, want alpha .75, red 2/3, blue 1/3", got)
	}
	// an opaque colour hides what is under it
	if got := white.Over(black); got != white {
		t.Errorf("white over black = %+v", got)
	}
}

// Large text by WCAG: 24px, or 18.66px and bold. The scale's 18px bold (size="large") is not.
func TestLargeText(t *testing.T) {
	for _, c := range []struct {
		px     float64
		weight int
		large  bool
	}{
		{24, 400, true}, {23.9, 400, false}, {18.66, 700, true}, {18.66, 600, false}, {18, 700, false}, {16, 900, false}, {30, 600, true},
	} {
		if got := LargeText(c.px, c.weight); got != c.large {
			t.Errorf("LargeText(%v, %d) = %v, want %v", c.px, c.weight, got, c.large)
		}
	}
	// the scale: huge text is 24px, a heading's small step 24px, its medium step 30px then 36px from small
	for _, c := range []struct {
		heading     bool
		value       string
		from, width int
		px          float64
	}{
		{false, "huge", 0, 0, 24}, {false, "large", 0, 3, 18}, {true, "small", 0, 0, 24}, {true, "medium", 0, 0, 30},
		{true, "medium", 0, 1, 36}, {true, "huge", 0, 3, 72}, {true, "huge", 2, 2, 72}, {true, "mini", 0, 0, 18},
	} {
		if got, ok := FontSize(c.heading, c.value, c.from, c.width); !ok || got != c.px {
			t.Errorf("FontSize(%v, %s, %d, %d) = %v %v, want %v", c.heading, c.value, c.from, c.width, got, ok, c.px)
		}
	}
}

// Paint is a colour value as the browser paints it in one mode: a token's own value, made lighter
// toward that mode's canvas in oklab (X-N) or stronger away from it (X+N), an alpha as the colour's
// alpha, a fixed colour the same in both. The old spellings and -N or +N on a fixed colour paint
// nothing: the table refuses them.
func TestPaint(t *testing.T) {
	p, err := NewPalette(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Modes() {
		ink, _ := p.Paint("ink", m)
		canvas, _ := p.Paint("canvas", m)
		if got, _ := p.Paint("ink-30", m); got != MixOklab(ink, canvas, 70) {
			t.Errorf("%s: ink-30 = %s, want ink mixed 30%% toward canvas (70%% ink)", m.Word(), got.Hex())
		}
		if got, _ := p.Paint("ink-0", m); got.Hex() != ink.Hex() {
			t.Errorf("%s: ink-0 = %s, want ink %s", m.Word(), got.Hex(), ink.Hex())
		}
		if got, _ := p.Paint("ink-100", m); got.Hex() != canvas.Hex() {
			t.Errorf("%s: ink-100 = %s, want canvas %s", m.Word(), got.Hex(), canvas.Hex())
		}
		brand, _ := p.Paint("brand", m)
		if got, _ := p.Paint("brand+15", m); got != Stronger(brand, canvas, 15).Painted() || got == brand {
			t.Errorf("%s: brand+15 = %s, want brand %s made 15%% stronger, as the 8 bits of its custom property", m.Word(), got.Hex(), brand.Hex())
		}
		if got, _ := p.Paint("paper/80", m); got.A != 0.8 {
			t.Errorf("%s: paper/80 alpha %v", m.Word(), got.A)
		}
		if got, _ := p.Paint("white", m); got.Hex() != "#ffffff" {
			t.Errorf("%s: white = %s", m.Word(), got.Hex())
		}
	}
	light, _ := p.Paint("ink", LightMode)
	dark, _ := p.Paint("ink", DarkMode)
	if light == dark {
		t.Error("ink is the same in light and dark: the dark look is not read")
	}
	for _, bad := range []string{"nope", "white.50", "white-20", "tomato+10", "ink.70", "brand.soft", "brand.deep", "ink-30+10", "ink:80", ""} {
		if _, ok := p.Paint(bad, LightMode); ok {
			t.Errorf("Paint(%q) is a colour", bad)
		}
	}
}

// X+N with an alpha is painted as the page paints it: the #rrggbb its custom property holds
// (--kitwork-strong-N-X, strongValue) at that alpha, not the colour before it was rounded. At the
// threshold the two part: danger+12/95 over paper on Kit Design in light is #e12f41 on the screen,
// 4.48:1, short of 4.5:1, where the unrounded colour composites to #e02f41, 4.51:1 (CONTROL: the check
// tells the two apart). Every token, step and alpha agrees with its custom property, in both modes.
func TestStrongerWithAnAlphaIsItsCustomProperty(t *testing.T) {
	kit, _ := NewPalette(true)
	wrong := 0
	for _, token := range kit.Tokens() {
		for n := 0; n <= 100; n++ {
			property := kit.strongValue(token, n)
			for _, m := range kit.Modes() {
				hex := property.light
				if m == DarkMode {
					hex = property.dark
				}
				want, ok := ParseHex(hex)
				if !ok {
					t.Fatalf("%s %s: no value", property.name, m.Word())
				}
				for _, alpha := range []int{50, 90, 95, 100} {
					value := token + "+" + strconv.Itoa(n) + "/" + strconv.Itoa(alpha)
					got, ok := kit.Paint(value, m)
					if (!ok || got.R != want.R || got.G != want.G || got.B != want.B || got.A != float64(alpha)/100) && wrong < 3 {
						wrong++
						t.Errorf("%s %s is %+v, want %s (%s), %+v at %v", value, m.Word(), got, hex, property.name, want, float64(alpha)/100)
					}
				}
			}
		}
	}
	paper, _ := kit.Paint("paper", LightMode)
	text, _ := kit.Paint("danger+12/95", LightMode)
	pixel := text.Over(paper).Painted()
	if ratio := Ratio(pixel, paper.Painted()); pixel.Hex() != "#e12f41" || ratio >= NormalTextRatio {
		t.Errorf("danger+12/95 over paper in light is %s, %.4f:1; want #e12f41, short of %v:1", pixel.Hex(), ratio, NormalTextRatio)
	}
	danger, _ := kit.Paint("danger", LightMode)
	canvas, _ := kit.Paint("canvas", LightMode)
	unrounded := Stronger(danger, canvas, 12)
	unrounded.A = 0.95
	if raw := unrounded.Over(paper).Painted(); raw.Hex() != "#e02f41" || Ratio(raw, paper.Painted()) < NormalTextRatio {
		t.Errorf("CONTROL: the unrounded danger+12 at 95%% over paper is %s, %.4f:1; want #e02f41 reading %v:1", raw.Hex(), Ratio(raw, paper.Painted()), NormalTextRatio)
	}
}

// The engine's own text passes on Kit Design's default palette in both modes (decision C): a hint's
// ink-30 and an error's danger+25 on canvas, paper and panel, a badge's text X+n on its X-85 fill for
// every accent, ink on every surface, the label rung on every fill. CONTROL: danger, the colour .error
// had before, falls short on the light canvas (3.70:1) — the reason reboot.css changed it.
func TestEngineDefaultsPassOnKitDesign(t *testing.T) {
	p, err := NewPalette(true)
	if err != nil {
		t.Fatal(err)
	}
	table := NewTable(p)
	check := func(text, ground string, need float64) {
		t.Helper()
		for _, m := range p.Modes() {
			fg, ok1 := p.Paint(text, m)
			bg, ok2 := p.Paint(ground, m)
			if !ok1 || !ok2 {
				t.Fatalf("%s on %s: not in the palette", text, ground)
			}
			if r := Ratio(fg, bg); r < need {
				t.Errorf("%s on %s in %s: %.2f:1, want %.1f:1", text, ground, m.Word(), r, need)
			}
		}
	}
	for _, class := range []string{"hint", "error"} {
		look := LookOf("p", []string{class}, "", 0)
		if LargeText(look.Size, 400) {
			t.Fatalf(".%s is large text", class)
		}
		for _, ground := range []string{"canvas", "paper", "panel"} {
			check(look.Text, ground, NormalTextRatio)
		}
	}
	for _, accent := range []string{"brand", "success", "warning", "danger", "info"} {
		fill, text, family := table.BadgeTint(accent)
		if !family {
			t.Fatalf("badge %s has no family", accent)
		}
		check(text, fill, NormalTextRatio) // 12px: never large
		check(accent+".on", accent, NormalTextRatio)
	}
	for _, ground := range []string{"canvas", "paper", "panel"} {
		check(RootLook().Text, ground, NormalTextRatio)
	}
	fg, _ := p.Paint("danger", LightMode)
	bg, _ := p.Paint("canvas", LightMode)
	if r := Ratio(fg, bg); r >= NormalTextRatio || strconv.FormatFloat(r, 'f', 2, 64) != "3.70" {
		t.Errorf("CONTROL: danger on the light canvas = %.4f, want 3.70 and short of 4.5", r)
	}
}
