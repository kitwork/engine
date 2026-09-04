package node

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
)

func TestMaintenanceRunsCheckpointAndVerification(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{})
	path := filepath.Join(root, "tenant.kitdb")
	lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := lease.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("product/1"), []byte("Kitwork")); err != nil {
		t.Fatal(err)
	}
	committed, err := transaction.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	checkpoint, err := manager.ScheduleCheckpoint(
		context.Background(), path, kitdb.OpenOptions{}, MaintenanceUrgent,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := checkpoint.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != MaintenanceCheckpoint || result.Transaction != committed || result.Duration < 0 {
		t.Fatalf("checkpoint result = %#v, want transaction %d", result, committed)
	}

	verification, err := manager.ScheduleVerify(
		context.Background(), path, kitdb.OpenOptions{}, MaintenanceBackground,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verification.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := manager.Stats()
	if stats.MaintenanceSubmissions != 2 || stats.MaintenanceCompletions != 2 ||
		stats.CheckpointCompletions != 1 || stats.VerificationCompletions != 1 ||
		stats.MaintenanceFailures != 0 || stats.RunningMaintenance != 0 ||
		stats.QueuedMaintenance != 0 {
		t.Fatalf("maintenance stats = %#v", stats)
	}
}

func TestMaintenanceCoalescesDuplicateCheckpoint(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return 7, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	path := filepath.Join(root, "tenant.kitdb")
	first, err := manager.ScheduleCheckpoint(
		context.Background(), path, kitdb.OpenOptions{}, MaintenanceNormal,
	)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	second, err := manager.ScheduleCheckpoint(
		context.Background(), path, kitdb.OpenOptions{}, MaintenanceUrgent,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.task != second.task {
		t.Fatal("duplicate checkpoint did not join the active task")
	}
	close(release)
	for _, ticket := range []*MaintenanceTicket{first, second} {
		result, waitErr := ticket.Wait(context.Background())
		if waitErr != nil || result.Transaction != 7 {
			t.Fatalf("coalesced result = (%#v, %v)", result, waitErr)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("executor calls = %d, want 1", calls.Load())
	}
	stats := manager.Stats()
	if stats.MaintenanceSubmissions != 2 || stats.MaintenanceCoalesced != 1 ||
		stats.MaintenanceCompletions != 1 {
		t.Fatalf("coalescing stats = %#v", stats)
	}
}

func TestMaintenanceBoundsConcurrencyAndSerializesEachDatabase(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 4, MaxConcurrentMaintenance: 2,
		MaxQueuedMaintenance: 8, MaxMaintenancePerDatabase: 2,
	})
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	var mu sync.Mutex
	active := 0
	maximumActive := 0
	activePaths := make(map[string]bool)
	pathOverlap := false
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		mu.Lock()
		active++
		maximumActive = max(maximumActive, active)
		if activePaths[database.Path()] {
			pathOverlap = true
		}
		activePaths[database.Path()] = true
		mu.Unlock()
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		mu.Lock()
		delete(activePaths, database.Path())
		active--
		mu.Unlock()
		return 0, ctx.Err()
	}
	pathA := filepath.Join(root, "a.kitdb")
	pathB := filepath.Join(root, "b.kitdb")
	tickets := make([]*MaintenanceTicket, 0, 3)
	for _, schedule := range []func() (*MaintenanceTicket, error){
		func() (*MaintenanceTicket, error) {
			return manager.ScheduleCheckpoint(context.Background(), pathA, kitdb.OpenOptions{}, MaintenanceNormal)
		},
		func() (*MaintenanceTicket, error) {
			return manager.ScheduleVerify(context.Background(), pathA, kitdb.OpenOptions{}, MaintenanceNormal)
		},
		func() (*MaintenanceTicket, error) {
			return manager.ScheduleCheckpoint(context.Background(), pathB, kitdb.OpenOptions{}, MaintenanceNormal)
		},
	} {
		ticket, err := schedule()
		if err != nil {
			t.Fatal(err)
		}
		tickets = append(tickets, ticket)
	}
	<-started
	<-started
	close(release)
	for _, ticket := range tickets {
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if maximumActive != 2 || pathOverlap {
		t.Fatalf("maximum active = %d, same-path overlap = %t", maximumActive, pathOverlap)
	}
}

func TestMaintenancePriorityAndQueueBackpressure(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 4, MaxConcurrentMaintenance: 1,
		MaxQueuedMaintenance: 2, MaxMaintenancePerDatabase: 2,
	})
	started := make(chan string, 4)
	release := make(chan struct{})
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		started <- filepath.Base(database.Path())
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	first := mustScheduleCheckpoint(t, manager, filepath.Join(root, "first.kitdb"), MaintenanceNormal)
	if got := <-started; got != "first.kitdb" {
		t.Fatalf("first job = %q", got)
	}
	background := mustScheduleCheckpoint(t, manager, filepath.Join(root, "background.kitdb"), MaintenanceBackground)
	urgent := mustScheduleCheckpoint(t, manager, filepath.Join(root, "urgent.kitdb"), MaintenanceUrgent)
	if _, err := manager.ScheduleCheckpoint(
		context.Background(), filepath.Join(root, "rejected.kitdb"), kitdb.OpenOptions{}, MaintenanceNormal,
	); !errors.Is(err, ErrMaintenanceQueueFull) {
		t.Fatalf("full queue error = %v", err)
	}

	release <- struct{}{}
	if got := <-started; got != "urgent.kitdb" {
		t.Fatalf("job after blocker = %q, want urgent.kitdb", got)
	}
	release <- struct{}{}
	if got := <-started; got != "background.kitdb" {
		t.Fatalf("last job = %q, want background.kitdb", got)
	}
	release <- struct{}{}
	for _, ticket := range []*MaintenanceTicket{first, urgent, background} {
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if stats := manager.Stats(); stats.MaintenanceRejections != 1 ||
		stats.MaintenanceCompletions != 3 {
		t.Fatalf("backpressure stats = %#v", stats)
	}
}

