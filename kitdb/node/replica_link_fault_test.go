package node

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
)

const (
	replicaFaultChildEnvironment      = "KITDB_REPLICA_FAULT_CHILD"
	replicaFaultStageEnvironment      = "KITDB_REPLICA_FAULT_STAGE"
	replicaFaultSourceEnvironment     = "KITDB_REPLICA_FAULT_SOURCE"
	replicaFaultTargetEnvironment     = "KITDB_REPLICA_FAULT_TARGET"
	replicaFaultMailboxEnvironment    = "KITDB_REPLICA_FAULT_MAILBOX"
	replicaFaultPinEnvironment        = "KITDB_REPLICA_FAULT_PIN"
	replicaFaultSoakIterations        = "KITDB_REPLICA_SOAK_ITERATIONS"
	replicaFaultSoakSeed              = "KITDB_REPLICA_SOAK_SEED"
	replicaFaultExitCode              = 86
	maximumReplicaFaultSoakIterations = 10_000

	replicaFaultAfterRegistration = "after-registration"
	replicaFaultAfterPublish      = "after-publish"
	replicaFaultAfterTargetCommit = "after-target-commit"
	replicaFaultAfterApply        = "after-apply"
	replicaFaultAfterPin          = "after-source-pin"
	replicaFaultAfterAcknowledge  = "after-acknowledge"
)

var replicaFaultStages = []string{
	replicaFaultAfterRegistration,
	replicaFaultAfterPublish,
	replicaFaultAfterTargetCommit,
	replicaFaultAfterApply,
	replicaFaultAfterPin,
	replicaFaultAfterAcknowledge,
}

type replicaFaultTopology struct {
	source  string
	target  string
	mailbox string
	pinName string
}

func TestReplicaFileLinkHardCrashMatrix(t *testing.T) {
	for _, stage := range replicaFaultStages {
		t.Run(stage, func(t *testing.T) {
			topology := prepareReplicaFaultTopology(t, t.TempDir())
			expected := map[string]string{"base": "anchor"}
			commitReplicaFaultValues(t, topology, []replicaFaultValue{
				{key: "product/1", value: "one"},
				{key: "product/2", value: "two"},
				{key: "product/3", value: "three"},
			})
			expected["product/1"] = "one"
			expected["product/2"] = "two"
			expected["product/3"] = "three"

			runReplicaFaultChild(t, topology, stage)
			recoverReplicaFaultTopology(t, topology, expected, true)
		})
	}
}

