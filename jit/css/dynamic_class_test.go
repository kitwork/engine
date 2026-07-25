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

// CONTROL for the anchoring: the expression's syntax must never be treated as class names. Without
// this the suite would still pass on a scanner that splits data-kit-class on whitespace — it would
// happen to pick up the literals too, along with junk, and nobody would notice until a rule name
// collided.
func TestExpressionSyntaxIsNotTreatedAsClassNames(t *testing.T) {
	html := `<div data-kit-class="{ 'p-4': big }">x</div>`
	css := GenerateJITCached(html, nil)

	for _, junk := range []string{`.{`, `.}`, `.big`, `.'p-4':`, `.big }`} {
		if strings.Contains(css, junk) {
			t.Fatalf("expression token %q leaked into the stylesheet\n--- css ---\n%s", junk, css)
		}
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
