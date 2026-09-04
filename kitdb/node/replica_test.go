package node

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestManagedReplicaCatchUpSurvivesPruneAndResumes(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 4, MaxPageCacheBytes: 4 << 20,
		MaxConcurrentMaintenance: 2,
	})
	sourceOptions := kitdb.OpenOptions{RetainHistory: true}
	targetOptions := kitdb.OpenOptions{RetainHistory: true}
	source := filepath.Join(root, "source.kitdb")
	target := filepath.Join(root, "replica.kitdb")
	first := commitNodeBackupValue(t, manager, source, sourceOptions, "product/1", "anchor")
	bootstrap := scheduleNodeReplicaBootstrap(
		t, manager, source, target, sourceOptions, "replica/eu",
	)
	if bootstrap.Anchor.Transaction != first || bootstrap.Pin.Cursor != bootstrap.Anchor.Cursor() {
		t.Fatalf("replica bootstrap = %#v", bootstrap)
	}

	commitNodeBackupValue(t, manager, source, sourceOptions, "product/2", "second")
	third := commitNodeBackupValue(t, manager, source, sourceOptions, "product/3", "third")
	request := ReplicaCatchUpRequest{
		Source: source, Target: target, SourceOptions: sourceOptions,
		TargetOptions: targetOptions, PinName: "replica/eu",
		Priority: MaintenanceBackground,
	}
	firstCatchUp := scheduleNodeReplicaCatchUp(t, manager, request)
	if firstCatchUp.From != bootstrap.Anchor.Cursor() ||
		firstCatchUp.To != firstCatchUp.SourceBoundary ||
		firstCatchUp.To.Transaction != third || firstCatchUp.AppliedTransactions != 2 ||
		firstCatchUp.Pin.Cursor != firstCatchUp.To {
		t.Fatalf("first managed catch-up = %#v", firstCatchUp)
	}
	assertNodeReplicaValue(t, manager, target, targetOptions, "product/1", "anchor")
	assertNodeReplicaValue(t, manager, target, targetOptions, "product/2", "second")
	assertNodeReplicaValue(t, manager, target, targetOptions, "product/3", "third")

	pruneTicket, err := manager.ScheduleHistoryPrune(context.Background(), HistoryPruneRequest{
		Path: source, Through: firstCatchUp.To.Transaction, Options: sourceOptions,
		Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	pruned, err := pruneTicket.Wait(context.Background())
	if err != nil || pruned.HistoryPrune == nil ||
		pruned.HistoryPrune.BaseTransaction != third ||
		pruned.HistoryPrune.PrunedSegments != 2 {
		t.Fatalf("replica-watermark prune = (%#v, %v)", pruned, err)
	}

	fourth := commitNodeBackupValue(t, manager, source, sourceOptions, "product/4", "after-prune")
	resumed := scheduleNodeReplicaCatchUp(t, manager, request)
	if resumed.From != firstCatchUp.To || resumed.To.Transaction != fourth ||
		resumed.AppliedTransactions != 1 || resumed.Pin.Cursor != resumed.To {
		t.Fatalf("resumed managed catch-up = %#v", resumed)
	}
	assertNodeReplicaValue(t, manager, target, targetOptions, "product/4", "after-prune")

	noOp := scheduleNodeReplicaCatchUp(t, manager, request)
	if noOp.From != resumed.To || noOp.To != resumed.To ||
		noOp.AppliedTransactions != 0 || noOp.Pin.Cursor != resumed.To {
		t.Fatalf("no-op managed catch-up = %#v", noOp)
	}
	assertNodeBackupPin(t, manager, source, sourceOptions, "replica/eu", resumed.To)

	stats := manager.Stats()
	if stats.ReplicaCatchUpCompletions != 3 || stats.ReplicaTransactionsApplied != 3 ||
		stats.ReplicaCatchUpNoops != 1 || stats.HistoryPruneCompletions != 1 ||
		stats.MaintenanceFailures != 0 {
		t.Fatalf("managed replica stats = %#v", stats)
	}
}

func TestManagedReplicaCatchUpReturnsDivergenceEvidence(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{})
	sourceOptions := kitdb.OpenOptions{RetainHistory: true}
	source := filepath.Join(root, "source.kitdb")
	target := filepath.Join(root, "unrelated.kitdb")
	commitNodeBackupValue(t, manager, source, sourceOptions, "source", "value")
	unrelated, err := kitdb.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := unrelated.Close(); err != nil {
		t.Fatal(err)
	}

	ticket, err := manager.ScheduleReplicaCatchUp(context.Background(), ReplicaCatchUpRequest{
		Source: source, Target: target, SourceOptions: sourceOptions,
		PinName: "replica/diverged", Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if !errors.Is(err, kitdb.ErrReplicaDiverged) {
		t.Fatalf("diverged managed catch-up error = %v", err)
	}
	if result.ReplicaCatchUp == nil || result.ReplicaCatchUp.From.DatabaseID == "" ||
		result.ReplicaCatchUp.AppliedTransactions != 0 {
		t.Fatalf("diverged managed catch-up evidence = %#v", result)
	}
	if stats := manager.Stats(); stats.ReplicaCatchUpCompletions != 0 ||
		stats.ReplicaTransactionsApplied != 0 || stats.MaintenanceFailures != 1 {
		t.Fatalf("diverged managed replica stats = %#v", stats)
	}
}

func TestManagedReplicaCatchUpRejectsInsufficientFleetCapacity(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.kitdb")
	target := filepath.Join(root, "replica.kitdb")
	sourceDatabase, err := kitdb.OpenWithOptions(
		source, kitdb.OpenOptions{RetainHistory: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	commitNodeBackupTransaction(t, sourceDatabase, "source", "value")
	if _, err := sourceDatabase.CreateBackupAnchor(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := sourceDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 1, MaxConcurrentOpens: 1, MaxConcurrentMaintenance: 1,
	})

	_, err = manager.ScheduleReplicaCatchUp(context.Background(), ReplicaCatchUpRequest{
		Source: source, Target: target,
		SourceOptions: kitdb.OpenOptions{RetainHistory: true},
		PinName:       "replica/capacity", Priority: MaintenanceBackground,
	})
	if !errors.Is(err, ErrMaintenanceResourceCapacity) {
		t.Fatalf("replica capacity error = %v", err)
	}
	if stats := manager.Stats(); stats.MaintenanceSubmissions != 0 ||
		stats.ManagedDatabases != 0 {
		t.Fatalf("rejected replica catch-up acquired resources: %#v", stats)
	}
}

func TestManagedReplicaCatchUpCoalescesExactOperation(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 4, MaxPageCacheBytes: 4 << 20,
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 4,
	})
	sourceOptions := kitdb.OpenOptions{RetainHistory: true}
	source := filepath.Join(root, "source.kitdb")
	target := filepath.Join(root, "replica.kitdb")
	commitNodeBackupValue(t, manager, source, sourceOptions, "product/1", "anchor")
	scheduleNodeReplicaBootstrap(t, manager, source, target, sourceOptions, "replica/main")
	commitNodeBackupValue(t, manager, source, sourceOptions, "product/2", "next")

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
			return 0, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	blocker := mustScheduleCheckpoint(
		t, manager, filepath.Join(root, "blocker.kitdb"), MaintenanceNormal,
	)
	<-started
	request := ReplicaCatchUpRequest{
		Source: source, Target: target, SourceOptions: sourceOptions,
		PinName: "replica/main", Priority: MaintenanceBackground,
	}
	first, err := manager.ScheduleReplicaCatchUp(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Priority = MaintenanceUrgent
	joined, err := manager.ScheduleReplicaCatchUp(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	different, err := manager.ScheduleReplicaCatchUp(context.Background(), ReplicaCatchUpRequest{
		Source: source, Target: target, SourceOptions: sourceOptions,
		PinName: "replica/secondary", Priority: MaintenanceNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.task != joined.task || first.task.priority != MaintenanceUrgent {
		t.Fatal("identical replica catch-up did not coalesce and promote")
	}
	if first.task == different.task {
		t.Fatal("different replica pin operations were incorrectly coalesced")
	}
	close(release)
	if _, err := blocker.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, ticket := range []*MaintenanceTicket{first, joined, different} {
		result, err := ticket.Wait(context.Background())
		if err != nil || result.ReplicaCatchUp == nil {
			t.Fatalf("coalesced replica catch-up = (%#v, %v)", result, err)
		}
	}
	if stats := manager.Stats(); stats.MaintenanceCoalesced != 1 ||
		stats.ReplicaCatchUpCompletions != 2 || stats.ReplicaTransactionsApplied != 1 ||
		stats.ReplicaCatchUpNoops != 1 {
		t.Fatalf("coalesced replica stats = %#v", stats)
	}
}

func scheduleNodeReplicaBootstrap(
	t *testing.T,
	manager *Manager,
	source string,
	target string,
	options kitdb.OpenOptions,
	pinName string,
) VerifiedBackupResult {
	t.Helper()
	ticket, err := manager.ScheduleBackup(context.Background(), BackupRequest{
		Source: source, Destination: target, Options: options,
		PinName: pinName, Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil || result.Backup == nil {
		t.Fatalf("replica bootstrap = (%#v, %v)", result, err)
	}
	return *result.Backup
}

func scheduleNodeReplicaCatchUp(
	t *testing.T,
	manager *Manager,
	request ReplicaCatchUpRequest,
) kitdb.ReplicaCatchUp {
	t.Helper()
	ticket, err := manager.ScheduleReplicaCatchUp(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil || result.ReplicaCatchUp == nil {
		t.Fatalf("managed replica catch-up = (%#v, %v)", result, err)
	}
	return *result.ReplicaCatchUp
}

func assertNodeReplicaValue(
	t *testing.T,
	manager *Manager,
	path string,
	options kitdb.OpenOptions,
	key string,
	want string,
) {
	t.Helper()
	options.Replica = true
	lease, err := manager.Acquire(context.Background(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	value, found, getErr := lease.DB().Get([]byte(key))
	if releaseErr := lease.Release(); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if getErr != nil || !found || string(value) != want {
		t.Fatalf("replica %q = (%q, %t, %v), want %q", key, value, found, getErr, want)
	}
}
