package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb"
)

const (
	defaultMaxProductionPolicies     = 1_024
	defaultMaxProductionStoreEntries = 4_096
	defaultProductionInitialBackoff  = time.Second
	defaultProductionMaxBackoff      = 5 * time.Minute
	defaultProductionCycleTimeout    = 30 * time.Minute
	maximumProductionPolicies        = 100_000
	maximumProductionStoreEntries    = 1_000_000
	maximumProductionInterval        = 365 * 24 * time.Hour
	maximumProductionBackups         = 10_000
	productionBackupPrefix           = "anchor-v1-"
	productionBackupSuffix           = ".kitdb"
	productionBackupRandomBytes      = 8
)

var (
	ErrProductionPolicyLimit = errors.New(
		"kitdb node: production policy limit reached",
	)
	ErrProductionPolicyExists = errors.New(
		"kitdb node: production policy already has different configuration",
	)
	ErrProductionPolicyNotFound = errors.New(
		"kitdb node: production policy does not exist",
	)
	ErrProductionPolicyBusy = errors.New(
		"kitdb node: production policy is queued or running",
	)
	ErrProductionPolicyTopology = errors.New(
		"kitdb node: production source or backup store is already owned",
	)
	ErrProductionStoreLimit = errors.New(
		"kitdb node: production backup store entry limit reached",
	)
	ErrProductionUnsafe = errors.New(
		"kitdb node: production protection evidence is unsafe",
	)
)

// ProductionSupervisorLimits bound one process-local controller. One
// dispatcher and a fixed worker pool serve every policy; idle policies own no
// goroutine, database lease, or page cache.
type ProductionSupervisorLimits struct {
	MaxPolicies     int
	MaxConcurrent   int
	MaxStoreEntries int
	InitialBackoff  time.Duration
	MaxBackoff      time.Duration
	CycleTimeout    time.Duration
}

type normalizedProductionSupervisorLimits struct {
	maxPolicies     int
	maxConcurrent   int
	maxStoreEntries int
	initialBackoff  time.Duration
	maxBackoff      time.Duration
	cycleTimeout    time.Duration
}

// ProductionPolicyConfig declares one host-owned recovery policy. The source
// and backup directory are intentionally omitted from health snapshots.
// Configuration is registered again after restart; backup truth is recovered
// only from verified immutable anchors and the source's durable history pin.
type ProductionPolicyConfig struct {
	Name            string
	Source          string
	BackupDirectory string
	Publisher       string
	SourceOptions   kitdb.OpenOptions
	PinName         string
	BackupInterval  time.Duration
	MaxBackupAge    time.Duration
	RestoreInterval time.Duration
	MaxRestoreAge   time.Duration
	KeepBackups     int
	Priority        MaintenancePriority
	StartPaused     bool
}

// ProductionPolicyState describes controller activity, not data safety.
type ProductionPolicyState string

const (
	ProductionPolicyIdle    ProductionPolicyState = "idle"
	ProductionPolicyQueued  ProductionPolicyState = "queued"
	ProductionPolicyRunning ProductionPolicyState = "running"
	ProductionPolicyPaused  ProductionPolicyState = "paused"
	ProductionPolicyBackoff ProductionPolicyState = "backoff"
)

// ProductionReadiness is derived from verified recovery evidence and explicit
// policy ages. It is never set from a successful scheduler wake alone.
type ProductionReadiness string

const (
	ProductionReady    ProductionReadiness = "ready"
	ProductionDegraded ProductionReadiness = "degraded"
	ProductionUnsafe   ProductionReadiness = "unsafe"
)

// ProductionBackupEvidence is path-free metadata for one completely verified
// standalone anchor.
type ProductionBackupEvidence struct {
	DatabaseID  string
	Transaction uint64
	Records     uint64
	Bytes       int64
	SHA256      string
	CreatedAt   time.Time
	VerifiedAt  time.Time
	Resumed     bool
}

// ProductionPublicationEvidence proves that a host publisher retrieved and
// verified the exact current backup anchor. It deliberately omits destination
// paths, object keys, endpoints, and credentials.
type ProductionPublicationEvidence struct {
	Publisher       string
	DatabaseID      string
	Transaction     uint64
	Records         uint64
	Bytes           int64
	SHA256          string
	AnchorCreatedAt time.Time
	VerifiedAt      time.Time
	Resumed         bool
	PrunedAnchors   int
}

// ProductionRestoreEvidence proves that an anchor was restored to an
// independent file and retained the exact canonical logical digest.
type ProductionRestoreEvidence struct {
	DatabaseID    string
	Transaction   uint64
	Records       uint64
	LogicalBytes  uint64
	LogicalSHA256 string
	VerifiedAt    time.Time
	Duration      time.Duration
}

// ProductionPolicyCycle summarizes one bounded reconciliation attempt. It
// contains no source path, backup path, key, or value.
type ProductionPolicyCycle struct {
	Name          string
	Backup        *ProductionBackupEvidence
	Publication   *ProductionPublicationEvidence
	Restore       *ProductionRestoreEvidence
	PrunedBackups int
	StartedAt     time.Time
	FinishedAt    time.Time
	Duration      time.Duration

	publicationAttempted bool
	publicationFailed    bool
}

