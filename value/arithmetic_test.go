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

func TestModuloByZeroReturnsNil(t *testing.T) {
	if got := New(42).Mod(New(0)); got.K != Nil {
		t.Fatalf("42 %% 0 = %#v, want nil", got)
	}
}
