package node

import (
	"context"
	"errors"
	"fmt"

	"github.com/kitwork/engine/kitdb"
)

var (
	ErrReplicaFileHistoryRequired = errors.New(
		"kitdb node: filesystem replica requires retained source history",
	)
	ErrReplicaFileSamePath = errors.New(
		"kitdb node: filesystem replica database and mailbox paths match",
	)
	ErrReplicaFilePinRequired = errors.New(
		"kitdb node: filesystem replica source pin does not exist",
	)
)

// ReplicaFilePublishRequest describes one bounded source-to-mailbox step. The
// source pin is the only authoritative sender cursor. Managed publication keeps
// at most one batch in flight so restart requires no second cursor file.
type ReplicaFilePublishRequest struct {
	Source          string
	Mailbox         string
	SourceOptions   kitdb.OpenOptions
	PinName         string
	BatchLimits     kitdb.ReplicaBatchLimits
	TransportLimits kitdb.ReplicaFileTransportLimits
	Priority        MaintenancePriority
}

// ReplicaFileApplyRequest describes one mailbox-to-target step. TargetOptions
// are forced into replica mode while preserving cache and history settings.
type ReplicaFileApplyRequest struct {
	Target          string
	Mailbox         string
	TargetOptions   kitdb.OpenOptions
	TransportLimits kitdb.ReplicaFileTransportLimits
	Priority        MaintenancePriority
}

// ReplicaFileAcknowledgeRequest describes one ACK-to-source-pin step.
type ReplicaFileAcknowledgeRequest struct {
	Source          string
	Mailbox         string
	SourceOptions   kitdb.OpenOptions
	PinName         string
	TransportLimits kitdb.ReplicaFileTransportLimits
	Priority        MaintenancePriority
}

// ReplicaFilePublishResult reports a bounded publication decision without
// retaining transaction bodies in the manager or ticket.
type ReplicaFilePublishResult struct {
	Mailbox               string
	Pin                   kitdb.HistoryPin
	SourceBoundary        kitdb.HistoryCursor
	Publication           *kitdb.ReplicaFilePublication
	MailboxStats          kitdb.ReplicaFileTransportStats
	Published             bool
	Pending               bool
	NoChanges             bool
	LagTransactions       uint64
	RemainingTransactions uint64
}

// ReplicaFileApplyResult reports target WAL progress for at most one batch.
type ReplicaFileApplyResult struct {
	Mailbox      string
	Target       kitdb.HistoryCursor
	Apply        *kitdb.ReplicaFileApply
	MailboxStats kitdb.ReplicaFileTransportStats
	Found        bool
}

// ReplicaFileAcknowledgeResult reports source-pin and mailbox cleanup progress
// for at most one acknowledgement.
type ReplicaFileAcknowledgeResult struct {
	Mailbox      string
	Pin          kitdb.HistoryPin
	Acknowledge  *kitdb.ReplicaFileAcknowledge
	MailboxStats kitdb.ReplicaFileTransportStats
	Found        bool
}

type replicaFilePublishMaintenance struct {
	mailbox         string
	pinName         string
	batchLimits     kitdb.ReplicaBatchLimits
	transportLimits kitdb.ReplicaFileTransportLimits
}

type replicaFileApplyMaintenance struct {
	mailbox         string
	transportLimits kitdb.ReplicaFileTransportLimits
}

type replicaFileAcknowledgeMaintenance struct {
	mailbox         string
	pinName         string
	transportLimits kitdb.ReplicaFileTransportLimits
}

