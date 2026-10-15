package fonts

import (
	"strings"
	"testing"
)

// A family the kernel toggles in — data-kitwork-class="font-outfit: wide" — gets its @font-face and
// its .font-outfit utility on the page before the class arrives, or the switch draws the fallback.
func TestRenderReadsFamiliesAToggleNames(t *testing.T) {
	out := Render(`<html><head></head><body><p data-kitwork-class="font-outfit: wide; font-bold: loud">x</p></body></html>`)
	if !strings.Contains(out, "@font-face{font-family:'Outfit'") {
		t.Errorf("the toggled family needs its @font-face: %s", out)
	}
	if !strings.Contains(out, ".font-outfit{font-family:'Outfit',") {
		t.Errorf("the toggled family needs its .font-outfit utility: %s", out)
	}
	if strings.Count(out, styleMarker) != 1 {
		t.Errorf("one fonts style, got: %s", out)
	}
	// control: a family named inside a condition is text — not a class this page can carry — and the
	// retired bind form toggles nothing, so neither brings a face
	for _, plain := range []string{
		`<html><head></head><body><p data-kitwork-class="wide: font-outfit">x</p></body></html>`,
		`<html><head></head><body><p data-kitwork-class="wide: face == 'font-outfit'">x</p></body></html>`,
		`<html><head></head><body><p data-kitwork-bind="class.font-outfit: wide">x</p></body></html>`,
	} {
		if got := Render(plain); got != plain {
			t.Errorf("not a class source: %s\n  got %s", plain, got)
		}
	}
}
