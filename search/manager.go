package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	defaultManagerOpenIndexes        = 64
	defaultManagerSearchesPerIndex   = 8
	defaultManagerQueuedSearches     = 4096
	defaultManagerQueuedPerIndex     = 256
	defaultManagerMutationBatches    = 8
	defaultManagerReplacements       = 1
	defaultManagerMaintenances       = 2
	defaultManagerMutationQueue      = 1024
	defaultManagerMutationBatch      = 128
	defaultManagerMutationDelay      = 5 * time.Millisecond
	defaultManagerMutationBytes      = int64(256 << 20)
	defaultManagerMutationIndexBytes = int64(64 << 20)
	defaultManagerCommitTimeout      = 30 * time.Second
	defaultManagerReplacementTimeout = 2 * time.Hour
	defaultManagerMaintenanceTimeout = 10 * time.Minute
	maximumManagerOpenIndexes        = 10_000
	maximumManagerConcurrentSearches = 1_000_000
	maximumManagerQueuedSearches     = 1 << 20
	maximumManagerMutationQueue      = 1 << 20
	maximumManagerMutationDelay      = 10 * time.Second
	maximumManagerMutationBytes      = int64(1 << 40)
	maximumManagerCommitTimeout      = 10 * time.Minute
	maximumManagerReplacementTimeout = 24 * time.Hour
	maximumManagerMaintenanceTimeout = 2 * time.Hour
	maximumManagedIndexKeyBytes      = 1024
)

// ManagerOptions bound process-local ownership for managed tenant indexes.
// AutoGarbageCollect is deliberately false by default because it is only safe
// when this Manager owns every reader of its root directory.
type ManagerOptions struct {
	MaxOpenIndexes                  int
	MaxConcurrentSearches           int
	MaxConcurrentSearchesPerIndex   int
	MaxQueuedSearches               int
	MaxQueuedSearchesPerIndex       int
	MaxConcurrentMutationBatches    int
	MaxConcurrentReplacements       int
	MaxConcurrentMaintenance        int
	MutationQueueSize               int
	MutationBatchSize               int
	MutationBatchDelay              time.Duration
	MaxPendingMutationBytes         int64
	MaxPendingMutationBytesPerIndex int64
	CommitTimeout                   time.Duration
	ReplacementTimeout              time.Duration
	MaintenanceTimeout              time.Duration
	Writer                          WriterOptions
	Compact                         CompactOptions
	DisableAutoCompact              bool
	AutoGarbageCollect              bool
}

type managerOptions struct {
	ManagerOptions
	compact CompactOptions
}

// ManagerStats are bounded process-local lifecycle counters.
type ManagerStats struct {
	OpenIndexes             int
	ActiveSearches          int64
	WaitingSearches         int64
	ActiveMutationBatches   int64
	ActiveReplacements      int64
	ActiveMaintenance       int64
	QueuedMutations         int64
	QueuedReplacements      int64
	PendingMutationBytes    int64
	PendingReplacementBytes int64
	PendingWriteBytes       int64
	Commits                 uint64
	Compactions             uint64
	GarbageCollections      uint64
	Searches                SearchRuntimeStats
}

// ManagedIndexStats describe one already-open tenant without causing disk I/O.
type ManagedIndexStats struct {
	Loading                 bool
	Generation              uint64
	Segments                int
	Documents               uint64
	PhysicalDocuments       uint64
	Deleted                 uint64
	ActiveSearches          int64
	WaitingSearches         int64
	QueuedMutations         int64
	PendingMutations        int64
	PendingMutationBytes    int64
	ReplacementActive       bool
	PendingReplacementDocs  int64
	PendingReplacementBytes int64
	PendingWriteBytes       int64
	RetiredGenerations      int
	Failed                  bool
	Failure                 string
	LastMaintenance         time.Time
	LastMaintenanceError    string
	Searches                SearchRuntimeStats
}

