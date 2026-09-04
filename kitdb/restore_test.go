package kitdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreToTransactionFromInsideHistorySegment(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "tenant.kitdb")
	anchorPath := filepath.Join(root, "anchor.kitdb")
	destination := filepath.Join(root, "restored.kitdb")
	db := mustOpenWithHistory(t, sourcePath)
	defer db.Close()

	commitPut(t, db, "product/1", "old")
	commitPut(t, db, "product/2", "remove-me")
	anchor, err := db.CreateBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.Transaction != 2 {
		t.Fatalf("anchor transaction = %d, want 2", anchor.Transaction)
	}

	third := mustBegin(t, db)
	if err := third.Put([]byte("product/1"), []byte("at-three")); err != nil {
		t.Fatal(err)
	}
	if err := third.Delete([]byte("product/2")); err != nil {
		t.Fatal(err)
	}
	if err := third.Put([]byte("product/3"), []byte("created-at-three")); err != nil {
		t.Fatal(err)
	}
	if transaction, err := third.Commit(); err != nil || transaction != 3 {
		t.Fatalf("third Commit = (%d, %v), want (3, nil)", transaction, err)
	}
	commitPut(t, db, "product/1", "too-new")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	result, err := RestoreToTransaction(
		context.Background(), anchorPath, databaseHistoryPath(sourcePath), destination, 3,
	)
	if err != nil {
		t.Fatalf("RestoreToTransaction: %v", err)
	}
	if result.Path != destination || result.DatabaseID != db.ID() || result.Transaction != 3 {
		t.Fatalf("restore path/identity/transaction = %q/%q/%d", result.Path, result.DatabaseID, result.Transaction)
	}
	if result.AnchorTransaction != 2 || result.AppliedTransactions != 1 || result.HistorySegments != 1 {
		t.Fatalf("restore anchor/applied/segments = %d/%d/%d, want 2/1/1", result.AnchorTransaction, result.AppliedTransactions, result.HistorySegments)
	}
	if result.Records != 2 || result.BoundaryChecksum == 0 {
		t.Fatalf("restore records/checksum = %d/%08x, want 2/nonzero", result.Records, result.BoundaryChecksum)
	}
	if _, err := os.Stat(databaseWALPath(destination)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("standalone restore WAL stat = %v, want not exist", err)
	}
	if _, err := os.Stat(destination + writerLockSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("standalone restore lock stat = %v, want not exist", err)
	}

	verified, err := VerifyBackupAnchor(context.Background(), destination)
	if err != nil {
		t.Fatalf("verify restored image: %v", err)
	}
	if !sameRestoreImage(verified, result.BackupAnchor) {
		t.Fatalf("verified restore = %+v, want %+v", verified, result.BackupAnchor)
	}

	restored := mustOpen(t, destination)
	defer restored.Close()
	if value := requireValue(t, restored, "product/1"); string(value) != "at-three" {
		t.Fatalf("restored product/1 = %q, want at-three", value)
	}
	if _, found, err := restored.Get([]byte("product/2")); err != nil || found {
		t.Fatalf("restored deleted product found=%v err=%v", found, err)
	}
	if value := requireValue(t, restored, "product/3"); string(value) != "created-at-three" {
		t.Fatalf("restored product/3 = %q", value)
	}
	if value := requireValue(t, db, "product/1"); string(value) != "too-new" {
		t.Fatalf("source product/1 = %q, want too-new", value)
	}
}