// ProductionPolicyHealth is one bounded host-facing snapshot.
type ProductionPolicyHealth struct {
	Name                string
	State               ProductionPolicyState
	Readiness           ProductionReadiness
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
	BackupAge           time.Duration
	PublicationAge      time.Duration
	RestoreAge          time.Duration
	PublicationRequired bool
	LastError           string
	LastBackup          *ProductionBackupEvidence
	LastPublication     *ProductionPublicationEvidence
	LastRestore         *ProductionRestoreEvidence
	LastCycle           ProductionPolicyCycle
}

type normalizedProductionPolicyConfig struct {
	public    ProductionPolicyConfig
	sourceKey string
	storeKey  string
}

type productionPolicy struct {
	config normalizedProductionPolicyConfig

	paused  bool
	queued  bool
	running bool
	run     *productionPolicyRun
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
	lastUnsafe          bool
	lastBackup          *ProductionBackupEvidence
	lastPublication     *ProductionPublicationEvidence
	lastRestore         *ProductionRestoreEvidence
	lastCycle           ProductionPolicyCycle
}

type productionPolicyRun struct {
	manual bool
	done   chan struct{}
	cycle  ProductionPolicyCycle
	err    error
}

type productionPolicyWork struct {
	name string
	run  *productionPolicyRun
}

type productionBackupRecord struct {
	anchor    kitdb.BackupAnchor
	createdAt time.Time
}

func normalizeProductionSupervisorLimits(
	limits ProductionSupervisorLimits,
	maxMaintenance int,
) (normalizedProductionSupervisorLimits, error) {
	if limits.MaxPolicies == 0 {
		limits.MaxPolicies = defaultMaxProductionPolicies
	}
	if limits.MaxConcurrent == 0 {
		limits.MaxConcurrent = min(2, maxMaintenance)
	}
	if limits.MaxStoreEntries == 0 {
		limits.MaxStoreEntries = defaultMaxProductionStoreEntries
	}
	if limits.InitialBackoff == 0 {
		limits.InitialBackoff = defaultProductionInitialBackoff
	}
	if limits.MaxBackoff == 0 {
		limits.MaxBackoff = defaultProductionMaxBackoff
	}
	if limits.CycleTimeout == 0 {
		limits.CycleTimeout = defaultProductionCycleTimeout
	}
	if limits.MaxPolicies < 1 || limits.MaxPolicies > maximumProductionPolicies {
		return normalizedProductionSupervisorLimits{}, fmt.Errorf(
			"kitdb node: Production.MaxPolicies must be between 1 and %d",
			maximumProductionPolicies,
		)
	}
	if limits.MaxConcurrent < 1 || limits.MaxConcurrent > limits.MaxPolicies ||
		limits.MaxConcurrent > maxMaintenance {
		return normalizedProductionSupervisorLimits{}, fmt.Errorf(
			"kitdb node: Production.MaxConcurrent must be between 1 and both MaxPolicies and MaxConcurrentMaintenance",
		)
	}
	if limits.MaxStoreEntries < 2 || limits.MaxStoreEntries > maximumProductionStoreEntries {
		return normalizedProductionSupervisorLimits{}, fmt.Errorf(
			"kitdb node: Production.MaxStoreEntries must be between 2 and %d",
			maximumProductionStoreEntries,
		)
	}
	for name, value := range map[string]time.Duration{
		"InitialBackoff": limits.InitialBackoff,
		"MaxBackoff":     limits.MaxBackoff,
		"CycleTimeout":   limits.CycleTimeout,
	} {
		if err := validateProductionDuration(name, value); err != nil {
			return normalizedProductionSupervisorLimits{}, err
		}
	}
	if limits.MaxBackoff < limits.InitialBackoff {
		return normalizedProductionSupervisorLimits{}, fmt.Errorf(
			"kitdb node: Production.MaxBackoff cannot be shorter than InitialBackoff",
		)
	}
	return normalizedProductionSupervisorLimits{
		maxPolicies: limits.MaxPolicies, maxConcurrent: limits.MaxConcurrent,
		maxStoreEntries: limits.MaxStoreEntries,
		initialBackoff:  limits.InitialBackoff, maxBackoff: limits.MaxBackoff,
		cycleTimeout: limits.CycleTimeout,
	}, nil
}

func validateProductionDuration(name string, value time.Duration) error {
	if value < time.Millisecond || value > maximumProductionInterval {
		return fmt.Errorf(
			"kitdb node: Production.%s must be between 1ms and %s",
			name, maximumProductionInterval,
		)
	}
	return nil
}

