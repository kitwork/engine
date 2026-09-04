package kitdb

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
)

const (
	replicaBootstrapPinAttempts = 4

	// ReplicaProtocolVersion is the in-memory transport contract version. It is
	// independent of the main-file, WAL, and retained-history format versions.
	ReplicaProtocolVersion uint16 = 1

	// DefaultReplicaBatchTransactions is the default transaction-count ceiling.
	DefaultReplicaBatchTransactions uint32 = 1024
	// MaxReplicaBatchTransactions is the hard protocol transaction-count ceiling.
	MaxReplicaBatchTransactions uint32 = 4096
	// DefaultReplicaBatchBytes admits any one legal KitDB transaction.
	DefaultReplicaBatchBytes uint64 = frameHeaderSize + maxPayloadSize + frameTrailerSize
	// MaxReplicaBatchBytes is the hard protocol payload-memory ceiling.
	MaxReplicaBatchBytes uint64 = DefaultReplicaBatchBytes
)

// ReplicaBatchLimits bounds one transport unit before any target mutation.
// Zero values select the defaults. The byte count uses exact WAL-frame sizing,
// including operation headers, so every legal transaction remains measurable.
type ReplicaBatchLimits struct {
	MaxTransactions uint32
	MaxBytes        uint64
}

// ValidateReplicaBatchLimits validates public batch policy without reading or
// mutating a database. Zero values select the documented defaults.
func ValidateReplicaBatchLimits(limits ReplicaBatchLimits) error {
	_, err := normalizeReplicaBatchLimits(limits)
	return err
}

// ReplicaBatchRequest asks the source for transactions after From. A zero
// SourceBoundary fixes a new checkpoint boundary; subsequent requests should
// carry the first batch's boundary so a catch-up cannot chase new commits.
type ReplicaBatchRequest struct {
	From           HistoryCursor
	SourceBoundary HistoryCursor
	Limits         ReplicaBatchLimits
}

// ReplicaBatch is the transport-neutral v1 envelope. Transactions are ordered
// and caller-owned. Their checksums are the source WAL-frame checksums.
type ReplicaBatch struct {
	Version        uint16
	DatabaseID     string
	From           HistoryCursor
	To             HistoryCursor
	SourceBoundary HistoryCursor
	Transactions   []CommitEvent
	Bytes          uint64
}

// Complete reports whether this batch reaches its fixed source boundary.
func (batch ReplicaBatch) Complete() bool {
	return batch.To == batch.SourceBoundary
}

// ReplicaBatchApply describes the target's durable progress for one apply
// attempt. An error may still return a committed prefix in To.
type ReplicaBatchApply struct {
	From                HistoryCursor
	To                  HistoryCursor
	AppliedTransactions uint64
	Acknowledgement     ReplicaAcknowledgement
}

// ReplicaAcknowledgement is the small protocol-v1 message returned only after
// a target reaches a complete batch end. It never repeats transaction bodies.
type ReplicaAcknowledgement struct {
	Version uint16
	Cursor  HistoryCursor
}

// ReplicaBootstrap describes a standalone baseline and the source pin that
// protects every transaction needed to catch it up.
type ReplicaBootstrap struct {
	Name   string
	Anchor BackupAnchor
	Pin    HistoryPin
}

// ReplicaCatchUp describes one bounded source-history walk. The target's
// durable main/WAL boundary is its restart cursor; no second cursor file is
// required.
type ReplicaCatchUp struct {
	Name                string
	From                HistoryCursor
	To                  HistoryCursor
	SourceBoundary      HistoryCursor
	AppliedTransactions uint64
	Pin                 HistoryPin
}

// OpenReplica opens a database in replica mode. Reads, checkpoints, snapshots,
// and downstream history retention remain available, but Begin is refused.
func OpenReplica(path string, options OpenOptions) (*DB, error) {
	options.Replica = true
	return OpenWithOptions(path, options)
}

