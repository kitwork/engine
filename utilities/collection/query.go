package collection

import (
	"fmt"
	"sort"
	"strings"
)

// Query is the in-memory filter/sort/slice spec applied to a collection's frontmatter index. It runs
// over the ALREADY-CACHED []IndexEntry (signature-invalidated), so a query costs microseconds and
// touches no SQL — full-text search is the only collection operation that leaves RAM.
type Query struct {
	Filters    []Filter
	OrderField string
	OrderDesc  bool
	SkipN      int
	LimitN     int // 0 = no limit
}

// Filter is one where() clause. Op: "=", "!=", ">", ">=", "<", "<=", "contains".
// "contains" matches list frontmatter (tags: [a, b]) by element, or substring on strings.
type Filter struct {
	Field string
	Op    string
	Value any
}

// Apply filters, sorts and slices entries. The input slice is never mutated.
func (q Query) Apply(entries []IndexEntry) []IndexEntry {
	out := make([]IndexEntry, 0, len(entries))
	for _, e := range entries {
		keep := true
		for _, f := range q.Filters {
			if !f.match(e) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, e)
		}
	}

	if q.OrderField != "" {
		field, desc := q.OrderField, q.OrderDesc
		sort.SliceStable(out, func(i, j int) bool {
			c := compareValues(fieldOf(out[i], field), fieldOf(out[j], field))
			if desc {
				return c > 0
			}
			return c < 0
		})
	}

	if q.SkipN > 0 {
		if q.SkipN >= len(out) {
			return []IndexEntry{}
		}
		out = out[q.SkipN:]
	}
	if q.LimitN > 0 && len(out) > q.LimitN {
		out = out[:q.LimitN]
	}
	return out
}

func (f Filter) match(e IndexEntry) bool {
	got := fieldOf(e, f.Field)
	switch f.Op {
	case "", "=", "==":
		return compareValues(got, f.Value) == 0
	case "!=":
		return compareValues(got, f.Value) != 0
	case ">":
		return got != nil && compareValues(got, f.Value) > 0
	case ">=":
		return got != nil && compareValues(got, f.Value) >= 0
	case "<":
		return got != nil && compareValues(got, f.Value) < 0
	case "<=":
		return got != nil && compareValues(got, f.Value) <= 0
	case "contains":
		return containsValue(got, f.Value)
	default:
		return false
	}
}

// fieldOf resolves a query field: frontmatter first, then the file's own facts (slug/name/size/
// modified) so `orderBy("modified", "desc")` works with no frontmatter at all.
func fieldOf(e IndexEntry, field string) any {
	if v, ok := e.Meta[field]; ok {
		return v
	}
	switch field {
	case "slug":
		return e.File.Slug
	case "name":
		return e.File.Name
	case "size":
		return e.File.Size
	case "modified":
		return e.File.Modified
	}
	return nil
}

// compareValues orders two frontmatter values: numerically when both sides are numbers, boolean
// false<true, otherwise as strings (ISO dates order correctly as strings). nil sorts first.
func compareValues(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	na, aNum := toFloat(a)
	nb, bNum := toFloat(b)
	if aNum && bNum {
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		default:
			return 0
		}
	}
	if ba, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok {
			switch {
			case ba == bb:
				return 0
			case bb:
				return -1
			default:
				return 1
			}
		}
	}
	return strings.Compare(toString(a), toString(b))
}

func containsValue(haystack, needle any) bool {
	switch h := haystack.(type) {
	case []any:
		for _, item := range h {
			if compareValues(item, needle) == 0 {
				return true
			}
		}
		return false
	case string:
		return strings.Contains(strings.ToLower(h), strings.ToLower(toString(needle)))
	default:
		return false
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
