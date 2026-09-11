package css

import (
	"strings"
	"testing"
)

// parse() strips a leading "-" and reports it in `neg`, so the matched text can never start with
// one. Six handlers tested `m[0][0] == '-'` instead and therefore emitted a POSITIVE value:
// `-left-6` rendered identically to `left-6`, putting the element on the opposite side of its
// anchor with no error anywhere. The positive cases are kept alongside so a fix that simply
// negates everything fails too.
func TestNegativeValuesActuallyGoNegative(t *testing.T) {
	cfg := DefaultConfig
	for _, c := range []struct{ cls, want string }{
		{"-left-6", "left: -1.5rem;"},
		{"-top-4", "top: -1rem;"},
		{"-right-2", "right: -0.5rem;"},
		{"-bottom-8", "bottom: -2rem;"},
		{"-inset-x-2", "left: -0.5rem; right: -0.5rem;"},
		{"-inset-4", "inset: -1rem;"},
		{"-z-10", "z-index: -10;"},
		{"-indent-4", "text-indent: -1rem;"},
		{"-outline-offset-2", "outline-offset: -2px;"},
		{"-hue-rotate-15", "hue-rotate(-15deg)"},
		{"-backdrop-hue-rotate-15", "hue-rotate(-15deg)"},
		// …and the same utilities without the prefix must stay positive.
		{"left-6", "left: 1.5rem;"},
		{"top-4", "top: 1rem;"},
		{"inset-4", "inset: 1rem;"},
		{"z-10", "z-index: 10;"},
		{"indent-4", "text-indent: 1rem;"},
		{"outline-offset-2", "outline-offset: 2px;"},
		{"hue-rotate-15", "hue-rotate(15deg)"},
	} {
		css, _, _ := ResolveCore(c.cls, &cfg)
		if !strings.Contains(css, c.want) {
			t.Errorf("%s → %q, want it to contain %q", c.cls, css, c.want)
		}
	}
	// The clearest statement of the bug: these two must not be the same rule.
	negative, _, _ := ResolveCore("-left-6", &cfg)
	positive, _, _ := ResolveCore("left-6", &cfg)
	if negative == positive {
		t.Fatalf("-left-6 and left-6 both emit %q", negative)
	}
}
