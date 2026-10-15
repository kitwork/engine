package material

import (
	"strings"
	"testing"
)

// A material class the kernel toggles in — data-kitwork-class="card: open" — brings its family to
// the page before the class arrives, or the element turns into a card with no card styling.
func TestRenderReadsFamiliesAToggleNames(t *testing.T) {
	out := Render(`<html><head></head><body><div data-kitwork-class="card: open; badge-success: done"></div></body></html>`)
	if !strings.Contains(out, ".card{") || !strings.Contains(out, ".badge{") {
		t.Errorf("the toggled card and badge families are expected: %s", out)
	}
	if strings.Contains(out, ".button,.btn{") {
		t.Errorf("the button family is not used: %s", out)
	}
	// control: a family word in a condition is not a class, and the retired bind form toggles nothing
	for _, plain := range []string{
		`<html><head></head><body><input data-kitwork-class="dirty: input != ''"></body></html>`,
		`<html><head></head><body><div data-kitwork-class="wide: table-mode"></div></body></html>`,
		`<html><head></head><body><div data-kitwork-bind="class.card: open"></div></body></html>`,
	} {
		if got := Render(plain); got != plain {
			t.Errorf("not a class source: %s\n  got %s", plain, got)
		}
	}
}
