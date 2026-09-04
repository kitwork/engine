package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
)

func TestReplicaFileLinkRunOnceResumesFromDurableStateAcrossManagers(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.kitdb")
	target := filepath.Join(root, "replica.kitdb")
	mailbox := filepath.Join(root, "mailbox")
	options := kitdb.OpenOptions{RetainHistory: true}
	config := ReplicaFileLinkConfig{
		Name: "replica/manual-restart", Source: source, Target: target,
		Mailbox: mailbox, SourceOptions: options, PinName: "replica/manual-restart",
		BatchLimits: kitdb.ReplicaBatchLimits{MaxTransactions: 1},
		Priority:    MaintenanceBackground, StartPaused: true,
	}

	first := newReplicaFileLinkTestManager(t, ReplicaFileLinkLimits{})
	commitNodeBackupValue(t, first, source, options, "product/0", "anchor")
	bootstrap := scheduleNodeReplicaBootstrap(
		t, first, source, target, options, config.PinName,
	)
	for index, value := range []string{"one", "two", "three"} {
		commitNodeBackupValue(
			t, first, source, options,
			"product/"+string(rune('1'+index)), value,
		)
	}
	registered, err := first.RegisterReplicaFileLink(config)
	if err != nil {
		t.Fatal(err)
	}
	if registered.State != ReplicaFileLinkPaused || !registered.Paused {
		t.Fatalf("registered health = %#v", registered)
	}
	cycle, err := first.RunReplicaFileLinkOnce(context.Background(), config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !cycle.Progress || cycle.CaughtUp || cycle.Publish == nil ||
		cycle.Publish.Publication == nil || cycle.Publish.Publication.Transactions != 1 ||
		cycle.Publish.RemainingTransactions != 2 || cycle.Apply == nil ||
		!cycle.Apply.Found || cycle.Acknowledge == nil || !cycle.Acknowledge.Found {
		t.Fatalf("first controller cycle = %#v", cycle)
	}
	if cycle.Acknowledge.Pin.Cursor.Transaction != bootstrap.Anchor.Transaction+1 {
		t.Fatalf(
			"first ACK transaction = %d, want %d",
			cycle.Acknowledge.Pin.Cursor.Transaction, bootstrap.Anchor.Transaction+1,
		)
	}
	if stats := first.Stats(); stats.ReplicaFileLinkCycles != 1 ||
		stats.ReplicaFileLinkSuccesses != 1 || stats.ActiveLeases != 0 {
		t.Fatalf("first manager stats = %#v", stats)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := newReplicaFileLinkTestManager(t, ReplicaFileLinkLimits{})
	if _, err := second.RegisterReplicaFileLink(config); err != nil {
		t.Fatal(err)
	}
	for expectedRemaining := uint64(1); ; expectedRemaining-- {
		cycle, err = second.RunReplicaFileLinkOnce(context.Background(), config.Name)
		if err != nil {
			t.Fatal(err)
		}
		if cycle.Publish == nil || cycle.Publish.Publication == nil ||
			cycle.Publish.RemainingTransactions != expectedRemaining || !cycle.Progress {
			t.Fatalf("resumed cycle with %d remaining = %#v", expectedRemaining, cycle)
		}
		if cycle.CaughtUp != (expectedRemaining == 0) {
			t.Fatalf("resumed caught-up state with %d remaining = %#v", expectedRemaining, cycle)
		}
		if expectedRemaining == 0 {
			break
		}
	}
	noChanges, err := second.RunReplicaFileLinkOnce(context.Background(), config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if noChanges.Progress || !noChanges.CaughtUp || noChanges.Publish == nil ||
		!noChanges.Publish.NoChanges || noChanges.Apply == nil || noChanges.Apply.Found ||
		noChanges.Acknowledge == nil || noChanges.Acknowledge.Found {
		t.Fatalf("caught-up controller cycle = %#v", noChanges)
	}
	for key, want := range map[string]string{
		"product/0": "anchor", "product/1": "one",
		"product/2": "two", "product/3": "three",
	} {
		assertNodeReplicaValue(t, second, target, kitdb.OpenOptions{}, key, want)
	}
	assertNodeBackupPin(
		t, second, source, options, config.PinName, noChanges.Publish.SourceBoundary,
	)
	health, err := second.ReplicaFileLinkHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != ReplicaFileLinkPaused || health.Attempts != 3 ||
		health.Successes != 3 || health.Failures != 0 || !health.LastCycle.CaughtUp {
		t.Fatalf("resumed health = %#v", health)
	}
}

func TestReplicaFileLinkRetriesWithBackoffAndRecovers(t *testing.T) {
	root := t.TempDir()
	manager := newReplicaFileLinkTestManager(t, ReplicaFileLinkLimits{
		MaxLinks: 4, MaxConcurrent: 1,
		ActiveInterval: time.Millisecond, IdleInterval: time.Second,
		InitialBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
	})
	source := filepath.Join(root, "source.kitdb")
	target := filepath.Join(root, "replica.kitdb")
	mailbox := filepath.Join(root, "mailbox")
	options := kitdb.OpenOptions{RetainHistory: true}
	pinName := "replica/retry"
	commitNodeBackupValue(t, manager, source, options, "base", "anchor")
	scheduleNodeReplicaBootstrap(t, manager, source, target, options, pinName)
	commitNodeBackupValue(t, manager, source, options, "next", "recovered")
	config := ReplicaFileLinkConfig{
		Name: pinName, Source: source, Target: target, Mailbox: mailbox,
		SourceOptions: options, PinName: pinName,
		Priority: MaintenanceBackground, StartPaused: true,
	}
	if _, err := manager.RegisterReplicaFileLink(config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mailbox, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ResumeReplicaFileLink(config.Name); err != nil {
		t.Fatal(err)
	}
	failed := waitForReplicaFileLinkHealth(t, manager, config.Name, func(health ReplicaFileLinkHealth) bool {
		return health.Failures >= 1 && health.ConsecutiveFailures >= 1
	})
	if failed.LastError == "" || failed.LastSuccessAt != (time.Time{}) {
		t.Fatalf("failed health = %#v", failed)
	}
	if _, err := manager.PauseReplicaFileLink(config.Name); err != nil {
		t.Fatal(err)
	}
	waitForReplicaFileLinkHealth(t, manager, config.Name, func(health ReplicaFileLinkHealth) bool {
		return health.State == ReplicaFileLinkPaused
	})
	if err := os.Remove(mailbox); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ResumeReplicaFileLink(config.Name); err != nil {
		t.Fatal(err)
	}
	recovered := waitForReplicaFileLinkHealth(t, manager, config.Name, func(health ReplicaFileLinkHealth) bool {
		return health.Successes >= 1 && health.LastCycle.CaughtUp
	})
	if recovered.ConsecutiveFailures != 0 || recovered.LastError != "" ||
		recovered.LastSuccessAt.IsZero() {
		t.Fatalf("recovered health = %#v", recovered)
	}
	if _, err := manager.PauseReplicaFileLink(config.Name); err != nil {
		t.Fatal(err)
	}
	waitForReplicaFileLinkHealth(t, manager, config.Name, func(health ReplicaFileLinkHealth) bool {
		return health.State == ReplicaFileLinkPaused
	})
	assertNodeReplicaValue(t, manager, target, kitdb.OpenOptions{}, "next", "recovered")
	stats := manager.Stats()
	if stats.ReplicaFileLinkFailures < 1 || stats.ReplicaFileLinkBackoffs < 1 ||
		stats.ReplicaFileLinkSuccesses < 1 || stats.RunningReplicaFileLinks != 0 {
		t.Fatalf("retry stats = %#v", stats)
	}
}

func TestReplicaFileLinkSharedWorkerDoesNotStarveAnotherLink(t *testing.T) {
	root := t.TempDir()
	manager := newReplicaFileLinkTestManager(t, ReplicaFileLinkLimits{
		MaxLinks: 4, MaxConcurrent: 1,
		ActiveInterval: time.Millisecond, IdleInterval: time.Second,
		InitialBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
	})
	options := kitdb.OpenOptions{RetainHistory: true}
	configFor := func(name string) ReplicaFileLinkConfig {
		return ReplicaFileLinkConfig{
			Name:          "replica/" + name,
			Source:        filepath.Join(root, name+"-source.kitdb"),
			Target:        filepath.Join(root, name+"-target.kitdb"),
			Mailbox:       filepath.Join(root, name+"-mailbox"),
			SourceOptions: options, PinName: "replica/" + name,
			BatchLimits: kitdb.ReplicaBatchLimits{MaxTransactions: 1},
			Priority:    MaintenanceBackground,
		}
	}
	noisy := configFor("a-noisy")
	quiet := configFor("b-quiet")
	for _, config := range []ReplicaFileLinkConfig{noisy, quiet} {
		commitNodeBackupValue(t, manager, config.Source, options, "base", "anchor")
		scheduleNodeReplicaBootstrap(
			t, manager, config.Source, config.Target, options, config.PinName,
		)
	}
	for index := range 8 {
		commitNodeBackupValue(
			t, manager, noisy.Source, options,
			"noisy/"+string(rune('a'+index)), "value",
		)
	}
	commitNodeBackupValue(t, manager, quiet.Source, options, "quiet", "value")

	if _, err := manager.RegisterReplicaFileLink(noisy); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterReplicaFileLink(quiet); err != nil {
		t.Fatal(err)
	}
	quietHealth := waitForReplicaFileLinkHealth(t, manager, quiet.Name, func(health ReplicaFileLinkHealth) bool {
		return health.Successes >= 1 && health.LastCycle.CaughtUp
	})
	noisyHealth := waitForReplicaFileLinkHealth(t, manager, noisy.Name, func(health ReplicaFileLinkHealth) bool {
		return health.Successes >= 2
	})
	if quietHealth.Attempts == 0 || noisyHealth.Attempts < 2 {
		t.Fatalf("fairness health = quiet %#v, noisy %#v", quietHealth, noisyHealth)
	}
	assertNodeReplicaValue(t, manager, quiet.Target, kitdb.OpenOptions{}, "quiet", "value")
	if stats := manager.Stats(); stats.MaxConcurrentReplicaFileLinks != 1 ||
		stats.ReplicaFileLinks != 2 {
		t.Fatalf("shared worker stats = %#v", stats)
	}
}

func TestReplicaFileLinkRegistrationBoundsTopologyAndListing(t *testing.T) {
	root := t.TempDir()
	manager := newReplicaFileLinkTestManager(t, ReplicaFileLinkLimits{
		MaxLinks: 2, MaxConcurrent: 1,
		ActiveInterval: time.Millisecond, IdleInterval: time.Second,
		InitialBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
	})
	configFor := func(name string) ReplicaFileLinkConfig {
		return ReplicaFileLinkConfig{
			Name:          "replica/" + name,
			Source:        filepath.Join(root, name+"-source.kitdb"),
			Target:        filepath.Join(root, name+"-target.kitdb"),
			Mailbox:       filepath.Join(root, name+"-mailbox"),
			SourceOptions: kitdb.OpenOptions{RetainHistory: true},
			PinName:       "replica/" + name, StartPaused: true,
		}
	}
	second := configFor("b")
	first := configFor("a")
	third := configFor("c")
	for _, config := range []ReplicaFileLinkConfig{first, second, third} {
		createReplicaFileLinkDatabase(t, config.Source)
		createReplicaFileLinkDatabase(t, config.Target)
	}
	if _, err := manager.RegisterReplicaFileLink(second); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterReplicaFileLink(second); err != nil {
		t.Fatalf("idempotent registration: %v", err)
	}
	changed := second
	changed.BatchLimits.MaxTransactions = 1
	if _, err := manager.RegisterReplicaFileLink(changed); !errors.Is(err, ErrReplicaFileLinkExists) {
		t.Fatalf("changed registration error = %v", err)
	}
	sharedTarget := first
	sharedTarget.Target = second.Target
	if _, err := manager.RegisterReplicaFileLink(sharedTarget); !errors.Is(err, ErrReplicaFileLinkTopology) {
		t.Fatalf("shared target error = %v", err)
	}
	sharedMailbox := first
	sharedMailbox.Mailbox = second.Mailbox
	if _, err := manager.RegisterReplicaFileLink(sharedMailbox); !errors.Is(err, ErrReplicaFileLinkTopology) {
		t.Fatalf("shared mailbox error = %v", err)
	}
	sharedPin := first
	sharedPin.Source = second.Source
	sharedPin.PinName = second.PinName
	if _, err := manager.RegisterReplicaFileLink(sharedPin); !errors.Is(err, ErrReplicaFileLinkTopology) {
		t.Fatalf("shared source pin error = %v", err)
	}
	databaseMailbox := first
	databaseMailbox.Mailbox = second.Source
	if _, err := manager.RegisterReplicaFileLink(databaseMailbox); !errors.Is(err, ErrReplicaFileLinkTopology) {
		t.Fatalf("database mailbox error = %v", err)
	}
	if _, err := manager.RegisterReplicaFileLink(first); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterReplicaFileLink(third); !errors.Is(err, ErrReplicaFileLinkLimit) {
		t.Fatalf("link limit error = %v", err)
	}
	links := manager.ReplicaFileLinks()
	if len(links) != 2 || links[0].Name != first.Name || links[1].Name != second.Name ||
		links[0].State != ReplicaFileLinkPaused || links[1].State != ReplicaFileLinkPaused {
		t.Fatalf("sorted bounded links = %#v", links)
	}
	if stats := manager.Stats(); stats.ReplicaFileLinks != 2 ||
		stats.PausedReplicaFileLinks != 2 || stats.MaxReplicaFileLinks != 2 {
		t.Fatalf("registration stats = %#v", stats)
	}
	if err := manager.UnregisterReplicaFileLink(first.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReplicaFileLinkHealth(first.Name); !errors.Is(err, ErrReplicaFileLinkNotFound) {
		t.Fatalf("unregistered health error = %v", err)
	}
}

func TestReplicaFileLinkCloseDrainsQueuedCycle(t *testing.T) {
	root := t.TempDir()
	manager := newReplicaFileLinkTestManager(t, ReplicaFileLinkLimits{
		MaxLinks: 2, MaxConcurrent: 1,
		ActiveInterval: time.Millisecond, IdleInterval: time.Second,
		InitialBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
	})
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
	blocker, err := manager.ScheduleCheckpoint(
		context.Background(), filepath.Join(root, "blocker.kitdb"),
		kitdb.OpenOptions{}, MaintenanceNormal,
	)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	config := ReplicaFileLinkConfig{
		Name:          "replica/close",
		Source:        filepath.Join(root, "source.kitdb"),
		Target:        filepath.Join(root, "target.kitdb"),
		Mailbox:       filepath.Join(root, "mailbox"),
		SourceOptions: kitdb.OpenOptions{RetainHistory: true},
		PinName:       "replica/close", Priority: MaintenanceBackground,
	}
	createReplicaFileLinkDatabase(t, config.Source)
	createReplicaFileLinkDatabase(t, config.Target)
	if _, err := manager.RegisterReplicaFileLink(config); err != nil {
		t.Fatal(err)
	}
	waitForNodeStats(t, manager, func(stats Stats) bool {
		return stats.RunningReplicaFileLinks == 1 && stats.QueuedMaintenance >= 1
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.CloseContext(ctx); err != nil {
		t.Fatalf("close controller: %v", err)
	}
	if _, err := blocker.Wait(context.Background()); !errors.Is(err, context.Canceled) &&
		!errors.Is(err, ErrClosed) {
		t.Fatalf("blocked maintenance error = %v", err)
	}
	stats := manager.Stats()
	if !stats.Closed || stats.RunningReplicaFileLinks != 0 ||
		stats.QueuedReplicaFileLinks != 0 || stats.RunningMaintenance != 0 ||
		stats.QueuedMaintenance != 0 {
		t.Fatalf("closed controller stats = %#v", stats)
	}
}

func TestReplicaFileLinkBackoffIsDeterministicAndBounded(t *testing.T) {
	limits := normalizedReplicaFileLinkLimits{
		initialBackoff: 8 * time.Millisecond,
		maxBackoff:     20 * time.Millisecond,
	}
	for _, test := range []struct {
		failures uint32
		minimum  time.Duration
		maximum  time.Duration
	}{
		{failures: 1, minimum: 6 * time.Millisecond, maximum: 8 * time.Millisecond},
		{failures: 2, minimum: 12 * time.Millisecond, maximum: 16 * time.Millisecond},
		{failures: 3, minimum: 15 * time.Millisecond, maximum: 20 * time.Millisecond},
		{failures: 12, minimum: 15 * time.Millisecond, maximum: 20 * time.Millisecond},
	} {
		got := replicaFileLinkBackoff(limits, "replica/bounded", test.failures, 17)
		again := replicaFileLinkBackoff(limits, "replica/bounded", test.failures, 17)
		if got != again || got < test.minimum || got > test.maximum || got > limits.maxBackoff {
			t.Fatalf(
				"backoff failure %d = %s and %s, want [%s, %s]",
				test.failures, got, again, test.minimum, test.maximum,
			)
		}
	}
}

func newReplicaFileLinkTestManager(
	t *testing.T,
	linkLimits ReplicaFileLinkLimits,
) *Manager {
	t.Helper()
	limits := Limits{
		MaxOpenDatabases: 12, MaxPageCacheBytes: 12 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 4,
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 32,
		MaxMaintenancePerDatabase: 2, MaintenanceTimeout: time.Minute,
		ReplicaFileLinks: linkLimits,
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

func waitForReplicaFileLinkHealth(
	t *testing.T,
	manager *Manager,
	name string,
	ready func(ReplicaFileLinkHealth) bool,
) ReplicaFileLinkHealth {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var health ReplicaFileLinkHealth
	var err error
	for time.Now().Before(deadline) {
		health, err = manager.ReplicaFileLinkHealth(name)
		if err == nil && ready(health) {
			return health
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("replica link %q did not converge: health=%#v err=%v", name, health, err)
	return ReplicaFileLinkHealth{}
}

func createReplicaFileLinkDatabase(t *testing.T, path string) {
	t.Helper()
	database, err := kitdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}
