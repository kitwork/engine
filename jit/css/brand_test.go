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
		if css, _, _ := ResolveCore(cls); css != want {
			t.Errorf("%s → %q, want %q", cls, css, want)
		}
	}

	// Variants ride along: the property still resolves and the selector carries the pseudo.
	css, sel, _ := ResolveCore("hover:text-brand-acme")
	if css != "color: #181717;" {
		t.Errorf("hover brand colour lost: %q", css)
	}
	if !strings.Contains(sel, ":hover") {
		t.Errorf("hover variant should be on the selector: %q", sel)
	}

	// Unknown brand slug does not resolve (class is dropped, like any unknown colour).
	if css, _, _ := ResolveCore("text-brand-nope"); css != "" {
		t.Errorf("unknown brand should not resolve, got %q", css)
	}
}

// Without a registered palette, brand-* must not resolve (and must not panic).
func TestBrandColorUnwired(t *testing.T) {
	RegisterBrandPalette(nil)
	if css, _, _ := ResolveCore("text-brand-acme"); css != "" {
		t.Errorf("unwired brand-* should not resolve, got %q", css)
	}
}
