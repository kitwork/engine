package node

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/kitdb"
)

const (
	defaultMaxOpenDatabases             = 64
	defaultMaxPageCacheBytes      int64 = 256 << 20
	defaultPageCacheBytes         int64 = 1 << 20
	maximumOpenDatabases                = 1_000_000
	maximumConcurrentOpens              = 1_024
	maximumPageCacheBytes         int64 = 1 << 50
	defaultMaxQueuedMaintenance         = 1_024
	defaultMaintenancePerDatabase       = 2
	defaultMaintenanceTimeout           = 10 * time.Minute
	maximumConcurrentMaintenance        = 128
	maximumQueuedMaintenance            = 1_000_000
	maximumMaintenancePerDatabase       = 128
	maximumMaintenanceTimeout           = 24 * time.Hour
)

var (
	ErrClosed             = errors.New("kitdb node: manager is closed")
	ErrDatabaseBusy       = errors.New("kitdb node: database is busy")
	ErrDatabaseNotManaged = errors.New("kitdb node: database is not managed")
	ErrOptionsMismatch    = errors.New("kitdb node: open options do not match the active lease")
	ErrPolicyMismatch     = errors.New("kitdb node: database policy does not match the managed handle")
	ErrPageCacheBudget    = errors.New("kitdb node: database page cache exceeds the fleet budget")
	ErrWarmCapacity       = errors.New("kitdb node: warm databases exhaust fleet capacity")
)

// DatabasePolicy controls process-local fleet ownership for one managed
// database. It is deliberately separate from kitdb.OpenOptions: residency is
// a property of the host running a fleet, not part of the database file or its
// transaction/recovery contract.
type DatabasePolicy struct {
	// Warm protects an idle handle from capacity eviction and TrimIdle. It keeps
	// the handle, catalog, and bounded caches available; it does not read the
	// complete database into memory. Manager.Close and explicit database
	// removal still close warm handles.
	Warm bool
}

// Limits bound resources reserved by one Manager. Zero values select bounded
// defaults. A negative DefaultPageCacheBytes disables caching unless an
// Acquire call explicitly requests a positive PageCacheBytes value.
type Limits struct {
	MaxOpenDatabases      int
	MaxPageCacheBytes     int64
	DefaultPageCacheBytes int64
	MaxConcurrentOpens    int

	MaxConcurrentMaintenance  int
	MaxQueuedMaintenance      int
	MaxMaintenancePerDatabase int
	MaintenanceTimeout        time.Duration

	ReplicaFileLinks ReplicaFileLinkLimits
	Production       ProductionSupervisorLimits
}

type normalizedLimits struct {
	maxOpenDatabases      int
	maxPageCacheBytes     int64
	defaultPageCacheBytes int64
	maxConcurrentOpens    int

	maxConcurrentMaintenance  int
	maxQueuedMaintenance      int
	maxMaintenancePerDatabase int
	maintenanceTimeout        time.Duration

	replicaFileLinks normalizedReplicaFileLinkLimits
	production       normalizedProductionSupervisorLimits
}

