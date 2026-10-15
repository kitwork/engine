package render

import (
	"strings"
	"testing"
)

// The markup highlighter colours a directive as a directive in either form: data-kit-* and the eight
// grouped data-kitwork-* attributes. A transport marker is an ordinary attribute.
func TestHighlightColoursGroupedDirectives(t *testing.T) {
	directive := `<span class="` + hlDefault.directive + `">`
	attribute := `<span class="` + hlDefault.attr + `">`
	out := highlightWith(`<b data-kitwork-bind="text: x" data-kitwork-event="click: f()" data-kit-text="y" data-kitwork-generated="css" title="t"></b>`, hlDefault)
	for _, name := range []string{"data-kitwork-bind", "data-kitwork-event", "data-kit-text"} {
		if !strings.Contains(out, directive+name+"</span>") {
			t.Errorf("%s is a directive and must be coloured as one:\n%s", name, out)
		}
	}
	// control: transport and a plain attribute keep the attribute colour
	for _, name := range []string{"data-kitwork-generated", "title"} {
		if !strings.Contains(out, attribute+name+"</span>") {
			t.Errorf("%s is not a directive:\n%s", name, out)
		}
	}
}

// standalone() drops the version from a grouped host too, and keeps its alias: the alias is the
// markup's, the version is staging.
func TestStripComponentVersionGrouped(t *testing.T) {
	for in, want := range map[string]string{
		`<a data-kitwork-component="tabs@2.1.0: $tabs" class="x">`:                       `<a data-kitwork-component="tabs: $tabs" class="x">`,
		`<a data-kitwork-component="tabs@2.1.0" class="x">`:                              `<a data-kitwork-component="tabs" class="x">`,
		`<a data-kit-component="tabs@2.1.0"><b data-kitwork-component="menu@1.0.0: $m">`: `<a data-kit-component="tabs"><b data-kitwork-component="menu: $m">`,
	} {
		if got := stripComponentVersion(in); got != want {
			t.Errorf("%s\n  got  %s\n  want %s", in, got, want)
		}
	}
	// control: a grouped host with no version, alias or not, passes through untouched
	for _, unchanged := range []string{`<a data-kitwork-component="tabs: $tabs">`, `<a data-kitwork-component="tabs">`} {
		if got := stripComponentVersion(unchanged); got != unchanged {
			t.Errorf("must pass through untouched: %s, got %s", unchanged, got)
		}
	}
}
