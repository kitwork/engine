package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestManagedReplicaFileLifecycleResumesAcrossManagers(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.kitdb")
	target := filepath.Join(root, "replica.kitdb")
	mailbox := filepath.Join(root, "replica-mailbox")
	options := kitdb.OpenOptions{RetainHistory: true}
	pinName := "replica/file-restart"

	firstManager := newMaintenanceTestManager(t, Limits{})
	commitNodeBackupValue(t, firstManager, source, options, "product/0", "anchor")
	bootstrap := scheduleNodeReplicaBootstrap(
		t, firstManager, source, target, options, pinName,
	)
	for index, value := range []string{"one", "two", "three"} {
		commitNodeBackupValue(
			t, firstManager, source, options,
			"product/"+string(rune('1'+index)), value,
		)
	}
	publishRequest := ReplicaFilePublishRequest{
		Source: source, Mailbox: mailbox, SourceOptions: options, PinName: pinName,
		BatchLimits: kitdb.ReplicaBatchLimits{MaxTransactions: 1},
		Priority:    MaintenanceBackground,
	}
	published := scheduleNodeReplicaFilePublish(t, firstManager, publishRequest)
	if !published.Published || published.Pending || published.NoChanges ||
		published.Publication == nil || published.Publication.Transactions != 1 ||
		published.LagTransactions != 3 || published.RemainingTransactions != 2 ||
		published.Pin.Cursor != bootstrap.Anchor.Cursor() {
		t.Fatalf("first managed publication = %#v", published)
	}
	pending := scheduleNodeReplicaFilePublish(t, firstManager, publishRequest)
	if !pending.Pending || pending.Published || pending.Publication != nil ||
		pending.MailboxStats.PendingBatches != 1 {
		t.Fatalf("managed publication backpressure = %#v", pending)
	}
	if err := firstManager.Close(); err != nil {
		t.Fatal(err)
	}

	applyManager := newMaintenanceTestManager(t, Limits{})
	applied := scheduleNodeReplicaFileApply(t, applyManager, ReplicaFileApplyRequest{
		Target: target, Mailbox: mailbox, Priority: MaintenanceBackground,
	})
	if !applied.Found || applied.Apply == nil ||
		applied.Apply.AppliedTransactions != 1 ||
		applied.Apply.To != published.Publication.To ||
		applied.MailboxStats.PendingAcknowledgements != 1 {
		t.Fatalf("managed apply after restart = %#v", applied)
	}
	if err := applyManager.Close(); err != nil {
		t.Fatal(err)
	}

	ackManager := newMaintenanceTestManager(t, Limits{})
	acknowledged := scheduleNodeReplicaFileAcknowledge(
		t, ackManager, ReplicaFileAcknowledgeRequest{
			Source: source, Mailbox: mailbox, SourceOptions: options,
			PinName: pinName, Priority: MaintenanceBackground,
		},
	)
	if !acknowledged.Found || acknowledged.Acknowledge == nil ||
		acknowledged.Pin.Cursor != published.Publication.To ||
		acknowledged.MailboxStats != (kitdb.ReplicaFileTransportStats{}) {
		t.Fatalf("managed acknowledge after restart = %#v", acknowledged)
	}

	for _, expectedRemaining := range []uint64{1, 0} {
		next := scheduleNodeReplicaFilePublish(t, ackManager, publishRequest)
		if !next.Published || next.Publication == nil ||
			next.RemainingTransactions != expectedRemaining {
			t.Fatalf("resumed publication with %d remaining = %#v", expectedRemaining, next)
		}
		scheduleNodeReplicaFileApply(t, ackManager, ReplicaFileApplyRequest{
			Target: target, Mailbox: mailbox, Priority: MaintenanceBackground,
		})
		scheduleNodeReplicaFileAcknowledge(t, ackManager, ReplicaFileAcknowledgeRequest{
			Source: source, Mailbox: mailbox, SourceOptions: options,
			PinName: pinName, Priority: MaintenanceBackground,
		})
	}
	for key, want := range map[string]string{
		"product/0": "anchor", "product/1": "one",
		"product/2": "two", "product/3": "three",
	} {
		assertNodeReplicaValue(t, ackManager, target, kitdb.OpenOptions{}, key, want)
	}
	assertNodeBackupPin(t, ackManager, source, options, pinName, published.SourceBoundary)
}

