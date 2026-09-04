package node

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb"
)

var (
	ErrMaintenanceQueueFull         = errors.New("kitdb node: maintenance queue is full")
	ErrMaintenanceDatabaseQueueFull = errors.New(
		"kitdb node: database maintenance queue is full",
	)
	ErrMaintenanceResourceCapacity = errors.New(
		"kitdb node: maintenance resources exceed simultaneous fleet capacity",
	)
	ErrRowMigrationDriverUnavailable = errors.New(
		"kitdb node: row-migration driver is unavailable",
	)
	ErrRowMigrationDriverConflict = errors.New(
		"kitdb node: a different row-migration driver is already registered",
	)
	ErrSecondaryIndexDriverUnavailable = errors.New(
		"kitdb node: secondary-index driver is unavailable",
	)
	ErrSecondaryIndexDriverConflict = errors.New(
		"kitdb node: a different secondary-index driver is already registered",
	)
)

// MaintenanceKind identifies a bounded, engine-owned maintenance operation.
// Arbitrary callbacks are deliberately not accepted by the public scheduler.
type MaintenanceKind string

const (
	MaintenanceCheckpoint             MaintenanceKind = "checkpoint"
	MaintenanceVerify                 MaintenanceKind = "verify"
	MaintenanceBackup                 MaintenanceKind = "backup"
	MaintenanceHistoryPrune           MaintenanceKind = "history-prune"
	MaintenanceHistoryRetention       MaintenanceKind = "history-retention"
	MaintenanceReplicaCatchUp         MaintenanceKind = "replica-catch-up"
	MaintenanceReplicaFilePublish     MaintenanceKind = "replica-file-publish"
	MaintenanceReplicaFileApply       MaintenanceKind = "replica-file-apply"
	MaintenanceReplicaFileAcknowledge MaintenanceKind = "replica-file-acknowledge"
	MaintenanceRowMigration           MaintenanceKind = "row-migration"
	MaintenanceSecondaryIndex         MaintenanceKind = "secondary-index"
)

// RowMigrationProgress is the bounded outcome of one relational migration
// chunk. Pending asks the scheduler to put the same task at the back of the
// background queue. Durable migration state, definitions, and cursors must
// remain inside the database; a driver may not rely on this value for resume.
type RowMigrationProgress struct {
	Transaction uint64
	Pending     bool
	Advanced    bool
}

// RowMigrationDriver connects a host-owned relational layer to the KitDB node
// governor. Registration is process-local, while all resumable state remains
// durable in KitDB. The stable ID makes repeated registration by app runtimes
// idempotent without permitting a different implementation to replace it.
type RowMigrationDriver interface {
	RowMigrationDriverID() string
	AdvanceRowMigration(context.Context, *kitdb.DB) (RowMigrationProgress, error)
}

// RowMigrationResult summarizes the chunks executed by one coalesced task.
type RowMigrationResult struct {
	Chunks         uint64
	AdvancedChunks uint64
	Pending        bool
}

// SecondaryIndexProgress is the bounded outcome of one durable KIBS build or
// cleanup chunk. Pending is valid only when the same commit advanced durable
// progress, preventing a broken driver from spinning in the background queue.
type SecondaryIndexProgress struct {
	Transaction uint64
	Pending     bool
	Advanced    bool
}

// SecondaryIndexDriver connects the relational secondary-index lifecycle to
// the node governor. Definitions, generations, and cursors remain in KitDB;
// the driver is deliberately stateless across dispatches.
type SecondaryIndexDriver interface {
	SecondaryIndexDriverID() string
	AdvanceSecondaryIndex(context.Context, *kitdb.DB) (SecondaryIndexProgress, error)
}

// SecondaryIndexResult summarizes one coalesced build/cleanup task.
type SecondaryIndexResult struct {
	Chunks         uint64
	AdvancedChunks uint64
	Pending        bool
}

// MaintenancePriority controls scheduling order. Priority is weighted rather
// than absolute, so background work continues making progress under load.
type MaintenancePriority uint8

const (
	MaintenanceNormal MaintenancePriority = iota
	MaintenanceBackground
	MaintenanceUrgent

	maintenancePriorityCount
	maximumForegroundMaintenanceBurst = 6
	maximumUrgentMaintenanceBurst     = 4
)

// MaintenanceResult describes one completed operation. Transaction is the
// published checkpoint, backup-anchor, retained-history base, or replica target
// boundary and remains zero for verify.
type MaintenanceResult struct {
	Kind                   MaintenanceKind
	Path                   string
	Transaction            uint64
	Backup                 *VerifiedBackupResult
	HistoryPrune           *kitdb.HistoryPruneResult
	HistoryRetention       *kitdb.HistoryRetentionResult
	ReplicaCatchUp         *kitdb.ReplicaCatchUp
	ReplicaFilePublish     *ReplicaFilePublishResult
	ReplicaFileApply       *ReplicaFileApplyResult
	ReplicaFileAcknowledge *ReplicaFileAcknowledgeResult
	RowMigration           *RowMigrationResult
	SecondaryIndex         *SecondaryIndexResult
	QueuedAt               time.Time
	StartedAt              time.Time
	FinishedAt             time.Time
	Duration               time.Duration
}

// MaintenanceTicket observes a shared scheduled operation. Canceling Wait
// stops only that waiter; it never cancels a job shared by other callers.
type MaintenanceTicket struct {
	task *maintenanceTask
}

