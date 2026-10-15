package ui

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// highlightCallAt is the place of a router.highlight call in these tests, as the VM gives it.
var highlightCallAt = stylesheet.Source{File: "router.work", Line: 7, Column: 30}

// highlightPalette is the kitwork example's palette with router.highlight: keyword violet and comment
// ink-40 (both short on the page's grounds), function a pair that reads there.
func highlightPalette(t *testing.T) *stylesheet.Palette {
	t.Helper()
	p, err := kitworkExampleColors(t).WithHighlight(map[string]stylesheet.HighlightColour{
		"keyword":  {Color: "violet", Source: highlightCallAt},
		"comment":  {Color: "ink-40", Source: highlightCallAt},
		"function": {Pair: stylesheet.Color{Light: "#7c3aed", Dark: "#bb9af7"}, Source: highlightCallAt},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A colour router.highlight sets is told once, at the call in router.work, by the palette's line
// (stylesheet.Palette.HighlightWarning) — not again at language= for the page's canvas or a card's
// paper, where the line already measured it — and under "strict" that line is the page's compile
// error. CONTROL: a page that shows none of the call's colours says nothing of it.
func TestHighlightIsToldAtTheRouter(t *testing.T) {
	p := highlightPalette(t)
	line := "router.work:7:30: " + p.HighlightWarning(highlightCallAt)
	if !strings.Contains(line, `keyword "violet" ở chế độ sáng`) || !strings.Contains(line, `comment "ink-40" ở chế độ sáng`) {
		t.Fatalf("the call's line names keyword and comment: %s", line)
	}
	for _, source := range []string{
		`<content><code language="go">func main() {} // chạy</code></content>`,
		`<content><card><code language="go">func main() {}</code></card></content>`,
		`<content><box background="panel"><code language="go">func main() {}</code></box></content>`,
	} {
		// contrastCompile also checks that "strict" makes the same line the page's error
		if got := contrastCompile(t, p, content(source)); got != line {
			t.Errorf("%s:\n got  %s\n want %s", source, got, line)
		}
	}
	contrastPasses(t, p, content(`<content><text>Không mã</text></content>`)) // CONTROL
}

// On any other ground the author's colour is measured where the code stands and told at language=,
// with what changes it: the call of router.highlight, or the ground. A pair is named by its side of the
// mode measured. CONTROL: an engine colour on the same ground keeps the engine's advice (router.colors,
// or the ground).
func TestHighlightAdviceNamesTheRouter(t *testing.T) {
	warned := contrastCompile(t, highlightPalette(t), content(`<content><box background="ink-10"><code language="go">func main() {} // chạy</code></box></content>`))
	for _, want := range []string{
		`content.ui:1:41: chữ keyword (violet) trong khối mã trên nền ink-10 chỉ 1.16:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Màu này là của router.highlight (router.work:7:30): đổi keyword ở đó (một cặp { light, dark } khi mỗi chế độ cần một màu), hoặc đặt khối mã trên nền khác (background).`,
		`content.ui:1:41: chữ function (#7c3aed) trong khối mã trên nền ink-10 chỉ 2.04:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Màu này là của router.highlight (router.work:7:30): đổi function ở đó`,
		// CONTROL
		`content.ui:1:41: chữ punctuation (ink-30) trong khối mã trên nền ink-10 chỉ 1.81:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors từ ink, mà nền cũng là ink: đặt khối mã trên nền khác (background).`,
	} {
		if !strings.Contains(warned, want) {
			t.Errorf("missing %s in\n%s", want, warned)
		}
	}
	for _, line := range strings.Split(warned, "\n") {
		if strings.Contains(line, "Màu này là của router.highlight") && strings.Contains(line, "router.colors") {
			t.Errorf("an author's colour is never told to declare router.colors anew: %s", line)
		}
	}
}

// Only the page's grounds as they are, solid, are the call's to tell: a code on paper that turns violet
// on hover is still measured on the hover ground at language= (the pair at rest, which the call tells,
// does not stop the check of the code); on a glass of paper, which lets the canvas show through, it is
// measured too. CONTROL: the same code on plain paper has the call's line alone.
func TestHighlightMeasuresTheGroundsTheRouterDoesNot(t *testing.T) {
	p := highlightPalette(t)
	hover := contrastCompile(t, p, content(`<content><box background="paper" hover:background="violet"><code language="go">func main() {}</code></box></content>`))
	if want := `content.ui:1:66: chữ keyword (violet) trong khối mã trên nền violet chỉ 1.00:1 ở chế độ sáng khi hover:;`; !strings.Contains(hover, want) {
		t.Errorf("the hover ground is measured at language=: want %s in\n%s", want, hover)
	}
	glass := contrastCompile(t, p, content(`<content><box material="glass"><code language="go">func main() {}</code></box></content>`))
	if want := `content.ui:1:38: chữ keyword (violet) trong khối mã trên nền kính paper (material="glass") phủ trên canvas chỉ 2.29:1 ở chế độ sáng;`; !strings.Contains(glass, want) {
		t.Errorf("a glass of paper is measured at language=: want %s in\n%s", want, glass)
	}
	plain := contrastCompile(t, p, content(`<content><box background="paper"><code language="go">func main() {}</code></box></content>`)) // CONTROL
	if plain != "router.work:7:30: "+p.HighlightWarning(highlightCallAt) {
		t.Errorf("CONTROL: on plain paper the call's line alone, got\n%s", plain)
	}
}