// Manager owns a bounded set of tenant search indexes. It is safe for
// concurrent use. Each open tenant has exactly one writer goroutine.
type Manager struct {
	root    string
	options managerOptions

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	indexes map[string]*managerEntry
	closed  bool
	loading sync.WaitGroup

	searchSlots      chan struct{}
	searchQueueSlots chan struct{}
	mutationSlots    chan struct{}
	replacementSlots chan struct{}
	maintenanceSlots chan struct{}
	mutationBytes    *byteBudget

	searchHealth            searchRuntimeHealth
	activeMutationBatches   atomic.Int64
	activeReplacements      atomic.Int64
	activeMaintenance       atomic.Int64
	queuedMutations         atomic.Int64
	queuedReplacements      atomic.Int64
	pendingMutationBytes    atomic.Int64
	pendingReplacementBytes atomic.Int64
	commits                 atomic.Uint64
	compactions             atomic.Uint64
	garbageCollections      atomic.Uint64

	closeOnce sync.Once
	closeErr  error
}

type managerEntry struct {
	ready chan struct{}
	index *managedIndex
	err   error
	// closing is published before CloseIndex starts draining the managed
	// owner. New callers wait for it and retry instead of borrowing an index
	// whose context has already been canceled.
	closing  chan struct{}
	closeErr error
	users    sync.WaitGroup
}

// NewManager creates one bounded multi-index owner rooted at directory.
func NewManager(directory string, options ManagerOptions) (*Manager, error) {
	if directory == "" {
		return nil, fmt.Errorf("search: manager directory is empty")
	}
	normalized, err := normalizeManagerOptions(options)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	absolute = filepath.Clean(absolute)
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return nil, err
	}
	stat, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("search: manager path is not a directory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		root: absolute, options: normalized, ctx: ctx, cancel: cancel,
		indexes:          make(map[string]*managerEntry),
		searchSlots:      make(chan struct{}, normalized.MaxConcurrentSearches),
		searchQueueSlots: make(chan struct{}, normalized.MaxQueuedSearches),
		mutationSlots:    make(chan struct{}, normalized.MaxConcurrentMutationBatches),
		replacementSlots: make(chan struct{}, normalized.MaxConcurrentReplacements),
		maintenanceSlots: make(chan struct{}, normalized.MaxConcurrentMaintenance),
		mutationBytes:    newByteBudget(normalized.MaxPendingMutationBytes),
	}, nil
}

