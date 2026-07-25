package css

import (
	"strings"
	"testing"
)

// A class chosen at runtime still has to exist in the stylesheet. The JIT emits only what it can
// see, so if it reads a data-kit-class expression as anything other than an expression, the page
// renders unstyled the moment the condition flips — the failure appears later, on one branch only,
// far from the cause.
func TestJITEmitsBothBranchesOfADynamicClass(t *testing.T) {
	html := `<div data-kit-class="open ? 'text-red-500' : 'text-green-500'">x</div>`
	css := GenerateJITCached(html, nil)

	for _, want := range []string{".text-red-500", ".text-green-500"} {
		if !strings.Contains(css, want) {
			t.Fatalf("dynamic class %s was not emitted\n--- css ---\n%s", want, css)
		}
	}
}

// The author picks the shape; the JIT has to cope with all of them. These are the forms the
// grammar allows, so each is something someone will write.
func TestJITEmitsDynamicClassesFromEveryForm(t *testing.T) {
	cases := []struct {
		html string
		want []string
	}{
		{`<div data-kit-class="{ 'p-4': big }">x</div>`, []string{".p-4"}},
		{`<div data-kit-class="['m-2', on ? 'p-8' : '']">x</div>`, []string{".m-2", ".p-8"}},
		{`<div data-kit-class="'flex gap-4'">x</div>`, []string{".flex", ".gap-4"}},
	}
	for _, tc := range cases {
		css := GenerateJITCached(tc.html, nil)
		for _, w := range tc.want {
			if !strings.Contains(css, w) {
				t.Errorf("%s\n  missing %s", tc.html, w)
			}
		}
	}
}

// A static class attribute sitting next to a dynamic one must survive. The two are read by
// different scanners and the dynamic attribute ENDS in `class="`, so a pattern that is not anchored
// matches both and shreds the expression into tokens.
func TestStaticClassStillWorksBesideDynamic(t *testing.T) {
	html := `<div data-kit-class="{ 'p-4': big }" class="m-2 flex">x</div>`
	css := GenerateJITCached(html, nil)

	for _, want := range []string{".m-2", ".flex", ".p-4"} {
		if !strings.Contains(css, want) {
			t.Fatalf("missing %s\n--- css ---\n%s", want, css)
		}
	}
}

// The expression's SYNTAX must never be mistaken for class names.
//
// This asserts on the collected class SET, not on the generated CSS, and the difference matters.
// Junk tokens never reach the stylesheet either way — an unresolvable name produces no rule, so
// buildJITCSS drops it silently — which means a CSS-level assertion here would pass even with the
// scanner's anchoring removed, and prove nothing. (Measured: it did.) The set is where the damage
// is visible: it keys the JIT cache, so garbage tokens split one entry into many, and a token that
// ever DOES resolve becomes a rule nobody asked for.
func TestExpressionSyntaxIsNotTreatedAsClassNames(t *testing.T) {
	seen := map[string]bool{}
	var classes []string
	collectClasses(`<div data-kit-class="{ 'p-4': big }" class="m-2">x</div>`, seen, &classes)

	want := map[string]bool{"m-2": true, "p-4": true}
	for _, c := range classes {
		if !want[c] {
			t.Fatalf("expression token %q was collected as a class name; got %v", c, classes)
		}
	}
	if len(classes) != len(want) {
		t.Fatalf("expected exactly %v, got %v", want, classes)
	}
}

// The site-wide stylesheet and the per-page one must agree: same markup, same classes. They are
// separate entry points, which is exactly how they drift.
func TestSiteCSSSeesDynamicClassesToo(t *testing.T) {
	html := `<div data-kit-class="open ? 'text-red-500' : 'text-green-500'">x</div>`
	site := GenerateSiteCSS(nil, html)

	for _, want := range []string{".text-red-500", ".text-green-500"} {
		if !strings.Contains(site, want) {
			t.Fatalf("site-wide CSS missing %s", want)
		}
	}
}
