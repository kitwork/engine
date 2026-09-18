package icons

import (
	"strings"
	"testing"
)

func TestHasAndNames(t *testing.T) {
	// mesh is the sole built-in; the rest now resolve from vendored Tabler (one consistent style).
	for _, n := range []string{"mesh", "shield-check", "lock", "key", "fingerprint", "signature", "binary"} {
		if !Has(n) {
			t.Errorf("expected icon %q to resolve (built-in or Tabler)", n)
		}
	}
	if Has("definitely-not-an-icon") {
		t.Error("unknown icon reported present")
	}
	if len(Names()) < 1 {
		t.Errorf("expected at least the built-in set, got %d names", len(Names()))
	}
}

func TestTablerVendored(t *testing.T) {
	// These resolve only once Tabler is vendored into ./tabler. Skip (don't fail) otherwise, so the
	// suite still passes on a build shipping only the built-in brand set.
	if !Has("home") {
		t.Skip("Tabler not vendored (./tabler has no svgs) — built-in set only")
	}
	for _, n := range []string{"home", "search", "user", "settings", "menu-2"} {
		if !Has(n) {
			t.Errorf("expected vendored Tabler icon %q", n)
		}
		if CSS([]string{n}) == "" {
			t.Errorf("CSS for vendored icon %q is empty", n)
		}
	}
	if got := len(Names()); got < 1000 {
		t.Errorf("expected the full Tabler set, got only %d names", got)
	}
	// Built-in brand icons must still win over any same-named Tabler file.
	if inner, _ := lookup("mesh"); !strings.Contains(inner, "5.5") {
		t.Errorf("built-in 'mesh' should win over Tabler, got: %s", inner)
	}

	// Regression: Tabler files open with an HTML comment header + a multi-line <svg> tag. svgInner
	// must strip the WHOLE wrapper, leaving only drawable content — no nested <svg>, no comment.
	inner, _ := lookup("sun")
	if strings.Contains(inner, "<svg") || strings.Contains(inner, "<!--") {
		t.Errorf("svgInner left the Tabler wrapper/comment in place: %q", inner)
	}
	if !strings.Contains(inner, "<path") {
		t.Errorf("svgInner dropped the drawable paths: %q", inner)
	}
	// The emitted data-URI must contain EXACTLY one encoded <svg> (our wrapper) — not a nested one.
	if n := strings.Count(CSS([]string{"sun"}), "%3Csvg"); n != 1 {
		t.Errorf("expected exactly one <svg> in the mask data-URI, got %d", n)
	}
}

func TestTablerFilled(t *testing.T) {
	// The filled set lives in ./tabler/filled and answers to the -fill suffix. Skip, don't fail,
	// on a build that ships only the outline set.
	if !Has("cloud-fill") {
		t.Skip("Tabler filled set not vendored (./tabler/filled has no svgs)")
	}
	for _, n := range []string{"cloud-fill", "star-fill", "heart-fill", "bell-fill"} {
		if !Has(n) {
			t.Errorf("expected vendored filled icon %q", n)
		}
	}
	// A -fill name that has no filled file is a miss, not a fallback to the outline drawing.
	if Has("arrow-right-fill") {
		t.Errorf("arrow-right has no filled variant; -fill must not fall back to the outline file")
	}
	// The filled wrapper paints fill, not stroke — and never bakes a stroke-width.
	css := CSS([]string{"cloud-fill"})
	if !strings.Contains(css, "fill='%23000' stroke='none'") {
		t.Errorf("filled icon should use the fill wrapper, got: %s", css)
	}
	if strings.Contains(css, "stroke-width") {
		t.Errorf("filled icon must not carry a stroke-width: %s", css)
	}
	// Control: the outline drawing of the same icon keeps the stroke wrapper.
	if out := CSS([]string{"cloud"}); !strings.Contains(out, "stroke-width='1.75'") || strings.Contains(out, "stroke='none'") {
		t.Errorf("outline icon should keep the stroke wrapper, got: %s", out)
	}
	// The spacer path is stripped from filled files too (their generator writes it with a space).
	inner, _ := lookup("cloud-fill")
	if strings.Contains(inner, "M0 0h24v24H0z") {
		t.Errorf("spacer path left in filled icon: %q", inner)
	}
	// Both styles are scanned from markup and listed in Names.
	if got := scan(`<i class="icon-cloud icon-cloud-fill"></i>`); len(got) != 2 {
		t.Errorf("scan should pick up both styles, got %v", got)
	}
	names := Names()
	hasFill := false
	for _, n := range names {
		if n == "cloud-fill" {
			hasFill = true
			break
		}
	}
	if !hasFill {
		t.Errorf("Names should list the filled set with its suffix")
	}
}

