package collection

import (
	"math"
	"testing"

	collectionhelper "github.com/kitwork/engine/utilities/collection"
	"github.com/kitwork/engine/value"
)

// .limit(n) and .skip(n) read n as a count (value.Value.Int, 15/10/2026, the review of L2): NaN is 0,
// which limits and skips nothing, and Infinity is the largest int, past every entry. Go's int() made both
// -9223372036854775808, so .skip(1 / 0) skipped nothing. CONTROL: .skip(1).limit(1) of three entries.
func TestLimitAndSkipOfNaNAndInfinity(t *testing.T) {
	index := []collectionhelper.IndexEntry{{Meta: map[string]any{"n": 1}}, {Meta: map[string]any{"n": 2}}, {Meta: map[string]any{"n": 3}}}
	for _, row := range []struct {
		name        string
		limit, skip float64
		want        int
	}{
		{"skip Infinity", 0, math.Inf(1), 0},
		{"limit NaN, skip NaN", math.NaN(), math.NaN(), 3},
		{"limit Infinity", math.Inf(1), 0, 3},
		{"CONTROL: skip 1, limit 1", 1, 1, 1},
	} {
		query := &CollectionQuery{}
		query.Limit(value.New(row.limit)).Skip(value.New(row.skip))
		if got := len(query.spec.Apply(index)); got != row.want {
			t.Errorf("%s: %d entries (limit %d, skip %d), want %d", row.name, got, query.spec.LimitN, query.spec.SkipN, row.want)
		}
	}
}
