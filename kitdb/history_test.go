package kitdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryRetentionIsOptIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	defer db.Close()
	commitPut(t, db, "key", "value")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(databaseHistoryPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("history directory stat = %v, want not exist", err)
	}
	if _, err := db.WalkHistory(context.Background(), 0, func(CommitEvent) error { return nil }); !errors.Is(err, ErrHistoryDisabled) {
		t.Fatalf("WalkHistory without retention = %v, want ErrHistoryDisabled", err)
	}
}

func TestRetainedHistoryStreamsTransactionsAcrossCheckpointsAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)

	first := mustBegin(t, db)
	if err := first.Put([]byte("a"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := first.Put([]byte("b"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	if transaction, err := first.Commit(); err != nil || transaction != 1 {
		t.Fatalf("first Commit = (%d, %v), want (1, nil)", transaction, err)
	}

	second := mustBegin(t, db)
	if err := second.Delete([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := second.Put([]byte("c"), []byte("three")); err != nil {
		t.Fatal(err)
	}
	if transaction, err := second.Commit(); err != nil || transaction != 2 {
		t.Fatalf("second Commit = (%d, %v), want (2, nil)", transaction, err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "d", "four")

	events := collectHistory(t, db, 0)
	if len(events) != 3 {
		t.Fatalf("history events = %d, want 3", len(events))
	}
	assertHistoryOperation(t, events[0], 1, 0, CommitOperationPut, "a", "one")
	assertHistoryOperation(t, events[0], 1, 1, CommitOperationPut, "b", "two")
	assertHistoryOperation(t, events[1], 2, 0, CommitOperationDelete, "a", "")
	assertHistoryOperation(t, events[1], 2, 1, CommitOperationPut, "c", "three")
	assertHistoryOperation(t, events[2], 3, 0, CommitOperationPut, "d", "four")

	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if !stats.HistoryEnabled || stats.HistoryBaseTransaction != 0 || stats.HistorySegments != 2 || stats.HistoryBytes == 0 {
		t.Fatalf("history stats = enabled %v base %d segments %d bytes %d", stats.HistoryEnabled, stats.HistoryBaseTransaction, stats.HistorySegments, stats.HistoryBytes)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	afterIdempotent, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if afterIdempotent.HistorySegments != stats.HistorySegments || afterIdempotent.HistoryBytes != stats.HistoryBytes {
		t.Fatalf("idempotent checkpoint changed history stats from %d/%d to %d/%d", stats.HistorySegments, stats.HistoryBytes, afterIdempotent.HistorySegments, afterIdempotent.HistoryBytes)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// An existing history directory keeps retention enabled even when callers
	// reopen with default options.
	db = mustOpen(t, path)
	defer db.Close()
	if transaction := commitPut(t, db, "e", "five"); transaction != 4 {
		t.Fatalf("reopened Commit transaction = %d, want 4", transaction)
	}
	events = collectHistory(t, db, 2)
	if len(events) != 2 || events[0].Transaction != 3 || events[1].Transaction != 4 {
		t.Fatalf("incremental history transactions = %v, want [3 4]", historyTransactions(events))
	}
}

func TestRetainedHistoryHasExplicitBaseWhenEnabledLater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "before", "retention")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpenWithHistory(t, path)
	defer db.Close()
	commitPut(t, db, "after", "retention")
	var visited int
	rangeInfo, err := db.WalkHistory(context.Background(), 0, func(CommitEvent) error {
		visited++
		return nil
	})
	if !errors.Is(err, ErrHistoryGap) {
		t.Fatalf("WalkHistory before retained base = %v, want ErrHistoryGap", err)
	}
	if rangeInfo.BaseTransaction != 1 || rangeInfo.LastTransaction != 2 || visited != 0 {
		t.Fatalf("late history range/visited = %+v/%d, want base 1 last 2 and no visits", rangeInfo, visited)
	}
	events := collectHistory(t, db, 1)
	if len(events) != 1 || events[0].Transaction != 2 {
		t.Fatalf("late retained events = %v, want [2]", historyTransactions(events))
	}
}

func TestWalkHistoryHonorsCanceledContextBeforeCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	defer db.Close()
	commitPut(t, db, "key", "value")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.WalkHistory(ctx, 0, func(CommitEvent) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("WalkHistory with canceled context = %v, want context.Canceled", err)
	}
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.HistorySegments != 0 || stats.CheckpointTransaction != 0 {
		t.Fatalf("canceled history walk checkpointed state: segments %d checkpoint %d", stats.HistorySegments, stats.CheckpointTransaction)
	}
}

func TestHistorySealFailureMakesHandleUnavailableUntilRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	commitPut(t, db, "key", "value")
	db.mu.Lock()
	db.history.segments = maxHistorySegments
	db.mu.Unlock()

	if transaction, err := db.Checkpoint(); transaction != 1 || !errors.Is(err, ErrHistoryLimit) || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Checkpoint with history limit = (%d, %v), want transaction 1 with ErrHistoryLimit and ErrUnavailable", transaction, err)
	}
	if _, err := db.LastTransaction(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("LastTransaction after history failure = %v, want ErrUnavailable", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	events := collectHistory(t, db, 0)
	if len(events) != 1 || events[0].Transaction != 1 {
		t.Fatalf("history after recovery = %v, want [1]", historyTransactions(events))
	}
}

func TestHistoryWALRotationFailureMakesHandleUnavailableUntilRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	commitPut(t, db, "key", "value")
	db.mu.Lock()
	db.wal = &historyCloseFailWAL{commitWAL: db.wal}
	db.mu.Unlock()

	if transaction, err := db.Checkpoint(); transaction != 1 || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Checkpoint with WAL close failure = (%d, %v), want transaction 1 with ErrUnavailable", transaction, err)
	}
	if _, err := db.Begin(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Begin after history WAL rotation failure = %v, want ErrUnavailable", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	events := collectHistory(t, db, 0)
	if len(events) != 1 || events[0].Transaction != 1 {
		t.Fatalf("history after WAL rotation recovery = %v, want [1]", historyTransactions(events))
	}
}

func TestRetainedHistoryDetectsCorruptedSegmentBeforeEmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	commitPut(t, db, "key", "value")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "second", "value")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	segments, err := listHistorySegments(databaseHistoryPath(path))
	if err != nil || len(segments) != 2 {
		t.Fatalf("history segments = %d, %v", len(segments), err)
	}
	file, err := os.OpenFile(segments[0].path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	checksumOffset := segments[0].bytes - frameTrailerSize + 16
	var checksumByte [1]byte
	if _, err := file.ReadAt(checksumByte[:], checksumOffset); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	checksumByte[0] ^= 0xff
	if _, err := file.WriteAt(checksumByte[:], checksumOffset); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	visited := 0
	_, err = db.WalkHistory(context.Background(), 0, func(CommitEvent) error {
		visited++
		return nil
	})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt WalkHistory = %v, want ErrCorrupt", err)
	}
	if visited != 0 {
		t.Fatalf("corrupt history emitted %d events", visited)
	}
}

func TestRetainedHistoryRejectsMissingSegmentOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	commitPut(t, db, "one", "first")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "two", "second")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	segments, err := listHistorySegments(databaseHistoryPath(path))
	if err != nil || len(segments) != 2 {
		t.Fatalf("history segments = %d, %v", len(segments), err)
	}
	if err := os.Remove(segments[0].path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrHistoryGap) {
		t.Fatalf("Open with missing history segment = %v, want ErrHistoryGap", err)
	}
}

