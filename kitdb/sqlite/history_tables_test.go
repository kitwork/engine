package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/search"
)

// recordedChanges is what the history records after revision: each change as "operation table", a Tracked one as
// "tracked table".
func recordedChanges(t *testing.T, database *Database, after uint64) []string {
	t.Helper()
	var out []string
	if err := database.Changes(context.Background(), after, func(commit Commit) error {
		for _, change := range commit.Changes {
			switch change.Operation {
			case Insert:
				out = append(out, "insert "+change.Table)
			case Update:
				out = append(out, "update "+change.Table)
			case Delete:
				out = append(out, "delete "+change.Table)
			case Tracked:
				out = append(out, "tracked "+change.Table)
			case Statement:
				out = append(out, "statement")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func mustWrite(t *testing.T, database *Database, statements ...string) uint64 {
	t.Helper()
	revision, err := database.Write(context.Background(), "t", func(transaction *Transaction) error {
		for _, statement := range statements {
			if _, err := transaction.Exec(statement); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

// TestHistoryFollowsTheTablesFollowed: in a new file the history records no table's rows until one's history is on
// (KITWORK_KITDB.md câu 13.11 (a), (d)): writes to a table without history commit no revision; SetHistory on writes a
// Tracked change and from then the table's changes are recorded; off, they are no longer; on again, a new Tracked
// change. A table without history may be WITHOUT ROWID, and turning on its history is refused.
//
// CONTROL (07/11/2026): with the hook recording every table, "writes to notes, a table without history: revisions 1 →
// 2, want no commit"; with no Tracked change when the recording starts, "after SetHistory(orders, on): [insert
// kitwork_tables], want [insert kitwork_tables tracked orders]".
func TestHistoryFollowsTheTablesFollowed(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "kitwork.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, amount INTEGER)`); err != nil {
		t.Fatal(err)
	}
	before := database.Revision()
	if after := mustWrite(t, database, `INSERT INTO notes (body) VALUES ('a'), ('b')`, `UPDATE notes SET body = 'c' WHERE id = 1`); after != before {
		t.Fatalf("writes to notes, a table without history: revisions %d → %d, want no commit", before, after)
	}

	mark := database.Revision()
	if _, err := database.SetHistory(ctx, "t", "orders", true); err != nil {
		t.Fatal(err)
	}
	if got := recordedChanges(t, database, mark); strings.Join(got, ",") != "insert kitwork_tables,tracked orders" {
		t.Fatalf("after SetHistory(orders, on): %v, want [insert kitwork_tables tracked orders]", got)
	}
	mark = database.Revision()
	mustWrite(t, database, `INSERT INTO orders (amount) VALUES (5)`, `INSERT INTO notes (body) VALUES ('d')`)
	mustWrite(t, database, `UPDATE orders SET amount = 6`)
	if got := recordedChanges(t, database, mark); strings.Join(got, ",") != "insert orders,update orders" {
		t.Fatalf("writes to orders and notes: %v, want orders' only", got)
	}

	if _, err := database.SetHistory(ctx, "t", "orders", false); err != nil {
		t.Fatal(err)
	}
	mark = database.Revision()
	if after := mustWrite(t, database, `INSERT INTO orders (amount) VALUES (7)`); after != mark {
		t.Fatalf("a write to orders with its history off committed revision %d", after)
	}
	if _, err := database.SetHistory(ctx, "t", "orders", true); err != nil {
		t.Fatal(err)
	}
	if got := recordedChanges(t, database, mark); strings.Join(got, ",") != "update kitwork_tables,tracked orders" {
		t.Fatalf("on again: %v, want [update kitwork_tables tracked orders]", got)
	}

	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE keyed (k TEXT PRIMARY KEY, v TEXT) WITHOUT ROWID`); err != nil {
		t.Fatalf("a WITHOUT ROWID table without history: %v", err)
	}
	mustWrite(t, database, `INSERT INTO keyed VALUES ('a', 'b')`)
	if _, err := database.SetHistory(ctx, "t", "keyed", true); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("the history of a WITHOUT ROWID table turned on: %v, want ErrUnsupportedSchema", err)
	}
	if _, err := database.SetHistory(ctx, "t", "missing", true); err == nil {
		t.Fatal("the history of a table the file does not have was set")
	}
}

// TestAFileOfBeforeKeepsTheHistoryOfEveryTable: a file whose history recorded every table (written with
// HistoryOfEveryTable, as before 07/11/2026) opened without it keeps every table's history on, recorded, with no
// Tracked change; a table made after has none.
//
// CONTROL (07/11/2026): with no adoption at open, "after the file of before opened again: [], want [insert notes]".
func TestAFileOfBeforeKeepsTheHistoryOfEveryTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	old, err := Open(ctx, path, Options{HistoryOfEveryTable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, old, `INSERT INTO notes (body) VALUES ('a')`)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	mark := database.Revision()
	mustWrite(t, database, `INSERT INTO notes (body) VALUES ('b')`)
	if got := recordedChanges(t, database, mark); strings.Join(got, ",") != "insert notes" {
		t.Fatalf("after the file of before opened again: %v, want [insert notes]", got)
	}
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE fresh (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	mark = database.Revision()
	if after := mustWrite(t, database, `INSERT INTO fresh DEFAULT VALUES`); after != mark {
		t.Fatalf("a table made after: its write committed revision %d", after)
	}
	// opened again, nothing changes: no table is adopted twice, none starts
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if revision := again.Revision(); revision != mark {
		t.Fatalf("opened again: revision %d, want %d", revision, mark)
	}
	mark = again.Revision()
	if after := mustWrite(t, again, `INSERT INTO fresh DEFAULT VALUES`); after != mark {
		t.Fatalf("opened again, a table without history committed revision %d", after)
	}
}

// TestASearchIndexRebuildsWhereTheRecordingStarts: rows written while a table was not followed (no Options.Search)
// are found once it is: the writer writes a Tracked change as the recording starts, and the index, which cannot know
// what changed with no trace, rebuilds from the rows.
//
// CONTROL (07/11/2026): with the search index taking no notice of a Tracked change, "rows written before the table
// was searchable: 0 hits for bình giữ, want 3".
func TestASearchIndexRebuildsWhereTheRecordingStarts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	searchable := Options{Search: []SearchTable{{Table: "products", Columns: searchColumns}}}
	database, err := Open(ctx, path, searchable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, description TEXT)`); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, database, `INSERT INTO products (name, description) VALUES ('áo thun', 'mềm')`)
	count := func(database *Database, text string) int {
		t.Helper()
		var hits []SearchHit
		if err := database.Read(ctx, func(snapshot *Snapshot) error {
			var err error
			hits, err = snapshot.Search("products", search.MatchQuery{Field: "name", Text: text}, search.SearchOptions{})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return len(hits)
	}
	if n := count(database, "áo thun"); n != 1 {
		t.Fatalf("a searchable table: %d hits, want 1", n)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	// not searchable for a while: its rows change with no trace
	plain, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		mustWrite(t, plain, fmt.Sprintf(`INSERT INTO products (name, description) VALUES ('bình giữ nhiệt %d', 'inox')`, i))
	}
	mustWrite(t, plain, `DELETE FROM products WHERE name = 'áo thun'`)
	if err := plain.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(ctx, path, searchable)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if n := count(again, "bình giữ"); n != 3 {
		t.Fatalf("rows written before the table was searchable: %d hits for bình giữ, want 3", n)
	}
	if n := count(again, "áo thun"); n != 0 {
		t.Fatalf("a row deleted before the table was searchable: %d hits for áo thun, want 0", n)
	}
}

// TestRestoreKeepsTheHistoryOfTheTablesFollowed: a restore to a moment brings a table with history to its rows at
// that moment, and leaves one without history as the anchor holds it.
//
// CONTROL (07/11/2026): with replay refusing a Tracked change (as an unknown operation), "restore: replay revision 3:
// kitdb: history is corrupt: operation 6".
func TestRestoreKeepsTheHistoryOfTheTablesFollowed(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "kitwork.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, amount INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetHistory(ctx, "t", "orders", true); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, database, `INSERT INTO orders (amount) VALUES (1), (2)`, `INSERT INTO notes (body) VALUES ('a')`)
	time.Sleep(2 * time.Millisecond)
	moment := time.Now()
	time.Sleep(2 * time.Millisecond)
	mustWrite(t, database, `UPDATE orders SET amount = amount * 10`, `INSERT INTO notes (body) VALUES ('b')`)
	target := filepath.Join(t.TempDir(), "restored.db")
	if _, err := database.Restore(ctx, moment, target); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := sql.Open("sqlite", "file:"+filepath.ToSlash(target)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var amounts string
	var notes int
	if err := restored.QueryRow(`SELECT group_concat(amount, ',') FROM (SELECT amount FROM orders ORDER BY id)`).Scan(&amounts); err != nil {
		t.Fatal(err)
	}
	if err := restored.QueryRow(`SELECT count(*) FROM notes`).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if amounts != "1,2" || notes != 0 {
		t.Fatalf("restored: orders %q, %d notes; want orders 1,2 as at the moment, and notes as the anchor holds them (0)", amounts, notes)
	}
}