// BootstrapReplica creates a verified standalone baseline and a durable source
// pin. The destination must not exist. If publication succeeds but a later pin
// advance fails, the returned anchor remains valid and the older pin remains
// intentionally conservative.
func (db *DB) BootstrapReplica(ctx context.Context, name, destination string) (ReplicaBootstrap, error) {
	result := ReplicaBootstrap{Name: name}
	if ctx == nil {
		return result, fmt.Errorf("kitdb: nil replica bootstrap context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := ValidateHistoryPinName(name); err != nil {
		return result, err
	}
	resolvedDestination, err := resolveBackupAnchorPath(destination)
	if err != nil {
		return result, err
	}
	if sameBackupPath(resolvedDestination, db.path) {
		return result, fmt.Errorf("kitdb: replica destination is the live source database")
	}
	if err := validateBackupDestination(resolvedDestination); err != nil {
		return result, err
	}
	if !db.replicaMu.TryLock() {
		return result, ErrReplicaBusy
	}
	defer db.replicaMu.Unlock()

	pins, err := db.HistoryPins()
	if err != nil {
		return result, err
	}
	for _, pin := range pins {
		if pin.Name == name {
			return result, errors.Join(ErrReplicaPinExists, fmt.Errorf("kitdb: replica pin %q already exists", name))
		}
	}

	var pin HistoryPin
	for attempt := 0; attempt < replicaBootstrapPinAttempts; attempt++ {
		cursor, cursorErr := db.CurrentCursor()
		if cursorErr != nil {
			return result, cursorErr
		}
		if _, checkpointErr := db.Checkpoint(); checkpointErr != nil {
			return result, checkpointErr
		}
		pin, err = db.SetHistoryPin(ctx, name, cursor)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrHistoryGap) && !errors.Is(err, ErrHistoryCursor) {
			return result, err
		}
	}
	if err != nil {
		return result, err
	}
	result.Pin = pin

	anchor, err := db.CreateBackupAnchor(ctx, resolvedDestination)
	if err != nil {
		result.Anchor = anchor
		if anchor.Path == "" {
			// A post-publication sync failure may return no descriptor even though
			// a complete anchor is visible. Keep the conservative pin if so.
			verified, verifyErr := VerifyBackupAnchor(context.WithoutCancel(ctx), resolvedDestination)
			if verifyErr == nil && verified.DatabaseID == db.ID() {
				result.Anchor = verified
				return result, err
			}
		}
		if anchor.Path != "" {
			return result, err
		}
		cleanupErr := db.ReleaseHistoryPin(context.WithoutCancel(ctx), name)
		return result, errors.Join(err, cleanupErr)
	}
	result.Anchor = anchor

	// The first pin protects every frame produced while the snapshot was being
	// built. Seal through at least the anchor before moving that pin forward.
	if _, err := db.Checkpoint(); err != nil {
		return result, err
	}
	pin, err = db.SetHistoryPin(ctx, name, anchor.Cursor())
	if err != nil {
		return result, err
	}
	result.Pin = pin
	return result, nil
}