func TestMaintenancePromotesCoalescedCheckpoint(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 4, MaxConcurrentMaintenance: 1,
		MaxQueuedMaintenance: 4, MaxMaintenancePerDatabase: 2,
	})
	started := make(chan string, 4)
	release := make(chan struct{})
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		started <- filepath.Base(database.Path())
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	blocker := mustScheduleCheckpoint(t, manager, filepath.Join(root, "blocker.kitdb"), MaintenanceNormal)
	<-started
	targetPath := filepath.Join(root, "target.kitdb")
	target := mustScheduleCheckpoint(t, manager, targetPath, MaintenanceBackground)
	normal := mustScheduleCheckpoint(t, manager, filepath.Join(root, "normal.kitdb"), MaintenanceNormal)
	promoted := mustScheduleCheckpoint(t, manager, targetPath, MaintenanceUrgent)
	if promoted.task != target.task || promoted.task.priority != MaintenanceUrgent {
		t.Fatal("coalesced checkpoint was not promoted to urgent priority")
	}

	release <- struct{}{}
	if got := <-started; got != "target.kitdb" {
		t.Fatalf("job after blocker = %q, want promoted target.kitdb", got)
	}
	release <- struct{}{}
	if got := <-started; got != "normal.kitdb" {
		t.Fatalf("job after promoted target = %q, want normal.kitdb", got)
	}
	release <- struct{}{}
	for _, ticket := range []*MaintenanceTicket{blocker, target, promoted, normal} {
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMaintenanceBackgroundProgressUnderUrgentLoad(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 12, MaxConcurrentOpens: 4,
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 12,
		MaxMaintenancePerDatabase: 2,
	})
	started := make(chan string, 12)
	release := make(chan struct{})
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		started <- filepath.Base(database.Path())
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	tickets := []*MaintenanceTicket{
		mustScheduleCheckpoint(t, manager, filepath.Join(root, "blocker.kitdb"), MaintenanceNormal),
	}
	<-started
	tickets = append(tickets,
		mustScheduleCheckpoint(t, manager, filepath.Join(root, "background.kitdb"), MaintenanceBackground),
	)
	for index := range 7 {
		tickets = append(tickets, mustScheduleCheckpoint(
			t, manager, filepath.Join(root, fmt.Sprintf("urgent-%d.kitdb", index)), MaintenanceUrgent,
		))
	}

	backgroundPosition := -1
	for position := range 8 {
		release <- struct{}{}
		name := <-started
		if name == "background.kitdb" {
			backgroundPosition = position
			break
		}
	}
	if backgroundPosition < 0 || backgroundPosition > maximumForegroundMaintenanceBurst {
		t.Fatalf("background position = %d, want at most %d", backgroundPosition, maximumForegroundMaintenanceBurst)
	}
	remaining := 7 - backgroundPosition
	for range remaining {
		release <- struct{}{}
		<-started
	}
	release <- struct{}{}
	for _, ticket := range tickets {
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMaintenanceWaitCancellationDoesNotCancelSharedJob(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	started := make(chan struct{})
	release := make(chan struct{})
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		close(started)
		select {
		case <-release:
			return 11, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	path := filepath.Join(root, "tenant.kitdb")
	first := mustScheduleCheckpoint(t, manager, path, MaintenanceNormal)
	<-started
	second := mustScheduleCheckpoint(t, manager, path, MaintenanceNormal)
	waitContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := first.Wait(waitContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v", err)
	}
	close(release)
	result, err := second.Wait(context.Background())
	if err != nil || result.Transaction != 11 {
		t.Fatalf("surviving waiter = (%#v, %v)", result, err)
	}
	if stats := manager.Stats(); stats.MaintenanceCancellations != 0 ||
		stats.MaintenanceCoalesced != 1 {
		t.Fatalf("wait cancellation affected job stats: %#v", stats)
	}
}

func TestMaintenanceCloseCancelsRunningAndQueuedJobs(t *testing.T) {
	manager, err := NewManager(Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 2,
		MaxMaintenancePerDatabase: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	}
	root := t.TempDir()
	running := mustScheduleCheckpoint(t, manager, filepath.Join(root, "running.kitdb"), MaintenanceNormal)
	<-started
	queued := mustScheduleCheckpoint(t, manager, filepath.Join(root, "queued.kitdb"), MaintenanceNormal)
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := running.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("running job error = %v", err)
	}
	if _, err := queued.Wait(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("queued job error = %v", err)
	}
	stats := manager.Stats()
	if !stats.Closed || stats.MaintenanceCancellations != 2 ||
		stats.QueuedMaintenance != 0 || stats.RunningMaintenance != 0 {
		t.Fatalf("closed maintenance stats = %#v", stats)
	}
}

func TestMaintenancePerDatabaseBound(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxConcurrentMaintenance: 1, MaxMaintenancePerDatabase: 1,
	})
	started := make(chan struct{})
	release := make(chan struct{})
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		close(started)
		<-release
		return 0, nil
	}
	path := filepath.Join(root, "tenant.kitdb")
	ticket := mustScheduleCheckpoint(t, manager, path, MaintenanceNormal)
	<-started
	if _, err := manager.ScheduleVerify(
		context.Background(), path, kitdb.OpenOptions{}, MaintenanceNormal,
	); !errors.Is(err, ErrMaintenanceDatabaseQueueFull) {
		t.Fatalf("per-database bound error = %v", err)
	}
	close(release)
	if _, err := ticket.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceContainsExecutorPanicAndReleasesLease(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		panic("broken maintenance")
	}
	path := filepath.Join(root, "tenant.kitdb")
	ticket := mustScheduleCheckpoint(t, manager, path, MaintenanceNormal)
	if _, err := ticket.Wait(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "maintenance panicked") {
		t.Fatalf("panic result error = %v", err)
	}
	if stats := manager.Stats(); stats.MaintenanceFailures != 1 ||
		stats.ActiveLeases != 0 || stats.IdleDatabases != 1 {
		t.Fatalf("panic maintenance leaked ownership: %#v", stats)
	}
}