type maintenanceTaskID struct {
	path      string
	kind      MaintenanceKind
	operation string
}

type maintenanceTask struct {
	id        maintenanceTaskID
	path      string
	resources []maintenanceResource
	kind      MaintenanceKind
	priority  MaintenancePriority
	payload   maintenancePayload
	queuedAt  time.Time
	done      chan struct{}
	result    MaintenanceResult
	err       error
	element   *list.Element
	startedAt time.Time
	duration  time.Duration

	rowMigrationChunks           uint64
	rowMigrationAdvancedChunks   uint64
	secondaryIndexChunks         uint64
	secondaryIndexAdvancedChunks uint64
}

type maintenanceResourceRequest struct {
	path        string
	options     kitdb.OpenOptions
	reserveOnly bool
}

type maintenanceResource struct {
	path        string
	key         string
	options     kitdb.OpenOptions
	cacheBytes  int64
	reserveOnly bool
}

type maintenanceHandle struct {
	resource maintenanceResource
	lease    *Lease
}

type maintenancePayload struct {
	backup                 *backupMaintenance
	historyPrune           *historyPruneMaintenance
	historyRetention       *historyRetentionMaintenance
	replicaCatchUp         *replicaCatchUpMaintenance
	replicaFilePublish     *replicaFilePublishMaintenance
	replicaFileApply       *replicaFileApplyMaintenance
	replicaFileAcknowledge *replicaFileAcknowledgeMaintenance
	rowMigration           RowMigrationDriver
	secondaryIndex         SecondaryIndexDriver
}

type maintenanceQueueSet [maintenancePriorityCount]list.List

type maintenanceExecutor func(
	context.Context,
	*kitdb.DB,
	MaintenanceKind,
) (uint64, error)

type maintenanceExecution struct {
	transaction            uint64
	backup                 *VerifiedBackupResult
	historyPrune           *kitdb.HistoryPruneResult
	historyRetention       *kitdb.HistoryRetentionResult
	replicaCatchUp         *kitdb.ReplicaCatchUp
	replicaFilePublish     *ReplicaFilePublishResult
	replicaFileApply       *ReplicaFileApplyResult
	replicaFileAcknowledge *ReplicaFileAcknowledgeResult
	rowMigration           *RowMigrationResult
	secondaryIndex         *SecondaryIndexResult
}

// Done closes when the shared maintenance operation finishes.
func (ticket *MaintenanceTicket) Done() <-chan struct{} {
	if ticket == nil || ticket.task == nil {
		return nil
	}
	return ticket.task.done
}

// Wait waits for completion without taking ownership of the underlying job.
func (ticket *MaintenanceTicket) Wait(ctx context.Context) (MaintenanceResult, error) {
	if ticket == nil || ticket.task == nil {
		return MaintenanceResult{}, fmt.Errorf("kitdb node: maintenance ticket is nil")
	}
	if ctx == nil {
		return MaintenanceResult{}, fmt.Errorf("kitdb node: maintenance wait context is nil")
	}
	select {
	case <-ticket.task.done:
		return ticket.task.result, ticket.task.err
	case <-ctx.Done():
		return MaintenanceResult{}, ctx.Err()
	}
}

// ScheduleCheckpoint schedules or joins the pending checkpoint for path.
func (manager *Manager) ScheduleCheckpoint(
	ctx context.Context,
	path string,
	options kitdb.OpenOptions,
	priority MaintenancePriority,
) (*MaintenanceTicket, error) {
	return manager.scheduleMaintenance(
		ctx, path, options, MaintenanceCheckpoint, "", priority, maintenancePayload{}, nil,
	)
}

// ScheduleVerify schedules or joins the pending full verification for path.
func (manager *Manager) ScheduleVerify(
	ctx context.Context,
	path string,
	options kitdb.OpenOptions,
	priority MaintenancePriority,
) (*MaintenanceTicket, error) {
	return manager.scheduleMaintenance(
		ctx, path, options, MaintenanceVerify, "", priority, maintenancePayload{}, nil,
	)
}

// RegisterRowMigrationDriver installs the one host-owned relational migration
// adapter used by this Manager. Re-registering the same stable driver ID is
// idempotent; replacement with a different driver is refused.
func (manager *Manager) RegisterRowMigrationDriver(driver RowMigrationDriver) error {
	if manager == nil {
		return ErrClosed
	}
	if driver == nil {
		return fmt.Errorf("kitdb node: row-migration driver is nil")
	}
	id := strings.TrimSpace(driver.RowMigrationDriverID())
	if id == "" || len(id) > 128 {
		return fmt.Errorf("kitdb node: row-migration driver ID must contain 1 to 128 bytes")
	}
	manager.maintenanceMu.Lock()
	defer manager.maintenanceMu.Unlock()
	if manager.maintenanceClosed || manager.ctx.Err() != nil {
		return ErrClosed
	}
	if manager.rowMigrationDriver != nil {
		if manager.rowMigrationDriverID == id {
			return nil
		}
		return fmt.Errorf(
			"%w: have %q, received %q",
			ErrRowMigrationDriverConflict, manager.rowMigrationDriverID, id,
		)
	}
	manager.rowMigrationDriver = driver
	manager.rowMigrationDriverID = id
	return nil
}

