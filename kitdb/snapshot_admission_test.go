package kitdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func admissionFixture(t *testing.T) (*DB, []*SnapshotReservation) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "admission.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var reservations []*SnapshotReservation
	for range maxActiveSnapshots {
		r, err := db.ReserveSnapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		reservations = append(reservations, r)
		t.Cleanup(func() { _ = r.Close() })
	}
	return db, reservations
}

func awaitAdmissionWaiters(t *testing.T, db *DB, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		db.snapshotMu.Lock()
		n := len(db.snapshotWaiters)
		db.snapshotMu.Unlock()
		if n == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("admission queue did not reach %d", count)
}

func TestSnapshotAdmissionFIFOAndCancellation(t *testing.T) {
	db, held := admissionFixture(t)
	if _, err := db.Snapshot(); !errors.Is(err, ErrTooManySnapshots) {
		t.Fatalf("legacy snapshot ignored reservations: %v", err)
	}
	stats, err := db.Stats()
	if err != nil || stats.ActiveSnapshots != 0 || stats.SnapshotReservations != maxActiveSnapshots {
		t.Fatalf("capacity allocated a snapshot: %+v %v", stats, err)
	}
	type result struct {
		r   *SnapshotReservation
		err error
	}
	results := []chan result{make(chan result, 1), make(chan result, 1), make(chan result, 1)}
	contexts := []context.Context{context.Background(), nil, context.Background()}
	var cancel context.CancelFunc
	contexts[1], cancel = context.WithCancel(context.Background())
	defer cancel()
	for i := range results {
		go func(i int) { r, err := db.ReserveSnapshot(contexts[i]); results[i] <- result{r, err} }(i)
		awaitAdmissionWaiters(t, db, i+1)
	}
	cancel()
	if result := <-results[1]; !errors.Is(result.err, context.Canceled) {
		t.Fatal(result.err)
	}
	awaitAdmissionWaiters(t, db, 2)
	_ = held[0].Close()
	first := <-results[0]
	if first.err != nil {
		t.Fatal(first.err)
	}
	select {
	case r := <-results[2]:
		_ = r.r.Close()
		t.Fatal("FIFO slot stolen")
	default:
	}
	snapshot, err := first.r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.r.Snapshot(); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatalf("reservation reused: %v", err)
	}
	_ = snapshot.Close()
	last := <-results[2]
	if last.err != nil {
		t.Fatal(last.err)
	}
	_ = last.r.Close()
	awaitAdmissionWaiters(t, db, 0)
}

func TestSnapshotAdmissionQueueBound(t *testing.T) {
	db, _ := admissionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, maximumSnapshotWaiters)
	for range maximumSnapshotWaiters {
		go func() {
			r, err := db.ReserveSnapshot(ctx)
			if r != nil {
				_ = r.Close()
			}
			results <- err
		}()
	}
	awaitAdmissionWaiters(t, db, maximumSnapshotWaiters)
	if _, err := db.ReserveSnapshot(context.Background()); !errors.Is(err, ErrSnapshotAdmissionFull) {
		t.Fatalf("queue overflow: %v", err)
	}
	cancel()
	for range maximumSnapshotWaiters {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	awaitAdmissionWaiters(t, db, 0)
}

func TestSnapshotAdmissionTimeoutAndClose(t *testing.T) {
	db, _ := admissionFixture(t)
	if _, err := db.ReserveSnapshot(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.ReserveSnapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := db.ReserveSnapshot(context.Background()); !errors.Is(err, ErrSnapshotAdmissionTimeout) {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < snapshotAdmissionWait || elapsed > snapshotAdmissionWait+time.Second {
		t.Fatalf("wait=%v", elapsed)
	}
	awaitAdmissionWaiters(t, db, 0)
	result := make(chan error, 1)
	go func() { _, err := db.ReserveSnapshot(context.Background()); result <- err }()
	awaitAdmissionWaiters(t, db, 1)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	db.snapshotMu.Lock()
	defer db.snapshotMu.Unlock()
	if len(db.snapshotReservations) != 0 || len(db.snapshotWaiters) != 0 {
		t.Fatal("close retained admission state")
	}
}

func TestSnapshotAdmissionGrantCancelRace(t *testing.T) {
	db, held := admissionFixture(t)
	for range 100 {
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			r, err := db.ReserveSnapshot(ctx)
			if r != nil {
				_ = r.Close()
			}
			result <- err
		}()
		awaitAdmissionWaiters(t, db, 1)
		go cancel()
		_ = held[0].Close()
		if err := <-result; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		cancel()
		r, err := db.ReserveSnapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		held[0] = r
	}
	for _, r := range held {
		_ = r.Close()
	}
	stats, err := db.Stats()
	if err != nil || stats.SnapshotReservations != 0 || stats.SnapshotWaiters != 0 {
		t.Fatalf("handoff leaked: %+v %v", stats, err)
	}
	snapshot, err := db.SnapshotContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = snapshot.Close()
}
