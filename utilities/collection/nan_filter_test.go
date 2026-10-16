package collection

import (
	"math"
	"slices"
	"testing"
)

// NaN equals nothing and is ordered with nothing in a filter, as in JavaScript (16/10/2026, the owner's
// E1): Number("abc") is NaN since E2, and compareValues gives 0 for NaN beside any number, so
// views = NaN matched every number, >= NaN and <= NaN as well, and a list "contained" NaN when it held a
// number. CONTROL: a number filters as it did.
func TestFilterWithNaNMatchesNoNumber(t *testing.T) {
	index := []IndexEntry{
		entry("a", map[string]any{"views": 10, "tags": []any{1, 2}}),
		entry("b", map[string]any{"views": 20.5}),
		entry("c", map[string]any{"views": "many"}),
		entry("d", map[string]any{}),
	}
	nan := math.NaN()
	for _, row := range []struct {
		op    string
		field string
		value any
		want  []string
	}{
		{"=", "views", nan, []string{}},
		{"==", "views", nan, []string{}},
		{"", "views", nan, []string{}},
		{">=", "views", nan, []string{}},
		{"<=", "views", nan, []string{}},
		{">", "views", nan, []string{}},
		{"<", "views", nan, []string{}},
		{"!=", "views", nan, []string{"a", "b", "c", "d"}},
		{"contains", "tags", nan, []string{}},
		// CONTROL
		{"=", "views", 10, []string{"a"}},
		{"<", "views", 15, []string{"a"}},
		{"!=", "views", 10, []string{"b", "c", "d"}},
		{"contains", "tags", 2, []string{"a"}},
	} {
		got := slugs(Query{Filters: []Filter{{Field: row.field, Op: row.op, Value: row.value}}}.Apply(index))
		if !slices.Equal(got, row.want) {
			t.Errorf("%s %s %v = %v, want %v", row.field, row.op, row.value, got, row.want)
		}
	}
}
