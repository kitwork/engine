//go:build kitdb_profile

package kitdb

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestLockProfileBoundsAndOwnership(t *testing.T) {
	db := &DB{}
	span := db.profileRLock(lockSnapshot)
	db.profileRUnlock(&span)
	if len(db.LockProfile().Samples) != 0 {
		t.Fatal("profile enabled implicitly")
	}
	if err := db.EnableLockProfile(); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableLockProfile(); err == nil {
		t.Fatal("profile epoch can be reset")
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 1024 {
				span := db.profileRLock(lockSnapshot)
				span.transaction(7)
				at := span.mark()
				span.phase(0, at)
				db.profileRUnlock(&span)
			}
		})
	}
	workers.Wait()
	report := db.LockProfile()
	if report.Capacity != lockProfileCapacity || len(report.Samples) != lockProfileCapacity || report.Dropped != 8192-lockProfileCapacity {
		t.Fatalf("capacity=%d count=%d dropped=%d", report.Capacity, len(report.Samples), report.Dropped)
	}
	for _, s := range report.Samples {
		if s.Site != "snapshot" || s.Transaction != 7 || s.Start > s.Acquired || s.Acquired > s.Release || s.Release > s.Unlocked || s.Phases[0] < 0 || s.Phases[0] > s.Release-s.Acquired {
			t.Fatalf("invalid interval: %+v", s)
		}
	}
	report.Samples[0].Site = "changed"
	if db.LockProfile().Samples[0].Site != "snapshot" {
		t.Fatal("caller can mutate retained evidence")
	}
	if len((&DB{}).LockProfile().Samples) != 0 {
		t.Fatal("evidence leaked across databases")
	}
}

func TestLockProfileSnapshotCheckpoint(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "profile.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.EnableLockProfile(); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("key"), []byte("first")); err != nil {
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
	reserved, err := db.SnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer reserved.Close()
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Snapshot{snapshot, reserved} {
		value, found, err := s.Get([]byte("key"))
		if err != nil || !found || string(value) != "first" {
			t.Fatalf("snapshot value=%q found=%v error=%v", value, found, err)
		}
	}
	report := db.LockProfile()
	seen := map[string]int{}
	for _, s := range report.Samples {
		seen[s.Site]++
		if s.Acquired > s.Release || s.Release > s.Unlocked {
			t.Fatalf("invalid lock span: %+v", s)
		}
	}
	for _, name := range []string{"snapshot", "snapshot.reserve", "snapshot.capture", "checkpoint.read", "checkpoint.install", "wal.detach", "wal.install"} {
		if seen[name] != 1 {
			t.Fatalf("site %s count=%d", name, seen[name])
		}
	}
	if report.Dropped != 0 {
		t.Fatal("unexpected dropped evidence")
	}
}
