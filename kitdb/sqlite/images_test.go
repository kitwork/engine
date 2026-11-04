package sqlite

import (
	"errors"
	"reflect"
	"testing"
)

// WholeImages rebuilds the images of format 3's updates by walking each row back: from the row as it is now, or from
// a later delete's image, across a column added since by name, and around format 1's updates, which are whole.
//
// CONTROL (04/11/2026): with each update's Old values left out of its before image, "row 1, the first update: before
// [1 b 11], want [1 a 10]".
func TestWholeImagesWalkRowsBack(t *testing.T) {
	columns := []string{"id", "name", "price"}
	update := func(rowID int64, position int, old, new any) Change {
		return Change{Operation: Update, Table: "items", RowID: rowID, Columns: columns,
			Updates: []ColumnUpdate{{Position: position, Old: old, New: new}}}
	}
	changes := []Change{
		update(1, 2, int64(10), int64(11)), // 0
		update(1, 1, "a", "b"),             // 1
		{Operation: Insert, Table: "items", RowID: 2, Columns: columns, After: []any{int64(2), "x", int64(5)}}, // 2
		update(2, 2, int64(5), int64(6)), // 3
		{Operation: Delete, Table: "items", RowID: 2, Columns: columns, Before: []any{int64(2), "x", int64(6)}}, // 4
		{Operation: Update, Table: "items", RowID: 3, Columns: columns, // 5: format 1, whole
			Before: []any{int64(3), "y", int64(8)}, After: []any{int64(3), "z", int64(8)},
			Updates: []ColumnUpdate{{0, int64(3), int64(3)}, {1, "y", "z"}, {2, int64(8), int64(8)}}},
		update(3, 2, int64(8), int64(9)), // 6
		{Operation: Update, Table: "items", RowID: 4, Columns: []string{"id", "name"}, // 7: before a column was added
			Updates: []ColumnUpdate{{Position: 1, Old: "p", New: "q"}}},
	}
	var asked []int64
	live := func(table string, rowIDs []int64) ([]string, map[int64][]any, error) {
		asked = append(asked, rowIDs...)
		return []string{"id", "name", "price", "tag"}, map[int64][]any{
			1: {int64(1), "b", int64(11), nil},
			3: {int64(3), "z", int64(9), "new"},
			4: {int64(4), "q", int64(0), nil},
		}, nil
	}
	if err := WholeImages(changes, "items", live); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(asked, []int64{1, 3, 4}) {
		t.Fatalf("live rows asked %v, want 1, 3, 4: row 2 ends deleted", asked)
	}
	for _, test := range []struct {
		name          string
		index         int
		before, after []any
	}{
		{"row 1, the first update", 0, []any{int64(1), "a", int64(10)}, []any{int64(1), "a", int64(11)}},
		{"row 1, the second update", 1, []any{int64(1), "a", int64(11)}, []any{int64(1), "b", int64(11)}},
		{"row 2, its update before the delete", 3, []any{int64(2), "x", int64(5)}, []any{int64(2), "x", int64(6)}},
		{"row 3, after format 1's update", 6, []any{int64(3), "z", int64(8)}, []any{int64(3), "z", int64(9)}},
		{"row 4, before a column was added", 7, []any{int64(4), "p"}, []any{int64(4), "q"}},
	} {
		change := changes[test.index]
		if !reflect.DeepEqual(change.Before, test.before) {
			t.Errorf("%s: before %v, want %v", test.name, change.Before, test.before)
		}
		if !reflect.DeepEqual(change.After, test.after) {
			t.Errorf("%s: after %v, want %v", test.name, change.After, test.after)
		}
	}
}

// The history and the rows disagreeing is ErrCorruptHistory: a row updated last and not in the table, a column an
// older change names that no newer image holds.
func TestWholeImagesRefusesWhatDoesNotAddUp(t *testing.T) {
	gone := []Change{{Operation: Update, Table: "items", RowID: 9, Columns: []string{"id", "name"},
		Updates: []ColumnUpdate{{Position: 1, Old: "a", New: "b"}}}}
	none := func(string, []int64) ([]string, map[int64][]any, error) {
		return []string{"id", "name"}, map[int64][]any{}, nil
	}
	if err := WholeImages(gone, "items", none); !errors.Is(err, ErrCorruptHistory) {
		t.Fatalf("an update of a row the table does not hold: %v", err)
	}
	dropped := []Change{{Operation: Update, Table: "items", RowID: 1, Columns: []string{"id", "note"},
		Updates: []ColumnUpdate{{Position: 1, Old: "a", New: "b"}}}}
	narrower := func(string, []int64) ([]string, map[int64][]any, error) {
		return []string{"id"}, map[int64][]any{1: {int64(1)}}, nil
	}
	if err := WholeImages(dropped, "items", narrower); !errors.Is(err, ErrCorruptHistory) {
		t.Fatalf("a column no newer image holds: %v", err)
	}
}
