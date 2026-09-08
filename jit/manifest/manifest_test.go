package manifest

import (
	"strings"
	"testing"
)

const page = `<html><head><title>x</title></head><body>hi</body></html>`

func TestRenderInjectsTheLinkWhenNoMarkerIsPresent(t *testing.T) {
	out := Render(page, "/manifest.json")
	if !strings.Contains(out, `rel="manifest"`) || !strings.Contains(out, `href="/manifest.json"`) {
		t.Fatalf("the manifest link was not injected.\ngot: %s", out)
	}
	if strings.Index(out, `rel="manifest"`) > strings.Index(out, "</head>") {
		t.Fatalf("the link landed outside <head>.\ngot: %s", out)
	}
}

// The author owns the position when they place a marker — the same rule
// jit/theme follows.
func TestRenderFillsAnAuthorPlacedMarkerInPlace(t *testing.T) {
	source := `<html><head><link data-kitwork-jit="manifest"><title>x</title></head><body></body></html>`
	out := Render(source, "/manifest.json")

	if strings.Count(out, `rel="manifest"`) != 1 {
		t.Fatalf("expected exactly one manifest link, got %d.\n%s", strings.Count(out, `rel="manifest"`), out)
	}
	if strings.Index(out, `rel="manifest"`) > strings.Index(out, "<title>") {
		t.Fatalf("the marker was not filled in place; the link moved past the author's position.\ngot: %s", out)
	}
}

// A site with no manifest must never gain a link to a document that would 404.
func TestRenderIsANoOpWithoutAManifest(t *testing.T) {
	if out := Render(page, ""); out != page {
		t.Fatalf("a site that declared no manifest was modified.\ngot: %s", out)
	}
}

func TestRenderIsIdempotent(t *testing.T) {
	once := Render(page, "/manifest.json")
	twice := Render(once, "/manifest.json")
	if once != twice {
		t.Fatalf("a second pass changed the output.\nonce:  %s\ntwice: %s", once, twice)
	}
	if strings.Count(twice, `rel="manifest"`) != 1 {
		t.Fatalf("the second pass added a duplicate link.\ngot: %s", twice)
	}
}

func TestRenderEscapesThePath(t *testing.T) {
	out := Render(page, `/a"><script>alert(1)</script>`)
	if strings.Contains(out, "<script>alert(1)") {
		t.Fatalf("the path escaped its attribute and injected markup.\ngot: %s", out)
	}
}

func TestRenderAcceptsTheShortMarkerPrefix(t *testing.T) {
	source := `<html><head><link data-kit-jit="manifest"></head><body></body></html>`
	if out := Render(source, "/manifest.json"); !strings.Contains(out, `href="/manifest.json"`) {
		t.Fatalf("the short-prefix marker was not recognized.\ngot: %s", out)
	}
}
