package ui

import (
	"math"
	"testing"
)

// The .ui server render writes a number in a piece of text or an attribute ({{ total / count }}) as
// JavaScript writes it, as the client kernel then does (value.NumberText; 15/10/2026: Go wrote +Inf,
// every digit of 1e21 and "-0"). CONTROL: a fraction and a whole number keep their text.
func TestStringifyWritesNumbersAsJavaScript(t *testing.T) {
	for _, row := range []struct {
		in   float64
		want string
	}{
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
		{math.NaN(), "NaN"},
		{math.Copysign(0, -1), "0"},
		{1e21, "1e+21"},
		{1e-7, "1e-7"},
		{1234567.5, "1234567.5"},
		{2.5, "2.5"},
		{40, "40"},
	} {
		if got := stringify(row.in); got != row.want {
			t.Errorf("stringify(%v) = %q, want %q", row.in, got, row.want)
		}
	}
}
