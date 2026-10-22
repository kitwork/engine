package stylesheet

import (
	"regexp"
	"strings"
	"testing"
)

// The tooltip of a toolbar's button (editor-tip, 21/10/2026; made and placed by the editor's part,
// jit/js/parts/editor-place.js): small words on one line, an ink fill with paper words, a small radius,
// above what floats in the editor, an edge of 1px transparent (a contrast theme draws it in the system's
// colour), taking no press (pointer-events: none: a button it covers stays the pointer's; the part reads
// the pointer on it from where it is, so it is still hoverable), and nothing beside it that takes one
// (the ::before it had over the gap took presses at the foot of its button: the review of 21/10/2026).
// Its words read on its fill in light and in dark: measured on Kit Design's palette and on a site's own
// ink and paper, at the 4.5 a 12px text needs. (jit/js/editor_place_test.go's editorTipRules copies the
// rule for the browser proofs.)
// CONTROL (run 21/10/2026, copies of editor.go through -overlay, controls.py in the scratchpad's
// editor-tips/fix/controls): with the words painted in ink on ink, "the tooltip's rule" and "its words on
// its fill, light" (1.00) failed; with the ::before back, "nothing of the tooltip takes a press" failed;
// without pointer-events:none, "the tooltip's rule" failed.
func TestEditorTipLook(t *testing.T) {
	scope := editorScope(nil)
	const rule = `.editor-tip{position:absolute;z-index:60;padding:.25rem .5rem;border:1px solid transparent;border-radius:.375rem;background:var(--ink);color:var(--paper);font-size:.75rem;line-height:1.5;white-space:nowrap;pointer-events:none}` + "\n"
	if strings.Count(scope, rule) != 1 {
		t.Errorf("the tooltip's rule: want once in the editor's scope\n%s\n%s", rule, scope)
	}
	if got := strings.Count(scope, ".editor-tip"); got != 1 {
		t.Errorf("nothing of the tooltip takes a press: want its one rule, got %d naming it\n%s", got, scope)
	}
	look := regexp.MustCompile(`\.editor-tip\{[^}]*background:var\(--([a-z]+)\);color:var\(--([a-z]+)\)`).FindStringSubmatch(scope)
	if look == nil {
		t.Fatalf("the tooltip paints no fill and words of the palette:\n%s", scope)
	}
	fill, words := look[1], look[2]
	kit, err := NewPalette(true)
	if err != nil {
		t.Fatal(err)
	}
	site, err := NewPalette(false, map[string]Color{"ink": {Light: "#1f2937"}, "paper": {Light: "#fdfcf8"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Palette{kit, site} {
		for _, m := range p.Modes() {
			a, okA := p.Paint(words, m)
			b, okB := p.Paint(fill, m)
			if !okA || !okB {
				t.Fatalf("the palette has no %s or %s", words, fill)
			}
			mode := map[Mode]string{LightMode: "light", DarkMode: "dark"}[m]
			r := Ratio(a, b)
			t.Logf("%s on %s, %s: %.2f", words, fill, mode, r)
			if r < 4.5 {
				t.Errorf("its words on its fill, %s: %s on %s is %.2f, under 4.5", mode, words, fill, r)
			}
		}
	}
	if len(kit.Modes()) != 2 {
		t.Fatalf("Kit Design's palette has no dark look to measure")
	}
}