// ScheduleRowMigration schedules or joins background progress for one file.
// Each dispatch runs one driver chunk and releases its lease before the next
// chunk competes for the shared maintenance pool.
func (manager *Manager) ScheduleRowMigration(
	ctx context.Context,
	path string,
	options kitdb.OpenOptions,
) (*MaintenanceTicket, error) {
	return manager.scheduleMaintenance(
		ctx, path, options, MaintenanceRowMigration, "", MaintenanceBackground,
		maintenancePayload{}, nil,
	)
}

// RegisterSecondaryIndexDriver installs the one host-owned relational index
// adapter used by this Manager. Re-registering the same stable driver ID is
// idempotent; a different implementation cannot replace it while work may be
// queued against the old contract.
func (manager *Manager) RegisterSecondaryIndexDriver(driver SecondaryIndexDriver) error {
	if manager == nil {
		return ErrClosed
	}
	if driver == nil {
		return fmt.Errorf("kitdb node: secondary-index driver is nil")
	}
	id := strings.TrimSpace(driver.SecondaryIndexDriverID())
	if id == "" || len(id) > 128 {
		return fmt.Errorf("kitdb node: secondary-index driver ID must contain 1 to 128 bytes")
	}
	manager.maintenanceMu.Lock()
	defer manager.maintenanceMu.Unlock()
	if manager.maintenanceClosed || manager.ctx.Err() != nil {
		return ErrClosed
	}
	if manager.secondaryIndexDriver != nil {
		if manager.secondaryIndexDriverID == id {
			return nil
		}
		return fmt.Errorf(
			"%w: have %q, received %q",
			ErrSecondaryIndexDriverConflict, manager.secondaryIndexDriverID, id,
		)
	}
	manager.secondaryIndexDriver = driver
	manager.secondaryIndexDriverID = id
	return nil
}

// ScheduleSecondaryIndex schedules or joins background KIBS build/cleanup
// progress for one file. One dispatch owns one bounded chunk, then releases
// the database before any continuation returns to the background queue.
func (manager *Manager) ScheduleSecondaryIndex(
	ctx context.Context,
	path string,
	options kitdb.OpenOptions,
) (*MaintenanceTicket, error) {
	return manager.scheduleMaintenance(
		ctx, path, options, MaintenanceSecondaryIndex, "", MaintenanceBackground,
		maintenancePayload{}, nil,
	)
}

func (manager *Manager) scheduleMaintenance(
	ctx context.Context,
	path string,
	options kitdb.OpenOptions,
	kind MaintenanceKind,
	operation string,
	priority MaintenancePriority,
	payload maintenancePayload,
	additional []maintenanceResourceRequest,
) (*MaintenanceTicket, error) {
	if manager == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb node: maintenance context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validMaintenancePriority(priority) {
		return nil, fmt.Errorf("kitdb node: invalid maintenance priority %d", priority)
	}
	resources, err := manager.prepareMaintenanceResources(
		maintenanceResourceRequest{path: path, options: options},
		additional,
	)
	if err != nil {
		return nil, err
	}
	primary := resources[0]

	id := maintenanceTaskID{path: primary.key, kind: kind, operation: operation}
	manager.maintenanceMu.Lock()
	defer manager.maintenanceMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if manager.maintenanceClosed || manager.ctx.Err() != nil {
		return nil, ErrClosed
	}
	if kind == MaintenanceRowMigration {
		if manager.rowMigrationDriver == nil {
			manager.maintenanceRejections++
			return nil, ErrRowMigrationDriverUnavailable
		}
		payload.rowMigration = manager.rowMigrationDriver
	} else if kind == MaintenanceSecondaryIndex {
		if manager.secondaryIndexDriver == nil {
			manager.maintenanceRejections++
			return nil, ErrSecondaryIndexDriverUnavailable
		}
		payload.secondaryIndex = manager.secondaryIndexDriver
	}
	manager.maintenanceSubmissions++
	if current := manager.maintenanceTasks[id]; current != nil {
		if !sameMaintenanceResources(current.resources, resources) {
			manager.maintenanceRejections++
			return nil, optionsMismatchError(primary.path)
		}
		if maintenancePriorityRank(priority) > maintenancePriorityRank(current.priority) &&
			current.element != nil {
			manager.maintenanceQueues[current.priority].Remove(current.element)
			current.priority = priority
			current.element = manager.maintenanceQueues[priority].PushBack(current)
			manager.maintenanceCond.Broadcast()
		}
		manager.maintenanceCoalesced++
		return &MaintenanceTicket{task: current}, nil
	}
	for _, resource := range resources {
		if current, found := manager.maintenanceDatabaseOptions[resource.key]; found && current != resource.options {
			manager.maintenanceRejections++
			return nil, optionsMismatchError(resource.path)
		}
	}
	if manager.maintenanceQueued >= manager.limits.maxQueuedMaintenance {
		manager.maintenanceRejections++
		return nil, ErrMaintenanceQueueFull
	}
	for _, resource := range resources {
		if manager.maintenancePerDatabase[resource.key] >= manager.limits.maxMaintenancePerDatabase {
			manager.maintenanceRejections++
			return nil, fmt.Errorf(
				"%w for %q", ErrMaintenanceDatabaseQueueFull, resource.path,
			)
		}
	}

	task := &maintenanceTask{
		id: id, path: primary.path, resources: resources, kind: kind, priority: priority,
		payload: payload, queuedAt: time.Now(), done: make(chan struct{}),
	}
	manager.maintenanceTasks[id] = task
	for _, resource := range resources {
		manager.maintenancePerDatabase[resource.key]++
		manager.maintenanceDatabaseOptions[resource.key] = resource.options
	}
	task.element = manager.maintenanceQueues[priority].PushBack(task)
	manager.maintenanceQueued++
	manager.startMaintenanceLocked()
	manager.maintenanceCond.Broadcast()
	return &MaintenanceTicket{task: task}, nil
}