func normalizeManagerOptions(options ManagerOptions) (managerOptions, error) {
	if options.MaxOpenIndexes == 0 {
		options.MaxOpenIndexes = defaultManagerOpenIndexes
	}
	if options.MaxConcurrentSearches == 0 {
		options.MaxConcurrentSearches = defaultManagerConcurrentSearches(runtime.GOMAXPROCS(0))
	}
	if options.MaxConcurrentSearchesPerIndex == 0 {
		options.MaxConcurrentSearchesPerIndex = min(defaultManagerSearchesPerIndex, options.MaxConcurrentSearches)
	}
	if options.MaxQueuedSearches == 0 {
		options.MaxQueuedSearches = defaultManagerQueuedSearches
	}
	if options.MaxQueuedSearchesPerIndex == 0 {
		options.MaxQueuedSearchesPerIndex = min(defaultManagerQueuedPerIndex, options.MaxQueuedSearches)
	}
	if options.MaxConcurrentMutationBatches == 0 {
		options.MaxConcurrentMutationBatches = min(
			defaultManagerMutationBatches, options.MaxOpenIndexes, max(1, runtime.GOMAXPROCS(0)),
		)
	}
	if options.MaxConcurrentReplacements == 0 {
		options.MaxConcurrentReplacements = min(defaultManagerReplacements, options.MaxOpenIndexes)
	}
	if options.MaxConcurrentMaintenance == 0 {
		options.MaxConcurrentMaintenance = min(
			defaultManagerMaintenances, options.MaxOpenIndexes, max(1, runtime.GOMAXPROCS(0)/2),
		)
	}
	if options.MutationQueueSize == 0 {
		options.MutationQueueSize = defaultManagerMutationQueue
	}
	if options.MutationBatchSize == 0 {
		options.MutationBatchSize = min(defaultManagerMutationBatch, options.MutationQueueSize)
	}
	if options.MutationBatchDelay == 0 {
		options.MutationBatchDelay = defaultManagerMutationDelay
	}
	if options.MaxPendingMutationBytes == 0 {
		options.MaxPendingMutationBytes = defaultManagerMutationBytes
	}
	if options.MaxPendingMutationBytesPerIndex == 0 {
		options.MaxPendingMutationBytesPerIndex = min(
			defaultManagerMutationIndexBytes, options.MaxPendingMutationBytes,
		)
	}
	if options.CommitTimeout == 0 {
		options.CommitTimeout = defaultManagerCommitTimeout
	}
	if options.ReplacementTimeout == 0 {
		options.ReplacementTimeout = defaultManagerReplacementTimeout
	}
	if options.MaintenanceTimeout == 0 {
		options.MaintenanceTimeout = defaultManagerMaintenanceTimeout
	}
	if options.MaxOpenIndexes < 1 || options.MaxOpenIndexes > maximumManagerOpenIndexes ||
		options.MaxConcurrentSearches < 1 || options.MaxConcurrentSearches > maximumManagerConcurrentSearches ||
		options.MaxConcurrentSearchesPerIndex < 1 ||
		options.MaxConcurrentSearchesPerIndex > options.MaxConcurrentSearches ||
		options.MaxQueuedSearches < 1 || options.MaxQueuedSearches > maximumManagerQueuedSearches ||
		options.MaxQueuedSearchesPerIndex < 1 ||
		options.MaxQueuedSearchesPerIndex > options.MaxQueuedSearches ||
		options.MaxConcurrentMutationBatches < 1 ||
		options.MaxConcurrentMutationBatches > options.MaxOpenIndexes ||
		options.MaxConcurrentReplacements < 1 ||
		options.MaxConcurrentReplacements > options.MaxOpenIndexes ||
		options.MaxConcurrentMaintenance < 1 || options.MaxConcurrentMaintenance > options.MaxOpenIndexes ||
		options.MutationQueueSize < 1 || options.MutationQueueSize > maximumManagerMutationQueue ||
		options.MutationBatchSize < 1 || options.MutationBatchSize > options.MutationQueueSize ||
		options.MutationBatchDelay < 0 || options.MutationBatchDelay > maximumManagerMutationDelay ||
		options.MaxPendingMutationBytes < 1 || options.MaxPendingMutationBytes > maximumManagerMutationBytes ||
		options.MaxPendingMutationBytesPerIndex < 1 ||
		options.MaxPendingMutationBytesPerIndex > options.MaxPendingMutationBytes ||
		options.CommitTimeout <= 0 || options.CommitTimeout > maximumManagerCommitTimeout ||
		options.ReplacementTimeout <= 0 || options.ReplacementTimeout > maximumManagerReplacementTimeout ||
		options.MaintenanceTimeout <= 0 || options.MaintenanceTimeout > maximumManagerMaintenanceTimeout {
		return managerOptions{}, fmt.Errorf("search: invalid manager options")
	}
	compact, err := normalizeCompactOptions(options.Compact)
	if err != nil {
		return managerOptions{}, err
	}
	return managerOptions{ManagerOptions: options, compact: compact}, nil
}

func defaultManagerConcurrentSearches(processors int) int {
	return min(maximumManagerConcurrentSearches, max(4, processors))
}