// ReadReplicaBatch returns one bounded transport unit after an exact source
// cursor. It validates the retained chain through the fixed source boundary
// even when only the first bounded prefix is returned.
func (db *DB) ReadReplicaBatch(ctx context.Context, request ReplicaBatchRequest) (ReplicaBatch, error) {
	if ctx == nil {
		return ReplicaBatch{}, fmt.Errorf("kitdb: nil replica batch context")
	}
	if err := ctx.Err(); err != nil {
		return ReplicaBatch{}, err
	}
	limits, err := normalizeReplicaBatchLimits(request.Limits)
	if err != nil {
		return ReplicaBatch{}, err
	}
	if request.From.DatabaseID != db.ID() {
		return ReplicaBatch{}, errors.Join(
			ErrHistoryCursor,
			fmt.Errorf("kitdb: replica source cursor does not belong to this database"),
		)
	}
	boundary := request.SourceBoundary
	if isZeroHistoryCursor(boundary) {
		boundary, err = db.checkpointCursor()
		if err != nil {
			return ReplicaBatch{}, err
		}
	} else if boundary.DatabaseID != db.ID() {
		return ReplicaBatch{}, errors.Join(
			ErrHistoryCursor,
			fmt.Errorf("kitdb: replica source boundary does not belong to this database"),
		)
	}
	batch := newReplicaBatch(request.From, boundary)
	full := false
	var limitErr error
	_, err = db.walkReplicaHistory(ctx, request.From, boundary, func(event CommitEvent) error {
		if full {
			return nil
		}
		added, eventBytes, appendErr := appendReplicaBatchEvent(&batch, event, limits)
		if appendErr != nil {
			return appendErr
		}
		if added {
			return nil
		}
		full = true
		if len(batch.Transactions) == 0 {
			limitErr = errors.Join(
				ErrReplicaBatchLimit,
				fmt.Errorf("kitdb: replica transaction %d requires %d bytes, limit is %d", event.Transaction, eventBytes, limits.MaxBytes),
			)
		}
		return nil
	})
	if err != nil {
		return batch, err
	}
	if limitErr != nil {
		return batch, limitErr
	}
	return batch, nil
}

// ApplyReplicaBatch validates a complete v1 envelope before committing its
// missing suffix through the target's ordinary WAL path. Replaying the same
// complete or partially applied batch is idempotent.
func (db *DB) ApplyReplicaBatch(ctx context.Context, batch ReplicaBatch) (ReplicaBatchApply, error) {
	if ctx == nil {
		return ReplicaBatchApply{}, fmt.Errorf("kitdb: nil replica apply context")
	}
	if err := ctx.Err(); err != nil {
		return ReplicaBatchApply{}, err
	}
	if !db.replicaMu.TryLock() {
		return ReplicaBatchApply{}, ErrReplicaBusy
	}
	defer db.replicaMu.Unlock()
	return db.applyReplicaBatchLocked(ctx, batch)
}

// AcknowledgeReplicaBatch advances one durable source pin only after the
// caller acknowledges the batch's exact To cursor. The source re-proves that
// cursor against retained history before publishing the pin.
func (db *DB) AcknowledgeReplicaBatch(
	ctx context.Context,
	name string,
	acknowledgement ReplicaAcknowledgement,
) (HistoryPin, error) {
	if ctx == nil {
		return HistoryPin{}, fmt.Errorf("kitdb: nil replica acknowledgement context")
	}
	if err := ctx.Err(); err != nil {
		return HistoryPin{}, err
	}
	if err := ValidateHistoryPinName(name); err != nil {
		return HistoryPin{}, err
	}
	if err := validateReplicaAcknowledgement(acknowledgement); err != nil {
		return HistoryPin{}, err
	}
	if acknowledgement.Cursor.DatabaseID != db.ID() {
		return HistoryPin{}, errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica acknowledgement belongs to another database"),
		)
	}
	return db.SetHistoryPin(ctx, name, acknowledgement.Cursor)
}

