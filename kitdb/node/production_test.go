package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
)

func TestProductionPolicyCreatesVerifiedBackupRestoreAndPin(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "secret-key", "secret-value")
	store := filepath.Join(root, "backups", "source")
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	config := productionTestConfig("tenant-a", source, store)
	config.StartPaused = true
	health, err := manager.RegisterProductionPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionUnsafe || health.State != ProductionPolicyPaused {
		t.Fatalf("initial health = %#v", health)
	}
	if _, err := os.Stat(store); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registration created the backup store: %v", err)
	}

	cycle, err := manager.RunProductionPolicyOnce(context.Background(), config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if cycle.Backup == nil || cycle.Restore == nil || cycle.Backup.Resumed ||
		cycle.Backup.DatabaseID != cycle.Restore.DatabaseID ||
		cycle.Backup.Transaction != cycle.Restore.Transaction ||
		cycle.Backup.Records != 1 || cycle.Restore.Records != 1 ||
		cycle.Restore.LogicalSHA256 == "" {
		t.Fatalf("production cycle = %#v", cycle)
	}
	health, err = manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionDegraded || !health.Paused ||
		health.Successes != 1 || health.Failures != 0 || health.LastError != "" {
		t.Fatalf("completed health = %#v", health)
	}
	encoded, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{root, source, store, "secret-key", "secret-value"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("health leaked %q: %s", secret, encoded)
		}
	}

	records, err := discoverProductionBackups(context.Background(), store, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].anchor.SHA256 != cycle.Backup.SHA256 {
		t.Fatalf("stored backups = %#v", records)
	}
	assertProductionPin(t, manager, source, config, cycle.Backup)
	stats := manager.Stats()
	if stats.ProductionPolicies != 1 || stats.PausedProductionPolicies != 1 ||
		stats.DegradedProductionPolicies != 1 || stats.UnsafeProductionPolicies != 0 ||
		stats.ProductionPolicySuccesses != 1 || stats.ProductionBackupsCreated != 1 ||
		stats.ProductionRestoreDrills != 1 || stats.ActiveLeases != 0 {
		t.Fatalf("production stats = %#v", stats)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPolicyRecoversFromVerifiedAnchorsAfterRestart(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	store := filepath.Join(root, "backups", "source")
	config := productionTestConfig("tenant-a", source, store)
	config.StartPaused = true

	first := newProductionTestManager(t, ProductionSupervisorLimits{})
	if _, err := first.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	created, err := first.RunProductionPolicyOnce(context.Background(), config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	config.StartPaused = false
	second := newProductionTestManager(t, ProductionSupervisorLimits{})
	if _, err := second.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	health := waitForProductionHealth(t, second, config.Name, func(current ProductionPolicyHealth) bool {
		return current.Successes >= 1
	})
	if health.Readiness != ProductionReady || health.LastBackup == nil ||
		!health.LastBackup.Resumed || health.LastRestore == nil ||
		health.LastBackup.SHA256 != created.Backup.SHA256 {
		t.Fatalf("restarted health = %#v", health)
	}
	records, err := discoverProductionBackups(context.Background(), store, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("restart created duplicate backups: %d", len(records))
	}
	stats := second.Stats()
	if stats.ProductionBackupsCreated != 0 || stats.ProductionBackupsResumed != 1 ||
		stats.ProductionRestoreDrills != 1 || stats.ReadyProductionPolicies != 1 ||
		stats.ActiveLeases != 0 {
		t.Fatalf("restarted stats = %#v", stats)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPolicyRetentionKeepsOnlyNewestVerifiedAnchors(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	store := filepath.Join(root, "backups", "source")
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	config := productionTestConfig("tenant-a", source, store)
	config.StartPaused = true
	config.KeepBackups = 2
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(store, "operator-notes.txt")
	if err := os.WriteFile(unrelated, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4; index++ {
		if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); err != nil {
			t.Fatal(err)
		}
	}
	records, err := discoverProductionBackups(context.Background(), store, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("retained backups = %d, want 2", len(records))
	}
	if stored, err := os.ReadFile(unrelated); err != nil || string(stored) != "preserve" {
		t.Fatalf("unrelated store file = %q, %v", stored, err)
	}
	stats := manager.Stats()
	if stats.ProductionBackupsCreated != 4 || stats.ProductionRestoreDrills != 4 ||
		stats.ProductionBackupsPruned != 2 {
		t.Fatalf("retention stats = %#v", stats)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionReadinessExpiresAtExplicitRPOAndRTO(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	store := filepath.Join(root, "backups", "source")
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	config := productionTestConfig("tenant-a", source, store)
	config.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); err != nil {
		t.Fatal(err)
	}
	manager.productionMu.Lock()
	policy := manager.productionPolicies[config.Name]
	policy.paused = false
	ready := productionPolicyHealthLocked(policy, policy.lastFinishedAt)
	backupExpired := productionPolicyHealthLocked(
		policy, policy.lastBackup.CreatedAt.Add(config.MaxBackupAge+time.Nanosecond),
	)
	restoreExpired := productionPolicyHealthLocked(
		policy, policy.lastRestore.VerifiedAt.Add(config.MaxRestoreAge+time.Nanosecond),
	)
	manager.productionMu.Unlock()
	if ready.Readiness != ProductionReady || backupExpired.Readiness != ProductionUnsafe ||
		restoreExpired.Readiness != ProductionUnsafe {
		t.Fatalf(
			"readiness = ready:%s backup-expired:%s restore-expired:%s",
			ready.Readiness, backupExpired.Readiness, restoreExpired.Readiness,
		)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPolicySchedulesEarliestBackupOrRestoreDeadline(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	store := filepath.Join(root, "backups", "source")
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	config := productionTestConfig("tenant-a", source, store)
	config.StartPaused = true
	config.BackupInterval = 4 * time.Hour
	config.MaxBackupAge = 5 * time.Hour
	config.RestoreInterval = 30 * time.Minute
	config.MaxRestoreAge = time.Hour
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); err != nil {
		t.Fatal(err)
	}
	health, err := manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	want := health.LastRestore.VerifiedAt.Add(config.RestoreInterval)
	backupDue := health.LastBackup.CreatedAt.Add(config.BackupInterval)
	if !health.NextRunAt.Equal(want) || !health.NextRunAt.Before(backupDue) {
		t.Fatalf(
			"next production run = %s, want restore deadline %s before backup %s",
			health.NextRunAt, want, backupDue,
		)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPolicyBoundsBackupStoreBeforeCreatingAnchor(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	store := filepath.Join(root, "backups", "source")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(store, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manager := newProductionTestManager(t, ProductionSupervisorLimits{
		MaxStoreEntries: 2,
	})
	config := productionTestConfig("tenant-a", source, store)
	config.StartPaused = true
	config.KeepBackups = 2
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); !errors.Is(err, ErrProductionStoreLimit) {
		t.Fatalf("bounded store error = %v", err)
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("bounded store changed entry count to %d", len(entries))
	}
	health, err := manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionUnsafe ||
		health.LastError != ErrProductionStoreLimit.Error() {
		t.Fatalf("bounded store health = %#v", health)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPolicyRejectsFutureDatedAnchorEvidence(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	store := filepath.Join(root, "backups", "source")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	futurePath, err := nextProductionBackupPath(store, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	database, err := kitdb.OpenWithOptions(source, kitdb.OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateBackupAnchor(context.Background(), futurePath); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	config := productionTestConfig("tenant-a", source, store)
	config.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); !errors.Is(err, ErrProductionUnsafe) {
		t.Fatalf("future anchor error = %v", err)
	}
	health, err := manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionUnsafe ||
		health.LastError != ErrProductionUnsafe.Error() {
		t.Fatalf("future anchor health = %#v", health)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPolicyRejectsForeignAnchorWithoutTrustingItsEvidence(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "source")
	foreign := createProductionSource(t, root, "foreign", "key", "foreign")
	store := filepath.Join(root, "backups", "source")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	foreignPath, err := nextProductionBackupPath(store, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	database, err := kitdb.OpenWithOptions(foreign, kitdb.OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateBackupAnchor(context.Background(), foreignPath); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	config := productionTestConfig("tenant-a", source, store)
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	health := waitForProductionHealth(t, manager, config.Name, func(current ProductionPolicyHealth) bool {
		return current.Failures >= 1
	})
	if health.Readiness != ProductionUnsafe || health.LastBackup != nil ||
		health.LastError != ErrProductionUnsafe.Error() ||
		manager.Stats().ProductionBackupsResumed != 0 {
		t.Fatalf("foreign anchor health = %#v stats=%#v", health, manager.Stats())
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPolicyFailsClosedOnCorruptStoredAnchorAndRecovers(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	store := filepath.Join(root, "backups", "source")
	config := productionTestConfig("tenant-a", source, store)
	config.StartPaused = true

	first := newProductionTestManager(t, ProductionSupervisorLimits{})
	if _, err := first.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	if _, err := first.RunProductionPolicyOnce(context.Background(), config.Name); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	records, err := discoverProductionBackups(context.Background(), store, 16)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := records[0].anchor.Path
	info, err := os.Stat(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(corrupt, info.Size()-1); err != nil {
		t.Fatal(err)
	}

	second := newProductionTestManager(t, ProductionSupervisorLimits{
		InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	})
	if _, err := second.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	if _, err := second.RunProductionPolicyOnce(context.Background(), config.Name); !errors.Is(err, ErrProductionUnsafe) {
		t.Fatalf("corrupt anchor error = %v", err)
	}
	health, err := second.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionUnsafe || health.Failures != 1 ||
		health.LastError != ErrProductionUnsafe.Error() ||
		strings.Contains(health.LastError, root) {
		t.Fatalf("corrupt health = %#v", health)
	}
	if err := os.Remove(corrupt); err != nil {
		t.Fatal(err)
	}
	if _, err := second.RunProductionPolicyOnce(context.Background(), config.Name); err != nil {
		t.Fatal(err)
	}
	health, err = second.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionDegraded || health.LastError != "" ||
		health.ConsecutiveFailures != 0 {
		t.Fatalf("recovered health = %#v", health)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPolicyValidatesTopologyAndLimits(t *testing.T) {
	root := t.TempDir()
	sourceA := createProductionSource(t, root, "source-a", "key", "a")
	sourceB := createProductionSource(t, root, "source-b", "key", "b")
	storeA := filepath.Join(root, "backups", "a")
	storeB := filepath.Join(root, "backups", "b")
	manager := newProductionTestManager(t, ProductionSupervisorLimits{
		MaxPolicies: 3, MaxConcurrent: 1,
	})
	first := productionTestConfig("tenant-a", sourceA, storeA)
	first.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(first); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterProductionPolicy(first); err != nil {
		t.Fatalf("exact registration was not idempotent: %v", err)
	}
	duplicateSource := productionTestConfig("tenant-b", sourceA, storeB)
	duplicateSource.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(duplicateSource); !errors.Is(err, ErrProductionPolicyTopology) {
		t.Fatalf("duplicate source error = %v", err)
	}
	duplicateStore := productionTestConfig("tenant-b", sourceB, storeA)
	duplicateStore.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(duplicateStore); !errors.Is(err, ErrProductionPolicyTopology) {
		t.Fatalf("duplicate store error = %v", err)
	}
	second := productionTestConfig("tenant-b", sourceB, storeB)
	second.StartPaused = true
	withoutHistory := productionTestConfig("no-history", sourceB, storeB)
	withoutHistory.SourceOptions.RetainHistory = false
	if _, err := manager.RegisterProductionPolicy(withoutHistory); !errors.Is(err, ErrBackupHistoryRequired) {
		t.Fatalf("history error = %v", err)
	}
	insideHistory := productionTestConfig(
		"inside-history", sourceB, filepath.Join(sourceB+".history", "backups"),
	)
	if _, err := manager.RegisterProductionPolicy(insideHistory); err == nil {
		t.Fatal("backup store inside source history was accepted")
	}
	if err := manager.UnregisterProductionPolicy(first.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterProductionPolicy(second); err != nil {
		t.Fatalf("registration after release = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	limited := newProductionTestManager(t, ProductionSupervisorLimits{
		MaxPolicies: 1, MaxConcurrent: 1,
	})
	limitA := productionTestConfig("limit-a", sourceA, filepath.Join(root, "limited", "a"))
	limitA.StartPaused = true
	if _, err := limited.RegisterProductionPolicy(limitA); err != nil {
		t.Fatal(err)
	}
	limitB := productionTestConfig("limit-b", sourceB, filepath.Join(root, "limited", "b"))
	limitB.StartPaused = true
	if _, err := limited.RegisterProductionPolicy(limitB); !errors.Is(err, ErrProductionPolicyLimit) {
		t.Fatalf("policy limit error = %v", err)
	}
	if err := limited.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPoliciesShareBoundedNodeWorkersAndDrain(t *testing.T) {
	root := t.TempDir()
	manager := newProductionTestManager(t, ProductionSupervisorLimits{
		MaxPolicies: 8, MaxConcurrent: 2,
	})
	for index := 0; index < 8; index++ {
		name := "tenant-" + string(rune('a'+index))
		source := createProductionSource(t, root, "source-"+name, "key", name)
		store := filepath.Join(root, "backups", name)
		if _, err := manager.RegisterProductionPolicy(
			productionTestConfig(name, source, store),
		); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		stats := manager.Stats()
		if stats.ProductionPolicySuccesses == 8 &&
			stats.QueuedProductionPolicies == 0 &&
			stats.RunningProductionPolicies == 0 {
			if stats.ReadyProductionPolicies != 8 || stats.ActiveLeases != 0 ||
				stats.MaxConcurrentProductionPolicies != 2 {
				t.Fatalf("settled fleet stats = %#v", stats)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("production fleet did not settle: %#v", stats)
		}
		time.Sleep(time.Millisecond)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func newProductionTestManager(
	t *testing.T,
	production ProductionSupervisorLimits,
) *Manager {
	t.Helper()
	manager, err := NewManager(Limits{
		MaxOpenDatabases: 16, MaxPageCacheBytes: 16 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 4,
		MaxConcurrentMaintenance: 2, MaxQueuedMaintenance: 32,
		MaxMaintenancePerDatabase: 2, MaintenanceTimeout: time.Minute,
		Production: production,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func productionTestConfig(name, source, store string) ProductionPolicyConfig {
	return ProductionPolicyConfig{
		Name: name, Source: source, BackupDirectory: store,
		SourceOptions:   kitdb.OpenOptions{RetainHistory: true},
		BackupInterval:  time.Hour,
		MaxBackupAge:    2 * time.Hour,
		RestoreInterval: time.Hour,
		MaxRestoreAge:   2 * time.Hour,
		KeepBackups:     3,
		Priority:        MaintenanceBackground,
	}
}

func createProductionSource(
	t *testing.T,
	root string,
	name string,
	key string,
	value string,
) string {
	t.Helper()
	directory := filepath.Join(root, "live")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name+".kitdb")
	database, err := kitdb.OpenWithOptions(path, kitdb.OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	commitNodeBackupTransaction(t, database, key, value)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertProductionPin(
	t *testing.T,
	manager *Manager,
	source string,
	config ProductionPolicyConfig,
	backup *ProductionBackupEvidence,
) {
	t.Helper()
	lease, err := manager.Acquire(context.Background(), source, config.SourceOptions)
	if err != nil {
		t.Fatal(err)
	}
	pins, pinErr := lease.DB().HistoryPins()
	releaseErr := lease.Release()
	if pinErr != nil || releaseErr != nil {
		t.Fatal(errors.Join(pinErr, releaseErr))
	}
	wantName := "backup/production/" + config.Name
	for _, pin := range pins {
		if pin.Name == wantName && pin.Cursor.DatabaseID == backup.DatabaseID &&
			pin.Cursor.Transaction == backup.Transaction {
			return
		}
	}
	t.Fatalf("production pin %q at transaction %d not found: %#v", wantName, backup.Transaction, pins)
}

func waitForProductionHealth(
	t *testing.T,
	manager *Manager,
	name string,
	ready func(ProductionPolicyHealth) bool,
) ProductionPolicyHealth {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		health, err := manager.ProductionPolicyHealth(name)
		if err != nil {
			t.Fatal(err)
		}
		if ready(health) {
			return health
		}
		if time.Now().After(deadline) {
			t.Fatalf("production policy %q did not settle: %#v", name, health)
		}
		time.Sleep(time.Millisecond)
	}
}