// ScheduleReplicaFilePublish schedules or joins one exact source, mailbox,
// pin, and limit policy. A pending batch or ACK is backpressure, not another
// publication, which keeps managed restart state entirely in protocol files.
func (manager *Manager) ScheduleReplicaFilePublish(
	ctx context.Context,
	request ReplicaFilePublishRequest,
) (*MaintenanceTicket, error) {
	if manager == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb node: replica file publish context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !request.SourceOptions.RetainHistory {
		return nil, ErrReplicaFileHistoryRequired
	}
	if err := kitdb.ValidateHistoryPinName(request.PinName); err != nil {
		return nil, err
	}
	if err := kitdb.ValidateReplicaBatchLimits(request.BatchLimits); err != nil {
		return nil, err
	}
	if err := kitdb.ValidateReplicaFileTransportLimits(request.TransportLimits); err != nil {
		return nil, err
	}
	source, sourceKey, err := canonicalDatabasePath(request.Source)
	if err != nil {
		return nil, err
	}
	mailbox, mailboxKey, err := canonicalDatabasePath(request.Mailbox)
	if err != nil {
		return nil, fmt.Errorf("kitdb node: resolve replica mailbox: %w", err)
	}
	if sourceKey == mailboxKey {
		return nil, ErrReplicaFileSamePath
	}
	if err := requireReplicaDatabasePath(source, "source"); err != nil {
		return nil, err
	}
	batchLimits := normalizeReplicaBatchLimitsForOperation(request.BatchLimits)
	transportLimits := normalizeReplicaFileLimitsForOperation(request.TransportLimits)
	operation := replicaFileOperation(
		mailboxKey, request.PinName, batchLimits, transportLimits,
	)
	return manager.scheduleMaintenance(
		ctx, source, request.SourceOptions, MaintenanceReplicaFilePublish,
		operation, request.Priority,
		maintenancePayload{replicaFilePublish: &replicaFilePublishMaintenance{
			mailbox: mailbox, pinName: request.PinName,
			batchLimits: batchLimits, transportLimits: transportLimits,
		}},
		[]maintenanceResourceRequest{{path: mailbox, reserveOnly: true}},
	)
}

// ScheduleReplicaFileApply schedules or joins one exact target, mailbox, and
// limit policy. At most one mailbox batch is applied by each completed job.
func (manager *Manager) ScheduleReplicaFileApply(
	ctx context.Context,
	request ReplicaFileApplyRequest,
) (*MaintenanceTicket, error) {
	if manager == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb node: replica file apply context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := kitdb.ValidateReplicaFileTransportLimits(request.TransportLimits); err != nil {
		return nil, err
	}
	target, targetKey, err := canonicalDatabasePath(request.Target)
	if err != nil {
		return nil, err
	}
	mailbox, mailboxKey, err := canonicalDatabasePath(request.Mailbox)
	if err != nil {
		return nil, fmt.Errorf("kitdb node: resolve replica mailbox: %w", err)
	}
	if targetKey == mailboxKey {
		return nil, ErrReplicaFileSamePath
	}
	if err := requireReplicaDatabasePath(target, "target"); err != nil {
		return nil, err
	}
	request.TargetOptions.Replica = true
	transportLimits := normalizeReplicaFileLimitsForOperation(request.TransportLimits)
	operation := replicaFileOperation(
		mailboxKey, "", kitdb.ReplicaBatchLimits{}, transportLimits,
	)
	return manager.scheduleMaintenance(
		ctx, target, request.TargetOptions, MaintenanceReplicaFileApply,
		operation, request.Priority,
		maintenancePayload{replicaFileApply: &replicaFileApplyMaintenance{
			mailbox: mailbox, transportLimits: transportLimits,
		}},
		[]maintenanceResourceRequest{{path: mailbox, reserveOnly: true}},
	)
}

// ScheduleReplicaFileAcknowledge schedules or joins one exact source,
// mailbox, pin, and limit policy. At most one ACK is consumed by each job.
func (manager *Manager) ScheduleReplicaFileAcknowledge(
	ctx context.Context,
	request ReplicaFileAcknowledgeRequest,
) (*MaintenanceTicket, error) {
	if manager == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb node: replica file acknowledge context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !request.SourceOptions.RetainHistory {
		return nil, ErrReplicaFileHistoryRequired
	}
	if err := kitdb.ValidateHistoryPinName(request.PinName); err != nil {
		return nil, err
	}
	if err := kitdb.ValidateReplicaFileTransportLimits(request.TransportLimits); err != nil {
		return nil, err
	}
	source, sourceKey, err := canonicalDatabasePath(request.Source)
	if err != nil {
		return nil, err
	}
	mailbox, mailboxKey, err := canonicalDatabasePath(request.Mailbox)
	if err != nil {
		return nil, fmt.Errorf("kitdb node: resolve replica mailbox: %w", err)
	}
	if sourceKey == mailboxKey {
		return nil, ErrReplicaFileSamePath
	}
	if err := requireReplicaDatabasePath(source, "source"); err != nil {
		return nil, err
	}
	transportLimits := normalizeReplicaFileLimitsForOperation(request.TransportLimits)
	operation := replicaFileOperation(
		mailboxKey, request.PinName, kitdb.ReplicaBatchLimits{}, transportLimits,
	)
	return manager.scheduleMaintenance(
		ctx, source, request.SourceOptions, MaintenanceReplicaFileAcknowledge,
		operation, request.Priority,
		maintenancePayload{replicaFileAcknowledge: &replicaFileAcknowledgeMaintenance{
			mailbox: mailbox, pinName: request.PinName, transportLimits: transportLimits,
		}},
		[]maintenanceResourceRequest{{path: mailbox, reserveOnly: true}},
	)
}