func (manager *Manager) prepareMaintenanceResources(
	primary maintenanceResourceRequest,
	additional []maintenanceResourceRequest,
) ([]maintenanceResource, error) {
	requests := make([]maintenanceResourceRequest, 0, 1+len(additional))
	requests = append(requests, primary)
	requests = append(requests, additional...)
	resources := make([]maintenanceResource, 0, len(requests))
	byKey := make(map[string]int, len(requests))
	totalCacheBytes := int64(0)
	databaseResources := 0
	for _, request := range requests {
		absolute, key, err := canonicalDatabasePath(request.path)
		if err != nil {
			return nil, err
		}
		options := kitdb.OpenOptions{}
		cacheBytes := int64(0)
		if !request.reserveOnly {
			options, cacheBytes, err = manager.normalizeOpenOptions(request.options)
			if err != nil {
				return nil, err
			}
		}
		if index, found := byKey[key]; found {
			if resources[index].reserveOnly != request.reserveOnly {
				return nil, fmt.Errorf(
					"kitdb node: maintenance path %q has conflicting resource roles", absolute,
				)
			}
			if !request.reserveOnly && resources[index].options != options {
				return nil, optionsMismatchError(absolute)
			}
			continue
		}
		if !request.reserveOnly {
			requiredHandles := databaseResources + 1
			requiredCacheBytes := totalCacheBytes + cacheBytes
			if requiredHandles > manager.limits.maxOpenDatabases ||
				cacheBytes > manager.limits.maxPageCacheBytes-totalCacheBytes {
				return nil, fmt.Errorf(
					"%w: operation requires %d handles and %d page-cache bytes; fleet permits %d handles and %d bytes",
					ErrMaintenanceResourceCapacity,
					requiredHandles,
					requiredCacheBytes,
					manager.limits.maxOpenDatabases,
					manager.limits.maxPageCacheBytes,
				)
			}
			databaseResources++
			totalCacheBytes = requiredCacheBytes
		}
		byKey[key] = len(resources)
		resources = append(resources, maintenanceResource{
			path: absolute, key: key, options: options, cacheBytes: cacheBytes,
			reserveOnly: request.reserveOnly,
		})
	}
	return resources, nil
}

func sameMaintenanceResources(left, right []maintenanceResource) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].key != right[index].key ||
			left[index].options != right[index].options ||
			left[index].reserveOnly != right[index].reserveOnly {
			return false
		}
	}
	return true
}

func maintenanceDatabaseResourceCount(resources []maintenanceResource) int {
	count := 0
	for _, resource := range resources {
		if !resource.reserveOnly {
			count++
		}
	}
	return count
}

func validMaintenancePriority(priority MaintenancePriority) bool {
	return priority < maintenancePriorityCount
}

func maintenancePriorityRank(priority MaintenancePriority) int {
	switch priority {
	case MaintenanceUrgent:
		return 2
	case MaintenanceNormal:
		return 1
	case MaintenanceBackground:
		return 0
	default:
		return -1
	}
}

func (manager *Manager) startMaintenanceLocked() {
	if manager.maintenanceStarted || manager.maintenanceClosed {
		return
	}
	manager.maintenanceStarted = true
	manager.maintenanceWorkers.Add(manager.limits.maxConcurrentMaintenance)
	for range manager.limits.maxConcurrentMaintenance {
		go manager.maintenanceWorker()
	}
	go func() {
		manager.maintenanceWorkers.Wait()
		close(manager.maintenanceDone)
	}()
}

func (manager *Manager) maintenanceWorker() {
	defer manager.maintenanceWorkers.Done()
	for {
		manager.maintenanceMu.Lock()
		var task *maintenanceTask
		for task == nil && !manager.maintenanceClosed {
			task = manager.nextMaintenanceTaskLocked()
			if task == nil {
				manager.maintenanceCond.Wait()
			}
		}
		if task == nil {
			manager.maintenanceMu.Unlock()
			return
		}
		manager.maintenanceMu.Unlock()

		result, err := manager.executeMaintenance(task)
		manager.finishMaintenance(task, result, err)
	}
}

