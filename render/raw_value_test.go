package render

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// A Raw-marked value is trusted, already-safe HTML: it is emitted verbatim even when the template
// would otherwise escape it — so an engine value like $.meta.jsonld needs no raw() wrapper.
func TestWriteResolvedValueRawSkipsEscape(t *testing.T) {
	raw := value.New(`{"a":"<b>&"}`)
	raw.Raw = true

	var out strings.Builder
	writeResolvedValue(&out, raw, true) // escape requested, but Raw wins
	if got := out.String(); got != `{"a":"<b>&"}` {
		t.Fatalf("Raw value must be emitted verbatim, got: %s", got)
	}

	// Control: the same content WITHOUT the Raw flag is escaped as usual.
	var control strings.Builder
	writeResolvedValue(&control, value.New(`<b>&`), true)
	if !strings.Contains(control.String(), "&lt;") {
		t.Fatalf("non-Raw value should be escaped, got: %s", control.String())
	}
}
