package hydrate

import (
	"math"
	"testing"
)

// The server's first paint writes numbers as the client kernel does, which is JavaScript, as text and as
// an attribute's value: NaN, Infinity and -Infinity (15/10/2026: Go wrote +Inf), -0, 1e21 and 1e-7. The
// conformance corpus runs the same through both engines (non-finite-numbers-print-as-javascript,
// numbers-print-and-round-as-javascript, with toFixed's ties). CONTROL: a fraction and a whole number keep
// their text.
func TestNonFiniteNumbersPrintAsJavaScript(t *testing.T) {
	for _, row := range []struct {
		in   float64
		want string
	}{
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
		{math.NaN(), "NaN"},
		{2.5, "2.5"},
		{40, "40"},
		// as String(n) writes it (value.NumberText; 15/10/2026: 'f' wrote "-0", every digit of 1e21
		// and "0.0000001", where the client writes "0", "1e+21" and "1e-7")
		{math.Copysign(0, -1), "0"},
		{1e21, "1e+21"},
		{1e-7, "1e-7"},
		{1234567.5, "1234567.5"},
	} {
		if got := toStr(row.in); got != row.want {
			t.Errorf("text of %v = %q, want %q", row.in, got, row.want)
		}
		if got := attrText(row.in); got != row.want {
			t.Errorf("attribute of %v = %q, want %q", row.in, got, row.want)
		}
	}
}
