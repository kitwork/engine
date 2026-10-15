package stylesheet

import (
	"strings"
	"testing"
)

// The five Figma names that only rename a CSS colour are refused with the CSS name however their letters
// are written: run together (navyblue), in capitals, with an underscore, as a hyphenated one is (review
// of 12/10/2026: run together they fell to the unknown-colour error, whose nearest name could be another
// colour — crimsonred near crimson #dc143c, while Figma's crimson red is firebrick #b22222). The same in
// router.colors (a seed) and as a gradient's name. No name of the dictionary has the letters of one, so
// an alias never hides a name. CONTROL: each CSS name, and crimson itself, read as written.
func TestNamedAliasesWrittenAnyWay(t *testing.T) {
	table := NewTable(nil)
	for alias, css := range namedAliases {
		if name, taken := namedLetters[letters(alias)]; taken {
			t.Errorf("%s has the letters of the name %s: one of them would hide the other", alias, name)
		}
		together := strings.ReplaceAll(alias, "-", "")
		for _, written := range []string{together, strings.ToUpper(together), strings.ReplaceAll(alias, "-", "_")} {
			for _, form := range []struct{ value, suggestion string }{{written, css}, {written + "/50", css + "/50"}} {
				_, _, err := table.Read(nil, Intent{Name: "color", Value: form.value})
				var p *Problem
				if err == nil || !asProblem(err, &p) || p.Warning || p.Suggestion != form.suggestion ||
					!strings.Contains(p.Message, written+" không phải tên màu: đó là tên Figma đặt cho "+css+" ("+namedColours[css]+") của CSS; viết "+css) {
					t.Errorf("color=%q: %v (suggestion %q), want it refused as Figma's name of %s", form.value, err, suggestionOf(p), css)
				}
			}
		}
		if _, err := NewPalette(false, map[string]Color{"brand": {Light: together}}); err == nil || !strings.Contains(err.Error(), "viết "+css) {
			t.Errorf("router.colors({ brand: %q }): %v, want it told to write %s", together, err, css)
		}
		if err := CheckGradient(together, Gradient{Type: "linear", Direction: "to-end", Stops: []Stop{{Color: "ink", At: 0}, {Color: "brand", At: 100}}}); err == nil ||
			!strings.Contains(err.Error(), "là cách viết khác của màu "+css) {
			t.Errorf("router.gradients({ %s: … }): %v, want the colour's name taken", together, err)
		}
		if _, _, err := table.Read(nil, Intent{Name: "color", Value: css}); err != nil {
			t.Errorf("control: color=%q: %v", css, err)
		}
	}
	if hex, ok := NamedColour("crimson"); !ok || hex != "#dc143c" {
		t.Errorf("control: crimson is CSS's #dc143c, got %q %v", hex, ok)
	}
}
