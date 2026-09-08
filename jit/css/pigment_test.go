package css

import (
	"math"
	"testing"
)

// A generated family is only worth having if it holds up for EVERY hue a site might pick — the
// reason for OKLCH over HSL. These pin the properties an HSL implementation cannot deliver:
// rungs that read at the same lightness across hues, text that clears WCAG AA whatever the seed,
// and a desaturated seed that stays desaturated.

var seeds = map[string]string{
	"blurple": "#635bff", "coral": "#ff6a4d", "emerald": "#059669", "ocean": "#0284c7",
	"amber": "#d97706", "rose": "#e11d48", "violet": "#8b5cf6", "cyan": "#06b6d4",
}

func lightnessOf(t *testing.T, hex string) float64 {
	t.Helper()
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		t.Fatalf("derived colour %q is not a valid hex", hex)
	}
	return rgbToOklch(r, g, b).L
}

func chromaOf(t *testing.T, hex string) float64 {
	t.Helper()
	r, g, b, ok := parseHexRGB(hex)
	if !ok {
		t.Fatalf("derived colour %q is not a valid hex", hex)
	}
	return rgbToOklch(r, g, b).C
}

// THE point of OKLCH: one fixed lightness target reads the same on every hue. Under HSL the same
// nominal lightness swings wildly between, say, amber and blurple, which is what forced per-hue
// hand-tuning in the first place.
func TestPaletteRungsAreEvenAcrossHues(t *testing.T) {
	for name, hex := range seeds {
		fam := Palette(hex)
		if fam == nil {
			t.Fatalf("%s: Palette returned nil for %q", name, hex)
		}
		if fam["DEFAULT"] != hex {
			t.Errorf("%s: DEFAULT must be the untouched input, got %q want %q", name, fam["DEFAULT"], hex)
		}
		if l := lightnessOf(t, fam["soft"]); math.Abs(l-0.90) > 0.02 {
			t.Errorf("%s: soft lands at L=%.3f, want ~0.90 for every hue", name, l)
		}
		if l := lightnessOf(t, fam["wash"]); math.Abs(l-0.955) > 0.02 {
			t.Errorf("%s: wash lands at L=%.3f, want ~0.955 for every hue", name, l)
		}
		// deep is the hover/active step: it must actually be darker than the seed.
		if lightnessOf(t, fam["deep"]) >= lightnessOf(t, hex) {
			t.Errorf("%s: deep (%s) is not darker than DEFAULT (%s)", name, fam["deep"], hex)
		}
	}
}

// Generated text is worthless if it cannot be read. Every seed must yield body and secondary
// copy that clears WCAG AA on the canvas.
func TestPigmentTextAlwaysClearsAA(t *testing.T) {
	cr, cg, cb, _ := parseHexRGB(defaultCanvas)
	for name, hex := range seeds {
		ink := Pigment(hex, defaultCanvas)
		if ink == nil {
			t.Fatalf("%s: Pigment returned nil", name)
		}
		for _, rung := range []string{"DEFAULT", "soft"} {
			r, g, b, _ := parseHexRGB(ink[rung])
			if got := contrastRatio(r, g, b, cr, cg, cb); got < 4.5 {
				t.Errorf("%s: ink.%s = %s gives %.2f:1 on canvas, want >= 4.5", name, rung, ink[rung], got)
			}
		}
	}
}

// A site that deliberately picks a desaturated identity must not get tinted prose. The previous
// HSL formula floored saturation, so a grey seed produced visibly coloured text.
func TestPigmentKeepsAGreySeedGrey(t *testing.T) {
	ink := Pigment("#808080", defaultCanvas)
	if ink == nil {
		t.Fatal("Pigment returned nil for a grey seed")
	}
	for _, rung := range []string{"deep", "DEFAULT", "soft", "wash"} {
		if c := chromaOf(t, ink[rung]); c > 0.005 {
			t.Errorf("grey seed produced tinted ink.%s = %s (chroma %.4f), want ~0", rung, ink[rung], c)
		}
	}
}

// CONTROL: the guard must be doing real work, not passing by luck. A pale seed asks for a light
// ink; if the guard were removed the requested lightness would fail AA outright.
func TestPigmentGuardActuallyDarkens(t *testing.T) {
	cr, cg, cb, _ := parseHexRGB(defaultCanvas)

	// What an unguarded ramp would emit for a very light seed: the raw L target, no contrast check.
	raw := oklchHex(oklch{L: 0.78, C: 0.02, H: 90})
	rr, rg, rb, _ := parseHexRGB(raw)
	if contrastRatio(rr, rg, rb, cr, cg, cb) >= 4.5 {
		t.Fatal("control is void: the unguarded colour already passes AA, so the guard proves nothing")
	}

	got := readableHex(oklch{L: 0.78, C: 0.02, H: 90}, cr, cg, cb, 4.5)
	gr, gg, gb, _ := parseHexRGB(got)
	if ratio := contrastRatio(gr, gg, gb, cr, cg, cb); ratio < 4.5 {
		t.Errorf("guard failed to reach AA: %s gives %.2f:1", got, ratio)
	}
}

func TestPaletteRejectsInvalidInput(t *testing.T) {
	for _, bad := range []string{"", "#12", "not-a-colour", "#zzzzzz"} {
		if Palette(bad) != nil {
			t.Errorf("Palette(%q) should be nil", bad)
		}
		if Pigment(bad, defaultCanvas) != nil {
			t.Errorf("Pigment(%q) should be nil", bad)
		}
	}
}

// A label on a solid fill is where a generated palette can silently produce an unreadable UI: white
// looks right on a blurple button and fails outright on an amber one. `on` must therefore clear AA
// for EVERY hue, and must actually switch rather than always answering white.
func TestPaletteOnRungIsReadableOnEveryHue(t *testing.T) {
	var sawWhite, sawDark bool
	for name, hex := range seeds {
		fam := Palette(hex)
		on, ok := fam["on"]
		if !ok {
			t.Fatalf("%s: Palette has no `on` rung", name)
		}
		fr, fg, fb, _ := parseHexRGB(hex)
		or, og, ob, _ := parseHexRGB(on)
		if got := contrastRatio(or, og, ob, fr, fg, fb); got < 4.5 {
			t.Errorf("%s: label %s on fill %s gives %.2f:1, want >= 4.5", name, on, hex, got)
		}
		if on == "#ffffff" {
			sawWhite = true
		} else {
			sawDark = true
		}
	}

	// CONTROL: a rung that always returned white would pass nothing but a hue-blind test, and an
	// always-dark one is equally wrong. The decision has to depend on the fill.
	if !sawWhite || !sawDark {
		t.Fatalf("`on` never switched (white seen: %v, dark seen: %v) — it is not reading the fill", sawWhite, sawDark)
	}
}

// Spot-check the two ends so a regression in the comparison direction is obvious, not subtle.
func TestPaletteOnRungPicksTheRightEnd(t *testing.T) {
	if on := Palette("#635bff")["on"]; on != "#ffffff" {
		t.Errorf("a dark blurple fill should take a white label, got %s", on)
	}
	if on := Palette("#d97706")["on"]; on == "#ffffff" {
		t.Error("a light amber fill must not take a white label — that is the failure this rung exists to prevent")
	}
}