// RegisterProductionPolicy registers or returns one exact policy. Registration
// opens no database and creates no directory.
func (manager *Manager) RegisterProductionPolicy(
	config ProductionPolicyConfig,
) (ProductionPolicyHealth, error) {
	if manager == nil {
		return ProductionPolicyHealth{}, ErrClosed
	}
	normalized, err := manager.normalizeProductionPolicyConfig(config)
	if err != nil {
		return ProductionPolicyHealth{}, err
	}
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	if manager.productionClosed || manager.ctx.Err() != nil {
		return ProductionPolicyHealth{}, ErrClosed
	}
	if current := manager.productionPolicies[normalized.public.Name]; current != nil {
		if current.config != normalized {
			return ProductionPolicyHealth{}, ErrProductionPolicyExists
		}
		return productionPolicyHealthLocked(current, time.Now()), nil
	}
	if len(manager.productionPolicies) >= manager.limits.production.maxPolicies {
		return ProductionPolicyHealth{}, ErrProductionPolicyLimit
	}
	if owner := manager.productionSources[normalized.sourceKey]; owner != "" {
		return ProductionPolicyHealth{}, errors.Join(
			ErrProductionPolicyTopology,
			fmt.Errorf("kitdb node: production source is owned by policy %q", owner),
		)
	}
	if owner := manager.productionStores[normalized.storeKey]; owner != "" {
		return ProductionPolicyHealth{}, errors.Join(
			ErrProductionPolicyTopology,
			fmt.Errorf("kitdb node: production backup store is owned by policy %q", owner),
		)
	}
	if normalized.public.Publisher != "" {
		publisher := manager.productionPublishers[normalized.public.Publisher]
		if publisher == nil {
			return ProductionPolicyHealth{}, ErrProductionPublisherNotFound
		}
		if owner := manager.productionPublisherOwners[normalized.public.Publisher]; owner != "" {
			return ProductionPolicyHealth{}, errors.Join(
				ErrProductionPolicyTopology,
				fmt.Errorf("kitdb node: production publisher is owned by policy %q", owner),
			)
		}
		if err := validateProductionPublisherTopology(
			publisher, normalized.public.Source, normalized.public.BackupDirectory,
		); err != nil {
			return ProductionPolicyHealth{}, err
		}
	}
	now := time.Now()
	policy := &productionPolicy{
		config: normalized, paused: normalized.public.StartPaused, nextRun: now,
	}
	manager.productionPolicies[normalized.public.Name] = policy
	manager.productionSources[normalized.sourceKey] = normalized.public.Name
	manager.productionStores[normalized.storeKey] = normalized.public.Name
	if normalized.public.Publisher != "" {
		manager.productionPublisherOwners[normalized.public.Publisher] = normalized.public.Name
	}
	manager.startProductionLocked()
	manager.signalProductionLocked()
	return productionPolicyHealthLocked(policy, now), nil
}

func (manager *Manager) normalizeProductionPolicyConfig(
	config ProductionPolicyConfig,
) (normalizedProductionPolicyConfig, error) {
	if err := kitdb.ValidateHistoryPinName(config.Name); err != nil {
		return normalizedProductionPolicyConfig{}, fmt.Errorf(
			"kitdb node: invalid production policy name: %w", err,
		)
	}
	if config.Publisher != "" {
		if err := validateProductionPublisherName(config.Publisher); err != nil {
			return normalizedProductionPolicyConfig{}, err
		}
	}
	if !config.SourceOptions.RetainHistory {
		return normalizedProductionPolicyConfig{}, ErrBackupHistoryRequired
	}
	if strings.TrimSpace(config.PinName) == "" {
		config.PinName = "backup/production/" + config.Name
	}
	if err := kitdb.ValidateHistoryPinName(config.PinName); err != nil {
		return normalizedProductionPolicyConfig{}, err
	}
	for name, value := range map[string]time.Duration{
		"BackupInterval":  config.BackupInterval,
		"MaxBackupAge":    config.MaxBackupAge,
		"RestoreInterval": config.RestoreInterval,
		"MaxRestoreAge":   config.MaxRestoreAge,
	} {
		if err := validateProductionDuration(name, value); err != nil {
			return normalizedProductionPolicyConfig{}, err
		}
	}
	if config.MaxBackupAge < config.BackupInterval {
		return normalizedProductionPolicyConfig{}, fmt.Errorf(
			"kitdb node: production MaxBackupAge cannot be shorter than BackupInterval",
		)
	}
	if config.MaxRestoreAge < config.RestoreInterval {
		return normalizedProductionPolicyConfig{}, fmt.Errorf(
			"kitdb node: production MaxRestoreAge cannot be shorter than RestoreInterval",
		)
	}
	if config.KeepBackups < 2 || config.KeepBackups > maximumProductionBackups ||
		config.KeepBackups > manager.limits.production.maxStoreEntries {
		return normalizedProductionPolicyConfig{}, fmt.Errorf(
			"kitdb node: production KeepBackups must be between 2 and MaxStoreEntries",
		)
	}
	if !validMaintenancePriority(config.Priority) {
		return normalizedProductionPolicyConfig{}, fmt.Errorf(
			"kitdb node: invalid production maintenance priority %d", config.Priority,
		)
	}
	source, sourceKey, err := canonicalDatabasePath(config.Source)
	if err != nil {
		return normalizedProductionPolicyConfig{}, err
	}
	if err := requireReplicaDatabasePath(source, "production source"); err != nil {
		return normalizedProductionPolicyConfig{}, err
	}
	store, storeKey, err := canonicalProductionDirectory(config.BackupDirectory)
	if err != nil {
		return normalizedProductionPolicyConfig{}, err
	}
	inside, err := pathInsideDirectory(store, source)
	if err != nil {
		return normalizedProductionPolicyConfig{}, err
	}
	if inside {
		return normalizedProductionPolicyConfig{}, fmt.Errorf(
			"kitdb node: production source must not be stored inside its backup directory",
		)
	}
	historyInside, err := pathInsideDirectory(source+".history", store)
	if err != nil {
		return normalizedProductionPolicyConfig{}, err
	}
	if historyInside {
		return normalizedProductionPolicyConfig{}, fmt.Errorf(
			"kitdb node: production backup directory must not be inside source history",
		)
	}
	options, _, err := manager.normalizeOpenOptions(config.SourceOptions)
	if err != nil {
		return normalizedProductionPolicyConfig{}, err
	}
	config.Source = source
	config.BackupDirectory = store
	config.SourceOptions = options
	return normalizedProductionPolicyConfig{
		public: config, sourceKey: sourceKey, storeKey: storeKey,
	}, nil
}

