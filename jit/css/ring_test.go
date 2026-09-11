package css

import (
	"strings"
	"testing"
)

// `ring-*` rendered NOTHING before this: there was no width pattern at all, and `ring-<colour>`
// emitted `box-shadow: rgb(…)` — not a valid box-shadow, so browsers dropped the declaration.
// 176 elements in the reference site carried a ring class and 175 computed to `box-shadow: none`.
// These pin the Tailwind model: width builds the shadow, colour only feeds a variable.
func TestRingWidthEmitsARealShadow(t *testing.T) {
	for _, cls := range []string{"ring", "ring-1", "ring-2", "ring-4"} {
		css, _, _ := ResolveCore(cls, nil)
		if css == "" {
			t.Fatalf("%s produced no CSS at all", cls)
		}
		if !strings.Contains(css, "box-shadow:") {
			t.Errorf("%s must build a box-shadow, got %q", cls, css)
		}
		if !strings.Contains(css, "var(--kitwork-ring-color)") {
			t.Errorf("%s must read the ring colour variable, got %q", cls, css)
		}
	}
}

func TestRingColourOnlySetsTheVariable(t *testing.T) {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["line"] = Hex("#e6ebf1")

	css, _, _ := ResolveCore("ring-line", &cfg)
	if !strings.Contains(css, "--kitwork-ring-color:") {
		t.Errorf("ring-line must set --kitwork-ring-color, got %q", css)
	}
	// CONTROL: the old bug. Assigning the colour straight to box-shadow yields an invalid
	// declaration, so this must NOT come back.
	if strings.Contains(css, "box-shadow:") {
		t.Errorf("ring-line must not assign box-shadow directly — that is the invalid form: %q", css)
	}
	if !strings.Contains(css, "var(--color-line") {
		t.Errorf("ring colour should stay themeable, got %q", css)
	}
}

func TestRingInsetAndOffset(t *testing.T) {
	if css, _, _ := ResolveCore("ring-inset", nil); !strings.Contains(css, "--kitwork-ring-inset: inset") {
		t.Errorf("ring-inset got %q", css)
	}
	if css, _, _ := ResolveCore("ring-offset-2", nil); !strings.Contains(css, "--kitwork-ring-offset-width: 2px") {
		t.Errorf("ring-offset-2 got %q", css)
	}

	// CONTROL: `ring-offset-canvas` must be read as an OFFSET COLOUR. If the generic colour pattern
	// won the race it would be parsed as the colour named "offset-canvas" and silently emit nothing.
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["canvas"] = Hex("#f6f9fc")
	css, _, _ := ResolveCore("ring-offset-canvas", &cfg)
	if !strings.Contains(css, "--kitwork-ring-offset-color:") {
		t.Errorf("ring-offset-canvas must set the offset colour, got %q", css)
	}
}

// A card commonly carries both. Before composition the two rules fought and one was lost.
func TestRingAndShadowCompose(t *testing.T) {
	shadow, _, _ := ResolveCore("shadow-md", nil)
	if !strings.Contains(shadow, "--kitwork-shadow:") {
		t.Errorf("shadow-md must feed --kitwork-shadow so a ring can coexist, got %q", shadow)
	}
	ring, _, _ := ResolveCore("ring-1", nil)
	for _, want := range []string{"var(--kitwork-ring-offset-shadow)", "var(--kitwork-ring-shadow)", "var(--kitwork-shadow)"} {
		if !strings.Contains(shadow, want) || !strings.Contains(ring, want) {
			t.Errorf("both shadow-md and ring-1 must compose %s\n shadow=%q\n ring=%q", want, shadow, ring)
		}
	}
}

// The variables the composed shadow reads must be defined, or the whole declaration is invalid.
func TestPreflightDefinesRingVariables(t *testing.T) {
	css := GenerateJITCached(`<div class="ring-1">x</div>`, nil)
	for _, want := range []string{
		"--kitwork-ring-inset:", "--kitwork-ring-offset-width: 0px", "--kitwork-ring-offset-color:",
		"--kitwork-ring-color:", "--kitwork-ring-offset-shadow: 0 0 #0000", "--kitwork-ring-shadow: 0 0 #0000",
		"--kitwork-shadow: 0 0 #0000",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("Preflight is missing %q — the composed box-shadow would be invalid", want)
		}
	}
}
