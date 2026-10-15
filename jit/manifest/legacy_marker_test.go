package manifest

import (
	"strings"
	"testing"
)

// An author-placed slot written before the rename (10/10/2026), <link data-kitwork-jit="manifest">, is
// still filled in place, under today's name.
func TestLegacySlotIsStillFilled(t *testing.T) {
	source := `<html><head><title>x</title><link data-kitwork-jit="manifest"><meta name="a"></head><body></body></html>`
	out := Render(source, "/manifest.webmanifest")
	if strings.Contains(out, "data-kitwork-jit") {
		t.Errorf("the old slot must be replaced by today's link:\n%s", out)
	}
	if strings.Count(out, `rel="manifest"`) != 1 {
		t.Fatalf("one manifest link, in the slot:\n%s", out)
	}
	link := strings.Index(out, `data-kitwork-generated="manifest"`)
	if !(strings.Index(out, "<title>") < link && link < strings.Index(out, `<meta name="a">`)) {
		t.Errorf("the link must take the author's slot, not the end of <head>:\n%s", out)
	}
}
