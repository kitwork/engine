package css

import (
	"strings"
	"testing"
)

// A utility written inside a template branch must reach the stylesheet. Before 23/09 the scanner
// split the attribute on whitespace only, so "}}right-0{{" matched nothing and the rule was never
// emitted — the page rendered the class and the styling silently did not exist.
func TestUtilitiesInsideATemplateBranchAreEmitted(t *testing.T) {
	html := `<div class="fixed left-0 {{ if $.request.platform == 'macos' }}right-0{{ else }}[right:var(--kitwork-caption-width,144px)]{{ end }} z-50"></div>`
	out := GenerateJIT(html, nil)

	for _, want := range []string{
		"position: fixed;",
		"left: 0",
		"right: 0",
		"right: var(--kitwork-caption-width,144px);",
		"z-index: 50;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q — a class beside a template delimiter must still be emitted:\n%s", want, out)
		}
	}

	// CONTROL: the same markup with no template must produce the same rules, so the test cannot
	// pass because of something unrelated to the delimiters.
	plain := GenerateJIT(`<div class="fixed left-0 right-0 z-50"></div>`, nil)
	for _, want := range []string{"position: fixed;", "left: 0", "right: 0", "z-index: 50;"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("control lost %q — the failure is not about templates at all", want)
		}
	}
}
