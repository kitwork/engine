package css

import (
	"strings"
	"testing"
)

// cacheKeyConfig is a configuration of its own (no map shared with DefaultConfig) with the given body
// typeface.
func cacheKeyConfig(body string) *Config {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{"brand": Hex("#c8742c"), "canvas": Hex("#f6f3ee"), "ink": Hex("#1a1714")}
	cfg.Order = []string{"brand", "canvas", "ink"}
	cfg.FontFamily = map[string]string{}
	for role, family := range DefaultConfig.FontFamily {
		cfg.FontFamily[role] = family
	}
	cfg.FontFamily["body"] = body + ", ui-sans-serif, sans-serif"
	cfg.Themes = nil
	return &cfg
}

// The stylesheet of one class set is cached, so everything that changes the CSS of the same classes
// must be part of the key: the typefaces (a branch's router.fonts()), the derived appearance blocks (a
// route shown dark first) and the selector they sit under. Otherwise the second configuration is served
// the first one's stylesheet. Each case writes its own class set, so no other test's entry can answer.
func TestGenerateJITCachedKeysOnFontsAndAppearance(t *testing.T) {
	page := `<p class="font-body bg-canvas text-brand cache-key-fonts">x</p>`
	outfit := GenerateJITCached(page, cacheKeyConfig("Outfit"))
	if !strings.Contains(outfit, "font-family: Outfit") {
		t.Fatalf("control: the first configuration's typeface is in its stylesheet:\n%s", outfit)
	}
	if again := GenerateJITCached(page, cacheKeyConfig("Outfit")); again != outfit {
		t.Fatal("control: the same configuration must get the same stylesheet")
	}
	if inter := GenerateJITCached(page, cacheKeyConfig("Inter")); !strings.Contains(inter, "font-family: Inter") || strings.Contains(inter, "Outfit") {
		t.Errorf("another body typeface must get its own stylesheet, got the cached one:\n%s", inter)
	}

	page = `<p class="bg-canvas text-brand cache-key-themes">x</p>`
	light := cacheKeyConfig("Outfit")
	if plain := GenerateJITCached(page, light); strings.Contains(plain, ".dark {") {
		t.Fatalf("control: a configuration with no dark has no dark block:\n%s", plain)
	}
	dark := cacheKeyConfig("Outfit")
	DeriveThemes(dark, "dark")
	derived := GenerateJITCached(page, dark)
	if !strings.Contains(derived, ".dark {") {
		t.Errorf("a derived dark must get its dark block, got the cached light stylesheet:\n%s", derived)
	}

	selector := cacheKeyConfig("Outfit")
	DeriveThemes(selector, "dark")
	selector.DarkSelector = `[data-theme="dark"]`
	if scoped := GenerateJITCached(page, selector); !strings.Contains(scoped, `[data-theme="dark"] {`) {
		t.Errorf("another dark selector must get its own stylesheet, got the cached one:\n%s", scoped)
	}
}