// Stats is a process-local, point-in-time view of fleet ownership. Reserved
// page-cache bytes are ceilings passed to open handles, not current cache use.
type Stats struct {
	Closed                     bool
	ManagedDatabases           int
	OpenDatabases              int
	OpeningDatabases           int
	ActiveDatabases            int
	IdleDatabases              int
	WarmDatabases              int
	WarmIdleDatabases          int
	ClosingDatabases           int
	ActiveLeases               int
	WaitingAcquisitions        int
	ReservedPageCacheBytes     int64
	WarmReservedPageCacheBytes int64
	MaxOpenDatabases           int
	MaxPageCacheBytes          int64
	DefaultPageCacheBytes      int64
	MaxConcurrentOpens         int
	Acquisitions               uint64
	Reuses                     uint64
	Opens                      uint64
	OpenFailures               uint64
	Evictions                  uint64
	Closes                     uint64
	Waits                      uint64
	LastCloseError             string

	QueuedMaintenance                 int
	RunningMaintenance                int
	MultiDatabaseMaintenanceRunning   bool
	MaxConcurrentMaintenance          int
	MaxQueuedMaintenance              int
	MaxMaintenancePerDatabase         int
	MaintenanceTimeout                time.Duration
	MaintenanceSubmissions            uint64
	MaintenanceCoalesced              uint64
	MaintenanceRejections             uint64
	MaintenanceCompletions            uint64
	MaintenanceFailures               uint64
	MaintenanceCancellations          uint64
	CheckpointCompletions             uint64
	VerificationCompletions           uint64
	BackupCompletions                 uint64
	BackupResumptions                 uint64
	HistoryPruneCompletions           uint64
	HistoryRetentionCompletions       uint64
	HistoryRetentionEvaluations       uint64
	HistoryRetentionLimitedByPins     uint64
	HistoryRetentionBytesOverLimit    int64
	HistoryPrunedSegments             uint64
	HistoryPrunedBytes                int64
	ReplicaCatchUpCompletions         uint64
	ReplicaTransactionsApplied        uint64
	ReplicaCatchUpNoops               uint64
	ReplicaFilePublishCompletions     uint64
	ReplicaFileApplyCompletions       uint64
	ReplicaFileAcknowledgeCompletions uint64
	RowMigrationCompletions           uint64
	RowMigrationChunks                uint64
	RowMigrationAdvancedChunks        uint64
	SecondaryIndexCompletions         uint64
	SecondaryIndexChunks              uint64
	SecondaryIndexAdvancedChunks      uint64
	ReplicaFileBatchesPublished       uint64
	ReplicaFileTransactionsPublished  uint64
	ReplicaFileTransactionsApplied    uint64
	ReplicaFileAcknowledgements       uint64
	ReplicaFileNoops                  uint64
	ReplicaFilePeakPendingBatches     uint32
	ReplicaFilePeakPendingBytes       uint64
	ReplicaFileMaxLagTransactions     uint64
	ReplicaFileLinks                  int
	PausedReplicaFileLinks            int
	QueuedReplicaFileLinks            int
	RunningReplicaFileLinks           int
	MaxReplicaFileLinks               int
	MaxConcurrentReplicaFileLinks     int
	ReplicaFileLinkActiveInterval     time.Duration
	ReplicaFileLinkIdleInterval       time.Duration
	ReplicaFileLinkInitialBackoff     time.Duration
	ReplicaFileLinkMaxBackoff         time.Duration
	ReplicaFileLinkCycles             uint64
	ReplicaFileLinkSuccesses          uint64
	ReplicaFileLinkFailures           uint64
	ReplicaFileLinkCancellations      uint64
	ReplicaFileLinkBackoffs           uint64
	ProductionPolicies                int
	PausedProductionPolicies          int
	ReadyProductionPolicies           int
	DegradedProductionPolicies        int
	UnsafeProductionPolicies          int
	QueuedProductionPolicies          int
	RunningProductionPolicies         int
	MaxProductionPolicies             int
	MaxConcurrentProductionPolicies   int
	ProductionPolicyAttempts          uint64
	ProductionPolicySuccesses         uint64
	ProductionPolicyFailures          uint64
	ProductionPolicyCancellations     uint64
	ProductionPolicyBackoffs          uint64
	ProductionBackupsCreated          uint64
	ProductionBackupsResumed          uint64
	ProductionPublishers              int
	ProductionPublicationAttempts     uint64
	ProductionPublicationFailures     uint64
	ProductionAnchorsPublished        uint64
	ProductionAnchorsResumed          uint64
	ProductionPublishedBytes          uint64
	ProductionPublishedAnchorsPruned  uint64
	ProductionRestoreDrills           uint64
	ProductionBackupsPruned           uint64
	MaintenanceTotalDuration          time.Duration
	MaintenanceLongestDuration        time.Duration
}

type entryState uint8

const (
	entryOpening entryState = iota
	entryOpen
	entryClosing
)

type databaseEntry struct {
	key        string
	path       string
	options    kitdb.OpenOptions
	policy     DatabasePolicy
	cacheBytes int64
	gate       DatabaseGate
	state      entryState
	ready      chan struct{}
	database   *kitdb.DB
	leases     int
	idle       *list.Element
	err        error
	closeErr   error
}

// DatabaseGate serializes host-level relational operations that span more
// than one kernel call. It belongs to the managed database entry so every app
// runtime, foreground request, and maintenance worker leasing the same file
// observes one gate.
//
// The KitDB kernel still owns transaction serialization. This gate closes the
// higher-level validation-to-commit gap for schema-aware operations.
type DatabaseGate struct {
	mu sync.RWMutex
}

func (gate *DatabaseGate) Lock()    { gate.mu.Lock() }
func (gate *DatabaseGate) Unlock()  { gate.mu.Unlock() }
func (gate *DatabaseGate) RLock()   { gate.mu.RLock() }
func (gate *DatabaseGate) RUnlock() { gate.mu.RUnlock() }

