package css

import (
	"strings"
	"testing"
)

// fill-none / stroke-none / *-current / *-inherit are Tailwind's keyword colours. They resolved
// to nothing here, so `<circle class="fill-none stroke-[#f97316]">` had no fill rule and painted
// SVG's default — black — which the dark hero hid and the light hero showed as a grey disc
// pulsing out of the cube.
func TestKeywordColours(t *testing.T) {
	cfg := DefaultConfig
	for _, c := range []struct{ class, want string }{
		{"fill-none", "fill: none;"},
		{"stroke-none", "stroke: none;"},
		{"fill-current", "fill: currentColor;"},
		{"stroke-current", "stroke: currentColor;"},
		{"text-current", "color: currentColor;"},
		{"bg-inherit", "background-color: inherit;"},
		{"border-current", "border-color: currentColor;"},
	} {
		t.Run(c.class, func(t *testing.T) {
			out := GenerateJIT(`<i class="`+c.class+`"></i>`, &cfg)
			if !strings.Contains(out, c.want) {
				t.Fatalf("%s: khong sinh %q\nCSS:\n%s", c.class, c.want, out)
			}
		})
	}
	// `none` is not a colour for the other families: bg-none stays whatever it was (Tailwind
	// makes it background-image: none) and must not become `background-color: none`.
	if out := GenerateJIT(`<i class="bg-none"></i>`, &cfg); strings.Contains(out, "background-color: none") {
		t.Fatalf("bg-none must not emit an invalid background-color: %s", out)
	}
}
