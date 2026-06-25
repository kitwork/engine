package logo

import (
	"strings"
	"testing"

	css "github.com/kitwork/engine/jit/css"
)

// End-to-end: importing jit/logo runs its init(), registering BrandHex into jit/css — so the real
// brandColor map drives `text-brand-<slug>` without a stub. Proves the cross-package wiring.
func TestBrandColorWiredIntoCSS(t *testing.T) {
	if hex, ok := BrandHex("github"); !ok || hex != "#181717" {
		t.Fatalf("BrandHex(github) = %q,%v want #181717,true", hex, ok)
	}
	if got, _, _ := css.ResolveCore("text-brand-github"); got != "color: #181717;" {
		t.Errorf("text-brand-github → %q via the real palette, want color: #181717;", got)
	}
	if got, _, _ := css.ResolveCore("bg-brand-github"); got != "background-color: #181717;" {
		t.Errorf("bg-brand-github → %q, want background-color: #181717;", got)
	}
}

func TestHasAndNames(t *testing.T) {
	for _, n := range []string{"github", "x", "apple", "google", "substack", "rss"} {
		if !Has(n) {
			t.Errorf("expected built-in logo %q", n)
		}
	}
	if Has("definitely-not-a-brand") {
		t.Error("unknown logo reported present")
	}
	if len(Names()) < 9 {
		t.Errorf("expected the built-in set, got %d", len(Names()))
	}
}

func TestCSSFilledMask(t *testing.T) {
	css := CSS([]string{"github", "x", "nope"})
	if !strings.Contains(css, ":where(.logo-github,.logo-x){") {
		t.Errorf("base :where rule missing/not sorted: %s", css)
	}
	if !strings.Contains(css, "background:currentColor") || !strings.Contains(css, "mask:var(--i) center/contain no-repeat") {
		t.Errorf("mask/currentColor declarations missing: %s", css)
	}
	if !strings.Contains(css, `.logo-github{--i:url("data:image/svg+xml,`) {
		t.Errorf("github var missing: %s", css)
	}
	// Logos are FILLED: the wrapper bakes fill=#000 (→ %23000) and carries no stroke.
	if !strings.Contains(css, "fill='%23000'") {
		t.Errorf("expected filled wrapper: %s", css)
	}
	if strings.Contains(css, "stroke") {
		t.Errorf("logos are filled — no stroke expected: %s", css)
	}
	// Monochrome by default: currentColor appears once (the base paint), no brand colour baked.
	if n := strings.Count(css, "currentColor"); n != 1 {
		t.Errorf("currentColor should appear once (base paint), got %d", n)
	}
	if strings.Contains(css, "logo-color") || strings.Contains(css, "--logo-brand") {
		t.Errorf("the old logo-color modifier must be gone: %s", css)
	}
	if CSS([]string{"nope"}) != "" {
		t.Error("only-unknown names should be empty")
	}
}

// The `-brand` sugar paints the shape in its own official brand colour, while bare stays mono.
func TestCSSBrandSugar(t *testing.T) {
	css := CSS([]string{"github-brand"})
	if !strings.Contains(css, ":where(.logo-github-brand){") {
		t.Errorf("brand variant should be in the base rule: %s", css)
	}
	if !strings.Contains(css, `.logo-github-brand{--i:url("data:image/svg+xml,`) {
		t.Errorf("brand variant mask var missing: %s", css)
	}
	if !strings.Contains(css, ".logo-github-brand{color:#181717}") {
		t.Errorf("brand variant should bake the official hex: %s", css)
	}

	// When both variants appear, the mask data-URI is emitted once on a shared selector and the
	// colour only on the -brand selector.
	both := CSS([]string{"github", "github-brand"})
	if !strings.Contains(both, ":where(.logo-github,.logo-github-brand){") {
		t.Errorf("both selectors expected in base: %s", both)
	}
	if !strings.Contains(both, `.logo-github,.logo-github-brand{--i:url("data:image/svg+xml,`) {
		t.Errorf("mask var should be shared by both variants: %s", both)
	}
	if strings.Count(both, "--i:url") != 1 {
		t.Errorf("mask data-URI should be emitted once, got %d: %s", strings.Count(both, "--i:url"), both)
	}
	if !strings.Contains(both, ".logo-github-brand{color:#181717}") {
		t.Errorf("brand colour still applies to the -brand selector: %s", both)
	}
}

func TestRenderInjectsAndKeepsMarkup(t *testing.T) {
	out := Render(`<html><head></head><body><i class="logo-github"></i></body></html>`)
	if strings.Count(out, `data-kitwork-jit="logo"`) != 1 {
		t.Errorf("expected exactly one logo style block: %s", out)
	}
	if !strings.Contains(out, `<i class="logo-github"></i>`) {
		t.Errorf("markup should be untouched: %s", out)
	}
	si := strings.Index(out, `<style data-kitwork-jit="logo">`)
	if hi := strings.Index(out, "</head>"); si < 0 || si > hi {
		t.Errorf("logo style should be injected before </head>: %s", out)
	}
}

func TestRenderIgnoresNonLogos(t *testing.T) {
	in := `<body><i class="brand-thing"></i><div class="my-logo-wrap">x</div></body>`
	if out := Render(in); out != in {
		t.Errorf("non-logo / mid-string token should be untouched: %s", out)
	}
	unknown := `<body><i class="logo-nope-not-real"></i></body>`
	if out := Render(unknown); out != unknown {
		t.Errorf("unknown logo should be left as-is: %s", out)
	}
}
