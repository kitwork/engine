package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// formatFixture is a commit of every kind of change: a schema statement, an insert of every value type, an update
// that changes two columns (one to NULL) and keeps a third whole, a delete, a barrier.
func formatFixture() []Change {
	columns := []string{"id", "title", "price", "rating", "picture", "note"}
	return []Change{
		{Operation: Statement, Table: "notes", Statement: "CREATE TABLE notes (…)", By: "boot"},
		{Operation: Insert, Table: "notes", RowID: 7, Columns: columns, By: "an",
			After: []any{int64(7), "cà phê", int64(35000), 4.5, []byte{0, 1, 2}, nil}},
		{Operation: Update, Table: "notes", RowID: 7, Columns: columns, By: "bình", Updates: []ColumnUpdate{
			{Position: 1, Old: "cà phê", New: "cà phê"}, // kept whole: unchanged
			{Position: 2, Old: int64(35000), New: int64(36000)},
			{Position: 5, Old: nil, New: "đã sửa"},
		}},
		{Operation: Delete, Table: "notes", RowID: 8, Columns: columns, By: "an",
			Before: []any{int64(8), "trà", int64(20000), -1.25, []byte{}, "x"}},
		{Operation: Barrier, Statement: "ALTER TABLE notes ADD COLUMN tag TEXT", By: "migration"},
	}
}

func TestColumnsFormatRoundTrip(t *testing.T) {
	changes := formatFixture()
	encoded, err := encodeColumns(changes)
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != columnsFormat {
		t.Fatalf("format byte %d, want %d", encoded[0], columnsFormat)
	}
	decoded, err := decodeChanges(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, changes) {
		t.Fatalf("decoded\n  %#v\nwant\n  %#v", decoded, changes)
	}
	// the kept column is written once: its old value is its new one
	if strings.Count(string(encoded), "cà phê") != 2 {
		t.Fatalf("\"cà phê\" is written %d times, want 2: the insert's and the kept column's new value", strings.Count(string(encoded), "cà phê"))
	}
}

