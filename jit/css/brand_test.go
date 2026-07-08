package css

import (
	"strings"
	"testing"
)

// text-brand-<slug> / bg-brand-<slug> / … resolve a registered brand colour through the normal
// colour machinery — every colour utility, alpha and variant for free, with one twColor branch.
func TestBrandColorUtilities(t *testing.T) {
	RegisterBrandPalette(func(slug string) (string, bool) {
		if slug == "acme" {
			return "#181717", true
		}
		return "", false
	})
	defer RegisterBrandPalette(nil)

	cases := map[string]string{
		"text-brand-acme":   "color: #181717;",
		"bg-brand-acme":     "background-color: #181717;",
		"border-brand-acme": "border-color: #181717;",
	}
	for cls, want := range cases {
		if css, _, _ := ResolveCore(cls, nil); css != want {
			t.Errorf("%s → %q, want %q", cls, css, want)
		}
	}

	// Variants ride along: the property still resolves and the selector carries the pseudo.
	css, sel, _ := ResolveCore("hover:text-brand-acme", nil)
	if css != "color: #181717;" {
		t.Errorf("hover brand colour lost: %q", css)
	}
	if !strings.Contains(sel, ":hover") {
		t.Errorf("hover variant should be on the selector: %q", sel)
	}

	// Unknown brand slug does not resolve (class is dropped, like any unknown colour).
	if css, _, _ := ResolveCore("text-brand-nope", nil); css != "" {
		t.Errorf("unknown brand should not resolve, got %q", css)
	}
}

// Without a registered palette, brand-* must not resolve (and must not panic).
func TestBrandColorUnwired(t *testing.T) {
	RegisterBrandPalette(nil)
	if css, _, _ := ResolveCore("text-brand-acme", nil); css != "" {
		t.Errorf("unwired brand-* should not resolve, got %q", css)
	}
}

func TestJITPreflightAndBoxSizingUtilities(t *testing.T) {
	out := GenerateJITCached(`<a class="box-border border border-t w-[240px] min-h-dvh px-6 flex-shrink-0 dark:text-zinc-100"></a>`, nil)

	for _, want := range []string{
		"*, ::before, ::after { box-sizing: border-box;",
		"body { margin: 0; line-height: inherit; }",
		"a { color: inherit; text-decoration: inherit; }",
		".box-border { box-sizing: border-box; }",
		".border { border-width: 1px; border-style: solid; }",
		".border-t { border-top-width: 1px; border-top-style: solid; }",
		".flex-shrink-0 { flex-shrink: 0; }",
		".min-h-dvh { min-height: 100dvh; }",
		".w-\\[240px\\] { width: 240px; }",
		".px-6 { padding-left: 1.5rem; padding-right: 1.5rem; }",
		".dark .dark\\:text-zinc-100 { color: rgb(244, 244, 245); }",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated CSS missing %q in:\n%s", want, out)
		}
	}
}

func TestArbitraryPropertyUtility(t *testing.T) {
	out := GenerateJITCached(`<h1 class="[font-variation-settings:'opsz'_72]"></h1>`, nil)

	if !strings.Contains(out, "font-variation-settings: 'opsz' 72;") {
		t.Fatalf("generated CSS missing arbitrary property in:\n%s", out)
	}
}
