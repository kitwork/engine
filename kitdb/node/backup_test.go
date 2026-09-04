package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestVerifiedBackupCreatesAnchorSealsHistoryAndPins(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	options := kitdb.OpenOptions{RetainHistory: true}
	source := filepath.Join(root, "tenant.kitdb")
	destination := filepath.Join(root, "tenant-backup.kitdb")
	committed := commitNodeBackupValue(t, manager, source, options, "product/1", "Kitwork")
	request := BackupRequest{
		Source: source, Destination: destination, Options: options,
		PinName: "backup/daily", Priority: MaintenanceBackground,
	}

	ticket, err := manager.ScheduleBackup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != MaintenanceBackup || result.Transaction != committed || result.Backup == nil {
		t.Fatalf("backup maintenance result = %#v", result)
	}
	backup := *result.Backup
	if backup.Resumed || backup.Anchor.Transaction != committed ||
		backup.Anchor.DatabaseID == "" || backup.Anchor.SHA256 == "" ||
		backup.CheckpointTransaction < committed || backup.Pin.Name != request.PinName ||
		backup.Pin.Cursor != backup.Anchor.Cursor() {
		t.Fatalf("verified backup = %#v", backup)
	}
	verified, err := kitdb.VerifyBackupAnchor(context.Background(), destination)
	if err != nil || verified != backup.Anchor {
		t.Fatalf("VerifyBackupAnchor = (%#v, %v), want %#v", verified, err, backup.Anchor)
	}
	assertNodeBackupPin(t, manager, source, options, request.PinName, backup.Anchor.Cursor())
	later := commitNodeBackupValue(t, manager, source, options, "product/2", "after-anchor")

	retry, err := manager.ScheduleBackup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := retry.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if retried.Backup == nil || !retried.Backup.Resumed ||
		retried.Backup.Anchor != backup.Anchor || retried.Backup.Pin != backup.Pin ||
		retried.Backup.CheckpointTransaction < later {
		t.Fatalf("retried backup = %#v, want resumed %#v", retried.Backup, backup)
	}
	restoredPath := filepath.Join(root, "restored.kitdb")
	restored, err := kitdb.RestoreToTransaction(
		context.Background(), destination, source+".history", restoredPath, later,
	)
	if err != nil || restored.Transaction != later || restored.AnchorTransaction != committed {
		t.Fatalf("RestoreToTransaction = (%#v, %v)", restored, err)
	}
	restoredDatabase, err := kitdb.Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"product/1": "Kitwork", "product/2": "after-anchor"} {
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
	if stats.BackupCompletions != 2 || stats.BackupResumptions != 1 ||
		stats.MaintenanceFailures != 0 {
		t.Fatalf("backup stats = %#v", stats)
	}
}