// Manager owns shared KitDB handles and their fleet-level resource bounds.
// It is safe for concurrent use.
type Manager struct {
	limits normalizedLimits

	ctx    context.Context
	cancel context.CancelFunc

	mu                     sync.Mutex
	databases              map[string]*databaseEntry
	retiring               map[string]struct{}
	idle                   list.List
	notify                 chan struct{}
	closed                 bool
	reservedPageCacheBytes int64
	activeLeases           int
	waitingAcquisitions    int
	acquisitions           uint64
	reuses                 uint64
	opens                  uint64
	openFailures           uint64
	evictions              uint64
	closes                 uint64
	waits                  uint64
	lastCloseError         string
	closeErr               error

	openSlots chan struct{}

	maintenanceMu                     sync.Mutex
	maintenanceCond                   *sync.Cond
	maintenanceQueues                 maintenanceQueueSet
	maintenanceTasks                  map[maintenanceTaskID]*maintenanceTask
	maintenancePerDatabase            map[string]int
	maintenanceDatabaseOptions        map[string]kitdb.OpenOptions
	maintenanceRunningPaths           map[string]bool
	maintenanceMultiRunning           bool
	maintenanceForegroundBurst        int
	maintenanceUrgentBurst            int
	maintenanceQueued                 int
	maintenanceRunning                int
	maintenanceStarted                bool
	maintenanceClosed                 bool
	maintenanceDone                   chan struct{}
	maintenanceWorkers                sync.WaitGroup
	maintenanceExecutor               maintenanceExecutor
	rowMigrationDriver                RowMigrationDriver
	rowMigrationDriverID              string
	secondaryIndexDriver              SecondaryIndexDriver
	secondaryIndexDriverID            string
	maintenanceSubmissions            uint64
	maintenanceCoalesced              uint64
	maintenanceRejections             uint64
	maintenanceCompletions            uint64
	maintenanceFailures               uint64
	maintenanceCancellations          uint64
	checkpointCompletions             uint64
	verificationCompletions           uint64
	backupCompletions                 uint64
	backupResumptions                 uint64
	historyPruneCompletions           uint64
	historyRetentionCompletions       uint64
	historyRetentionEvaluations       uint64
	historyRetentionLimitedByPins     uint64
	historyRetentionBytesOverLimit    int64
	historyPrunedSegments             uint64
	historyPrunedBytes                int64
	replicaCatchUpCompletions         uint64
	replicaTransactionsApplied        uint64
	replicaCatchUpNoops               uint64
	replicaFilePublishCompletions     uint64
	replicaFileApplyCompletions       uint64
	replicaFileAcknowledgeCompletions uint64
	replicaFileBatchesPublished       uint64
	replicaFileTransactionsPublished  uint64
	replicaFileTransactionsApplied    uint64
	replicaFileAcknowledgements       uint64
	replicaFileNoops                  uint64
	replicaFilePeakPendingBatches     uint32
	replicaFilePeakPendingBytes       uint64
	replicaFileMaxLagTransactions     uint64
	rowMigrationCompletions           uint64
	rowMigrationChunks                uint64
	rowMigrationAdvancedChunks        uint64
	secondaryIndexCompletions         uint64
	secondaryIndexChunks              uint64
	secondaryIndexAdvancedChunks      uint64
	maintenanceTotalDuration          time.Duration
	maintenanceLongestDuration        time.Duration

	replicaFileLinkMu            sync.Mutex
	replicaFileLinks             map[string]*replicaFileLink
	replicaFileLinkTargets       map[string]string
	replicaFileLinkMailboxes     map[string]string
	replicaFileLinkPins          map[string]string
	replicaFileLinkDatabases     map[string]int
	replicaFileLinkNotify        chan struct{}
	replicaFileLinkWork          chan replicaFileLinkWork
	replicaFileLinkStarted       bool
	replicaFileLinkClosed        bool
	replicaFileLinkDone          chan struct{}
	replicaFileLinkWorkers       sync.WaitGroup
	replicaFileLinkQueued        int
	replicaFileLinkRunning       int
	replicaFileLinkCycles        uint64
	replicaFileLinkSuccesses     uint64
	replicaFileLinkFailures      uint64
	replicaFileLinkCancellations uint64
	replicaFileLinkBackoffs      uint64

	productionMu                     sync.Mutex
	productionPolicies               map[string]*productionPolicy
	productionSources                map[string]string
	productionStores                 map[string]string
	productionPublishers             map[string]ProductionAnchorPublisher
	productionPublisherOwners        map[string]string
	productionNotify                 chan struct{}
	productionWork                   chan productionPolicyWork
	productionDone                   chan struct{}
	productionStarted                bool
	productionClosed                 bool
	productionWorkers                sync.WaitGroup
	productionQueued                 int
	productionRunning                int
	productionAttempts               uint64
	productionSuccesses              uint64
	productionFailures               uint64
	productionCancellations          uint64
	productionBackoffs               uint64
	productionBackups                uint64
	productionResumptions            uint64
	productionPublicationAttempts    uint64
	productionPublicationFailures    uint64
	productionAnchorsPublished       uint64
	productionAnchorsResumed         uint64
	productionPublishedBytes         uint64
	productionPublishedAnchorsPruned uint64
	productionRestoreDrills          uint64
	productionPruned                 uint64
}

// Lease pins one managed handle. Callers must keep the lease until every
// transaction, snapshot, cursor, and direct DB operation derived from it has
// finished. Release is idempotent.
type Lease struct {
	manager  *Manager
	entry    *databaseEntry
	database *kitdb.DB
	path     string

	once       sync.Once
	released   atomic.Bool
	releaseErr error
}

