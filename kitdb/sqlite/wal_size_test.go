package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// A long read holds the checkpoint back, so the WAL grows by every commit meanwhile: here the anchor of a table's
// history start, held by an anchor already running (anchoring), as on shopping on 08/11/2026, whose WAL reached 2.46 GB
// and stayed so until the file closed. Once the read ends, the WAL is cut back to walSizeLimit as it restarts, after
// the checkpoint the next commits run; the writer kept writing all along.
//
// CONTROL (08/11/2026): with no journal_size_limit, "the WAL after the anchor: 11622552 bytes, want at most 262144".
func TestWALShrinksAfterALongRead(t *testing.T) {
	previous := walSizeLimit
	walSizeLimit = 256 << 10
	t.Cleanup(func() { walSizeLimit = previous })
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	// pages of 4 KiB: SQLite checkpoints every 1,000 pages, 4 MiB of WAL
	database, err := Open(ctx, path, Options{PageSize: 4096})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	walSize := func() int64 {
		t.Helper()
		info, err := os.Stat(path + "-wal")
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	write := func(rows int) {
		t.Helper()
		for row := 0; row < rows; row++ {
			mustWrite(t, database, `INSERT INTO notes (body) VALUES (hex(randomblob(4000)))`)
		}
	}
	func() {
		database.anchoring.Lock() // an anchor running: the anchor of notes' start waits, its snapshot open
		defer database.anchoring.Unlock()
		if _, err := database.SetHistory(ctx, "t", "notes", true); err != nil {
			t.Fatal(err)
		}
		write(400)
		if grown := walSize(); grown < 8<<20 {
			t.Fatalf("the WAL behind the anchor's snapshot: %d bytes, want it grown past 8 MiB", grown)
		}
	}()
	database.background.Wait() // the anchor written, its snapshot closed
	database.backgroundMutex.Lock()
	failed := database.backgroundErr
	database.backgroundMutex.Unlock()
	if failed != nil {
		t.Fatal(failed)
	}
	write(3) // a commit checkpoints the whole WAL, the next restarts it
	if size := walSize(); size > walSizeLimit {
		t.Fatalf("the WAL after the anchor: %d bytes, want at most %d", size, walSizeLimit)
	}
	var count int
	if err := database.readers.QueryRowContext(ctx, `SELECT count(*) FROM notes`).Scan(&count); err != nil || count != 403 {
		t.Fatalf("notes holds %d rows (%v), want 403", count, err)
	}
}

// A file closed is whole by itself: the writer's connection closes last, so SQLite checkpoints the WAL into the file
// and removes it, and a copy of kitwork.db alone holds every commit. Readers closed last (a read-only connection
// neither checkpoints nor removes the WAL) left it beside the file, 2.64 GB on shopping's copy (08/11/2026).
//
// CONTROL (08/11/2026): with the readers closed after the writer as before, "Close left kitwork.db-wal beside the
// file (… bytes)".
func TestCloseLeavesNoWAL(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "kitwork.db")
	database, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	for row := 0; row < 50; row++ {
		mustWrite(t, database, `INSERT INTO notes (body) VALUES ('a note')`)
	}
	// readers in the pool, open as a server's are
	if err := database.Read(ctx, func(snapshot *Snapshot) error {
		rows, err := snapshot.Query(`SELECT count(*) FROM notes`)
		if err != nil {
			return err
		}
		return rows.Close()
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + "-wal"); err == nil {
		t.Fatalf("Close left kitwork.db-wal beside the file (%d bytes)", info.Size())
	}
	// the file by itself, copied alone, holds every row
	copied := filepath.Join(t.TempDir(), "copy.db")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, data, 0o644); err != nil {
		t.Fatal(err)
	}
	alone, err := sql.Open("sqlite", "file:"+filepath.ToSlash(copied)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer alone.Close()
	var count int
	if err := alone.QueryRowContext(ctx, `SELECT count(*) FROM notes`).Scan(&count); err != nil || count != 50 {
		t.Fatalf("a copy of the file alone holds %d notes (%v), want 50", count, err)
	}
}
