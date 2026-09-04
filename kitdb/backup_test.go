package kitdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupAnchorSupportsEmptyDatabase(t *testing.T) {
	root := t.TempDir()
	db := mustOpenWithHistory(t, filepath.Join(root, "empty.kitdb"))
	defer db.Close()
	anchorPath := filepath.Join(root, "empty-anchor.kitdb")

	anchor, err := db.CreateBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatalf("CreateBackupAnchor: %v", err)
	}
	if anchor.Transaction != 0 || anchor.Generation != 0 || anchor.Records != 0 || anchor.Bytes != generationDataOffset {
		t.Fatalf("empty anchor = %+v", anchor)
	}
	verified, err := VerifyBackupAnchor(context.Background(), anchorPath)
	if err != nil || verified != anchor {
		t.Fatalf("VerifyBackupAnchor = (%+v, %v), want %+v", verified, err, anchor)
	}
	opened := mustOpen(t, anchorPath)
	defer opened.Close()
	if cursor, err := opened.CurrentCursor(); err != nil || cursor != anchor.Cursor() {
		t.Fatalf("empty anchor cursor = (%+v, %v), want %+v", cursor, err, anchor.Cursor())
	}
}

func TestBackupAnchorCapturesWALOverlayAndRemainsStandalone(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "tenant.kitdb")
	anchorPath := filepath.Join(root, "anchor.kitdb")
	db := mustOpenWithHistory(t, source)
	defer db.Close()

	if transaction := commitPut(t, db, "product/1", "before-anchor"); transaction != 1 {
		t.Fatalf("first transaction = %d, want 1", transaction)
	}
	before, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if before.CheckpointTransaction != 0 || before.OverlayMutations != 1 {
		t.Fatalf("pre-anchor checkpoint/overlay = %d/%d, want 0/1", before.CheckpointTransaction, before.OverlayMutations)
	}

	anchor, err := db.CreateBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatalf("CreateBackupAnchor: %v", err)
	}
	if anchor.Path != anchorPath || anchor.DatabaseID != db.ID() {
		t.Fatalf("anchor path/identity = %q/%q, want %q/%q", anchor.Path, anchor.DatabaseID, anchorPath, db.ID())
	}
	if anchor.FormatVersion != mainFormatVersion || anchor.Generation != backupAnchorGeneration {
		t.Fatalf("anchor format/generation = %d/%d", anchor.FormatVersion, anchor.Generation)
	}
	if cursor := anchor.Cursor(); cursor.DatabaseID != anchor.DatabaseID || cursor.Transaction != anchor.Transaction || cursor.Checksum != anchor.BoundaryChecksum {
		t.Fatalf("anchor cursor = %+v", cursor)
	}
	if anchor.Transaction != 1 || anchor.Records != 1 || anchor.Bytes <= generationDataOffset {
		t.Fatalf("anchor transaction/records/bytes = %d/%d/%d", anchor.Transaction, anchor.Records, anchor.Bytes)
	}
	if anchor.BoundaryChecksum == 0 || len(anchor.SHA256) != sha256.Size*2 {
		t.Fatalf("anchor checksum/digest = %08x/%q", anchor.BoundaryChecksum, anchor.SHA256)
	}
	after, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if after.CheckpointTransaction != 0 || after.OverlayMutations != 1 || after.ActiveSnapshots != 0 {
		t.Fatalf("anchor changed source checkpoint/overlay/snapshots = %d/%d/%d", after.CheckpointTransaction, after.OverlayMutations, after.ActiveSnapshots)
	}

	verified, err := VerifyBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatalf("VerifyBackupAnchor: %v", err)
	}
	if verified != anchor {
		t.Fatalf("verified anchor = %+v, want %+v", verified, anchor)
	}

	if transaction := commitPut(t, db, "product/2", "after-anchor"); transaction != 2 {
		t.Fatalf("second transaction = %d, want 2", transaction)
	}
	events := collectHistory(t, db, anchor.Transaction)
	if len(events) != 1 || events[0].Transaction != 2 {
		t.Fatalf("history after anchor = %+v, want transaction 2", events)
	}

	restored := mustOpen(t, anchorPath)
	defer restored.Close()
	if restored.ID() != db.ID() {
		t.Fatalf("standalone anchor identity = %q, want %q", restored.ID(), db.ID())
	}
	if last, err := restored.LastTransaction(); err != nil || last != 1 {
		t.Fatalf("standalone anchor transaction = %d, %v; want 1", last, err)
	}
	if value := requireValue(t, restored, "product/1"); string(value) != "before-anchor" {
		t.Fatalf("standalone anchor value = %q", value)
	}
	if _, found, err := restored.Get([]byte("product/2")); err != nil || found {
		t.Fatalf("post-anchor value found=%v err=%v", found, err)
	}
}

