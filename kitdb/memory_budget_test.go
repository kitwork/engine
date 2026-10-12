package kitdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func memoryDB(t testing.TB, options OpenOptions) *DB {
	t.Helper()
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "memory.kitdb"), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func memoryPut(t testing.TB, db *DB, key string, value []byte) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := tx.Put([]byte(key), value); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotMemoryBudgetLifecycle(t *testing.T) {
	cost := snapshotBaseCost + snapshotEntryCost + 3 + 1024
	db := memoryDB(t, OpenOptions{SnapshotMemoryBytes: 2*cost + snapshotBaseCost})
	memoryPut(t, db, "key", bytes.Repeat([]byte("x"), 1024))
	first, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := db.Snapshot(); !errors.Is(err, ErrSnapshotMemory) {
		t.Fatalf("budget rejection = %v", err)
	}
	done := make(chan *SnapshotReservation, 1)
	failed := make(chan error, 1)
	go func() {
		r, err := db.ReserveSnapshot(context.Background())
		if err != nil {
			failed <- err
		} else {
			done <- r
		}
	}()
	awaitAdmissionWaiters(t, db, 1)
	// Checkpoint frees current-overlay admission pressure, not the old copies.
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	var reservation *SnapshotReservation
	select {
	case reservation = <-done:
	case err := <-failed:
		t.Fatal(err)
	}
	third, err := reservation.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.SnapshotBytes != 2*cost+snapshotBaseCost || stats.SnapshotReservedBytes != 0 {
		t.Fatalf("charges = %+v", stats)
	}
	value, found, err := first.Get([]byte("key"))
	if err != nil || !found || len(value) != 1024 {
		t.Fatalf("old snapshot: %v/%v/%d", err, found, len(value))
	}
	_ = first.Close()
	_ = second.Close()
	_ = third.Close()
	stats, _ = db.Stats()
	if stats.SnapshotBytes != 0 || stats.SnapshotReservedBytes != 0 || stats.SnapshotWaiters != 0 {
		t.Fatalf("leak = %+v", stats)
	}
}

func TestSnapshotMemoryGrowthAndRecovery(t *testing.T) {
	db := memoryDB(t, OpenOptions{SnapshotMemoryBytes: 1024})
	r, err := db.ReserveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	memoryPut(t, db, "key", bytes.Repeat([]byte("x"), 2048))
	if _, err := r.Snapshot(); !errors.Is(err, ErrSnapshotMemory) {
		t.Fatalf("capture growth = %v", err)
	}
	if _, err := db.ReserveSnapshot(context.Background()); !errors.Is(err, ErrSnapshotMemory) {
		t.Fatalf("oversized overlay = %v", err)
	}
	stats, _ := db.Stats()
	if stats.SnapshotReservedBytes != 0 || stats.SnapshotBytes != 0 {
		t.Fatalf("failed capture leaked = %+v", stats)
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(path, OpenOptions{SnapshotMemoryBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stats, _ = reopened.Stats()
	if stats.SnapshotOverlayEstimateBytes != snapshotOverlayCost(reopened.overlay) {
		t.Fatal("recovery estimate mismatch")
	}
	if _, err := reopened.Snapshot(); !errors.Is(err, ErrSnapshotMemory) {
		t.Fatalf("recovered rejection = %v", err)
	}
	if _, err := reopened.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := reopened.SnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = snapshot.Close()
}

func TestTransactionMemorySharedBudget(t *testing.T) {
	db := memoryDB(t, OpenOptions{TransactionMemoryBytes: 1024})
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.ReserveTransactionMemory(800); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("key"), make([]byte, 300)); !errors.Is(err, ErrActiveTransactionMemory) {
		t.Fatalf("shared rejection = %v", err)
	}
	_ = tx.Rollback()
	snapshot.ReleaseTransactionMemory(600)
	stats, _ := db.Stats()
	if stats.FrontendTransactionBytes != 200 {
		t.Fatalf("release = %+v", stats)
	}
	_ = snapshot.Close()
	stats, _ = db.Stats()
	if stats.FrontendTransactionBytes != 0 || stats.ActiveTransactionBytes != 0 {
		t.Fatalf("leak = %+v", stats)
	}
	snapshot, _ = db.Snapshot()
	_ = snapshot.ReserveTransactionMemory(1024)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if db.frontendTxBytes != 0 || db.snapshotBytes != 0 || db.snapshotReservedBytes != 0 {
		t.Fatal("shutdown charges leaked")
	}
}

func TestSnapshotMemoryEstimateRepeatedKeys(t *testing.T) {
	db := memoryDB(t, OpenOptions{})
	tx, _ := db.Begin()
	_ = tx.Put([]byte("key"), make([]byte, 1000))
	_ = tx.Put([]byte("key"), []byte("small"))
	_ = tx.Delete([]byte("key"))
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := db.snapshotOverlayBytes.Load(); got != snapshotOverlayCost(db.overlay) {
		t.Fatalf("estimate=%d", got)
	}
	for _, options := range []OpenOptions{{SnapshotMemoryBytes: -1}, {TransactionMemoryBytes: -1}} {
		if _, err := OpenWithOptions(filepath.Join(t.TempDir(), "invalid.kitdb"), options); err == nil {
			t.Fatal("negative budget accepted")
		}
	}
}

func BenchmarkSnapshotOverlayMemory(b *testing.B) {
	for _, megabytes := range []int{1, 8} {
		b.Run(fmt.Sprintf("%dMiB", megabytes), func(b *testing.B) {
			db := memoryDB(b, OpenOptions{})
			for i := 0; i < megabytes*256; i++ {
				memoryPut(b, db, fmt.Sprintf("k%06d", i), make([]byte, 4096))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s, err := db.Snapshot()
				if err != nil {
					b.Fatal(err)
				}
				_ = s.Close()
			}
			b.ReportMetric(float64(db.snapshotOverlayBytes.Load()), "charged-bytes/snapshot")
		})
	}
}
