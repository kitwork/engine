package css

import (
	"strings"
	"testing"
)

// Tailwind owns five animation names — none, spin, ping, pulse, bounce — and people who know
// them expect the exact motion. The rule of the house (Tailwind-shaped, not bound) is: add freely
// in the empty slots, never change what an existing name means. These four therefore carry
// Tailwind's own shorthand and keyframes, byte for byte in the parts that shape the motion.
func TestTailwindAnimationParity(t *testing.T) {
	cfg := DefaultConfig
	for _, c := range []struct {
		class     string
		shorthand string   // Tailwind's `animation` value, with our keyframe name
		frames    []string // fragments that must appear in the emitted @keyframes
	}{
		{"animate-spin", "animate--spin 1s linear infinite",
			[]string{"to{transform:rotate(360deg)}"}},
		{"animate-ping", "animate--ping 1s cubic-bezier(0,0,0.2,1) infinite",
			[]string{"75%,100%{transform:scale(2);opacity:0}"}},
		{"animate-pulse", "animate--pulse 2s cubic-bezier(0.4,0,0.6,1) infinite",
			[]string{"50%{opacity:.5}"}},
		{"animate-bounce", "animate--bounce 1s infinite",
			[]string{
				"0%,100%{transform:translateY(-25%);animation-timing-function:cubic-bezier(0.8,0,1,1)}",
				"50%{transform:none;animation-timing-function:cubic-bezier(0,0,0.2,1)}",
			}},
	} {
		t.Run(c.class, func(t *testing.T) {
			out := GenerateJIT(`<i class="`+c.class+`"></i>`, &cfg)
			rule := strings.ReplaceAll(out, "animation: ", "animation:")
			if !strings.Contains(rule, "animation:"+c.shorthand+";") {
				t.Fatalf("shorthand differs from Tailwind\nwant: animation:%s;\nCSS:\n%s", c.shorthand, out)
			}
			// the @keyframes ride separately, emitted only for what the utility CSS references
			frames := UsedKeyframes(out, &cfg)
			for _, f := range c.frames {
				if !strings.Contains(frames, f) {
					t.Fatalf("keyframes differ from Tailwind — missing %q\nkeyframes:\n%s", f, frames)
				}
			}
		})
	}
}
