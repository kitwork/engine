package fulltext

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/search"
)

// trackedFile is a SQLite file with a table posts whose history the engine keeps: every write through its one
// writer (engine/kitdb/sqlite), title the searchable column. write runs one statement as one commit.
func trackedFile(t *testing.T) (string, *kitdbsqlite.Database, func(statement string)) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database, err := kitdbsqlite.Open(ctx, path, kitdbsqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "test", `CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL, stock INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	write := func(statement string) {
		t.Helper()
		if _, err := database.Write(ctx, "test", func(transaction *kitdbsqlite.Transaction) error {
			_, err := transaction.Exec(statement)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	write(`INSERT INTO posts (id, title) VALUES (1, 'Cà phê sữa đá'), (2, 'Trà đào cam sả'), (3, 'Bánh mì thịt')`)
	return path, database, write
}

func openPosts(t *testing.T, path string) (*Table, func()) {
	t.Helper()
	release, err := Shared().Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release) // a test that stops early still lets go of the file
	table, err := Shared().Open(path, "posts", "test.posts", []Column{{Name: "title", Boost: 1}}, false)
	if err != nil {
		release()
		t.Fatal(err)
	}
	return table, release
}

// found is the ids of the rows that match text, in order.
func found(t *testing.T, table *Table, text string) string {
	t.Helper()
	hits, err := table.Search(context.Background(), search.MatchQuery{Fields: []string{"title"}, Text: text}, 100)
	if err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	ids := make([]string, len(hits))
	for position, hit := range hits {
		ids[position] = hit.ID
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// A tracked table's index holds what the app just wrote: a row added is found at the next read, a title changed is
// found by its new words and no longer by its old ones, a row deleted is gone; a change of another column folds
// nothing in. The index kept across a close folds in what was written while it was closed; a change of the schema
// builds it anew.
//
// CONTROL (03/11/2026, the changes read from kitwork_history): with catchUp returning at once, "after an insert: banh
// xeo = "", want 4"; with fold keeping the deleted row, "after a delete: tra dao = "2", want """; with an update
// counted whatever it changed, "a change of stock wrote the index again"; with reuse refusing a tracked index, "after
// the restart: ready false, 0 rows read again, want the index used as it is"; with a Barrier read as any commit,
// "after the schema changed: com tam = "11" with 0 rows read, want 11 with 5 (built again)".
func TestTrackedTableFoldsWhatIsWritten(t *testing.T) {
	path, database, write := trackedFile(t)
	table, release := openPosts(t, path)
	if !table.Tracked() {
		t.Fatal("posts is not tracked")
	}
	if got := found(t, table, "ca phe"); got != "1" {
		t.Fatalf("ca phe = %q, want 1", got)
	}
	steps := []struct {
		name, statement, text, want string
	}{
		{"an insert", `INSERT INTO posts (id, title) VALUES (4, 'Bánh xèo miền Tây')`, "banh xeo", "4"},
		{"a change of title", `UPDATE posts SET title = 'Cà phê muối' WHERE id = 1`, "ca phe muoi", "1"},
		{"the old title", ``, "sua da", ""},
		{"a delete", `DELETE FROM posts WHERE id = 2`, "tra dao", ""},
		{"a change of key", `UPDATE posts SET id = 9 WHERE id = 3`, "banh mi", "9"},
	}
	for _, step := range steps {
		if step.statement != "" {
			write(step.statement)
		}
		if got := found(t, table, step.text); got != step.want {
			t.Fatalf("after %s: %s = %q, want %q", step.name, step.text, got, step.want)
		}
	}
	table.mutex.RLock()
	index := table.index
	table.mutex.RUnlock()
	write(`UPDATE posts SET stock = 5`)
	found(t, table, "banh")
	table.mutex.RLock()
	same := table.index == index
	table.mutex.RUnlock()
	if !same {
		t.Fatal("a change of stock wrote the index again")
	}
	release()

	// Written while closed: the index kept on disk is used as it is, then folds the change in.
	write(`INSERT INTO posts (id, title) VALUES (10, 'Phở bò tái')`)
	table, release = openPosts(t, path)
	defer release()
	if !table.Ready() || table.Built() != 0 {
		t.Fatalf("after the restart: ready %v, %d rows read again, want the index used as it is", table.Ready(), table.Built())
	}
	if got := found(t, table, "pho bo"); got != "10" {
		t.Fatalf("after the restart: pho bo = %q, want 10", got)
	}
	// The schema changed (a column added outside the history, a Barrier): the index is built again, not folded.
	if _, err := database.Maintain(context.Background(), "test", func(connection *sql.Conn) error {
		_, err := connection.ExecContext(context.Background(), `ALTER TABLE posts ADD COLUMN note TEXT`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	write(`INSERT INTO posts (id, title) VALUES (11, 'Cơm tấm sườn')`)
	if got := found(t, table, "com tam"); got != "11" || table.Built() != 5 {
		t.Fatalf("after the schema changed: com tam = %q with %d rows read, want 11 with 5 (built again)", got, table.Built())
	}
}

// An index kept on disk whose revision is older than the history the retention kept cannot fold in the commits taken
// out of it: it is built again from the rows, and finds what they wrote.
//
// CONTROL (03/11/2026): with the index folding in what the history still holds, "banh xeo = "", want 4".
func TestTrackedTableRebuildsOverHistoryTheRetentionTookOut(t *testing.T) {
	path, database, write := trackedFile(t)
	table, release := openPosts(t, path)
	if got := found(t, table, "ca phe"); got != "1" {
		t.Fatalf("ca phe = %q, want 1", got)
	}
	release()
	write(`INSERT INTO posts (id, title) VALUES (4, 'Bánh xèo miền Tây')`)
	write(`INSERT INTO posts (id, title) VALUES (5, 'Phở bò tái')`)
	var last kitdbsqlite.Commit
	if err := database.Changes(context.Background(), 0, func(commit kitdbsqlite.Commit) error {
		last = commit
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	kept, err := database.Retain(context.Background(), last.At.Add(day+time.Hour), kitdbsqlite.Retention{Keep: day, Every: day})
	if err != nil || kept != last.Revision {
		t.Fatalf("Retain kept from revision %d (%v), want %d", kept, err, last.Revision)
	}
	table, release = openPosts(t, path)
	defer release()
	if got := found(t, table, "banh xeo"); got != "4" {
		t.Fatalf("banh xeo = %q, want 4", got)
	}
	if got := found(t, table, "pho bo"); got != "5" {
		t.Fatalf("pho bo = %q, want 5", got)
	}
}

// The last release closes the container and its indexes; an owner that still holds the file keeps them.
//
// CONTROL: with release keeping the container open, "the container answers after the last release: <nil>".
func TestReleaseClosesTheContainer(t *testing.T) {
	path, _, _ := trackedFile(t)
	first, releaseFirst := openPosts(t, path)
	_, releaseSecond := openPosts(t, path)
	if got := found(t, first, "tra dao"); got != "2" {
		t.Fatalf("tra dao = %q, want 2", got)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	Shared().mutex.Lock()
	container := Shared().files[absolute].container
	Shared().mutex.Unlock()
	releaseFirst()
	if Shared().Lookup(path, "posts") == nil {
		t.Fatal("the index closed while another owner holds the file")
	}
	releaseSecond()
	if Shared().Lookup(path, "posts") != nil {
		t.Fatal("the index stays open after the last release")
	}
	if _, _, err := container.Index("posts", first.definition); !errors.Is(err, search.ErrClosed) {
		t.Fatalf("the container answers after the last release: %v", err)
	}
}

// An update is read as its Updates: format 3 holds only the columns it changed, and the index reads a changed row
// again by its id. A changed column the index does not hold changes nothing for it.
//
// CONTROL (04/11/2026): with every column of an update taken for the index's text, "an update of the price alone is a
// change of the index's text".
func TestTextChangedReadsUpdates(t *testing.T) {
	table := &Table{columns: []Column{{Name: "title"}}}
	columns := []string{"id", "title", "price"}
	change := func(updates ...kitdbsqlite.ColumnUpdate) kitdbsqlite.Change {
		return kitdbsqlite.Change{Operation: kitdbsqlite.Update, Table: "notes", RowID: 1, Columns: columns, Updates: updates}
	}
	if !table.textChanged(change(kitdbsqlite.ColumnUpdate{Position: 1, Old: "cà phê", New: "trà"})) {
		t.Fatal("an update of the title is not a change of the index's text")
	}
	if table.textChanged(change(kitdbsqlite.ColumnUpdate{Position: 2, Old: int64(1), New: int64(2)})) {
		t.Fatal("an update of the price alone is a change of the index's text")
	}
	if table.textChanged(change(kitdbsqlite.ColumnUpdate{Position: 1, Old: "trà", New: "trà"})) {
		t.Fatal("a title kept as it was is a change of the index's text")
	}
}