func (manager *Manager) managed(
	ctx context.Context,
	key string,
	schema Schema,
) (*managedIndex, func(), error) {
	if manager == nil {
		return nil, nil, ErrClosed
	}
	if ctx == nil {
		return nil, nil, fmt.Errorf("search: manager context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !schema.valid() {
		return nil, nil, fmt.Errorf("search: invalid schema")
	}
	if err := validateManagedIndexKey(key); err != nil {
		return nil, nil, err
	}

	for {
		manager.mu.Lock()
		if manager.closed {
			manager.mu.Unlock()
			return nil, nil, ErrClosed
		}
		entry := manager.indexes[key]
		if entry != nil && entry.closing != nil {
			closing := entry.closing
			manager.mu.Unlock()
			select {
			case <-closing:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-manager.ctx.Done():
				return nil, nil, ErrClosed
			}
		}
		if entry == nil {
			if len(manager.indexes) >= manager.options.MaxOpenIndexes {
				manager.mu.Unlock()
				return nil, nil, ErrManagerCapacity
			}
			entry = &managerEntry{ready: make(chan struct{})}
			manager.indexes[key] = entry
			manager.loading.Add(1)
			manager.mu.Unlock()
			go manager.loadManagedIndex(key, schema, entry)
		} else {
			manager.mu.Unlock()
		}

		select {
		case <-entry.ready:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-manager.ctx.Done():
			return nil, nil, ErrClosed
		}
		if entry.err != nil {
			return nil, nil, entry.err
		}

		// Recheck under the ownership lock after loading. CloseIndex may have
		// started while this caller waited on ready; users added here are then
		// guaranteed to drain before the managed owner is closed.
		manager.mu.Lock()
		if manager.closed {
			manager.mu.Unlock()
			return nil, nil, ErrClosed
		}
		if manager.indexes[key] != entry {
			manager.mu.Unlock()
			continue
		}
		if entry.closing != nil {
			closing := entry.closing
			manager.mu.Unlock()
			select {
			case <-closing:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-manager.ctx.Done():
				return nil, nil, ErrClosed
			}
		}
		if entry.index == nil {
			manager.mu.Unlock()
			return nil, nil, ErrIndexUnavailable
		}
		if entry.index.schema.fingerprint != schema.fingerprint {
			manager.mu.Unlock()
			return nil, nil, ErrSchemaMismatch
		}
		entry.users.Add(1)
		managed := entry.index
		manager.mu.Unlock()
		return managed, entry.users.Done, nil
	}
}

func (manager *Manager) loadManagedIndex(key string, schema Schema, entry *managerEntry) {
	managed, openErr := openManagedIndex(manager, key, schema)
	manager.mu.Lock()
	if manager.closed && openErr == nil {
		openErr = ErrClosed
	}
	entry.err = openErr
	if openErr != nil {
		entry.index = nil
		delete(manager.indexes, key)
	} else {
		entry.index = managed
	}
	close(entry.ready)
	manager.mu.Unlock()
	if openErr != nil && managed != nil {
		_ = managed.Close()
	}
	manager.loading.Done()
}

// Search runs against one immutable leased snapshot.
func (manager *Manager) Search(
	ctx context.Context,
	key string,
	schema Schema,
	query MatchQuery,
	options SearchOptions,
) (hits []Hit, searchErr error) {
	if manager == nil {
		return nil, ErrClosed
	}
	started := time.Now()
	manager.searchHealth.start()
	defer func() {
		manager.searchHealth.finish(time.Since(started), searchErr)
	}()

	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return nil, err
	}
	defer releaseManaged()
	managedStarted := time.Now()
	managed.searchHealth.start()
	defer func() {
		managed.searchHealth.finish(time.Since(managedStarted), searchErr)
	}()
	linked, releaseContext := linkContexts(ctx, manager.ctx, managed.ctx)
	defer releaseContext()

	if !tryAcquireBoundedSlot(managed.searchQueueSlots) {
		return nil, ErrSearchOverloaded
	}
	indexQueued := true
	defer func() {
		if indexQueued {
			<-managed.searchQueueSlots
		}
	}()
	if !tryAcquireBoundedSlot(manager.searchQueueSlots) {
		return nil, ErrSearchOverloaded
	}
	managerQueued := true
	defer func() {
		if managerQueued {
			<-manager.searchQueueSlots
		}
	}()
	waitStarted := time.Now()
	manager.searchHealth.waitStarted()
	managed.searchHealth.waitStarted()
	waiting := true
	defer func() {
		if waiting {
			elapsed := time.Since(waitStarted)
			manager.searchHealth.waitFinished(elapsed)
			managed.searchHealth.waitFinished(elapsed)
		}
	}()
	releaseIndexSlot, err := managed.acquireSearchSlot(linked)
	if err != nil {
		return nil, err
	}
	defer releaseIndexSlot()
	if err := acquireBoundedSlot(linked, manager.searchSlots); err != nil {
		return nil, err
	}
	<-manager.searchQueueSlots
	managerQueued = false
	<-managed.searchQueueSlots
	indexQueued = false
	waitElapsed := time.Since(waitStarted)
	manager.searchHealth.waitFinished(waitElapsed)
	managed.searchHealth.waitFinished(waitElapsed)
	waiting = false

	manager.searchHealth.executionStarted()
	managed.searchHealth.executionStarted()
	executionStarted := time.Now()
	defer func() {
		elapsed := time.Since(executionStarted)
		managed.searchHealth.executionFinished(elapsed)
		manager.searchHealth.executionFinished(elapsed)
		<-manager.searchSlots
	}()
	return managed.searchSnapshot(linked, query, options)
}