// An update read from format 1 is every column in Updates, Before and After whole, so a reader of updates reads
// Updates alone whatever the format.
//
// CONTROL (04/11/2026): with format 1 left without Updates, "format 1 update read as … Updates:[]sqlite.ColumnUpdate(nil)".
func TestFormatOneUpdatesReadAsUpdates(t *testing.T) {
	columns := []string{"id", "amount"}
	encoded, err := encodeChanges([]Change{{Operation: Update, Table: "orders", RowID: 1, Columns: columns,
		Before: []any{int64(1), int64(35000)}, After: []any{int64(1), int64(36000)}}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeChanges(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := []ColumnUpdate{{Position: 0, Old: int64(1), New: int64(1)}, {Position: 1, Old: int64(35000), New: int64(36000)}}
	if len(decoded) != 1 || !reflect.DeepEqual(decoded[0].Updates, want) {
		t.Fatalf("format 1 update read as %#v, want Updates %#v", decoded, want)
	}
}

// Every prefix of a commit, and a commit with a byte changed where it says what follows, is ErrCorruptHistory, never a
// panic; an encoder given columns out of order or past the table refuses.
//
// CONTROL (04/11/2026): with the bounds of a position taken out of the decoder, "a position past the columns decoded:
// <nil>".
func TestColumnsFormatRefusesMalformed(t *testing.T) {
	encoded, err := encodeColumns(formatFixture())
	if err != nil {
		t.Fatal(err)
	}
	for length := 0; length < len(encoded); length++ {
		if _, err := decodeChanges(encoded[:length]); !errors.Is(err, ErrCorruptHistory) {
			t.Fatalf("the first %d of %d bytes decoded: %v", length, len(encoded), err)
		}
	}
	update := Change{Operation: Update, Table: "t", RowID: 1, Columns: []string{"a", "b"},
		Updates: []ColumnUpdate{{Position: 1, Old: int64(1), New: int64(2)}}}
	good, err := encodeColumns([]Change{update})
	if err != nil {
		t.Fatal(err)
	}
	// the update's entry is its last bytes: position 1, kind, old 1, new 2
	entry := len(good) - 6
	if good[entry] != 1 || good[entry+1] != columnChanged {
		t.Fatalf("the update's entry is not where the test reads it: % x", good[entry:])
	}
	past := append([]byte{}, good...)
	past[entry] = 2
	if _, err := decodeChanges(past); !errors.Is(err, ErrCorruptHistory) {
		t.Fatalf("a position past the columns decoded: %v", err)
	}
	kind := append([]byte{}, good...)
	kind[entry+1] = 9
	if _, err := decodeChanges(kind); !errors.Is(err, ErrCorruptHistory) {
		t.Fatalf("an unknown kind of column entry decoded: %v", err)
	}
	for _, updates := range [][]ColumnUpdate{
		{{Position: 1, New: int64(1)}, {Position: 0, New: int64(2)}},
		{{Position: 2, New: int64(1)}},
	} {
		update.Updates = updates
		if _, err := encodeColumns([]Change{update}); err == nil {
			t.Fatalf("an update with columns %v encoded", updates)
		}
	}
}

// An update of two numbers of a wide row is a few bytes in format 3, a row twice over in format 1.
func TestColumnsFormatIsSmallForAnUpdate(t *testing.T) {
	columns := make([]string, 31)
	before := make([]any, 31)
	for position := range columns {
		columns[position] = fmt.Sprintf("column_%d", position)
		before[position] = strings.Repeat("x", 45)
	}
	before[24], before[15] = int64(10), int64(500)
	after := append([]any{}, before...)
	after[24], after[15] = int64(9), int64(501)
	one, err := encodeChanges([]Change{{Operation: Update, Table: "shopping", RowID: 42, Columns: columns, Before: before, After: after}})
	if err != nil {
		t.Fatal(err)
	}
	three, err := encodeColumns([]Change{{Operation: Update, Table: "shopping", RowID: 42, Columns: columns, Updates: []ColumnUpdate{
		{Position: 15, Old: int64(500), New: int64(501)}, {Position: 24, Old: int64(10), New: int64(9)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	// both carry the same table header: the update itself is what differs
	t.Logf("format 1 %d bytes, format 3 %d bytes", len(one), len(three))
	// what format 3 leaves out: both images of the 29 text columns the update did not touch
	if len(one)-len(three) < 2*29*45 {
		t.Fatalf("format 3 saves %d bytes on the update, want both images of the row", len(one)-len(three))
	}
}

func FuzzDecodeChanges(f *testing.F) {
	three, err := encodeColumns(formatFixture())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(three)
	one, err := encodeChanges(formatFixture()[1:2])
	if err != nil {
		f.Fatal(err)
	}
	f.Add(one)
	f.Fuzz(func(t *testing.T, data []byte) {
		changes, err := decodeChanges(data)
		if err != nil {
			if !errors.Is(err, ErrCorruptHistory) {
				t.Fatalf("an error that is not ErrCorruptHistory: %v", err)
			}
			return
		}
		for _, change := range changes {
			for _, update := range change.Updates {
				if update.Position < 0 || update.Position >= len(change.Columns) {
					t.Fatalf("update of column %d of %d decoded", update.Position, len(change.Columns))
				}
			}
		}
	})
}

// A commit of format 3 replays as the commit left its rows: an update's row is read as it was before the commit and
// written whole with its changed columns set, so two rows that swap a unique value in one commit replay, the first
// row's INSERT OR REPLACE taking the second away before the second's own image is written back.
//
// CONTROL (04/11/2026): with each update's row read only when its change runs, "replay: … update of row 2 of
// \"codes\", which the restored copy does not hold as the history says".
func TestReplayOfFormatThreeUpdates(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "copy.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE codes (id INTEGER PRIMARY KEY, code TEXT UNIQUE, note TEXT);
		INSERT INTO codes VALUES (1, 'a', 'one'), (2, 'b', 'two'), (3, 'c', 'three')`); err != nil {
		t.Fatal(err)
	}
	columns := []string{"id", "code", "note"}
	encoded, err := encodeColumns([]Change{
		{Operation: Update, Table: "codes", RowID: 1, Columns: columns, Updates: []ColumnUpdate{{Position: 1, Old: "a", New: "b"}}},
		{Operation: Update, Table: "codes", RowID: 2, Columns: columns, Updates: []ColumnUpdate{{Position: 1, Old: "b", New: "a"}}},
		{Operation: Update, Table: "codes", RowID: 3, Columns: columns, Updates: []ColumnUpdate{{Position: 2, Old: "three", New: "drei"}}},
		{Operation: Insert, Table: "codes", RowID: 4, Columns: columns, After: []any{int64(4), "d", "four"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	changes, err := decodeChanges(encoded)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	replay := &replayer{target: db, tx: tx, columns: map[string][]string{}}
	if err := replay.apply(ctx, changes); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT id, code, note FROM codes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id int
		var code, note string
		if err := rows.Scan(&id, &code, &note); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d %s %s", id, code, note))
	}
	if want := "1 b one, 2 a two, 3 c drei, 4 d four"; strings.Join(got, ", ") != want {
		t.Fatalf("after the replay: %s, want %s", strings.Join(got, ", "), want)
	}
}
