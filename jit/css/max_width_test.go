package css

import (
	"strings"
	"testing"
)

// max-w-prose and max-w-screen-<breakpoint> are Tailwind v3 words that only max-w has. They wrote
// `max-width: prose;` and `max-width: screen-lg;` — CSS a browser drops, so the element was not capped.
func TestMaxWidthReadsProseAndTheScreens(t *testing.T) {
	cases := map[string]string{
		"max-w-prose":      "max-width: 65ch;",
		"max-w-screen-sm":  "max-width: 640px;",
		"max-w-screen-md":  "max-width: 768px;",
		"max-w-screen-lg":  "max-width: 1024px;",
		"max-w-screen-xl":  "max-width: 1280px;",
		"max-w-screen-2xl": "max-width: 1536px;",
	}
	for className, want := range cases {
		if css, _, _ := ResolveCore(className, nil); css != want {
			t.Errorf("%s generated %q, want %q", className, css, want)
		}
	}
	// control: the words keep their meaning elsewhere — max-w-lg is the 32rem of the size scale, not a
	// screen; max-w-screen alone is the viewport; and w-prose / w-screen-lg are no Tailwind words, so
	// they do not borrow max-w's.
	for className, want := range map[string]string{
		"max-w-lg":     "max-width: 32rem;",
		"max-w-screen": "max-width: 100vw;",
	} {
		if css, _, _ := ResolveCore(className, nil); css != want {
			t.Errorf("control: %s generated %q, want %q", className, css, want)
		}
	}
	for className, borrowed := range map[string]string{"w-prose": "65ch", "w-screen-lg": "1024px"} {
		if css, _, _ := ResolveCore(className, nil); strings.Contains(css, borrowed) {
			t.Errorf("control: %s borrowed max-w's word: %q", className, css)
		}
	}
}