// Add durably appends a document in a bounded per-index batch.
func (manager *Manager) Add(ctx context.Context, key string, schema Schema, document Document) (IndexInfo, error) {
	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return IndexInfo{}, err
	}
	defer releaseManaged()
	response, err := managed.submit(ctx, mutationRequest{kind: mutationAdd, document: document})
	return response.info, err
}

// Update durably replaces a live committed document.
func (manager *Manager) Update(ctx context.Context, key string, schema Schema, document Document) (IndexInfo, error) {
	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return IndexInfo{}, err
	}
	defer releaseManaged()
	response, err := managed.submit(ctx, mutationRequest{kind: mutationUpdate, document: document})
	return response.info, err
}

// Delete durably tombstones a live committed identifier.
func (manager *Manager) Delete(ctx context.Context, key string, schema Schema, identifier string) (bool, IndexInfo, error) {
	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return false, IndexInfo{}, err
	}
	defer releaseManaged()
	response, err := managed.submit(ctx, mutationRequest{kind: mutationDelete, identifier: identifier})
	return response.deleted, response.info, err
}

// BeginReplacement starts a bounded streaming rebuild for one tenant. Search
// continues on the previously committed snapshot until Commit publishes the
// replacement. The supplied context owns the lifetime of the whole session.
func (manager *Manager) BeginReplacement(
	ctx context.Context,
	key string,
	schema Schema,
) (*ManagedReplacement, error) {
	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return nil, err
	}
	defer releaseManaged()
	return managed.beginReplacement(ctx)
}

// Reindex rebuilds one tenant from a streaming source callback without
// materializing the full dataset in memory.
func (manager *Manager) Reindex(
	ctx context.Context,
	key string,
	schema Schema,
	visit func(add func(Document) error) error,
) (_ IndexInfo, returnErr error) {
	if visit == nil {
		return IndexInfo{}, fmt.Errorf("search: reindex visitor is nil")
	}
	replacement, err := manager.BeginReplacement(ctx, key, schema)
	if err != nil {
		return IndexInfo{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, replacement.Abort()) }()
	add := func(document Document) error {
		return replacement.Add(ctx, document)
	}
	if err := visit(add); err != nil {
		return IndexInfo{}, err
	}
	return replacement.Commit(ctx)
}

// Maintain serializes one configured compact/GC pass after prior mutations.
func (manager *Manager) Maintain(ctx context.Context, key string, schema Schema) error {
	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return err
	}
	defer releaseManaged()
	return managed.requestMaintenance(ctx)
}

// CloseIndex drains and evicts one tenant so another tenant can use its slot.
func (manager *Manager) CloseIndex(key string) error {
	if manager == nil {
		return nil
	}
	if err := validateManagedIndexKey(key); err != nil {
		return err
	}
	manager.mu.Lock()
	entry := manager.indexes[key]
	ownsClose := false
	if entry != nil && entry.closing == nil {
		entry.closing = make(chan struct{})
		ownsClose = true
	}
	var closing <-chan struct{}
	if entry != nil {
		closing = entry.closing
	}
	manager.mu.Unlock()
	if entry == nil {
		return nil
	}
	if !ownsClose {
		<-closing
		return entry.closeErr
	}

	<-entry.ready
	entry.users.Wait()
	var closeErr error
	if entry.index != nil {
		closeErr = entry.index.Close()
	}
	manager.mu.Lock()
	entry.closeErr = closeErr
	if manager.indexes[key] == entry {
		delete(manager.indexes, key)
	}
	close(entry.closing)
	manager.mu.Unlock()
	return closeErr
}

