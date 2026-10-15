package icons

import (
	"strings"
	"testing"
)

// The appearance component swaps its icon on the client: data-kitwork-class="icon-sun: dark; icon-moon: !dark".
// The server prints one of the two; both rules must be on the page, or the swap draws nothing.
func TestRenderReadsIconsAToggleNames(t *testing.T) {
	html := `<html><head></head><body><i class="icon-moon" data-kitwork-class="icon-sun: dark; icon-moon: !dark"></i></body></html>`
	out := Render(html)
	for _, want := range []string{".icon-sun{--i:", ".icon-moon{--i:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in: %s", want, out)
		}
	}
	// control: the same names inside a value or a condition are text, not classes, and the retired
	// bind form (class.icon-sun in data-kitwork-bind) toggles nothing, so it brings no rule. The
	// unquoted condition (icon - sun, a subtraction) is the case a class="…" pattern that is not
	// anchored on white space reads as the class icon-sun.
	for _, plain := range []string{
		`<html><head></head><body><p data-kitwork-bind="text: 'icon-sun'; title: 'class.icon-sun: x'"></p></body></html>`,
		`<html><head></head><body><p data-kitwork-class="dark: icon == 'icon-sun'"></p></body></html>`,
		`<html><head></head><body><p data-kitwork-class="dark: icon-sun > 0"></p></body></html>`,
		`<html><head></head><body><p data-kitwork-bind="class.icon-sun: dark"></p></body></html>`,
	} {
		if got := Render(plain); got != plain {
			t.Errorf("not a class source: %s\n  got %s", plain, got)
		}
	}
}
