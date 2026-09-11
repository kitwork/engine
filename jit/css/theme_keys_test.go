package css

import (
	"strings"
	"testing"
)

// theme.fontFamily and theme.boxShadow were both READ from the router config and then never
// consulted when resolving a class: `font-sans` always returned a hard-coded system stack, and
// `shadow-glow` returned nothing at all. A config key that is accepted and ignored is worse than a
// rejected one — the site looks configured and renders as if it were not.
func TestThemeKeysAreActuallyUsed(t *testing.T) {
	cfg := DefaultConfig
	cfg.FontFamily = map[string]string{
		"sans":    "'Plus Jakarta Sans', system-ui, sans-serif",
		"display": "'Outfit', sans-serif",
	}
	cfg.ShadowLevels = map[string]string{"glow": "0 0 40px rgba(248,34,68,0.15)"}

	if css, _, _ := ResolveCore("font-sans", &cfg); !strings.Contains(css, "Plus Jakarta Sans") {
		t.Errorf("font-sans → %q, want the site's declared stack, not the built-in one", css)
	}
	if css, _, _ := ResolveCore("font-display", &cfg); !strings.Contains(css, "Outfit") {
		t.Errorf("font-display → %q, want the site's declared stack", css)
	}
	if css, _, _ := ResolveCore("shadow-glow", &cfg); !strings.Contains(css, "0 0 40px rgba(248,34,68,0.15)") {
		t.Errorf("shadow-glow → %q, want the site's declared shadow", css)
	}

	// A key the site never declared must render NOTHING rather than quietly falling back to the
	// default shadow — a wrong shadow is harder to notice than a missing one.
	if css, _, _ := ResolveCore("shadow-nosuchkey", &cfg); strings.Contains(css, "box-shadow") {
		t.Errorf("shadow-nosuchkey → %q, want no shadow at all", css)
	}
	// …and `font-bold` must still reach the weight handler rather than being eaten by font-family.
	if css, _, _ := ResolveCore("font-bold", &cfg); !strings.Contains(css, "font-weight: 700") {
		t.Errorf("font-bold → %q, want a font-weight", css)
	}
	// shadow-<colour> must still work: the size pattern now matches the same shape, so it has to
	// step aside for a name it does not own.
	cfg.Colors = map[string]Color{"brand": Hex("#635bff")}
	if css, _, _ := ResolveCore("shadow-brand", &cfg); !strings.Contains(css, "--kitwork-shadow-color") {
		t.Errorf("shadow-brand → %q, want a shadow colour", css)
	}
}

// Arbitrary colours were limited to bg/text/border/decoration/accent with a whole-percent alpha, so
// `outline-[#e8173a]`, `shadow-[#e8173a]/20` and `bg-[#e8173a]/[0.02]` — all live on kitwork.vn —
// produced nothing.
func TestArbitraryColourReach(t *testing.T) {
	cfg := DefaultConfig
	for _, c := range []struct{ cls, want string }{
		{"outline-[#e8173a]", "outline-color: #e8173a;"},
		{"ring-[#e8173a]", "--kitwork-ring-color: #e8173a;"},
		{"shadow-[#e8173a]/20", "--kitwork-shadow-color: #e8173a33;"},
		{"stroke-[#365047]", "stroke: #365047;"},
		{"fill-[#365047]", "fill: #365047;"},
		{"bg-[#e8173a]/[0.02]", "background-color: #e8173a05;"},
	} {
		css, _, _ := ResolveCore(c.cls, &cfg)
		if !strings.Contains(css, c.want) {
			t.Errorf("%s → %q, want %q", c.cls, css, c.want)
		}
	}
	// divide-[#hex] must keep the child combinator, or it colours the container's own border
	// instead of the rules between its children.
	_, sel, _ := ResolveCore("divide-[#365047]", &cfg)
	if !strings.Contains(sel, "> :not([hidden]) ~ :not([hidden])") {
		t.Errorf("divide-[#365047] selector = %q, want the between-children combinator", sel)
	}
}