func TestCSSGeneratesMaskRules(t *testing.T) {
	css := CSS([]string{"shield-check", "lock", "definitely-not-an-icon"})

	// One shared base rule listing the exact selectors (no substring matching), with the mask.
	if !strings.Contains(css, ":where(.icon-lock,.icon-shield-check){") {
		t.Errorf("base :where rule missing or not sorted: %s", css)
	}
	if !strings.Contains(css, "mask:var(--i) center/contain no-repeat") || !strings.Contains(css, "background:currentColor") {
		t.Errorf("mask/currentColor declarations missing: %s", css)
	}
	// Per-icon mask variable as a data-URI (stroke baked to %23000, never currentColor).
	if !strings.Contains(css, `.icon-shield-check{--i:url("data:image/svg+xml,`) {
		t.Errorf("shield-check var missing: %s", css)
	}
	// currentColor must appear exactly once — the base `background:currentColor` paint — and never
	// inside a data-URI mask, where it can't resolve.
	if n := strings.Count(css, "currentColor"); n != 1 {
		t.Errorf("currentColor should appear once (the paint), got %d: %s", n, css)
	}
	// Unknown names are dropped, not emitted.
	if strings.Contains(css, "definitely-not-an-icon") {
		t.Errorf("unknown icon should not appear: %s", css)
	}
	if CSS([]string{"nope"}) != "" {
		t.Error("CSS of only-unknown names should be empty")
	}
}

func TestRenderInjectsStyleAndKeepsMarkup(t *testing.T) {
	html := `<html><head><title>x</title></head><body><i class="icon-shield-check"></i> <i class="icon-lock cube-ico"></i></body></html>`
	out := Render(html)

	// Markup is NOT rewritten — the <i> placeholders survive verbatim.
	if !strings.Contains(out, `<i class="icon-shield-check"></i>`) {
		t.Errorf("icon markup should be left untouched: %s", out)
	}
	// Exactly one style block, injected into <head>, carrying both icons' rules.
	if strings.Count(out, `data-kitwork-jit="icons"`) != 1 {
		t.Errorf("expected exactly one icon style block: %s", out)
	}
	si := strings.Index(out, `<style data-kitwork-jit="icons">`)
	if hi := strings.Index(out, "</head>"); si < 0 || si > hi {
		t.Errorf("icon style should be injected before </head>: %s", out)
	}
	if !strings.Contains(out, ".icon-shield-check{--i:") || !strings.Contains(out, ".icon-lock{--i:") {
		t.Errorf("both icon rules expected: %s", out)
	}
	// No `kw` prefix anywhere in the emitted machinery.
	if strings.Contains(out, "kwi-") || strings.Contains(out, "kw-i") {
		t.Errorf("output still contains a kw- prefix: %s", out)
	}
}

func TestRenderIgnoresUnknownAndNonIcons(t *testing.T) {
	// FA-style <i> (no `icon-` token) → returned verbatim.
	fa := `<body><i class="fa-duotone fa-home"></i><em>x</em></body>`
	if out := Render(fa); out != fa {
		t.Errorf("expected unchanged output, got: %s", out)
	}
	// An unknown `icon-<name>` → no rule, returned verbatim.
	unknown := `<body><i class="icon-nope-not-real"></i></body>`
	if out := Render(unknown); out != unknown {
		t.Errorf("unknown icon should be left as-is, got: %s", out)
	}
	// A class that merely CONTAINS "icon-" mid-string must NOT match (token boundary).
	mid := `<body><div class="my-icon-thing">x</div></body>`
	if out := Render(mid); out != mid {
		t.Errorf("mid-string icon- should not match: %s", out)
	}
}

// An icon named only inside a data-kit-class expression is a class the page will wear — both
// branches of `open ? 'icon-x' : 'icon-menu-2'` — so both get a rule. The scan used to read
// class="…" alone, and the branch the first paint did not take drew an empty box.
func TestRenderReadsIconsFromDynamicClasses(t *testing.T) {
	html := `<html><head></head><body><i class="block h-4 w-4" data-kit-class="open ? 'icon-x' : 'icon-menu-2'"></i></body></html>`
	out := Render(html)
	for _, want := range []string{".icon-x{--i:", ".icon-menu-2{--i:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in: %s", want, out)
		}
	}
	// CONTROL: a plain string that merely contains the prefix is not a class.
	plain := `<body><p data-kit-text="'icon-x'"></p></body>`
	if got := Render(plain); got != plain {
		t.Errorf("data-kit-text is not a class source: %s", got)
	}
}
