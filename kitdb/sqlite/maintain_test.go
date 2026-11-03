package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// notesOf is every row of notes in the file at path, one "a|b|…" per row in id order, its columns first.
func notesOf(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dataSourceName(path, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT * FROM notes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	lines := []string{strings.Join(columns, "|")}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for position := range values {
			pointers[position] = &values[position]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		fields := make([]string, len(values))
		for position, value := range values {
			if bytes, ok := value.([]byte); ok {
				value = string(bytes)
			}
			fields[position] = fmt.Sprint(value)
		}
		lines = append(lines, strings.Join(fields, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "; ")
}

func restoredNotes(t *testing.T, database *Database, moment time.Time) (string, error) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "restored.db")
	if _, err := database.Restore(context.Background(), moment, target); err != nil {
		return "", err
	}
	return notesOf(t, target), nil
}

// TestMaintainMarksASchemaChangeAndRestoresAcrossIt: work Maintain runs that adds a column (and fills it, a change the
// history does not follow row by row) commits one Barrier and writes an anchor at it, so a moment before it restores
// the table as it was and a moment after it restores the table with its new column and every row written since. Work
// that changes rows of a table with the schema as it was is a Barrier too; work on bookkeeping alone (kitwork_*)
// commits nothing. An anchor that did not get written makes the restore say so, and the next Open writes it. Work
// that leaves a trigger fails, and so does every later write.
//
// CONTROL (03/11/2026): with the Barrier not committed, "the schema change committed revision 2, want 3"; with no
// anchor written at it, "the newest anchor is at revision 0, want the Barrier's 3"; with Open not looking for a
// Barrier after the newest anchor, the restore after reopening stops at the Barrier, "no anchor was written at it";
// with the rows Maintain changed not looked at, "work that changed rows of notes gave revision 4 (<nil>), want 5";
// with bookkeeping counted as rows, "work on bookkeeping alone gave revision 5 (<nil>), want 4 and no commit".
func TestMaintainMarksASchemaChangeAndRestoresAcrossIt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	write := func(query string, arguments ...any) uint64 {
		t.Helper()
		revision, err := database.Write(ctx, "t", func(tx *Transaction) error {
			_, err := tx.Exec(query, arguments...)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT)`,
		`CREATE TABLE kitwork_state (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	first := write(`INSERT INTO notes (id, title) VALUES (1, 'first')`)

	barrier, err := database.Maintain(ctx, "engine", func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `ALTER TABLE notes ADD COLUMN body TEXT`); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, `UPDATE notes SET body = 'kept'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if barrier != first+1 {
		t.Fatalf("the schema change committed revision %d, want %d", barrier, first+1)
	}
	anchors, err := database.anchors()
	if err != nil {
		t.Fatal(err)
	}
	if last := anchors[len(anchors)-1]; last.revision != barrier {
		t.Fatalf("the newest anchor is at revision %d, want the Barrier's %d", last.revision, barrier)
	}
	last := write(`INSERT INTO notes (id, title, body) VALUES (2, 'second', 'b')`)

	// Bookkeeping only (a table kitwork_*): nothing to mark. Rows of notes changed with the schema as it was: a Barrier.
	same, err := database.Maintain(ctx, "engine", func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `INSERT INTO kitwork_state VALUES ('kinds', 'written')`)
		return err
	})
	if err != nil || same != last {
		t.Fatalf("work on bookkeeping alone gave revision %d (%v), want %d and no commit", same, err, last)
	}
	edited, err := database.Maintain(ctx, "engine", func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE notes SET title = 'second, copied' WHERE id = 2`)
		return err
	})
	if err != nil || edited != last+1 {
		t.Fatalf("work that changed rows of notes gave revision %d (%v), want %d", edited, err, last+1)
	}

	moments := revisionMoments(t, database)
	var barriers []string
	if err := database.Changes(ctx, 0, func(commit Commit) error {
		for _, change := range commit.Changes {
			if change.Operation == Barrier {
				barriers = append(barriers, change.Statement)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(barriers, ", "); !strings.HasPrefix(got, "schema_version ") || !strings.HasSuffix(got, ", rows of notes") || len(barriers) != 2 {
		t.Fatalf("the history holds the Barriers %q, want a schema_version and the rows of notes", got)
	}
	if got, err := restoredNotes(t, database, moments[first]); err != nil || got != "id|title; 1|first" {
		t.Fatalf("restored before the change = %q (%v), want the table of two columns", got, err)
	}
	if got, err := restoredNotes(t, database, moments[last]); err != nil || got != "id|title|body; 1|first|kept; 2|second|b" {
		t.Fatalf("restored after the change = %q (%v), want the table of three columns and both rows", got, err)
	}
	const now = "id|title|body; 1|first|kept; 2|second, copied|b"
	if got, err := restoredNotes(t, database, moments[edited]); err != nil || got != now {
		t.Fatalf("restored after the rows changed = %q (%v), want %q", got, err, now)
	}
	if got := notesOf(t, path); got != now {
		t.Fatalf("live = %q", got)
	}

	// The anchor of the last Barrier gone: the restore says so; the next Open writes one.
	if anchors, err = database.anchors(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(anchors[len(anchors)-1].path); err != nil {
		t.Fatal(err)
	}
	if _, err := restoredNotes(t, database, moments[edited]); !errors.Is(err, ErrNoAnchor) || !strings.Contains(err.Error(), "no anchor was written at it") {
		t.Fatalf("restore across a Barrier without its anchor returned %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if database, err = Open(ctx, path, Options{}); err != nil {
		t.Fatal(err)
	}
	if got, err := restoredNotes(t, database, time.Now()); err != nil || got != now {
		t.Fatalf("restored after reopening = %q (%v), want %q", got, err, now)
	}

	// A trigger the history cannot record: the work fails, and so does every write after it.
	_, err = database.Maintain(ctx, "engine", func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `CREATE TRIGGER notes_touch AFTER INSERT ON notes BEGIN SELECT 1; END`)
		return err
	})
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("a trigger left by the work returned %v", err)
	}
	if _, err := database.Write(ctx, "t", func(tx *Transaction) error {
		_, err := tx.Exec(`INSERT INTO notes (id, title) VALUES (3, 'third')`)
		return err
	}); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("a write after it returned %v", err)
	}
}

// TestOpenAnchorsAFileTheHistoryStartsFollowing: a file that held rows before the history followed it gets an anchor
// when it opens, so every moment from then on restores.
//
// CONTROL (03/11/2026): with Open anchoring only a file it creates, the restore finds no anchor, "no anchor at or
// before the moment".
func TestOpenAnchorsAFileTheHistoryStartsFollowing(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	raw, err := sql.Open("sqlite", dataSourceName(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT)`, `INSERT INTO notes VALUES (1, 'before')`} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	database := openTestDatabase(t, path)
	revision, err := database.Write(ctx, "t", func(tx *Transaction) error {
		_, err := tx.Exec(`INSERT INTO notes VALUES (2, 'after')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restoredNotes(t, database, revisionMoments(t, database)[revision]); err != nil || got != "id|title; 1|before; 2|after" {
		t.Fatalf("restored = %q (%v), want both rows", got, err)
	}
}
