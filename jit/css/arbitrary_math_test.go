package css

import (
	"strings"
	"testing"
)

// A class cannot hold spaces, so `calc(100%+8px)` is what an arbitrary value says; CSS wants
// `calc(100% + 8px)`. And the negative prefix on an expression is `calc(… * -1)` — `-calc(…)`
// is not CSS, and with it in the chain the whole transform was dropped: the editor's bubble
// sat below its selection instead of above it.
func TestArbitraryMathIsSpacedAndNegatedAsCSS(t *testing.T) {
	cfg := DefaultConfig
	out := GenerateJIT(`<div class="-translate-y-[calc(100%+8px)] w-[calc(100%-2rem)] -mt-[var(--gap)] h-[min(100vh-4rem,40rem)] -translate-x-1/2 -mt-4 mt-[calc(100%_-_2.5rem)] pl-[env(safe-area-inset-left)] top-[calc(var(--bar-height)*-1)]"></div>`, &cfg)
	for _, want := range []string{
		`--kitwork-translate-y: calc(calc(100% + 8px) * -1);`,
		`width: calc(100% - 2rem);`,
		`margin-top: calc(var(--gap) * -1);`,
		`height: min(100vh - 4rem,40rem);`,
		// The plain forms keep their plain sign.
		`--kitwork-translate-x: -50%;`,
		`mt-4 { margin-top: -1rem; }`,
		// Already-spaced input is not doubled; hyphenated names and signs stay.
		`margin-top: calc(100% - 2.5rem);`,
		`padding-left: env(safe-area-inset-left);`,
		`top: calc(var(--bar-height) * -1);`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\nin:\n%s", want, out)
		}
	}
	if strings.Contains(out, "-calc(") || strings.Contains(out, "safe - area") || strings.Contains(out, "*  -") {
		t.Errorf("bad math survived:\n%s", out)
	}
}

func TestNormalizeMathUnit(t *testing.T) {
	cases := map[string]string{
		"calc(100%+8px)":                                   "calc(100% + 8px)",
		"calc(100vh-env(safe-area-inset-top))":             "calc(100vh - env(safe-area-inset-top))",
		"calc(var(--a-b)*-1)":                              "calc(var(--a-b) * -1)",
		"clamp(1rem,2vw+1rem,3rem)":                        "clamp(1rem,2vw + 1rem,3rem)",
		"linear-gradient(to bottom,black 80%,transparent)": "linear-gradient(to bottom,black 80%,transparent)",
		"calc(100% - 2rem)":                                "calc(100% - 2rem)",
		"10px":                                             "10px",
	}
	for in, want := range cases {
		if got := normalizeMath(in); got != want {
			t.Errorf("normalizeMath(%q) = %q, want %q", in, got, want)
		}
	}
}
