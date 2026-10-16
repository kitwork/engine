package render

import (
	"math"
	"testing"

	"github.com/kitwork/engine/value"
)

// A template divides as a script does (15/10/2026, the owner's L2): by zero it prints Infinity, -Infinity
// and NaN, as JavaScript writes them; it printed null. NaN is ordered with nothing: in an expression
// (value.LessEqual, value.GreaterEqual), and in an {{ if }}, which reads its whole condition with the
// same operators since 16/10/2026 (condition.go); until then it read one word before its operator, so
// the NaN of the {{ if }} rows comes from the data, as it had to then. A number prints as String(n) does (1234567.5; Go's 'g' wrote 1.2345675e+06). CONTROL: a division by a
// number that is not zero, and the literal 10 the apps divide by.
func TestTemplateDivisionByZeroPrintsAsJavaScript(t *testing.T) {
	data := value.New(map[string]any{"total": 7, "zero": 0, "nan": math.NaN(), "big": 1234567.5, "item": map[string]any{"order": 25}})
	got := engineRender(`[{{ total / zero }}|{{ 0 - total / zero }}|{{ zero / zero }}|{{ total % zero }}|{{ zero / zero >= 1 }}|{{ zero / zero <= 1 }}|{{ if nan >= 1 }}ordered{{ else }}unordered{{ end }}|{{ if nan <= 1 }}ordered{{ else }}unordered{{ end }}|{{ if nan != 1 }}apart{{ else }}equal{{ end }}|{{ big }}|{{ total / 2 }}|{{ item.order / 10 }}]`, data, "", "")
	want := "[Infinity|-Infinity|NaN|NaN|false|false|unordered|unordered|apart|1234567.5|3.5|2.5]"
	if got != want {
		t.Fatalf("rendered\n%q\nwant\n%q", got, want)
	}
}
