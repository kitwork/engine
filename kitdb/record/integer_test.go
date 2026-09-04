package record

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

func TestIntegerKeyCodecExactOrder(t *testing.T) {
	values := []int64{math.MinInt64, math.MinInt64 + 1, -1<<53 - 1, -1 << 53, -1, 0, 1, 1 << 53, 1<<53 + 1, math.MaxInt64 - 1, math.MaxInt64}
	rng := rand.New(rand.NewPCG(71, 19))
	for range 4096 {
		values = append(values, int64(rng.Uint64()))
	}
	slices.Sort(values)
	values = slices.Compact(values)
	var previous []byte
	for _, integer := range values {
		value := Scalar{Kind: ScalarInteger, Integer: integer}
		ordered, err := OrderedScalarComponent(value)
		if err != nil || len(ordered) != 9 || ordered[0] != 6 {
			t.Fatalf("%d: %x %v", integer, ordered, err)
		}
		if previous != nil && bytes.Compare(previous, ordered) >= 0 {
			t.Fatalf("non-increasing key for %d: %x <= %x", integer, ordered, previous)
		}
		if decoded := int64(binary.BigEndian.Uint64(ordered[1:]) ^ (uint64(1) << 63)); decoded != integer {
			t.Fatalf("round trip: %d != %d", decoded, integer)
		}
		row, err := ScalarComponent(value)
		if err != nil || !bytes.Equal(row, append([]byte{9}, ordered...)) {
			t.Fatalf("row key: %x %v", row, err)
		}
		previous = ordered
	}
}

func TestLegacyNumericKeyBytesRemainUnchanged(t *testing.T) {
	for _, test := range []struct {
		value float64
		want  []byte
	}{
		{0, []byte{2, 128, 0, 0, 0, 0, 0, 0, 0}},
		{1, []byte{2, 191, 240, 0, 0, 0, 0, 0, 0}},
		{-1, []byte{2, 64, 15, 255, 255, 255, 255, 255, 255}},
	} {
		got, err := OrderedScalarComponent(Scalar{Kind: ScalarNumber, Number: test.value})
		if err != nil || !bytes.Equal(got, test.want) {
			t.Fatalf("%v: %x %v", test.value, got, err)
		}
	}
}
