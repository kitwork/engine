package css

import (
	"strings"
	"testing"
)

func TestTailwindFormAndDecorationUtilities(t *testing.T) {
	cases := map[string]string{
		"accent-lime-500":     "accent-color: rgb(132, 204, 22);",
		"decoration-zinc-300": "text-decoration-color: rgb(212, 212, 216);",
		"underline-offset-4":  "text-underline-offset: 4px;",
		"outline-none":        "outline: 2px solid transparent; outline-offset: 2px;",
		"sm:divide-y-0":       "border-top-width: 0px;",
	}

	for className, want := range cases {
		css, selector, _ := ResolveCore(className, nil)
		if css != want {
			t.Errorf("%s generated %q, want %q", className, css, want)
		}
		if className == "sm:divide-y-0" &&
			!strings.Contains(selector, ` > :not([hidden]) ~ :not([hidden])`) {
			t.Errorf("%s generated selector %q without the sibling combinator", className, selector)
		}
	}
}
