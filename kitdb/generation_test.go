package kitdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIncrementalCheckpointAppendsOnlyDeltaSegment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	seedRows(t, db, 10_000, 256)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	baseInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	baseSize := baseInfo.Size()
	if db.main.generation != 1 || len(db.main.segments) != 1 {
		t.Fatalf("base generation/segments = %d/%d", db.main.generation, len(db.main.segments))
	}

	commitPut(t, db, rowKey(5000), "updated")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	deltaInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	growth := deltaInfo.Size() - baseSize
	if growth <= 0 || growth >= baseSize/20 {
		t.Fatalf("one-row delta grew file by %d bytes from %d", growth, baseSize)
	}
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.MainGeneration != 2 || stats.MainSegments != 2 || stats.MainRecords != 10_000 || stats.MainMutations != 10_001 {
		t.Fatalf("incremental Stats = %+v", stats)
	}
	if got := requireValue(t, db, rowKey(5000)); string(got) != "updated" {
		t.Fatalf("updated row = %q", got)
	}
	if got := requireValue(t, db, rowKey(4999)); len(got) != 256 {
		t.Fatalf("stable row length = %d", len(got))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	if got := requireValue(t, db, rowKey(5000)); string(got) != "updated" {
		t.Fatalf("reopened updated row = %q", got)
	}
}

func TestGenerationNewestMutationWinsAndWalkIsLogical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	seedNamedRows(t, db, map[string]string{"a": "a1", "b": "b1", "d": "d1"})
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, db)
	if err := tx.Delete([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("b"), []byte("b2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("c"), []byte("c2")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "a", "a3")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	var rows []string
	if err := db.Walk(func(key, value []byte) error {
		rows = append(rows, fmt.Sprintf("%s=%s", key, value))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"a=a3", "b=b2", "c=c2", "d=d1"}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("Walk = %v, want %v", rows, want)
	}
	if db.main.records != 4 || len(db.main.segments) != 3 {
		t.Fatalf("logical records/segments = %d/%d", db.main.records, len(db.main.segments))
	}
	if err := db.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	if got := requireValue(t, db, "a"); string(got) != "a3" {
		t.Fatalf("reopened newest value = %q", got)
	}
}

func TestGenerationOpenIgnoresAbandonedTailAndNextCheckpointTruncatesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "stable", "one")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	activeEnd := db.main.fileEnd
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	junk := bytes.Repeat([]byte("abandoned-tail"), 64)
	if _, err := file.Write(junk); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	if db.main.fileEnd != activeEnd {
		t.Fatalf("active boundary = %d, want %d", db.main.fileEnd, activeEnd)
	}
	if got := requireValue(t, db, "stable"); string(got) != "one" {
		t.Fatalf("stable value = %q", got)
	}
	commitPut(t, db, "next", "two")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != db.main.fileEnd {
		t.Fatalf("physical/active file end = %d/%d", info.Size(), db.main.fileEnd)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTornNewerGenerationSlotFallsBackAndReplaysWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "value", "one")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "value", "two")

	db.commitMu.Lock()
	db.mu.RLock()
	logicalRecords, err := countMergedRecords(db.main, db.overlay)
	if err == nil {
		_, err = appendIncrementalGeneration(db.path, db.main, db.overlay, db.lastTx, db.walChecksum, logicalRecords)
	}
	tornSlot := 1 - db.main.activeSlot
	db.mu.RUnlock()
	db.commitMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	zero := make([]byte, 16)
	if _, err := file.WriteAt(zero, int64(generationSlotAOffset+tornSlot*generationSlotSize)); err != nil {
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
	if db.main.generation != 1 {
		t.Fatalf("fallback generation = %d, want 1", db.main.generation)
	}
	if last, err := db.LastTransaction(); err != nil || last != 2 {
		t.Fatalf("replayed transaction = %d, %v", last, err)
	}
	if got := requireValue(t, db, "value"); string(got) != "two" {
		t.Fatalf("WAL-replayed value = %q", got)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if db.main.generation != 2 {
		t.Fatalf("republished generation = %d, want 2", db.main.generation)
	}
}

func TestGenerationManifestCorruptionIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "key", "value")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	activeSlot := db.main.activeSlot
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	slotOffset := generationSlotAOffset + activeSlot*generationSlotSize
	manifestOffset := binary.LittleEndian.Uint64(data[slotOffset+56 : slotOffset+64])
	data[manifestOffset] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(path); !errors.Is(err, ErrCorrupt) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("Open with corrupted manifest error = %v, want ErrCorrupt", err)
	}
}

func TestGenerationSegmentCountTriggersBoundedCompaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	for generation := 1; generation <= maxMainSegments; generation++ {
		commitPut(t, db, "counter", fmt.Sprintf("%d", generation))
		if _, err := db.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	if len(db.main.segments) != maxMainSegments {
		t.Fatalf("segments before compaction = %d", len(db.main.segments))
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, db)
	if err := tx.Delete([]byte("counter")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(db.main.segments) != 0 || db.main.generation != maxMainSegments+1 || db.main.records != 0 {
		t.Fatalf("post-compaction segments/generation/records = %d/%d/%d", len(db.main.segments), db.main.generation, db.main.records)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("compaction size = %d, want below %d", after.Size(), before.Size())
	}
	if _, found, err := db.Get([]byte("counter")); err != nil || found {
		t.Fatalf("compacted deleted value = found %v, error %v", found, err)
	}
	commitPut(t, db, "counter", "reborn")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	if got := requireValue(t, db, "counter"); string(got) != "reborn" {
		t.Fatalf("reopened compacted value = %q", got)
	}
}
