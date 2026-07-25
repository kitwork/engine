package css

import (
	"strings"
	"testing"
)

// Before this token existed the engine had TWO variable names for the brand colour and defined
// NEITHER: every `var(--kitwork-brand, #f82244)` and `var(--color-brand-rgb, #f82244)` fell through
// to its hardcoded fallback, so the var() wrappers were decoration and the accent could not be
// changed at all. These tests pin the two things that were missing: the token is emitted, and it
// follows the site's configuration.

func TestBrandTokenIsEmitted(t *testing.T) {
	css := GenerateJITCached(`<div class="flex">x</div>`, nil)

	for _, want := range []string{"--kitwork-brand:", "--kitwork-brand-rgb:"} {
		if !strings.Contains(css, want) {
			t.Fatalf("%s is never defined, so every var() referencing it silently falls back\n%s", want, css)
		}
	}
	// Both forms are needed: the hex for a plain colour, the triplet for alpha compositing.
	if !strings.Contains(css, "--kitwork-brand: #f82244") {
		t.Errorf("default brand should be the Kitwork red as a hex colour\n%s", firstLines(css, 3))
	}
	if !strings.Contains(css, "--kitwork-brand-rgb: 248, 34, 68") {
		t.Errorf("rgb triplet missing or malformed\n%s", firstLines(css, 3))
	}
}

// The point of a token is that ONE change moves everything. A site setting its own brand must see
// it in the stylesheet — otherwise the token is as inert as the fallbacks it replaced.
func TestBrandTokenFollowsSiteConfig(t *testing.T) {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["brand"] = Hex("#00aaff")

	css := GenerateJITCached(`<div class="flex">x</div>`, &cfg)

	if !strings.Contains(css, "--kitwork-brand: #00aaff") {
		t.Fatalf("a configured brand colour did not reach the token\n%s", firstLines(css, 3))
	}
	if !strings.Contains(css, "--kitwork-brand-rgb: 0, 170, 255") {
		t.Fatalf("configured brand missing from the rgb triplet\n%s", firstLines(css, 3))
	}
	// CONTROL: the default must be gone, not merely accompanied. Without this the test would pass
	// on an implementation that emits both.
	if strings.Contains(css, "--kitwork-brand: #f82244") {
		t.Fatal("the default brand is still emitted alongside the configured one")
	}
}

// NOTE — there is no test for the brand GRADIENT, and no way to write one.
//
// buildProp has a "special-bg" case handling bg grid/haze/gradient, but NOTHING in Registry maps to
// that type, so no class name reaches it: bg-brand-gradient, bg-gradient-brand and the rest all
// resolve to "". The code is unreachable. A test would pass on any implementation — including one
// with the branch deleted — because the CSS it checks is never generated either way.
//
// The branch was left as-is (its stops now derive from the configured brand like everything else,
// so it is correct if it is ever wired up), but whether to wire it or delete it is a product call.

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}
