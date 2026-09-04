package kitdb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCommitCloseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	identity := db.ID()
	if len(identity) != 32 {
		t.Fatalf("database identity length = %d, want 32", len(identity))
	}

	key := []byte("product/1")
	value := []byte(`{"title":"Kitwork"}`)
	tx := mustBegin(t, db)
	if err := tx.Put(key, value); err != nil {
		t.Fatalf("put: %v", err)
	}
	key[0] = 'X'
	value[0] = 'X'
	transaction, err := tx.Commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if transaction != 1 {
		t.Fatalf("transaction = %d, want 1", transaction)
	}

	got := requireValue(t, db, "product/1")
	if string(got) != `{"title":"Kitwork"}` {
		t.Fatalf("value = %q", got)
	}
	got[0] = 'X'
	if again := requireValue(t, db, "product/1"); string(again) != `{"title":"Kitwork"}` {
		t.Fatalf("Get returned retained caller memory: %q", again)
	}
	if _, found, err := db.Get([]byte("Xroduct/1")); err != nil || found {
		t.Fatalf("mutated caller key found=%v err=%v", found, err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	if db.ID() != identity {
		t.Fatalf("identity changed across reopen: %q != %q", db.ID(), identity)
	}
	if last, err := db.LastTransaction(); err != nil || last != 1 {
		t.Fatalf("last transaction = %d, %v; want 1", last, err)
	}
	if got := requireValue(t, db, "product/1"); string(got) != `{"title":"Kitwork"}` {
		t.Fatalf("reopened value = %q", got)
	}
}

func TestTransactionLifecycleAndOperationOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	defer db.Close()

	tx := mustBegin(t, db)
	concurrent := mustBegin(t, db)
	if err := concurrent.Put([]byte("concurrent"), []byte("prepared")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("a"), []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("b"), []byte("delete me")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("a"), []byte("last")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("empty"), nil); err != nil {
		t.Fatal(err)
	}
	if transaction, err := tx.Commit(); err != nil || transaction != 1 {
		t.Fatalf("commit: %v", err)
	}
	if transaction, err := concurrent.Commit(); err != nil || transaction != 2 {
		t.Fatalf("concurrent commit = (%d, %v), want transaction 2", transaction, err)
	}
	if err := tx.Put([]byte("late"), []byte("value")); !errors.Is(err, ErrTransactionClosed) {
		t.Fatalf("put after commit error = %v", err)
	}

	if got := requireValue(t, db, "a"); string(got) != "last" {
		t.Fatalf("ordered value = %q", got)
	}
	if _, found, err := db.Get([]byte("b")); err != nil || found {
		t.Fatalf("deleted value found=%v err=%v", found, err)
	}
	if got := requireValue(t, db, "empty"); len(got) != 0 {
		t.Fatalf("empty value length = %d", len(got))
	}
	if got := requireValue(t, db, "concurrent"); string(got) != "prepared" {
		t.Fatalf("concurrently prepared value = %q", got)
	}

	rollback := mustBegin(t, db)
	if err := rollback.Put([]byte("rolled-back"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := rollback.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := rollback.Rollback(); !errors.Is(err, ErrTransactionClosed) {
		t.Fatalf("second rollback error = %v", err)
	}
	if _, found, err := db.Get([]byte("rolled-back")); err != nil || found {
		t.Fatalf("rolled-back value found=%v err=%v", found, err)
	}

	empty := mustBegin(t, db)
	if _, err := empty.Commit(); !errors.Is(err, ErrEmptyTransaction) {
		t.Fatalf("empty commit error = %v", err)
	}
	if next, err := db.Begin(); err != nil {
		t.Fatalf("empty commit did not release writer: %v", err)
	} else if err := next.Rollback(); err != nil {
		t.Fatalf("release rollback: %v", err)
	}
}

func TestRecoveryTruncatesEveryIncompleteTail(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.kitdb")
	db := mustOpen(t, source)
	commitPut(t, db, "stable", "before")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	mainBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := os.ReadFile(databaseWALPath(source))
	if err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, source)
	commitPut(t, db, "new", "after")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	complete, err := os.ReadFile(databaseWALPath(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(complete) <= len(baseline) {
		t.Fatalf("second transaction did not extend WAL")
	}

	for cut := len(baseline); cut < len(complete); cut++ {
		cut := cut
		t.Run(fmt.Sprintf("cut_%03d", cut-len(baseline)), func(t *testing.T) {
			path := filepath.Join(root, fmt.Sprintf("cut-%03d.kitdb", cut-len(baseline)))
			if err := os.WriteFile(path, mainBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(databaseWALPath(path), complete[:cut], 0o600); err != nil {
				t.Fatal(err)
			}
			recovered := mustOpen(t, path)
			if last, err := recovered.LastTransaction(); err != nil || last != 1 {
				t.Fatalf("last transaction = %d, %v; want 1", last, err)
			}
			if got := requireValue(t, recovered, "stable"); string(got) != "before" {
				t.Fatalf("stable value = %q", got)
			}
			if _, found, err := recovered.Get([]byte("new")); err != nil || found {
				t.Fatalf("incomplete transaction visible: found=%v err=%v", found, err)
			}
			if err := recovered.Close(); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(databaseWALPath(path))
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() != int64(len(baseline)) {
				t.Fatalf("recovered WAL size = %d, want %d", info.Size(), len(baseline))
			}
		})
	}
}

func TestCompleteFrameCorruptionIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "key", "value")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	walPath := databaseWALPath(path)
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	keyOffset := walHeaderSize + frameHeaderSize + payloadPrefixSize + operationHeaderSize
	data[keyOffset] ^= 0xff
	if err := os.WriteFile(walPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Open(path)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open error = %v, want ErrCorrupt", err)
	}
	var corruption *CorruptionError
	if !errors.As(err, &corruption) {
		t.Fatalf("Open error %T does not expose CorruptionError", err)
	}
}

func TestDatabaseIdentityCorruptionIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	walPath := databaseWALPath(path)
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	data[16] ^= 0xff
	if err := os.WriteFile(walPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open error = %v, want ErrCorrupt", err)
	}
}

func TestCloseInvalidatesOpenTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	tx := mustBegin(t, db)
	if err := tx.Put([]byte("uncommitted"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, _, err := db.Get([]byte("uncommitted")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close error = %v, want ErrClosed", err)
	}
	if _, err := db.Checkpoint(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Checkpoint after Close error = %v, want ErrClosed", err)
	}
	if err := tx.Put([]byte("late"), []byte("value")); !errors.Is(err, ErrTransactionClosed) {
		t.Fatalf("Put after Close error = %v, want ErrTransactionClosed", err)
	}
	if _, err := tx.Commit(); !errors.Is(err, ErrTransactionClosed) {
		t.Fatalf("Commit after Close error = %v, want ErrTransactionClosed", err)
	}
	if err := tx.Rollback(); !errors.Is(err, ErrTransactionClosed) {
		t.Fatalf("Rollback after Close error = %v, want ErrTransactionClosed", err)
	}
}

func TestWriterLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	first := mustOpen(t, path)
	if _, err := Open(path); !errors.Is(err, ErrWriterLocked) {
		t.Fatalf("second Open error = %v, want ErrWriterLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := mustOpen(t, path)
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPartialAppendPoisonsHandleAndRecoversPreviousState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	failure := errors.New("injected partial write")
	db.mu.Lock()
	db.wal = &partialFailWAL{commitWAL: db.wal, limit: 17, failure: failure}
	db.mu.Unlock()

	tx := mustBegin(t, db)
	if err := tx.Put([]byte("uncertain"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	transaction, err := tx.Commit()
	if transaction != 1 || !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatalf("commit = (%d, %v), want transaction 1 with uncertain durability", transaction, err)
	}
	if _, _, err := db.Get([]byte("uncertain")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get after partial write error = %v, want ErrUnavailable", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if last, err := db.LastTransaction(); err != nil || last != 0 {
		t.Fatalf("recovered transaction = %d, %v; want 0", last, err)
	}
	if _, found, err := db.Get([]byte("uncertain")); err != nil || found {
		t.Fatalf("partially written value found=%v err=%v", found, err)
	}
}

func TestSyncFailureRequiresReopenToResolveCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	failure := errors.New("injected sync failure")
	db.mu.Lock()
	db.wal = &syncFailWAL{commitWAL: db.wal, failure: failure}
	db.mu.Unlock()

	tx := mustBegin(t, db)
	if err := tx.Put([]byte("uncertain"), []byte("committed frame")); err != nil {
		t.Fatal(err)
	}
	transaction, err := tx.Commit()
	if transaction != 1 || !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatalf("commit = (%d, %v), want transaction 1 with uncertain durability", transaction, err)
	}
	if _, _, err := db.Get([]byte("uncertain")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get after sync failure error = %v, want ErrUnavailable", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if last, err := db.LastTransaction(); err != nil || last != 1 {
		t.Fatalf("resolved transaction = %d, %v; want 1", last, err)
	}
	if got := requireValue(t, db, "uncertain"); string(got) != "committed frame" {
		t.Fatalf("resolved value = %q", got)
	}
}

func TestConcurrentReadsDuringCommits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	defer db.Close()
	commitPut(t, db, "counter", "0")

	var stop atomic.Bool
	errorsSeen := make(chan error, 8)
	var readers sync.WaitGroup
	for index := 0; index < 8; index++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				value, found, err := db.Get([]byte("counter"))
				if err != nil {
					errorsSeen <- err
					return
				}
				if !found || len(value) == 0 {
					errorsSeen <- fmt.Errorf("invalid concurrent value %q found=%v", value, found)
					return
				}
			}
		}()
	}
	for value := 1; value <= 50; value++ {
		commitPut(t, db, "counter", strconv.Itoa(value))
	}
	stop.Store(true)
	readers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	if got := requireValue(t, db, "counter"); string(got) != "50" {
		t.Fatalf("final value = %q, want 50", got)
	}
}

type partialFailWAL struct {
	commitWAL
	limit   int
	failure error
}

func (wal *partialFailWAL) Write(data []byte) (int, error) {
	limit := wal.limit
	if limit > len(data) {
		limit = len(data)
	}
	written, err := wal.commitWAL.Write(data[:limit])
	if err != nil {
		return written, err
	}
	return written, wal.failure
}

type syncFailWAL struct {
	commitWAL
	failure error
}

func (wal *syncFailWAL) Sync() error { return wal.failure }

func mustOpen(t *testing.T, path string) *DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	return db
}

func mustBegin(t *testing.T, db *DB) *Tx {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return tx
}

func commitPut(t *testing.T, db *DB, key, value string) uint64 {
	t.Helper()
	tx := mustBegin(t, db)
	if err := tx.Put([]byte(key), []byte(value)); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
	transaction, err := tx.Commit()
	if err != nil {
		t.Fatalf("Commit(%q): %v", key, err)
	}
	return transaction
}

func requireValue(t *testing.T, db *DB, key string) []byte {
	t.Helper()
	value, found, err := db.Get([]byte(key))
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if !found {
		t.Fatalf("Get(%q): not found", key)
	}
	return value
}