func canonicalProductionDirectory(path string) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		return "", "", fmt.Errorf("kitdb node: production backup directory is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("kitdb node: resolve production backup directory: %w", err)
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Stat(absolute)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", "", fmt.Errorf("kitdb node: inspect production backup directory: %w", err)
	case !info.IsDir():
		return "", "", fmt.Errorf("kitdb node: production backup path is not a directory")
	}
	key := absolute
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return absolute, key, nil
}

func pathInsideDirectory(directory, path string) (bool, error) {
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return false, fmt.Errorf("kitdb node: compare production paths: %w", err)
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))), nil
}

func validateProductionPublisherTopology(
	publisher ProductionAnchorPublisher,
	source string,
	backupDirectory string,
) error {
	directoryPublisher, ok := publisher.(productionDirectoryPublisher)
	if !ok {
		return nil
	}
	directory := directoryPublisher.productionPublisherDirectory()
	checks := []struct {
		directory string
		path      string
	}{
		{directory: directory, path: source},
		{directory: source + ".history", path: directory},
		{directory: directory, path: backupDirectory},
		{directory: backupDirectory, path: directory},
	}
	for _, check := range checks {
		inside, err := pathInsideDirectory(check.directory, check.path)
		if err != nil {
			return err
		}
		if inside {
			return errors.Join(
				ErrProductionPolicyTopology,
				fmt.Errorf("kitdb node: directory publisher overlaps live or local backup storage"),
			)
		}
	}
	return nil
}

// PauseProductionPolicy stops future automatic cycles. A running cycle drains;
// explicit RunProductionPolicyOnce remains available.
func (manager *Manager) PauseProductionPolicy(name string) (ProductionPolicyHealth, error) {
	return manager.setProductionPolicyPaused(name, true)
}

// ResumeProductionPolicy schedules immediate reconciliation.
func (manager *Manager) ResumeProductionPolicy(name string) (ProductionPolicyHealth, error) {
	return manager.setProductionPolicyPaused(name, false)
}

func (manager *Manager) setProductionPolicyPaused(
	name string,
	paused bool,
) (ProductionPolicyHealth, error) {
	if manager == nil {
		return ProductionPolicyHealth{}, ErrClosed
	}
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	if manager.productionClosed {
		return ProductionPolicyHealth{}, ErrClosed
	}
	policy := manager.productionPolicies[name]
	if policy == nil {
		return ProductionPolicyHealth{}, ErrProductionPolicyNotFound
	}
	policy.paused = paused
	if !paused {
		policy.nextRun = time.Now()
	}
	manager.signalProductionLocked()
	return productionPolicyHealthLocked(policy, time.Now()), nil
}

// WakeProductionPolicy makes an unpaused policy eligible immediately.
func (manager *Manager) WakeProductionPolicy(name string) error {
	if manager == nil {
		return ErrClosed
	}
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	if manager.productionClosed {
		return ErrClosed
	}
	policy := manager.productionPolicies[name]
	if policy == nil {
		return ErrProductionPolicyNotFound
	}
	if !policy.paused {
		policy.nextRun = time.Now()
		manager.signalProductionLocked()
	}
	return nil
}

// RunProductionPolicyOnce schedules or joins one cycle. Canceling ctx stops
// only this waiter and never cancels a cycle shared with the automatic owner.
func (manager *Manager) RunProductionPolicyOnce(
	ctx context.Context,
	name string,
) (ProductionPolicyCycle, error) {
	if manager == nil {
		return ProductionPolicyCycle{}, ErrClosed
	}
	if ctx == nil {
		return ProductionPolicyCycle{}, fmt.Errorf(
			"kitdb node: production policy wait context is nil",
		)
	}
	if err := ctx.Err(); err != nil {
		return ProductionPolicyCycle{}, err
	}
	manager.productionMu.Lock()
	if manager.productionClosed || manager.ctx.Err() != nil {
		manager.productionMu.Unlock()
		return ProductionPolicyCycle{}, ErrClosed
	}
	policy := manager.productionPolicies[name]
	if policy == nil {
		manager.productionMu.Unlock()
		return ProductionPolicyCycle{}, ErrProductionPolicyNotFound
	}
	manager.startProductionLocked()
	run := policy.run
	if run == nil {
		run = &productionPolicyRun{manual: true, done: make(chan struct{})}
		policy.run = run
		policy.nextRun = time.Now()
	} else {
		run.manual = true
	}
	manager.signalProductionLocked()
	manager.productionMu.Unlock()

	select {
	case <-run.done:
		return cloneProductionPolicyCycle(run.cycle), run.err
	case <-ctx.Done():
		return ProductionPolicyCycle{}, ctx.Err()
	}
}

