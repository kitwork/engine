package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type managedSnapshot struct {
	index      *Index
	generation uint64
	references int
	retired    bool
}

type managedIndex struct {
	manager   *Manager
	key       string
	directory string
	schema    Schema
	build     BuildOptions
	writer    *IndexWriter

	ctx    context.Context
	cancel context.CancelFunc

	admission   sync.RWMutex
	accepting   bool
	failureMu   sync.Mutex
	failure     error
	replacement *managedReplacementState
	requests    chan mutationRequest
	maintain    chan maintenanceRequest
	done        chan struct{}

	snapshotMu              sync.Mutex
	snapshotClosing         bool
	current                 *managedSnapshot
	retired                 map[uint64]*managedSnapshot
	searches                sync.WaitGroup
	searchSlots             chan struct{}
	searchQueueSlots        chan struct{}
	mutationBytes           *byteBudget
	searchHealth            searchRuntimeHealth
	queuedMutations         atomic.Int64
	pendingMutations        atomic.Int64
	pendingMutationBytes    atomic.Int64
	pendingReplacementDocs  atomic.Int64
	pendingReplacementBytes atomic.Int64

	statusMu            sync.Mutex
	lastMaintenanceErr  error
	lastMaintenanceTime time.Time
	checkpointMu        sync.Mutex

	closeOnce sync.Once
	closeErr  error
}

func openManagedIndex(manager *Manager, key string, schema Schema) (*managedIndex, error) {
	directory := managedIndexDirectory(manager.root, key)
	writer, err := NewIndexWriter(directory, schema, manager.options.Writer)
	if err != nil {
		return nil, err
	}
	var current *Index
	current, err = OpenIndex(directory, schema)
	if errors.Is(err, ErrIndexNotFound) {
		current, err = nil, nil
	}
	if err != nil {
		_ = writer.Close()
		return nil, err
	}
	if current != nil {
		// A process can stop after immutable segment publication but before the
		// manifest commit. No reader can reference those files, so reclaim them
		// while this manager holds the directory's sole writer lock.
		if _, err := writer.GarbageCollect(manager.ctx, GarbageCollectOptions{}); err != nil {
			_ = current.Close()
			_ = writer.Close()
			return nil, fmt.Errorf("search: reclaim unpublished artifacts: %w", err)
		}
	} else if _, err := writer.reclaimUnpublishedArtifacts(manager.ctx); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("search: reclaim unpublished artifacts: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	managed := &managedIndex{
		manager: manager, key: key, directory: directory, schema: schema,
		build: writer.segmentOptions, writer: writer,
		ctx: ctx, cancel: cancel, accepting: true,
		requests: make(chan mutationRequest, manager.options.MutationQueueSize),
		maintain: make(chan maintenanceRequest, 1), done: make(chan struct{}),
		retired:          make(map[uint64]*managedSnapshot),
		searchSlots:      make(chan struct{}, manager.options.MaxConcurrentSearchesPerIndex),
		searchQueueSlots: make(chan struct{}, manager.options.MaxQueuedSearchesPerIndex),
		mutationBytes:    newByteBudget(manager.options.MaxPendingMutationBytesPerIndex),
	}
	if current != nil {
		managed.current = &managedSnapshot{
			index: current, generation: current.Info().Generation,
		}
	}
	go managed.runWriter()
	return managed, nil
}

func (managed *managedIndex) acquireSearchSlot(ctx context.Context) (func(), error) {
	if managed == nil {
		return nil, ErrClosed
	}
	if err := acquireBoundedSlot(ctx, managed.searchSlots); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-managed.searchSlots
		})
	}, nil
}

func (managed *managedIndex) searchSnapshot(ctx context.Context, query MatchQuery, options SearchOptions) ([]Hit, error) {
	snapshot, release, err := managed.acquireSnapshot()
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, nil
	}
	defer release()
	return snapshot.index.Search(ctx, query, options)
}

