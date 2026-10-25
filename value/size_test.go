package value

import (
	"encoding/json"
	"reflect"
	"testing"
	"unsafe"
)

// A Value is three fields in at most four words: the line under which Go keeps a struct in registers
// (ssa.CanSSA: at most 4*PtrSize bytes and 4 fields). Past it every push, pop and store of a Value goes
// through memory with bulk write barriers (3.5x on push/pop in the layout bench of 25/10/2026), so the
// shape is a budget, not a layout that happened: what wants a field goes into K or V.
func TestValueIsThreeFieldsInFourWords(t *testing.T) {
	if fields := reflect.TypeOf(Value{}).NumField(); fields != 3 {
		t.Fatalf("Value has %d fields, want 3: N, V, K", fields)
	}
	if size, limit := unsafe.Sizeof(Value{}), 4*unsafe.Sizeof(uintptr(0)); size > limit {
		t.Fatalf("Value is %d bytes, past the %d bytes the compiler keeps in registers", size, limit)
	}
}

// A failure is one allocation, the Failure in V. Until 25/10/2026 it was a boxed message and a boxed
// Failure, and a PARTIAL failure boxed the Failure once more.
func TestFailureIsOneAllocation(t *testing.T) {
	var sink Value
	if allocs := testing.AllocsPerRun(100, func() { sink = InvalidFailure(FailCode, "boom") }); allocs != 1 {
		t.Fatalf("InvalidFailure allocates %v times, want 1", allocs)
	}
	progress := New(3)
	if allocs := testing.AllocsPerRun(100, func() { sink = PartialFailure("stopped", progress) }); allocs != 1 {
		t.Fatalf("PartialFailure allocates %v times, want 1", allocs)
	}
	_ = sink
}

// The failure is read back whole: Failed, Failure, FailureFrom and Text agree, the zero Value is no
// failure, and a copy shares the one Failure.
func TestFailureReadsBack(t *testing.T) {
	failure := PartialFailure("stopped at 3", New(3))
	if !failure.Failed() || failure.K != Invalid {
		t.Fatalf("a failure is K == Invalid and Failed(): %v %v", failure.K, failure.Failed())
	}
	attached, ok := failure.Failure()
	if !ok || attached.Code != PartialCode || attached.Message != "stopped at 3" || attached.Progress.N != 3 {
		t.Fatalf("Failure() = %+v, %v", attached, ok)
	}
	if copied, ok := FailureFrom(failure); !ok || copied != *attached {
		t.Fatalf("FailureFrom = %+v, %v, want a copy of %+v", copied, ok, *attached)
	}
	if failure.Text() != "stopped at 3" || failure.String() != "stopped at 3" {
		t.Fatalf("Text = %q, String = %q", failure.Text(), failure.String())
	}
	again := failure
	if shared, _ := again.Failure(); shared != attached {
		t.Fatal("a copy of the value must share the one Failure")
	}
	if plain, ok := InvalidFailure(FailCode, "boom").Failure(); !ok || plain.Code != FailCode || plain.Message != "boom" {
		t.Fatalf("InvalidFailure().Failure() = %+v, %v", plain, ok)
	}
	if (Value{}).Failed() {
		t.Fatal("the zero Value is nothing, not a failure")
	}
	if _, ok := (Value{}).Failure(); ok {
		t.Fatal("the zero Value carries no Failure")
	}
	if _, ok := New("x").Failure(); ok {
		t.Fatal("a String carries no Failure")
	}
	// a failure built by hand around a string (tests) is a failure with no code
	byHand := Value{K: Invalid, V: "boom"}
	if !byHand.Failed() || byHand.Text() != "boom" {
		t.Fatalf("by hand: Failed %v, Text %q", byHand.Failed(), byHand.Text())
	}
	if _, ok := FailureFrom(byHand); ok {
		t.Fatal("a failure built around a string has no code to give")
	}
}

// Trust is the payload's type: Trusted makes it, IsTrusted reads it, AsString reads the text of both,
// equality looks at the text, and New keeps a TrustedHTML a String.
func TestTrustedIsAStringByType(t *testing.T) {
	trusted := Trusted("<b>x</b>")
	if trusted.K != String || !trusted.IsTrusted() {
		t.Fatalf("Trusted: K %v, IsTrusted %v", trusted.K, trusted.IsTrusted())
	}
	if s, ok := trusted.AsString(); !ok || s != "<b>x</b>" {
		t.Fatalf("AsString = %q, %v", s, ok)
	}
	if trusted.Text() != "<b>x</b>" || trusted.String() != "<b>x</b>" || trusted.Len() != 8 {
		t.Fatalf("Text %q, String %q, Len %d", trusted.Text(), trusted.String(), trusted.Len())
	}
	if !trusted.Equal(New("<b>x</b>")) || !New("<b>x</b>").Equal(trusted) {
		t.Fatal("a trusted string equals the plain string of the same text")
	}
	if !trusted.Truthy() || Trusted("").Truthy() {
		t.Fatal("truthiness is the text's")
	}
	if got := New(TrustedHTML("<i>y</i>")); got.K != String || !got.IsTrusted() {
		t.Fatalf("New(TrustedHTML) = %v, trusted %v", got.K, got.IsTrusted())
	}
	if New("<b>x</b>").IsTrusted() {
		t.Fatal("a plain string is not trusted")
	}
	// JSON writes the text, as it writes a plain string (encoding/json escapes < and >)
	want, _ := json.Marshal("<b>x</b>")
	if data, err := trusted.MarshalJSON(); err != nil || string(data) != string(want) {
		t.Fatalf("JSON of a trusted string = %s, %v, want %s", data, err, want)
	}
}
