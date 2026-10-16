package compiler

import "testing"

// unique() keeps one item of each key, the key unique(cb) gives its callback's value (runtime
// makeArrayUniqueKey): JavaScript's SameValueZero, so NaN is one key and 0 and -0 are one, a text is its
// text, and a list or an object is itself, by reference. Until 16/10/2026 unique() keyed the item's Go
// value: NaN was never NaN (four items stayed of [NaN, NaN, 0, -0, 1, 1], where unique((x) => x) kept
// three), and a list as an item failed the whole program ("hash of unhashable type"), which safe() did
// not catch. CONTROL: texts beside numbers, and null beside true and 1, which both keyed apart already.
func TestUniqueKeysItemsAsUniqueWithACallbackDoes(t *testing.T) {
	source := `const l = [1]; const o = { a: 1 };
const both = (list) => [list.slice().unique().length, list.slice().unique((x) => x).length];
const result = [both([0 / 0, 0 / 0, 0, -0, 1, 1]), both([[1], [1]]), both([l, l]), both([o, o, { a: 1 }]),
  both(["a", "a", "b", 1, "1"]), both([null, null, true, 1])];`
	got, failure := parityRun(t, source)
	if failure != nil {
		t.Fatalf("%s: %v", source, failure)
	}
	if want := `[[3,3],[2,2],[1,1],[2,2],[4,4],[3,3]]`; !sameJSON(t, got, want) {
		t.Fatalf("unique() and unique((x) => x) kept\n%s\nwant\n%s", got, want)
	}
}
