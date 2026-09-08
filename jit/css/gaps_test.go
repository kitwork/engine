package css

import (
	"strings"
	"testing"
)

// An audit of the reference site found 60 utilities that were WRITTEN in markup but produced no CSS
// at all — silent failures, because an unmatched class simply emits nothing. The largest group was
// a version mismatch: the markup uses Tailwind v4 size names (`shadow-xs`, `rounded-xs`) while the
// engine only knew the v3 scale. That naming split was settled by committing to v3: the v4
// names were removed again and the markup renamed, so only genuine v3 gaps remain pinned here.

func TestSvgFillAndStroke(t *testing.T) {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["ink"] = Hex("#1a184a")

	if css, _, _ := ResolveCore("fill-ink", &cfg); !strings.Contains(css, "fill: rgb(var(--color-ink") {
		t.Errorf("fill-ink should paint a themeable fill, got %q", css)
	}
	if css, _, _ := ResolveCore("stroke-ink", &cfg); !strings.Contains(css, "stroke: rgb(var(--color-ink") {
		t.Errorf("stroke-ink should paint a themeable stroke, got %q", css)
	}
	// A number is a WIDTH, not a colour — the two forms must not be confused.
	if css, _, _ := ResolveCore("stroke-2", nil); css != "stroke-width: 2;" {
		t.Errorf("stroke-2 should set width, got %q", css)
	}
	if css, _, _ := ResolveCore("stroke-[1.5]", nil); css != "stroke-width: 1.5;" {
		t.Errorf("stroke-[1.5] should set width, got %q", css)
	}
}

func TestBorderStyleAndGradientAlpha(t *testing.T) {
	if css, _, _ := ResolveCore("border-dashed", nil); css != "border-style: dashed;" {
		t.Errorf("border-dashed got %q", css)
	}
	// CONTROL: border-dashed must not be swallowed by the colour pattern as the colour "dashed".
	if css, _, _ := ResolveCore("border-dashed", nil); strings.Contains(css, "border-color") {
		t.Errorf("border-dashed was read as a colour: %q", css)
	}
	css, _, _ := ResolveCore("from-black/20", nil)
	if !strings.Contains(css, "rgba(") || !strings.Contains(css, "0.20") {
		t.Errorf("gradient stops must honour the alpha modifier, got %q", css)
	}
}

func TestPlaceholderAndMotionVariants(t *testing.T) {
	// `ink` is not in the engine's default palette, so a config is required for the colour to
	// resolve at all — otherwise this would test nothing.
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["ink"] = Hex("#1a184a")

	_, sel, _ := ResolveCore("placeholder:text-ink", &cfg)
	if !strings.Contains(sel, "::placeholder") {
		t.Errorf("placeholder: variant should target ::placeholder, got %q", sel)
	}
	_, _, mq := ResolveCore("motion-safe:opacity-50", nil)
	if !strings.Contains(mq, "prefers-reduced-motion: no-preference") {
		t.Errorf("motion-safe: should wrap a reduced-motion media query, got %q", mq)
	}
	_, _, mq = ResolveCore("motion-reduce:opacity-100", nil)
	if !strings.Contains(mq, "prefers-reduced-motion: reduce") {
		t.Errorf("motion-reduce: got %q", mq)
	}
}
