package kitdb

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestCommitListenerReceivesCommittedOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	defer db.Close()

	var mu sync.Mutex
	events := make([]CommitEvent, 0, 1)
	unsubscribe, err := db.AddCommitListener(func(event CommitEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("AddCommitListener: %v", err)
	}
	defer unsubscribe()

	tx := mustBegin(t, db)
	if err := tx.Put([]byte("b"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("a"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte("b")); err != nil {
		t.Fatal(err)
	}
	transaction, err := tx.Commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("listener events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Transaction != transaction {
		t.Fatalf("event transaction = %d, want %d", event.Transaction, transaction)
	}
	db.mu.RLock()
	checksum := db.walChecksum
	db.mu.RUnlock()
	if event.Checksum != checksum {
		t.Fatalf("event checksum = %08x, want %08x", event.Checksum, checksum)
	}
	if len(event.Operations) != 3 {
		t.Fatalf("event operations = %d, want 3", len(event.Operations))
	}
	if event.Operations[0].Kind != CommitOperationPut || !bytes.Equal(event.Operations[0].Key, []byte("b")) {
		t.Fatalf("first operation = %#v", event.Operations[0])
	}
	if event.Operations[2].Kind != CommitOperationDelete || !bytes.Equal(event.Operations[2].Key, []byte("b")) {
		t.Fatalf("third operation = %#v", event.Operations[2])
	}
}

func TestWalkReturnsCurrentSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	defer db.Close()

	tx := mustBegin(t, db)
	if err := tx.Put([]byte("c"), []byte("three")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("a"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("b"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	keys := make([]string, 0, 2)
	if err := db.Walk(func(key, value []byte) error {
		if len(value) == 0 {
			return errors.New("unexpected empty value")
		}
		keys = append(keys, string(key))
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "c" {
		t.Fatalf("walk keys = %#v, want [a c]", keys)
	}
}