// UnregisterProductionPolicy removes one idle process-local configuration.
// Backup anchors and the durable source pin are intentionally preserved.
func (manager *Manager) UnregisterProductionPolicy(name string) error {
	if manager == nil {
		return ErrClosed
	}
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	if manager.productionClosed {
		return ErrClosed
	}
	policy := manager.productionPolicies[name]
	if policy == nil {
		return ErrProductionPolicyNotFound
	}
	if policy.queued || policy.running || policy.run != nil {
		return ErrProductionPolicyBusy
	}
	delete(manager.productionPolicies, name)
	delete(manager.productionSources, policy.config.sourceKey)
	delete(manager.productionStores, policy.config.storeKey)
	if policy.config.public.Publisher != "" {
		delete(manager.productionPublisherOwners, policy.config.public.Publisher)
	}
	manager.signalProductionLocked()
	return nil
}

// ProductionPolicyHealth returns one named path-free health snapshot.
func (manager *Manager) ProductionPolicyHealth(name string) (ProductionPolicyHealth, error) {
	if manager == nil {
		return ProductionPolicyHealth{}, ErrClosed
	}
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	policy := manager.productionPolicies[name]
	if policy == nil {
		return ProductionPolicyHealth{}, ErrProductionPolicyNotFound
	}
	return productionPolicyHealthLocked(policy, time.Now()), nil
}

// ProductionPolicies returns bounded health snapshots sorted by policy name.
func (manager *Manager) ProductionPolicies() []ProductionPolicyHealth {
	if manager == nil {
		return nil
	}
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	names := make([]string, 0, len(manager.productionPolicies))
	for name := range manager.productionPolicies {
		names = append(names, name)
	}
	sort.Strings(names)
	health := make([]ProductionPolicyHealth, 0, len(names))
	now := time.Now()
	for _, name := range names {
		health = append(health, productionPolicyHealthLocked(
			manager.productionPolicies[name], now,
		))
	}
	return health
}

func productionPolicyHealthLocked(
	policy *productionPolicy,
	now time.Time,
) ProductionPolicyHealth {
	state := ProductionPolicyIdle
	switch {
	case policy.running:
		state = ProductionPolicyRunning
	case policy.queued:
		state = ProductionPolicyQueued
	case policy.paused:
		state = ProductionPolicyPaused
	case policy.consecutiveFailures != 0 && policy.nextRun.After(now):
		state = ProductionPolicyBackoff
	}
	readiness, backupAge, restoreAge := productionReadinessLocked(policy, now)
	lastError := ""
	if policy.lastErr != nil {
		lastError = productionErrorSummary(policy.lastErr)
	}
	return ProductionPolicyHealth{
		Name: policy.config.public.Name, State: state, Readiness: readiness,
		Paused: policy.paused, Attempts: policy.attempts,
		Successes: policy.successes, Failures: policy.failures,
		Cancellations:       policy.cancellations,
		ConsecutiveFailures: policy.consecutiveFailures,
		LastStartedAt:       policy.lastStartedAt,
		LastFinishedAt:      policy.lastFinishedAt,
		LastSuccessAt:       policy.lastSuccessAt,
		NextRunAt:           policy.nextRun,
		BackupAge:           backupAge,
		PublicationAge:      productionPublicationAge(policy, now),
		RestoreAge:          restoreAge,
		PublicationRequired: policy.config.public.Publisher != "",
		LastError:           lastError,
		LastBackup:          cloneProductionBackupEvidence(policy.lastBackup),
		LastPublication:     cloneProductionPublicationEvidence(policy.lastPublication),
		LastRestore:         cloneProductionRestoreEvidence(policy.lastRestore),
		LastCycle:           cloneProductionPolicyCycle(policy.lastCycle),
	}
}

func productionReadinessLocked(
	policy *productionPolicy,
	now time.Time,
) (ProductionReadiness, time.Duration, time.Duration) {
	if policy.lastBackup == nil || policy.lastRestore == nil {
		return ProductionUnsafe, 0, 0
	}
	backupAge := now.Sub(policy.lastBackup.CreatedAt)
	restoreAge := now.Sub(policy.lastRestore.VerifiedAt)
	if backupAge < 0 || restoreAge < 0 ||
		backupAge > policy.config.public.MaxBackupAge ||
		restoreAge > policy.config.public.MaxRestoreAge ||
		policy.lastUnsafe {
		return ProductionUnsafe, backupAge, restoreAge
	}
	if policy.config.public.Publisher != "" {
		if policy.lastPublication == nil ||
			!productionPublicationMatchesBackup(
				policy.lastPublication,
				policy.lastBackup,
				policy.config.public.Publisher,
			) ||
			policy.lastPublication.AnchorCreatedAt.After(now) ||
			policy.lastPublication.VerifiedAt.After(now) {
			return ProductionUnsafe, backupAge, restoreAge
		}
	}
	if policy.paused || policy.lastErr != nil || policy.consecutiveFailures != 0 {
		return ProductionDegraded, backupAge, restoreAge
	}
	return ProductionReady, backupAge, restoreAge
}

func productionPublicationAge(policy *productionPolicy, now time.Time) time.Duration {
	if policy == nil || policy.lastPublication == nil {
		return 0
	}
	return now.Sub(policy.lastPublication.AnchorCreatedAt)
}