// TestReplicaFileLinkCrashChild is invoked only as a subprocess by the fault
// lab. A successful child deliberately exits without running defers.
func TestReplicaFileLinkCrashChild(t *testing.T) {
	if os.Getenv(replicaFaultChildEnvironment) != "1" {
		return
	}
	topology := replicaFaultTopology{
		source:  os.Getenv(replicaFaultSourceEnvironment),
		target:  os.Getenv(replicaFaultTargetEnvironment),
		mailbox: os.Getenv(replicaFaultMailboxEnvironment),
		pinName: os.Getenv(replicaFaultPinEnvironment),
	}
	if topology.source == "" || topology.target == "" || topology.mailbox == "" ||
		topology.pinName == "" {
		t.Fatal("replica fault child topology is incomplete")
	}
	stage := os.Getenv(replicaFaultStageEnvironment)
	manager, err := newReplicaFaultManager()
	if err != nil {
		t.Fatal(err)
	}
	config := replicaFaultConfig(topology)
	if stage == replicaFaultAfterRegistration {
		if _, err := manager.RegisterReplicaFileLink(config); err != nil {
			t.Fatal(err)
		}
		os.Exit(replicaFaultExitCode)
	}

	published := scheduleNodeReplicaFilePublish(t, manager, ReplicaFilePublishRequest{
		Source: topology.source, Mailbox: topology.mailbox,
		SourceOptions: config.SourceOptions, PinName: topology.pinName,
		BatchLimits: config.BatchLimits, Priority: MaintenanceBackground,
	})
	if !published.Published || published.Publication == nil ||
		published.Publication.Transactions < 2 {
		t.Fatalf("fault child publication = %#v", published)
	}
	if stage == replicaFaultAfterPublish {
		os.Exit(replicaFaultExitCode)
	}
	if stage == replicaFaultAfterTargetCommit {
		crashReplicaFaultAfterTargetCommit(t, topology, published)
	}

	applied := scheduleNodeReplicaFileApply(t, manager, ReplicaFileApplyRequest{
		Target: topology.target, Mailbox: topology.mailbox,
		Priority: MaintenanceBackground,
	})
	if !applied.Found || applied.Apply == nil ||
		applied.Apply.To != published.Publication.To {
		t.Fatalf("fault child apply = %#v", applied)
	}
	if stage == replicaFaultAfterApply {
		os.Exit(replicaFaultExitCode)
	}
	if stage == replicaFaultAfterPin {
		lease, err := manager.Acquire(
			context.Background(), topology.source, config.SourceOptions,
		)
		if err != nil {
			t.Fatal(err)
		}
		pin, acknowledgeErr := lease.DB().AcknowledgeReplicaBatch(
			context.Background(), topology.pinName, kitdb.ReplicaAcknowledgement{
				Version: kitdb.ReplicaProtocolVersion,
				Cursor:  published.Publication.To,
			},
		)
		releaseErr := lease.Release()
		if acknowledgeErr != nil || releaseErr != nil ||
			pin.Cursor != published.Publication.To {
			t.Fatalf(
				"fault child source pin = (%#v, %v, release=%v)",
				pin, acknowledgeErr, releaseErr,
			)
		}
		os.Exit(replicaFaultExitCode)
	}
	if stage != replicaFaultAfterAcknowledge {
		t.Fatalf("unknown replica fault stage %q", stage)
	}
	acknowledged := scheduleNodeReplicaFileAcknowledge(
		t, manager, ReplicaFileAcknowledgeRequest{
			Source: topology.source, Mailbox: topology.mailbox,
			SourceOptions: config.SourceOptions, PinName: topology.pinName,
			Priority: MaintenanceBackground,
		},
	)
	if !acknowledged.Found || acknowledged.Acknowledge == nil ||
		acknowledged.Pin.Cursor != published.Publication.To {
		t.Fatalf("fault child acknowledgement = %#v", acknowledged)
	}
	os.Exit(replicaFaultExitCode)
}

func TestReplicaFileLinkHardCrashSoak(t *testing.T) {
	rawIterations := os.Getenv(replicaFaultSoakIterations)
	if rawIterations == "" {
		t.Skip("set KITDB_REPLICA_SOAK_ITERATIONS to run the hard-crash campaign")
	}
	iterations, err := strconv.Atoi(rawIterations)
	if err != nil || iterations < 1 || iterations > maximumReplicaFaultSoakIterations {
		t.Fatalf(
			"%s must be between 1 and %d, got %q",
			replicaFaultSoakIterations, maximumReplicaFaultSoakIterations, rawIterations,
		)
	}
	seed := int64(1)
	if rawSeed := os.Getenv(replicaFaultSoakSeed); rawSeed != "" {
		seed, err = strconv.ParseInt(rawSeed, 10, 64)
		if err != nil {
			t.Fatalf("invalid %s %q: %v", replicaFaultSoakSeed, rawSeed, err)
		}
	}
	t.Logf("replica hard-crash soak: iterations=%d seed=%d", iterations, seed)

	topology := prepareReplicaFaultTopology(t, t.TempDir())
	model := map[string]string{"base": "anchor"}
	random := rand.New(rand.NewSource(seed))
	for iteration := range iterations {
		values := []replicaFaultValue{
			{
				key:   fmt.Sprintf("soak/%06d/a", iteration),
				value: fmt.Sprintf("value-%06d-a", iteration),
			},
			{
				key:   fmt.Sprintf("soak/%06d/b", iteration),
				value: fmt.Sprintf("value-%06d-b", iteration),
			},
		}
		commitReplicaFaultValues(t, topology, values)
		for _, value := range values {
			model[value.key] = value.value
		}
		stage := replicaFaultStages[random.Intn(len(replicaFaultStages))]
		t.Logf("iteration=%d stage=%s", iteration, stage)
		runReplicaFaultChild(t, topology, stage)

		expected := map[string]string{
			values[0].key: values[0].value,
			values[1].key: values[1].value,
		}
		last := iteration == iterations-1
		if last {
			expected = model
		}
		recoverReplicaFaultTopology(
			t, topology, expected, iteration%16 == 0 || last,
		)
	}
}

