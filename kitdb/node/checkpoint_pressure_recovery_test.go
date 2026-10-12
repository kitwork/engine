package node

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
)

func TestCheckpointPressureRecoversAfterQueueFullAndCanceledWait(t *testing.T) {
	root := t.TempDir()
	m := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 1, MaxMaintenancePerDatabase: 1})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	m.maintenanceExecutor = func(ctx context.Context, db *kitdb.DB, kind MaintenanceKind) (uint64, error) {
		if filepath.Base(db.Path()) == "first.kitdb" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		return runMaintenanceOperation(ctx, db, kind)
	}
	ctx := context.Background()
	first, err := m.ScheduleVerify(ctx, filepath.Join(root, "first.kitdb"), kitdb.OpenOptions{}, MaintenanceNormal)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never started")
	}
	second, err := m.ScheduleVerify(ctx, filepath.Join(root, "second.kitdb"), kitdb.OpenOptions{}, MaintenanceNormal)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "pressure.kitdb")
	lease, err := m.Acquire(ctx, path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	pressurePut(t, lease.DB(), "value", []byte("durable"))
	policy := CheckpointPressurePolicy{Mutations: 1, InitialBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond}
	if _, err := m.EvaluateCheckpoint(ctx, path, kitdb.OpenOptions{}, policy, MaintenanceUrgent); !errors.Is(err, ErrMaintenanceQueueFull) {
		t.Fatalf("full queue did not reject pressure admission: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := first.Wait(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait cancellation: %v", err)
	}
	select {
	case <-first.Done():
		t.Fatal("one canceled waiter canceled a shared job")
	default:
	}
	once.Do(func() { close(release) })
	waitCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	if _, err := first.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	var ticket *MaintenanceTicket
	for {
		ticket, err = m.EvaluateCheckpoint(waitCtx, path, kitdb.OpenOptions{}, policy, MaintenanceUrgent)
		if !errors.Is(err, ErrCheckpointBackoff) {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("pressure never recovered")
		case <-time.After(time.Millisecond):
		}
	}
	if err != nil || ticket == nil {
		t.Fatalf("recovered schedule: %v", err)
	}
	if _, err := ticket.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	stats, err := lease.DB().Stats()
	if err != nil || stats.OverlayMutations != 0 || stats.ActiveSnapshots != 0 || stats.PendingCommits != 0 {
		t.Fatalf("kernel not drained: %+v %v", stats, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	nodeStats := m.Stats()
	if nodeStats.ActiveLeases != 0 || nodeStats.QueuedMaintenance != 0 || nodeStats.RunningMaintenance != 0 || nodeStats.MaintenanceRejections != 1 || nodeStats.CheckpointCompletions != 1 {
		t.Fatalf("node not recovered: %+v", nodeStats)
	}
}

func TestCheckpointPressureReaderRecoveryPreservesRetryFloor(t *testing.T) {
	now := time.Now()
	state := checkpointPressureState{readerBlocked: true, readerRetryAt: now.Add(time.Second), next: now.Add(time.Minute)}
	if state.readerRetryReady(kitdb.Stats{}, now) {
		t.Fatal("retry interval bypassed")
	}
	for _, stats := range []kitdb.Stats{{ActiveSnapshots: 1}, {SnapshotReservations: 1}} {
		if state.readerRetryReady(stats, now.Add(2*time.Second)) {
			t.Fatal("reader ownership ignored")
		}
	}
	if !state.readerRetryReady(kitdb.Stats{}, now.Add(2*time.Second)) {
		t.Fatal("quiescent reader recovery waited for exponential delay")
	}
	state.readerBlocked = false
	if state.readerRetryReady(kitdb.Stats{}, now.Add(2*time.Second)) {
		t.Fatal("non-reader failure bypassed backoff")
	}
}