func (manager *Manager) nextMaintenanceTaskLocked() *maintenanceTask {
	background := manager.firstRunnableMaintenanceLocked(MaintenanceBackground)
	urgent := manager.firstRunnableMaintenanceLocked(MaintenanceUrgent)
	normal := manager.firstRunnableMaintenanceLocked(MaintenanceNormal)
	if background == nil {
		manager.maintenanceForegroundBurst = 0
	}
	if normal == nil {
		manager.maintenanceUrgentBurst = 0
	}

	var task *maintenanceTask
	switch {
	case background != nil && manager.maintenanceForegroundBurst >= maximumForegroundMaintenanceBurst:
		task = background
		manager.maintenanceForegroundBurst = 0
	case urgent != nil && (normal == nil || manager.maintenanceUrgentBurst < maximumUrgentMaintenanceBurst):
		task = urgent
		if normal != nil {
			manager.maintenanceUrgentBurst++
		}
		if background != nil {
			manager.maintenanceForegroundBurst++
		}
	case normal != nil:
		task = normal
		manager.maintenanceUrgentBurst = 0
		if background != nil {
			manager.maintenanceForegroundBurst++
		}
	case urgent != nil:
		task = urgent
		if background != nil {
			manager.maintenanceForegroundBurst++
		}
	case background != nil:
		task = background
		manager.maintenanceForegroundBurst = 0
	default:
		return nil
	}

	queue := &manager.maintenanceQueues[task.priority]
	queue.Remove(task.element)
	task.element = nil
	manager.maintenanceQueued--
	manager.maintenanceRunning++
	for _, resource := range task.resources {
		manager.maintenanceRunningPaths[resource.key] = true
	}
	if maintenanceDatabaseResourceCount(task.resources) > 1 {
		manager.maintenanceMultiRunning = true
	}
	return task
}

func (manager *Manager) firstRunnableMaintenanceLocked(
	priority MaintenancePriority,
) *maintenanceTask {
	queue := &manager.maintenanceQueues[priority]
tasks:
	for element := queue.Front(); element != nil; element = element.Next() {
		task := element.Value.(*maintenanceTask)
		if maintenanceDatabaseResourceCount(task.resources) > 1 && manager.maintenanceMultiRunning {
			continue
		}
		for _, resource := range task.resources {
			if manager.maintenanceRunningPaths[resource.key] {
				continue tasks
			}
		}
		return task
	}
	return nil
}

func (manager *Manager) executeMaintenance(
	task *maintenanceTask,
) (MaintenanceResult, error) {
	result := MaintenanceResult{
		Kind: task.kind, Path: task.path, QueuedAt: task.queuedAt, StartedAt: time.Now(),
	}
	ctx, cancel := context.WithTimeout(manager.ctx, manager.limits.maintenanceTimeout)
	defer cancel()
	handles, err := manager.acquireMaintenanceHandles(ctx, task.resources)
	if err == nil {
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		} else {
			var execution maintenanceExecution
			primary := maintenanceDatabase(handles, task.id.path)
			if primary == nil {
				err = fmt.Errorf("kitdb node: primary maintenance handle is unavailable")
			} else {
				execution, err = manager.invokeMaintenanceExecutor(
					ctx, primary, handles, task,
				)
			}
			result.Transaction = execution.transaction
			result.Backup = execution.backup
			result.HistoryPrune = execution.historyPrune
			result.HistoryRetention = execution.historyRetention
			result.ReplicaCatchUp = execution.replicaCatchUp
			result.ReplicaFilePublish = execution.replicaFilePublish
			result.ReplicaFileApply = execution.replicaFileApply
			result.ReplicaFileAcknowledge = execution.replicaFileAcknowledge
			result.RowMigration = execution.rowMigration
			result.SecondaryIndex = execution.secondaryIndex
		}
		err = errors.Join(err, releaseMaintenanceHandles(handles))
	}
	result.FinishedAt = time.Now()
	result.Duration = result.FinishedAt.Sub(result.StartedAt)
	return result, err
}

func (manager *Manager) acquireMaintenanceHandles(
	ctx context.Context,
	resources []maintenanceResource,
) ([]maintenanceHandle, error) {
	ordered := append([]maintenanceResource(nil), resources...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].key < ordered[right].key
	})
	handles := make([]maintenanceHandle, 0, len(ordered))
	for _, resource := range ordered {
		if resource.reserveOnly {
			continue
		}
		lease, err := manager.Acquire(ctx, resource.path, resource.options)
		if err != nil {
			return nil, errors.Join(err, releaseMaintenanceHandles(handles))
		}
		handles = append(handles, maintenanceHandle{resource: resource, lease: lease})
	}
	return handles, nil
}

func maintenanceDatabase(handles []maintenanceHandle, key string) *kitdb.DB {
	for _, handle := range handles {
		if handle.resource.key == key {
			return handle.lease.DB()
		}
	}
	return nil
}

func maintenanceDatabaseGate(handles []maintenanceHandle, key string) *DatabaseGate {
	for _, handle := range handles {
		if handle.resource.key == key {
			return handle.lease.Gate()
		}
	}
	return nil
}

func releaseMaintenanceHandles(handles []maintenanceHandle) error {
	var releaseErr error
	for index := len(handles) - 1; index >= 0; index-- {
		releaseErr = errors.Join(releaseErr, handles[index].lease.Release())
	}
	return releaseErr
}