func TestManagedReplicaFileMetricsAndNoopBoundaries(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{MaxConcurrentMaintenance: 2})
	source := filepath.Join(root, "source.kitdb")
	target := filepath.Join(root, "replica.kitdb")
	mailbox := filepath.Join(root, "mailbox")
	options := kitdb.OpenOptions{RetainHistory: true}
	pinName := "replica/file-metrics"
	commitNodeBackupValue(t, manager, source, options, "base", "anchor")
	scheduleNodeReplicaBootstrap(t, manager, source, target, options, pinName)
	commitNodeBackupValue(t, manager, source, options, "next/1", "one")
	commitNodeBackupValue(t, manager, source, options, "next/2", "two")

	publish := ReplicaFilePublishRequest{
		Source: source, Mailbox: mailbox, SourceOptions: options, PinName: pinName,
		BatchLimits: kitdb.ReplicaBatchLimits{MaxTransactions: 1},
		Priority:    MaintenanceBackground,
	}
	apply := ReplicaFileApplyRequest{
		Target: target, Mailbox: mailbox, Priority: MaintenanceBackground,
	}
	acknowledge := ReplicaFileAcknowledgeRequest{
		Source: source, Mailbox: mailbox, SourceOptions: options,
		PinName: pinName, Priority: MaintenanceBackground,
	}

	first := scheduleNodeReplicaFilePublish(t, manager, publish)
	if first.LagTransactions != 2 || first.RemainingTransactions != 1 {
		t.Fatalf("first lag = %#v", first)
	}
	if pending := scheduleNodeReplicaFilePublish(t, manager, publish); !pending.Pending {
		t.Fatalf("pending publication = %#v", pending)
	}
	for range 2 {
		scheduleNodeReplicaFileApply(t, manager, apply)
		scheduleNodeReplicaFileAcknowledge(t, manager, acknowledge)
		if first.Publication.To.Transaction+1 < first.SourceBoundary.Transaction+1 {
			first = scheduleNodeReplicaFilePublish(t, manager, publish)
		}
	}
	noChanges := scheduleNodeReplicaFilePublish(t, manager, publish)
	if !noChanges.NoChanges || noChanges.Published || noChanges.Pending {
		t.Fatalf("no-change publication = %#v", noChanges)
	}
	emptyApply := scheduleNodeReplicaFileApply(t, manager, apply)
	emptyAcknowledge := scheduleNodeReplicaFileAcknowledge(t, manager, acknowledge)
	if emptyApply.Found || emptyAcknowledge.Found {
		t.Fatalf("empty managed operations = (%#v, %#v)", emptyApply, emptyAcknowledge)
	}

	stats := manager.Stats()
	if stats.ReplicaFilePublishCompletions != 4 ||
		stats.ReplicaFileApplyCompletions != 3 ||
		stats.ReplicaFileAcknowledgeCompletions != 3 ||
		stats.ReplicaFileBatchesPublished != 2 ||
		stats.ReplicaFileTransactionsPublished != 2 ||
		stats.ReplicaFileTransactionsApplied != 2 ||
		stats.ReplicaFileAcknowledgements != 2 ||
		stats.ReplicaFileNoops != 4 ||
		stats.ReplicaFilePeakPendingBatches != 1 ||
		stats.ReplicaFilePeakPendingBytes == 0 ||
		stats.ReplicaFileMaxLagTransactions != 2 ||
		stats.MaintenanceFailures != 0 {
		t.Fatalf("managed replica file stats = %#v", stats)
	}
}