// CatchUpReplica streams bounded protocol-v1 batches into a replica-mode
// handle and advances the source pin only after the complete fixed boundary is
// acknowledged. Partial target progress remains its own durable restart cursor.
func (db *DB) CatchUpReplica(ctx context.Context, name string, replica *DB) (ReplicaCatchUp, error) {
	result := ReplicaCatchUp{Name: name}
	if ctx == nil {
		return result, fmt.Errorf("kitdb: nil replica catch-up context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if replica == nil {
		return result, fmt.Errorf("kitdb: nil replica target")
	}
	if db == replica {
		return result, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: source and replica are the same handle"))
	}
	if err := ValidateHistoryPinName(name); err != nil {
		return result, err
	}
	if !replica.replicaMu.TryLock() {
		return result, ErrReplicaBusy
	}
	defer replica.replicaMu.Unlock()

	replica.mu.RLock()
	replicaMode := replica.replicaMode
	replicaStateErr := replica.stateErrorLocked()
	replica.mu.RUnlock()
	if replicaStateErr != nil {
		return result, replicaStateErr
	}
	if !replicaMode {
		return result, ErrReplicaModeRequired
	}

	from, err := replica.CurrentCursor()
	if err != nil {
		return result, err
	}
	result.From = from
	result.To = from
	if from.DatabaseID != db.ID() {
		return result, errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica database %q does not match source %q", from.DatabaseID, db.ID()),
		)
	}

	pin, err := db.SetHistoryPin(ctx, name, from)
	if err != nil {
		return result, err
	}
	result.Pin = pin
	boundary, err := db.checkpointCursor()
	if err != nil {
		return result, err
	}
	result.SourceBoundary = boundary
	limits, err := normalizeReplicaBatchLimits(ReplicaBatchLimits{})
	if err != nil {
		return result, err
	}

	batch := newReplicaBatch(from, boundary)
	var lastAcknowledgement ReplicaAcknowledgement
	flush := func(retain bool) error {
		applied, applyErr := replica.applyReplicaBatchLocked(ctx, batch)
		result.To = applied.To
		result.AppliedTransactions += applied.AppliedTransactions
		if applyErr != nil {
			return applyErr
		}
		if retain {
			lastAcknowledgement = applied.Acknowledgement
		} else {
			lastAcknowledgement = ReplicaAcknowledgement{}
		}
		batch = newReplicaBatch(result.To, boundary)
		return nil
	}

	_, walkErr := db.walkReplicaHistory(ctx, from, boundary, func(event CommitEvent) error {
		added, _, appendErr := appendReplicaBatchEvent(&batch, event, limits)
		if appendErr != nil {
			return appendErr
		}
		if added {
			return nil
		}
		if len(batch.Transactions) == 0 {
			return ErrReplicaBatchLimit
		}
		if err := flush(false); err != nil {
			return err
		}
		added, eventBytes, appendErr := appendReplicaBatchEvent(&batch, event, limits)
		if appendErr != nil {
			return appendErr
		}
		if !added {
			return errors.Join(
				ErrReplicaBatchLimit,
				fmt.Errorf("kitdb: replica transaction %d requires %d bytes", event.Transaction, eventBytes),
			)
		}
		return nil
	})
	if walkErr != nil {
		return result, walkErr
	}
	if len(batch.Transactions) != 0 || from == boundary {
		if err := flush(true); err != nil {
			return result, err
		}
	}
	if result.To != boundary {
		return result, errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica reached %+v, source boundary is %+v", result.To, boundary),
		)
	}
	if lastAcknowledgement.Version != ReplicaProtocolVersion {
		return result, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica catch-up produced no final batch"))
	}
	pin, err = db.AcknowledgeReplicaBatch(ctx, name, lastAcknowledgement)
	if err != nil {
		return result, err
	}
	result.Pin = pin
	return result, nil
}

func normalizeReplicaBatchLimits(limits ReplicaBatchLimits) (ReplicaBatchLimits, error) {
	if limits.MaxTransactions == 0 {
		limits.MaxTransactions = DefaultReplicaBatchTransactions
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = DefaultReplicaBatchBytes
	}
	if limits.MaxTransactions > MaxReplicaBatchTransactions {
		return ReplicaBatchLimits{}, errors.Join(
			ErrReplicaBatchLimit,
			fmt.Errorf("kitdb: replica batch transaction limit %d exceeds maximum %d", limits.MaxTransactions, MaxReplicaBatchTransactions),
		)
	}
	if limits.MaxBytes > MaxReplicaBatchBytes {
		return ReplicaBatchLimits{}, errors.Join(
			ErrReplicaBatchLimit,
			fmt.Errorf("kitdb: replica batch byte limit %d exceeds maximum %d", limits.MaxBytes, MaxReplicaBatchBytes),
		)
	}
	return limits, nil
}

func isZeroHistoryCursor(cursor HistoryCursor) bool {
	return cursor.DatabaseID == "" && cursor.Transaction == 0 && cursor.Checksum == 0
}