func TestRestoreToTransactionAcrossHistorySegments(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "tenant.kitdb")
	anchorPath := filepath.Join(root, "anchor.kitdb")
	destination := filepath.Join(root, "restored.kitdb")
	db := mustOpenWithHistory(t, sourcePath)

	commitPut(t, db, "value", "one")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	anchor, err := db.CreateBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "value", "two")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	third := mustBegin(t, db)
	if err := third.Delete([]byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := third.Put([]byte("final"), []byte("three")); err != nil {
		t.Fatal(err)
	}
	if transaction, err := third.Commit(); err != nil || transaction != 3 {
		t.Fatalf("third Commit = (%d, %v), want (3, nil)", transaction, err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := RestoreToTransaction(
		context.Background(), anchorPath, databaseHistoryPath(sourcePath), destination, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.AnchorTransaction != anchor.Transaction || result.HistorySegments != 2 || result.AppliedTransactions != 2 {
		t.Fatalf("restore result = %+v, want two applied history segments", result)
	}
	restored := mustOpen(t, destination)
	defer restored.Close()
	if _, found, err := restored.Get([]byte("value")); err != nil || found {
		t.Fatalf("deleted value found=%v err=%v", found, err)
	}
	if value := requireValue(t, restored, "final"); string(value) != "three" {
		t.Fatalf("final value = %q", value)
	}
}

func TestRestoreCompactsAnAnchorAtTheSegmentBound(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "tenant.kitdb")
	anchorPath := filepath.Join(root, "segment-bound.kitdb")
	destination := filepath.Join(root, "restored.kitdb")
	db := mustOpenWithHistory(t, sourcePath)

	for transaction := uint64(1); transaction <= maxMainSegments; transaction++ {
		commitPut(t, db, "value", string(rune('a'+transaction%26)))
		if checkpoint, err := db.Checkpoint(); err != nil || checkpoint != transaction {
			t.Fatalf("Checkpoint %d = (%d, %v)", transaction, checkpoint, err)
		}
	}
	db.mu.RLock()
	segments := len(db.main.segments)
	generation := db.main.generation
	db.mu.RUnlock()
	if segments != maxMainSegments || generation != maxMainSegments {
		t.Fatalf("source segments/generation = %d/%d, want %d/%d", segments, generation, maxMainSegments, maxMainSegments)
	}
	anchorBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(anchorPath, anchorBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	anchor, err := VerifyBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.Transaction != maxMainSegments || anchor.Generation != maxMainSegments {
		t.Fatalf("segment-bound anchor = %+v", anchor)
	}

	commitPut(t, db, "value", "after-bound")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := RestoreToTransaction(
		context.Background(), anchorPath, databaseHistoryPath(sourcePath), destination, maxMainSegments+1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.HistorySegments != 1 || result.Generation != maxMainSegments+1 {
		t.Fatalf("compacted restore result = %+v", result)
	}
	main, err := readMainSnapshotWithCache(destination, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer main.close()
	if len(main.segments) != 1 {
		t.Fatalf("restored active segments = %d, want 1", len(main.segments))
	}
	value, found, err := main.get([]byte("value"))
	if err != nil || !found || string(value) != "after-bound" {
		t.Fatalf("restored value = %q found=%v err=%v", value, found, err)
	}
}

func TestRestoreCopiesAnchorWithoutHistoryAndRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	db := mustOpen(t, filepath.Join(root, "tenant.kitdb"))
	defer db.Close()
	commitPut(t, db, "key", "value")
	anchorPath := filepath.Join(root, "anchor.kitdb")
	anchor, err := db.CreateBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(root, "copy.kitdb")
	result, err := RestoreToTransaction(context.Background(), anchorPath, "", destination, anchor.Transaction)
	if err != nil {
		t.Fatal(err)
	}
	if result.AppliedTransactions != 0 || result.HistorySegments != 0 || result.SHA256 != anchor.SHA256 {
		t.Fatalf("anchor-only restore = %+v, want exact anchor bytes", result)
	}

	existing := filepath.Join(root, "existing.kitdb")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreToTransaction(context.Background(), anchorPath, "", existing, anchor.Transaction); !errors.Is(err, ErrRestoreExists) {
		t.Fatalf("existing restore error = %v, want ErrRestoreExists", err)
	}
	contents, err := os.ReadFile(existing)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("existing destination = %q, %v", contents, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	canceledPath := filepath.Join(root, "canceled.kitdb")
	if _, err := RestoreToTransaction(canceled, anchorPath, "", canceledPath, anchor.Transaction); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(canceledPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled destination stat = %v, want not exist", err)
	}
	temporary, err := filepath.Glob(filepath.Join(root, ".kitdb-restore-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("restore left temporary files: %v", temporary)
	}
}

func TestRestoreRejectsHistoryGapWrongIdentityAndCorruption(t *testing.T) {
	makeSource := func(t *testing.T) (string, string, string) {
		t.Helper()
		root := t.TempDir()
		sourcePath := filepath.Join(root, "tenant.kitdb")
		anchorPath := filepath.Join(root, "anchor.kitdb")
		db := mustOpenWithHistory(t, sourcePath)
		commitPut(t, db, "key", "one")
		if _, err := db.CreateBackupAnchor(context.Background(), anchorPath); err != nil {
			t.Fatal(err)
		}
		commitPut(t, db, "key", "two")
		if _, err := db.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		return root, sourcePath, anchorPath
	}

	t.Run("gap", func(t *testing.T) {
		root, sourcePath, anchorPath := makeSource(t)
		segments, err := listHistorySegments(databaseHistoryPath(sourcePath))
		if err != nil || len(segments) != 1 {
			t.Fatalf("history segments = %d, %v", len(segments), err)
		}
		if err := os.Remove(segments[0].path); err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, "gap.kitdb")
		if _, err := RestoreToTransaction(context.Background(), anchorPath, databaseHistoryPath(sourcePath), destination, 2); !errors.Is(err, ErrHistoryGap) {
			t.Fatalf("history gap error = %v, want ErrHistoryGap", err)
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("gap destination stat = %v, want not exist", err)
		}
	})

	t.Run("wrong identity", func(t *testing.T) {
		root, _, anchorPath := makeSource(t)
		otherPath := filepath.Join(root, "other.kitdb")
		other := mustOpenWithHistory(t, otherPath)
		commitPut(t, other, "other", "one")
		commitPut(t, other, "other", "two")
		if _, err := other.Checkpoint(); err != nil {
			t.Fatal(err)
		}
		if err := other.Close(); err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, "wrong.kitdb")
		if _, err := RestoreToTransaction(context.Background(), anchorPath, databaseHistoryPath(otherPath), destination, 2); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("wrong identity error = %v, want ErrCorrupt", err)
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("wrong identity destination stat = %v, want not exist", err)
		}
	})

	t.Run("corrupt chunk", func(t *testing.T) {
		root, sourcePath, anchorPath := makeSource(t)
		segments, err := listHistorySegments(databaseHistoryPath(sourcePath))
		if err != nil || len(segments) != 1 {
			t.Fatalf("history segments = %d, %v", len(segments), err)
		}
		flipFileByte(t, segments[0].path, segments[0].bytes-1)
		destination := filepath.Join(root, "corrupt.kitdb")
		if _, err := RestoreToTransaction(context.Background(), anchorPath, databaseHistoryPath(sourcePath), destination, 2); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("corrupt history error = %v, want ErrCorrupt", err)
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("corrupt destination stat = %v, want not exist", err)
		}
	})
}