type replicaFaultValue struct {
	key   string
	value string
}

func prepareReplicaFaultTopology(t *testing.T, root string) replicaFaultTopology {
	t.Helper()
	topology := replicaFaultTopology{
		source:  filepath.Join(root, "source.kitdb"),
		target:  filepath.Join(root, "replica.kitdb"),
		mailbox: filepath.Join(root, "mailbox"),
		pinName: "replica/fault-lab",
	}
	manager, err := newReplicaFaultManager()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close replica fault setup manager: %v", err)
		}
	}()
	options := kitdb.OpenOptions{RetainHistory: true}
	commitNodeBackupValue(t, manager, topology.source, options, "base", "anchor")
	scheduleNodeReplicaBootstrap(
		t, manager, topology.source, topology.target, options, topology.pinName,
	)
	return topology
}

func commitReplicaFaultValues(
	t *testing.T,
	topology replicaFaultTopology,
	values []replicaFaultValue,
) {
	t.Helper()
	manager, err := newReplicaFaultManager()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close replica fault commit manager: %v", err)
		}
	}()
	options := kitdb.OpenOptions{RetainHistory: true}
	for _, value := range values {
		commitNodeBackupValue(
			t, manager, topology.source, options, value.key, value.value,
		)
	}
}

func runReplicaFaultChild(
	t *testing.T,
	topology replicaFaultTopology,
	stage string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx, os.Args[0],
		"-test.run=^TestReplicaFileLinkCrashChild$",
		"-test.count=1",
		"-test.timeout=20s",
	)
	command.Env = append(os.Environ(),
		replicaFaultChildEnvironment+"=1",
		replicaFaultStageEnvironment+"="+stage,
		replicaFaultSourceEnvironment+"="+topology.source,
		replicaFaultTargetEnvironment+"="+topology.target,
		replicaFaultMailboxEnvironment+"="+topology.mailbox,
		replicaFaultPinEnvironment+"="+topology.pinName,
	)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("replica fault child %q timed out: %v\n%s", stage, ctx.Err(), output)
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != replicaFaultExitCode {
		t.Fatalf(
			"replica fault child %q = %v (exit=%d), want exit %d\n%s",
			stage, err, replicaFaultProcessExitCode(exitError),
			replicaFaultExitCode, output,
		)
	}
}

func replicaFaultProcessExitCode(exitError *exec.ExitError) int {
	if exitError == nil {
		return 0
	}
	return exitError.ExitCode()
}