func newReplicaBatch(from, boundary HistoryCursor) ReplicaBatch {
	return ReplicaBatch{
		Version: ReplicaProtocolVersion, DatabaseID: from.DatabaseID,
		From: from, To: from, SourceBoundary: boundary,
	}
}

func appendReplicaBatchEvent(
	batch *ReplicaBatch,
	event CommitEvent,
	limits ReplicaBatchLimits,
) (bool, uint64, error) {
	eventBytes, err := replicaEventFrameBytes(event)
	if err != nil {
		return false, 0, err
	}
	if batch.Bytes > limits.MaxBytes {
		return false, eventBytes, errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica batch already contains %d bytes, limit is %d", batch.Bytes, limits.MaxBytes),
		)
	}
	if uint32(len(batch.Transactions)) >= limits.MaxTransactions ||
		eventBytes > limits.MaxBytes-batch.Bytes {
		return false, eventBytes, nil
	}
	batch.Transactions = append(batch.Transactions, cloneReplicaCommitEvent(event))
	batch.Bytes += eventBytes
	batch.To = HistoryCursor{
		DatabaseID: batch.DatabaseID, Transaction: event.Transaction, Checksum: event.Checksum,
	}
	return true, eventBytes, nil
}

func cloneReplicaCommitEvent(event CommitEvent) CommitEvent {
	cloned := CommitEvent{
		Transaction: event.Transaction,
		Checksum:    event.Checksum,
		CommittedAt: event.CommittedAt,
		Operations:  make([]CommitOperation, len(event.Operations)),
	}
	for index, operation := range event.Operations {
		cloned.Operations[index] = CommitOperation{
			Kind:  operation.Kind,
			Key:   append([]byte(nil), operation.Key...),
			Value: append([]byte(nil), operation.Value...),
		}
	}
	return cloned
}

func replicaEventFrameBytes(event CommitEvent) (uint64, error) {
	if event.Transaction == 0 {
		return 0, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica transaction is zero"))
	}
	if len(event.Operations) == 0 || len(event.Operations) > maxOperations {
		return 0, errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica transaction %d has %d operations", event.Transaction, len(event.Operations)),
		)
	}
	payloadBytes := uint64(payloadPrefixSize)
	if !event.CommittedAt.IsZero() {
		if commitTimeUnixNano(event.CommittedAt) <= 0 {
			return 0, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica transaction %d has an invalid commit timestamp", event.Transaction))
		}
		payloadBytes = timedPayloadPrefixSize
	}
	for index, item := range event.Operations {
		kind := operationKind(0)
		switch item.Kind {
		case CommitOperationPut:
			kind = operationPut
		case CommitOperationDelete:
			kind = operationDelete
		default:
			return 0, errors.Join(
				ErrReplicaProtocol,
				fmt.Errorf("kitdb: replica operation %d has kind %d", index, item.Kind),
			)
		}
		if err := validateOperation(operation{kind: kind, key: item.Key, value: item.Value}); err != nil {
			return 0, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica operation %d: %w", index, err))
		}
		operationBytes := uint64(operationHeaderSize + len(item.Key) + len(item.Value))
		if operationBytes > uint64(maxPayloadSize)-payloadBytes {
			return 0, errors.Join(ErrReplicaProtocol, ErrTransactionTooLarge)
		}
		payloadBytes += operationBytes
	}
	return uint64(frameHeaderSize+frameTrailerSize) + payloadBytes, nil
}

func validateReplicaDatabaseID(databaseID string) error {
	if len(databaseID) != 32 {
		return errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica database identity has %d bytes, want 32", len(databaseID)),
		)
	}
	decoded, err := hex.DecodeString(databaseID)
	if err != nil || hex.EncodeToString(decoded) != databaseID {
		return errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica database identity is not canonical lowercase hexadecimal"),
		)
	}
	return nil
}