func TestVerifiedBackupResumesPublishedAnchorWithoutPin(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	options := kitdb.OpenOptions{RetainHistory: true}
	source := filepath.Join(root, "tenant.kitdb")
	destination := filepath.Join(root, "partial-backup.kitdb")
	lease, err := manager.Acquire(context.Background(), source, options)
	if err != nil {
		t.Fatal(err)
	}
	committed := commitNodeBackupTransaction(t, lease.DB(), "product/1", "before-crash")
	anchor, err := lease.DB().CreateBackupAnchor(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	ticket, err := manager.ScheduleBackup(context.Background(), BackupRequest{
		Source: source, Destination: destination, Options: options,
		PinName: "backup/crash-retry", Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Backup == nil || !result.Backup.Resumed ||
		result.Backup.Anchor != anchor || result.Backup.CheckpointTransaction < committed {
		t.Fatalf("resumed partial backup = %#v", result.Backup)
	}
	assertNodeBackupPin(
		t, manager, source, options, "backup/crash-retry", anchor.Cursor(),
	)
}

func TestVerifiedBackupRejectsForeignExistingAnchor(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	options := kitdb.OpenOptions{RetainHistory: true}
	firstSource := filepath.Join(root, "first.kitdb")
	secondSource := filepath.Join(root, "second.kitdb")
	destination := filepath.Join(root, "occupied.kitdb")
	first, err := manager.Acquire(context.Background(), firstSource, options)
	if err != nil {
		t.Fatal(err)
	}
	commitNodeBackupTransaction(t, first.DB(), "owner", "first")
	foreign, err := first.DB().CreateBackupAnchor(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	commitNodeBackupValue(t, manager, secondSource, options, "owner", "second")

	ticket, err := manager.ScheduleBackup(context.Background(), BackupRequest{
		Source: secondSource, Destination: destination, Options: options,
		PinName: "backup/foreign", Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if !errors.Is(err, ErrBackupSourceMismatch) {
		t.Fatalf("foreign backup error = %v", err)
	}
	if result.Backup == nil || !result.Backup.Resumed || result.Backup.Anchor != foreign {
		t.Fatalf("foreign backup evidence = %#v", result.Backup)
	}
	lease, err := manager.Acquire(context.Background(), secondSource, options)
	if err != nil {
		t.Fatal(err)
	}
	pins, pinErr := lease.DB().HistoryPins()
	if releaseErr := lease.Release(); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if pinErr != nil || len(pins) != 0 {
		t.Fatalf("foreign backup created pins = (%#v, %v)", pins, pinErr)
	}
	if stats := manager.Stats(); stats.BackupCompletions != 0 ||
		stats.BackupResumptions != 0 || stats.MaintenanceFailures != 1 {
		t.Fatalf("foreign backup stats = %#v", stats)
	}
}

func TestVerifiedBackupRejectsMissingHistoryBeforeAdmission(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{})
	destination := filepath.Join(root, "backup.kitdb")
	_, err := manager.ScheduleBackup(context.Background(), BackupRequest{
		Source: filepath.Join(root, "tenant.kitdb"), Destination: destination,
		PinName: "backup/daily", Priority: MaintenanceBackground,
	})
	if !errors.Is(err, ErrBackupHistoryRequired) {
		t.Fatalf("missing history error = %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected backup destination stat = %v", err)
	}
	if stats := manager.Stats(); stats.MaintenanceSubmissions != 0 {
		t.Fatalf("rejected backup entered maintenance queue: %#v", stats)
	}
}

func TestVerifiedBackupDestinationReservationDoesNotConsumeHandleCapacity(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20,
		MaxConcurrentOpens: 1, MaxConcurrentMaintenance: 1,
	})
	options := kitdb.OpenOptions{RetainHistory: true}
	source := filepath.Join(root, "source.kitdb")
	destination := filepath.Join(root, "backup.kitdb")
	commitNodeBackupValue(t, manager, source, options, "base", "value")
	ticket, err := manager.ScheduleBackup(context.Background(), BackupRequest{
		Source: source, Destination: destination, Options: options,
		PinName: "backup/capacity", Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil || result.Backup == nil {
		t.Fatalf("single-handle backup = (%#v, %v)", result, err)
	}
	if stats := manager.Stats(); stats.ManagedDatabases != 1 ||
		stats.ReservedPageCacheBytes != 1<<20 {
		t.Fatalf("backup destination consumed handle capacity: %#v", stats)
	}
}

func TestVerifiedBackupCoalescesAndPromotesIdenticalRequest(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 4,
	})
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
	request := BackupRequest{
		Source:      filepath.Join(root, "tenant.kitdb"),
		Destination: filepath.Join(root, "backup.kitdb"),
		Options:     kitdb.OpenOptions{RetainHistory: true},
		PinName:     "backup/coalesced", Priority: MaintenanceBackground,
	}
	first, err := manager.ScheduleBackup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Priority = MaintenanceUrgent
	second, err := manager.ScheduleBackup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.task != second.task || first.task.priority != MaintenanceUrgent {
		t.Fatal("identical backup did not coalesce and promote")
	}
	close(release)
	if _, err := blocker.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, ticket := range []*MaintenanceTicket{first, second} {
		result, err := ticket.Wait(context.Background())
		if err != nil || result.Backup == nil {
			t.Fatalf("coalesced backup = (%#v, %v)", result, err)
		}
	}
	if stats := manager.Stats(); stats.MaintenanceCoalesced != 1 ||
		stats.BackupCompletions != 1 {
		t.Fatalf("coalesced backup stats = %#v", stats)
	}
}

func commitNodeBackupValue(
	t *testing.T,
	manager *Manager,
	path string,
	options kitdb.OpenOptions,
	key string,
	value string,
) uint64 {
	t.Helper()
	lease, err := manager.Acquire(context.Background(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	transaction := commitNodeBackupTransaction(t, lease.DB(), key, value)
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	return transaction
}

func commitNodeBackupTransaction(
	t *testing.T,
	database *kitdb.DB,
	key string,
	value string,
) uint64 {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte(key), []byte(value)); err != nil {
		t.Fatal(err)
	}
	committed, err := transaction.Commit()
	if err != nil {
		t.Fatal(err)
	}
	return committed
}

func assertNodeBackupPin(
	t *testing.T,
	manager *Manager,
	path string,
	options kitdb.OpenOptions,
	name string,
	cursor kitdb.HistoryCursor,
) {
	t.Helper()
	lease, err := manager.Acquire(context.Background(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	pins, pinErr := lease.DB().HistoryPins()
	if releaseErr := lease.Release(); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if pinErr != nil || len(pins) != 1 || pins[0].Name != name || pins[0].Cursor != cursor {
		t.Fatalf("backup pins = (%#v, %v), want %q at %#v", pins, pinErr, name, cursor)
	}
}
