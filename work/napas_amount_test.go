package work

import (
	"math"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// A Napas amount that is a number but not a finite one (NaN, Infinity) is not read, as a value that is
// no number (null) is not: the payload carries no amount (EMV field 54). parseInt("abc") and
// Number("abc") are NaN since 16/10/2026 (the owner's E2), and the payload carried the amount "NaN"
// (5403NaN). CONTROL: a whole and a fractional amount are written as they were.
func TestNapasAmountThatIsNotFiniteIsNotRead(t *testing.T) {
	payload := func(amount value.Value) string {
		return (&KitWork{}).Napas().Bank(value.New("970415"), value.New("1234567890")).Amount(amount).Payload()
	}
	none := payload(value.NewNil())
	if strings.Contains(none, "54") && strings.Contains(none, "NaN") {
		t.Fatalf("a payload with no amount holds one: %s", none)
	}
	for _, amount := range []value.Value{
		value.ToNumber(value.New("abc")), value.New(math.NaN()), value.New(math.Inf(1)), value.New(math.Inf(-1)),
	} {
		if got := payload(amount); got != none {
			t.Errorf("amount %s gave\n%s\nwant the payload with no amount\n%s", amount.Text(), got, none)
		}
	}
	// CONTROL
	for amount, field := range map[float64]string{50000: "540550000", 12.5: "540412.5"} {
		if got := payload(value.New(amount)); !strings.Contains(got, field) {
			t.Errorf("amount %v gave %s, want it to hold %s", amount, got, field)
		}
	}
}
