package value

import "testing"

func TestArrayEqualityUsesCanonicalPointerStorage(t *testing.T) {
	left := New([]Value{New(1), New(2), New(3)})
	right := New([]Value{New(1), New(2), New(3)})
	if !left.Equal(right) {
		t.Fatal("equivalent canonical arrays were not equal")
	}
	if left.Equal(New([]Value{New(1), New(2), New(4)})) {
		t.Fatal("different canonical arrays were equal")
	}

	legacy := Value{K: Array, V: []Value{New(1), New(2), New(3)}}
	if !left.Equal(legacy) {
		t.Fatal("canonical and legacy array storage were not equal")
	}
}

func TestCollectionEqualityHandlesCycles(t *testing.T) {
	leftItems := []Value{}
	left := New(&leftItems)
	leftItems = append(leftItems, left)

	rightItems := []Value{}
	right := New(&rightItems)
	rightItems = append(rightItems, right)

	if !left.Equal(right) {
		t.Fatal("equivalent self-referencing arrays were not equal")
	}

	leftMapData := map[string]Value{}
	leftMap := New(leftMapData)
	leftMapData["self"] = leftMap
	leftMapData["value"] = New(42)

	rightMapData := map[string]Value{}
	rightMap := New(rightMapData)
	rightMapData["self"] = rightMap
	rightMapData["value"] = New(42)

	if !leftMap.Equal(rightMap) {
		t.Fatal("equivalent self-referencing maps were not equal")
	}
	rightMapData["value"] = New(43)
	if leftMap.Equal(rightMap) {
		t.Fatal("different self-referencing maps were equal")
	}
}

func TestEqualityDoesNotPanicForNonComparablePayloads(t *testing.T) {
	left := Value{K: Any, V: []int{1, 2, 3}}
	right := Value{K: Any, V: []int{1, 2, 3}}
	if !left.Equal(right) {
		t.Fatal("equivalent non-comparable payloads were not equal")
	}
}
