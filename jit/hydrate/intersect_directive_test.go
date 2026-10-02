package hydrate

import (
	"strings"
	"testing"
)

// data-kit-intersect is read by the server like the event family: an expression directive whose
// modifiers are checked — the observer's own (:leave :once :half :full :margin(x)), and nothing an
// element entering the screen cannot mean. A page whose only grammar is the directive gets the
// runtime.
func TestIntersectDirectiveIsVerified(t *testing.T) {
	m := directiveRe.FindStringSubmatch(`data-kit-intersect:once:margin(-20%)="count++"`)
	if m == nil || m[1] != "intersect:once:margin(-20%)" || m[2] != "count++" {
		t.Fatalf("data-kit-intersect with a margin was not read as a directive: %q", m)
	}
	for _, ok := range []string{"intersect", "intersect:once", "intersect:leave", "intersect:half", "intersect:full:once", "intersect:margin(0px 0px -20% 0px)"} {
		if err := checkEventModifiers(ok); err != nil {
			t.Errorf("checkEventModifiers(%q) = %v, want nil", ok, err)
		}
	}
	for directive, reason := range map[string]string{
		"intersect:prevent":      "unknown modifier :prevent",
		"intersect:outside":      "unknown modifier :outside",
		"intersect:half:full":    "pick one",
		"intersect:once:once":    "repeated",
		"intersect:margin()":     "needs a CSS margin",
		"intersect:debounce(10)": "unknown modifier",
	} {
		if err := checkEventModifiers(directive); err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("checkEventModifiers(%q) = %v, want an error naming %q", directive, err, reason)
		}
	}
	page := `<head></head><body>` + marker + `<section data-kit-intersect:once="count++"></section></section></body></html>`
	if strings.Count(Render(page), injectTag) != 1 {
		t.Error("a page with only data-kit-intersect needs the runtime injected")
	}
	if found := strings.Join(authoringMistakes(`<section data-kit-intersect:sideways="count++"></section>`), "\n"); !strings.Contains(found, "unknown modifier :sideways") {
		t.Errorf("a bad modifier must be reported at render, got %q", found)
	}
	// CONTROL: a well-formed one reports nothing about its modifiers.
	if found := strings.Join(authoringMistakes(`<section data-kit-scope="count: 0" data-kit-intersect:once:half="count++"></section>`), "\n"); strings.Contains(found, "modifier") {
		t.Errorf("a well-formed data-kit-intersect was reported: %q", found)
	}
}
