package logo

import (
	"strings"
	"testing"
)

// A logo the kernel toggles in — data-kitwork-class="logo-github: connected" — gets its rule on
// the page before the class arrives.
func TestRenderReadsLogosAToggleNames(t *testing.T) {
	out := Render(`<html><head></head><body><i data-kitwork-class="logo-github: connected; logo-github-brand: loud"></i></body></html>`)
	if strings.Count(out, `data-kitwork-generated="logo"`) != 1 || !strings.Contains(out, ".logo-github") || !strings.Contains(out, ".logo-github-brand") {
		t.Errorf("the toggled logo and its brand sugar must be styled: %s", out)
	}
	// control: the name inside a value or a condition is text, and the retired bind form toggles
	// nothing, so it brings no rule. The unquoted condition (logo - github, a subtraction) is the case
	// a class="…" pattern that is not anchored on white space reads as the class logo-github.
	for _, plain := range []string{
		`<html><head></head><body><p data-kitwork-bind="text: 'logo-github'"></p></body></html>`,
		`<html><head></head><body><p data-kitwork-class="on: brand == 'logo-github'"></p></body></html>`,
		`<html><head></head><body><p data-kitwork-class="on: logo-github > 0"></p></body></html>`,
		`<html><head></head><body><p data-kitwork-bind="class.logo-github: connected"></p></body></html>`,
	} {
		if got := Render(plain); got != plain {
			t.Errorf("not a class source: %s\n  got %s", plain, got)
		}
	}
}
