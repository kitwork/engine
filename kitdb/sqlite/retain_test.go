package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/kitwork/engine/search"
)

// TestRetainAnchorsADayAndLetsGoOfWhatNoMomentNeeds: Retain writes an anchor once the oldest commit no anchor holds
// is Every old, and takes out the anchors and the commits no moment of the Keep before now needs. The oldest kept
// anchor's own commit stays, so a moment between it and the next commit still names its revision; a moment before it
// is ErrPruned for RevisionAt and Restore alike; a later moment restores from the kept anchor, the base the anchor of
// that day was folded into, the file as it was then with its history.
//
// CONTROL (03/11/2026): with no anchor written when one is due, "a day later Retain kept from revision 0, want 3";
// with the commits before the kept anchor left in the file, "the history holds revisions 1..3, want 3..3"; with the
// kept anchor's own commit taken out too, "the history holds revisions 0..0, want 3..3" (and the search index finds
// its revision older than its base); with a moment before the kept history not refused, "RevisionAt of the first
// insert = 0 (<nil>), want ErrPruned".
func TestRetainAnchorsADayAndLetsGoOfWhatNoMomentNeeds(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database := openTestDatabase(t, path)
	write := func(query string) uint64 {
		t.Helper()
		revision, err := database.Write(ctx, "t", func(tx *Transaction) error {
			_, err := tx.Exec(query)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT)`); err != nil {
		t.Fatal(err)
	}
	first := write(`INSERT INTO notes (id, title) VALUES (1, 'first')`)
	second := write(`INSERT INTO notes (id, title) VALUES (2, 'second')`)
	moments := revisionMoments(t, database)
	day := 24 * time.Hour
	retention := Retention{Keep: day, Every: day}

	// Within a day of its commits nothing is due: the anchor Open wrote stays the only one, every commit stays.
	kept, err := database.Retain(ctx, moments[second].Add(time.Hour), retention)
	if err != nil || kept != 0 {
		t.Fatalf("within a day Retain kept from revision %d (%v), want 0", kept, err)
	}
	if anchors, _ := database.anchors(); len(anchors) != 1 {
		t.Fatalf("within a day there are %d anchors, want the one Open wrote", len(anchors))
	}

	// A day on, the commits are anchored, and nothing a moment of the last day needs is older than that anchor.
	kept, err = database.Retain(ctx, moments[second].Add(day+time.Hour), retention)
	if err != nil || kept != second {
		t.Fatalf("a day later Retain kept from revision %d (%v), want %d", kept, err, second)
	}
	anchors, err := database.anchors()
	if err != nil || len(anchors) != 1 || anchors[0].revision != second {
		t.Fatalf("anchors = %+v (%v), want the one at revision %d alone", anchors, err, second)
	}
	var low, high int64
	if err := database.readers.QueryRowContext(ctx, `SELECT coalesce(min(rev), 0), coalesce(max(rev), 0) FROM kitwork_history`).Scan(&low, &high); err != nil {
		t.Fatal(err)
	}
	if uint64(low) != second || uint64(high) != second {
		t.Fatalf("the history holds revisions %d..%d, want %d..%d", low, high, second, second)
	}
	if _, err := database.VerifyHistory(ctx); err != nil {
		t.Fatalf("the kept history does not verify: %v", err)
	}
	from, pruned, err := database.KeptFrom(ctx)
	if err != nil || !pruned || !from.Equal(moments[second]) {
		t.Fatalf("KeptFrom = %v %v (%v), want %v", from, pruned, err, moments[second])
	}
	if revision, err := database.RevisionAt(ctx, moments[second].Add(time.Millisecond)); err != nil || revision != second {
		t.Fatalf("a moment just after revision %d names revision %d (%v), want %d", second, revision, err, second)
	}
	if revision, err := database.RevisionAt(ctx, moments[first]); !errors.Is(err, ErrPruned) {
		t.Fatalf("RevisionAt of the first insert = %d (%v), want ErrPruned", revision, err)
	}
	if _, err := restoredNotes(t, database, moments[first]); !errors.Is(err, ErrPruned) {
		t.Fatalf("restoring the first insert: %v, want ErrPruned", err)
	}

	third := write(`INSERT INTO notes (id, title) VALUES (3, 'third')`)
	moments = revisionMoments(t, database)
	if notes, err := restoredNotes(t, database, moments[third]); err != nil || notes != "id|title; 1|first; 2|second; 3|third" {
		t.Fatalf("restoring the third insert = %q (%v)", notes, err)
	}
	if notes, err := restoredNotes(t, database, moments[second]); err != nil || notes != "id|title; 1|first; 2|second" {
		t.Fatalf("restoring the second insert = %q (%v)", notes, err)
	}
	// The kept anchor is the base the anchor of revision second was folded into: the file as it was then, history and
	// all (decided 05/11/2026), read from a copy so the history directory gets no -wal or -shm.
	if anchors[0].kind != anchorBase {
		t.Fatalf("the kept anchor is %s, want a base", filepath.Base(anchors[0].path))
	}
	copyPath := filepath.Join(t.TempDir(), "anchor.db")
	if err := copyFile(anchors[0].path, copyPath); err != nil {
		t.Fatal(err)
	}
	copied, err := sql.Open("sqlite", dataSourceName(copyPath, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	var commits, top int64
	if err := copied.QueryRow(`SELECT count(*), max(rev) FROM kitwork_history`).Scan(&commits, &top); err != nil ||
		uint64(top) != second || uint64(commits) != second {
		t.Fatalf("the anchor holds %d commits up to revision %d (%v), want the %d up to its own", commits, top, err, second)
	}
}

// TestSearchRebuildsOverHistoryRetainTookOut: an index whose revision is older than the history Retain kept cannot
// fold in the commits taken out, so it is built again from the rows, and finds what they wrote.
//
// CONTROL (03/11/2026): with the index folding in what the history still holds, "áo thun finds [1], want [1 2]".
func TestSearchRebuildsOverHistoryRetainTookOut(t *testing.T) {
	ctx := context.Background()
	database := openSearchDatabase(t, filepath.Join(t.TempDir(), "kitwork.db"), 1<<20)
	defer database.Close()
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, description TEXT)`); err != nil {
		t.Fatal(err)
	}
	write := func(query string) uint64 {
		t.Helper()
		revision, err := database.Write(ctx, "seed", func(tx *Transaction) error {
			_, err := tx.Exec(query)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	found := func() []int64 {
		t.Helper()
		var ids []int64
		if err := database.Read(ctx, func(snapshot *Snapshot) error {
			hits, err := snapshot.Search("products", searchQueries[0], search.SearchOptions{})
			for _, hit := range hits {
				ids = append(ids, hit.RowID)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
		return ids
	}
	write(`INSERT INTO products VALUES (1, 'áo thun', 'cotton')`)
	if ids := found(); len(ids) != 1 {
		t.Fatalf("áo thun finds %v, want [1]", ids)
	}
	write(`INSERT INTO products VALUES (2, 'áo thun nữ', 'cotton')`)
	last := write(`INSERT INTO products VALUES (3, 'điện thoại', 'pin')`)
	moments := revisionMoments(t, database)
	day := 24 * time.Hour
	if kept, err := database.Retain(ctx, moments[last].Add(day+time.Hour), Retention{Keep: day, Every: day}); err != nil || kept != last {
		t.Fatalf("Retain kept from revision %d (%v), want %d", kept, err, last)
	}
	if ids := found(); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("áo thun finds %v, want [1 2]", ids)
	}
}
