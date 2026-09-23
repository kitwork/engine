package hydrate

import (
	"strings"
	"testing"
)

// ReadNames answers one question — which names does this expression look up in a scope? — and the
// wrong answers are what make it worth a test: a member name, an object key or a lambda parameter
// looks like a name but is never looked up, and reporting one would make the caller unusable.
func TestReadNames(t *testing.T) {
	cases := []struct {
		expression string
		want       string
	}{
		{"darkMode", "darkMode"},
		{"a + b * c", "a,b,c"},
		{"open ? label : fallback", "open,label,fallback"},
		{"user.email", "user"},                                    // email is a member, not a lookup
		{"user.profile.city.name", "user"},                        // …however deep it goes
		{"user[key]", "user,key"},                                 // but a computed member IS a lookup
		{"({ label: title }).label", "title"},                     // keys are written down, values are read
		{"rows.filter((row) => row.done).length", "rows"},         // the parameter shadows the boundary
		{"rows.filter((row) => row.done && ready)", "rows,ready"}, // …only the parameter, not the rest
		{"count + count", "count"},                                // reported once
		{"'literal' + 2", ""},
		{"select('guide')", "select"},
	}
	for _, c := range cases {
		t.Run(c.expression, func(t *testing.T) {
			names, err := ReadNames(c.expression)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if got := strings.Join(names, ","); got != c.want {
				t.Fatalf("want %q, got %q", c.want, got)
			}
		})
	}
}

// An expression the grammar rejects has no names to report: the verify pass already tells the
// author about it, and guessing here would produce a second, worse message.
func TestReadNamesRejectsMalformed(t *testing.T) {
	if _, err := ReadNames("rows.filter(row => row.done)"); err == nil {
		t.Fatal("a bare lambda parameter is not this grammar — want a compile error")
	}
	if names, err := ReadNames("rows.filter((row) => row.done)"); err != nil || strings.Join(names, ",") != "rows" {
		t.Fatalf("control: the parenthesised form should read rows, got %v %v", names, err)
	}
}