// NewManager creates an empty fleet owner without opening a database.
func NewManager(limits Limits) (*Manager, error) {
	normalized, err := normalizeLimits(limits)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		limits:                     normalized,
		ctx:                        ctx,
		cancel:                     cancel,
		databases:                  make(map[string]*databaseEntry),
		retiring:                   make(map[string]struct{}),
		notify:                     make(chan struct{}),
		openSlots:                  make(chan struct{}, normalized.maxConcurrentOpens),
		maintenanceTasks:           make(map[maintenanceTaskID]*maintenanceTask),
		maintenancePerDatabase:     make(map[string]int),
		maintenanceDatabaseOptions: make(map[string]kitdb.OpenOptions),
		maintenanceRunningPaths:    make(map[string]bool),
		maintenanceDone:            make(chan struct{}),
		maintenanceExecutor:        runMaintenanceOperation,
		replicaFileLinks:           make(map[string]*replicaFileLink),
		replicaFileLinkTargets:     make(map[string]string),
		replicaFileLinkMailboxes:   make(map[string]string),
		replicaFileLinkPins:        make(map[string]string),
		replicaFileLinkDatabases:   make(map[string]int),
		replicaFileLinkNotify:      make(chan struct{}, 1),
		replicaFileLinkWork: make(
			chan replicaFileLinkWork, normalized.replicaFileLinks.maxConcurrent,
		),
		replicaFileLinkDone:       make(chan struct{}),
		productionPolicies:        make(map[string]*productionPolicy),
		productionSources:         make(map[string]string),
		productionStores:          make(map[string]string),
		productionPublishers:      make(map[string]ProductionAnchorPublisher),
		productionPublisherOwners: make(map[string]string),
		productionNotify:          make(chan struct{}, 1),
		productionWork: make(
			chan productionPolicyWork, normalized.production.maxConcurrent,
		),
		productionDone: make(chan struct{}),
	}
	manager.maintenanceCond = sync.NewCond(&manager.maintenanceMu)
	return manager, nil
}

func normalizeLimits(limits Limits) (normalizedLimits, error) {
	if limits.MaxOpenDatabases == 0 {
		limits.MaxOpenDatabases = defaultMaxOpenDatabases
	}
	if limits.MaxPageCacheBytes == 0 {
		limits.MaxPageCacheBytes = defaultMaxPageCacheBytes
	}
	if limits.DefaultPageCacheBytes == 0 {
		limits.DefaultPageCacheBytes = defaultPageCacheBytes
	} else if limits.DefaultPageCacheBytes < 0 {
		limits.DefaultPageCacheBytes = 0
	}
	if limits.MaxConcurrentOpens == 0 {
		limits.MaxConcurrentOpens = min(
			limits.MaxOpenDatabases,
			min(4, max(1, runtime.GOMAXPROCS(0))),
		)
	}
	if limits.MaxConcurrentMaintenance == 0 {
		limits.MaxConcurrentMaintenance = min(
			limits.MaxOpenDatabases,
			min(2, max(1, runtime.GOMAXPROCS(0))),
		)
	}
	if limits.MaxQueuedMaintenance == 0 {
		limits.MaxQueuedMaintenance = defaultMaxQueuedMaintenance
	}
	if limits.MaxMaintenancePerDatabase == 0 {
		limits.MaxMaintenancePerDatabase = defaultMaintenancePerDatabase
	}
	if limits.MaintenanceTimeout == 0 {
		limits.MaintenanceTimeout = defaultMaintenanceTimeout
	}
	if limits.MaxOpenDatabases < 1 || limits.MaxOpenDatabases > maximumOpenDatabases {
		return normalizedLimits{}, fmt.Errorf("kitdb node: MaxOpenDatabases must be between 1 and %d", maximumOpenDatabases)
	}
	if limits.MaxPageCacheBytes < 1 || limits.MaxPageCacheBytes > maximumPageCacheBytes {
		return normalizedLimits{}, fmt.Errorf("kitdb node: MaxPageCacheBytes must be between 1 and %d", maximumPageCacheBytes)
	}
	if limits.DefaultPageCacheBytes > limits.MaxPageCacheBytes {
		return normalizedLimits{}, fmt.Errorf("kitdb node: DefaultPageCacheBytes exceeds MaxPageCacheBytes")
	}
	if limits.MaxConcurrentOpens < 1 || limits.MaxConcurrentOpens > maximumConcurrentOpens ||
		limits.MaxConcurrentOpens > limits.MaxOpenDatabases {
		return normalizedLimits{}, fmt.Errorf("kitdb node: MaxConcurrentOpens must be between 1 and MaxOpenDatabases")
	}
	if limits.MaxConcurrentMaintenance < 1 || limits.MaxConcurrentMaintenance > maximumConcurrentMaintenance ||
		limits.MaxConcurrentMaintenance > limits.MaxOpenDatabases {
		return normalizedLimits{}, fmt.Errorf("kitdb node: MaxConcurrentMaintenance must be between 1 and MaxOpenDatabases")
	}
	if limits.MaxQueuedMaintenance < 1 || limits.MaxQueuedMaintenance > maximumQueuedMaintenance {
		return normalizedLimits{}, fmt.Errorf("kitdb node: MaxQueuedMaintenance must be between 1 and %d", maximumQueuedMaintenance)
	}
	if limits.MaxMaintenancePerDatabase < 1 || limits.MaxMaintenancePerDatabase > maximumMaintenancePerDatabase ||
		limits.MaxMaintenancePerDatabase > limits.MaxQueuedMaintenance {
		return normalizedLimits{}, fmt.Errorf("kitdb node: MaxMaintenancePerDatabase must be between 1 and MaxQueuedMaintenance")
	}
	if limits.MaintenanceTimeout < time.Millisecond || limits.MaintenanceTimeout > maximumMaintenanceTimeout {
		return normalizedLimits{}, fmt.Errorf("kitdb node: MaintenanceTimeout must be between 1ms and %s", maximumMaintenanceTimeout)
	}
	replicaFileLinks, err := normalizeReplicaFileLinkLimits(
		limits.ReplicaFileLinks, limits.MaxConcurrentMaintenance,
	)
	if err != nil {
		return normalizedLimits{}, err
	}
	production, err := normalizeProductionSupervisorLimits(
		limits.Production, limits.MaxConcurrentMaintenance,
	)
	if err != nil {
		return normalizedLimits{}, err
	}
	return normalizedLimits{
		maxOpenDatabases:          limits.MaxOpenDatabases,
		maxPageCacheBytes:         limits.MaxPageCacheBytes,
		defaultPageCacheBytes:     limits.DefaultPageCacheBytes,
		maxConcurrentOpens:        limits.MaxConcurrentOpens,
		maxConcurrentMaintenance:  limits.MaxConcurrentMaintenance,
		maxQueuedMaintenance:      limits.MaxQueuedMaintenance,
		maxMaintenancePerDatabase: limits.MaxMaintenancePerDatabase,
		maintenanceTimeout:        limits.MaintenanceTimeout,
		replicaFileLinks:          replicaFileLinks,
		production:                production,
	}, nil
}

