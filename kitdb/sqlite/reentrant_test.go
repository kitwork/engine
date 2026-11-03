package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A write asked for from inside a request of the same writer would wait for the request it is part of: it is
// refused at once (ErrReentrant), and the request that asked goes on. A write from any other goroutine waits its turn.
//
// CONTROL (03/11/2026): with submit not looking at the goroutine, the inner write waits for ever: the test hangs
// until its deadline, the writer stuck in the request that asked.
func TestWriteFromInsideAWriteIsRefused(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "kitwork.db"))
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT)`); err != nil {
		t.Fatal(err)
	}
	inner := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		_, err := database.Write(ctx, "t", func(tx *Transaction) error {
			_, innerErr := database.Write(ctx, "t", func(*Transaction) error { return nil })
			inner <- innerErr
			_, err := tx.Exec(`INSERT INTO notes VALUES (1, 'kept')`)
			return err
		})
		done <- err
	}()
	select {
	case err := <-inner:
		if !errors.Is(err, ErrReentrant) {
			t.Fatalf("the inner write returned %v, want ErrReentrant", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the inner write did not answer in 10s")
	}
	if err := <-done; err != nil {
		t.Fatalf("the outer write: %v", err)
	}
	if got := notesOf(t, database.Path()); got != "id|title; 1|kept" {
		t.Fatalf("notes = %q", got)
	}
	// from another goroutine, a write waits and runs
	if _, err := database.Write(ctx, "t", func(tx *Transaction) error {
		_, err := tx.Exec(`INSERT INTO notes VALUES (2, 'next')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// A deleted row's change names its columns, as an inserted or changed row's does: what history() reads the row as
// it was by. RevisionAt is the last revision committed at or before a moment.
//
// CONTROL (03/11/2026): with the Delete written without its columns, "the delete names [] for [3 deleted]"; with
// RevisionAt counting the commit after the moment, "revision at the insert's moment = 3 (<nil>), want 2".
func TestDeleteNamesItsColumnsAndRevisionAt(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t, filepath.Join(t.TempDir(), "kitwork.db"))
	if _, err := database.Migrate(ctx, "boot", `CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT)`); err != nil {
		t.Fatal(err)
	}
	insert, err := database.Write(ctx, "a", func(tx *Transaction) error {
		_, err := tx.Exec(`INSERT INTO notes VALUES (3, 'deleted')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Write(ctx, "b", func(tx *Transaction) error {
		_, err := tx.Exec(`DELETE FROM notes WHERE id = 3`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var deleted *Change
	moments := map[uint64]time.Time{}
	if err := database.Changes(ctx, 0, func(commit Commit) error {
		moments[commit.Revision] = commit.At
		for index := range commit.Changes {
			if commit.Changes[index].Operation == Delete {
				deleted = &commit.Changes[index]
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if deleted == nil || strings.Join(deleted.Columns, ",") != "id,title" || len(deleted.Before) != 2 || deleted.By != "b" {
		t.Fatalf("the delete names %v for %v", deleted.Columns, deleted.Before)
	}
	if revision, err := database.RevisionAt(ctx, moments[insert]); err != nil || revision != insert {
		t.Fatalf("revision at the insert's moment = %d (%v), want %d", revision, err, insert)
	}
	if revision, err := database.RevisionAt(ctx, moments[insert].Add(-time.Hour)); err != nil || revision != 0 {
		t.Fatalf("revision an hour before = %d (%v), want 0", revision, err)
	}
}