func validateReplicaBatchEnvelope(batch ReplicaBatch) error {
	if batch.Version != ReplicaProtocolVersion {
		return errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica protocol version %d is unsupported", batch.Version),
		)
	}
	if err := validateReplicaDatabaseID(batch.DatabaseID); err != nil {
		return err
	}
	cursors := []struct {
		name   string
		cursor HistoryCursor
	}{
		{name: "from", cursor: batch.From},
		{name: "to", cursor: batch.To},
		{name: "source boundary", cursor: batch.SourceBoundary},
	}
	for _, item := range cursors {
		name := item.name
		cursor := item.cursor
		if cursor.DatabaseID != batch.DatabaseID {
			return errors.Join(
				ErrReplicaProtocol,
				fmt.Errorf("kitdb: replica %s cursor does not match the envelope database identity", name),
			)
		}
		if cursor.Transaction == 0 && cursor.Checksum != 0 {
			return errors.Join(
				ErrReplicaProtocol,
				fmt.Errorf("kitdb: replica %s cursor has a checksum at transaction zero", name),
			)
		}
	}
	if batch.From.Transaction > batch.To.Transaction || batch.To.Transaction > batch.SourceBoundary.Transaction {
		return errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf(
				"kitdb: replica cursor order is %d -> %d -> %d",
				batch.From.Transaction,
				batch.To.Transaction,
				batch.SourceBoundary.Transaction,
			),
		)
	}
	if len(batch.Transactions) > int(MaxReplicaBatchTransactions) {
		return errors.Join(
			ErrReplicaBatchLimit,
			fmt.Errorf("kitdb: replica batch has %d transactions, maximum is %d", len(batch.Transactions), MaxReplicaBatchTransactions),
		)
	}
	expectedTransaction := batch.From.Transaction
	expectedTo := batch.From
	computedBytes := uint64(0)
	lastCommitTime := int64(0)
	for index, event := range batch.Transactions {
		if expectedTransaction == math.MaxUint64 {
			return errors.Join(ErrReplicaProtocol, ErrTransactionIDOverflow)
		}
		expectedTransaction++
		if event.Transaction != expectedTransaction {
			return errors.Join(
				ErrReplicaProtocol,
				fmt.Errorf("kitdb: replica transaction %d at index %d does not follow %d", event.Transaction, index, expectedTransaction-1),
			)
		}
		committedAt := commitTimeUnixNano(event.CommittedAt)
		if committedAt != 0 {
			if lastCommitTime != 0 && committedAt <= lastCommitTime {
				return errors.Join(
					ErrReplicaProtocol,
					fmt.Errorf("kitdb: replica transaction %d commit timestamp does not follow the previous transaction", event.Transaction),
				)
			}
			lastCommitTime = committedAt
		}
		eventBytes, err := replicaEventFrameBytes(event)
		if err != nil {
			return err
		}
		if eventBytes > MaxReplicaBatchBytes-computedBytes {
			return errors.Join(ErrReplicaBatchLimit, fmt.Errorf("kitdb: replica batch byte count exceeds %d", MaxReplicaBatchBytes))
		}
		computedBytes += eventBytes
		expectedTo = HistoryCursor{
			DatabaseID: batch.DatabaseID, Transaction: event.Transaction, Checksum: event.Checksum,
		}
	}
	if batch.To != expectedTo {
		return errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica batch ends at %+v, transactions end at %+v", batch.To, expectedTo),
		)
	}
	if batch.Bytes != computedBytes {
		return errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica batch declares %d bytes, computed %d", batch.Bytes, computedBytes),
		)
	}
	if len(batch.Transactions) == 0 && batch.To != batch.SourceBoundary {
		return errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: empty replica batch does not reach its source boundary"))
	}
	if batch.To.Transaction == batch.SourceBoundary.Transaction && batch.To.Checksum != batch.SourceBoundary.Checksum {
		return errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica source-boundary checksum does not match batch end"),
		)
	}
	return nil
}

