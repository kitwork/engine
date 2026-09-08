package css

import (
	"strings"
	"testing"
)

// Design tokens are named by role, never numbered. A numeric shade on one used to be IGNORED, so
// bg-brand-100, bg-brand-500 and bg-brand-900 all painted the same colour — and silently dropped
// the themeable var() form, because only the unshaded path emits it. A plausible-looking wrong
// colour is worse than no colour: Tailwind emits nothing for a shade you never defined, so do that.
func TestNumericShadeOnATokenEmitsNothing(t *testing.T) {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["brand"] = Hex("#635bff")
	cfg.Colors["ink"] = Hex("#1a184a")

	for _, cls := range []string{"bg-brand-100", "bg-brand-500", "bg-brand-900", "text-ink-700", "border-brand-50"} {
		if css, _, _ := ResolveCore(cls, &cfg); css != "" {
			t.Errorf("%s should emit nothing, got %q", cls, css)
		}
	}

	// The role names themselves must still work, and stay themeable.
	css, _, _ := ResolveCore("bg-brand", &cfg)
	if !strings.Contains(css, "var(--color-brand") {
		t.Errorf("bg-brand must stay themeable, got %q", css)
	}

	// CONTROL 1: `sky` is BOTH a config token and a real Tailwind family. Numbered sky must keep
	// resolving through the Tailwind palette — this fix must not cost us 1000+ live usages.
	cfg.Colors["sky"] = Hex("#e1effe")
	if css, _, _ := ResolveCore("text-sky-300", &cfg); !strings.Contains(css, "color: rgb(") {
		t.Errorf("text-sky-300 must still resolve from the Tailwind palette, got %q", css)
	}
	// CONTROL 2: and the unshaded token form still wins for the same name.
	if css, _, _ := ResolveCore("bg-sky", &cfg); !strings.Contains(css, "var(--color-sky") {
		t.Errorf("bg-sky should use the config token, got %q", css)
	}
}