// Acquire opens or reuses path with its existing/default database policy and
// returns a lease that pins the handle. When every evictable capacity candidate
// is in use, Acquire waits until a lease is released or ctx is canceled.
func (manager *Manager) Acquire(
	ctx context.Context,
	path string,
	options kitdb.OpenOptions,
) (*Lease, error) {
	return manager.acquire(ctx, path, options, nil)
}

// AcquireWithPolicy opens or reuses path under an explicit process-local
// database policy. An already managed handle must have the same explicit
// policy; use SetDatabasePolicy for a deliberate live policy change.
func (manager *Manager) AcquireWithPolicy(
	ctx context.Context,
	path string,
	options kitdb.OpenOptions,
	policy DatabasePolicy,
) (*Lease, error) {
	return manager.acquire(ctx, path, options, &policy)
}

func (manager *Manager) acquire(
	ctx context.Context,
	path string,
	options kitdb.OpenOptions,
	requestedPolicy *DatabasePolicy,
) (*Lease, error) {
	if manager == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb node: acquire context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absolute, key, err := canonicalDatabasePath(path)
	if err != nil {
		return nil, err
	}
	options, cacheBytes, err := manager.normalizeOpenOptions(options)
	if err != nil {
		return nil, err
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		manager.mu.Lock()
		if manager.closed {
			manager.mu.Unlock()
			return nil, ErrClosed
		}
		if _, retiring := manager.retiring[key]; retiring {
			manager.mu.Unlock()
			return nil, fmt.Errorf("%w: %q is being removed", ErrDatabaseBusy, absolute)
		}
		if current := manager.databases[key]; current != nil {
			switch current.state {
			case entryOpening:
				if current.options != options {
					manager.mu.Unlock()
					return nil, optionsMismatchError(absolute)
				}
				if requestedPolicy != nil && current.policy != *requestedPolicy {
					manager.mu.Unlock()
					return nil, policyMismatchError(absolute)
				}
				ready := current.ready
				manager.mu.Unlock()
				if err := manager.wait(ctx, ready); err != nil {
					return nil, err
				}
				if current.err != nil {
					return nil, current.err
				}
				continue
			case entryClosing:
				ready := current.ready
				manager.mu.Unlock()
				if err := manager.wait(ctx, ready); err != nil {
					return nil, err
				}
				if current.closeErr != nil {
					return nil, current.closeErr
				}
				continue
			case entryOpen:
				if current.options != options {
					if current.leases != 0 {
						manager.mu.Unlock()
						return nil, optionsMismatchError(absolute)
					}
					manager.startClosingLocked(current)
					manager.mu.Unlock()
					if err := manager.closeEntry(current); err != nil {
						return nil, err
					}
					continue
				}
				if requestedPolicy != nil && current.policy != *requestedPolicy {
					manager.mu.Unlock()
					return nil, policyMismatchError(absolute)
				}
				manager.removeIdleLocked(current)
				current.leases++
				manager.activeLeases++
				manager.acquisitions++
				manager.reuses++
				lease := manager.newLeaseLocked(current)
				manager.mu.Unlock()
				return lease, nil
			}
		}

		if len(manager.databases) >= manager.limits.maxOpenDatabases ||
			manager.reservedPageCacheBytes+cacheBytes > manager.limits.maxPageCacheBytes {
			if victim := manager.oldestEvictableIdleLocked(); victim != nil {
				manager.startClosingLocked(victim)
				manager.evictions++
				manager.mu.Unlock()
				if err := manager.closeEntry(victim); err != nil {
					return nil, err
				}
				continue
			}
			if manager.capacityHeldOnlyByWarmLocked() {
				manager.mu.Unlock()
				return nil, fmt.Errorf(
					"%w: open databases=%d, page-cache bytes=%d",
					ErrWarmCapacity, len(manager.databases), manager.reservedPageCacheBytes,
				)
			}
			notify := manager.notify
			manager.mu.Unlock()
			if err := manager.wait(ctx, notify); err != nil {
				return nil, err
			}
			continue
		}

		policy := DatabasePolicy{}
		if requestedPolicy != nil {
			policy = *requestedPolicy
		}
		entry := &databaseEntry{
			key: key, path: absolute, options: options, cacheBytes: cacheBytes,
			policy: policy, state: entryOpening, ready: make(chan struct{}), leases: 1,
		}
		manager.databases[key] = entry
		manager.reservedPageCacheBytes += cacheBytes
		manager.activeLeases++
		manager.mu.Unlock()

		opened, err := manager.openEntry(ctx, entry)
		if err != nil {
			return nil, err
		}
		return opened, nil
	}
}

