package hydrate

import (
	"strings"
	"testing"
)

// data-kit-event:<name> is the long form of the event family (02/10): the server reads it as an
// expression directive, injects the runtime for it, and checks its modifiers the way it checks
// data-kit-click's — a key filter still belongs on a key event, whatever the spelling.
func TestEventDirectiveIsReadLikeTheShortForm(t *testing.T) {
	m := directiveRe.FindStringSubmatch(`data-kit-event:seen:once="started++"`)
	if m == nil || m[1] != "event:seen:once" || m[2] != "started++" {
		t.Fatalf("data-kit-event:seen:once was not read as a directive: %q", m)
	}
	if directiveRe.MatchString(`data-kit-event="x"`) || directiveRe.MatchString(`data-kit-event:="x"`) {
		t.Error("data-kit-event with no name is not a directive")
	}

	for _, ok := range []string{"event:seen", "event:seen:prevent:once", "event:keydown:mod+k", "event:paste:prevent", "event:scroll:window:throttle(100)"} {
		if err := checkEventModifiers(ok); err != nil {
			t.Errorf("checkEventModifiers(%q) = %v, want nil", ok, err)
		}
	}
	for directive, reason := range map[string]string{
		"event:seen:enter":     "belong on keydown/keyup",
		"event:seen:mod+k":     "belongs on keydown/keyup",
		"event:seen:bogus":     "unknown modifier :bogus",
		"event:seen:outside":   ":outside applies to",
		"event:seen:once:once": "repeated",
	} {
		err := checkEventModifiers(directive)
		if err == nil || !strings.Contains(err.Error(), reason) {
			t.Errorf("checkEventModifiers(%q) = %v, want an error naming %q", directive, err, reason)
		}
	}

	// A page whose only grammar is data-kit-event still gets the runtime, and a broken expression in
	// it is named like any other.
	page := `<head></head><body>` + marker + `<section data-kit-event:seen="count++"></section></section></body>`
	if strings.Count(Render(page), injectTag) != 1 {
		t.Error("a page with only data-kit-event needs the runtime injected")
	}
	found := strings.Join(authoringMistakes(`<section data-kit-event:seen="count +"></section>`), "\n")
	if !strings.Contains(found, `data-kit-event:seen="count +"`) {
		t.Errorf("a broken data-kit-event expression must be reported, got %q", found)
	}
	// CONTROL: the same expression, well formed, reports nothing about it.
	if found := strings.Join(authoringMistakes(`<section data-kit-scope="count: 0" data-kit-event:seen="count++"></section>`), "\n"); strings.Contains(found, "data-kit-event") {
		t.Errorf("a well-formed data-kit-event was reported: %q", found)
	}
}
