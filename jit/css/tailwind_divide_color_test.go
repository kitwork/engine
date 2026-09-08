package css

import (
	"strings"
	"testing"
)

// divide-<color> used to accept ONLY a numbered palette shade ("divide-gray-200"): a bare design
// token ("divide-line") matched no rule and an /alpha modifier was impossible, so "divide-line/40"
// silently emitted nothing. These pin that divide-* now shares the bg/text/border colour path — a
// themeable token resolves through var(), a palette shade stays baked, alpha is honoured — while
// still targeting the gaps BETWEEN children.
func TestDivideColorTokenAndAlpha(t *testing.T) {
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	cfg.Colors["line"] = Hex("#e6ebf1") // triplet 230, 235, 241

	cases := map[string]string{
		// THE regression: bare token + alpha, themeable via var() with the triplet as fallback.
		"divide-line/40": "border-color: rgba(var(--color-line, 230, 235, 241), 0.40);",
		// bare token, no alpha
		"divide-line": "border-color: rgb(var(--color-line, 230, 235, 241));",
		// palette family + shade stays baked (no var) — no regression
		"divide-zinc-300": "border-color: rgb(212, 212, 216);",
		// shade + alpha
		"divide-zinc-300/50": "border-color: rgba(212, 212, 216, 0.50);",
	}
	for className, want := range cases {
		css, selector, _ := ResolveCore(className, &cfg)
		if css != want {
			t.Errorf("%s generated %q, want %q", className, css, want)
		}
		if !strings.Contains(selector, ` > :not([hidden]) ~ :not([hidden])`) {
			t.Errorf("%s selector %q lost the between-children combinator", className, selector)
		}
	}

	// CONTROL 1: on the old rule this emitted nothing. Prove it resolves through the THEMEABLE var —
	// a plain rgb with no var() would mean the token stopped following [data-theme].
	if css, _, _ := ResolveCore("divide-line/40", &cfg); !strings.Contains(css, "var(--color-line") {
		t.Fatalf("divide-line/40 must resolve through the themeable var, got %q", css)
	}

	// CONTROL 2: the broadened base rule must NOT swallow the structural divide-x/divide-y width
	// utilities (they are matched by earlier rules and must keep winning).
	if css, _, _ := ResolveCore("divide-y", &cfg); css != "border-top-width: 1px;" {
		t.Fatalf("divide-y should stay a width utility, got %q", css)
	}
}