func (manager *Manager) normalizeOpenOptions(options kitdb.OpenOptions) (kitdb.OpenOptions, int64, error) {
	if err := options.HistoryRetention.Validate(); err != nil {
		return kitdb.OpenOptions{}, 0, err
	}
	if options.HistoryRetention.Enabled() && !options.RetainHistory {
		return kitdb.OpenOptions{}, 0, errors.Join(
			kitdb.ErrHistoryRetentionPolicy,
			fmt.Errorf("kitdb node: bounded history retention requires retained history"),
		)
	}
	cacheBytes := options.PageCacheBytes
	switch {
	case cacheBytes == 0:
		cacheBytes = manager.limits.defaultPageCacheBytes
	case cacheBytes < 0:
		cacheBytes = 0
	}
	if cacheBytes > manager.limits.maxPageCacheBytes {
		return kitdb.OpenOptions{}, 0, fmt.Errorf("%w: requested %d bytes, fleet limit %d bytes",
			ErrPageCacheBudget, cacheBytes, manager.limits.maxPageCacheBytes)
	}
	if cacheBytes == 0 {
		options.PageCacheBytes = -1
	} else {
		options.PageCacheBytes = cacheBytes
	}
	return options, cacheBytes, nil
}

func (manager *Manager) openEntry(ctx context.Context, entry *databaseEntry) (*Lease, error) {
	if err := manager.acquireOpenSlot(ctx); err != nil {
		manager.failOpening(entry, err)
		return nil, err
	}
	database, openErr := kitdb.OpenWithOptions(entry.path, entry.options)
	<-manager.openSlots
	if openErr != nil {
		openErr = fmt.Errorf("kitdb node: open %q: %w", entry.path, openErr)
		manager.failOpening(entry, openErr)
		return nil, openErr
	}

	manager.mu.Lock()
	if manager.closed {
		entry.database = database
		entry.leases = 0
		manager.activeLeases--
		close(entry.ready)
		entry.ready = make(chan struct{})
		entry.state = entryClosing
		manager.mu.Unlock()
		closeErr := manager.closeEntry(entry)
		return nil, errors.Join(ErrClosed, closeErr)
	}
	entry.database = database
	entry.state = entryOpen
	close(entry.ready)
	manager.opens++
	manager.acquisitions++
	lease := manager.newLeaseLocked(entry)
	manager.mu.Unlock()
	return lease, nil
}

func (manager *Manager) acquireOpenSlot(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case manager.openSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-manager.ctx.Done():
		return ErrClosed
	}
}

func (manager *Manager) failOpening(entry *databaseEntry, openErr error) {
	manager.mu.Lock()
	entry.err = openErr
	if manager.databases[entry.key] == entry {
		delete(manager.databases, entry.key)
		manager.reservedPageCacheBytes -= entry.cacheBytes
		manager.activeLeases -= entry.leases
		entry.leases = 0
	}
	manager.openFailures++
	close(entry.ready)
	manager.signalLocked()
	manager.mu.Unlock()
}

func (manager *Manager) newLeaseLocked(entry *databaseEntry) *Lease {
	return &Lease{
		manager: manager, entry: entry, database: entry.database, path: entry.path,
	}
}

func (manager *Manager) startClosingLocked(entry *databaseEntry) {
	manager.removeIdleLocked(entry)
	entry.state = entryClosing
	entry.ready = make(chan struct{})
}

