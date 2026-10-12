package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
)

func pressurePut(t *testing.T, db *kitdb.DB, key string, value []byte) {
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

func TestCheckpointPressureThresholds(t *testing.T) {
	now := time.Now()
	s := kitdb.Stats{OverlayMutations: 1, WALBytes: 200, SnapshotOverlayEstimateBytes: 500, OverlaySince: now.Add(-time.Minute)}
	for _, p := range []CheckpointPressurePolicy{{WALBytes: 200}, {OverlayBytes: 500}, {Mutations: 1}, {MaxAge: time.Minute}} {
		if !p.due(s, now) {
			t.Fatalf("threshold missed: %+v", p)
		}
		empty := s
		empty.OverlayMutations = 0
		if p.due(empty, now) {
			t.Fatal("empty overlay triggered checkpoint")
		}
	}
	for _, p := range []CheckpointPressurePolicy{{}, {OverlayBytes: -1}, {Mutations: 1, MinInterval: -1}, {Mutations: 1, InitialBackoff: time.Hour, MaxBackoff: time.Second}} {
		if _, err := p.normalize(); err == nil {
			t.Fatalf("invalid policy accepted: %+v", p)
		}
	}
	p, _ := (CheckpointPressurePolicy{Mutations: 1}).normalize()
	emptyWAL := kitdb.Stats{LastTransaction: 2, CheckpointTransaction: 1, WALBytes: 200}
	if !(CheckpointPressurePolicy{WALBytes: 200}).due(emptyWAL, now) {
		t.Fatal("uncheckpointed WAL-only state was ignored")
	}
	if (CheckpointPressurePolicy{OverlayBytes: 1}).due(emptyWAL, now) {
		t.Fatal("empty overlay triggered byte pressure")
	}
	if checkpointPressureBackoff(p, 32) != p.MaxBackoff {
		t.Fatal("backoff is not capped")
	}
}

func TestCheckpointPressureCoalescesAndBacksOff(t *testing.T) {
	root := t.TempDir()
	m := newMaintenanceTestManager(t, Limits{})
	path := filepath.Join(root, "pressure.kitdb")
	l, err := m.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	pressurePut(t, l.DB(), "key", make([]byte, 2048))
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int32
	m.maintenanceExecutor = func(ctx context.Context, db *kitdb.DB, kind MaintenanceKind) (uint64, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
			return 0, kitdb.ErrSnapshotsActive
		}
		return runMaintenanceOperation(ctx, db, kind)
	}
	p := CheckpointPressurePolicy{OverlayBytes: 1024, MinInterval: time.Second, InitialBackoff: time.Minute, MaxBackoff: time.Minute}
	ticket, err := m.EvaluateCheckpoint(context.Background(), path, kitdb.OpenOptions{}, p, MaintenanceNormal)
	if err != nil || ticket == nil {
		t.Fatalf("schedule: %v", err)
	}
	<-entered
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := m.EvaluateCheckpoint(context.Background(), path, kitdb.OpenOptions{}, p, MaintenanceNormal)
			if e != nil || r != ticket {
				t.Errorf("pending job not shared: %v", e)
			}
		}()
	}
	wg.Wait()
	if m.Stats().MaintenanceSubmissions != 1 {
		t.Fatal("duplicate jobs queued")
	}
	once.Do(func() { close(release) })
	if _, err := ticket.Wait(context.Background()); !errors.Is(err, kitdb.ErrSnapshotsActive) {
		t.Fatalf("failure=%v", err)
	}
	for range 16 {
		if _, err := m.EvaluateCheckpoint(context.Background(), path, kitdb.OpenOptions{}, p, MaintenanceUrgent); !errors.Is(err, ErrCheckpointBackoff) {
			t.Fatalf("backoff=%v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("failure caused retry storm")
	}
	m.mu.Lock()
	l.entry.checkpointPressure.next = time.Now().Add(-time.Second)
	m.mu.Unlock()
	ticket, err = m.EvaluateCheckpoint(context.Background(), path, kitdb.OpenOptions{}, p, MaintenanceUrgent)
	if err != nil || ticket == nil {
		t.Fatalf("retry=%v", err)
	}
	if _, err = ticket.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, _ := l.DB().Stats()
	if stats.OverlayMutations != 0 || !stats.OverlaySince.IsZero() {
		t.Fatalf("pressure not reset: %+v", stats)
	}
	pressurePut(t, l.DB(), "key", make([]byte, 2048))
	if ticket, err = m.EvaluateCheckpoint(context.Background(), path, kitdb.OpenOptions{}, p, MaintenanceNormal); err != nil || ticket != nil {
		t.Fatalf("cooldown=%v/%v", ticket, err)
	}
}

func TestCheckpointPressureSustainedWritesAndReader(t *testing.T) {
	root := t.TempDir()
	m := newMaintenanceTestManager(t, Limits{})
	path := filepath.Join(root, "soak.kitdb")
	l, err := m.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	pressurePut(t, l.DB(), "old", []byte("original"))
	reader, err := l.DB().Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	p := CheckpointPressurePolicy{Mutations: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	blocked := false
	for i := 0; i < 80; i++ {
		pressurePut(t, l.DB(), fmt.Sprintf("key/%03d", i), make([]byte, 4096))
		ticket, err := m.EvaluateCheckpoint(context.Background(), path, kitdb.OpenOptions{}, p, MaintenanceUrgent)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ticket.Wait(context.Background())
		if errors.Is(err, kitdb.ErrSnapshotsActive) {
			blocked = true
			v, ok, e := reader.Get([]byte("old"))
			if e != nil || !ok || string(v) != "original" {
				t.Fatal("reader changed")
			}
			_ = reader.Close()
			m.mu.Lock()
			l.entry.checkpointPressure.ticket = nil
			l.entry.checkpointPressure.next = time.Time{}
			m.mu.Unlock()
			ticket, err = m.EvaluateCheckpoint(context.Background(), path, kitdb.OpenOptions{}, p, MaintenanceUrgent)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ticket.Wait(context.Background())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = reader.Close()
	if !blocked {
		t.Fatal("did not exercise reader-blocked compaction")
	}
	if err := l.DB().Verify(); err != nil {
		t.Fatal(err)
	}
	_ = l.Release()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := kitdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 80; i++ {
		v, ok, err := db.Get([]byte(fmt.Sprintf("key/%03d", i)))
		if err != nil || !ok || !bytes.Equal(v, make([]byte, 4096)) {
			t.Fatalf("reopen %d: %v", i, err)
		}
	}
}

func TestCheckpointPressureConcurrentWriters(t *testing.T) {
	root := t.TempDir()
	m := newMaintenanceTestManager(t, Limits{})
	path := filepath.Join(root, "concurrent.kitdb")
	l, err := m.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	var writers sync.WaitGroup
	errorsOut := make(chan error, 4)
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 64; i++ {
				tx, err := l.DB().Begin()
				if err != nil {
					errorsOut <- err
					return
				}
				if err = tx.Put([]byte(fmt.Sprintf("writer/%d/%d", w, i)), bytes.Repeat([]byte{byte(w + 1)}, 4096)); err == nil {
					_, err = tx.Commit()
				} else {
					_ = tx.Rollback()
				}
				if err != nil {
					errorsOut <- err
					return
				}
			}
		}(w)
	}
	done := make(chan struct{})
	go func() { writers.Wait(); close(done) }()
	p := CheckpointPressurePolicy{OverlayBytes: 32 << 10, Mutations: 16}
	for {
		ticket, err := m.EvaluateCheckpoint(context.Background(), path, kitdb.OpenOptions{}, p, MaintenanceUrgent)
		if err != nil {
			t.Fatal(err)
		}
		if ticket != nil {
			if _, err = ticket.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-done:
			goto drained
		default:
			time.Sleep(time.Millisecond)
		}
	}
drained:
	close(errorsOut)
	for err := range errorsOut {
		t.Fatal(err)
	}
	ticket, err := m.ScheduleCheckpoint(context.Background(), path, kitdb.OpenOptions{}, MaintenanceUrgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ticket.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats, _ := l.DB().Stats()
	if stats.OverlayMutations != 0 || !stats.OverlaySince.IsZero() {
		t.Fatal("overlay did not drain")
	}
	_ = l.Release()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := kitdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for w := 0; w < 4; w++ {
		for i := 0; i < 64; i++ {
			v, ok, err := db.Get([]byte(fmt.Sprintf("writer/%d/%d", w, i)))
			if err != nil || !ok || !bytes.Equal(v, bytes.Repeat([]byte{byte(w + 1)}, 4096)) {
				t.Fatalf("oracle failed for %d/%d: %v", w, i, err)
			}
		}
	}
	if err := db.Verify(); err != nil {
		t.Fatal(err)
	}
}