func replicaAcknowledgement(batch ReplicaBatch) ReplicaAcknowledgement {
	return ReplicaAcknowledgement{
		Version: batch.Version,
		Cursor:  batch.To,
	}
}

func validateReplicaAcknowledgement(acknowledgement ReplicaAcknowledgement) error {
	if acknowledgement.Version != ReplicaProtocolVersion {
		return errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica acknowledgement version %d is unsupported", acknowledgement.Version),
		)
	}
	if err := validateReplicaDatabaseID(acknowledgement.Cursor.DatabaseID); err != nil {
		return err
	}
	if acknowledgement.Cursor.Transaction == 0 && acknowledgement.Cursor.Checksum != 0 {
		return errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: replica acknowledgement has a checksum at transaction zero"),
		)
	}
	return nil
}

func validateReplicaBatchChecksums(ctx context.Context, batch ReplicaBatch) error {
	for _, event := range batch.Transactions {
		if err := ctx.Err(); err != nil {
			return err
		}
		operations, err := referenceReplicaOperations(event.Operations)
		if err != nil {
			return err
		}
		frame, err := encodeFrameAt(event.Transaction, commitTimeUnixNano(event.CommittedAt), operations)
		if err != nil {
			return errors.Join(ErrReplicaDiverged, err)
		}
		if checksum := frameChecksum(frame); checksum != event.Checksum {
			return errors.Join(
				ErrReplicaDiverged,
				fmt.Errorf("kitdb: replica event %d checksum is %08x, computed %08x", event.Transaction, event.Checksum, checksum),
			)
		}
	}
	return nil
}

func (db *DB) applyReplicaBatchLocked(ctx context.Context, batch ReplicaBatch) (ReplicaBatchApply, error) {
	result := ReplicaBatchApply{}
	if err := validateReplicaBatchEnvelope(batch); err != nil {
		return result, err
	}
	db.mu.RLock()
	replicaMode := db.replicaMode
	stateErr := db.stateErrorLocked()
	db.mu.RUnlock()
	if stateErr != nil {
		return result, stateErr
	}
	if !replicaMode {
		return result, ErrReplicaModeRequired
	}
	current, err := db.CurrentCursor()
	if err != nil {
		return result, err
	}
	result.From = current
	result.To = current
	if current.DatabaseID != batch.DatabaseID {
		return result, errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica batch belongs to database %q, target is %q", batch.DatabaseID, current.DatabaseID),
		)
	}
	start, err := replicaBatchResumeIndex(batch, current)
	if err != nil {
		return result, err
	}
	if err := validateReplicaBatchChecksums(ctx, batch); err != nil {
		return result, err
	}
	for index := start; index < len(batch.Transactions); index++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		operations, err := decodeReplicaOperations(batch.Transactions[index].Operations)
		if err != nil {
			return result, err
		}
		cursor, applyErr := db.applyReplicaOperations(ctx, batch.DatabaseID, batch.Transactions[index], operations)
		result.To = cursor
		if applyErr != nil {
			return result, applyErr
		}
		result.AppliedTransactions++
	}
	if result.To != batch.To {
		return result, errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica batch reached %+v, want %+v", result.To, batch.To),
		)
	}
	result.Acknowledgement = replicaAcknowledgement(batch)
	return result, nil
}

func replicaBatchResumeIndex(batch ReplicaBatch, current HistoryCursor) (int, error) {
	if current == batch.From {
		return 0, nil
	}
	if current.Transaction <= batch.From.Transaction || current.Transaction > batch.To.Transaction {
		return 0, errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica target cursor %d is outside batch range %d..%d", current.Transaction, batch.From.Transaction, batch.To.Transaction),
		)
	}
	offset := current.Transaction - batch.From.Transaction - 1
	if offset >= uint64(len(batch.Transactions)) {
		return 0, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: replica target cursor is absent from batch"))
	}
	event := batch.Transactions[int(offset)]
	if current.Checksum != event.Checksum {
		return 0, errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica target checksum %08x does not match batch transaction %d checksum %08x", current.Checksum, current.Transaction, event.Checksum),
		)
	}
	return int(offset) + 1, nil
}