func crashReplicaFaultAfterTargetCommit(
	t *testing.T,
	topology replicaFaultTopology,
	published ReplicaFilePublishResult,
) {
	t.Helper()
	replica, err := kitdb.OpenReplica(topology.target, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := kitdb.OpenReplicaFileTransport(
		topology.mailbox, kitdb.ReplicaFileTransportLimits{},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	firstTransaction := published.Publication.From.Transaction + 1
	remove, err := replica.AddCommitListener(func(event kitdb.CommitEvent) {
		if event.Transaction == firstTransaction {
			cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	partial, found, applyErr := transport.ApplyNext(ctx, replica)
	remove()
	if !found || !errors.Is(applyErr, context.Canceled) ||
		partial.AppliedTransactions != 1 ||
		partial.To.Transaction != firstTransaction {
		t.Fatalf(
			"fault child target prefix = (%#v, found=%t, err=%v)",
			partial, found, applyErr,
		)
	}
	os.Exit(replicaFaultExitCode)
}

func recoverReplicaFaultTopology(
	t *testing.T,
	topology replicaFaultTopology,
	expected map[string]string,
	verify bool,
) {
	t.Helper()
	manager, err := newReplicaFaultManager()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close replica fault recovery manager: %v", err)
		}
	}()
	config := replicaFaultConfig(topology)
	if _, err := manager.RegisterReplicaFileLink(config); err != nil {
		t.Fatal(err)
	}
	caughtUp := false
	for range 8 {
		cycle, err := manager.RunReplicaFileLinkOnce(context.Background(), config.Name)
		if err != nil {
			t.Fatal(err)
		}
		if cycle.CaughtUp {
			caughtUp = true
			break
		}
	}
	if !caughtUp {
		health, _ := manager.ReplicaFileLinkHealth(config.Name)
		t.Fatalf("replica fault recovery did not catch up: %#v", health)
	}
	settled, err := manager.RunReplicaFileLinkOnce(context.Background(), config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !settled.CaughtUp || settled.Progress || settled.Publish == nil ||
		!settled.Publish.NoChanges {
		t.Fatalf("replica fault settled cycle = %#v", settled)
	}
	for key, want := range expected {
		assertNodeReplicaValue(
			t, manager, topology.target, kitdb.OpenOptions{}, key, want,
		)
	}
	assertNodeBackupPin(
		t, manager, topology.source, config.SourceOptions,
		topology.pinName, settled.Publish.SourceBoundary,
	)
	if verify {
		verifyReplicaFaultDatabase(
			t, manager, topology.source, config.SourceOptions,
		)
		verifyReplicaFaultDatabase(
			t, manager, topology.target, kitdb.OpenOptions{Replica: true},
		)
	}
	transport, err := kitdb.OpenReplicaFileTransport(
		topology.mailbox, config.TransportLimits,
	)
	if err != nil {
		t.Fatal(err)
	}
	mailbox, err := transport.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mailbox != (kitdb.ReplicaFileTransportStats{}) {
		t.Fatalf("replica fault mailbox after recovery = %#v", mailbox)
	}
	health, err := manager.ReplicaFileLinkHealth(config.Name)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != ReplicaFileLinkPaused || health.Failures != 0 ||
		!health.LastCycle.CaughtUp || manager.Stats().ActiveLeases != 0 {
		t.Fatalf("replica fault recovery health = %#v stats=%#v", health, manager.Stats())
	}
}

func verifyReplicaFaultDatabase(
	t *testing.T,
	manager *Manager,
	path string,
	options kitdb.OpenOptions,
) {
	t.Helper()
	ticket, err := manager.ScheduleVerify(
		context.Background(), path, options, MaintenanceBackground,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Wait(context.Background()); err != nil {
		t.Fatalf("verify recovered replica fault database %q: %v", path, err)
	}
}

func replicaFaultConfig(topology replicaFaultTopology) ReplicaFileLinkConfig {
	return ReplicaFileLinkConfig{
		Name: topology.pinName, Source: topology.source, Target: topology.target,
		Mailbox:       topology.mailbox,
		SourceOptions: kitdb.OpenOptions{RetainHistory: true},
		PinName:       topology.pinName,
		BatchLimits:   kitdb.ReplicaBatchLimits{MaxTransactions: 8},
		Priority:      MaintenanceBackground, StartPaused: true,
	}
}

func newReplicaFaultManager() (*Manager, error) {
	return NewManager(Limits{
		MaxOpenDatabases: 8, MaxPageCacheBytes: 8 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
		MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 16,
		MaxMaintenancePerDatabase: 2, MaintenanceTimeout: time.Minute,
		ReplicaFileLinks: ReplicaFileLinkLimits{
			MaxLinks: 4, MaxConcurrent: 1,
			ActiveInterval: time.Millisecond, IdleInterval: time.Second,
			InitialBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
		},
	})
}