func runReplicaFilePublish(
	ctx context.Context,
	source *kitdb.DB,
	operation *replicaFilePublishMaintenance,
) (ReplicaFilePublishResult, error) {
	var result ReplicaFilePublishResult
	if operation == nil {
		return result, fmt.Errorf("kitdb node: replica file publish payload is unavailable")
	}
	result.Mailbox = operation.mailbox
	pin, err := findReplicaFilePin(source, operation.pinName)
	result.Pin = pin
	if err != nil {
		return result, err
	}
	transport, err := openManagedReplicaFileTransport(
		ctx, operation.mailbox, operation.transportLimits,
	)
	if err != nil {
		return result, err
	}
	stats, err := transport.Stats(ctx)
	result.MailboxStats = stats
	if err != nil {
		return result, err
	}
	if stats.PendingBatches != 0 || stats.PendingAcknowledgements != 0 {
		result.Pending = true
		current, cursorErr := source.CurrentCursor()
		result.SourceBoundary = current
		result.LagTransactions = transactionDistance(current, pin.Cursor)
		result.RemainingTransactions = result.LagTransactions
		return result, cursorErr
	}

	batch, err := source.ReadReplicaBatch(ctx, kitdb.ReplicaBatchRequest{
		From: pin.Cursor, Limits: operation.batchLimits,
	})
	result.SourceBoundary = batch.SourceBoundary
	result.LagTransactions = transactionDistance(batch.SourceBoundary, pin.Cursor)
	if err != nil {
		return result, err
	}
	if len(batch.Transactions) == 0 {
		result.NoChanges = true
		return result, nil
	}
	publication, publishErr := transport.PublishBatch(ctx, batch)
	result.Publication = &publication
	result.Published = publishErr == nil
	result.RemainingTransactions = transactionDistance(batch.SourceBoundary, batch.To)
	stats, statsErr := transport.Stats(ctx)
	result.MailboxStats = stats
	return result, errors.Join(publishErr, statsErr)
}

func runReplicaFileApply(
	ctx context.Context,
	target *kitdb.DB,
	operation *replicaFileApplyMaintenance,
) (ReplicaFileApplyResult, error) {
	var result ReplicaFileApplyResult
	if operation == nil {
		return result, fmt.Errorf("kitdb node: replica file apply payload is unavailable")
	}
	result.Mailbox = operation.mailbox
	current, err := target.CurrentCursor()
	result.Target = current
	if err != nil {
		return result, err
	}
	transport, err := openManagedReplicaFileTransport(
		ctx, operation.mailbox, operation.transportLimits,
	)
	if err != nil {
		return result, err
	}
	applied, found, applyErr := transport.ApplyNext(ctx, target)
	result.Found = found
	if found {
		result.Apply = &applied
		if applied.To.DatabaseID != "" {
			result.Target = applied.To
		}
	}
	stats, statsErr := transport.Stats(ctx)
	result.MailboxStats = stats
	return result, errors.Join(applyErr, statsErr)
}

func runReplicaFileAcknowledge(
	ctx context.Context,
	source *kitdb.DB,
	operation *replicaFileAcknowledgeMaintenance,
) (ReplicaFileAcknowledgeResult, error) {
	var result ReplicaFileAcknowledgeResult
	if operation == nil {
		return result, fmt.Errorf("kitdb node: replica file acknowledge payload is unavailable")
	}
	result.Mailbox = operation.mailbox
	pin, err := findReplicaFilePin(source, operation.pinName)
	result.Pin = pin
	if err != nil {
		return result, err
	}
	transport, err := openManagedReplicaFileTransport(
		ctx, operation.mailbox, operation.transportLimits,
	)
	if err != nil {
		return result, err
	}
	acknowledged, found, acknowledgeErr := transport.AcknowledgeNext(
		ctx, source, operation.pinName,
	)
	result.Found = found
	if found {
		result.Acknowledge = &acknowledged
		if acknowledged.Pin.Name != "" {
			result.Pin = acknowledged.Pin
		}
	}
	stats, statsErr := transport.Stats(ctx)
	result.MailboxStats = stats
	return result, errors.Join(acknowledgeErr, statsErr)
}

func openManagedReplicaFileTransport(
	ctx context.Context,
	mailbox string,
	limits kitdb.ReplicaFileTransportLimits,
) (*kitdb.ReplicaFileTransport, error) {
	transport, err := kitdb.OpenReplicaFileTransport(mailbox, limits)
	if err != nil {
		return nil, err
	}
	if _, err := transport.CleanupStaging(ctx); err != nil {
		return nil, err
	}
	return transport, nil
}

