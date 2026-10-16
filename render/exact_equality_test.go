package render

import (
	"testing"

	"github.com/kitwork/engine/value"
)

// A template's == and != compare two numbers exactly, as the VM's and JavaScript's (16/10/2026, the
// owner's E1): {{ a + b == third }} of 0.1 + 0.2 and 0.3 is false, and 1e-13 is not 0. Two numbers less
// than 1e-12 apart were equal (value.equalScalar). CONTROL: numbers that are equal stay equal, a sum and
// a whole number included.
func TestTemplateEqualityComparesNumbersExactly(t *testing.T) {
	data := value.New(map[string]any{"a": 0.1, "b": 0.2, "third": 0.3, "tiny": 1e-13, "zero": 0, "half": 0.5, "quarter": 0.25, "three": 0.75})
	got := engineRender(`[{{ a + b == third }}|{{ a + b != third }}|{{ tiny == zero }}|{{ tiny != zero }}|{{ half + quarter == three }}|{{ zero == 0 }}|{{ third == third }}]`, data, "", "")
	want := "[false|true|false|true|true|true|true]"
	if got != want {
		t.Fatalf("rendered\n%q\nwant\n%q", got, want)
	}
}
