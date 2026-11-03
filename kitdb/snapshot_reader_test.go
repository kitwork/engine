package kitdb

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSnapshotReaderCheckpointHandoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db, err := OpenWithOptions(path, OpenOptions{PageCacheBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	before := bytes.Repeat([]byte("old"), 1024)
	commitPut(t, db, "value", string(before))
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	original := db.main.file
	snapshots := make([]*Snapshot, 8)
	for i := range snapshots {
		s, err := db.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if s.main.file == original {
			t.Fatal("snapshot does not own a distinct file wrapper")
		}
		t.Cleanup(func() { _ = s.Close() })
		snapshots[i] = s
	}
	commitPut(t, db, "value", "new")
	ready := make(chan struct{}, len(snapshots))
	advanced := make(chan struct{})
	var workers sync.WaitGroup
	for _, s := range snapshots {
		workers.Go(func() {
			value, found, err := s.Get([]byte("value"))
			if err != nil || !found || !bytes.Equal(value, before) {
				t.Errorf("before checkpoint value=%d found=%v error=%v", len(value), found, err)
			}
			ready <- struct{}{}
			<-advanced
			for range 64 {
				value, found, err := s.Get([]byte("value"))
				if err != nil || !found || !bytes.Equal(value, before) {
					t.Errorf("after checkpoint value=%d found=%v error=%v", len(value), found, err)
					return
				}
			}
		})
	}
	for range snapshots {
		<-ready
	}
	// Release workers even if checkpoint fails so cleanup cannot strand them.
	_, checkpointErr := db.Checkpoint()
	close(advanced)
	workers.Wait()
	if checkpointErr != nil {
		t.Fatal(checkpointErr)
	}
	if _, err := original.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("old main reader not closed: %v", err)
	}
	for _, s := range snapshots {
		file := s.main.file
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := file.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("snapshot reader not closed: %v", err)
		}
	}
	if got := string(requireValue(t, db, "value")); got != "new" {
		t.Fatal(got)
	}
	stats, err := db.Stats()
	if err != nil || stats.ActiveSnapshots != 0 {
		t.Fatalf("snapshots=%d error=%v", stats.ActiveSnapshots, err)
	}
}

func TestSnapshotReaderCloseErrorReleasesAdmission(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "tenant.kitdb"))
	t.Cleanup(func() { _ = db.Close() })
	s, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.main.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("close error=%v", err)
	}
	stats, err := db.Stats()
	if err != nil || stats.ActiveSnapshots != 0 {
		t.Fatalf("snapshots=%d error=%v", stats.ActiveSnapshots, err)
	}
	if next, err := db.Snapshot(); err != nil {
		t.Fatal(err)
	} else if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}
