package theme

import (
	"strings"
	"testing"
)

// A template written before 10/10/2026 pins the pre-paint with data-kitwork-jit="theme", and one
// written before router.theme() became router.appearance() (11/10/2026) with
// data-kitwork-generated="theme". The slot still holds: it is filled in place, under today's name,
// data-kitwork-generated="appearance", and only once.
func TestLegacyMarkerStillPinsThePrepaint(t *testing.T) {
	for _, legacy := range []string{`data-kitwork-jit="theme"`, `data-kitwork-generated="theme"`} {
		out := Render(`<head><title>x</title><script ` + legacy + `></script><meta name="a"></head>`)
		if strings.Contains(out, legacy) {
			t.Errorf("the old name %s must be replaced by today's:\n%s", legacy, out)
		}
		if strings.Count(out, `data-kitwork-generated="appearance"`) != 1 || !injected(out) {
			t.Fatalf("the slot %s must be filled once, with today's pre-paint:\n%s", legacy, out)
		}
		// it stays where the author put it: after <title>, before the meta, not moved to the top of <head>
		if !(strings.Index(out, "<title>") < strings.Index(out, `data-kitwork-generated="appearance"`) &&
			strings.Index(out, `data-kitwork-generated="appearance"`) < strings.Index(out, `<meta name="a">`)) {
			t.Errorf("the pre-paint must stay at the author's slot (%s):\n%s", legacy, out)
		}
	}
}

// A page that already holds the pre-paint under an old marker (a cached render) is the same page to
// Canonicalize: one script, today's name, today's body — never a second copy beside the first.
func TestCanonicalizeRewritesAnOldPrepaintToToday(t *testing.T) {
	old := `<head><script data-kitwork-generated="theme">(function(){/* yesterday */})();</script><title>x</title></head>`
	out := Canonicalize(old)
	if strings.Count(out, "<script") != 1 || !injected(out) || strings.Contains(out, "yesterday") {
		t.Fatalf("an old pre-paint must become today's, once:\n%s", out)
	}
	// control: a page with no pre-paint gains none from Canonicalize
	plain := `<head><title>x</title></head><body><div data-kit-component="appearance"></div></body>`
	if Canonicalize(plain) != plain {
		t.Fatalf("Canonicalize must never introduce a marker:\n%s", Canonicalize(plain))
	}
}