// Stats reports process-local bounded counters without opening indexes.
func (manager *Manager) Stats() ManagerStats {
	if manager == nil {
		return ManagerStats{}
	}
	manager.mu.Lock()
	open := len(manager.indexes)
	manager.mu.Unlock()
	searches := manager.searchHealth.snapshot()
	return ManagerStats{
		OpenIndexes: open, ActiveSearches: searches.Active, WaitingSearches: searches.Waiting,
		ActiveMutationBatches:   manager.activeMutationBatches.Load(),
		ActiveReplacements:      manager.activeReplacements.Load(),
		ActiveMaintenance:       manager.activeMaintenance.Load(),
		QueuedMutations:         manager.queuedMutations.Load(),
		QueuedReplacements:      manager.queuedReplacements.Load(),
		PendingMutationBytes:    manager.pendingMutationBytes.Load(),
		PendingReplacementBytes: manager.pendingReplacementBytes.Load(),
		PendingWriteBytes:       manager.mutationBytes.value(),
		Commits:                 manager.commits.Load(), Compactions: manager.compactions.Load(),
		GarbageCollections: manager.garbageCollections.Load(), Searches: searches,
	}
}

// Info opens or reuses one managed index and returns its current durable
// generation without executing a query.
func (manager *Manager) Info(ctx context.Context, key string, schema Schema) (IndexInfo, error) {
	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return IndexInfo{}, err
	}
	defer releaseManaged()
	return managed.currentInfo(), nil
}

// IndexStats returns runtime state for an already-open tenant. It never opens
// an index and reports false when key is not currently owned by this Manager.
func (manager *Manager) IndexStats(key string) (ManagedIndexStats, bool) {
	if manager == nil {
		return ManagedIndexStats{}, false
	}
	manager.mu.Lock()
	entry := manager.indexes[key]
	if entry == nil {
		manager.mu.Unlock()
		return ManagedIndexStats{}, false
	}
	select {
	case <-entry.ready:
		if entry.closing != nil || entry.index == nil {
			manager.mu.Unlock()
			return ManagedIndexStats{}, false
		}
		entry.users.Add(1)
		index := entry.index
		manager.mu.Unlock()
		defer entry.users.Done()
		return index.stats(), true
	default:
		manager.mu.Unlock()
		return ManagedIndexStats{Loading: true}, true
	}
}

// Close stops admission, cancels searches, drains accepted mutations, and
// releases every snapshot and writer.
func (manager *Manager) Close() error {
	if manager == nil {
		return nil
	}
	manager.closeOnce.Do(func() {
		manager.mu.Lock()
		manager.closed = true
		manager.cancel()
		manager.mu.Unlock()

		manager.loading.Wait()
		manager.mu.Lock()
		indexes := make([]*managedIndex, 0, len(manager.indexes))
		for _, entry := range manager.indexes {
			if entry.index != nil {
				indexes = append(indexes, entry.index)
			}
		}
		manager.indexes = make(map[string]*managerEntry)
		manager.mu.Unlock()

		closeErrors := make([]error, 0)
		for _, index := range indexes {
			if err := index.Close(); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		manager.closeErr = errors.Join(closeErrors...)
	})
	return manager.closeErr
}

func validateManagedIndexKey(key string) error {
	if key == "" || len(key) > maximumManagedIndexKeyBytes || !utf8.ValidString(key) {
		return fmt.Errorf("search: managed index key is invalid")
	}
	return nil
}

func managedIndexDirectory(root, key string) string {
	hash := sha256.Sum256([]byte(key))
	encoded := hex.EncodeToString(hash[:])
	return filepath.Join(root, encoded[:2], encoded)
}

func acquireBoundedSlot(ctx context.Context, slots chan struct{}) error {
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func tryAcquireBoundedSlot(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func linkContexts(parent context.Context, lifetimes ...context.Context) (context.Context, func()) {
	linked, cancel := context.WithCancel(parent)
	stops := make([]func() bool, 0, len(lifetimes))
	for _, lifetime := range lifetimes {
		if lifetime != nil {
			stops = append(stops, context.AfterFunc(lifetime, cancel))
		}
	}
	return linked, func() {
		for _, stop := range stops {
			stop()
		}
		cancel()
	}
}

var _ interface{ Close() error } = (*Manager)(nil)
