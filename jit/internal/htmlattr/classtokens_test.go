package htmlattr

import (
	"strings"
	"testing"
)

// A class attribute holding a template branch is ordinary authoring, and every JIT engine that
// reads classes — css, icons, logo — must see the names on BOTH sides of the delimiters. Splitting
// on whitespace alone glues a name to the braces ("}}right-0{{"), which matches nothing: the page
// renders the class and the stylesheet never holds it, with no error anywhere.
func TestClassTokensSeesNamesBesideTemplateDelimiters(t *testing.T) {
	got := strings.Join(ClassTokens(`fixed left-0 {{ if macos }}right-0{{ else }}right-36{{ end }} z-50`), " ")
	for _, want := range []string{"fixed", "left-0", "right-0", "right-36", "z-50"} {
		if !strings.Contains(" "+got+" ", " "+want+" ") {
			t.Errorf("%q is missing from %q", want, got)
		}
	}
	for _, glued := range []string{"}}right-0{{", "}}right-36{{", "{{"} {
		if strings.Contains(got, glued) {
			t.Errorf("%q survived as a token in %q", glued, got)
		}
	}

	// Plain attributes are untouched, and the template's own keywords stay as ordinary tokens —
	// they match no utility, which is what they did before.
	if strings.Join(ClassTokens("p-4  text-ink"), "|") != "p-4|text-ink" {
		t.Errorf("a plain attribute must split exactly as before: %q", ClassTokens("p-4  text-ink"))
	}
	if strings.Join(ClassTokens("{{ if x }}a{{ end }}"), "|") != "if|x|a|end" {
		t.Errorf("template keywords should remain harmless tokens: %q", ClassTokens("{{ if x }}a{{ end }}"))
	}
}
