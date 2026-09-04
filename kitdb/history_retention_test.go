package kitdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryPinsPersistAndProtectWholeSegmentPruning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	for transaction := 1; transaction <= 3; transaction++ {
		commitPut(t, db, string(rune('a'+transaction-1)), "value")
		if _, err := db.Checkpoint(); err != nil {
			t.Fatalf("Checkpoint %d: %v", transaction, err)
		}
	}
	rangeInfo, events := collectHistoryRange(t, db, 0)
	if rangeInfo.LastTransaction != 3 || rangeInfo.LastChecksum != events[2].Checksum {
		t.Fatalf("history range = %+v, events = %+v", rangeInfo, events)
	}
	cursorTwo := HistoryCursor{DatabaseID: db.ID(), Transaction: 2, Checksum: events[1].Checksum}
	pin, err := db.SetHistoryPin(context.Background(), "replica/eu", cursorTwo)
	if err != nil || pin.Cursor != cursorTwo {
		t.Fatalf("SetHistoryPin = (%+v, %v)", pin, err)
	}
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.HistoryPins != 1 || stats.HistoryOldestPinTransaction != 2 {
		t.Fatalf("history pin stats = %d/%d, want 1/2", stats.HistoryPins, stats.HistoryOldestPinTransaction)
	}
	if _, err := db.PruneHistory(context.Background(), 3); !errors.Is(err, ErrHistoryPinned) {
		t.Fatalf("PruneHistory through pin = %v, want ErrHistoryPinned", err)
	}
	result, err := db.PruneHistory(context.Background(), 2)
	if err != nil {
		t.Fatalf("PruneHistory through 2: %v", err)
	}
	if result.PreviousBaseTransaction != 0 || result.BaseTransaction != 2 || result.PrunedSegments != 2 || result.CleanupPendingSegments != 0 {
		t.Fatalf("prune result = %+v", result)
	}
	visited := 0
	if _, err := db.WalkHistory(context.Background(), 1, func(CommitEvent) error {
		visited++
		return nil
	}); !errors.Is(err, ErrHistoryGap) || visited != 0 {
		t.Fatalf("WalkHistory before pruned base = (%d, %v)", visited, err)
	}
	remaining := collectHistory(t, db, 2)
	if len(remaining) != 1 || remaining[0].Transaction != 3 {
		t.Fatalf("remaining history = %v, want [3]", historyTransactions(remaining))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	pins, err := db.HistoryPins()
	if err != nil || len(pins) != 1 || pins[0].Name != "replica/eu" || pins[0].Cursor != cursorTwo {
		t.Fatalf("reopened history pins = (%+v, %v)", pins, err)
	}
	cursorThree := HistoryCursor{DatabaseID: db.ID(), Transaction: 3, Checksum: remaining[0].Checksum}
	if _, err := db.SetHistoryPin(context.Background(), "replica/eu", cursorThree); err != nil {
		t.Fatalf("advance history pin: %v", err)
	}
	result, err = db.PruneHistory(context.Background(), 3)
	if err != nil || result.BaseTransaction != 3 || result.PrunedSegments != 1 {
		t.Fatalf("final prune = (%+v, %v)", result, err)
	}
	if err := db.ReleaseHistoryPin(context.Background(), "replica/eu"); err != nil {
		t.Fatalf("ReleaseHistoryPin: %v", err)
	}
	pins, err = db.HistoryPins()
	if err != nil || len(pins) != 0 {
		t.Fatalf("released history pins = (%+v, %v)", pins, err)
	}
}

func TestHistoryPruneDoesNotSplitOneWALSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	defer db.Close()
	for transaction := 1; transaction <= 3; transaction++ {
		commitPut(t, db, string(rune('a'+transaction-1)), "value")
	}
	_, events := collectHistoryRange(t, db, 0)
	if len(events) != 3 {
		t.Fatalf("history events = %d, want 3", len(events))
	}
	cursorTwo := HistoryCursor{DatabaseID: db.ID(), Transaction: 2, Checksum: events[1].Checksum}
	if _, err := db.SetHistoryPin(context.Background(), "analytics", cursorTwo); err != nil {
		t.Fatalf("SetHistoryPin: %v", err)
	}
	result, err := db.PruneHistory(context.Background(), 2)
	if err != nil || result.BaseTransaction != 0 || result.PrunedSegments != 0 {
		t.Fatalf("partial-segment prune = (%+v, %v)", result, err)
	}
	if _, err := db.PruneHistory(context.Background(), 3); !errors.Is(err, ErrHistoryPinned) {
		t.Fatalf("PruneHistory beyond interior pin = %v, want ErrHistoryPinned", err)
	}
	cursorThree := HistoryCursor{DatabaseID: db.ID(), Transaction: 3, Checksum: events[2].Checksum}
	if _, err := db.SetHistoryPin(context.Background(), "analytics", cursorThree); err != nil {
		t.Fatalf("advance history pin: %v", err)
	}
	result, err = db.PruneHistory(context.Background(), 3)
	if err != nil || result.BaseTransaction != 3 || result.PrunedSegments != 1 {
		t.Fatalf("whole-segment prune = (%+v, %v)", result, err)
	}
}

