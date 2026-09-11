package css

import (
	"strings"
	"testing"
)

// A token written as `{ DEFAULT: …, dark: … }` flattens to `canvas` and `canvas-dark`, but until
// this the two were unrelated: a site had to spell `dark:bg-canvas-dark` on every element, and any
// element that forgot stayed light. The dark rung now overrides the SAME variable the light one
// defines, so one declaration re-skins the page — the reason the variable indirection exists.
func TestDarkRungOverridesItsOwnVariable(t *testing.T) {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{
		"canvas": Hex("#f7f7f8"), "canvas-dark": Hex("#0e1014"),
		"ink": Hex("#25262c"), "ink-dark": Hex("#e7e8ec"),
		"brand": Hex("#f82244"), // no dark rung: must not appear in the dark block
	}
	css := buildJITCSS([]string{"bg-canvas"}, &cfg)

	root := css[strings.Index(css, ":root {"):]
	if !strings.Contains(root, "--color-canvas: 247, 247, 248;") {
		t.Error("the light value must still define the variable at :root")
	}
	dark := css[strings.Index(css, ".dark {"):]
	dark = dark[:strings.Index(dark, "}")]
	for _, want := range []string{"--color-canvas: 14, 16, 20;", "--color-ink: 231, 232, 236;"} {
		if !strings.Contains(dark, want) {
			t.Errorf(".dark block = %q, want it to contain %q", dark, want)
		}
	}
	// A token with no dark rung must not be touched, or every site gets a dark theme it never asked
	// for — and `brand-dark` is not a thing here, so writing one would invent a colour.
	if strings.Contains(dark, "--color-brand:") {
		t.Errorf(".dark block overrode a token that declared no dark rung: %q", dark)
	}
	// The rung itself stays addressable as its own token; sites already write bg-canvas-dark.
	if !strings.Contains(root, "--color-canvas-dark:") {
		t.Error("the -dark rung must remain a token of its own")
	}
}

// A site with one committed palette must pay nothing: no selector, no bytes.
func TestNoDarkBlockWithoutDarkRungs(t *testing.T) {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{"canvas": Hex("#ffffff"), "ink": Hex("#111111")}
	css := buildJITCSS([]string{"bg-canvas"}, &cfg)
	if strings.Contains(css, ".dark {") {
		t.Error("a palette with no dark rungs still emitted a dark block")
	}
}

// darkMode: ['class', '<selector>'] moves the dark: variant; the token block must follow it, or the
// variables flip on a selector the variants never match.
func TestDarkBlockFollowsTheConfiguredSelector(t *testing.T) {
	cfg := DefaultConfig
	cfg.DarkSelector = `[data-theme="dark"]`
	cfg.Colors = map[string]Color{"canvas": Hex("#ffffff"), "canvas-dark": Hex("#000000")}
	css := buildJITCSS([]string{"bg-canvas"}, &cfg)
	if !strings.Contains(css, `[data-theme="dark"] { --color-canvas: 0, 0, 0; }`) {
		t.Errorf("dark block did not use the configured selector; got %q", css[strings.Index(css, ":root {"):])
	}
}