func (managed *managedIndex) acquireSnapshot() (*managedSnapshot, func(), error) {
	managed.snapshotMu.Lock()
	if managed.snapshotClosing {
		managed.snapshotMu.Unlock()
		return nil, nil, ErrClosed
	}
	snapshot := managed.current
	if snapshot == nil {
		managed.snapshotMu.Unlock()
		return nil, func() {}, nil
	}
	snapshot.references++
	managed.searches.Add(1)
	managed.snapshotMu.Unlock()
	var once sync.Once
	return snapshot, func() {
		once.Do(func() {
			managed.releaseSnapshot(snapshot)
		})
	}, nil
}

func (managed *managedIndex) releaseSnapshot(snapshot *managedSnapshot) {
	var closeIndex *Index
	managed.snapshotMu.Lock()
	if snapshot.references > 0 {
		snapshot.references--
	}
	if snapshot.retired && snapshot.references == 0 {
		delete(managed.retired, snapshot.generation)
		closeIndex = snapshot.index
	}
	managed.snapshotMu.Unlock()
	if closeIndex != nil {
		_ = closeIndex.Close()
	}
	managed.searches.Done()
	if closeIndex != nil && managed.manager.options.AutoGarbageCollect {
		managed.signalMaintenance()
	}
}

func (managed *managedIndex) publish(index *Index) {
	if index == nil {
		return
	}
	generation := index.Info().Generation
	var closeIndex *Index
	managed.snapshotMu.Lock()
	if managed.current != nil && managed.current.generation == generation {
		managed.snapshotMu.Unlock()
		_ = index.Close()
		return
	}
	previous := managed.current
	managed.current = &managedSnapshot{index: index, generation: generation}
	if previous != nil {
		previous.retired = true
		if previous.references == 0 {
			closeIndex = previous.index
		} else {
			managed.retired[previous.generation] = previous
		}
	}
	managed.snapshotMu.Unlock()
	if closeIndex != nil {
		_ = closeIndex.Close()
	}
}

func (managed *managedIndex) currentInfo() IndexInfo {
	managed.snapshotMu.Lock()
	defer managed.snapshotMu.Unlock()
	if managed.current == nil {
		return IndexInfo{Path: managed.directory}
	}
	return managed.current.index.Info()
}

func (managed *managedIndex) oldestActiveGeneration() uint64 {
	managed.snapshotMu.Lock()
	defer managed.snapshotMu.Unlock()
	oldest := uint64(0)
	if managed.current != nil {
		oldest = managed.current.generation
	}
	for generation, snapshot := range managed.retired {
		if snapshot.references != 0 && (oldest == 0 || generation < oldest) {
			oldest = generation
		}
	}
	return oldest
}

func (managed *managedIndex) refreshSnapshot() (IndexInfo, error) {
	index, err := OpenIndex(managed.directory, managed.schema)
	if err != nil {
		return IndexInfo{}, err
	}
	info := index.Info()
	managed.publish(index)
	return info, nil
}

func (managed *managedIndex) submit(ctx context.Context, request mutationRequest) (mutationResponse, error) {
	response, err := managed.admit(ctx, request)
	if err != nil {
		return mutationResponse{}, err
	}
	result := <-response
	return result, result.err
}

// submitMany admits every request before waiting, allowing the single writer
// to gather distinct identities into bounded durable batches. An admission or
// mutation failure never abandons already accepted responses; callers can
// safely replay the source batch because projection upserts/deletes are
// idempotent.
func (managed *managedIndex) submitMany(
	ctx context.Context,
	requests []mutationRequest,
) ([]mutationResponse, error) {
	responses := make([]<-chan mutationResponse, 0, len(requests))
	var resultErr error
	for _, request := range requests {
		response, err := managed.admit(ctx, request)
		if err != nil {
			resultErr = errors.Join(resultErr, err)
			break
		}
		responses = append(responses, response)
	}
	results := make([]mutationResponse, 0, len(responses))
	for _, response := range responses {
		result := <-response
		results = append(results, result)
		resultErr = errors.Join(resultErr, result.err)
	}
	return results, resultErr
}