func (manager *Manager) invokeMaintenanceExecutor(
	ctx context.Context,
	database *kitdb.DB,
	handles []maintenanceHandle,
	task *maintenanceTask,
) (execution maintenanceExecution, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			execution = maintenanceExecution{}
			err = fmt.Errorf("kitdb node: %s maintenance panicked: %v", task.kind, recovered)
		}
	}()
	switch task.kind {
	case MaintenanceBackup:
		backup, backupErr := runVerifiedBackup(ctx, database, task.payload.backup)
		if backup != nil {
			execution.transaction = backup.Anchor.Transaction
			execution.backup = backup
		}
		return execution, backupErr
	case MaintenanceHistoryPrune:
		prune, pruneErr := runHistoryPrune(ctx, database, task.payload.historyPrune)
		execution.transaction = prune.BaseTransaction
		execution.historyPrune = &prune
		return execution, pruneErr
	case MaintenanceHistoryRetention:
		retention, retentionErr := runHistoryRetention(
			ctx, database, task.payload.historyRetention,
		)
		execution.transaction = retention.AppliedThrough
		execution.historyRetention = &retention
		return execution, retentionErr
	case MaintenanceReplicaCatchUp:
		operation := task.payload.replicaCatchUp
		targetKey := ""
		if operation != nil {
			targetKey = operation.targetKey
		}
		target := maintenanceDatabase(handles, targetKey)
		catchUp, catchUpErr := runReplicaCatchUp(
			ctx, database, target, operation,
		)
		execution.transaction = catchUp.To.Transaction
		execution.replicaCatchUp = &catchUp
		return execution, catchUpErr
	case MaintenanceReplicaFilePublish:
		published, publishErr := runReplicaFilePublish(
			ctx, database, task.payload.replicaFilePublish,
		)
		execution.transaction = published.transaction()
		execution.replicaFilePublish = &published
		return execution, publishErr
	case MaintenanceReplicaFileApply:
		applied, applyErr := runReplicaFileApply(
			ctx, database, task.payload.replicaFileApply,
		)
		execution.transaction = applied.transaction()
		execution.replicaFileApply = &applied
		return execution, applyErr
	case MaintenanceReplicaFileAcknowledge:
		acknowledged, acknowledgeErr := runReplicaFileAcknowledge(
			ctx, database, task.payload.replicaFileAcknowledge,
		)
		execution.transaction = acknowledged.transaction()
		execution.replicaFileAcknowledge = &acknowledged
		return execution, acknowledgeErr
	case MaintenanceRowMigration:
		if task.payload.rowMigration == nil {
			return execution, ErrRowMigrationDriverUnavailable
		}
		gate := maintenanceDatabaseGate(handles, task.id.path)
		if gate == nil {
			return execution, fmt.Errorf("kitdb node: row-migration database gate is unavailable")
		}
		if err := ctx.Err(); err != nil {
			return execution, err
		}
		progress, migrationErr := advanceRowMigrationWithGate(
			ctx, gate, task.payload.rowMigration, database,
		)
		if migrationErr == nil && progress.Pending && !progress.Advanced {
			migrationErr = fmt.Errorf(
				"kitdb node: row-migration driver reported pending work without durable progress",
			)
		}
		execution.transaction = progress.Transaction
		execution.rowMigration = &RowMigrationResult{
			Chunks: 1, AdvancedChunks: boolCount(progress.Advanced), Pending: progress.Pending,
		}
		return execution, migrationErr
	case MaintenanceSecondaryIndex:
		if task.payload.secondaryIndex == nil {
			return execution, ErrSecondaryIndexDriverUnavailable
		}
		gate := maintenanceDatabaseGate(handles, task.id.path)
		if gate == nil {
			return execution, fmt.Errorf("kitdb node: secondary-index database gate is unavailable")
		}
		if err := ctx.Err(); err != nil {
			return execution, err
		}
		progress, indexErr := advanceSecondaryIndexWithGate(
			ctx, gate, task.payload.secondaryIndex, database,
		)
		if indexErr == nil && progress.Pending && !progress.Advanced {
			indexErr = fmt.Errorf(
				"kitdb node: secondary-index driver reported pending work without durable progress",
			)
		}
		execution.transaction = progress.Transaction
		execution.secondaryIndex = &SecondaryIndexResult{
			Chunks: 1, AdvancedChunks: boolCount(progress.Advanced), Pending: progress.Pending,
		}
		return execution, indexErr
	}
	transaction, err := manager.maintenanceExecutor(ctx, database, task.kind)
	execution.transaction = transaction
	if err == nil && task.kind == MaintenanceCheckpoint {
		policy := maintenancePrimaryOptions(task).HistoryRetention
		if policy.Enabled() {
			retention, retentionErr := database.EnforceHistoryRetention(ctx, policy, time.Now())
			execution.historyRetention = &retention
			err = retentionErr
		}
	}
	return execution, err
}

func maintenancePrimaryOptions(task *maintenanceTask) kitdb.OpenOptions {
	if task == nil {
		return kitdb.OpenOptions{}
	}
	for _, resource := range task.resources {
		if resource.key == task.id.path {
			return resource.options
		}
	}
	return kitdb.OpenOptions{}
}

func advanceRowMigrationWithGate(
	ctx context.Context,
	gate *DatabaseGate,
	driver RowMigrationDriver,
	database *kitdb.DB,
) (RowMigrationProgress, error) {
	gate.Lock()
	defer gate.Unlock()
	return driver.AdvanceRowMigration(ctx, database)
}

func advanceSecondaryIndexWithGate(
	ctx context.Context,
	gate *DatabaseGate,
	driver SecondaryIndexDriver,
	database *kitdb.DB,
) (SecondaryIndexProgress, error) {
	gate.Lock()
	defer gate.Unlock()
	return driver.AdvanceSecondaryIndex(ctx, database)
}