func TestMaintenanceReserveOnlyPathsSerializeLocallyButNotGlobally(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
		MaxConcurrentMaintenance: 2, MaxQueuedMaintenance: 6,
	})
	started := make(chan string, 3)
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
	sharedMailbox := filepath.Join(root, "shared-mailbox")
	otherMailbox := filepath.Join(root, "other-mailbox")
	first, err := manager.scheduleMaintenance(
		context.Background(), filepath.Join(root, "a.kitdb"), kitdb.OpenOptions{},
		MaintenanceCheckpoint, "a", MaintenanceNormal, maintenancePayload{},
		[]maintenanceResourceRequest{{path: sharedMailbox, reserveOnly: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "a.kitdb" {
		t.Fatalf("first reserve-only task = %q", got)
	}
	blocked, err := manager.scheduleMaintenance(
		context.Background(), filepath.Join(root, "b.kitdb"), kitdb.OpenOptions{},
		MaintenanceCheckpoint, "b", MaintenanceNormal, maintenancePayload{},
		[]maintenanceResourceRequest{{path: sharedMailbox, reserveOnly: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	independent, err := manager.scheduleMaintenance(
		context.Background(), filepath.Join(root, "c.kitdb"), kitdb.OpenOptions{},
		MaintenanceCheckpoint, "c", MaintenanceNormal, maintenancePayload{},
		[]maintenanceResourceRequest{{path: otherMailbox, reserveOnly: true}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "c.kitdb" {
		t.Fatalf("independent reserve-only task = %q", got)
	}
	if stats := manager.Stats(); stats.RunningMaintenance != 2 ||
		stats.MultiDatabaseMaintenanceRunning {
		t.Fatalf("reserve-only scheduler stats = %#v", stats)
	}
	select {
	case got := <-started:
		t.Fatalf("shared mailbox task ran concurrently as %q", got)
	default:
	}
	close(release)
	for _, ticket := range []*MaintenanceTicket{first, blocked, independent} {
		if _, err := ticket.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{sharedMailbox, otherMailbox} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reserve-only path %q was opened or created: %v", path, err)
		}
	}
}

func TestManagedReplicaFileRejectsUnsafeRequestsBeforeAdmission(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{})
	source := filepath.Join(root, "source.kitdb")
	options := kitdb.OpenOptions{RetainHistory: true}
	commitNodeBackupValue(t, manager, source, options, "base", "value")

	_, err := manager.ScheduleReplicaFilePublish(context.Background(), ReplicaFilePublishRequest{
		Source: source, Mailbox: filepath.Join(root, "mailbox"), PinName: "replica/test",
	})
	if !errors.Is(err, ErrReplicaFileHistoryRequired) {
		t.Fatalf("missing history error = %v", err)
	}
	_, err = manager.ScheduleReplicaFilePublish(context.Background(), ReplicaFilePublishRequest{
		Source: source, Mailbox: source, SourceOptions: options, PinName: "replica/test",
	})
	if !errors.Is(err, ErrReplicaFileSamePath) {
		t.Fatalf("same path error = %v", err)
	}
	_, err = manager.ScheduleReplicaFilePublish(context.Background(), ReplicaFilePublishRequest{
		Source: source, Mailbox: filepath.Join(root, "mailbox"),
		SourceOptions: options, PinName: "replica/test",
		BatchLimits: kitdb.ReplicaBatchLimits{
			MaxTransactions: kitdb.MaxReplicaBatchTransactions + 1,
		},
	})
	if !errors.Is(err, kitdb.ErrReplicaBatchLimit) {
		t.Fatalf("invalid limit error = %v", err)
	}
	if stats := manager.Stats(); stats.MaintenanceSubmissions != 0 {
		t.Fatalf("unsafe requests entered maintenance admission: %#v", stats)
	}
}

func TestManagedReplicaFilePublishCoalescesNormalizedPolicy(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{
		MaxOpenDatabases: 3, MaxPageCacheBytes: 3 << 20,
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 4,
	})
	source := filepath.Join(root, "source.kitdb")
	target := filepath.Join(root, "replica.kitdb")
	mailbox := filepath.Join(root, "mailbox")
	options := kitdb.OpenOptions{RetainHistory: true}
	pinName := "replica/file-coalesced"
	commitNodeBackupValue(t, manager, source, options, "base", "anchor")
	scheduleNodeReplicaBootstrap(t, manager, source, target, options, pinName)
	commitNodeBackupValue(t, manager, source, options, "next", "value")

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
	request := ReplicaFilePublishRequest{
		Source: source, Mailbox: mailbox, SourceOptions: options,
		PinName: pinName, Priority: MaintenanceBackground,
	}
	first, err := manager.ScheduleReplicaFilePublish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Priority = MaintenanceUrgent
	request.BatchLimits = kitdb.ReplicaBatchLimits{
		MaxTransactions: kitdb.DefaultReplicaBatchTransactions,
		MaxBytes:        kitdb.DefaultReplicaBatchBytes,
	}
	request.TransportLimits = kitdb.ReplicaFileTransportLimits{
		MaxPendingBatches: kitdb.DefaultReplicaFilePendingBatches,
		MaxPendingBytes:   kitdb.DefaultReplicaFilePendingBytes,
	}
	joined, err := manager.ScheduleReplicaFilePublish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Priority = MaintenanceNormal
	request.BatchLimits.MaxTransactions = 1
	different, err := manager.ScheduleReplicaFilePublish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.task != joined.task || first.task.priority != MaintenanceUrgent {
		t.Fatal("semantically identical replica file policy did not coalesce")
	}
	if first.task == different.task {
		t.Fatal("different replica file batch policy was incorrectly coalesced")
	}
	close(release)
	if _, err := blocker.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstResult, err := first.Wait(context.Background())
	if err != nil || firstResult.ReplicaFilePublish == nil ||
		!firstResult.ReplicaFilePublish.Published {
		t.Fatalf("first coalesced publication = (%#v, %v)", firstResult, err)
	}
	joinedResult, err := joined.Wait(context.Background())
	if err != nil || joinedResult.ReplicaFilePublish != firstResult.ReplicaFilePublish {
		t.Fatalf("joined publication = (%#v, %v)", joinedResult, err)
	}
	differentResult, err := different.Wait(context.Background())
	if err != nil || differentResult.ReplicaFilePublish == nil ||
		!differentResult.ReplicaFilePublish.Pending {
		t.Fatalf("different queued publication = (%#v, %v)", differentResult, err)
	}
	if stats := manager.Stats(); stats.MaintenanceCoalesced != 1 ||
		stats.ReplicaFilePublishCompletions != 2 ||
		stats.ReplicaFileBatchesPublished != 1 {
		t.Fatalf("coalesced replica file stats = %#v", stats)
	}
}

func TestManagedReplicaFileApplyPreservesTargetCursorOnEarlyDivergence(t *testing.T) {
	root := t.TempDir()
	manager := newMaintenanceTestManager(t, Limits{})
	source := filepath.Join(root, "source.kitdb")
	bootstrapTarget := filepath.Join(root, "bootstrap.kitdb")
	unrelatedTarget := filepath.Join(root, "unrelated.kitdb")
	mailbox := filepath.Join(root, "mailbox")
	options := kitdb.OpenOptions{RetainHistory: true}
	pinName := "replica/file-diverged"
	commitNodeBackupValue(t, manager, source, options, "base", "anchor")
	scheduleNodeReplicaBootstrap(
		t, manager, source, bootstrapTarget, options, pinName,
	)
	commitNodeBackupValue(t, manager, source, options, "next", "value")
	published := scheduleNodeReplicaFilePublish(t, manager, ReplicaFilePublishRequest{
		Source: source, Mailbox: mailbox, SourceOptions: options,
		PinName: pinName, Priority: MaintenanceBackground,
	})
	if !published.Published {
		t.Fatalf("divergence publication = %#v", published)
	}
	unrelated, err := kitdb.Open(unrelatedTarget)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedCursor, err := unrelated.CurrentCursor()
	if err != nil {
		t.Fatal(err)
	}
	if err := unrelated.Close(); err != nil {
		t.Fatal(err)
	}

	ticket, err := manager.ScheduleReplicaFileApply(context.Background(), ReplicaFileApplyRequest{
		Target: unrelatedTarget, Mailbox: mailbox, Priority: MaintenanceBackground,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if !errors.Is(err, kitdb.ErrReplicaDiverged) {
		t.Fatalf("managed apply divergence error = %v", err)
	}
	if result.ReplicaFileApply == nil || !result.ReplicaFileApply.Found ||
		result.ReplicaFileApply.Apply == nil ||
		result.ReplicaFileApply.Target != unrelatedCursor ||
		result.Transaction != unrelatedCursor.Transaction {
		t.Fatalf("managed apply divergence evidence = %#v", result)
	}
}

func scheduleNodeReplicaFilePublish(
	t *testing.T,
	manager *Manager,
	request ReplicaFilePublishRequest,
) ReplicaFilePublishResult {
	t.Helper()
	ticket, err := manager.ScheduleReplicaFilePublish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil || result.ReplicaFilePublish == nil {
		t.Fatalf("managed replica file publish = (%#v, %v)", result, err)
	}
	return *result.ReplicaFilePublish
}

func scheduleNodeReplicaFileApply(
	t *testing.T,
	manager *Manager,
	request ReplicaFileApplyRequest,
) ReplicaFileApplyResult {
	t.Helper()
	ticket, err := manager.ScheduleReplicaFileApply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil || result.ReplicaFileApply == nil {
		t.Fatalf("managed replica file apply = (%#v, %v)", result, err)
	}
	return *result.ReplicaFileApply
}

func scheduleNodeReplicaFileAcknowledge(
	t *testing.T,
	manager *Manager,
	request ReplicaFileAcknowledgeRequest,
) ReplicaFileAcknowledgeResult {
	t.Helper()
	ticket, err := manager.ScheduleReplicaFileAcknowledge(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ticket.Wait(context.Background())
	if err != nil || result.ReplicaFileAcknowledge == nil {
		t.Fatalf("managed replica file acknowledge = (%#v, %v)", result, err)
	}
	return *result.ReplicaFileAcknowledge
}