func productionPublicationMatchesBackup(
	publication *ProductionPublicationEvidence,
	backup *ProductionBackupEvidence,
	publisher string,
) bool {
	return publication != nil && backup != nil &&
		publication.Publisher == publisher &&
		publication.DatabaseID == backup.DatabaseID &&
		publication.Transaction == backup.Transaction &&
		publication.Records == backup.Records &&
		publication.Bytes == backup.Bytes &&
		publication.SHA256 == backup.SHA256 &&
		publication.AnchorCreatedAt.Equal(backup.CreatedAt)
}

func productionErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrProductionUnsafe):
		return ErrProductionUnsafe.Error()
	case errors.Is(err, ErrProductionStoreLimit):
		return ErrProductionStoreLimit.Error()
	case errors.Is(err, ErrProductionPublisherLimit):
		return ErrProductionPublisherLimit.Error()
	case errors.Is(err, ErrProductionPublisherNotFound):
		return ErrProductionPublisherNotFound.Error()
	case errors.Is(err, ErrProductionPublication):
		return ErrProductionPublication.Error()
	case errors.Is(err, ErrBackupHistoryRequired):
		return ErrBackupHistoryRequired.Error()
	case errors.Is(err, ErrBackupSourceMismatch):
		return ErrBackupSourceMismatch.Error()
	case errors.Is(err, ErrBackupVerification):
		return ErrBackupVerification.Error()
	case errors.Is(err, ErrMaintenanceQueueFull):
		return ErrMaintenanceQueueFull.Error()
	case errors.Is(err, ErrMaintenanceDatabaseQueueFull):
		return ErrMaintenanceDatabaseQueueFull.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded.Error()
	case errors.Is(err, context.Canceled):
		return context.Canceled.Error()
	case errors.Is(err, ErrClosed):
		return ErrClosed.Error()
	default:
		return "kitdb node: production policy cycle failed"
	}
}

func (manager *Manager) executeProductionPolicyCycle(
	ctx context.Context,
	config ProductionPolicyConfig,
	publisher ProductionAnchorPublisher,
	forceBackup bool,
	restoreDue bool,
) (cycle ProductionPolicyCycle, err error) {
	cycle.Name = config.Name
	cycle.StartedAt = time.Now()
	defer func() {
		cycle.FinishedAt = time.Now()
		cycle.Duration = cycle.FinishedAt.Sub(cycle.StartedAt)
	}()
	if err := os.MkdirAll(config.BackupDirectory, 0o700); err != nil {
		return cycle, fmt.Errorf("kitdb node: create production backup directory: %w", err)
	}
	records, err := discoverProductionBackups(
		ctx, config.BackupDirectory, manager.limits.production.maxStoreEntries,
	)
	if err != nil {
		return cycle, err
	}
	if len(records) > 1 {
		databaseID := records[0].anchor.DatabaseID
		for _, record := range records[1:] {
			if record.anchor.DatabaseID != databaseID {
				return cycle, errors.Join(
					ErrProductionUnsafe,
					fmt.Errorf("kitdb node: production backup store contains mixed database identities"),
				)
			}
		}
	}
	now := time.Now()
	var selected *productionBackupRecord
	if !forceBackup && len(records) != 0 {
		latest := records[len(records)-1]
		age := now.Sub(latest.createdAt)
		if age >= 0 && age < config.BackupInterval {
			selected = &latest
		}
	}
	destination := ""
	createdAt := now.UTC()
	if selected != nil {
		destination = selected.anchor.Path
		createdAt = selected.createdAt
	} else {
		destination, err = nextProductionBackupPath(config.BackupDirectory, createdAt)
		if err != nil {
			return cycle, err
		}
	}
	ticket, err := manager.ScheduleBackup(ctx, BackupRequest{
		Source: config.Source, Destination: destination,
		Options: config.SourceOptions, PinName: config.PinName,
		Priority: config.Priority,
	})
	if err != nil {
		return cycle, err
	}
	completed, err := ticket.Wait(ctx)
	if completed.Backup != nil && !errors.Is(err, ErrBackupSourceMismatch) &&
		!errors.Is(err, ErrBackupVerification) {
		cycle.Backup = productionBackupEvidence(
			completed.Backup.Anchor, createdAt, time.Now(), completed.Backup.Resumed,
		)
	}
	if err != nil {
		if errors.Is(err, ErrBackupSourceMismatch) || errors.Is(err, ErrBackupVerification) {
			return cycle, errors.Join(ErrProductionUnsafe, err)
		}
		return cycle, err
	}
	if completed.Backup == nil || cycle.Backup == nil {
		return cycle, fmt.Errorf("kitdb node: production backup returned no evidence")
	}
	if selected != nil && completed.Backup.Anchor != selected.anchor {
		return cycle, errors.Join(
			ErrProductionUnsafe,
			fmt.Errorf("kitdb node: resumed production anchor changed during verification"),
		)
	}
	for _, record := range records {
		if record.anchor.DatabaseID != completed.Backup.Anchor.DatabaseID {
			return cycle, errors.Join(
				ErrProductionUnsafe,
				fmt.Errorf("kitdb node: production backup store contains mixed database identities"),
			)
		}
	}
	if config.Publisher != "" {
		cycle.publicationAttempted = true
		if publisher == nil {
			cycle.publicationFailed = true
			return cycle, ErrProductionPublisherNotFound
		}
		receipt, publishErr := publisher.Publish(ctx, completed.Backup.Anchor)
		if productionReceiptHasEvidence(receipt) {
			if receipt.PrunedAnchors < 0 ||
				!productionReceiptMatchesAnchor(receipt, completed.Backup.Anchor) {
				cycle.publicationFailed = true
				return cycle, errors.Join(
					ErrProductionUnsafe,
					fmt.Errorf("kitdb node: publisher returned mismatched anchor evidence"),
					publishErr,
				)
			}
			cycle.Publication = productionPublicationEvidence(
				config.Publisher, receipt, createdAt, time.Now(),
			)
		}
		if publishErr != nil {
			cycle.publicationFailed = true
			return cycle, errors.Join(ErrProductionPublication, publishErr)
		}
		if cycle.Publication == nil {
			cycle.publicationFailed = true
			return cycle, errors.Join(
				ErrProductionUnsafe,
				fmt.Errorf("kitdb node: publisher returned no verified anchor evidence"),
			)
		}
	}
	if restoreDue {
		restored, restoreErr := runProductionRestoreDrill(
			ctx, config.BackupDirectory, completed.Backup.Anchor,
		)
		cycle.Restore = restored
		if restoreErr != nil {
			return cycle, restoreErr
		}
	}
	pruned, err := pruneProductionBackups(
		ctx,
		config.BackupDirectory,
		completed.Backup.Anchor.DatabaseID,
		config.KeepBackups,
		manager.limits.production.maxStoreEntries,
	)
	cycle.PrunedBackups = pruned
	return cycle, err
}

