package css

import (
	"strings"
	"testing"
)

// Widening `shadow-<key>` to reach theme.boxShadow nearly cost the built-in sizes: a pattern
// starting [a-z] does not match `2xl`, and shadow-2xl (51 uses on kitwork.vn alone) went dark.
func TestShadowSizesStillResolve(t *testing.T) {
	cfg := DefaultConfig
	for _, cls := range []string{"shadow", "shadow-sm", "shadow-md", "shadow-lg", "shadow-xl", "shadow-2xl", "shadow-inner", "shadow-none"} {
		css, _, _ := ResolveCore(cls, &cfg)
		if !strings.Contains(css, "box-shadow:") {
			t.Errorf("%s → %q, want a box-shadow", cls, css)
		}
	}
}