func boolCount(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func runMaintenanceOperation(
	ctx context.Context,
	database *kitdb.DB,
	kind MaintenanceKind,
) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	switch kind {
	case MaintenanceCheckpoint:
		return database.Checkpoint()
	case MaintenanceVerify:
		return 0, database.VerifyContext(ctx)
	default:
		return 0, fmt.Errorf("kitdb node: unsupported maintenance operation %q", kind)
	}
}

func (manager *Manager) finishMaintenance(
	task *maintenanceTask,
	result MaintenanceResult,
	err error,
) {
	manager.maintenanceMu.Lock()
	manager.maintenanceRunning--
	for _, resource := range task.resources {
		delete(manager.maintenanceRunningPaths, resource.key)
	}
	if maintenanceDatabaseResourceCount(task.resources) > 1 {
		manager.maintenanceMultiRunning = false
	}
	if task.startedAt.IsZero() {
		task.startedAt = result.StartedAt
	}
	task.duration += result.Duration
	if result.RowMigration != nil {
		task.rowMigrationChunks += result.RowMigration.Chunks
		task.rowMigrationAdvancedChunks += result.RowMigration.AdvancedChunks
		manager.rowMigrationChunks += result.RowMigration.Chunks
		manager.rowMigrationAdvancedChunks += result.RowMigration.AdvancedChunks
		result.RowMigration.Chunks = task.rowMigrationChunks
		result.RowMigration.AdvancedChunks = task.rowMigrationAdvancedChunks
	}
	if result.SecondaryIndex != nil {
		task.secondaryIndexChunks += result.SecondaryIndex.Chunks
		task.secondaryIndexAdvancedChunks += result.SecondaryIndex.AdvancedChunks
		manager.secondaryIndexChunks += result.SecondaryIndex.Chunks
		manager.secondaryIndexAdvancedChunks += result.SecondaryIndex.AdvancedChunks
		result.SecondaryIndex.Chunks = task.secondaryIndexChunks
		result.SecondaryIndex.AdvancedChunks = task.secondaryIndexAdvancedChunks
	}
	continues := result.RowMigration != nil && result.RowMigration.Pending ||
		result.SecondaryIndex != nil && result.SecondaryIndex.Pending
	if err == nil && continues {
		if manager.maintenanceClosed || manager.ctx.Err() != nil {
			err = ErrClosed
		} else {
			task.element = manager.maintenanceQueues[task.priority].PushBack(task)
			manager.maintenanceQueued++
			manager.maintenanceCond.Broadcast()
			manager.maintenanceMu.Unlock()
			return
		}
	}
	result.StartedAt = task.startedAt
	result.Duration = task.duration
	manager.completeMaintenanceLocked(task, result, err)
	manager.maintenanceCond.Broadcast()
	manager.maintenanceMu.Unlock()
}

func (manager *Manager) completeMaintenanceLocked(
	task *maintenanceTask,
	result MaintenanceResult,
	err error,
) {
	delete(manager.maintenanceTasks, task.id)
	for _, resource := range task.resources {
		remaining := manager.maintenancePerDatabase[resource.key] - 1
		if remaining == 0 {
			delete(manager.maintenancePerDatabase, resource.key)
			delete(manager.maintenanceDatabaseOptions, resource.key)
		} else {
			manager.maintenancePerDatabase[resource.key] = remaining
		}
	}
	task.result = result
	task.err = err
	manager.maintenanceCompletions++
	if result.HistoryPrune != nil {
		manager.historyPrunedSegments += uint64(result.HistoryPrune.PrunedSegments)
		manager.historyPrunedBytes += result.HistoryPrune.PrunedBytes
	}
	if result.HistoryRetention != nil {
		manager.historyRetentionEvaluations++
		manager.historyPrunedSegments += uint64(result.HistoryRetention.Prune.PrunedSegments)
		manager.historyPrunedBytes += result.HistoryRetention.Prune.PrunedBytes
		if result.HistoryRetention.LimitedByPin {
			manager.historyRetentionLimitedByPins++
		}
		manager.historyRetentionBytesOverLimit += result.HistoryRetention.BytesOverLimit
	}
	if result.ReplicaCatchUp != nil {
		manager.replicaTransactionsApplied += result.ReplicaCatchUp.AppliedTransactions
	}
	manager.observeReplicaFileResultLocked(result, err)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, ErrClosed) {
			manager.maintenanceCancellations++
		} else {
			manager.maintenanceFailures++
		}
	} else {
		switch task.kind {
		case MaintenanceCheckpoint:
			manager.checkpointCompletions++
		case MaintenanceVerify:
			manager.verificationCompletions++
		case MaintenanceBackup:
			manager.backupCompletions++
			if result.Backup != nil && result.Backup.Resumed {
				manager.backupResumptions++
			}
		case MaintenanceHistoryPrune:
			manager.historyPruneCompletions++
		case MaintenanceHistoryRetention:
			manager.historyRetentionCompletions++
		case MaintenanceReplicaCatchUp:
			manager.replicaCatchUpCompletions++
			if result.ReplicaCatchUp != nil &&
				result.ReplicaCatchUp.AppliedTransactions == 0 {
				manager.replicaCatchUpNoops++
			}
		case MaintenanceReplicaFilePublish:
			manager.replicaFilePublishCompletions++
		case MaintenanceReplicaFileApply:
			manager.replicaFileApplyCompletions++
		case MaintenanceReplicaFileAcknowledge:
			manager.replicaFileAcknowledgeCompletions++
		case MaintenanceRowMigration:
			manager.rowMigrationCompletions++
		case MaintenanceSecondaryIndex:
			manager.secondaryIndexCompletions++
		}
	}
	manager.maintenanceTotalDuration += result.Duration
	manager.maintenanceLongestDuration = max(manager.maintenanceLongestDuration, result.Duration)
	close(task.done)
}

