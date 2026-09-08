package css

import (
	"strings"
	"testing"
)

func catchupCfg() Config {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["brand"] = Hex("#635bff")
	cfg.Colors["line"] = Hex("#e6ebf1")
	return cfg
}

// border-t-white resolved to nothing: the generic colour pattern read it as a colour named
// "t-white", which does not exist, so the class silently produced no rule.
func TestBorderSideColour(t *testing.T) {
	cfg := catchupCfg()
	if css, _, _ := ResolveCore("border-t-white", &cfg); !strings.Contains(css, "border-top-color:") {
		t.Errorf("border-t-white got %q", css)
	}
	if css, _, _ := ResolveCore("border-x-line/50", &cfg); !strings.Contains(css, "border-left-color:") || !strings.Contains(css, "border-right-color:") {
		t.Errorf("border-x-line/50 should colour both sides, got %q", css)
	}
	// CONTROL: the width form must keep winning — border-t-2 is a width, not a colour.
	if css, _, _ := ResolveCore("border-t-2", &cfg); !strings.Contains(css, "width") {
		t.Errorf("border-t-2 must stay a width, got %q", css)
	}
}

func TestGradientStopShadeWithAlpha(t *testing.T) {
	css, _, _ := ResolveCore("to-slate-950/95", nil)
	if !strings.Contains(css, "rgba(") || !strings.Contains(css, "0.95") {
		t.Errorf("a numbered gradient stop should honour alpha, got %q", css)
	}
}

// shadow-<colour> recolours the shadow a size utility already drew; it must never draw one itself,
// or `shadow-md shadow-brand` would lose the md geometry.
func TestColouredShadow(t *testing.T) {
	cfg := catchupCfg()
	css, _, _ := ResolveCore("shadow-brand/25", &cfg)
	if !strings.Contains(css, "--tw-shadow-color:") {
		t.Errorf("shadow-brand/25 should set the shadow colour variable, got %q", css)
	}
	if strings.Contains(css, "box-shadow:") {
		t.Errorf("shadow-<colour> must not draw a shadow itself: %q", css)
	}
	if !strings.Contains(css, "0.25") {
		t.Errorf("alpha lost: %q", css)
	}

	// The size still carries the geometry, and reads the colour variable so the pair composes.
	size, _, _ := ResolveCore("shadow-md", &cfg)
	if !strings.Contains(size, "var(--tw-shadow-color") {
		t.Errorf("shadow-md must read the colour variable so shadow-<colour> can override it: %q", size)
	}
	// CONTROL: `md` must be read as a SIZE, not as a colour named "md".
	if strings.Contains(size, "--tw-shadow-color:") {
		t.Errorf("shadow-md was parsed as a colour: %q", size)
	}
}

// An ancestor's shadow colour must not inherit into children that never asked for it.
func TestShadowColourDoesNotInherit(t *testing.T) {
	css := GenerateJITCached(`<div class="shadow-md">x</div>`, nil)
	if !strings.Contains(css, "--tw-shadow-color: initial") {
		t.Error("Preflight must reset --tw-shadow-color, or a coloured shadow leaks to descendants")
	}
}