func TestRetainedHistoryRejectsMissingTailSegmentOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	commitPut(t, db, "one", "first")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "two", "second")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	segments, err := listHistorySegments(databaseHistoryPath(path))
	if err != nil || len(segments) != 2 {
		t.Fatalf("history segments = %d, %v", len(segments), err)
	}
	if err := os.Remove(segments[len(segments)-1].path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrHistoryGap) {
		t.Fatalf("Open with missing tail history segment = %v, want ErrHistoryGap", err)
	}
}

func TestRecoveryAcceptsWALThatBridgesHistoryAcrossMain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	commitPut(t, db, "one", "first")
	walThroughFirst, err := os.ReadFile(databaseWALPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "two", "second")
	walAfterFirst, err := os.ReadFile(databaseWALPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	segments, err := listHistorySegments(databaseHistoryPath(path))
	if err != nil || len(segments) != 1 {
		t.Fatalf("history segments = %d, %v", len(segments), err)
	}
	if err := os.Remove(segments[0].path); err != nil {
		t.Fatal(err)
	}
	bridgingWAL := append(append([]byte(nil), walThroughFirst...), walAfterFirst[walHeaderSize:]...)
	if err := os.WriteFile(databaseWALPath(path), bridgingWAL, 0o600); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if transaction, err := db.LastTransaction(); err != nil || transaction != 2 {
		t.Fatalf("recovered LastTransaction = (%d, %v), want (2, nil)", transaction, err)
	}
	events := collectHistory(t, db, 0)
	if len(events) != 2 || events[0].Transaction != 1 || events[1].Transaction != 2 {
		t.Fatalf("bridged history transactions = %v, want [1 2]", historyTransactions(events))
	}
}