func findReplicaFilePin(database *kitdb.DB, name string) (kitdb.HistoryPin, error) {
	pins, err := database.HistoryPins()
	if err != nil {
		return kitdb.HistoryPin{}, err
	}
	for _, pin := range pins {
		if pin.Name == name {
			return pin, nil
		}
	}
	return kitdb.HistoryPin{}, errors.Join(
		ErrReplicaFilePinRequired,
		fmt.Errorf("kitdb node: replica source pin %q does not exist", name),
	)
}

func normalizeReplicaBatchLimitsForOperation(
	limits kitdb.ReplicaBatchLimits,
) kitdb.ReplicaBatchLimits {
	if limits.MaxTransactions == 0 {
		limits.MaxTransactions = kitdb.DefaultReplicaBatchTransactions
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = kitdb.DefaultReplicaBatchBytes
	}
	return limits
}

func normalizeReplicaFileLimitsForOperation(
	limits kitdb.ReplicaFileTransportLimits,
) kitdb.ReplicaFileTransportLimits {
	if limits.MaxPendingBatches == 0 {
		limits.MaxPendingBatches = kitdb.DefaultReplicaFilePendingBatches
	}
	if limits.MaxPendingBytes == 0 {
		limits.MaxPendingBytes = kitdb.DefaultReplicaFilePendingBytes
	}
	return limits
}

func replicaFileOperation(
	mailboxKey string,
	pinName string,
	batch kitdb.ReplicaBatchLimits,
	transport kitdb.ReplicaFileTransportLimits,
) string {
	return fmt.Sprintf(
		"%s\x00%s\x00%d\x00%d\x00%d\x00%d",
		mailboxKey, pinName, batch.MaxTransactions, batch.MaxBytes,
		transport.MaxPendingBatches, transport.MaxPendingBytes,
	)
}

func transactionDistance(newer, older kitdb.HistoryCursor) uint64 {
	if newer.DatabaseID != older.DatabaseID || newer.Transaction <= older.Transaction {
		return 0
	}
	return newer.Transaction - older.Transaction
}

func (result ReplicaFilePublishResult) transaction() uint64 {
	if result.Publication != nil &&
		(result.Published || result.Publication.AlreadyPublished) {
		return result.Publication.To.Transaction
	}
	return result.Pin.Cursor.Transaction
}

func (result ReplicaFileApplyResult) transaction() uint64 {
	return result.Target.Transaction
}

func (result ReplicaFileAcknowledgeResult) transaction() uint64 {
	return result.Pin.Cursor.Transaction
}

func (manager *Manager) observeReplicaFileResultLocked(
	result MaintenanceResult,
	operationErr error,
) {
	if published := result.ReplicaFilePublish; published != nil {
		manager.observeReplicaMailboxStatsLocked(published.MailboxStats)
		manager.replicaFileMaxLagTransactions = max(
			manager.replicaFileMaxLagTransactions, published.LagTransactions,
		)
		if operationErr == nil {
			if published.Published {
				if published.Publication != nil && !published.Publication.AlreadyPublished {
					manager.replicaFileBatchesPublished++
					manager.replicaFileTransactionsPublished += uint64(
						published.Publication.Transactions,
					)
				}
			} else {
				manager.replicaFileNoops++
			}
		}
	}
	if applied := result.ReplicaFileApply; applied != nil {
		manager.observeReplicaMailboxStatsLocked(applied.MailboxStats)
		if applied.Apply != nil {
			manager.replicaFileTransactionsApplied += applied.Apply.AppliedTransactions
		}
		if operationErr == nil && !applied.Found {
			manager.replicaFileNoops++
		}
	}
	if acknowledged := result.ReplicaFileAcknowledge; acknowledged != nil {
		manager.observeReplicaMailboxStatsLocked(acknowledged.MailboxStats)
		if operationErr == nil {
			if acknowledged.Found {
				manager.replicaFileAcknowledgements++
			} else {
				manager.replicaFileNoops++
			}
		}
	}
}

func (manager *Manager) observeReplicaMailboxStatsLocked(
	stats kitdb.ReplicaFileTransportStats,
) {
	manager.replicaFilePeakPendingBatches = max(
		manager.replicaFilePeakPendingBatches, stats.PendingBatches,
	)
	manager.replicaFilePeakPendingBytes = max(
		manager.replicaFilePeakPendingBytes, stats.PendingBytes,
	)
}
