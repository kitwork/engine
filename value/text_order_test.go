package value

import (
	"strconv"
	"strings"
	"testing"
)

// The text of an object writes its keys sorted, at every depth, so the same object always has the
// same text: console.log, a template printing an object, String() (14/10/2026). The language promises
// no order of keys (an object is a Go map), and the text used to follow Go's map order, which changes
// from one walk to the next: a log line printed {at, message, code} once and {code, at, message} the
// next time. Sorted is also the order JSON.stringify writes.
//
// CONTROL: the object has 26 keys, so a walk in Go's map order writes them sorted with a chance far
// below one in a billion, and every one of the 50 texts is checked: with the sort taken out, the
// first comparison already fails.
func TestTextOfAnObjectWritesItsKeysSorted(t *testing.T) {
	inner := map[string]Value{}
	outer := map[string]Value{}
	var want strings.Builder
	want.WriteString("{")
	for i := 0; i < 26; i++ {
		key := string(rune('a' + i))
		inner[key] = New(i)
		outer[key] = New(i)
	}
	outer["nested"] = New(inner)
	keys := make([]string, 0, 27)
	for i := 0; i < 26; i++ {
		keys = append(keys, string(rune('a'+i)))
	}
	keys = append(keys[:14], append([]string{"nested"}, keys[14:]...)...) // "nested" sorts after n, before o
	var innerText strings.Builder
	innerText.WriteString("{")
	for i := 0; i < 26; i++ {
		if i > 0 {
			innerText.WriteString(", ")
		}
		innerText.WriteString(string(rune('a'+i)) + ": " + strconv.Itoa(i))
	}
	innerText.WriteString("}")
	for i, key := range keys {
		if i > 0 {
			want.WriteString(", ")
		}
		if key == "nested" {
			want.WriteString("nested: " + innerText.String())
			continue
		}
		want.WriteString(key + ": " + strconv.Itoa(int(key[0]-'a')))
	}
	want.WriteString("}")

	object := New(outer)
	for round := 0; round < 50; round++ {
		if got := object.Text(); got != want.String() {
			t.Fatalf("round %d: the text of the object is\n%s\nwant its keys sorted:\n%s", round, got, want.String())
		}
	}
	// the same text through the other doors: String() and Append (a template writes with Append)
	if got := object.String(); got != want.String() {
		t.Errorf("String() = %s", got)
	}
	if got := string(object.Append(nil)); got != want.String() {
		t.Errorf("Append = %s", got)
	}
	// an array of objects: each object sorted, the array in its own order
	list := New([]Value{New(map[string]Value{"z": New(1), "a": New(2)}), New(map[string]Value{"m": New(3), "b": New(4)})})
	if got := list.Text(); got != "[{a: 2, z: 1}, {b: 4, m: 3}]" {
		t.Errorf("an array of objects = %s, want [{a: 2, z: 1}, {b: 4, m: 3}]", got)
	}
	// the place of a cycle is named on the sorted walk too: the first key that holds the object back
	self := map[string]Value{"b": New(1)}
	loop := New(self)
	self["c"] = loop
	self["a"] = loop
	if got := loop.Text(); got != "{a: "+cycleText+", b: 1, c: "+cycleText+"}" {
		t.Errorf("an object that holds itself = %s", got)
	}
}