func (manager *Manager) beginMaintenanceClose() {
	manager.maintenanceMu.Lock()
	if manager.maintenanceClosed {
		manager.maintenanceMu.Unlock()
		return
	}
	manager.maintenanceClosed = true
	now := time.Now()
	for priority := MaintenancePriority(0); priority < maintenancePriorityCount; priority++ {
		queue := &manager.maintenanceQueues[priority]
		for element := queue.Front(); element != nil; {
			next := element.Next()
			task := element.Value.(*maintenanceTask)
			queue.Remove(element)
			task.element = nil
			manager.maintenanceQueued--
			manager.completeMaintenanceLocked(task, MaintenanceResult{
				Kind: task.kind, Path: task.path, QueuedAt: task.queuedAt, FinishedAt: now,
			}, ErrClosed)
			element = next
		}
	}
	manager.maintenanceCond.Broadcast()
	if !manager.maintenanceStarted {
		close(manager.maintenanceDone)
	}
	manager.maintenanceMu.Unlock()
}

func (manager *Manager) waitForMaintenanceClose(ctx context.Context) error {
	select {
	case <-manager.maintenanceDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (manager *Manager) addMaintenanceStats(stats *Stats) {
	manager.maintenanceMu.Lock()
	defer manager.maintenanceMu.Unlock()
	stats.QueuedMaintenance = manager.maintenanceQueued
	stats.RunningMaintenance = manager.maintenanceRunning
	stats.MultiDatabaseMaintenanceRunning = manager.maintenanceMultiRunning
	stats.MaxConcurrentMaintenance = manager.limits.maxConcurrentMaintenance
	stats.MaxQueuedMaintenance = manager.limits.maxQueuedMaintenance
	stats.MaxMaintenancePerDatabase = manager.limits.maxMaintenancePerDatabase
	stats.MaintenanceTimeout = manager.limits.maintenanceTimeout
	stats.MaintenanceSubmissions = manager.maintenanceSubmissions
	stats.MaintenanceCoalesced = manager.maintenanceCoalesced
	stats.MaintenanceRejections = manager.maintenanceRejections
	stats.MaintenanceCompletions = manager.maintenanceCompletions
	stats.MaintenanceFailures = manager.maintenanceFailures
	stats.MaintenanceCancellations = manager.maintenanceCancellations
	stats.CheckpointCompletions = manager.checkpointCompletions
	stats.VerificationCompletions = manager.verificationCompletions
	stats.BackupCompletions = manager.backupCompletions
	stats.BackupResumptions = manager.backupResumptions
	stats.HistoryPruneCompletions = manager.historyPruneCompletions
	stats.HistoryRetentionCompletions = manager.historyRetentionCompletions
	stats.HistoryRetentionEvaluations = manager.historyRetentionEvaluations
	stats.HistoryRetentionLimitedByPins = manager.historyRetentionLimitedByPins
	stats.HistoryRetentionBytesOverLimit = manager.historyRetentionBytesOverLimit
	stats.HistoryPrunedSegments = manager.historyPrunedSegments
	stats.HistoryPrunedBytes = manager.historyPrunedBytes
	stats.ReplicaCatchUpCompletions = manager.replicaCatchUpCompletions
	stats.ReplicaTransactionsApplied = manager.replicaTransactionsApplied
	stats.ReplicaCatchUpNoops = manager.replicaCatchUpNoops
	stats.ReplicaFilePublishCompletions = manager.replicaFilePublishCompletions
	stats.ReplicaFileApplyCompletions = manager.replicaFileApplyCompletions
	stats.ReplicaFileAcknowledgeCompletions = manager.replicaFileAcknowledgeCompletions
	stats.RowMigrationCompletions = manager.rowMigrationCompletions
	stats.RowMigrationChunks = manager.rowMigrationChunks
	stats.RowMigrationAdvancedChunks = manager.rowMigrationAdvancedChunks
	stats.SecondaryIndexCompletions = manager.secondaryIndexCompletions
	stats.SecondaryIndexChunks = manager.secondaryIndexChunks
	stats.SecondaryIndexAdvancedChunks = manager.secondaryIndexAdvancedChunks
	stats.ReplicaFileBatchesPublished = manager.replicaFileBatchesPublished
	stats.ReplicaFileTransactionsPublished = manager.replicaFileTransactionsPublished
	stats.ReplicaFileTransactionsApplied = manager.replicaFileTransactionsApplied
	stats.ReplicaFileAcknowledgements = manager.replicaFileAcknowledgements
	stats.ReplicaFileNoops = manager.replicaFileNoops
	stats.ReplicaFilePeakPendingBatches = manager.replicaFilePeakPendingBatches
	stats.ReplicaFilePeakPendingBytes = manager.replicaFilePeakPendingBytes
	stats.ReplicaFileMaxLagTransactions = manager.replicaFileMaxLagTransactions
	stats.MaintenanceTotalDuration = manager.maintenanceTotalDuration
	stats.MaintenanceLongestDuration = manager.maintenanceLongestDuration
}