func productionReceiptHasEvidence(receipt ProductionAnchorReceipt) bool {
	return receipt.DatabaseID != "" || receipt.SHA256 != "" ||
		receipt.Bytes != 0 || receipt.Records != 0 || receipt.Transaction != 0
}

func productionPublicationEvidence(
	publisher string,
	receipt ProductionAnchorReceipt,
	anchorCreated time.Time,
	verifiedAt time.Time,
) *ProductionPublicationEvidence {
	return &ProductionPublicationEvidence{
		Publisher: publisher, DatabaseID: receipt.DatabaseID,
		Transaction: receipt.Transaction, Records: receipt.Records,
		Bytes: receipt.Bytes, SHA256: receipt.SHA256,
		AnchorCreatedAt: anchorCreated.UTC(), VerifiedAt: verifiedAt.UTC(),
		Resumed: receipt.AlreadyPublished, PrunedAnchors: receipt.PrunedAnchors,
	}
}

func productionBackupEvidence(
	anchor kitdb.BackupAnchor,
	createdAt time.Time,
	verifiedAt time.Time,
	resumed bool,
) *ProductionBackupEvidence {
	return &ProductionBackupEvidence{
		DatabaseID: anchor.DatabaseID, Transaction: anchor.Transaction,
		Records: anchor.Records, Bytes: anchor.Bytes, SHA256: anchor.SHA256,
		CreatedAt: createdAt.UTC(), VerifiedAt: verifiedAt.UTC(), Resumed: resumed,
	}
}

func runProductionRestoreDrill(
	ctx context.Context,
	backupDirectory string,
	anchor kitdb.BackupAnchor,
) (result *ProductionRestoreEvidence, err error) {
	startedAt := time.Now()
	workspace, err := os.MkdirTemp(backupDirectory, ".kitdb-restore-drill-*")
	if err != nil {
		return nil, fmt.Errorf("kitdb node: create restore drill workspace: %w", err)
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(workspace))
	}()
	sourceDigest, err := kitdb.LogicalDigestBackupAnchor(ctx, anchor.Path)
	if err != nil {
		return nil, errors.Join(ErrProductionUnsafe, err)
	}
	restoredPath := filepath.Join(workspace, "restored.kitdb")
	restored, err := kitdb.RestoreToTransaction(
		ctx, anchor.Path, "", restoredPath, anchor.Transaction,
	)
	if err != nil {
		return nil, errors.Join(ErrProductionUnsafe, err)
	}
	restoredDigest, err := kitdb.LogicalDigestBackupAnchor(ctx, restoredPath)
	if err != nil {
		return nil, errors.Join(ErrProductionUnsafe, err)
	}
	if restored.DatabaseID != anchor.DatabaseID ||
		restored.Transaction != anchor.Transaction ||
		sourceDigest != restoredDigest {
		return nil, errors.Join(
			ErrProductionUnsafe,
			fmt.Errorf("kitdb node: restored logical state does not match its production anchor"),
		)
	}
	return &ProductionRestoreEvidence{
		DatabaseID:  sourceDigest.DatabaseID,
		Transaction: sourceDigest.Transaction,
		Records:     sourceDigest.Records, LogicalBytes: sourceDigest.Bytes,
		LogicalSHA256: sourceDigest.SHA256,
		VerifiedAt:    time.Now().UTC(), Duration: time.Since(startedAt),
	}, nil
}