func TestRowMigrationMaintenanceContinuesAndCoalesces(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Uint64
	driver := rowMigrationTestDriver{
		id: "test/row-migration/v1",
		advance: func(ctx context.Context, database *kitdb.DB) (RowMigrationProgress, error) {
			call := calls.Add(1)
			if call == 1 {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return RowMigrationProgress{}, ctx.Err()
				}
			}
			return RowMigrationProgress{
				Transaction: call, Pending: call < 3, Advanced: true,
			}, nil
		},
	}
	if err := manager.RegisterRowMigrationDriver(driver); err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterRowMigrationDriver(rowMigrationTestDriver{id: driver.id}); err != nil {
		t.Fatalf("same driver ID was not idempotent: %v", err)
	}
	if err := manager.RegisterRowMigrationDriver(rowMigrationTestDriver{id: "different"}); !errors.Is(err, ErrRowMigrationDriverConflict) {
		t.Fatalf("different driver registration error = %v", err)
	}

	path := filepath.Join(root, "tenant.kitdb")
	first, err := manager.ScheduleRowMigration(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	second, err := manager.ScheduleRowMigration(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.task != second.task {
		t.Fatal("row-migration submissions did not coalesce")
	}
	close(release)
	result, err := first.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Transaction != 3 || result.RowMigration == nil ||
		result.RowMigration.Pending || result.RowMigration.Chunks != 3 ||
		result.RowMigration.AdvancedChunks != 3 {
		t.Fatalf("row-migration result = %#v", result)
	}
	stats := manager.Stats()
	if stats.MaintenanceSubmissions != 2 || stats.MaintenanceCoalesced != 1 ||
		stats.MaintenanceCompletions != 1 || stats.RowMigrationCompletions != 1 ||
		stats.RowMigrationChunks != 3 || stats.RowMigrationAdvancedChunks != 3 {
		t.Fatalf("row-migration stats = %#v", stats)
	}
}

func TestRowMigrationMaintenanceUsesDatabaseGate(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	started := make(chan struct{})
	if err := manager.RegisterRowMigrationDriver(rowMigrationTestDriver{
		id: "test/gate/v1",
		advance: func(context.Context, *kitdb.DB) (RowMigrationProgress, error) {
			close(started)
			return RowMigrationProgress{Advanced: true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tenant.kitdb")
	lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gate := lease.Gate()
	if gate == nil {
		t.Fatal("database gate is unavailable")
	}
	gate.Lock()
	ticket, err := manager.ScheduleRowMigration(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		gate.Unlock()
		_ = lease.Release()
		t.Fatal(err)
	}
	select {
	case <-started:
		gate.Unlock()
		_ = lease.Release()
		t.Fatal("row migration crossed a held foreground database gate")
	case <-time.After(25 * time.Millisecond):
	}
	gate.Unlock()
	if _, err := ticket.Wait(context.Background()); err != nil {
		_ = lease.Release()
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestRowMigrationMaintenanceContainsDriverPanicAndUnlocksGate(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	if err := manager.RegisterRowMigrationDriver(rowMigrationTestDriver{
		id: "test/panic/v1",
		advance: func(context.Context, *kitdb.DB) (RowMigrationProgress, error) {
			panic("broken row migration")
		},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tenant.kitdb")
	lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := manager.ScheduleRowMigration(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		_ = lease.Release()
		t.Fatal(err)
	}
	if _, err := ticket.Wait(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "maintenance panicked") {
		_ = lease.Release()
		t.Fatalf("row-migration panic error = %v", err)
	}
	locked := make(chan struct{})
	go func() {
		lease.Gate().Lock()
		lease.Gate().Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(time.Second):
		_ = lease.Release()
		t.Fatal("row-migration panic left the database gate locked")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if stats := manager.Stats(); stats.MaintenanceFailures != 1 || stats.RowMigrationChunks != 0 {
		t.Fatalf("row-migration panic stats = %#v", stats)
	}
}

func TestRowMigrationMaintenanceRejectsPendingWithoutProgress(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	var calls atomic.Uint64
	if err := manager.RegisterRowMigrationDriver(rowMigrationTestDriver{
		id: "test/no-progress/v1",
		advance: func(context.Context, *kitdb.DB) (RowMigrationProgress, error) {
			calls.Add(1)
			return RowMigrationProgress{Pending: true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	ticket, err := manager.ScheduleRowMigration(
		context.Background(), filepath.Join(root, "tenant.kitdb"), kitdb.OpenOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Wait(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "without durable progress") {
		t.Fatalf("no-progress error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("no-progress driver calls = %d, want 1", calls.Load())
	}
}

func TestRowMigrationContinuationYieldsToUrgentMaintenance(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 3, MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 4,
	})
	events := make(chan string, 8)
	releaseFirst := make(chan struct{})
	var calls atomic.Uint64
	if err := manager.RegisterRowMigrationDriver(rowMigrationTestDriver{
		id: "test/fairness/v1",
		advance: func(ctx context.Context, _ *kitdb.DB) (RowMigrationProgress, error) {
			call := calls.Add(1)
			events <- fmt.Sprintf("row-%d", call)
			if call == 1 {
				select {
				case <-releaseFirst:
				case <-ctx.Done():
					return RowMigrationProgress{}, ctx.Err()
				}
			}
			return RowMigrationProgress{Transaction: call, Pending: call < 3, Advanced: true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	manager.maintenanceExecutor = func(
		context.Context,
		*kitdb.DB,
		MaintenanceKind,
	) (uint64, error) {
		events <- "urgent"
		return 0, nil
	}
	migration, err := manager.ScheduleRowMigration(
		context.Background(), filepath.Join(root, "migration.kitdb"), kitdb.OpenOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if event := <-events; event != "row-1" {
		t.Fatalf("first event = %q", event)
	}
	urgent := mustScheduleCheckpoint(
		t, manager, filepath.Join(root, "urgent.kitdb"), MaintenanceUrgent,
	)
	close(releaseFirst)
	if event := <-events; event != "urgent" {
		t.Fatalf("event after first row chunk = %q, want urgent", event)
	}
	if _, err := urgent.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("row-migration calls = %d, want 3", calls.Load())
	}
}

func TestRowMigrationMaintenanceStopsWithManager(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	started := make(chan struct{})
	if err := manager.RegisterRowMigrationDriver(rowMigrationTestDriver{
		id: "test/shutdown/v1",
		advance: func(ctx context.Context, _ *kitdb.DB) (RowMigrationProgress, error) {
			close(started)
			<-ctx.Done()
			return RowMigrationProgress{}, ctx.Err()
		},
	}); err != nil {
		t.Fatal(err)
	}
	ticket, err := manager.ScheduleRowMigration(
		context.Background(), filepath.Join(root, "tenant.kitdb"), kitdb.OpenOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown row-migration error = %v", err)
	}
	stats := manager.Stats()
	if !stats.Closed || stats.MaintenanceCancellations != 1 ||
		stats.ActiveLeases != 0 || stats.RunningMaintenance != 0 {
		t.Fatalf("shutdown row-migration stats = %#v", stats)
	}
}

func TestSecondaryIndexMaintenanceContinuesAndCoalesces(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Uint64
	driver := secondaryIndexTestDriver{
		id: "test/secondary-index/v1",
		advance: func(ctx context.Context, _ *kitdb.DB) (SecondaryIndexProgress, error) {
			call := calls.Add(1)
			if call == 1 {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return SecondaryIndexProgress{}, ctx.Err()
				}
			}
			return SecondaryIndexProgress{
				Transaction: call, Pending: call < 3, Advanced: true,
			}, nil
		},
	}
	if err := manager.RegisterSecondaryIndexDriver(driver); err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterSecondaryIndexDriver(secondaryIndexTestDriver{id: driver.id}); err != nil {
		t.Fatalf("same driver ID was not idempotent: %v", err)
	}
	if err := manager.RegisterSecondaryIndexDriver(
		secondaryIndexTestDriver{id: "different"},
	); !errors.Is(err, ErrSecondaryIndexDriverConflict) {
		t.Fatalf("different driver registration error = %v", err)
	}

	path := filepath.Join(root, "tenant.kitdb")
	first, err := manager.ScheduleSecondaryIndex(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	second, err := manager.ScheduleSecondaryIndex(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.task != second.task {
		t.Fatal("secondary-index submissions did not coalesce")
	}
	close(release)
	result, err := first.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Transaction != 3 || result.SecondaryIndex == nil ||
		result.SecondaryIndex.Pending || result.SecondaryIndex.Chunks != 3 ||
		result.SecondaryIndex.AdvancedChunks != 3 {
		t.Fatalf("secondary-index result = %#v", result)
	}
	stats := manager.Stats()
	if stats.MaintenanceSubmissions != 2 || stats.MaintenanceCoalesced != 1 ||
		stats.MaintenanceCompletions != 1 || stats.SecondaryIndexCompletions != 1 ||
		stats.SecondaryIndexChunks != 3 || stats.SecondaryIndexAdvancedChunks != 3 {
		t.Fatalf("secondary-index stats = %#v", stats)
	}
}

func TestSecondaryIndexMaintenanceUsesDatabaseGate(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	started := make(chan struct{})
	if err := manager.RegisterSecondaryIndexDriver(secondaryIndexTestDriver{
		id: "test/secondary-index-gate/v1",
		advance: func(context.Context, *kitdb.DB) (SecondaryIndexProgress, error) {
			close(started)
			return SecondaryIndexProgress{Advanced: true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tenant.kitdb")
	lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gate := lease.Gate()
	gate.Lock()
	ticket, err := manager.ScheduleSecondaryIndex(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		gate.Unlock()
		_ = lease.Release()
		t.Fatal(err)
	}
	select {
	case <-started:
		gate.Unlock()
		_ = lease.Release()
		t.Fatal("secondary-index maintenance crossed a held foreground database gate")
	case <-time.After(25 * time.Millisecond):
	}
	gate.Unlock()
	if _, err := ticket.Wait(context.Background()); err != nil {
		_ = lease.Release()
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSecondaryIndexMaintenanceContainsPanicAndRejectsNoProgress(t *testing.T) {
	t.Run("panic", func(t *testing.T) {
		root := t.TempDir()
		manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
		if err := manager.RegisterSecondaryIndexDriver(secondaryIndexTestDriver{
			id: "test/secondary-index-panic/v1",
			advance: func(context.Context, *kitdb.DB) (SecondaryIndexProgress, error) {
				panic("broken secondary index")
			},
		}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "tenant.kitdb")
		lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := manager.ScheduleSecondaryIndex(context.Background(), path, kitdb.OpenOptions{})
		if err != nil {
			_ = lease.Release()
			t.Fatal(err)
		}
		if _, err := ticket.Wait(context.Background()); err == nil ||
			!strings.Contains(err.Error(), "maintenance panicked") {
			_ = lease.Release()
			t.Fatalf("secondary-index panic error = %v", err)
		}
		locked := make(chan struct{})
		go func() {
			lease.Gate().Lock()
			lease.Gate().Unlock()
			close(locked)
		}()
		select {
		case <-locked:
		case <-time.After(time.Second):
			_ = lease.Release()
			t.Fatal("secondary-index panic left the database gate locked")
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("no progress", func(t *testing.T) {
		root := t.TempDir()
		manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
		var calls atomic.Uint64
		if err := manager.RegisterSecondaryIndexDriver(secondaryIndexTestDriver{
			id: "test/secondary-index-no-progress/v1",
			advance: func(context.Context, *kitdb.DB) (SecondaryIndexProgress, error) {
				calls.Add(1)
				return SecondaryIndexProgress{Pending: true}, nil
			},
		}); err != nil {
			t.Fatal(err)
		}
		ticket, err := manager.ScheduleSecondaryIndex(
			context.Background(), filepath.Join(root, "tenant.kitdb"), kitdb.OpenOptions{},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ticket.Wait(context.Background()); err == nil ||
			!strings.Contains(err.Error(), "without durable progress") {
			t.Fatalf("secondary-index no-progress error = %v", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("secondary-index no-progress calls = %d, want 1", calls.Load())
		}
	})
}

func TestMaintenanceResourceSetSerializesEveryTouchedDatabase(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 6, MaxPageCacheBytes: 6 << 20,
		MaxConcurrentMaintenance: 2, MaxQueuedMaintenance: 8,
	})
	started := make(chan string, 4)
	release := make(chan struct{})
	manager.maintenanceExecutor = func(
		ctx context.Context,
		database *kitdb.DB,
		kind MaintenanceKind,
	) (uint64, error) {
		started <- filepath.Base(database.Path())
		select {
		case <-release:
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	pathA := filepath.Join(root, "a.kitdb")
	pathB := filepath.Join(root, "b.kitdb")
	pathC := filepath.Join(root, "c.kitdb")
	pathD := filepath.Join(root, "d.kitdb")
	pathE := filepath.Join(root, "e.kitdb")
	multi, err := manager.scheduleMaintenance(
		context.Background(), pathA, kitdb.OpenOptions{}, MaintenanceCheckpoint,
		"pair-a-b", MaintenanceNormal, maintenancePayload{},
		[]maintenanceResourceRequest{{path: pathB}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "a.kitdb" {
		t.Fatalf("first multi-resource task started %q", got)
	}
	if stats := manager.Stats(); !stats.MultiDatabaseMaintenanceRunning {
		t.Fatalf("multi-resource task is not observable: %#v", stats)
	}
	blocked, err := manager.ScheduleVerify(
		context.Background(), pathB, kitdb.OpenOptions{}, MaintenanceNormal,
	)
	if err != nil {
		t.Fatal(err)
	}
	otherMulti, err := manager.scheduleMaintenance(
		context.Background(), pathD, kitdb.OpenOptions{}, MaintenanceCheckpoint,
		"pair-d-e", MaintenanceNormal, maintenancePayload{},
		[]maintenanceResourceRequest{{path: pathE}},
	)
	if err != nil {
		t.Fatal(err)
	}
	independent, err := manager.ScheduleVerify(
		context.Background(), pathC, kitdb.OpenOptions{}, MaintenanceNormal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "c.kitdb" {
		t.Fatalf("overlapping or second multi-resource task started before independent: %q", got)
	}
	select {
	case unexpected := <-started:
		t.Fatalf("resource reservation allowed unexpected concurrent task %q", unexpected)
	default:
	}
	close(release)
	for _, ticket := range []*MaintenanceTicket{multi, blocked, otherMulti, independent} {
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if stats := manager.Stats(); stats.MaintenanceCompletions != 4 ||
		stats.MaintenanceFailures != 0 || stats.RunningMaintenance != 0 {
		t.Fatalf("multi-resource maintenance stats = %#v", stats)
	}
}

func newMaintenanceTestManager(t *testing.T, overrides Limits) *Manager {
	t.Helper()
	limits := Limits{
		MaxOpenDatabases: 4, MaxPageCacheBytes: 4 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
		MaxConcurrentMaintenance: 2, MaxQueuedMaintenance: 16,
		MaxMaintenancePerDatabase: 2, MaintenanceTimeout: time.Minute,
	}
	if overrides.MaxOpenDatabases != 0 {
		limits.MaxOpenDatabases = overrides.MaxOpenDatabases
	}
	if overrides.MaxPageCacheBytes != 0 {
		limits.MaxPageCacheBytes = overrides.MaxPageCacheBytes
	}
	if overrides.DefaultPageCacheBytes != 0 {
		limits.DefaultPageCacheBytes = overrides.DefaultPageCacheBytes
	}
	if overrides.MaxConcurrentOpens != 0 {
		limits.MaxConcurrentOpens = overrides.MaxConcurrentOpens
	}
	if overrides.MaxConcurrentMaintenance != 0 {
		limits.MaxConcurrentMaintenance = overrides.MaxConcurrentMaintenance
	}
	if overrides.MaxQueuedMaintenance != 0 {
		limits.MaxQueuedMaintenance = overrides.MaxQueuedMaintenance
	}
	if overrides.MaxMaintenancePerDatabase != 0 {
		limits.MaxMaintenancePerDatabase = overrides.MaxMaintenancePerDatabase
	}
	if overrides.MaintenanceTimeout != 0 {
		limits.MaintenanceTimeout = overrides.MaintenanceTimeout
	}
	if overrides.ReplicaFileLinks != (ReplicaFileLinkLimits{}) {
		limits.ReplicaFileLinks = overrides.ReplicaFileLinks
	}
	manager, err := NewManager(limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	})
	return manager
}

func mustScheduleCheckpoint(
	t *testing.T,
	manager *Manager,
	path string,
	priority MaintenancePriority,
) *MaintenanceTicket {
	t.Helper()
	ticket, err := manager.ScheduleCheckpoint(
		context.Background(), path, kitdb.OpenOptions{}, priority,
	)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

type rowMigrationTestDriver struct {
	id      string
	advance func(context.Context, *kitdb.DB) (RowMigrationProgress, error)
}

type secondaryIndexTestDriver struct {
	id      string
	advance func(context.Context, *kitdb.DB) (SecondaryIndexProgress, error)
}

func (driver secondaryIndexTestDriver) SecondaryIndexDriverID() string { return driver.id }

func (driver secondaryIndexTestDriver) AdvanceSecondaryIndex(
	ctx context.Context,
	database *kitdb.DB,
) (SecondaryIndexProgress, error) {
	if driver.advance == nil {
		return SecondaryIndexProgress{}, nil
	}
	return driver.advance(ctx, database)
}

func (driver rowMigrationTestDriver) RowMigrationDriverID() string { return driver.id }

func (driver rowMigrationTestDriver) AdvanceRowMigration(
	ctx context.Context,
	database *kitdb.DB,
) (RowMigrationProgress, error) {
	if driver.advance == nil {
		return RowMigrationProgress{}, nil
	}
	return driver.advance(ctx, database)
}