func (manager *Manager) closeEntry(entry *databaseEntry) error {
	closeErr := entry.database.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("kitdb node: close %q: %w", entry.path, closeErr)
	}

	manager.mu.Lock()
	entry.closeErr = closeErr
	entry.database = nil
	if manager.databases[entry.key] == entry {
		delete(manager.databases, entry.key)
		manager.reservedPageCacheBytes -= entry.cacheBytes
	}
	manager.closes++
	if closeErr != nil {
		manager.lastCloseError = closeErr.Error()
		if manager.closed {
			manager.closeErr = errors.Join(manager.closeErr, closeErr)
		}
	}
	close(entry.ready)
	manager.signalLocked()
	manager.mu.Unlock()
	return closeErr
}

func (manager *Manager) oldestEvictableIdleLocked() *databaseEntry {
	for element := manager.idle.Back(); element != nil; element = element.Prev() {
		entry := element.Value.(*databaseEntry)
		if !entry.policy.Warm {
			return entry
		}
	}
	return nil
}

func (manager *Manager) capacityHeldOnlyByWarmLocked() bool {
	if len(manager.databases) == 0 {
		return false
	}
	for _, entry := range manager.databases {
		if entry.state != entryOpen || !entry.policy.Warm {
			return false
		}
	}
	return true
}

func (manager *Manager) removeIdleLocked(entry *databaseEntry) {
	if entry.idle == nil {
		return
	}
	manager.idle.Remove(entry.idle)
	entry.idle = nil
}

func (manager *Manager) signalLocked() {
	close(manager.notify)
	manager.notify = make(chan struct{})
}

func (manager *Manager) wait(ctx context.Context, ready <-chan struct{}) error {
	manager.mu.Lock()
	manager.waitingAcquisitions++
	manager.waits++
	manager.mu.Unlock()
	defer func() {
		manager.mu.Lock()
		manager.waitingAcquisitions--
		manager.mu.Unlock()
	}()
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-manager.ctx.Done():
		return ErrClosed
	}
}

func optionsMismatchError(path string) error {
	return fmt.Errorf("%w for %q", ErrOptionsMismatch, path)
}

func policyMismatchError(path string) error {
	return fmt.Errorf("%w for %q", ErrPolicyMismatch, path)
}

func canonicalDatabasePath(path string) (absolute string, key string, err error) {
	if strings.TrimSpace(path) == "" {
		return "", "", fmt.Errorf("kitdb node: database path is empty")
	}
	absolute, err = filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("kitdb node: resolve database path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	key = absolute
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return absolute, key, nil
}

// SetDatabasePolicy deliberately replaces the process-local policy of an
// already managed database. Policy is never written into the KitDB file. A
// database must first be acquired so arbitrary paths cannot create unbounded
// manager state.
func (manager *Manager) SetDatabasePolicy(path string, policy DatabasePolicy) error {
	if manager == nil {
		return ErrClosed
	}
	absolute, key, err := canonicalDatabasePath(path)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return ErrClosed
	}
	entry := manager.databases[key]
	if entry == nil {
		return fmt.Errorf("%w: %q", ErrDatabaseNotManaged, absolute)
	}
	if entry.state == entryClosing {
		return fmt.Errorf("%w: %q is closing", ErrDatabaseBusy, absolute)
	}
	if entry.policy == policy {
		return nil
	}
	entry.policy = policy
	manager.signalLocked()
	return nil
}

// DB returns the leased handle, or nil after Release. Retaining the returned
// pointer beyond the lease lifetime violates the Manager ownership contract.
func (lease *Lease) DB() *kitdb.DB {
	if lease == nil || lease.released.Load() {
		return nil
	}
	return lease.database
}

// Path returns the canonical absolute path owned by the lease.
func (lease *Lease) Path() string {
	if lease == nil {
		return ""
	}
	return lease.path
}

// Gate returns the file-scoped relational operation gate. The pointer remains
// valid only for the lease lifetime and must not be retained after Release.
func (lease *Lease) Gate() *DatabaseGate {
	if lease == nil || lease.released.Load() || lease.entry == nil {
		return nil
	}
	return &lease.entry.gate
}

// Release ends the lease. The last release makes the handle idle, or closes it
// immediately when the Manager is draining.
func (lease *Lease) Release() error {
	if lease == nil {
		return nil
	}
	lease.once.Do(func() {
		lease.released.Store(true)
		lease.releaseErr = lease.manager.release(lease.entry)
	})
	return lease.releaseErr
}

// Close is an alias for Release so a Lease can be used with io.Closer-style
// ownership helpers.
func (lease *Lease) Close() error { return lease.Release() }

func (manager *Manager) release(entry *databaseEntry) error {
	manager.mu.Lock()
	if manager.databases[entry.key] != entry || entry.leases == 0 {
		manager.mu.Unlock()
		return nil
	}
	entry.leases--
	manager.activeLeases--
	if entry.leases != 0 {
		manager.signalLocked()
		manager.mu.Unlock()
		return nil
	}
	if manager.closed {
		manager.startClosingLocked(entry)
		manager.mu.Unlock()
		return manager.closeEntry(entry)
	}
	entry.idle = manager.idle.PushFront(entry)
	manager.signalLocked()
	manager.mu.Unlock()
	return nil
}

