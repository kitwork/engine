package work

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

func TestKitDBManagerLeasesSharedHandleAndValidationGate(t *testing.T) {
	root := t.TempDir()
	fleet, err := kitdbnode.NewManager(kitdbnode.Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := newKitDBManager(fleet, false)
	path := filepath.Join(root, "tenant.kitdb")

	first, err := manager.open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.open(context.Background(), path)
	if err != nil {
		first.Release()
		t.Fatal(err)
	}
	if first.database != second.database || first.writeMu != second.writeMu {
		t.Fatal("Kitwork operations did not share the managed handle and relational validation gate")
	}
	if stats := fleet.Stats(); stats.ActiveLeases != 2 || stats.ActiveDatabases != 1 || stats.Opens != 1 {
		t.Fatalf("active fleet stats = %#v", stats)
	}

	first.Release()
	second.Release()
	if stats := fleet.Stats(); stats.ActiveLeases != 0 || stats.IdleDatabases != 1 {
		t.Fatalf("released fleet stats = %#v", stats)
	}
	manager.Close()
	if err := fleet.Close(); err != nil {
		t.Fatal(err)
	}
	direct, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatalf("manager close left writer ownership behind: %v", err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestKitDBWritesUseBoundedCheckpointMaintenance(t *testing.T) {
	root := t.TempDir()
	fleet, err := kitdbnode.NewManager(kitdbnode.Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 4,
		MaxMaintenancePerDatabase: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := newKitDBManager(fleet, false)
	managed, err := manager.open(context.Background(), filepath.Join(root, "tenant.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		managed.Release()
		manager.Close()
		if err := fleet.Close(); err != nil {
			t.Errorf("close fleet: %v", err)
		}
	}()

	commitKitDBTestValue(t, managed.database, "soft", 5<<20)
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		t.Fatal(err)
	}
	waitForKitDBMaintenance(t, fleet, 1)
	if stats := fleet.Stats(); stats.MaintenanceSubmissions != 1 ||
		stats.CheckpointCompletions != 1 {
		t.Fatalf("soft checkpoint stats = %#v", stats)
	}

	commitKitDBTestValue(t, managed.database, "hard", 9<<20)
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		t.Fatal(err)
	}
	stats := fleet.Stats()
	if stats.MaintenanceSubmissions != 2 || stats.CheckpointCompletions != 2 ||
		stats.QueuedMaintenance != 0 || stats.RunningMaintenance != 0 {
		t.Fatalf("hard checkpoint did not finish before return: %#v", stats)
	}
	databaseStats, err := managed.database.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if databaseStats.OverlayMutations != 0 {
		t.Fatalf("checkpoint left %d overlay mutations", databaseStats.OverlayMutations)
	}
}

func TestKitDBBulkWriteUsesLargerBoundedCheckpointWindow(t *testing.T) {
	root := t.TempDir()
	fleet, err := kitdbnode.NewManager(kitdbnode.Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 4,
		MaxMaintenancePerDatabase: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := newKitDBManager(fleet, false)
	managed, err := manager.open(context.Background(), filepath.Join(root, "bulk.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		managed.Release()
		manager.Close()
		if err := fleet.Close(); err != nil {
			t.Errorf("close fleet: %v", err)
		}
	}()

	commitKitDBTestValue(t, managed.database, "bulk", 5<<20)
	if err := checkpointKitDBBeforeBulkWrite(managed); err != nil {
		t.Fatal(err)
	}
	if stats := fleet.Stats(); stats.MaintenanceSubmissions != 0 {
		t.Fatalf("bulk checkpoint ran at the CRUD threshold: %#v", stats)
	}
	databaseStats, err := managed.database.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if databaseStats.OverlayMutations != 1 {
		t.Fatalf("bulk policy flushed %d mutations below its bounded window", databaseStats.OverlayMutations)
	}
}

func TestKitDBBackgroundMaintenanceBoundsItsOwnOverlay(t *testing.T) {
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "maintenance.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	commitKitDBTestValue(t, database, "derived-index-entry", 64)
	checkpointed, err := checkpointKitDBBackgroundMaintenanceWithPolicy(
		context.Background(),
		database,
		kitDBCheckpointPolicy{
			softWALBytes: 1 << 30,
			softChanges:  1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !checkpointed {
		t.Fatal("background maintenance did not checkpoint at its overlay boundary")
	}
	stats, err := database.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.OverlayMutations != 0 || stats.WALBytes > 1<<10 || stats.CheckpointTransaction == 0 {
		t.Fatalf("background checkpoint stats = %#v", stats)
	}

	checkpointed, err = checkpointKitDBBackgroundMaintenanceWithPolicy(
		context.Background(),
		database,
		kitDBCheckpointPolicy{
			softWALBytes: 1 << 30,
			softChanges:  1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if checkpointed {
		t.Fatal("background maintenance checkpointed an empty overlay")
	}
}

func commitKitDBTestValue(t *testing.T, database *kitdbengine.DB, key string, size int) {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte(key), bytes.Repeat([]byte{'x'}, size)); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func waitForKitDBMaintenance(t *testing.T, fleet *kitdbnode.Manager, completions uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fleet.Stats().CheckpointCompletions >= completions {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("checkpoint maintenance did not complete: %#v", fleet.Stats())
}
