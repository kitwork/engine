package kitdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func fullCheckpointFixture(t *testing.T) *DB {
	t.Helper()
	db := mustOpen(t, filepath.Join(t.TempDir(), "compact.kitdb"))
	t.Cleanup(func() { _ = db.Close() })
	for range maxMainSegments {
		commitPut(t, db, "key", "value")
		if _, err := db.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	commitPut(t, db, "key", "pending")
	return db
}

func awaitCheckpointPause(t *testing.T, db *DB) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		db.snapshotMu.Lock()
		paused := db.snapshotPauseDone != nil
		db.snapshotMu.Unlock()
		if paused {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("checkpoint did not pause admission")
}

func TestCheckpointContextDrainsReservationsAndQueuesReaders(t *testing.T) {
	db := fullCheckpointFixture(t)
	r, err := db.ReserveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	finished := make(chan error, 1)
	go func() { _, err := db.CheckpointContext(context.Background()); finished <- err }()
	awaitCheckpointPause(t, db)
	if _, err := db.Snapshot(); !errors.Is(err, ErrSnapshotsActive) {
		t.Fatalf("nonblocking snapshot during compaction: %v", err)
	}
	reader := make(chan error, 1)
	go func() {
		s, err := db.SnapshotContext(context.Background())
		if err == nil {
			_, _, err = s.Get([]byte("key"))
			_ = s.Close()
		}
		reader <- err
	}()
	awaitAdmissionWaiters(t, db, 1)
	s, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if value, found, err := s.Get([]byte("key")); err != nil || !found || string(value) != "pending" {
		t.Fatalf("existing reservation invalidated: %q %v %v", value, found, err)
	}
	// A writer with an existing snapshot must still be able to finish.
	commitPut(t, db, "later", "committed")
	select {
	case err := <-finished:
		t.Fatalf("checkpoint skipped existing reader: %v", err)
	default:
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-reader; err != nil {
		t.Fatal(err)
	}
	if len(db.main.segments) != 1 || db.checkpointTx != db.lastTx {
		t.Fatal("full checkpoint did not publish the latest commit")
	}
}

func TestCheckpointContextCancellationReopensAdmission(t *testing.T) {
	db := fullCheckpointFixture(t)
	s, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := db.CheckpointContext(ctx); finished <- err }()
	awaitCheckpointPause(t, db)
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	next, err := db.SnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = next.Close()
	if _, found, err := s.Get([]byte("key")); !found || err != nil {
		t.Fatalf("cancellation invalidated old reader: %v", err)
	}
	if _, err := db.Checkpoint(); !errors.Is(err, ErrSnapshotsActive) {
		t.Fatalf("legacy checkpoint behavior changed: %v", err)
	}
}

func TestCheckpointContextCloseWakesDrain(t *testing.T) {
	db := fullCheckpointFixture(t)
	s, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	finished := make(chan error, 1)
	go func() { _, err := db.CheckpointContext(context.Background()); finished <- err }()
	awaitCheckpointPause(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestCheckpointContextDeadlineAndConcurrentOwner(t *testing.T) {
	db := fullCheckpointFixture(t)
	r, err := db.ReserveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := db.CheckpointContext(ctx); finished <- err }()
	awaitCheckpointPause(t, db)
	short, stop := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer stop()
	if _, err := db.CheckpointContext(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second owner ignored deadline: %v", err)
	}
	db.snapshotMu.Lock()
	stillPaused := db.snapshotPauseDone != nil
	db.snapshotMu.Unlock()
	if !stillPaused {
		t.Fatal("timed-out contender resumed another owner's admission")
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_ = r.Close()
	if _, err := db.CheckpointContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointContextBoundsLongReader(t *testing.T) {
	db := fullCheckpointFixture(t)
	s, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	started := time.Now()
	if _, err := db.CheckpointContext(context.Background()); !errors.Is(err, ErrSnapshotsActive) {
		t.Fatalf("long reader error: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("unbounded reader drain: %v", elapsed)
	}
	next, err := db.SnapshotContext(context.Background())
	if err != nil {
		t.Fatalf("timed-out drain left admission paused: %v", err)
	}
	_ = next.Close()
}

func TestCheckpointContextCommitLockDeadline(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "lock.kitdb"))
	defer db.Close()
	db.commitMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := db.CheckpointContext(ctx)
	db.commitMu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("commit-lock wait ignored context: %v", err)
	}
}
