package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/kitwork/engine/kitdb"
)

const (
	defaultMaxReplicaFileLinks           = 256
	defaultReplicaFileLinkActiveInterval = 25 * time.Millisecond
	defaultReplicaFileLinkIdleInterval   = 30 * time.Second
	defaultReplicaFileLinkInitialBackoff = 250 * time.Millisecond
	defaultReplicaFileLinkMaxBackoff     = time.Minute

	maximumReplicaFileLinks        = 100_000
	maximumReplicaFileLinkInterval = 24 * time.Hour
)

var (
	ErrReplicaFileLinkLimit = errors.New(
		"kitdb node: filesystem replica link limit reached",
	)
	ErrReplicaFileLinkExists = errors.New(
		"kitdb node: filesystem replica link already has different configuration",
	)
	ErrReplicaFileLinkNotFound = errors.New(
		"kitdb node: filesystem replica link does not exist",
	)
	ErrReplicaFileLinkBusy = errors.New(
		"kitdb node: filesystem replica link is queued or running",
	)
	ErrReplicaFileLinkTopology = errors.New(
		"kitdb node: filesystem replica target or mailbox is already owned",
	)
)

// ReplicaFileLinkLimits bound the process-local controller. One dispatcher and
// a fixed worker pool serve every registered link; idle links own no goroutine
// or database lease. Zero values select bounded defaults.
type ReplicaFileLinkLimits struct {
	MaxLinks       int
	MaxConcurrent  int
	ActiveInterval time.Duration
	IdleInterval   time.Duration
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type normalizedReplicaFileLinkLimits struct {
	maxLinks       int
	maxConcurrent  int
	activeInterval time.Duration
	idleInterval   time.Duration
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

// ReplicaFileLinkConfig describes one host-owned local filesystem topology.
// Configuration is registered again after process restart; durable progress is
// reconstructed only from the source pin, target WAL, and mailbox artifacts.
type ReplicaFileLinkConfig struct {
	Name            string
	Source          string
	Target          string
	Mailbox         string
	SourceOptions   kitdb.OpenOptions
	TargetOptions   kitdb.OpenOptions
	PinName         string
	BatchLimits     kitdb.ReplicaBatchLimits
	TransportLimits kitdb.ReplicaFileTransportLimits
	Priority        MaintenancePriority
	StartPaused     bool
}

// ReplicaFileLinkState is the current process-local controller state.
type ReplicaFileLinkState string

const (
	ReplicaFileLinkIdle    ReplicaFileLinkState = "idle"
	ReplicaFileLinkQueued  ReplicaFileLinkState = "queued"
	ReplicaFileLinkRunning ReplicaFileLinkState = "running"
	ReplicaFileLinkPaused  ReplicaFileLinkState = "paused"
	ReplicaFileLinkBackoff ReplicaFileLinkState = "backoff"
)

// ReplicaFileLinkCycle is one bounded publish, apply, and acknowledge attempt.
// It contains metadata only and never retains transaction bodies.
type ReplicaFileLinkCycle struct {
	Name        string
	Publish     *ReplicaFilePublishResult
	Apply       *ReplicaFileApplyResult
	Acknowledge *ReplicaFileAcknowledgeResult
	Progress    bool
	CaughtUp    bool
	StartedAt   time.Time
	FinishedAt  time.Time
	Duration    time.Duration
}

// ReplicaFileLinkHealth is a bounded snapshot for one explicitly named link.
// Global Stats intentionally contain no per-link labels.
type ReplicaFileLinkHealth struct {
	Name                string
	State               ReplicaFileLinkState
	Paused              bool
	Attempts            uint64
	Successes           uint64
	Failures            uint64
	Cancellations       uint64
	ConsecutiveFailures uint32
	LastStartedAt       time.Time
	LastFinishedAt      time.Time
	LastSuccessAt       time.Time
	NextRunAt           time.Time
	LastError           string
	LastCycle           ReplicaFileLinkCycle
}

type normalizedReplicaFileLinkConfig struct {
	public     ReplicaFileLinkConfig
	sourceKey  string
	targetKey  string
	mailboxKey string
	pinKey     string
}

type replicaFileLink struct {
	config normalizedReplicaFileLinkConfig

	paused  bool
	queued  bool
	running bool
	run     *replicaFileLinkRun
	nextRun time.Time

	attempts            uint64
	successes           uint64
	failures            uint64
	cancellations       uint64
	consecutiveFailures uint32
	lastStartedAt       time.Time
	lastFinishedAt      time.Time
	lastSuccessAt       time.Time
	lastErr             error
	lastCycle           ReplicaFileLinkCycle
}

type replicaFileLinkRun struct {
	manual bool
	done   chan struct{}
	cycle  ReplicaFileLinkCycle
	err    error
}

type replicaFileLinkWork struct {
	name string
	run  *replicaFileLinkRun
}

func normalizeReplicaFileLinkLimits(
	limits ReplicaFileLinkLimits,
	maxMaintenance int,
) (normalizedReplicaFileLinkLimits, error) {
	if limits.MaxLinks == 0 {
		limits.MaxLinks = defaultMaxReplicaFileLinks
	}
	if limits.MaxConcurrent == 0 {
		limits.MaxConcurrent = min(2, maxMaintenance)
	}
	if limits.ActiveInterval == 0 {
		limits.ActiveInterval = defaultReplicaFileLinkActiveInterval
	}
	if limits.IdleInterval == 0 {
		limits.IdleInterval = defaultReplicaFileLinkIdleInterval
	}
	if limits.InitialBackoff == 0 {
		limits.InitialBackoff = defaultReplicaFileLinkInitialBackoff
	}
	if limits.MaxBackoff == 0 {
		limits.MaxBackoff = defaultReplicaFileLinkMaxBackoff
	}
	if limits.MaxLinks < 1 || limits.MaxLinks > maximumReplicaFileLinks {
		return normalizedReplicaFileLinkLimits{}, fmt.Errorf(
			"kitdb node: ReplicaFileLinks.MaxLinks must be between 1 and %d",
			maximumReplicaFileLinks,
		)
	}
	if limits.MaxConcurrent < 1 || limits.MaxConcurrent > limits.MaxLinks ||
		limits.MaxConcurrent > maxMaintenance {
		return normalizedReplicaFileLinkLimits{}, fmt.Errorf(
			"kitdb node: ReplicaFileLinks.MaxConcurrent must be between 1 and both MaxLinks and MaxConcurrentMaintenance",
		)
	}
	if err := validateReplicaFileLinkDuration(
		"ActiveInterval", limits.ActiveInterval,
	); err != nil {
		return normalizedReplicaFileLinkLimits{}, err
	}
	if err := validateReplicaFileLinkDuration(
		"IdleInterval", limits.IdleInterval,
	); err != nil {
		return normalizedReplicaFileLinkLimits{}, err
	}
	if limits.IdleInterval < limits.ActiveInterval {
		return normalizedReplicaFileLinkLimits{}, fmt.Errorf(
			"kitdb node: ReplicaFileLinks.IdleInterval cannot be shorter than ActiveInterval",
		)
	}
	if err := validateReplicaFileLinkDuration(
		"InitialBackoff", limits.InitialBackoff,
	); err != nil {
		return normalizedReplicaFileLinkLimits{}, err
	}
	if err := validateReplicaFileLinkDuration(
		"MaxBackoff", limits.MaxBackoff,
	); err != nil {
		return normalizedReplicaFileLinkLimits{}, err
	}
	if limits.MaxBackoff < limits.InitialBackoff {
		return normalizedReplicaFileLinkLimits{}, fmt.Errorf(
			"kitdb node: ReplicaFileLinks.MaxBackoff cannot be shorter than InitialBackoff",
		)
	}
	return normalizedReplicaFileLinkLimits{
		maxLinks: limits.MaxLinks, maxConcurrent: limits.MaxConcurrent,
		activeInterval: limits.ActiveInterval, idleInterval: limits.IdleInterval,
		initialBackoff: limits.InitialBackoff, maxBackoff: limits.MaxBackoff,
	}, nil
}

func validateReplicaFileLinkDuration(name string, value time.Duration) error {
	if value < time.Millisecond || value > maximumReplicaFileLinkInterval {
		return fmt.Errorf(
			"kitdb node: ReplicaFileLinks.%s must be between 1ms and %s",
			name, maximumReplicaFileLinkInterval,
		)
	}
	return nil
}

// RegisterReplicaFileLink registers or returns one exact link configuration.
// An unpaused link becomes eligible immediately. Registration never opens a
// database or creates a mailbox.
func (manager *Manager) RegisterReplicaFileLink(
	config ReplicaFileLinkConfig,
) (ReplicaFileLinkHealth, error) {
	if manager == nil {
		return ReplicaFileLinkHealth{}, ErrClosed
	}
	normalized, err := manager.normalizeReplicaFileLinkConfig(config)
	if err != nil {
		return ReplicaFileLinkHealth{}, err
	}
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	if manager.replicaFileLinkClosed || manager.ctx.Err() != nil {
		return ReplicaFileLinkHealth{}, ErrClosed
	}
	if current := manager.replicaFileLinks[normalized.public.Name]; current != nil {
		if current.config != normalized {
			return ReplicaFileLinkHealth{}, ErrReplicaFileLinkExists
		}
		return replicaFileLinkHealthLocked(current, time.Now()), nil
	}
	if len(manager.replicaFileLinks) >= manager.limits.replicaFileLinks.maxLinks {
		return ReplicaFileLinkHealth{}, ErrReplicaFileLinkLimit
	}
	if owner := manager.replicaFileLinkPins[normalized.pinKey]; owner != "" {
		return ReplicaFileLinkHealth{}, errors.Join(
			ErrReplicaFileLinkTopology,
			fmt.Errorf("kitdb node: replica source pin is owned by link %q", owner),
		)
	}
	if owner := manager.replicaFileLinkTargets[normalized.targetKey]; owner != "" {
		return ReplicaFileLinkHealth{}, errors.Join(
			ErrReplicaFileLinkTopology,
			fmt.Errorf("kitdb node: replica target is owned by link %q", owner),
		)
	}
	if owner := manager.replicaFileLinkMailboxes[normalized.mailboxKey]; owner != "" {
		return ReplicaFileLinkHealth{}, errors.Join(
			ErrReplicaFileLinkTopology,
			fmt.Errorf("kitdb node: replica mailbox is owned by link %q", owner),
		)
	}
	if owner := manager.replicaFileLinkMailboxes[normalized.sourceKey]; owner != "" {
		return ReplicaFileLinkHealth{}, errors.Join(
			ErrReplicaFileLinkTopology,
			fmt.Errorf("kitdb node: replica source is the mailbox of link %q", owner),
		)
	}
	if owner := manager.replicaFileLinkMailboxes[normalized.targetKey]; owner != "" {
		return ReplicaFileLinkHealth{}, errors.Join(
			ErrReplicaFileLinkTopology,
			fmt.Errorf("kitdb node: replica target is the mailbox of link %q", owner),
		)
	}
	if manager.replicaFileLinkDatabases[normalized.mailboxKey] != 0 {
		return ReplicaFileLinkHealth{}, errors.Join(
			ErrReplicaFileLinkTopology,
			fmt.Errorf("kitdb node: replica mailbox is already a source or target database"),
		)
	}
	if err := requireReplicaFileLinkMailbox(normalized.public.Mailbox); err != nil {
		return ReplicaFileLinkHealth{}, err
	}
	now := time.Now()
	link := &replicaFileLink{
		config: normalized, paused: normalized.public.StartPaused, nextRun: now,
	}
	manager.replicaFileLinks[normalized.public.Name] = link
	manager.replicaFileLinkTargets[normalized.targetKey] = normalized.public.Name
	manager.replicaFileLinkMailboxes[normalized.mailboxKey] = normalized.public.Name
	manager.replicaFileLinkPins[normalized.pinKey] = normalized.public.Name
	manager.replicaFileLinkDatabases[normalized.sourceKey]++
	manager.replicaFileLinkDatabases[normalized.targetKey]++
	manager.startReplicaFileLinksLocked()
	manager.signalReplicaFileLinksLocked()
	return replicaFileLinkHealthLocked(link, now), nil
}

func requireReplicaFileLinkMailbox(path string) error {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("kitdb node: inspect replica mailbox %q: %w", path, err)
	case !info.IsDir():
		return fmt.Errorf("kitdb node: replica mailbox %q is not a directory", path)
	default:
		return nil
	}
}

func (manager *Manager) normalizeReplicaFileLinkConfig(
	config ReplicaFileLinkConfig,
) (normalizedReplicaFileLinkConfig, error) {
	if err := kitdb.ValidateHistoryPinName(config.Name); err != nil {
		return normalizedReplicaFileLinkConfig{}, fmt.Errorf(
			"kitdb node: invalid replica link name: %w", err,
		)
	}
	if !config.SourceOptions.RetainHistory {
		return normalizedReplicaFileLinkConfig{}, ErrReplicaFileHistoryRequired
	}
	if err := kitdb.ValidateHistoryPinName(config.PinName); err != nil {
		return normalizedReplicaFileLinkConfig{}, err
	}
	if !validMaintenancePriority(config.Priority) {
		return normalizedReplicaFileLinkConfig{}, fmt.Errorf(
			"kitdb node: invalid replica link maintenance priority %d", config.Priority,
		)
	}
	if err := kitdb.ValidateReplicaBatchLimits(config.BatchLimits); err != nil {
		return normalizedReplicaFileLinkConfig{}, err
	}
	if err := kitdb.ValidateReplicaFileTransportLimits(config.TransportLimits); err != nil {
		return normalizedReplicaFileLinkConfig{}, err
	}
	source, sourceKey, err := canonicalDatabasePath(config.Source)
	if err != nil {
		return normalizedReplicaFileLinkConfig{}, err
	}
	target, targetKey, err := canonicalDatabasePath(config.Target)
	if err != nil {
		return normalizedReplicaFileLinkConfig{}, fmt.Errorf(
			"kitdb node: resolve replica link target: %w", err,
		)
	}
	mailbox, mailboxKey, err := canonicalDatabasePath(config.Mailbox)
	if err != nil {
		return normalizedReplicaFileLinkConfig{}, fmt.Errorf(
			"kitdb node: resolve replica link mailbox: %w", err,
		)
	}
	if sourceKey == targetKey || sourceKey == mailboxKey || targetKey == mailboxKey {
		return normalizedReplicaFileLinkConfig{}, ErrReplicaFileSamePath
	}
	if err := requireReplicaDatabasePath(source, "source"); err != nil {
		return normalizedReplicaFileLinkConfig{}, err
	}
	if err := requireReplicaDatabasePath(target, "target"); err != nil {
		return normalizedReplicaFileLinkConfig{}, err
	}
	sourceOptions, _, err := manager.normalizeOpenOptions(config.SourceOptions)
	if err != nil {
		return normalizedReplicaFileLinkConfig{}, err
	}
	config.TargetOptions.Replica = true
	targetOptions, _, err := manager.normalizeOpenOptions(config.TargetOptions)
	if err != nil {
		return normalizedReplicaFileLinkConfig{}, err
	}
	config.Source = source
	config.Target = target
	config.Mailbox = mailbox
	config.SourceOptions = sourceOptions
	config.TargetOptions = targetOptions
	config.BatchLimits = normalizeReplicaBatchLimitsForOperation(config.BatchLimits)
	config.TransportLimits = normalizeReplicaFileLimitsForOperation(config.TransportLimits)
	return normalizedReplicaFileLinkConfig{
		public: config, sourceKey: sourceKey, targetKey: targetKey, mailboxKey: mailboxKey,
		pinKey: sourceKey + "\x00" + config.PinName,
	}, nil
}

// PauseReplicaFileLink prevents future automatic cycles. A running cycle drains
// normally, and RunReplicaFileLinkOnce remains available for explicit repair.
func (manager *Manager) PauseReplicaFileLink(name string) (ReplicaFileLinkHealth, error) {
	return manager.setReplicaFileLinkPaused(name, true)
}

// ResumeReplicaFileLink enables automatic cycles and schedules an immediate
// reconciliation attempt.
func (manager *Manager) ResumeReplicaFileLink(name string) (ReplicaFileLinkHealth, error) {
	return manager.setReplicaFileLinkPaused(name, false)
}

func (manager *Manager) setReplicaFileLinkPaused(
	name string,
	paused bool,
) (ReplicaFileLinkHealth, error) {
	if manager == nil {
		return ReplicaFileLinkHealth{}, ErrClosed
	}
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	if manager.replicaFileLinkClosed {
		return ReplicaFileLinkHealth{}, ErrClosed
	}
	link := manager.replicaFileLinks[name]
	if link == nil {
		return ReplicaFileLinkHealth{}, ErrReplicaFileLinkNotFound
	}
	link.paused = paused
	if !paused {
		link.nextRun = time.Now()
	}
	manager.signalReplicaFileLinksLocked()
	return replicaFileLinkHealthLocked(link, time.Now()), nil
}

// WakeReplicaFileLink makes an unpaused link eligible immediately. It is a
// cheap hook for a future commit-driven adapter and does not wait for a cycle.
func (manager *Manager) WakeReplicaFileLink(name string) error {
	if manager == nil {
		return ErrClosed
	}
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	if manager.replicaFileLinkClosed {
		return ErrClosed
	}
	link := manager.replicaFileLinks[name]
	if link == nil {
		return ErrReplicaFileLinkNotFound
	}
	if !link.paused {
		link.nextRun = time.Now()
		manager.signalReplicaFileLinksLocked()
	}
	return nil
}

// RunReplicaFileLinkOnce schedules or joins one cycle. Canceling ctx stops only
// this waiter and never cancels a cycle shared with the automatic controller.
func (manager *Manager) RunReplicaFileLinkOnce(
	ctx context.Context,
	name string,
) (ReplicaFileLinkCycle, error) {
	if manager == nil {
		return ReplicaFileLinkCycle{}, ErrClosed
	}
	if ctx == nil {
		return ReplicaFileLinkCycle{}, fmt.Errorf(
			"kitdb node: replica link wait context is nil",
		)
	}
	if err := ctx.Err(); err != nil {
		return ReplicaFileLinkCycle{}, err
	}
	manager.replicaFileLinkMu.Lock()
	if manager.replicaFileLinkClosed || manager.ctx.Err() != nil {
		manager.replicaFileLinkMu.Unlock()
		return ReplicaFileLinkCycle{}, ErrClosed
	}
	link := manager.replicaFileLinks[name]
	if link == nil {
		manager.replicaFileLinkMu.Unlock()
		return ReplicaFileLinkCycle{}, ErrReplicaFileLinkNotFound
	}
	manager.startReplicaFileLinksLocked()
	run := link.run
	if run == nil {
		run = &replicaFileLinkRun{manual: true, done: make(chan struct{})}
		link.run = run
		link.nextRun = time.Now()
	} else {
		run.manual = true
	}
	manager.signalReplicaFileLinksLocked()
	manager.replicaFileLinkMu.Unlock()

	select {
	case <-run.done:
		return cloneReplicaFileLinkCycle(run.cycle), run.err
	case <-ctx.Done():
		return ReplicaFileLinkCycle{}, ctx.Err()
	}
}

// UnregisterReplicaFileLink removes one idle link configuration. Durable pin,
// target, and mailbox state are intentionally left untouched.
func (manager *Manager) UnregisterReplicaFileLink(name string) error {
	if manager == nil {
		return ErrClosed
	}
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	if manager.replicaFileLinkClosed {
		return ErrClosed
	}
	link := manager.replicaFileLinks[name]
	if link == nil {
		return ErrReplicaFileLinkNotFound
	}
	if link.queued || link.running || link.run != nil {
		return ErrReplicaFileLinkBusy
	}
	delete(manager.replicaFileLinks, name)
	delete(manager.replicaFileLinkTargets, link.config.targetKey)
	delete(manager.replicaFileLinkMailboxes, link.config.mailboxKey)
	delete(manager.replicaFileLinkPins, link.config.pinKey)
	for _, key := range []string{link.config.sourceKey, link.config.targetKey} {
		if manager.replicaFileLinkDatabases[key] <= 1 {
			delete(manager.replicaFileLinkDatabases, key)
		} else {
			manager.replicaFileLinkDatabases[key]--
		}
	}
	manager.signalReplicaFileLinksLocked()
	return nil
}

// ReplicaFileLinkHealth returns one named process-local health snapshot.
func (manager *Manager) ReplicaFileLinkHealth(name string) (ReplicaFileLinkHealth, error) {
	if manager == nil {
		return ReplicaFileLinkHealth{}, ErrClosed
	}
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	link := manager.replicaFileLinks[name]
	if link == nil {
		return ReplicaFileLinkHealth{}, ErrReplicaFileLinkNotFound
	}
	return replicaFileLinkHealthLocked(link, time.Now()), nil
}

// ReplicaFileLinks returns bounded health snapshots sorted by link name.
func (manager *Manager) ReplicaFileLinks() []ReplicaFileLinkHealth {
	if manager == nil {
		return nil
	}
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	names := make([]string, 0, len(manager.replicaFileLinks))
	for name := range manager.replicaFileLinks {
		names = append(names, name)
	}
	sort.Strings(names)
	health := make([]ReplicaFileLinkHealth, 0, len(names))
	now := time.Now()
	for _, name := range names {
		health = append(health, replicaFileLinkHealthLocked(
			manager.replicaFileLinks[name], now,
		))
	}
	return health
}

func replicaFileLinkHealthLocked(
	link *replicaFileLink,
	now time.Time,
) ReplicaFileLinkHealth {
	state := ReplicaFileLinkIdle
	switch {
	case link.running:
		state = ReplicaFileLinkRunning
	case link.queued:
		state = ReplicaFileLinkQueued
	case link.paused:
		state = ReplicaFileLinkPaused
	case link.consecutiveFailures != 0 && link.nextRun.After(now):
		state = ReplicaFileLinkBackoff
	}
	lastError := ""
	if link.lastErr != nil {
		lastError = link.lastErr.Error()
	}
	return ReplicaFileLinkHealth{
		Name: link.config.public.Name, State: state, Paused: link.paused,
		Attempts: link.attempts, Successes: link.successes,
		Failures: link.failures, Cancellations: link.cancellations,
		ConsecutiveFailures: link.consecutiveFailures,
		LastStartedAt:       link.lastStartedAt, LastFinishedAt: link.lastFinishedAt,
		LastSuccessAt: link.lastSuccessAt, NextRunAt: link.nextRun,
		LastError: lastError, LastCycle: cloneReplicaFileLinkCycle(link.lastCycle),
	}
}