func (managed *managedIndex) admit(
	ctx context.Context,
	request mutationRequest,
) (<-chan mutationResponse, error) {
	if managed == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("search: mutation context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	managed.admission.RLock()
	accepting := managed.accepting
	replacing := managed.replacement != nil
	managed.admission.RUnlock()
	if !accepting {
		return nil, ErrClosed
	}
	if replacing {
		return nil, ErrReplacementActive
	}
	managed.failureMu.Lock()
	failure := managed.failure
	managed.failureMu.Unlock()
	if failure != nil {
		return nil, failure
	}
	reservedBytes, err := managed.mutationPayloadBytes(ctx, request)
	if err != nil {
		return nil, err
	}
	admissionCtx, releaseAdmission := linkContexts(ctx, managed.ctx, managed.manager.ctx)
	defer releaseAdmission()
	if err := managed.mutationBytes.acquire(admissionCtx, reservedBytes); err != nil {
		return nil, managed.admissionError(ctx, err)
	}
	if err := managed.manager.mutationBytes.acquire(admissionCtx, reservedBytes); err != nil {
		managed.mutationBytes.release(reservedBytes)
		return nil, managed.admissionError(ctx, err)
	}
	managed.pendingMutationBytes.Add(reservedBytes)
	managed.manager.pendingMutationBytes.Add(reservedBytes)
	releaseBudget := true
	defer func() {
		if releaseBudget {
			managed.releaseMutationBytes(reservedBytes)
		}
	}()
	request, err = managed.cloneMutation(admissionCtx, request)
	if err != nil {
		return nil, managed.admissionError(ctx, err)
	}
	request.reservedBytes = reservedBytes
	request.response = make(chan mutationResponse, 1)
	managed.admission.RLock()
	if !managed.accepting {
		managed.admission.RUnlock()
		return nil, ErrClosed
	}
	if managed.replacement != nil {
		managed.admission.RUnlock()
		return nil, ErrReplacementActive
	}
	managed.failureMu.Lock()
	failure = managed.failure
	managed.failureMu.Unlock()
	if failure != nil {
		managed.admission.RUnlock()
		return nil, failure
	}
	managed.manager.queuedMutations.Add(1)
	managed.queuedMutations.Add(1)
	managed.pendingMutations.Add(1)
	sent := false
	select {
	case managed.requests <- request:
		sent = true
	case <-ctx.Done():
	case <-managed.ctx.Done():
	case <-managed.manager.ctx.Done():
	}
	managed.admission.RUnlock()
	if !sent {
		managed.manager.queuedMutations.Add(-1)
		managed.queuedMutations.Add(-1)
		managed.pendingMutations.Add(-1)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrClosed
	}
	releaseBudget = false
	return request.response, nil
}

func (managed *managedIndex) stats() ManagedIndexStats {
	searches := managed.searchHealth.snapshot()
	stats := ManagedIndexStats{
		ActiveSearches:          searches.Active,
		WaitingSearches:         searches.Waiting,
		QueuedMutations:         managed.queuedMutations.Load(),
		PendingMutations:        managed.pendingMutations.Load(),
		PendingMutationBytes:    managed.pendingMutationBytes.Load(),
		PendingReplacementDocs:  managed.pendingReplacementDocs.Load(),
		PendingReplacementBytes: managed.pendingReplacementBytes.Load(),
		PendingWriteBytes:       managed.mutationBytes.value(),
		Searches:                searches,
	}
	managed.admission.RLock()
	stats.ReplacementActive = managed.replacement != nil
	managed.admission.RUnlock()
	managed.snapshotMu.Lock()
	if managed.current != nil {
		info := managed.current.index.Info()
		stats.Generation = info.Generation
		stats.Segments = info.Segments
		stats.Documents = info.Documents
		stats.PhysicalDocuments = info.PhysicalDocuments
		stats.Deleted = info.Deleted
	}
	stats.RetiredGenerations = len(managed.retired)
	managed.snapshotMu.Unlock()
	managed.failureMu.Lock()
	if managed.failure != nil {
		stats.Failed = true
		stats.Failure = managed.failure.Error()
	}
	managed.failureMu.Unlock()
	managed.statusMu.Lock()
	stats.LastMaintenance = managed.lastMaintenanceTime
	if managed.lastMaintenanceErr != nil {
		stats.LastMaintenanceError = managed.lastMaintenanceErr.Error()
	}
	managed.statusMu.Unlock()
	return stats
}

func (managed *managedIndex) requestMaintenance(ctx context.Context) error {
	if managed == nil {
		return ErrClosed
	}
	if ctx == nil {
		return fmt.Errorf("search: maintenance context is nil")
	}
	managed.admission.RLock()
	accepting := managed.accepting
	if !accepting {
		managed.admission.RUnlock()
		return ErrClosed
	}
	if managed.replacement != nil {
		managed.admission.RUnlock()
		return ErrReplacementActive
	}
	managed.admission.RUnlock()
	managed.failureMu.Lock()
	failure := managed.failure
	managed.failureMu.Unlock()
	if failure != nil {
		return failure
	}
	response := make(chan error, 1)
	request := maintenanceRequest{response: response}
	managed.admission.RLock()
	if !managed.accepting {
		managed.admission.RUnlock()
		return ErrClosed
	}
	if managed.replacement != nil {
		managed.admission.RUnlock()
		return ErrReplacementActive
	}
	select {
	case managed.maintain <- request:
	case <-ctx.Done():
		managed.admission.RUnlock()
		return ctx.Err()
	case <-managed.ctx.Done():
		managed.admission.RUnlock()
		return ErrClosed
	case <-managed.manager.ctx.Done():
		managed.admission.RUnlock()
		return ErrClosed
	}
	managed.admission.RUnlock()
	select {
	case err := <-response:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-managed.ctx.Done():
		return ErrClosed
	}
}

func (managed *managedIndex) signalMaintenance() {
	managed.admission.RLock()
	defer managed.admission.RUnlock()
	if !managed.accepting || managed.replacement != nil {
		return
	}
	select {
	case managed.maintain <- maintenanceRequest{}:
	default:
	}
}

func (managed *managedIndex) fail(err error) error {
	if err == nil {
		return nil
	}
	terminal := fmt.Errorf("%w: %w", ErrIndexUnavailable, err)
	managed.failureMu.Lock()
	if managed.failure == nil {
		managed.failure = terminal
	}
	terminal = managed.failure
	managed.failureMu.Unlock()
	return terminal
}

func (managed *managedIndex) recordMaintenance(err error) {
	managed.statusMu.Lock()
	managed.lastMaintenanceErr = err
	managed.lastMaintenanceTime = time.Now()
	managed.statusMu.Unlock()
}

func (managed *managedIndex) Close() error {
	if managed == nil {
		return nil
	}
	managed.closeOnce.Do(func() {
		managed.cancel()
		managed.admission.Lock()
		managed.accepting = false
		close(managed.requests)
		managed.admission.Unlock()
		<-managed.done

		managed.snapshotMu.Lock()
		managed.snapshotClosing = true
		var closeNow []*Index
		if managed.current != nil {
			managed.current.retired = true
			if managed.current.references == 0 {
				closeNow = append(closeNow, managed.current.index)
			} else {
				managed.retired[managed.current.generation] = managed.current
			}
			managed.current = nil
		}
		managed.snapshotMu.Unlock()
		for _, index := range closeNow {
			if err := index.Close(); err != nil {
				managed.closeErr = errors.Join(managed.closeErr, err)
			}
		}
		managed.searches.Wait()
	})
	return managed.closeErr
}
