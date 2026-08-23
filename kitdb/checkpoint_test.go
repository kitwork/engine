package kitdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointPublishesMainFileAndReplaysWALTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("main database = (%v, %v), want regular file", info, err)
	}

	commitPut(t, db, "product/a", "before")
	commitPut(t, db, "product/b", "checkpoint-only")
	if transaction, err := db.Checkpoint(); err != nil || transaction != 2 {
		t.Fatalf("Checkpoint = (%d, %v), want (2, nil)", transaction, err)
	}
	main, err := readMainSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer main.close()
	if main.transaction != 2 {
		t.Fatalf("main transaction = %d, want 2", main.transaction)
	}
	if info, err := os.Stat(databaseWALPath(path)); err != nil || info.Size() != walHeaderSize {
		t.Fatalf("rotated WAL = (%v, %v), want %d bytes", info, err, walHeaderSize)
	}

	tx := mustBegin(t, db)
	if err := tx.Put([]byte("product/a"), []byte("after")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte("product/b")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("product/c"), []byte("wal-tail")); err != nil {
		t.Fatal(err)
	}
	if transaction, err := tx.Commit(); err != nil || transaction != 3 {
		t.Fatalf("tail Commit = (%d, %v), want (3, nil)", transaction, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if db.checkpointTx != 2 || db.walBaseTx != 2 {
		t.Fatalf("recovered checkpoint/base = (%d, %d), want (2, 2)", db.checkpointTx, db.walBaseTx)
	}
	if last, err := db.LastTransaction(); err != nil || last != 3 {
		t.Fatalf("last transaction = %d, %v; want 3", last, err)
	}
	if got := requireValue(t, db, "product/a"); string(got) != "after" {
		t.Fatalf("product/a = %q, want after", got)
	}
	if _, found, err := db.Get([]byte("product/b")); err != nil || found {
		t.Fatalf("product/b found=%v err=%v, want deleted", found, err)
	}
	if got := requireValue(t, db, "product/c"); string(got) != "wal-tail" {
		t.Fatalf("product/c = %q, want wal-tail", got)
	}
}

func TestCheckpointIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	defer db.Close()
	commitPut(t, db, "z", "last")
	commitPut(t, db, "a", "first")

	if transaction, err := db.Checkpoint(); err != nil || transaction != 2 {
		t.Fatalf("first Checkpoint = (%d, %v)", transaction, err)
	}
	mainBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	walBefore, err := os.ReadFile(databaseWALPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if transaction, err := db.Checkpoint(); err != nil || transaction != 2 {
		t.Fatalf("idempotent Checkpoint = (%d, %v)", transaction, err)
	}
	mainAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	walAfter, err := os.ReadFile(databaseWALPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mainBefore, mainAfter) || !bytes.Equal(walBefore, walAfter) {
		t.Fatal("idempotent checkpoint changed durable bytes")
	}
	legacySegments := filepath.Join(path, "segments")
	if _, err := os.Stat(legacySegments); err == nil {
		t.Fatalf("legacy segment directory still exists: %s", legacySegments)
	} else if !errors.Is(err, os.ErrNotExist) {
		// Unix reports ENOTDIR for a child below the published database file,
		// while Windows commonly reports ErrNotExist. A regular parent proves
		// the legacy directory cannot coexist on either platform.
		info, parentErr := os.Stat(path)
		if parentErr != nil || !info.Mode().IsRegular() {
			t.Fatalf("inspect legacy segment directory: %v (database=%v, %v)", err, info, parentErr)
		}
	}
}

func TestRecoveryCompletesInterruptedWALRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "one", "first")
	commitPut(t, db, "two", "second")
	staleWAL, err := os.ReadFile(databaseWALPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Main publication is the first checkpoint commit point. Restoring the old
	// WAL models a process death immediately before its atomic replacement.
	if err := os.WriteFile(databaseWALPath(path), staleWAL, 0o600); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	if db.walBaseTx != 2 || db.walEnd != walHeaderSize {
		t.Fatalf("completed WAL rotation = base %d size %d, want base 2 size %d", db.walBaseTx, db.walEnd, walHeaderSize)
	}
	if got := requireValue(t, db, "one"); string(got) != "first" {
		t.Fatalf("one = %q", got)
	}
	if got := requireValue(t, db, "two"); string(got) != "second" {
		t.Fatalf("two = %q", got)
	}
}

func TestRecoveryRejectsStaleWALMissingMainBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "one", "first")
	walThroughOne, err := os.ReadFile(databaseWALPath(path))
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "two", "second")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databaseWALPath(path), walThroughOne, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open error = %v, want ErrCorrupt", err)
	}
}

func TestRecoveryRejectsWALBaseNewerThanMain(t *testing.T) {
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
	binary.LittleEndian.PutUint64(data[32:40], 1)
	binary.LittleEndian.PutUint32(data[walHeaderChecksumAt:], crc32.Checksum(data[:walHeaderChecksumAt], crc32cTable))
	if err := os.WriteFile(walPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open error = %v, want ErrCorrupt", err)
	}
}

func TestRecoveryRejectsWALBaseChecksumMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "key", "value")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	walPath := databaseWALPath(path)
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	baseChecksum := binary.LittleEndian.Uint32(data[40:44])
	binary.LittleEndian.PutUint32(data[40:44], baseChecksum^1)
	binary.LittleEndian.PutUint32(data[walHeaderChecksumAt:], crc32.Checksum(data[:walHeaderChecksumAt], crc32cTable))
	if err := os.WriteFile(walPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open error = %v, want ErrCorrupt", err)
	}
}

func TestTransactionSequenceContinuesAcrossWALRotations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	for transaction := uint64(1); transaction <= 16; transaction++ {
		if got := commitPut(t, db, "counter", string(rune('a'+transaction-1))); got != transaction {
			t.Fatalf("Commit transaction = %d, want %d", got, transaction)
		}
		if checkpoint, err := db.Checkpoint(); err != nil || checkpoint != transaction {
			t.Fatalf("Checkpoint = (%d, %v), want (%d, nil)", checkpoint, err, transaction)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if last, err := db.LastTransaction(); err != nil || last != 16 {
		t.Fatalf("last transaction = %d, %v; want 16", last, err)
	}
	if db.checkpointTx != 16 || db.walBaseTx != 16 || db.walEnd != walHeaderSize {
		t.Fatalf("checkpoint/base/end = (%d, %d, %d)", db.checkpointTx, db.walBaseTx, db.walEnd)
	}
}

func TestRecoveryReplaysTailAfterPublishedMainBeforeRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "value", "one")
	commitPut(t, db, "value", "two")
	staleWAL, err := os.ReadFile(databaseWALPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	tail, err := encodeFrame(3, []operation{{kind: operationPut, key: []byte("tail"), value: []byte("three")}})
	if err != nil {
		t.Fatal(err)
	}
	staleWAL = append(staleWAL, tail...)
	if err := os.WriteFile(databaseWALPath(path), staleWAL, 0o600); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if last, err := db.LastTransaction(); err != nil || last != 3 {
		t.Fatalf("last transaction = %d, %v; want 3", last, err)
	}
	if got := requireValue(t, db, "value"); string(got) != "two" {
		t.Fatalf("value = %q", got)
	}
	if got := requireValue(t, db, "tail"); string(got) != "three" {
		t.Fatalf("tail = %q", got)
	}
}

