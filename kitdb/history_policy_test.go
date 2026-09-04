package kitdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryRetentionByBytesPrunesOldestWholeSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	defer db.Close()
	for transaction := 1; transaction <= 3; transaction++ {
		commitPut(t, db, string(rune('a'+transaction-1)), "value")
		if _, err := db.Checkpoint(); err != nil {
			t.Fatalf("Checkpoint %d: %v", transaction, err)
		}
	}
	segments, err := listHistorySegments(databaseHistoryPath(path))
	if err != nil || len(segments) != 3 {
		t.Fatalf("history segments = (%d, %v), want 3", len(segments), err)
	}
	keepBytes := segments[1].bytes + segments[2].bytes
	result, err := db.EnforceHistoryRetention(
		context.Background(), HistoryRetentionPolicy{MaxBytes: keepBytes}, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.DesiredSegments != 1 || result.AppliedSegments != 1 ||
		result.AppliedThrough != 1 || result.Prune.PrunedSegments != 1 ||
		result.RetainedSegments != 2 || result.RetainedBytes != keepBytes ||
		result.BytesOverLimit != 0 || result.LimitedByPin {
		t.Fatalf("byte retention result = %#v", result)
	}
	remaining := collectHistory(t, db, 1)
	if got := historyTransactions(remaining); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("remaining history = %v, want [2 3]", got)
	}
}

func TestHistoryRetentionByAgeUsesDurableCommitTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	defer db.Close()
	for transaction := 1; transaction <= 2; transaction++ {
		commitPut(t, db, string(rune('a'+transaction-1)), "value")
		if _, err := db.Checkpoint(); err != nil {
			t.Fatalf("Checkpoint %d: %v", transaction, err)
		}
	}
	now := time.Now().Add(2 * time.Hour)
	result, err := db.EnforceHistoryRetention(
		context.Background(), HistoryRetentionPolicy{MaxAge: time.Hour}, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgeCutoff.IsZero() || result.DesiredSegments != 2 ||
		result.AppliedSegments != 2 || result.AppliedThrough != 2 ||
		result.RetainedSegments != 0 || result.Prune.PrunedSegments != 2 {
		t.Fatalf("age retention result = %#v", result)
	}
}

func TestHistoryRetentionPinWinsAndReportsPressure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpenWithHistory(t, path)
	defer db.Close()
	var firstCursor HistoryCursor
	for transaction := 1; transaction <= 3; transaction++ {
		commitPut(t, db, string(rune('a'+transaction-1)), "value")
		cursor, err := db.CurrentCursor()
		if err != nil {
			t.Fatal(err)
		}
		if transaction == 1 {
			firstCursor = cursor
		}
		if _, err := db.Checkpoint(); err != nil {
			t.Fatalf("Checkpoint %d: %v", transaction, err)
		}
	}
	if _, err := db.SetHistoryPin(context.Background(), "projection/search", firstCursor); err != nil {
		t.Fatal(err)
	}
	result, err := db.EnforceHistoryRetention(
		context.Background(), HistoryRetentionPolicy{MaxBytes: 1}, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.LimitedByPin || result.PinnedBy != "projection/search" ||
		result.PinnedAt != 1 || result.DesiredSegments != 3 ||
		result.AppliedSegments != 1 || result.AppliedThrough != 1 ||
		result.Prune.PrunedSegments != 1 || result.RetainedSegments != 2 ||
		result.BytesOverLimit <= 0 {
		t.Fatalf("pin-limited retention result = %#v", result)
	}
}

func TestHistoryRetentionPolicyRequiresExplicitHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	if _, err := OpenWithOptions(path, OpenOptions{
		HistoryRetention: HistoryRetentionPolicy{MaxAge: time.Hour},
	}); !errors.Is(err, ErrHistoryRetentionPolicy) {
		t.Fatalf("OpenWithOptions without history = %v, want ErrHistoryRetentionPolicy", err)
	}
	if _, err := OpenWithOptions(path, OpenOptions{
		RetainHistory:    true,
		HistoryRetention: HistoryRetentionPolicy{MaxAge: -time.Second},
	}); !errors.Is(err, ErrHistoryRetentionPolicy) {
		t.Fatalf("OpenWithOptions negative age = %v, want ErrHistoryRetentionPolicy", err)
	}
}
