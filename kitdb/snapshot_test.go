package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestSnapshotRemainsConsistentAcrossCommitAndCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	defer db.Close()

	tx := mustBegin(t, db)
	if err := tx.Put([]byte("product/1"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("product/2"), []byte("keep")); err != nil {
		t.Fatal(err)
	}
	transaction, err := tx.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if snapshot.Transaction() != transaction {
		t.Fatalf("snapshot transaction = %d, want %d", snapshot.Transaction(), transaction)
	}

	tx = mustBegin(t, db)
	if err := tx.Put([]byte("product/1"), []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte("product/2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("product/3"), []byte("added")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	value, found, err := snapshot.Get([]byte("product/1"))
	if err != nil || !found || string(value) != "old" {
		t.Fatalf("snapshot product/1 = (%q, %v, %v), want old", value, found, err)
	}
	value, found, err = snapshot.Get([]byte("product/2"))
	if err != nil || !found || string(value) != "keep" {
		t.Fatalf("snapshot product/2 = (%q, %v, %v), want keep", value, found, err)
	}
	if _, found, err = snapshot.Get([]byte("product/3")); err != nil || found {
		t.Fatalf("snapshot product/3 found = %v, err = %v", found, err)
	}
	if got := string(requireValue(t, db, "product/1")); got != "new" {
		t.Fatalf("live product/1 = %q, want new", got)
	}
	if _, found, err = db.Get([]byte("product/2")); err != nil || found {
		t.Fatalf("live product/2 found = %v, err = %v", found, err)
	}

	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveSnapshots != 1 {
		t.Fatalf("active snapshots = %d, want 1", stats.ActiveSnapshots)
	}
}

func TestSnapshotCursorSeeksAndMergesBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	defer db.Close()

	tx := mustBegin(t, db)
	for index := 0; index < 1_000; index++ {
		key := fmt.Sprintf("item/%04d", index)
		if err := tx.Put([]byte(key), []byte(fmt.Sprintf("v%04d", index))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	tx = mustBegin(t, db)
	if err := tx.Put([]byte("item/0902"), []byte("updated")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte("item/0903")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("item/0903a"), []byte("inserted")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()

	physical := newPhysicalSegmentIteratorFrom(snapshot.main, 0, len(snapshot.main.blocks), []byte("item/0900"))
	if physical.block <= 0 {
		t.Fatalf("seek selected block %d, want a sparse-index block after zero", physical.block)
	}

	cursor, err := snapshot.Cursor(RangeOptions{
		Start: []byte("item/0900"), End: []byte("item/0910"), Prefix: []byte("item/09"), Limit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	var keys, values []string
	for cursor.Next() {
		keys = append(keys, string(cursor.Key()))
		values = append(values, string(cursor.Value()))
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"item/0900", "item/0901", "item/0902", "item/0903a", "item/0904"}
	wantValues := []string{"v0900", "v0901", "updated", "inserted", "v0904"}
	if fmt.Sprint(keys) != fmt.Sprint(wantKeys) {
		t.Fatalf("cursor keys = %v, want %v", keys, wantKeys)
	}
	if fmt.Sprint(values) != fmt.Sprint(wantValues) {
		t.Fatalf("cursor values = %v, want %v", values, wantValues)
	}

	keyCopy := cursor.Key()
	valueCopy := cursor.Value()
	keyCopy[0] = 'X'
	valueCopy[0] = 'X'
	if bytes.Equal(keyCopy, cursor.Key()) || bytes.Equal(valueCopy, cursor.Value()) {
		t.Fatal("cursor returned storage aliased to caller-owned bytes")
	}
}

func TestActiveSnapshotPreventsOnlyFullCompaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	defer db.Close()

	for generation := 0; generation < maxMainSegments; generation++ {
		commitPut(t, db, "key", fmt.Sprintf("value-%d", generation))
		if _, err := db.Checkpoint(); err != nil {
			t.Fatalf("checkpoint generation %d: %v", generation, err)
		}
	}
	if len(db.main.segments) != maxMainSegments {
		t.Fatalf("segments = %d, want %d", len(db.main.segments), maxMainSegments)
	}

	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "second", "value")
	if _, err := db.Checkpoint(); !errors.Is(err, ErrSnapshotsActive) {
		t.Fatalf("checkpoint with active snapshot = %v, want ErrSnapshotsActive", err)
	}
	if got := string(requireValue(t, db, "second")); got != "value" {
		t.Fatalf("committed WAL value after blocked compaction = %q", got)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatalf("checkpoint after snapshot close: %v", err)
	}
	if len(db.main.segments) != 1 {
		t.Fatalf("segments after compaction = %d, want 1", len(db.main.segments))
	}
}

func TestSnapshotLimitAndDatabaseClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	commitPut(t, db, "key", "value")

	snapshots := make([]*Snapshot, 0, maxActiveSnapshots)
	for index := 0; index < maxActiveSnapshots; index++ {
		snapshot, err := db.Snapshot()
		if err != nil {
			t.Fatalf("snapshot %d: %v", index, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if _, err := db.Snapshot(); !errors.Is(err, ErrTooManySnapshots) {
		t.Fatalf("snapshot above limit = %v, want ErrTooManySnapshots", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for index, snapshot := range snapshots {
		if _, _, err := snapshot.Get([]byte("key")); !errors.Is(err, ErrSnapshotClosed) {
			t.Fatalf("snapshot %d after DB.Close = %v, want ErrSnapshotClosed", index, err)
		}
	}
}
