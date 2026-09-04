package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

func TestEngineOwnsOneBoundedKitDBNodeManager(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const alpha = struct({ id: id(), name: text().notNull() });
const beta = struct({ id: id(), name: text().notNull() });
const first = kitdb("first.kitdb", { alpha });
const second = kitdb("second.kitdb", { beta });
router.get((ctx) => {
  if (ctx.query("db") === "second") {
    second.beta.create({ name: "second" });
    return ctx.text("second");
  }
  first.alpha.create({ name: "first" });
  return ctx.text("first");
});`
	if err := os.WriteFile(filepath.Join(directory, "router.kitwork.js"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(root, 0, false, "")
	if err := engine.SetKitDBNodeLimits(kitdbnode.Limits{
		MaxOpenDatabases: 1, MaxPageCacheBytes: 64 << 10,
		DefaultPageCacheBytes: 64 << 10, MaxConcurrentOpens: 1,
	}); err != nil {
		engine.Close()
		t.Fatal(err)
	}

	requestKitDBNodeRoute(t, engine, "/", "first")
	stats := engine.KitDBNodeStats()
	if stats.ManagedDatabases != 1 || stats.IdleDatabases != 1 || stats.ActiveLeases != 0 ||
		stats.ReservedPageCacheBytes != 64<<10 || stats.Opens != 1 {
		engine.Close()
		t.Fatalf("first request node stats = %#v", stats)
	}
	requestKitDBNodeRoute(t, engine, "/?db=second", "second")
	stats = engine.KitDBNodeStats()
	if stats.ManagedDatabases != 1 || stats.IdleDatabases != 1 || stats.Evictions != 1 || stats.Opens != 2 {
		engine.Close()
		t.Fatalf("second request node stats = %#v", stats)
	}

	firstPath := filepath.Join(directory, ".data", "first.kitdb")
	first, err := kitdbengine.Open(firstPath)
	if err != nil {
		engine.Close()
		t.Fatalf("LRU did not release the first database: %v", err)
	}
	if err := first.Close(); err != nil {
		engine.Close()
		t.Fatal(err)
	}
	if err := engine.SetKitDBNodeLimits(kitdbnode.Limits{}); err == nil || !strings.Contains(err.Error(), "before loading") {
		engine.Close()
		t.Fatalf("late node limit change error = %v", err)
	}

	secondPath := filepath.Join(directory, ".data", "second.kitdb")
	if _, err := kitdbengine.Open(secondPath); !errors.Is(err, kitdbengine.ErrWriterLocked) {
		engine.Close()
		t.Fatalf("active host manager did not retain second writer lock: %v", err)
	}
	engine.Close()
	if stats := engine.KitDBNodeStats(); !stats.Closed || stats.ManagedDatabases != 0 {
		t.Fatalf("closed engine node stats = %#v", stats)
	}
	second, err := kitdbengine.Open(secondPath)
	if err != nil {
		t.Fatalf("engine close left KitDB writer ownership behind: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEngineSchedulesHostTrustedVerifiedKitDBBackup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "tenant.kitdb")
	destination := filepath.Join(root, "tenant-backup.kitdb")
	options := kitdbengine.OpenOptions{RetainHistory: true}
	database, err := kitdbengine.OpenWithOptions(source, options)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
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
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	engine := New(root, 0, false, "")
	defer engine.Close()
	ticket, err := engine.ScheduleKitDBBackup(context.Background(), kitdbnode.BackupRequest{
		Source: source, Destination: destination, Options: options,
		PinName: "backup/engine", Priority: kitdbnode.MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Backup == nil || result.Backup.Anchor.Transaction != committed ||
		result.Backup.Pin.Cursor != result.Backup.Anchor.Cursor() {
		t.Fatalf("engine backup result = %#v", result)
	}
	if _, err := kitdbengine.VerifyBackupAnchor(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	lease, err := engine.kitDBNodeManager.Acquire(context.Background(), source, options)
	if err != nil {
		t.Fatal(err)
	}
	laterTransaction, err := lease.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := laterTransaction.Put([]byte("product/2"), []byte("replicated")); err != nil {
		t.Fatal(err)
	}
	later, err := laterTransaction.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	catchUpTicket, err := engine.ScheduleKitDBReplicaCatchUp(
		context.Background(),
		kitdbnode.ReplicaCatchUpRequest{
			Source: source, Target: destination, SourceOptions: options,
			PinName: "backup/engine", Priority: kitdbnode.MaintenanceBackground,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	catchUp, err := catchUpTicket.Wait(context.Background())
	if err != nil || catchUp.ReplicaCatchUp == nil ||
		catchUp.ReplicaCatchUp.To.Transaction != later ||
		catchUp.ReplicaCatchUp.AppliedTransactions != 1 {
		t.Fatalf("engine replica catch-up result = (%#v, %v)", catchUp, err)
	}
	replicaOptions := kitdbengine.OpenOptions{Replica: true}
	replicaLease, err := engine.kitDBNodeManager.Acquire(
		context.Background(), destination, replicaOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	replicated, found, getErr := replicaLease.DB().Get([]byte("product/2"))
	if releaseErr := replicaLease.Release(); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if getErr != nil || !found || string(replicated) != "replicated" {
		t.Fatalf("engine replica product/2 = (%q, %t, %v)", replicated, found, getErr)
	}
	pruneTicket, err := engine.ScheduleKitDBHistoryPrune(
		context.Background(),
		kitdbnode.HistoryPruneRequest{
			Path: source, Through: later, Options: options,
			Priority: kitdbnode.MaintenanceBackground,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	pruned, err := pruneTicket.Wait(context.Background())
	if err != nil || pruned.HistoryPrune == nil ||
		pruned.HistoryPrune.BaseTransaction != later ||
		pruned.HistoryPrune.PrunedSegments != 2 {
		t.Fatalf("engine history prune result = (%#v, %v)", pruned, err)
	}
	if stats := engine.KitDBNodeStats(); stats.BackupCompletions != 1 ||
		stats.HistoryPruneCompletions != 1 || stats.HistoryPrunedSegments != 2 ||
		stats.ReplicaCatchUpCompletions != 1 || stats.ReplicaTransactionsApplied != 1 ||
		stats.MaintenanceFailures != 0 {
		t.Fatalf("engine backup/prune stats = %#v", stats)
	}
}

func TestEngineOwnsHostTrustedKitDBProductionSupervisor(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "live")
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(live, "tenant.kitdb")
	store := filepath.Join(root, "backups", "tenant")
	publishedStore := filepath.Join(root, "published", "tenant")
	if err := os.MkdirAll(publishedStore, 0o700); err != nil {
		t.Fatal(err)
	}
	options := kitdbengine.OpenOptions{RetainHistory: true}
	database, err := kitdbengine.OpenWithOptions(source, options)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("product/1"), []byte("Kitwork")); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	engine := New(root, 0, false, "")
	publisher, err := kitdbnode.NewVerifiedDirectoryPublisher(
		publishedStore,
		kitdbnode.VerifiedDirectoryPublisherOptions{KeepAnchors: 3},
	)
	if err != nil {
		engine.Close()
		t.Fatal(err)
	}
	if err := engine.RegisterKitDBProductionAnchorPublisher("offhost", publisher); err != nil {
		engine.Close()
		t.Fatal(err)
	}
	if publishers := engine.KitDBProductionAnchorPublishers(); len(publishers) != 1 || publishers[0] != "offhost" {
		engine.Close()
		t.Fatalf("engine production publishers = %#v", publishers)
	}
	config := kitdbnode.ProductionPolicyConfig{
		Name: "tenant", Source: source, BackupDirectory: store,
		Publisher:     "offhost",
		SourceOptions: options, BackupInterval: time.Hour,
		MaxBackupAge: 2 * time.Hour, RestoreInterval: time.Hour,
		MaxRestoreAge: 2 * time.Hour, KeepBackups: 3,
		Priority: kitdbnode.MaintenanceBackground, StartPaused: true,
	}
	health, err := engine.RegisterKitDBProductionPolicy(config)
	if err != nil {
		engine.Close()
		t.Fatal(err)
	}
	if health.Readiness != kitdbnode.ProductionUnsafe || !health.Paused {
		engine.Close()
		t.Fatalf("initial production health = %#v", health)
	}
	cycle, err := engine.RunKitDBProductionPolicyOnce(context.Background(), config.Name)
	if err != nil {
		engine.Close()
		t.Fatal(err)
	}
	policies := engine.KitDBProductionPolicies()
	if cycle.Backup == nil || cycle.Publication == nil || cycle.Restore == nil ||
		cycle.Publication.SHA256 != cycle.Backup.SHA256 || len(policies) != 1 ||
		policies[0].Readiness != kitdbnode.ProductionDegraded ||
		engine.KitDBNodeStats().ProductionPolicySuccesses != 1 ||
		engine.KitDBNodeStats().ProductionAnchorsPublished != 1 {
		engine.Close()
		t.Fatalf("engine production cycle = %#v policies=%#v", cycle, policies)
	}
	named, err := engine.KitDBProductionPolicyHealth(config.Name)
	if err != nil || named.LastBackup == nil || named.LastPublication == nil ||
		named.LastRestore == nil || !named.PublicationRequired {
		engine.Close()
		t.Fatalf("engine named production health = %#v, %v", named, err)
	}
	if err := engine.UnregisterKitDBProductionAnchorPublisher("offhost"); !errors.Is(err, kitdbnode.ErrProductionPublisherBusy) {
		engine.Close()
		t.Fatalf("owned production publisher unregister error = %v", err)
	}
	if err := engine.WakeKitDBProductionPolicy(config.Name); err != nil {
		engine.Close()
		t.Fatal(err)
	}
	if _, err := engine.ResumeKitDBProductionPolicy(config.Name); err != nil {
		engine.Close()
		t.Fatal(err)
	}
	if _, err := engine.PauseKitDBProductionPolicy(config.Name); err != nil {
		engine.Close()
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		err = engine.UnregisterKitDBProductionPolicy(config.Name)
		if err == nil {
			break
		}
		if !errors.Is(err, kitdbnode.ErrProductionPolicyBusy) || time.Now().After(deadline) {
			engine.Close()
			t.Fatalf("unregister production policy: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if len(engine.KitDBProductionPolicies()) != 0 {
		engine.Close()
		t.Fatal("unregistered production policy remains visible")
	}
	if err := engine.UnregisterKitDBProductionAnchorPublisher("offhost"); err != nil {
		engine.Close()
		t.Fatal(err)
	}
	if len(engine.KitDBProductionAnchorPublishers()) != 0 {
		engine.Close()
		t.Fatal("unregistered production publisher remains visible")
	}
	if _, err := engine.KitDBProductionPolicyHealth(config.Name); !errors.Is(err, kitdbnode.ErrProductionPolicyNotFound) {
		engine.Close()
		t.Fatalf("unregistered production policy health error = %v", err)
	}
	engine.Close()
	if _, err := engine.RegisterKitDBProductionPolicy(config); !errors.Is(err, kitdbnode.ErrClosed) {
		t.Fatalf("closed engine production registration error = %v", err)
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("engine production backups = %d, want 1", len(entries))
	}
	if _, err := kitdbengine.VerifyBackupAnchor(
		context.Background(), filepath.Join(store, entries[0].Name()),
	); err != nil {
		t.Fatal(err)
	}
	publishedEntries, err := os.ReadDir(publishedStore)
	if err != nil {
		t.Fatal(err)
	}
	if len(publishedEntries) != 1 {
		t.Fatalf("engine published production backups = %d, want 1", len(publishedEntries))
	}
	if _, err := kitdbengine.VerifyBackupAnchor(
		context.Background(), filepath.Join(publishedStore, publishedEntries[0].Name()),
	); err != nil {
		t.Fatal(err)
	}
	reopened, err := kitdbengine.Open(source)
	if err != nil {
		t.Fatalf("engine close left production source locked: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func requestKitDBNodeRoute(t *testing.T, engine *Engine, target, body string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://localhost"+target, nil))
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != body {
		t.Fatalf("GET %s = %d %q, want 200 %q", target, recorder.Code, recorder.Body.String(), body)
	}
}
