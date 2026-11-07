package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// anchorNames is what <path>.history holds, or nil when it is not there.
func anchorNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path + ".history")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// TestAnAnchorIsForAFileThatKeepsHistory: a file is anchored when it keeps some table's history, never because it
// opens (KITWORK_KITDB.md câu 13.11). A new file, a file made outside KitDB, a Barrier and Retain write no anchor of a
// file that keeps no table's history; Retain lets go of its commits older than Keep without one, the newest kept, so
// its revisions go on after it opens again. Once a table's history is on, Retain anchors it. With
// Options.HistoryOfEveryTable (the old engine) a file is anchored as it opens, as before.
//
// CONTROL (07/11/2026): with Open anchoring as before whatever the mode, "a new file that keeps no table's history:
// <path>.history holds [0-….base pages.hashes]"; with Retain anchoring whether or not the file keeps a table's history,
// "Retain anchored a file that keeps no table's history: […]"; with the newest commit not kept, "Retain three days
// on kept the history from revision 4, want the newest, 3"; with Maintain anchoring at a Barrier whatever the file keeps, "a Barrier of a file that
// keeps no table's history: anchors […]".
func TestAnAnchorIsForAFileThatKeepsHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if names := anchorNames(t, path); names != nil {
		t.Fatalf("a new file that keeps no table's history: %s.history holds %v", filepath.Base(path), names)
	}
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, database, `INSERT INTO notes (body) VALUES ('a')`)
	beforeBarrier := database.Revision()
	barrier, err := database.Maintain(ctx, "t", func(conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `ALTER TABLE notes ADD COLUMN extra TEXT`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if barrier != beforeBarrier+1 {
		t.Fatalf("Maintain changing the schema: revision %d after %d, want a Barrier", barrier, beforeBarrier)
	}
	if names := anchorNames(t, path); names != nil {
		t.Fatalf("a Barrier of a file that keeps no table's history: anchors %v", names)
	}
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE tags (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	last := database.Revision()
	retention := Retention{Keep: 24 * time.Hour, Every: 24 * time.Hour}
	start, err := database.Retain(ctx, time.Now().Add(72*time.Hour), retention)
	if err != nil {
		t.Fatal(err)
	}
	if names := anchorNames(t, path); names != nil {
		t.Fatalf("Retain anchored a file that keeps no table's history: %v", names)
	}
	if start != last {
		t.Fatalf("Retain three days on kept the history from revision %d, want the newest, %d", start, last)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if database, err = Open(ctx, path, Options{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if got := database.Revision(); got != last {
		t.Fatalf("opened again after Retain: revision %d, want %d", got, last)
	}
	mustWrite(t, database, `INSERT INTO tags DEFAULT VALUES`, `INSERT INTO notes (body) VALUES ('c')`)
	if got := database.Revision(); got != last {
		t.Fatalf("a write of tables without history committed revision %d", got)
	}

	if _, err := database.SetHistory(ctx, "t", "notes", true); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Retain(ctx, time.Now().Add(72*time.Hour), retention); err != nil {
		t.Fatal(err)
	}
	if names := anchorNames(t, path); len(names) == 0 || !strings.HasSuffix(strings.Join(names, " "), "pages.hashes") {
		t.Fatalf("Retain of a file that keeps notes' history: anchors %v", names)
	}

	// a file made outside KitDB opens without copying itself
	plainPath := filepath.Join(t.TempDir(), "plain.db")
	plain, err := sql.Open("sqlite", "file:"+filepath.ToSlash(plainPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Exec(`CREATE TABLE things (id INTEGER PRIMARY KEY, name TEXT); INSERT INTO things (name) VALUES ('một')`); err != nil {
		t.Fatal(err)
	}
	plain.Close()
	opened, err := Open(ctx, plainPath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	if names := anchorNames(t, plainPath); names != nil {
		t.Fatalf("a file made outside KitDB opened: anchors %v", names)
	}

	// the old engine's mode: anchored as it opens
	everyPath := filepath.Join(t.TempDir(), "every.db")
	every, err := Open(ctx, everyPath, Options{HistoryOfEveryTable: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = every.Close() })
	if names := anchorNames(t, everyPath); len(names) == 0 {
		t.Fatalf("a new file with Options.HistoryOfEveryTable: no anchor")
	}
}

// TestBackupIsTheFileAsOfACommit: Backup copies the file as of its latest commit while the writer goes on
// (KITWORK_KITDB.md câu 13.11 (b)): the copy opens as a database, at the revision Backup returned, with every row a
// commit up to it wrote and none after; a target that exists, or the file itself, is refused, and leaves nothing.
//
// CONTROL (07/11/2026): with Backup writing an empty page in place of each page it read, "Backup: kitdb: the backup does
// not read back: …"; with the existing target not refused, "Backup to a file that exists: <nil>".
func TestBackupIsTheFileAsOfACommit(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "kitwork.db")
	database, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetHistory(ctx, "t", "notes", true); err != nil {
		t.Fatal(err)
	}
	first := database.Revision()
	// one row a commit, while the backup is taken
	var writing sync.WaitGroup
	writing.Add(1)
	go func() {
		defer writing.Done()
		for i := 0; i < 300; i++ {
			if _, err := database.Write(ctx, "t", func(transaction *Transaction) error {
				_, err := transaction.Exec(`INSERT INTO notes (body) VALUES (?)`, strings.Repeat("x", 2000))
				return err
			}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	time.Sleep(5 * time.Millisecond)
	target := filepath.Join(t.TempDir(), "elsewhere", "kitwork.db")
	revision, err := database.Backup(ctx, target)
	writing.Wait()
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if revision < first {
		t.Fatalf("Backup at revision %d, before the file's %d", revision, first)
	}
	copied, err := Open(ctx, target, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = copied.Close() })
	if got := copied.Revision(); got != revision {
		t.Fatalf("the copy opened at revision %d, Backup said %d", got, revision)
	}
	var rows int64
	if err := copied.readers.QueryRowContext(ctx, `SELECT count(*) FROM notes`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if want := int64(revision - first); rows != want {
		t.Fatalf("the copy at revision %d holds %d rows, want the %d commits after %d wrote", revision, rows, want, first)
	}
	var check string
	if err := copied.readers.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("the copy's quick_check: %q, %v", check, err)
	}

	if _, err := database.Backup(ctx, target); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("Backup to a file that exists: %v", err)
	}
	if _, err := database.Backup(ctx, path); err == nil {
		t.Fatalf("Backup to the file itself: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".backup-") {
			t.Fatalf("a refused backup left %s", entry.Name())
		}
	}
}
