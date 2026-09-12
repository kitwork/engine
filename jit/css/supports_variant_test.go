package css

import (
	"strings"
	"testing"
)

// supports-[…]: is Tailwind's feature-query variant. The site's glass is bg-paper/60 under a
// backdrop blur; where backdrop-filter is unsupported that 60% is a washed-out card on the
// wallpaper, so the pattern is `bg-paper/90 supports-[backdrop-filter]:bg-paper/60`. (paper is a
// site token, not in DefaultConfig, so the test speaks in white.)
func TestSupportsVariant(t *testing.T) {
	cfg := DefaultConfig
	for _, c := range []struct{ class, wrap, rule string }{
		{"supports-[backdrop-filter]:bg-white/60", "@supports (backdrop-filter: var(--kitwork-supports)) {", "background-color: rgba(var(--color-white"},
		{"supports-[display:grid]:grid", "@supports (display: grid) {", "display: grid;"},
		{"supports-[backdrop-filter:blur(1px)]:backdrop-blur-md", "@supports (backdrop-filter: blur(1px)) {", "blur(12px)"},
	} {
		t.Run(c.class, func(t *testing.T) {
			out := GenerateJIT(`<div class="`+c.class+`"></div>`, &cfg)
			i := strings.Index(out, c.wrap)
			if i < 0 {
				t.Fatalf("no %q block\nCSS:\n%s", c.wrap, out)
			}
			block := out[i:]
			if j := strings.Index(block, "}\n}"); j > 0 {
				block = block[:j]
			}
			if !strings.Contains(block, c.rule) {
				t.Fatalf("rule %q not inside the @supports block:\n%s", c.rule, block)
			}
		})
	}
	// The base rule still comes out on its own, before the block, so the cascade is base → feature.
	out := GenerateJIT(`<div class="bg-white/90 supports-[backdrop-filter]:bg-white/60"></div>`, &cfg)
	base, feat := strings.Index(out, `.bg-white\/90 {`), strings.Index(out, "@supports (backdrop-filter")
	if base < 0 || feat < 0 || base > feat {
		t.Fatalf("base rule must precede the @supports block: base=%d feature=%d\n%s", base, feat, out)
	}
}