func discoverProductionBackups(
	ctx context.Context,
	directory string,
	maxEntries int,
) ([]productionBackupRecord, error) {
	opened, err := os.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("kitdb node: open production backup directory: %w", err)
	}
	entries := make([]os.DirEntry, 0, min(maxEntries+1, 256))
	var readErr error
	for len(entries) <= maxEntries {
		batchSize := min(256, maxEntries+1-len(entries))
		var batch []os.DirEntry
		batch, readErr = opened.ReadDir(batchSize)
		entries = append(entries, batch...)
		if errors.Is(readErr, io.EOF) {
			readErr = nil
			break
		}
		if readErr != nil || len(batch) == 0 {
			break
		}
	}
	closeErr := opened.Close()
	if readErr != nil {
		return nil, errors.Join(
			fmt.Errorf("kitdb node: list production backup directory: %w", readErr),
			closeErr,
		)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > maxEntries {
		return nil, ErrProductionStoreLimit
	}
	records := make([]productionBackupRecord, 0, min(len(entries), maxEntries))
	for _, entry := range entries {
		createdAt, matches, err := parseProductionBackupName(entry.Name())
		if err != nil {
			return nil, errors.Join(ErrProductionUnsafe, err)
		}
		if !matches {
			continue
		}
		if createdAt.After(time.Now().UTC()) {
			return nil, errors.Join(
				ErrProductionUnsafe,
				fmt.Errorf("kitdb node: production backup timestamp is in the future"),
			)
		}
		if entry.IsDir() {
			return nil, errors.Join(
				ErrProductionUnsafe,
				fmt.Errorf("kitdb node: production anchor name refers to a directory"),
			)
		}
		path := filepath.Join(directory, entry.Name())
		anchor, err := kitdb.VerifyBackupAnchor(ctx, path)
		if err != nil {
			return nil, errors.Join(ErrProductionUnsafe, err)
		}
		records = append(records, productionBackupRecord{
			anchor: anchor, createdAt: createdAt,
		})
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].createdAt.Equal(records[right].createdAt) {
			if records[left].anchor.Transaction == records[right].anchor.Transaction {
				return records[left].anchor.Path < records[right].anchor.Path
			}
			return records[left].anchor.Transaction < records[right].anchor.Transaction
		}
		return records[left].createdAt.Before(records[right].createdAt)
	})
	return records, nil
}

func nextProductionBackupPath(directory string, createdAt time.Time) (string, error) {
	random := make([]byte, productionBackupRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("kitdb node: generate production backup identity: %w", err)
	}
	name := fmt.Sprintf(
		"%s%020d-%s%s",
		productionBackupPrefix,
		createdAt.UTC().UnixNano(),
		hex.EncodeToString(random),
		productionBackupSuffix,
	)
	return filepath.Join(directory, name), nil
}

func parseProductionBackupName(name string) (time.Time, bool, error) {
	if !strings.HasPrefix(name, productionBackupPrefix) ||
		!strings.HasSuffix(name, productionBackupSuffix) {
		return time.Time{}, false, nil
	}
	body := strings.TrimSuffix(strings.TrimPrefix(name, productionBackupPrefix), productionBackupSuffix)
	parts := strings.Split(body, "-")
	if len(parts) != 2 || len(parts[1]) != productionBackupRandomBytes*2 {
		return time.Time{}, true, fmt.Errorf("kitdb node: malformed production backup name %q", name)
	}
	nanoseconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || nanoseconds <= 0 {
		return time.Time{}, true, fmt.Errorf("kitdb node: malformed production backup time in %q", name)
	}
	random, err := hex.DecodeString(parts[1])
	if err != nil || len(random) != productionBackupRandomBytes {
		return time.Time{}, true, fmt.Errorf("kitdb node: malformed production backup identity in %q", name)
	}
	return time.Unix(0, nanoseconds).UTC(), true, nil
}

func pruneProductionBackups(
	ctx context.Context,
	directory string,
	databaseID string,
	keep int,
	maxEntries int,
) (int, error) {
	records, err := discoverProductionBackups(ctx, directory, maxEntries)
	if err != nil {
		return 0, err
	}
	for _, record := range records {
		if record.anchor.DatabaseID != databaseID {
			return 0, errors.Join(
				ErrProductionUnsafe,
				fmt.Errorf("kitdb node: production backup store contains mixed database identities"),
			)
		}
	}
	if len(records) <= keep {
		return 0, nil
	}
	removed := 0
	for _, record := range records[:len(records)-keep] {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if err := os.Remove(record.anchor.Path); err != nil {
			return removed, fmt.Errorf("kitdb node: prune production backup: %w", err)
		}
		removed++
	}
	return removed, nil
}

func cloneProductionPolicyCycle(source ProductionPolicyCycle) ProductionPolicyCycle {
	cloned := source
	cloned.Backup = cloneProductionBackupEvidence(source.Backup)
	cloned.Publication = cloneProductionPublicationEvidence(source.Publication)
	cloned.Restore = cloneProductionRestoreEvidence(source.Restore)
	return cloned
}

func cloneProductionPublicationEvidence(
	source *ProductionPublicationEvidence,
) *ProductionPublicationEvidence {
	if source == nil {
		return nil
	}
	cloned := *source
	return &cloned
}

func cloneProductionBackupEvidence(
	source *ProductionBackupEvidence,
) *ProductionBackupEvidence {
	if source == nil {
		return nil
	}
	cloned := *source
	return &cloned
}

func cloneProductionRestoreEvidence(
	source *ProductionRestoreEvidence,
) *ProductionRestoreEvidence {
	if source == nil {
		return nil
	}
	cloned := *source
	return &cloned
}
