package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestManagedHistoryPrunePreservesBackupRestoreChain(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	options := kitdb.OpenOptions{RetainHistory: true}
	source := filepath.Join(root, "tenant.kitdb")
	destination := filepath.Join(root, "tenant-backup.kitdb")
	first := commitNodeBackupValue(t, manager, source, options, "product/1", "anchor")

	backupTicket, err := manager.ScheduleBackup(context.Background(), BackupRequest{
		Source: source, Destination: destination, Options: options,
		PinName: "backup/lifecycle", Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	backupResult, err := backupTicket.Wait(context.Background())
	if err != nil || backupResult.Backup == nil ||
		backupResult.Backup.Anchor.Transaction != first {
		t.Fatalf("backup result = (%#v, %v)", backupResult, err)
	}

	commitNodeBackupValue(t, manager, source, options, "product/2", "after-anchor")
	checkpointNodeHistory(t, manager, source, options)
	latest := commitNodeBackupValue(t, manager, source, options, "product/3", "latest")
	checkpointNodeHistory(t, manager, source, options)

	request := HistoryPruneRequest{
		Path: source, Through: first, Options: options, Priority: MaintenanceBackground,
	}
	pruneTicket, err := manager.ScheduleHistoryPrune(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	pruned, err := pruneTicket.Wait(context.Background())
	if err != nil || pruned.Kind != MaintenanceHistoryPrune ||
		pruned.Transaction != first || pruned.HistoryPrune == nil {
		t.Fatalf("history prune result = (%#v, %v)", pruned, err)
	}
	if result := pruned.HistoryPrune; result.RequestedThrough != first ||
		result.PreviousBaseTransaction != 0 || result.BaseTransaction != first ||
		result.PrunedSegments != 1 || result.PrunedBytes <= 0 ||
		result.CleanupPendingSegments != 0 || result.CleanupPendingBytes != 0 {
		t.Fatalf("history prune details = %#v", result)
	}

	retryTicket, err := manager.ScheduleHistoryPrune(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := retryTicket.Wait(context.Background())
	if err != nil || retried.HistoryPrune == nil ||
		retried.HistoryPrune.PreviousBaseTransaction != first ||
		retried.HistoryPrune.BaseTransaction != first ||
		retried.HistoryPrune.PrunedSegments != 0 ||
		retried.HistoryPrune.PrunedBytes != 0 {
		t.Fatalf("retried history prune = (%#v, %v)", retried, err)
	}

	restoredPath := filepath.Join(root, "restored.kitdb")
	restored, err := kitdb.RestoreToTransaction(
		context.Background(), destination, source+".history", restoredPath, latest,
	)
	if err != nil || restored.AnchorTransaction != first || restored.Transaction != latest {
		t.Fatalf("RestoreToTransaction = (%#v, %v)", restored, err)
	}
	restoredDatabase, err := kitdb.Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"product/1": "anchor", "product/2": "after-anchor", "product/3": "latest",
	} {
		value, found, getErr := restoredDatabase.Get([]byte(key))
		if getErr != nil || !found || string(value) != want {
			_ = restoredDatabase.Close()
			t.Fatalf("restored %q = (%q, %t, %v), want %q", key, value, found, getErr, want)
		}
	}
	if err := restoredDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	stats := manager.Stats()
	if stats.HistoryPruneCompletions != 2 || stats.HistoryPrunedSegments != 1 ||
		stats.HistoryPrunedBytes != pruned.HistoryPrune.PrunedBytes ||
		stats.MaintenanceFailures != 0 {
		t.Fatalf("history prune stats = %#v", stats)
	}
}

func TestManagedHistoryPruneRefusesToPassBackupPin(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	options := kitdb.OpenOptions{RetainHistory: true}
	source := filepath.Join(root, "tenant.kitdb")
	destination := filepath.Join(root, "tenant-backup.kitdb")
	first := commitNodeBackupValue(t, manager, source, options, "product/1", "anchor")
	backupTicket, err := manager.ScheduleBackup(context.Background(), BackupRequest{
		Source: source, Destination: destination, Options: options,
		PinName: "backup/protected", Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	backupResult, err := backupTicket.Wait(context.Background())
	if err != nil || backupResult.Backup == nil ||
		backupResult.Backup.Anchor.Transaction != first {
		t.Fatalf("backup result = (%#v, %v)", backupResult, err)
	}
	second := commitNodeBackupValue(t, manager, source, options, "product/2", "protected")
	checkpointNodeHistory(t, manager, source, options)

	ticket, err := manager.ScheduleHistoryPrune(context.Background(), HistoryPruneRequest{
		Path: source, Through: second, Options: options, Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if !errors.Is(err, kitdb.ErrHistoryPinned) {
		t.Fatalf("history prune beyond backup pin error = %v", err)
	}
	if result.HistoryPrune == nil || result.HistoryPrune.BaseTransaction != 0 ||
		result.HistoryPrune.PrunedSegments != 0 {
		t.Fatalf("blocked history prune result = %#v", result)
	}
	assertNodeBackupPin(
		t, manager, source, options, "backup/protected", backupResult.Backup.Anchor.Cursor(),
	)
	if stats := manager.Stats(); stats.HistoryPruneCompletions != 0 ||
		stats.HistoryPrunedSegments != 0 || stats.MaintenanceFailures != 1 {
		t.Fatalf("blocked history prune stats = %#v", stats)
	}
}

func TestManagedHistoryPruneRequiresRetentionBeforeAdmission(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{})
	path := filepath.Join(root, "tenant.kitdb")
	_, err := manager.ScheduleHistoryPrune(context.Background(), HistoryPruneRequest{
		Path: path, Through: 1, Priority: MaintenanceBackground,
	})
	if !errors.Is(err, ErrHistoryPruneHistoryRequired) {
		t.Fatalf("missing history error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected history prune source stat = %v", err)
	}
	if stats := manager.Stats(); stats.MaintenanceSubmissions != 0 {
		t.Fatalf("rejected history prune entered maintenance queue: %#v", stats)
	}
}

func TestManagedHistoryPruneCoalescesOnlyExactBoundary(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 4,
	})
	options := kitdb.OpenOptions{RetainHistory: true}
	source := filepath.Join(root, "tenant.kitdb")
	lease, err := manager.Acquire(context.Background(), source, options)
	if err != nil {
		t.Fatal(err)
	}
	first := commitNodeBackupTransaction(t, lease.DB(), "product/1", "first")
	firstCursor, err := lease.DB().CurrentCursor()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.DB().Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.DB().SetHistoryPin(context.Background(), "projection/search", firstCursor); err != nil {
		t.Fatal(err)
	}
	second := commitNodeBackupTransaction(t, lease.DB(), "product/2", "second")
	if _, err := lease.DB().Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

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
	request := HistoryPruneRequest{
		Path: source, Through: first, Options: options, Priority: MaintenanceBackground,
	}
	firstTicket, err := manager.ScheduleHistoryPrune(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Priority = MaintenanceUrgent
	joinedTicket, err := manager.ScheduleHistoryPrune(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	differentTicket, err := manager.ScheduleHistoryPrune(context.Background(), HistoryPruneRequest{
		Path: source, Through: second, Options: options, Priority: MaintenanceNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstTicket.task != joinedTicket.task || firstTicket.task.priority != MaintenanceUrgent {
		t.Fatal("identical history prune did not coalesce and promote")
	}
	if firstTicket.task == differentTicket.task {
		t.Fatal("different history prune boundaries were incorrectly coalesced")
	}
	close(release)
	if _, err := blocker.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, ticket := range []*MaintenanceTicket{firstTicket, joinedTicket} {
		result, err := ticket.Wait(context.Background())
		if err != nil || result.HistoryPrune == nil || result.HistoryPrune.BaseTransaction != first {
			t.Fatalf("coalesced history prune = (%#v, %v)", result, err)
		}
	}
	if _, err := differentTicket.Wait(context.Background()); !errors.Is(err, kitdb.ErrHistoryPinned) {
		t.Fatalf("different history prune error = %v, want ErrHistoryPinned", err)
	}
	if stats := manager.Stats(); stats.MaintenanceCoalesced != 1 ||
		stats.HistoryPruneCompletions != 1 || stats.HistoryPrunedSegments != 1 ||
		stats.MaintenanceFailures != 1 {
		t.Fatalf("coalesced history prune stats = %#v", stats)
	}
}

func checkpointNodeHistory(
	t *testing.T,
	manager *Manager,
	path string,
	options kitdb.OpenOptions,
) uint64 {
	t.Helper()
	ticket, err := manager.ScheduleCheckpoint(
		context.Background(), path, options, MaintenanceNormal,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result.Transaction
}