func TestMainPageCorruptionIsRejectedOnReadAndVerify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "key", "value")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	valueOffset := db.main.blocks[0].offset + operationHeaderSize + int64(len("key"))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[valueOffset] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatalf("fast Open error = %v", err)
	}
	_, _, err = db.Get([]byte("key"))
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Get corrupted page error = %v, want ErrCorrupt", err)
	}
	var corruption *CorruptionError
	if !errors.As(err, &corruption) || corruption.File != path {
		t.Fatalf("Get error = %#v, want main-file CorruptionError", err)
	}
	if err := db.Verify(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Verify error = %v, want ErrCorrupt", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWithOptions(path, OpenOptions{VerifyOnOpen: true}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("verified Open error = %v, want ErrCorrupt", err)
	}
}

func TestMainFileFromAnotherDatabaseIsRejected(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "first.kitdb")
	first := mustOpen(t, firstPath)
	commitPut(t, first, "same", "value")
	if _, err := first.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	secondPath := filepath.Join(root, "second.kitdb")
	second := mustOpen(t, secondPath)
	commitPut(t, second, "same", "other")
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	foreign, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(secondPath); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open with foreign main file error = %v, want ErrCorrupt", err)
	}
}

func TestCheckpointRecoveryTruncatesIncompleteWALTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "checkpoint", "one")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "tail", "two")
	wantWALEnd := db.walEnd

	failure := errors.New("injected checkpoint-tail write")
	db.mu.Lock()
	db.wal = &partialFailWAL{commitWAL: db.wal, limit: 17, failure: failure}
	db.mu.Unlock()
	tx := mustBegin(t, db)
	if err := tx.Put([]byte("partial"), []byte("three")); err != nil {
		t.Fatal(err)
	}
	if transaction, err := tx.Commit(); transaction != 3 || !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatalf("partial Commit = (%d, %v), want transaction 3 with uncertain durability", transaction, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if db.walEnd != wantWALEnd {
		t.Fatalf("recovered WAL end = %d, want %d", db.walEnd, wantWALEnd)
	}
	if last, err := db.LastTransaction(); err != nil || last != 2 {
		t.Fatalf("last transaction = %d, %v; want 2", last, err)
	}
	if got := requireValue(t, db, "checkpoint"); string(got) != "one" {
		t.Fatalf("checkpoint value = %q", got)
	}
	if got := requireValue(t, db, "tail"); string(got) != "two" {
		t.Fatalf("tail value = %q", got)
	}
	if _, found, err := db.Get([]byte("partial")); err != nil || found {
		t.Fatalf("partial value found=%v err=%v", found, err)
	}
}

func TestCheckpointIgnoresUnpublishedStagingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "stable", "value")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "tail", "value")
	if err := os.WriteFile(filepath.Join(root, ".kitdb-main-crashed.tmp"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if got := requireValue(t, db, "stable"); string(got) != "value" {
		t.Fatalf("stable value = %q", got)
	}
	if got := requireValue(t, db, "tail"); string(got) != "value" {
		t.Fatalf("tail value = %q", got)
	}
}

func TestCheckpointRoundTripsBinaryKeysAndEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	lowKey := []byte{0x00}
	highKey := []byte{0xff, 0x00}
	tx := mustBegin(t, db)
	if err := tx.Put(highKey, []byte{0x00, 0xff}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(lowKey, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	if value, found, err := db.Get(lowKey); err != nil || !found || len(value) != 0 {
		t.Fatalf("low binary key = (%x, %v, %v), want found empty value", value, found, err)
	}
	if value, found, err := db.Get(highKey); err != nil || !found || string(value) != string([]byte{0x00, 0xff}) {
		t.Fatalf("high binary key = (%x, %v, %v)", value, found, err)
	}
	tx = mustBegin(t, db)
	if err := tx.Delete(lowKey); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(highKey); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if db.main.records != 0 || len(db.overlay) != 0 {
		t.Fatalf("empty main file loaded %d records and %d overlay mutations", db.main.records, len(db.overlay))
	}
	if info, err := os.Stat(path); err != nil || info.Size() <= generationDataOffset {
		t.Fatalf("empty logical generation file = (%v, %v), want append-only generation metadata", info, err)
	}
}
