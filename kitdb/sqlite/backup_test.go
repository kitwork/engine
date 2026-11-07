package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// TestAnAnchorAsATableHistoryStarts: a table's history that starts after the file's newest anchor gets an anchor at the
// commit it starts in, written in the background (KITWORK_KITDB.md câu 13.11). Its read transaction starts as that
// commit ends, so what the writer commits while the anchor waits for another (an anchor, a Retain) is not in it, and the
// writer does not wait for it; a restore of the table to a moment from its start needs no other anchor. The next start
// gets its own. The file opened again writes no anchor for a start an anchor holds, and writes the one a start lacks.
//
// CONTROL (08/11/2026): with the writer not telling the database of the commits that change what the history follows,
// "no anchor of notes' start: anchors [], want one at revision 3"; with the read transaction started once the anchor
// is free to be written, "the anchor of notes' start: [revision 5 (base)], want revision 3 (base)"; with Open not
// looking for a start after the newest anchor, "opened again without the anchor of tags' start: anchors []".
func TestAnAnchorAsATableHistoryStarts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	listAnchors := func() []anchor {
		t.Helper()
		anchors, err := database.anchors()
		if err != nil {
			t.Fatal(err)
		}
		return anchors
	}
	describe := func(anchors []anchor) string {
		var said []string
		for _, item := range anchors {
			kind := map[anchorKind]string{anchorWhole: "whole", anchorBase: "base", anchorPages: "pages"}[item.kind]
			said = append(said, fmt.Sprintf("revision %d (%s)", item.revision, kind))
		}
		return "[" + strings.Join(said, ", ") + "]"
	}
	from := func(table string) uint64 {
		t.Helper()
		_, from, err := TableHistory(ctx, database.readers, table)
		if err != nil {
			t.Fatal(err)
		}
		return from
	}
	bodiesOf := func(target string) string {
		t.Helper()
		restored, err := sql.Open("sqlite", "file:"+filepath.ToSlash(target)+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		var bodies string
		if err := restored.QueryRow(`SELECT group_concat(body, ',') FROM (SELECT body FROM notes ORDER BY id)`).Scan(&bodies); err != nil {
			t.Fatal(err)
		}
		return bodies
	}
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`,
		`CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, database, `INSERT INTO notes (body) VALUES ('trước')`) // no trace: notes has no history yet

	var moment time.Time
	func() {
		database.anchoring.Lock() // an anchor or a Retain running: the anchor of notes' start waits for it
		defer database.anchoring.Unlock()
		if _, err := database.SetHistory(ctx, "t", "notes", true); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, database, `INSERT INTO notes (body) VALUES ('một')`)
		time.Sleep(2 * time.Millisecond)
		moment = time.Now()
		time.Sleep(2 * time.Millisecond)
		mustWrite(t, database, `INSERT INTO notes (body) VALUES ('hai')`)
	}()
	database.background.Wait()
	notesFrom := from("notes")
	anchors := listAnchors()
	if len(anchors) == 0 {
		t.Fatalf("no anchor of notes' start: anchors %s, want one at revision %d", describe(anchors), notesFrom)
	}
	if len(anchors) != 1 || anchors[0].revision != notesFrom || anchors[0].kind != anchorBase {
		t.Fatalf("the anchor of notes' start: %s, want revision %d (base)", describe(anchors), notesFrom)
	}
	target := filepath.Join(t.TempDir(), "restored.db")
	if _, err := database.RestoreTable(ctx, moment, target, "notes"); err != nil {
		t.Fatalf("RestoreTable of notes from the anchor of its start: %v", err)
	}
	if got := bodiesOf(target); got != "trước,một" {
		t.Fatalf("notes restored from the anchor of its start: %q, want trước,một", got)
	}

	if _, err := database.SetHistory(ctx, "t", "tags", true); err != nil {
		t.Fatal(err)
	}
	database.background.Wait()
	tagsFrom := from("tags")
	anchors = listAnchors()
	if len(anchors) != 2 || anchors[1].revision != tagsFrom || anchors[1].kind != anchorPages {
		t.Fatalf("after tags' start: anchors %s, want revision %d (pages) after notes'", describe(anchors), tagsFrom)
	}
	if database.Revision() != tagsFrom {
		t.Fatalf("revision %d after tags' start at %d: the test writes nothing after it", database.Revision(), tagsFrom)
	}
	before := describe(anchors)

	reopen := func() {
		t.Helper()
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if database, err = Open(ctx, path, Options{}); err != nil {
			t.Fatal(err)
		}
		database.background.Wait()
	}
	reopen()
	if got := describe(listAnchors()); got != before {
		t.Fatalf("opened again with an anchor of every start: anchors %s, want %s", got, before)
	}
	// the anchors gone, as if the file closed before the anchor of tags' start was written
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path + ".history"); err != nil {
		t.Fatal(err)
	}
	reopen()
	anchors = listAnchors()
	if len(anchors) == 0 {
		t.Fatalf("opened again without the anchor of tags' start: anchors %s", describe(anchors))
	}
	if len(anchors) != 1 || anchors[0].revision != tagsFrom {
		t.Fatalf("opened again without the anchor of tags' start: anchors %s, want revision %d", describe(anchors), tagsFrom)
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
