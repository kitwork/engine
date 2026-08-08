package css

import "testing"

func TestScreenSizingUsesTheCorrectViewportAxis(t *testing.T) {
	cases := map[string]string{
		"w-screen":     "width: 100vw;",
		"min-w-screen": "min-width: 100vw;",
		"max-w-screen": "max-width: 100vw;",
		"h-screen":     "height: 100vh;",
		"min-h-screen": "min-height: 100vh;",
		"max-h-screen": "max-height: 100vh;",
	}

	for className, want := range cases {
		css, _, _ := ResolveCore(className, nil)
		if css != want {
			t.Errorf("%s generated %q, want %q", className, css, want)
		}
	}
}
