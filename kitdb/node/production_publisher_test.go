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

type productionAnchorPublisherFunc func(
	context.Context,
	kitdb.BackupAnchor,
) (ProductionAnchorReceipt, error)

func (publisher productionAnchorPublisherFunc) Publish(
	ctx context.Context,
	anchor kitdb.BackupAnchor,
) (ProductionAnchorReceipt, error) {
	return publisher(ctx, anchor)
}

func TestProductionPublisherRegistryAndDirectoryOptionsAreBounded(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "published")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	compact, err := NewVerifiedDirectoryPublisher(
		parent,
		VerifiedDirectoryPublisherOptions{MaxEntries: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	if compact.keepAnchors != 2 {
		t.Fatalf("compact default retention = %d, want 2", compact.keepAnchors)
	}
	if _, err := NewVerifiedDirectoryPublisher(
		parent,
		VerifiedDirectoryPublisherOptions{MaxEntries: 2},
	); err == nil {
		t.Fatal("directory publisher accepted an unbounded retention shape")
	}
	regularFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(regularFile, []byte("not a store"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewVerifiedDirectoryPublisher(
		regularFile, VerifiedDirectoryPublisherOptions{},
	); err == nil {
		t.Fatal("directory publisher accepted a regular file root")
	}

	limited := newProductionTestManager(t, ProductionSupervisorLimits{
		MaxPolicies: 1, MaxConcurrent: 1,
	})
	stub := productionAnchorPublisherFunc(func(
		_ context.Context,
		anchor kitdb.BackupAnchor,
	) (ProductionAnchorReceipt, error) {
		return productionAnchorReceipt(anchor, false, 0), nil
	})
	if err := limited.RegisterProductionAnchorPublisher("first", stub); err != nil {
		t.Fatal(err)
	}
	if err := limited.RegisterProductionAnchorPublisher("second", stub); !errors.Is(err, ErrProductionPublisherLimit) {
		t.Fatalf("publisher registry limit error = %v", err)
	}
	for _, name := range []string{"", "../secret", "has space", "token:value"} {
		if err := limited.RegisterProductionAnchorPublisher(name, stub); err == nil {
			t.Fatalf("publisher accepted unsafe label %q", name)
		}
	}
	if err := limited.Close(); err != nil {
		t.Fatal(err)
	}

	topology := newProductionTestManager(t, ProductionSupervisorLimits{
		MaxPolicies: 3, MaxConcurrent: 1,
	})
	parentPublisher, err := NewVerifiedDirectoryPublisher(
		parent, VerifiedDirectoryPublisherOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	childPublisher, err := NewVerifiedDirectoryPublisher(
		child, VerifiedDirectoryPublisherOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := topology.RegisterProductionAnchorPublisher("parent", parentPublisher); err != nil {
		t.Fatal(err)
	}
	if err := topology.RegisterProductionAnchorPublisher("child", childPublisher); !errors.Is(err, ErrProductionPublisherTopology) {
		t.Fatalf("overlapping publisher destination error = %v", err)
	}
	if err := topology.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifiedDirectoryPublisherPublishesIdempotentlyAndRetains(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key/0", "value/0")
	localStore := filepath.Join(root, "local", "source")
	remoteStore := filepath.Join(root, "remote", "source")
	if err := os.MkdirAll(remoteStore, 0o700); err != nil {
		t.Fatal(err)
	}
	publisher, err := NewVerifiedDirectoryPublisher(
		remoteStore,
		VerifiedDirectoryPublisherOptions{MaxEntries: 4, KeepAnchors: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	if err := manager.RegisterProductionAnchorPublisher("offhost", publisher); err != nil {
		t.Fatal(err)
	}
	config := productionTestConfig("tenant-a", source, localStore)
	config.Publisher = "offhost"
	config.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4; index++ {
		if index != 0 {
			commitNodeBackupValue(
				t, manager, source, config.SourceOptions,
				"key/"+string(rune('0'+index)), "value",
			)
		}
		cycle, err := manager.RunProductionPolicyOnce(context.Background(), config.Name)
		if err != nil {
			t.Fatal(err)
		}
		if cycle.Backup == nil || cycle.Publication == nil || cycle.Restore == nil ||
			cycle.Publication.Resumed ||
			cycle.Publication.SHA256 != cycle.Backup.SHA256 {
			t.Fatalf("published cycle %d = %#v", index, cycle)
		}
	}
	entries, err := os.ReadDir(remoteStore)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("published retention entries = %d, want 2", len(entries))
	}
	for _, entry := range entries {
		if _, err := kitdb.VerifyBackupAnchor(
			context.Background(), filepath.Join(remoteStore, entry.Name()),
		); err != nil {
			t.Fatal(err)
		}
	}

	cycle, err := manager.RunProductionPolicyOnce(context.Background(), config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if cycle.Publication == nil || !cycle.Publication.Resumed {
		t.Fatalf("idempotent publication = %#v", cycle.Publication)
	}
	health, err := manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionDegraded || !health.PublicationRequired ||
		health.LastPublication == nil ||
		health.LastPublication.SHA256 != health.LastBackup.SHA256 {
		t.Fatalf("publication health = %#v", health)
	}
	encoded, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{root, source, localStore, remoteStore} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("publication health leaked %q: %s", secret, encoded)
		}
	}
	stats := manager.Stats()
	if stats.ProductionPublishers != 1 ||
		stats.ProductionPublicationAttempts != 5 ||
		stats.ProductionPublicationFailures != 0 ||
		stats.ProductionAnchorsPublished != 4 ||
		stats.ProductionAnchorsResumed != 1 ||
		stats.ProductionPublishedAnchorsPruned != 2 ||
		stats.ProductionPublishedBytes == 0 {
		t.Fatalf("publication stats = %#v", stats)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifiedDirectoryPublisherRejectsForeignIdentityAndBackwardAnchor(t *testing.T) {
	root := t.TempDir()
	remoteStore := filepath.Join(root, "remote")
	if err := os.MkdirAll(remoteStore, 0o700); err != nil {
		t.Fatal(err)
	}
	publisher, err := NewVerifiedDirectoryPublisher(
		remoteStore, VerifiedDirectoryPublisherOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	sourceA := createProductionSource(t, root, "source-a", "key/1", "one")
	databaseA, err := kitdb.OpenWithOptions(
		sourceA, kitdb.OpenOptions{RetainHistory: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	older, err := databaseA.CreateBackupAnchor(
		context.Background(), filepath.Join(root, "older.kitdb"),
	)
	if err != nil {
		t.Fatal(err)
	}
	commitNodeBackupTransaction(t, databaseA, "key/2", "two")
	newer, err := databaseA.CreateBackupAnchor(
		context.Background(), filepath.Join(root, "newer.kitdb"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseA.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(context.Background(), older); !errors.Is(err, ErrProductionUnsafe) {
		t.Fatalf("backward publication error = %v", err)
	}

	sourceB := createProductionSource(t, root, "source-b", "key", "foreign")
	databaseB, err := kitdb.OpenWithOptions(
		sourceB, kitdb.OpenOptions{RetainHistory: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := databaseB.CreateBackupAnchor(
		context.Background(), filepath.Join(root, "foreign.kitdb"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := databaseB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(context.Background(), foreign); !errors.Is(err, ErrProductionUnsafe) {
		t.Fatalf("foreign publication error = %v", err)
	}
	entries, err := os.ReadDir(remoteStore)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("unsafe publication changed remote store: %d entries", len(entries))
	}
}

func TestVerifiedDirectoryPublisherCleansAbandonedRestoreStaging(t *testing.T) {
	root := t.TempDir()
	remoteStore := filepath.Join(root, "remote")
	if err := os.MkdirAll(remoteStore, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		".kitdb-restore-abandoned-a.tmp",
		".kitdb-restore-abandoned-b.tmp",
	} {
		if err := os.WriteFile(
			filepath.Join(remoteStore, name), []byte("partial"), 0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	publisher, err := NewVerifiedDirectoryPublisher(
		remoteStore,
		VerifiedDirectoryPublisherOptions{MaxEntries: 3, KeepAnchors: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	source := createProductionSource(t, root, "source", "key", "value")
	database, err := kitdb.OpenWithOptions(
		source, kitdb.OpenOptions{RetainHistory: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := database.CreateBackupAnchor(
		context.Background(), filepath.Join(root, "anchor.kitdb"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(context.Background(), anchor); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(remoteStore)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || isDirectoryPublisherRestoreStaging(entries[0].Name()) {
		t.Fatalf("publisher staging recovery entries = %#v", entries)
	}
	if _, err := kitdb.VerifyBackupAnchor(
		context.Background(), filepath.Join(remoteStore, entries[0].Name()),
	); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPublisherRecoversPublishedAnchorAfterManagerRestart(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	localStore := filepath.Join(root, "local", "source")
	remoteStore := filepath.Join(root, "remote", "source")
	if err := os.MkdirAll(remoteStore, 0o700); err != nil {
		t.Fatal(err)
	}
	config := productionTestConfig("tenant-a", source, localStore)
	config.Publisher = "offhost"
	config.StartPaused = true

	first := newProductionTestManager(t, ProductionSupervisorLimits{})
	firstPublisher, err := NewVerifiedDirectoryPublisher(remoteStore, VerifiedDirectoryPublisherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.RegisterProductionAnchorPublisher(config.Publisher, firstPublisher); err != nil {
		t.Fatal(err)
	}
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
	secondPublisher, err := NewVerifiedDirectoryPublisher(remoteStore, VerifiedDirectoryPublisherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.RegisterProductionAnchorPublisher(config.Publisher, secondPublisher); err != nil {
		t.Fatal(err)
	}
	if _, err := second.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	health := waitForProductionHealth(t, second, config.Name, func(current ProductionPolicyHealth) bool {
		return current.Successes >= 1
	})
	if health.Readiness != ProductionReady || health.LastPublication == nil ||
		!health.LastPublication.Resumed ||
		health.LastPublication.SHA256 != created.Publication.SHA256 ||
		second.Stats().ProductionAnchorsResumed != 1 {
		t.Fatalf("restarted publication health = %#v stats=%#v", health, second.Stats())
	}
	entries, err := os.ReadDir(remoteStore)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("restart duplicated published anchor: %d", len(entries))
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPublisherFailureRetriesWithoutTrustingMissingEvidence(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	calls := 0
	publisher := productionAnchorPublisherFunc(func(
		_ context.Context,
		anchor kitdb.BackupAnchor,
	) (ProductionAnchorReceipt, error) {
		calls++
		if calls == 1 {
			return ProductionAnchorReceipt{}, errors.New("remote unavailable")
		}
		return productionAnchorReceipt(anchor, false, 0), nil
	})
	if err := manager.RegisterProductionAnchorPublisher("offhost", publisher); err != nil {
		t.Fatal(err)
	}
	config := productionTestConfig("tenant-a", source, filepath.Join(root, "local"))
	config.Publisher = "offhost"
	config.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); !errors.Is(err, ErrProductionPublication) {
		t.Fatalf("first publication error = %v", err)
	}
	health, err := manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionUnsafe || health.LastPublication != nil ||
		health.LastError != ErrProductionPublication.Error() {
		t.Fatalf("failed publication health = %#v", health)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); err != nil {
		t.Fatal(err)
	}
	health, err = manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionDegraded || health.LastPublication == nil ||
		health.LastError != "" ||
		manager.Stats().ProductionPublicationFailures != 1 {
		t.Fatalf("recovered publication health = %#v stats=%#v", health, manager.Stats())
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPublisherRejectsMismatchedReceipt(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	publisher := productionAnchorPublisherFunc(func(
		_ context.Context,
		anchor kitdb.BackupAnchor,
	) (ProductionAnchorReceipt, error) {
		receipt := productionAnchorReceipt(anchor, false, 0)
		receipt.SHA256 = strings.Repeat("0", 64)
		return receipt, nil
	})
	if err := manager.RegisterProductionAnchorPublisher("offhost", publisher); err != nil {
		t.Fatal(err)
	}
	config := productionTestConfig("tenant-a", source, filepath.Join(root, "local"))
	config.Publisher = "offhost"
	config.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); !errors.Is(err, ErrProductionUnsafe) {
		t.Fatalf("mismatched receipt error = %v", err)
	}
	health, err := manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionUnsafe || health.LastPublication != nil ||
		health.LastError != ErrProductionUnsafe.Error() {
		t.Fatalf("mismatched receipt health = %#v", health)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifiedDirectoryPublisherFailsClosedOnCorruptionAndRecovers(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	localStore := filepath.Join(root, "local")
	remoteStore := filepath.Join(root, "remote")
	if err := os.MkdirAll(remoteStore, 0o700); err != nil {
		t.Fatal(err)
	}
	publisher, err := NewVerifiedDirectoryPublisher(remoteStore, VerifiedDirectoryPublisherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	if err := manager.RegisterProductionAnchorPublisher("offhost", publisher); err != nil {
		t.Fatal(err)
	}
	config := productionTestConfig("tenant-a", source, localStore)
	config.Publisher = "offhost"
	config.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(remoteStore)
	if err != nil || len(entries) != 1 {
		t.Fatalf("published entries = %d, %v", len(entries), err)
	}
	corrupt := filepath.Join(remoteStore, entries[0].Name())
	info, err := os.Stat(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(corrupt, info.Size()-1); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); !errors.Is(err, ErrProductionUnsafe) {
		t.Fatalf("corrupt publication error = %v", err)
	}
	health, err := manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionUnsafe ||
		health.LastError != ErrProductionUnsafe.Error() {
		t.Fatalf("corrupt publication health = %#v", health)
	}
	if err := os.Remove(corrupt); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunProductionPolicyOnce(context.Background(), config.Name); err != nil {
		t.Fatal(err)
	}
	health, err = manager.ProductionPolicyHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.Readiness != ProductionDegraded || health.LastError != "" {
		t.Fatalf("recovered corrupt publication health = %#v", health)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPublisherOwnershipTopologyAndLifecycle(t *testing.T) {
	root := t.TempDir()
	sourceA := createProductionSource(t, root, "source-a", "key", "a")
	sourceB := createProductionSource(t, root, "source-b", "key", "b")
	remote := filepath.Join(root, "remote")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	publisher, err := NewVerifiedDirectoryPublisher(remote, VerifiedDirectoryPublisherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	if err := manager.RegisterProductionAnchorPublisher("offhost", publisher); err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterProductionAnchorPublisher("offhost", publisher); !errors.Is(err, ErrProductionPublisherExists) {
		t.Fatalf("duplicate publisher error = %v", err)
	}
	first := productionTestConfig("tenant-a", sourceA, filepath.Join(root, "local-a"))
	first.Publisher = "offhost"
	first.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(first); err != nil {
		t.Fatal(err)
	}
	if err := manager.UnregisterProductionAnchorPublisher("offhost"); !errors.Is(err, ErrProductionPublisherBusy) {
		t.Fatalf("owned publisher unregister error = %v", err)
	}
	second := productionTestConfig("tenant-b", sourceB, filepath.Join(root, "local-b"))
	second.Publisher = "offhost"
	second.StartPaused = true
	if _, err := manager.RegisterProductionPolicy(second); !errors.Is(err, ErrProductionPolicyTopology) {
		t.Fatalf("shared publisher policy error = %v", err)
	}
	if err := manager.UnregisterProductionPolicy(first.Name); err != nil {
		t.Fatal(err)
	}
	if err := manager.UnregisterProductionAnchorPublisher("offhost"); err != nil {
		t.Fatal(err)
	}
	if len(manager.ProductionAnchorPublishers()) != 0 {
		t.Fatal("unregistered publisher remains visible")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	nestedLocal := filepath.Join(root, "nested-local")
	nestedRemote := filepath.Join(nestedLocal, "remote")
	if err := os.MkdirAll(nestedRemote, 0o700); err != nil {
		t.Fatal(err)
	}
	nestedPublisher, err := NewVerifiedDirectoryPublisher(
		nestedRemote, VerifiedDirectoryPublisherOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	nested := newProductionTestManager(t, ProductionSupervisorLimits{})
	if err := nested.RegisterProductionAnchorPublisher("offhost", nestedPublisher); err != nil {
		t.Fatal(err)
	}
	nestedConfig := productionTestConfig("tenant-a", sourceA, nestedLocal)
	nestedConfig.Publisher = "offhost"
	nestedConfig.StartPaused = true
	if _, err := nested.RegisterProductionPolicy(nestedConfig); !errors.Is(err, ErrProductionPolicyTopology) {
		t.Fatalf("overlapping publisher topology error = %v", err)
	}
	if err := nested.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionPublisherCancellationDrainsWithManager(t *testing.T) {
	root := t.TempDir()
	source := createProductionSource(t, root, "source", "key", "value")
	started := make(chan struct{})
	publisher := productionAnchorPublisherFunc(func(
		ctx context.Context,
		_ kitdb.BackupAnchor,
	) (ProductionAnchorReceipt, error) {
		close(started)
		<-ctx.Done()
		return ProductionAnchorReceipt{}, ctx.Err()
	})
	manager := newProductionTestManager(t, ProductionSupervisorLimits{})
	if err := manager.RegisterProductionAnchorPublisher("offhost", publisher); err != nil {
		t.Fatal(err)
	}
	config := productionTestConfig("tenant-a", source, filepath.Join(root, "local"))
	config.Publisher = "offhost"
	if _, err := manager.RegisterProductionPolicy(config); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("publisher did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("manager did not drain canceled publisher")
	}
	stats := manager.Stats()
	if stats.ActiveLeases != 0 || stats.RunningProductionPolicies != 0 ||
		stats.ProductionPublicationAttempts != 1 ||
		stats.ProductionPublicationFailures != 1 {
		t.Fatalf("drained publisher stats = %#v", stats)
	}
}