func (db *DB) applyReplicaEvent(ctx context.Context, databaseID string, event CommitEvent) (HistoryCursor, error) {
	if err := ctx.Err(); err != nil {
		return HistoryCursor{}, err
	}
	if event.Transaction == 0 {
		return HistoryCursor{}, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: replica event has transaction zero"))
	}
	operations, err := decodeReplicaOperations(event.Operations)
	if err != nil {
		return HistoryCursor{}, err
	}
	frame, err := encodeFrameAt(event.Transaction, commitTimeUnixNano(event.CommittedAt), operations)
	if err != nil {
		return HistoryCursor{}, err
	}
	if checksum := frameChecksum(frame); checksum != event.Checksum {
		return HistoryCursor{}, errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica event %d checksum is %08x, computed %08x", event.Transaction, event.Checksum, checksum),
		)
	}
	return db.applyReplicaOperations(ctx, databaseID, event, operations)
}

func (db *DB) applyReplicaOperations(
	ctx context.Context,
	databaseID string,
	event CommitEvent,
	operations []operation,
) (HistoryCursor, error) {
	current, err := db.CurrentCursor()
	if err != nil {
		return HistoryCursor{}, err
	}
	if current.DatabaseID != databaseID {
		return current, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: replica event belongs to database %q", databaseID))
	}
	if event.Transaction == current.Transaction && event.Checksum == current.Checksum {
		return current, nil
	}
	if current.Transaction == math.MaxUint64 || event.Transaction != current.Transaction+1 {
		return current, errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica received transaction %d after %d", event.Transaction, current.Transaction),
		)
	}
	if err := ctx.Err(); err != nil {
		return current, err
	}
	transaction, err := db.commitReplica(operations, commitTimeUnixNano(event.CommittedAt))
	if err != nil {
		return current, err
	}
	applied, cursorErr := db.CurrentCursor()
	if cursorErr != nil {
		return HistoryCursor{}, cursorErr
	}
	if transaction != event.Transaction || applied.Transaction != event.Transaction || applied.Checksum != event.Checksum {
		diverged := errors.Join(
			ErrReplicaDiverged,
			fmt.Errorf("kitdb: replica committed transaction %d at checksum %08x, want %d/%08x", applied.Transaction, applied.Checksum, event.Transaction, event.Checksum),
		)
		db.markUnavailable(diverged)
		return applied, diverged
	}
	return applied, nil
}

func decodeReplicaOperations(source []CommitOperation) ([]operation, error) {
	return convertReplicaOperations(source, true)
}

func referenceReplicaOperations(source []CommitOperation) ([]operation, error) {
	return convertReplicaOperations(source, false)
}

func convertReplicaOperations(source []CommitOperation, clone bool) ([]operation, error) {
	if len(source) == 0 {
		return nil, errors.Join(ErrReplicaDiverged, ErrEmptyTransaction)
	}
	if len(source) > maxOperations {
		return nil, errors.Join(ErrReplicaDiverged, ErrTransactionTooLarge)
	}
	operations := make([]operation, len(source))
	for index, item := range source {
		key := item.Key
		value := item.Value
		if clone {
			key = append([]byte(nil), key...)
			value = append([]byte(nil), value...)
		}
		switch item.Kind {
		case CommitOperationPut:
			operations[index] = operation{
				kind: operationPut, key: key, value: value,
			}
		case CommitOperationDelete:
			if len(item.Value) != 0 {
				return nil, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: replica delete operation %d carries a value", index))
			}
			operations[index] = operation{kind: operationDelete, key: key}
		default:
			return nil, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: replica operation %d has kind %d", index, item.Kind))
		}
	}
	if err := validateCommitOperations(operations); err != nil {
		return nil, errors.Join(ErrReplicaDiverged, err)
	}
	return operations, nil
}