func TestBackupAnchorUsesCapturedSnapshotWhileSourceAdvances(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "tenant.kitdb")
	anchorPath := filepath.Join(root, "fixed.kitdb")
	db := mustOpen(t, source)
	defer db.Close()
	commitPut(t, db, "value", "old")

	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	commitPut(t, db, "value", "new")
	commitPut(t, db, "later", "visible-only-in-source")

	anchor, err := createBackupAnchorFromSnapshot(context.Background(), snapshot, anchorPath)
	if err != nil {
		t.Fatalf("createBackupAnchorFromSnapshot: %v", err)
	}
	if anchor.Transaction != 1 || anchor.Records != 1 {
		t.Fatalf("fixed anchor transaction/records = %d/%d, want 1/1", anchor.Transaction, anchor.Records)
	}

	standalone := mustOpen(t, anchorPath)
	defer standalone.Close()
	if value := requireValue(t, standalone, "value"); string(value) != "old" {
		t.Fatalf("captured value = %q, want old", value)
	}
	if _, found, err := standalone.Get([]byte("later")); err != nil || found {
		t.Fatalf("later value found=%v err=%v", found, err)
	}
	if value := requireValue(t, db, "value"); string(value) != "new" {
		t.Fatalf("live value = %q, want new", value)
	}
}

func TestBackupAnchorRefusesOverwriteAndCleansCanceledStaging(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "tenant.kitdb")
	db := mustOpen(t, source)
	defer db.Close()
	for index := 0; index < 20; index++ {
		commitPut(t, db, fmt.Sprintf("key/%02d", index), strings.Repeat("x", 128))
	}

	existing := filepath.Join(root, "existing.kitdb")
	if err := os.WriteFile(existing, []byte("do-not-replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateBackupAnchor(context.Background(), existing); !errors.Is(err, ErrBackupExists) {
		t.Fatalf("existing destination error = %v, want ErrBackupExists", err)
	}
	contents, err := os.ReadFile(existing)
	if err != nil || string(contents) != "do-not-replace" {
		t.Fatalf("existing destination changed to %q, %v", contents, err)
	}

	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	canceledPath := filepath.Join(root, "canceled.kitdb")
	cancelContext := &cancelAfterChecksContext{Context: context.Background(), cancelAt: 6}
	_, cancelErr := createBackupAnchorFromSnapshot(cancelContext, snapshot, canceledPath)
	if !errors.Is(cancelErr, context.Canceled) {
		t.Fatalf("canceled backup error = %v, want context.Canceled", cancelErr)
	}
	if _, err := os.Stat(canceledPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled destination stat = %v, want not exist", err)
	}
	staging, err := filepath.Glob(filepath.Join(root, ".kitdb-main-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(staging) != 0 {
		t.Fatalf("canceled backup left staging files: %v (error: %v)", staging, cancelErr)
	}
}

func TestVerifyBackupAnchorRejectsPageCorruptionAndTrailingBytes(t *testing.T) {
	newAnchor := func(t *testing.T, name string) string {
		t.Helper()
		root := t.TempDir()
		db := mustOpen(t, filepath.Join(root, "tenant.kitdb"))
		t.Cleanup(func() { _ = db.Close() })
		commitPut(t, db, "product/1", strings.Repeat("value", 128))
		path := filepath.Join(root, name)
		if _, err := db.CreateBackupAnchor(context.Background(), path); err != nil {
			t.Fatalf("CreateBackupAnchor: %v", err)
		}
		return path
	}

	t.Run("page corruption", func(t *testing.T) {
		path := newAnchor(t, "corrupt.kitdb")
		main, err := readMainSnapshotWithCache(path, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(main.blocks) == 0 {
			t.Fatal("anchor has no data block")
		}
		offset := main.blocks[0].offset + 8
		if err := main.close(); err != nil {
			t.Fatal(err)
		}
		flipFileByte(t, path, offset)
		if _, err := VerifyBackupAnchor(context.Background(), path); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("corrupt anchor error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("trailing bytes", func(t *testing.T) {
		path := newAnchor(t, "tail.kitdb")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("tail")); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyBackupAnchor(context.Background(), path); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("anchor with tail error = %v, want ErrCorrupt", err)
		}
	})
}

type cancelAfterChecksContext struct {
	context.Context
	checks   int
	cancelAt int
}

func (ctx *cancelAfterChecksContext) Err() error {
	ctx.checks++
	if ctx.checks >= ctx.cancelAt {
		return context.Canceled
	}
	return nil
}

func flipFileByte(t *testing.T, path string, offset int64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var value [1]byte
	if _, err := file.ReadAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	value[0] ^= 0xff
	if _, err := file.WriteAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}