func TestRecoverySealsStaleWALBeforeCompletingRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	commitPut(t, db, "key", "value")
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
	segments, err := listHistorySegments(databaseHistoryPath(path))
	if err != nil || len(segments) != 1 {
		t.Fatalf("history segments = %d, %v", len(segments), err)
	}
	if err := os.Remove(segments[0].path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databaseWALPath(path), staleWAL, 0o600); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.HistorySegments != 1 || stats.WALBaseTransaction != 1 {
		t.Fatalf("recovered history/WAL base = %d/%d, want 1/1", stats.HistorySegments, stats.WALBaseTransaction)
	}
	events := collectHistory(t, db, 0)
	if len(events) != 1 || events[0].Transaction != 1 {
		t.Fatalf("recovered history transactions = %v, want [1]", historyTransactions(events))
	}
}

func mustOpenWithHistory(t *testing.T, path string) *DB {
	t.Helper()
	db, err := OpenWithOptions(path, OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatalf("OpenWithOptions(%q, RetainHistory): %v", path, err)
	}
	return db
}

func collectHistory(t *testing.T, db *DB, after uint64) []CommitEvent {
	t.Helper()
	events := make([]CommitEvent, 0)
	_, err := db.WalkHistory(context.Background(), after, func(event CommitEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkHistory(after %d): %v", after, err)
	}
	return events
}

func assertHistoryOperation(t *testing.T, event CommitEvent, transaction uint64, index int, kind CommitOperationKind, key, value string) {
	t.Helper()
	if event.Transaction != transaction {
		t.Fatalf("event transaction = %d, want %d", event.Transaction, transaction)
	}
	if len(event.Operations) <= index {
		t.Fatalf("transaction %d operations = %d, need index %d", transaction, len(event.Operations), index)
	}
	operation := event.Operations[index]
	if operation.Kind != kind || string(operation.Key) != key || string(operation.Value) != value {
		t.Fatalf("transaction %d operation %d = kind %d key %q value %q", transaction, index, operation.Kind, operation.Key, operation.Value)
	}
}

func historyTransactions(events []CommitEvent) []uint64 {
	transactions := make([]uint64, len(events))
	for index, event := range events {
		transactions[index] = event.Transaction
	}
	return transactions
}

type historyCloseFailWAL struct {
	commitWAL
}

func (wal *historyCloseFailWAL) Close() error {
	if err := wal.commitWAL.Close(); err != nil {
		return err
	}
	return errors.New("injected WAL close failure")
}
