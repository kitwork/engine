package fulltext

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kitwork/engine/search"
)

// trackedFile is a SQLite file with a table posts the way the engine tracks one (db.sqlite_columns.go in work): the
// two tracking tables and the three triggers, title the searchable column.
func trackedFile(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	writer.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = writer.Close() })
	note := func(row, condition string) string {
		return `INSERT OR REPLACE INTO kitwork_search_changes (table_name, row_id, revision) SELECT 'posts', ` + row +
			`, revision FROM kitwork_search_revisions WHERE table_name = 'posts'` + condition + `; `
	}
	bump := `UPDATE kitwork_search_revisions SET revision = revision + 1 WHERE table_name = 'posts'; `
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL, stock INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO posts (id, title) VALUES (1, 'Cà phê sữa đá'), (2, 'Trà đào cam sả'), (3, 'Bánh mì thịt')`,
		`CREATE TABLE kitwork_search_revisions (table_name TEXT PRIMARY KEY, revision INTEGER NOT NULL) WITHOUT ROWID`,
		`CREATE TABLE kitwork_search_changes (table_name TEXT NOT NULL, row_id INTEGER NOT NULL, revision INTEGER NOT NULL, PRIMARY KEY (table_name, row_id)) WITHOUT ROWID`,
		`INSERT INTO kitwork_search_revisions VALUES ('posts', 0)`,
		`CREATE TRIGGER posts_insert AFTER INSERT ON posts BEGIN ` + bump + note("NEW.rowid", "") + `END`,
		`CREATE TRIGGER posts_delete AFTER DELETE ON posts BEGIN ` + bump + note("OLD.rowid", "") + `END`,
		`CREATE TRIGGER posts_update AFTER UPDATE OF title, id ON posts BEGIN ` + bump + note("NEW.rowid", "") +
			note("OLD.rowid", " AND OLD.rowid <> NEW.rowid") + `END`,
	} {
		if _, err := writer.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return path, writer
}

func openPosts(t *testing.T, path string) (*Table, func()) {
	t.Helper()
	release, err := Shared().Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
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
// nothing in. The index kept across a close folds in what was written while it was closed; revisions that began
// again build it anew.
//
// CONTROL: with catchUp returning at once, "after an insert: banh xeo = "", want 4"; with fold keeping the deleted row,
// "after a delete: tra dao = "2", want """; with reuse refusing a tracked index, "after the restart: ready false, 0 rows
// read again, want the index used as it is".
func TestTrackedTableFoldsWhatIsWritten(t *testing.T) {
	path, writer := trackedFile(t)
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
			if _, err := writer.Exec(step.statement); err != nil {
				t.Fatal(err)
			}
		}
		if got := found(t, table, step.text); got != step.want {
			t.Fatalf("after %s: %s = %q, want %q", step.name, step.text, got, step.want)
		}
	}
	held := table.revision
	if _, err := writer.Exec(`UPDATE posts SET stock = 5`); err != nil {
		t.Fatal(err)
	}
	if found(t, table, "banh"); table.revision != held {
		t.Fatalf("a change of stock moved the index from revision %d to %d", held, table.revision)
	}
	release()

	// Written while closed: the index kept on disk is used as it is, then folds the change in.
	if _, err := writer.Exec(`INSERT INTO posts (id, title) VALUES (10, 'Phở bò tái')`); err != nil {
		t.Fatal(err)
	}
	table, release = openPosts(t, path)
	defer release()
	if !table.Ready() || table.Built() != 0 {
		t.Fatalf("after the restart: ready %v, %d rows read again, want the index used as it is", table.Ready(), table.Built())
	}
	if got := found(t, table, "pho bo"); got != "10" {
		t.Fatalf("after the restart: pho bo = %q, want 10", got)
	}
	// The revisions began again (the tracking tables were made anew): the index is built again, not folded.
	if _, err := writer.Exec(`UPDATE kitwork_search_revisions SET revision = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(`INSERT INTO posts (id, title) VALUES (11, 'Cơm tấm sườn')`); err != nil {
		t.Fatal(err)
	}
	if got := found(t, table, "com tam"); got != "11" || table.Built() != 5 {
		t.Fatalf("after the revisions began again: com tam = %q with %d rows read, want 11 with 5 (built again)", got, table.Built())
	}
}

// The last release closes the container and its indexes; an owner that still holds the file keeps them.
//
// CONTROL: with release keeping the container open, "the container answers after the last release: <nil>".
func TestReleaseClosesTheContainer(t *testing.T) {
	path, _ := trackedFile(t)
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
