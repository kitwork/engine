package value

import (
	"math"
	"testing"
)

func TestModuloSupportsFractionalNumbers(t *testing.T) {
	tests := []struct {
		name  string
		left  float64
		right float64
		want  float64
	}{
		{name: "fractional dividend", left: 5.5, right: 2, want: 1.5},
		{name: "fractional divisor", left: 5, right: 0.5, want: 0},
		{name: "fuzz regression", left: 0, right: 0.1, want: 0},
		{name: "negative dividend", left: -5.5, right: 2, want: -1.5},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := New(test.left).Mod(New(test.right))
			if got.K != Number || math.Abs(got.N-test.want) > 1e-12 {
				t.Fatalf("%v %% %v = %#v, want %v", test.left, test.right, got, test.want)
			}
		})
	}
}

// A division or a remainder by zero follows IEEE 754 as JavaScript does (15/10/2026; it was null): the
// rows are what node gives. CONTROL: a division by a number that is not zero is the quotient.
func TestDivisionByZeroIsInfinityOrNaN(t *testing.T) {
	negativeZero := math.Copysign(0, -1)
	for _, row := range []struct {
		name        string
		left, right float64
		divide      bool
		want        string
	}{
		{"one by zero", 1, 0, true, "Infinity"},
		{"minus one by zero", -1, 0, true, "-Infinity"},
		{"one by minus zero", 1, negativeZero, true, "-Infinity"},
		{"zero by zero", 0, 0, true, "NaN"},
		{"remainder by zero", 42, 0, false, "NaN"},
		{"negative remainder by zero", -5, 0, false, "NaN"},
		{"remainder of infinity", math.Inf(1), 2, false, "NaN"},
		{"remainder by infinity", 5, math.Inf(1), false, "5"},
		{"CONTROL: six by four", 6, 4, true, "1.5"},
		{"CONTROL: seven remainder three", 7, 3, false, "1"},
	} {
		var got Value
		if row.divide {
			got = New(row.left).Div(New(row.right))
		} else {
			got = New(row.left).Mod(New(row.right))
		}
		if got.K != Number || got.Text() != row.want {
			t.Errorf("%s: %v, %v = %#v (%q), want the number %s", row.name, row.left, row.right, got, got.Text(), row.want)
		}
	}
}
