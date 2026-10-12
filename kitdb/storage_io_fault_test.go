//go:build kitdbfault

package kitdb

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestWALIOFaultDrainsCommitResources(t *testing.T) {
	for _, failure := range []struct{ point, kind string }{{"wal-write", "short"}, {"wal-write", "space"}, {"wal-sync", "io"}} {
		t.Run(failure.point+"-"+failure.kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "resources.kitdb")
			db := mustOpen(t, path)
			defer db.Close()
			commitPut(t, db, "confirmed", "stable")
			snapshot, err := db.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			tx := mustBegin(t, db)
			if err := tx.Put([]byte("candidate/a"), []byte("first")); err != nil {
				t.Fatal(err)
			}
			if err := tx.Put([]byte("candidate/b"), []byte("second")); err != nil {
				t.Fatal(err)
			}
			t.Setenv("KITDB_IO_FAULT_PATH", path)
			t.Setenv("KITDB_IO_FAULT_POINT", failure.point)
			t.Setenv("KITDB_IO_FAULT_KIND", failure.kind)
			if _, err := tx.Commit(); !errors.Is(err, ErrDurabilityUncertain) {
				t.Fatalf("commit error=%v", err)
			}
			if err := snapshot.Close(); err != nil {
				t.Fatal(err)
			}
			db.mu.RLock()
			transactions, bytes, frontend, queued := len(db.activeTx), db.activeTxBytes, db.frontendTxBytes, len(db.commitQueue)
			db.mu.RUnlock()
			db.snapshotMu.Lock()
			snapshots, reservations, waiters := len(db.snapshots), len(db.snapshotReservations), len(db.snapshotWaiters)
			snapshotBytes, reservedBytes := db.snapshotBytes, db.snapshotReservedBytes
			db.snapshotMu.Unlock()
			if transactions != 0 || bytes != 0 || frontend != 0 || queued != 0 || snapshots != 0 || reservations != 0 || waiters != 0 || snapshotBytes != 0 || reservedBytes != 0 {
				t.Fatalf("resources retained after error: tx=%d bytes=%d frontend=%d queued=%d snapshots=%d reservations=%d waiters=%d snapshotBytes=%d reservedBytes=%d", transactions, bytes, frontend, queued, snapshots, reservations, waiters, snapshotBytes, reservedBytes)
			}
			if _, err := db.Begin(); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("failed owner accepted transaction: %v", err)
			}
			t.Setenv("KITDB_IO_FAULT_POINT", "")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-db.commitWriterDone:
			default:
				t.Fatal("commit worker did not stop")
			}
			recovered := mustOpen(t, path)
			defer recovered.Close()
			if err := recovered.Verify(); err != nil {
				t.Fatal(err)
			}
			if got := string(requireValue(t, recovered, "confirmed")); got != "stable" {
				t.Fatal("acknowledged value lost")
			}
			_, first, err := recovered.Get([]byte("candidate/a"))
			if err != nil {
				t.Fatal(err)
			}
			_, second, err := recovered.Get([]byte("candidate/b"))
			if err != nil || first != second {
				t.Fatalf("partial transaction: first=%t second=%t err=%v", first, second, err)
			}
			if failure.point == "wal-write" && first {
				t.Fatal("incomplete or rejected WAL frame recovered")
			}
		})
	}
}
