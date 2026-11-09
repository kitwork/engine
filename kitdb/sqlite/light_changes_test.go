package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lightFile is a file whose posts the history follows for its columns copy alone (kitwork_columnar, written by a Write
// with no statement, as kitdb/gateway's CREATE INDEX … USING columnar writes it) and whose notes keeps its history.
func lightFile(t *testing.T) (string, *Database) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "boot",
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT, stock INTEGER)`,
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`,
		`CREATE TABLE IF NOT EXISTS kitwork_columnar (table_name TEXT NOT NULL, column_name TEXT NOT NULL, PRIMARY KEY (table_name, column_name)) STRICT`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetHistory(ctx, "t", "notes", true); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, database, `INSERT INTO kitwork_columnar (table_name, column_name) VALUES ('posts', 'title')`)
	database.background.Wait() // notes' first anchor
	return path, database
}

// commitsAfter reads the commits after revision: each one's format byte and changes.
func commitsAfter(t *testing.T, database *Database, revision uint64) ([]byte, [][]Change) {
	t.Helper()
	var formats []byte
	var commits [][]Change
	if err := ReadChanges(context.Background(), database.readers, revision, func(commit Commit) error {
		var format []byte
		if err := database.readers.QueryRow(`SELECT substr(changes, 1, 1) FROM kitwork_history WHERE rev = ?`, int64(commit.Revision)).Scan(&format); err != nil {
			return err
		}
		formats = append(formats, format[0])
		commits = append(commits, commit.Changes)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return formats, commits
}

// said is a commit's changes of table, "insert 1 light", in order.
func said(changes []Change, table string) string {
	var parts []string
	for _, change := range changes {
		if change.Table != table || change.Operation < Insert || change.Operation > Delete {
			continue
		}
		part := fmt.Sprintf("%s %d", operationName(change.Operation), change.RowID)
		if change.Light {
			part += " light"
			if change.Columns != nil || change.Before != nil || change.After != nil || change.Updates != nil {
				part += " WITH VALUES"
			}
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

// A table the history follows for its columns copy or its search alone is recorded light (KITWORK_KITDB.md câu
// 13.11, lát 2b): which row each commit changed and how (insert, update, delete), no value; a row inserted and deleted
// in one commit not at all; a row whose rowid moved, the old one deleted and the new one inserted. In the same commit a
// table with its history keeps its images. A commit holding a light change is written in lightFormat; one without,
// in format 3 as before.
//
// CONTROL (09/11/2026): with no table light, "commit 1 of posts: insert 1, insert 2, insert 3, want insert 1 light, …"
// and "formats [3 3 3], want 5, 5 and 3"; with a row inserted and deleted in one commit kept, "commit 2 of posts:
// update 1 light, delete 2 light, insert 4 light, …".
func TestLightChangesOfAFollowedTable(t *testing.T) {
	_, database := lightFile(t)
	start := database.Revision()
	if _, err := database.Write(context.Background(), "t", func(transaction *Transaction) error {
		for _, statement := range []string{
			`INSERT INTO posts (id, title, stock) VALUES (1, 'trà', 1), (2, 'cà phê', 2), (3, 'bánh', 3)`,
			`INSERT INTO notes (id, body) VALUES (1, 'một')`,
		} {
			if _, err := transaction.Exec(statement); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Write(context.Background(), "t", func(transaction *Transaction) error {
		for _, statement := range []string{
			`UPDATE posts SET stock = 9 WHERE id = 1`,
			`DELETE FROM posts WHERE id = 2`,
			`INSERT INTO posts (id, title) VALUES (4, 'bốn')`,
			`DELETE FROM posts WHERE id = 4`,
			`UPDATE posts SET id = 30 WHERE id = 3`,
		} {
			if _, err := transaction.Exec(statement); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, database, `UPDATE notes SET body = 'hai' WHERE id = 1`)
	formats, commits := commitsAfter(t, database, start)
	if len(commits) != 3 {
		t.Fatalf("%d commits, want 3", len(commits))
	}
	for index, want := range []string{
		"insert 1 light, insert 2 light, insert 3 light",
		"update 1 light, delete 2 light, delete 3 light, insert 30 light",
		"",
	} {
		if got := said(commits[index], "posts"); got != want {
			t.Errorf("commit %d of posts: %s, want %s", index+1, got, want)
		}
	}
	if got := said(commits[0], "notes"); got != "insert 1" {
		t.Errorf("commit 1 of notes: %s, want insert 1 with its image", got)
	}
	for _, change := range commits[0] {
		if change.Table == "notes" && (len(change.After) != 2 || change.After[1] != "một") {
			t.Errorf("notes' insert holds %v, want its image", change.After)
		}
	}
	if formats[0] != lightFormat || formats[1] != lightFormat || formats[2] != columnsFormat {
		t.Errorf("formats %v, want %d, %d and %d", formats, lightFormat, lightFormat, columnsFormat)
	}
}

// What a light change costs the history: a commit of 200 rows of posts with a long title, light, against the same
// commit of a table whose history keeps its images.
//
// CONTROL (09/11/2026): with no table light, "200 rows light: … bytes, with images: … bytes, want light under a tenth".
func TestLightChangesAreSmall(t *testing.T) {
	_, database := lightFile(t)
	if _, err := database.Migrate(context.Background(), "t", `CREATE TABLE copies (id INTEGER PRIMARY KEY, title TEXT, stock INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetHistory(context.Background(), "t", "copies", true); err != nil {
		t.Fatal(err)
	}
	size := func(table string) int64 {
		t.Helper()
		mustWrite(t, database, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 200) INSERT INTO `+table+
			` (id, title, stock) SELECT i, printf('%.200c', 'x'), i FROM n`)
		var bytes int64
		if err := database.readers.QueryRow(`SELECT length(changes) FROM kitwork_history ORDER BY rev DESC LIMIT 1`).Scan(&bytes); err != nil {
			t.Fatal(err)
		}
		return bytes
	}
	light, images := size("posts"), size("copies")
	if light*10 > images {
		t.Fatalf("200 rows light: %d bytes, with images: %d bytes, want light under a tenth", light, images)
	}
	t.Logf("200 rows: light %d bytes, with images %d bytes", light, images)
}

// A light change says which row changed, not how, so what needs the row's values refuses it rather than guess: a
// restore leaves the light table as the anchor holds it (as a table not followed) while it brings the others to the
// moment, and a restore of the light table itself is out of reach; a table recorded light whose history is then set on
// starts its history there (a Tracked mark), its changes keeping their images from then on; WholeImages of a light
// change is out of reach; a light operation byte in a format that has none does not decode.
//
// CONTROL (09/11/2026): with restore replaying light changes as others, "Restore: replay revision 6: kitdb: history is
// corrupt: table posts has 3 columns, the change 0"; with no Tracked mark when a light table's history is set on,
// "posts' history on true from 5 (<nil>), want the revision after it was set on, past 8"; with a light bit read in any
// format, "a light change in format 3 decoded: <nil>"; with WholeImages taking a light change, a panic on its row.
func TestLightChangesAreNotReplayed(t *testing.T) {
	ctx := context.Background()
	_, database := lightFile(t)
	mustWrite(t, database, `INSERT INTO posts (id, title) VALUES (1, 'trà')`, `INSERT INTO notes (id, body) VALUES (1, 'một')`)
	time.Sleep(5 * time.Millisecond)
	moment := time.Now()
	time.Sleep(5 * time.Millisecond)
	mustWrite(t, database, `INSERT INTO notes (id, body) VALUES (2, 'hai')`)

	restored := filepath.Join(t.TempDir(), "restored.db")
	if _, err := database.Restore(ctx, moment, restored); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	copied, err := Open(ctx, restored, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var notes, posts int
	if err := copied.readers.QueryRow(`SELECT (SELECT count(*) FROM notes), (SELECT count(*) FROM posts)`).Scan(&notes, &posts); err != nil {
		t.Fatal(err)
	}
	_ = copied.Close()
	if notes != 1 || posts != 0 {
		t.Fatalf("the restored file holds %d notes and %d posts, want 1 (the moment) and 0 (the anchor)", notes, posts)
	}
	if _, err := database.RestoreTable(ctx, moment, filepath.Join(t.TempDir(), "posts.db"), "posts"); !errors.Is(err, ErrPastUnreachable) {
		t.Fatalf("RestoreTable of the light posts: %v, want ErrPastUnreachable", err)
	}

	set, err := database.SetHistory(ctx, "t", "posts", true)
	if err != nil {
		t.Fatal(err)
	}
	on, from, err := TableHistory(ctx, database.readers, "posts")
	if err != nil || !on || from <= set {
		t.Fatalf("posts' history on %v from %d (%v), want the revision after it was set on, past %d", on, from, err, set)
	}
	before := database.Revision()
	mustWrite(t, database, `UPDATE posts SET title = 'trà sữa' WHERE id = 1`)
	_, commits := commitsAfter(t, database, before)
	if got := said(commits[0], "posts"); got != "update 1" {
		t.Fatalf("posts once its history is on: %s, want update 1 with its values", got)
	}

	if err := WholeImages([]Change{{Operation: Update, Table: "posts", RowID: 1, Light: true}}, "posts", nil); !errors.Is(err, ErrPastUnreachable) {
		t.Fatalf("WholeImages of a light change: %v, want ErrPastUnreachable", err)
	}
	encoded, err := encodeColumns([]Change{{Operation: Delete, Table: "posts", RowID: 7, Light: true}})
	if err != nil || encoded[0] != lightFormat {
		t.Fatalf("a light change encoded: format %v (%v), want %d", encoded[:1], err, lightFormat)
	}
	if changes, err := decodeChanges(encoded); err != nil || len(changes) != 1 || !changes[0].Light || changes[0].RowID != 7 || changes[0].Operation != Delete {
		t.Fatalf("a light change decoded: %+v (%v)", changes, err)
	}
	encoded[0] = columnsFormat
	if _, err := decodeChanges(encoded); !errors.Is(err, ErrCorruptHistory) {
		t.Fatalf("a light change in format 3 decoded: %v", err)
	}
}
