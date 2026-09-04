package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestManagedCheckpointAutomaticallyEnforcesHistoryRetention(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	path := filepath.Join(root, "tenant.kitdb")
	options := kitdb.OpenOptions{
		RetainHistory: true,
		HistoryRetention: kitdb.HistoryRetentionPolicy{
			MaxBytes: 1,
		},
	}
	committed := commitNodeBackupValue(t, manager, path, options, "product/1", "value")
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
	if result.Kind != MaintenanceCheckpoint || result.Transaction != committed ||
		result.HistoryRetention == nil ||
		result.HistoryRetention.AppliedThrough != committed ||
		result.HistoryRetention.Prune.PrunedSegments != 1 ||
		result.HistoryRetention.RetainedSegments != 0 {
		t.Fatalf("checkpoint retention result = %#v", result)
	}
	stats := manager.Stats()
	if stats.CheckpointCompletions != 1 || stats.HistoryRetentionEvaluations != 1 ||
		stats.HistoryRetentionCompletions != 0 || stats.HistoryPrunedSegments != 1 ||
		stats.HistoryRetentionLimitedByPins != 0 || stats.MaintenanceFailures != 0 {
		t.Fatalf("checkpoint retention stats = %#v", stats)
	}
}

func TestManagedHistoryRetentionReportsPinPressureWithoutFailing(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 1})
	path := filepath.Join(root, "tenant.kitdb")
	options := kitdb.OpenOptions{
		RetainHistory: true,
		HistoryRetention: kitdb.HistoryRetentionPolicy{
			MaxBytes: 1,
		},
	}
	lease, err := manager.Acquire(context.Background(), path, options)
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
	if _, err := lease.DB().SetHistoryPin(
		context.Background(), "projection/search", firstCursor,
	); err != nil {
		t.Fatal(err)
	}
	commitNodeBackupTransaction(t, lease.DB(), "product/2", "second")
	if _, err := lease.DB().Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	ticket, err := manager.ScheduleHistoryRetention(
		context.Background(),
		HistoryRetentionRequest{
			Path: path, Options: options, Priority: MaintenanceBackground,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	retention := result.HistoryRetention
	if result.Kind != MaintenanceHistoryRetention || result.Transaction != first ||
		retention == nil || !retention.LimitedByPin ||
		retention.PinnedBy != "projection/search" || retention.PinnedAt != first ||
		retention.Prune.PrunedSegments != 1 || retention.RetainedSegments != 1 ||
		retention.BytesOverLimit <= 0 {
		t.Fatalf("managed retention result = %#v", result)
	}
	stats := manager.Stats()
	if stats.HistoryRetentionCompletions != 1 || stats.HistoryRetentionEvaluations != 1 ||
		stats.HistoryRetentionLimitedByPins != 1 ||
		stats.HistoryRetentionBytesOverLimit != retention.BytesOverLimit ||
		stats.HistoryPrunedSegments != 1 || stats.MaintenanceFailures != 0 {
		t.Fatalf("managed retention stats = %#v", stats)
	}
}

func TestManagedHistoryRetentionRequiresPolicyBeforeAdmission(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{})
	path := filepath.Join(root, "tenant.kitdb")
	_, err := manager.ScheduleHistoryRetention(
		context.Background(),
		HistoryRetentionRequest{
			Path: path, Options: kitdb.OpenOptions{RetainHistory: true},
		},
	)
	if !errors.Is(err, ErrHistoryRetentionPolicyRequired) {
		t.Fatalf("missing retention policy error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected retention source stat = %v", err)
	}
}