func TestHistoryPruneRecoveryIgnoresRetiredPrefixDebris(t *testing.T) {
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
	_, events := collectHistoryRange(t, db, 0)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	historyPath := databaseHistoryPath(path)
	metadataPath := filepath.Join(historyPath, historyMetadataFilename)
	metadata, err := readHistoryMetadata(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata.baseTx = 1
	metadata.baseChecksum = events[0].Checksum
	if published, err := publishHistoryControlFile(metadataPath, ".test-history-meta-*.tmp", encodeHistoryMetadata(metadata)); err != nil || !published {
		t.Fatalf("publish simulated post-META crash = (%v, %v)", published, err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.HistoryBaseTransaction != 1 || stats.HistorySegments != 1 || stats.HistoryRetiredSegments != 1 {
		t.Fatalf("recovered pruned history stats = base %d active %d retired %d", stats.HistoryBaseTransaction, stats.HistorySegments, stats.HistoryRetiredSegments)
	}
	remaining := collectHistory(t, db, 1)
	if len(remaining) != 1 || remaining[0].Transaction != 2 {
		t.Fatalf("history after simulated crash = %v, want [2]", historyTransactions(remaining))
	}
	result, err := db.PruneHistory(context.Background(), 1)
	if err != nil || result.PreviousBaseTransaction != 1 ||
		result.BaseTransaction != 1 || result.PrunedSegments != 0 ||
		result.CleanupPendingSegments != 0 {
		t.Fatalf("same-boundary cleanup retry = (%+v, %v)", result, err)
	}
	listed, err := listHistorySegments(historyPath)
	if err != nil || len(listed) != 1 || listed[0].first != 2 || listed[0].last != 2 {
		t.Fatalf("history after cleanup retry = (%+v, %v), want active [2]", listed, err)
	}
	result, err = db.PruneHistory(context.Background(), 2)
	if err != nil || result.CleanupPendingSegments != 0 {
		t.Fatalf("cleanup prune = (%+v, %v)", result, err)
	}
	listed, err = listHistorySegments(historyPath)
	if err != nil || len(listed) != 0 {
		t.Fatalf("history debris after cleanup = (%d, %v)", len(listed), err)
	}
}

func TestHistoryRejectsMetadataBaseInsideSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	for transaction := 1; transaction <= 3; transaction++ {
		commitPut(t, db, string(rune('a'+transaction-1)), "value")
	}
	_, events := collectHistoryRange(t, db, 0)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	metadataPath := filepath.Join(databaseHistoryPath(path), historyMetadataFilename)
	metadata, err := readHistoryMetadata(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata.baseTx = 2
	metadata.baseChecksum = events[1].Checksum
	if _, err := publishHistoryControlFile(metadataPath, ".test-history-meta-*.tmp", encodeHistoryMetadata(metadata)); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrHistoryGap) {
		t.Fatalf("Open with straddling history base = %v, want ErrHistoryGap", err)
	}
}

func TestHistoryPruneVerifiesBytesBeforePublishingBase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	defer db.Close()
	commitPut(t, db, "one", "first")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	segments, err := listHistorySegments(databaseHistoryPath(path))
	if err != nil || len(segments) != 1 {
		t.Fatalf("history segments = (%d, %v)", len(segments), err)
	}
	file, err := os.OpenFile(segments[0].path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	offset := segments[0].bytes - frameTrailerSize + 16
	value := []byte{0}
	if _, err := file.ReadAt(value, offset); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	value[0] ^= 0xff
	if _, err := file.WriteAt(value, offset); err != nil {
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
	if _, err := db.PruneHistory(context.Background(), 1); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("PruneHistory corrupt segment = %v, want ErrCorrupt", err)
	}
	metadata, err := readHistoryMetadata(filepath.Join(databaseHistoryPath(path), historyMetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.baseTx != 0 {
		t.Fatalf("history base after rejected prune = %d, want 0", metadata.baseTx)
	}
}

func TestHistoryPinRejectsAnotherDatabaseAndCorruptPins(t *testing.T) {
	firstPath := filepath.Join(t.TempDir(), "first.kitdb")
	first := mustOpenWithHistory(t, firstPath)
	commitPut(t, first, "key", "value")
	firstRange, _ := collectHistoryRange(t, first, 0)
	firstCursor := firstRange.Cursor()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	secondPath := filepath.Join(t.TempDir(), "second.kitdb")
	second := mustOpenWithHistory(t, secondPath)
	commitPut(t, second, "key", "value")
	collectHistoryRange(t, second, 0)
	if _, err := second.SetHistoryPin(context.Background(), "replica", firstCursor); !errors.Is(err, ErrHistoryCursor) {
		t.Fatalf("cross-database SetHistoryPin = %v, want ErrHistoryCursor", err)
	}
	secondRange, _ := collectHistoryRange(t, second, 0)
	if _, err := second.SetHistoryPin(context.Background(), "replica", secondRange.Cursor()); err != nil {
		t.Fatalf("SetHistoryPin: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	pinsPath := filepath.Join(databaseHistoryPath(secondPath), historyPinsFilename)
	file, err := os.OpenFile(pinsPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	last[0] ^= 0xff
	if _, err := file.WriteAt(last, info.Size()-1); err != nil {
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
	if _, err := Open(secondPath); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open with corrupt PINS = %v, want ErrCorrupt", err)
	}
}

func collectHistoryRange(t *testing.T, db *DB, after uint64) (HistoryRange, []CommitEvent) {
	t.Helper()
	events := make([]CommitEvent, 0)
	rangeInfo, err := db.WalkHistory(context.Background(), after, func(event CommitEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkHistory(%d): %v", after, err)
	}
	return rangeInfo, events
}
