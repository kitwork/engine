package css

import "testing"

// The house badge recipe is `bg-<x>-soft text-<x>-deep`, so `deep` must be readable on `soft`.
// `deep` is derived as "11% darker than the seed", which holds for a mid-tone identity hue but
// collapses when the seed is already a pale tint: sage (#def7ec, L≈0.95) yields a deep that is
// still pale, and the badge becomes unreadable.
func TestDeepIsReadableOnSoft(t *testing.T) {
	for name, hex := range map[string]string{
		"blurple": "#635bff", "emerald": "#059669", "amber": "#d97706",
		"sage-tint": "#def7ec", "peach-tint": "#ffe3de", "sky-tint": "#e1effe",
	} {
		fam := Palette(hex)
		dr, dg, db, _ := parseHexRGB(fam["deep"])
		sr, sg, sb, _ := parseHexRGB(fam["soft"])
		if got := contrastRatio(dr, dg, db, sr, sg, sb); got < 4.5 {
			t.Errorf("%s (%s): deep %s on soft %s = %.2f:1, want >= 4.5",
				name, hex, fam["deep"], fam["soft"], got)
		}
	}
}