// TrimIdle closes least-recently-used evictable idle handles until at most keep
// idle handles remain or every remaining idle handle is warm. Active leases
// and warm handles are never interrupted.
func (manager *Manager) TrimIdle(ctx context.Context, keep int) (int, error) {
	if manager == nil {
		return 0, ErrClosed
	}
	if ctx == nil {
		return 0, fmt.Errorf("kitdb node: trim context is nil")
	}
	if keep < 0 {
		return 0, fmt.Errorf("kitdb node: idle keep count cannot be negative")
	}
	closed := 0
	for {
		if err := ctx.Err(); err != nil {
			return closed, err
		}
		manager.mu.Lock()
		if manager.closed {
			manager.mu.Unlock()
			return closed, ErrClosed
		}
		if manager.idle.Len() <= keep {
			manager.mu.Unlock()
			return closed, nil
		}
		victim := manager.oldestEvictableIdleLocked()
		if victim == nil {
			manager.mu.Unlock()
			return closed, nil
		}
		manager.startClosingLocked(victim)
		manager.evictions++
		manager.mu.Unlock()
		if err := manager.closeEntry(victim); err != nil {
			return closed, err
		}
		closed++
	}
}

// Stats reports fleet counters without opening a database or reading its data.
func (manager *Manager) Stats() Stats {
	if manager == nil {
		return Stats{}
	}
	manager.mu.Lock()
	stats := Stats{
		Closed:           manager.closed,
		ManagedDatabases: len(manager.databases), ActiveLeases: manager.activeLeases,
		WaitingAcquisitions:    manager.waitingAcquisitions,
		ReservedPageCacheBytes: manager.reservedPageCacheBytes,
		MaxOpenDatabases:       manager.limits.maxOpenDatabases,
		MaxPageCacheBytes:      manager.limits.maxPageCacheBytes,
		DefaultPageCacheBytes:  manager.limits.defaultPageCacheBytes,
		MaxConcurrentOpens:     manager.limits.maxConcurrentOpens,
		Acquisitions:           manager.acquisitions, Reuses: manager.reuses,
		Opens: manager.opens, OpenFailures: manager.openFailures,
		Evictions: manager.evictions, Closes: manager.closes, Waits: manager.waits,
		LastCloseError: manager.lastCloseError,
	}
	for _, entry := range manager.databases {
		if entry.policy.Warm {
			stats.WarmDatabases++
			stats.WarmReservedPageCacheBytes += entry.cacheBytes
		}
		switch entry.state {
		case entryOpening:
			stats.OpeningDatabases++
		case entryOpen:
			stats.OpenDatabases++
			if entry.leases == 0 {
				stats.IdleDatabases++
				if entry.policy.Warm {
					stats.WarmIdleDatabases++
				}
			} else {
				stats.ActiveDatabases++
			}
		case entryClosing:
			stats.OpenDatabases++
			stats.ClosingDatabases++
		}
	}
	manager.mu.Unlock()
	manager.addMaintenanceStats(&stats)
	manager.addReplicaFileLinkStats(&stats)
	manager.addProductionStats(&stats)
	return stats
}

// CloseContext stops admission and drains all leased and opening handles. A
// canceled context stops waiting but does not undo the manager's closed state;
// final lease releases continue closing their handles.
func (manager *Manager) CloseContext(ctx context.Context) error {
	if manager == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("kitdb node: close context is nil")
	}

	manager.mu.Lock()
	if !manager.closed {
		manager.closed = true
		manager.cancel()
		manager.signalLocked()
	}
	victims := make([]*databaseEntry, 0, manager.idle.Len())
	for element := manager.idle.Back(); element != nil; {
		previous := element.Prev()
		entry := element.Value.(*databaseEntry)
		manager.startClosingLocked(entry)
		victims = append(victims, entry)
		element = previous
	}
	manager.mu.Unlock()
	manager.beginReplicaFileLinkClose()
	manager.beginProductionClose()
	manager.beginMaintenanceClose()

	for _, victim := range victims {
		_ = manager.closeEntry(victim)
	}
	for {
		manager.mu.Lock()
		if len(manager.databases) == 0 {
			closeErr := manager.closeErr
			manager.mu.Unlock()
			if err := manager.waitForMaintenanceClose(ctx); err != nil {
				return errors.Join(err, closeErr)
			}
			if err := manager.waitForReplicaFileLinkClose(ctx); err != nil {
				return errors.Join(err, closeErr)
			}
			if err := manager.waitForProductionClose(ctx); err != nil {
				return errors.Join(err, closeErr)
			}
			return closeErr
		}
		notify := manager.notify
		manager.mu.Unlock()
		select {
		case <-notify:
		case <-ctx.Done():
			manager.mu.Lock()
			closeErr := manager.closeErr
			manager.mu.Unlock()
			return errors.Join(ctx.Err(), closeErr)
		}
	}
}

// Close drains the Manager without a deadline.
func (manager *Manager) Close() error {
	return manager.CloseContext(context.Background())
}

var _ interface{ Close() error } = (*Manager)(nil)
var _ interface{ Close() error } = (*Lease)(nil)
