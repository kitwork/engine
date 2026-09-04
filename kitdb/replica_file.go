package kitdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	replicaFileBatchDirectory = "batches"
	replicaFileAckDirectory   = "acks"
	replicaFileBatchSuffix    = ".krpb"
	replicaFileAckSuffix      = ".krpa"
	replicaFileTemporaryMark  = ".kitdb-replica-"

	// DefaultReplicaFilePendingBatches bounds one mailbox without tuning.
	DefaultReplicaFilePendingBatches uint32 = 64
	// MaxReplicaFilePendingBatches is the hard mailbox cardinality ceiling.
	MaxReplicaFilePendingBatches uint32 = 4096
	// DefaultReplicaFilePendingBytes bounds batch, ACK, and staging bytes.
	DefaultReplicaFilePendingBytes uint64 = 512 << 20
	// MaxReplicaFilePendingBytes is the hard mailbox byte ceiling.
	MaxReplicaFilePendingBytes uint64 = 64 << 30

	replicaFileStagingAllowance = 64
)

// ReplicaFileTransportLimits bounds one explicit filesystem mailbox. Zero
// values select defaults. These limits are operational policy, not wire-format
// limits.
type ReplicaFileTransportLimits struct {
	MaxPendingBatches uint32
	MaxPendingBytes   uint64
}

// ValidateReplicaFileTransportLimits validates mailbox policy without creating
// or inspecting a filesystem path. Zero values select the documented defaults.
func ValidateReplicaFileTransportLimits(limits ReplicaFileTransportLimits) error {
	_, err := normalizeReplicaFileTransportLimits(limits)
	return err
}

// ReplicaFileTransport is a host-owned durable mailbox. It has no background
// worker and stores no authoritative replica cursor; the target WAL and source
// history pin remain the only durable progress boundaries.
type ReplicaFileTransport struct {
	root        string
	batchPath   string
	ackPath     string
	limits      ReplicaFileTransportLimits
	operationMu sync.Mutex
}

// ReplicaFilePublication describes one atomically visible batch file.
type ReplicaFilePublication struct {
	Path             string
	DatabaseID       string
	From             HistoryCursor
	To               HistoryCursor
	SourceBoundary   HistoryCursor
	Transactions     uint32
	Bytes            uint64
	AlreadyPublished bool
}

// ReplicaFileApply describes target WAL progress and ACK publication for one
// mailbox batch.
type ReplicaFileApply struct {
	BatchPath           string
	AcknowledgementPath string
	From                HistoryCursor
	To                  HistoryCursor
	SourceBoundary      HistoryCursor
	AppliedTransactions uint64
}

// ReplicaFileAcknowledge describes a source pin advance and mailbox cleanup.
type ReplicaFileAcknowledge struct {
	BatchPath           string
	AcknowledgementPath string
	Cursor              HistoryCursor
	Pin                 HistoryPin
	AlreadyAcknowledged bool
}

// ReplicaFileTransportStats reports bounded mailbox pressure. Staging bytes
// are included in PendingBytes so interrupted publishers cannot evade quota.
type ReplicaFileTransportStats struct {
	PendingBatches          uint32
	PendingAcknowledgements uint32
	StagingFiles            uint32
	PendingBytes            uint64
}

// ReplicaFileCleanup reports staging files removed while the mailbox was
// explicitly quiescent.
type ReplicaFileCleanup struct {
	Files uint32
	Bytes uint64
}

// OpenReplicaFileTransport creates or opens a dedicated mailbox directory.
// Source and target may reopen it after a crash; callers must not place
// unrelated files in its managed batches/ or acks/ directories.
func OpenReplicaFileTransport(root string, limits ReplicaFileTransportLimits) (*ReplicaFileTransport, error) {
	normalized, err := normalizeReplicaFileTransportLimits(limits)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("kitdb: empty replica file transport path")
	}
	resolved, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("kitdb: resolve replica file transport: %w", err)
	}
	resolved = filepath.Clean(resolved)
	if err := ensureReplicaTransportDirectory(resolved); err != nil {
		return nil, err
	}
	batchPath := filepath.Join(resolved, replicaFileBatchDirectory)
	ackPath := filepath.Join(resolved, replicaFileAckDirectory)
	if err := ensureReplicaTransportDirectory(batchPath); err != nil {
		return nil, err
	}
	if err := ensureReplicaTransportDirectory(ackPath); err != nil {
		return nil, err
	}
	if err := syncDirectory(resolved); err != nil {
		return nil, fmt.Errorf("kitdb: sync replica transport root: %w", err)
	}
	if err := syncDirectory(filepath.Dir(resolved)); err != nil {
		return nil, fmt.Errorf("kitdb: sync replica transport parent: %w", err)
	}
	return &ReplicaFileTransport{
		root: resolved, batchPath: batchPath, ackPath: ackPath, limits: normalized,
	}, nil
}

// Root returns the resolved mailbox directory.
func (transport *ReplicaFileTransport) Root() string {
	if transport == nil {
		return ""
	}
	return transport.root
}

// PublishBatch stages, syncs, and exclusively publishes one deterministic
// batch file. Repeating the identical batch is idempotent; a filename collision
// with different content fails closed.
func (transport *ReplicaFileTransport) PublishBatch(
	ctx context.Context,
	batch ReplicaBatch,
) (ReplicaFilePublication, error) {
	result := replicaFilePublication(batch)
	if transport == nil {
		return result, fmt.Errorf("kitdb: nil replica file transport")
	}
	if ctx == nil {
		return result, fmt.Errorf("kitdb: nil replica file transport context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validateReplicaBatchEnvelope(batch); err != nil {
		return result, err
	}
	filename := replicaBatchFilename(batch.DatabaseID, batch.From, batch.To)
	result.Path = filepath.Join(transport.batchPath, filename)
	result.Bytes = uint64(replicaBatchMessageHeaderSize) + batch.Bytes + replicaBatchMessageTrailerSize

	transport.operationMu.Lock()
	defer transport.operationMu.Unlock()
	if existing, err := transport.readExistingBatch(ctx, result.Path, batch); existing || err != nil {
		result.AlreadyPublished = existing
		if existing && err == nil {
			err = syncReplicaTransportPublication(transport.batchPath, "batch")
		}
		return result, err
	}
	stats, err := transport.statsLocked(ctx)
	if err != nil {
		return result, err
	}
	reservedBytes, err := reservedReplicaTransportBytes(stats)
	if err != nil {
		return result, err
	}
	publicationBytes := result.Bytes + ReplicaAcknowledgementMessageBytes
	if reservedBytes > transport.limits.MaxPendingBytes ||
		stats.PendingBatches >= transport.limits.MaxPendingBatches ||
		publicationBytes > transport.limits.MaxPendingBytes-reservedBytes {
		return result, errors.Join(
			ErrReplicaTransportLimit,
			fmt.Errorf("kitdb: replica mailbox would contain %d batches and at least %d reserved bytes", stats.PendingBatches+1, reservedBytes+publicationBytes),
		)
	}

	temporary, err := os.CreateTemp(transport.batchPath, replicaFileTemporaryMark+"batch-*.tmp")
	if err != nil {
		return result, fmt.Errorf("kitdb: create replica batch staging: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return result, fmt.Errorf("kitdb: set replica batch permissions: %w", err)
	}
	written, err := WriteReplicaBatchMessage(ctx, temporary, batch)
	if err != nil {
		return result, err
	}
	if written != result.Bytes {
		return result, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica batch staging has %d bytes, want %d", written, result.Bytes))
	}
	if err := temporary.Sync(); err != nil {
		return result, fmt.Errorf("kitdb: sync replica batch staging: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return result, fmt.Errorf("kitdb: close replica batch staging: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := os.Link(temporaryPath, result.Path); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, existingErr := transport.readExistingBatch(ctx, result.Path, batch)
			result.AlreadyPublished = existing
			if existing && existingErr == nil {
				existingErr = syncReplicaTransportPublication(transport.batchPath, "batch")
			}
			return result, existingErr
		}
		return result, fmt.Errorf("kitdb: publish replica batch: %w", err)
	}
	removeErr := os.Remove(temporaryPath)
	if removeErr == nil {
		temporaryPath = ""
	}
	syncErr := syncDirectory(transport.batchPath)
	if syncErr != nil {
		syncErr = errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: sync replica batch directory: %w", syncErr))
	}
	if removeErr != nil {
		removeErr = fmt.Errorf("kitdb: remove replica batch staging link: %w", removeErr)
	}
	return result, errors.Join(removeErr, syncErr)
}

// ApplyNext applies the earliest unacknowledged batch through the target's
// ordinary WAL and atomically publishes its ACK. A false second result means
// the mailbox has no unapplied batch.
func (transport *ReplicaFileTransport) ApplyNext(
	ctx context.Context,
	target *DB,
) (ReplicaFileApply, bool, error) {
	var result ReplicaFileApply
	if transport == nil {
		return result, false, fmt.Errorf("kitdb: nil replica file transport")
	}
	if ctx == nil {
		return result, false, fmt.Errorf("kitdb: nil replica file transport context")
	}
	if target == nil {
		return result, false, fmt.Errorf("kitdb: nil replica file target")
	}
	if err := ctx.Err(); err != nil {
		return result, false, err
	}
	transport.operationMu.Lock()
	defer transport.operationMu.Unlock()

	batches, batchScan, err := transport.scanBatchFiles(ctx)
	if err != nil {
		return result, false, err
	}
	acks, ackScan, err := transport.scanAcknowledgementFiles(ctx)
	if err != nil {
		return result, false, err
	}
	if err := transport.validateScans(batchScan, ackScan); err != nil {
		return result, false, err
	}
	acknowledged := make(map[string]struct{}, len(acks))
	for _, acknowledgement := range acks {
		acknowledged[acknowledgement.name] = struct{}{}
	}
	var selected *replicaBatchTransportFile
	for index := range batches {
		ackName := replicaAcknowledgementFilename(batches[index].databaseID, batches[index].to)
		if _, found := acknowledged[ackName]; found {
			continue
		}
		selected = &batches[index]
		break
	}
	if selected == nil {
		return result, false, nil
	}
	current, err := target.CurrentCursor()
	if err != nil {
		return result, true, err
	}
	if selected.databaseID != current.DatabaseID {
		return result, true, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: replica mailbox batch belongs to database %q, target is %q", selected.databaseID, current.DatabaseID))
	}
	batch, err := readReplicaBatchFile(ctx, selected.path)
	if err != nil {
		return result, true, err
	}
	if err := validateReplicaBatchFilename(*selected, batch); err != nil {
		return result, true, err
	}
	result.BatchPath = selected.path
	result.From = batch.From
	result.To = batch.To
	result.SourceBoundary = batch.SourceBoundary
	applied, applyErr := target.ApplyReplicaBatch(ctx, batch)
	result.From = applied.From
	result.AppliedTransactions = applied.AppliedTransactions
	result.To = applied.To
	if applyErr != nil {
		return result, true, applyErr
	}
	ackPath, ackErr := transport.publishAcknowledgementLocked(ctx, applied.Acknowledgement)
	result.AcknowledgementPath = ackPath
	return result, true, ackErr
}

// AcknowledgeNext consumes the earliest ACK, moves the named source pin only
// forward, then removes the batch before removing the ACK. Replaying after a
// crash is safe whether the pin or either removal was already durable.
func (transport *ReplicaFileTransport) AcknowledgeNext(
	ctx context.Context,
	source *DB,
	pinName string,
) (ReplicaFileAcknowledge, bool, error) {
	var result ReplicaFileAcknowledge
	if transport == nil {
		return result, false, fmt.Errorf("kitdb: nil replica file transport")
	}
	if ctx == nil {
		return result, false, fmt.Errorf("kitdb: nil replica file transport context")
	}
	if source == nil {
		return result, false, fmt.Errorf("kitdb: nil replica file source")
	}
	if err := ValidateHistoryPinName(pinName); err != nil {
		return result, false, err
	}
	if err := ctx.Err(); err != nil {
		return result, false, err
	}
	transport.operationMu.Lock()
	defer transport.operationMu.Unlock()

	acknowledgements, ackScan, err := transport.scanAcknowledgementFiles(ctx)
	if err != nil {
		return result, false, err
	}
	batches, batchScan, err := transport.scanBatchFiles(ctx)
	if err != nil {
		return result, false, err
	}
	if err := transport.validateScans(batchScan, ackScan); err != nil {
		return result, false, err
	}
	if len(acknowledgements) == 0 {
		return result, false, nil
	}
	selected := acknowledgements[0]
	if selected.databaseID != source.ID() {
		return result, true, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: replica acknowledgement belongs to database %q, source is %q", selected.databaseID, source.ID()))
	}
	acknowledgement, err := readReplicaAcknowledgementFile(ctx, selected.path)
	if err != nil {
		return result, true, err
	}
	if acknowledgement.Cursor != selected.cursor {
		return result, true, errors.Join(ErrReplicaTransportConflict, fmt.Errorf("kitdb: replica acknowledgement filename does not match its message"))
	}
	result.AcknowledgementPath = selected.path
	result.Cursor = acknowledgement.Cursor

	matchingBatch := -1
	for index := range batches {
		if batches[index].databaseID == selected.databaseID && batches[index].to == selected.cursor {
			if matchingBatch >= 0 {
				return result, true, errors.Join(ErrReplicaTransportConflict, fmt.Errorf("kitdb: multiple replica batches end at acknowledged cursor %d", selected.cursor.Transaction))
			}
			matchingBatch = index
		}
	}
	if matchingBatch >= 0 {
		batch, err := readReplicaBatchFile(ctx, batches[matchingBatch].path)
		if err != nil {
			return result, true, err
		}
		if err := validateReplicaBatchFilename(batches[matchingBatch], batch); err != nil {
			return result, true, err
		}
		if batch.To != acknowledgement.Cursor {
			return result, true, errors.Join(ErrReplicaTransportConflict, fmt.Errorf("kitdb: replica batch end does not match acknowledgement"))
		}
		result.BatchPath = batches[matchingBatch].path
	}

	pins, err := source.HistoryPins()
	if err != nil {
		return result, true, err
	}
	var currentPin HistoryPin
	foundPin := false
	for _, pin := range pins {
		if pin.Name == pinName {
			currentPin = pin
			foundPin = true
			break
		}
	}
	if !foundPin {
		return result, true, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica source pin %q does not exist", pinName))
	}
	if currentPin.Cursor.DatabaseID != acknowledgement.Cursor.DatabaseID {
		return result, true, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: replica pin and acknowledgement identities differ"))
	}
	switch {
	case currentPin.Cursor.Transaction < acknowledgement.Cursor.Transaction:
		currentPin, err = source.AcknowledgeReplicaBatch(ctx, pinName, acknowledgement)
		if err != nil {
			return result, true, err
		}
	case currentPin.Cursor.Transaction == acknowledgement.Cursor.Transaction:
		if currentPin.Cursor.Checksum != acknowledgement.Cursor.Checksum {
			return result, true, errors.Join(ErrReplicaDiverged, fmt.Errorf("kitdb: replica pin checksum differs from acknowledgement"))
		}
		result.AlreadyAcknowledged = true
	default:
		// Never move a pin backward when replaying an older ACK after cleanup
		// was interrupted.
		result.AlreadyAcknowledged = true
	}
	result.Pin = currentPin

	if result.BatchPath != "" {
		if err := removeReplicaTransportFile(result.BatchPath, transport.batchPath); err != nil {
			return result, true, err
		}
	}
	if err := removeReplicaTransportFile(result.AcknowledgementPath, transport.ackPath); err != nil {
		return result, true, err
	}
	return result, true, nil
}

// Stats reports current bounded mailbox pressure.
func (transport *ReplicaFileTransport) Stats(ctx context.Context) (ReplicaFileTransportStats, error) {
	if transport == nil {
		return ReplicaFileTransportStats{}, fmt.Errorf("kitdb: nil replica file transport")
	}
	if ctx == nil {
		return ReplicaFileTransportStats{}, fmt.Errorf("kitdb: nil replica file transport context")
	}
	transport.operationMu.Lock()
	defer transport.operationMu.Unlock()
	return transport.statsLocked(ctx)
}

// CleanupStaging removes only unpublished temporary files. The host must call
// it while publishers are quiescent, normally once during transport startup.
func (transport *ReplicaFileTransport) CleanupStaging(ctx context.Context) (ReplicaFileCleanup, error) {
	var result ReplicaFileCleanup
	if transport == nil {
		return result, fmt.Errorf("kitdb: nil replica file transport")
	}
	if ctx == nil {
		return result, fmt.Errorf("kitdb: nil replica file transport context")
	}
	transport.operationMu.Lock()
	defer transport.operationMu.Unlock()
	batches, batchScan, err := transport.scanBatchFiles(ctx)
	if err != nil {
		return result, err
	}
	acks, ackScan, err := transport.scanAcknowledgementFiles(ctx)
	if err != nil {
		return result, err
	}
	_ = batches
	_ = acks
	for _, scan := range []replicaTransportScan{batchScan, ackScan} {
		for _, temporary := range scan.temporary {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if err := os.Remove(temporary.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return result, fmt.Errorf("kitdb: remove replica staging %q: %w", temporary.path, err)
			}
			result.Files++
			result.Bytes += temporary.bytes
		}
	}
	if len(batchScan.temporary) != 0 {
		if err := syncDirectory(transport.batchPath); err != nil {
			return result, fmt.Errorf("kitdb: sync replica batch cleanup: %w", err)
		}
	}
	if len(ackScan.temporary) != 0 {
		if err := syncDirectory(transport.ackPath); err != nil {
			return result, fmt.Errorf("kitdb: sync replica acknowledgement cleanup: %w", err)
		}
	}
	return result, nil
}

func (transport *ReplicaFileTransport) publishAcknowledgementLocked(
	ctx context.Context,
	acknowledgement ReplicaAcknowledgement,
) (string, error) {
	filename := replicaAcknowledgementFilename(acknowledgement.Cursor.DatabaseID, acknowledgement.Cursor)
	destination := filepath.Join(transport.ackPath, filename)
	if existing, err := readMatchingReplicaAcknowledgement(ctx, destination, acknowledgement); existing || err != nil {
		if existing && err == nil {
			err = syncReplicaTransportPublication(transport.ackPath, "acknowledgement")
		}
		return destination, err
	}
	stats, err := transport.statsLocked(ctx)
	if err != nil {
		return destination, err
	}
	if stats.PendingAcknowledgements >= transport.limits.MaxPendingBatches ||
		ReplicaAcknowledgementMessageBytes > transport.limits.MaxPendingBytes-stats.PendingBytes {
		return destination, errors.Join(ErrReplicaTransportLimit, fmt.Errorf("kitdb: replica mailbox acknowledgement limit reached"))
	}

	temporary, err := os.CreateTemp(transport.ackPath, replicaFileTemporaryMark+"ack-*.tmp")
	if err != nil {
		return destination, fmt.Errorf("kitdb: create replica acknowledgement staging: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return destination, fmt.Errorf("kitdb: set replica acknowledgement permissions: %w", err)
	}
	written, err := WriteReplicaAcknowledgementMessage(ctx, temporary, acknowledgement)
	if err != nil {
		return destination, err
	}
	if written != ReplicaAcknowledgementMessageBytes {
		return destination, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica acknowledgement staging has %d bytes", written))
	}
	if err := temporary.Sync(); err != nil {
		return destination, fmt.Errorf("kitdb: sync replica acknowledgement staging: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return destination, fmt.Errorf("kitdb: close replica acknowledgement staging: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return destination, err
	}
	if err := os.Link(temporaryPath, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, existingErr := readMatchingReplicaAcknowledgement(ctx, destination, acknowledgement)
			if existing && existingErr == nil {
				existingErr = syncReplicaTransportPublication(transport.ackPath, "acknowledgement")
			}
			return destination, existingErr
		}
		return destination, fmt.Errorf("kitdb: publish replica acknowledgement: %w", err)
	}
	removeErr := os.Remove(temporaryPath)
	if removeErr == nil {
		temporaryPath = ""
	}
	syncErr := syncDirectory(transport.ackPath)
	if syncErr != nil {
		syncErr = errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: sync replica acknowledgement directory: %w", syncErr))
	}
	if removeErr != nil {
		removeErr = fmt.Errorf("kitdb: remove replica acknowledgement staging link: %w", removeErr)
	}
	return destination, errors.Join(removeErr, syncErr)
}

func (transport *ReplicaFileTransport) readExistingBatch(
	ctx context.Context,
	path string,
	expected ReplicaBatch,
) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("kitdb: inspect existing replica batch: %w", err)
	}
	existing, err := readReplicaBatchFile(ctx, path)
	if err != nil {
		return false, err
	}
	if !replicaBatchesEqual(existing, expected) {
		return false, errors.Join(ErrReplicaTransportConflict, fmt.Errorf("kitdb: replica batch destination already contains a different message"))
	}
	return true, nil
}

func (transport *ReplicaFileTransport) statsLocked(ctx context.Context) (ReplicaFileTransportStats, error) {
	_, batchScan, err := transport.scanBatchFiles(ctx)
	if err != nil {
		return ReplicaFileTransportStats{}, err
	}
	_, ackScan, err := transport.scanAcknowledgementFiles(ctx)
	if err != nil {
		return ReplicaFileTransportStats{}, err
	}
	if err := transport.validateScans(batchScan, ackScan); err != nil {
		return ReplicaFileTransportStats{}, err
	}
	return ReplicaFileTransportStats{
		PendingBatches:          batchScan.published,
		PendingAcknowledgements: ackScan.published,
		StagingFiles:            batchScan.staging + ackScan.staging,
		PendingBytes:            batchScan.bytes + ackScan.bytes,
	}, nil
}

func (transport *ReplicaFileTransport) validateScans(batch, acknowledgement replicaTransportScan) error {
	if batch.published > transport.limits.MaxPendingBatches || acknowledgement.published > transport.limits.MaxPendingBatches {
		return errors.Join(ErrReplicaTransportLimit, fmt.Errorf("kitdb: replica mailbox file count exceeds configured limit"))
	}
	if batch.bytes > transport.limits.MaxPendingBytes ||
		acknowledgement.bytes > transport.limits.MaxPendingBytes-batch.bytes {
		return errors.Join(ErrReplicaTransportLimit, fmt.Errorf("kitdb: replica mailbox byte count exceeds configured limit"))
	}
	return nil
}

func (transport *ReplicaFileTransport) scanBatchFiles(ctx context.Context) ([]replicaBatchTransportFile, replicaTransportScan, error) {
	entries, scan, err := scanReplicaTransportDirectory(ctx, transport.batchPath, replicaFileBatchSuffix)
	if err != nil {
		return nil, scan, err
	}
	files := make([]replicaBatchTransportFile, 0, len(entries))
	for _, entry := range entries {
		parsed, err := parseReplicaBatchTransportFile(entry)
		if err != nil {
			return nil, scan, err
		}
		files = append(files, parsed)
	}
	sort.Slice(files, func(left, right int) bool {
		if files[left].from.Transaction != files[right].from.Transaction {
			return files[left].from.Transaction < files[right].from.Transaction
		}
		return files[left].name < files[right].name
	})
	return files, scan, nil
}

func (transport *ReplicaFileTransport) scanAcknowledgementFiles(ctx context.Context) ([]replicaAcknowledgementTransportFile, replicaTransportScan, error) {
	entries, scan, err := scanReplicaTransportDirectory(ctx, transport.ackPath, replicaFileAckSuffix)
	if err != nil {
		return nil, scan, err
	}
	files := make([]replicaAcknowledgementTransportFile, 0, len(entries))
	for _, entry := range entries {
		parsed, err := parseReplicaAcknowledgementTransportFile(entry)
		if err != nil {
			return nil, scan, err
		}
		files = append(files, parsed)
	}
	sort.Slice(files, func(left, right int) bool {
		if files[left].cursor.Transaction != files[right].cursor.Transaction {
			return files[left].cursor.Transaction < files[right].cursor.Transaction
		}
		return files[left].name < files[right].name
	})
	return files, scan, nil
}

type replicaTransportDirectoryEntry struct {
	name  string
	path  string
	bytes uint64
}

type replicaTransportScan struct {
	published uint32
	staging   uint32
	bytes     uint64
	temporary []replicaTransportDirectoryEntry
}

func scanReplicaTransportDirectory(
	ctx context.Context,
	path string,
	publishedSuffix string,
) ([]replicaTransportDirectoryEntry, replicaTransportScan, error) {
	var scan replicaTransportScan
	directory, err := os.Open(path)
	if err != nil {
		return nil, scan, fmt.Errorf("kitdb: open replica transport directory: %w", err)
	}
	defer directory.Close()
	entryLimit := int(MaxReplicaFilePendingBatches) + replicaFileStagingAllowance
	entries := make([]replicaTransportDirectoryEntry, 0, min(entryLimit, 128))
	seen := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, scan, err
		}
		page, readErr := directory.ReadDir(128)
		for _, item := range page {
			seen++
			if seen > entryLimit {
				return nil, scan, errors.Join(ErrReplicaTransportLimit, fmt.Errorf("kitdb: replica transport directory has more than %d entries", entryLimit))
			}
			info, err := item.Info()
			if err != nil {
				return nil, scan, fmt.Errorf("kitdb: inspect replica transport entry %q: %w", item.Name(), err)
			}
			if !info.Mode().IsRegular() {
				return nil, scan, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica transport entry %q is not a regular file", item.Name()))
			}
			if info.Size() < 0 {
				return nil, scan, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica transport entry %q has a negative size", item.Name()))
			}
			entryBytes := uint64(info.Size())
			if entryBytes > ^uint64(0)-scan.bytes {
				return nil, scan, ErrReplicaTransportLimit
			}
			scan.bytes += entryBytes
			entry := replicaTransportDirectoryEntry{
				name: item.Name(), path: filepath.Join(path, item.Name()), bytes: entryBytes,
			}
			switch {
			case strings.HasPrefix(item.Name(), replicaFileTemporaryMark) && strings.HasSuffix(item.Name(), ".tmp"):
				scan.staging++
				scan.temporary = append(scan.temporary, entry)
			case strings.HasSuffix(item.Name(), publishedSuffix):
				if entryBytes > MaxReplicaBatchMessageBytes {
					return nil, scan, errors.Join(ErrReplicaTransportLimit, fmt.Errorf("kitdb: replica transport entry %q is too large", item.Name()))
				}
				scan.published++
				entries = append(entries, entry)
			default:
				return nil, scan, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: unmanaged replica transport entry %q", item.Name()))
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, scan, fmt.Errorf("kitdb: read replica transport directory: %w", readErr)
		}
	}
	return entries, scan, nil
}

type replicaBatchTransportFile struct {
	name       string
	path       string
	databaseID string
	from       HistoryCursor
	to         HistoryCursor
}

type replicaAcknowledgementTransportFile struct {
	name       string
	path       string
	databaseID string
	cursor     HistoryCursor
}

func replicaBatchFilename(databaseID string, from, to HistoryCursor) string {
	return fmt.Sprintf(
		"%s-%016x-%08x-%016x-%08x%s",
		databaseID, from.Transaction, from.Checksum, to.Transaction, to.Checksum, replicaFileBatchSuffix,
	)
}

func replicaAcknowledgementFilename(databaseID string, cursor HistoryCursor) string {
	return fmt.Sprintf("%s-%016x-%08x%s", databaseID, cursor.Transaction, cursor.Checksum, replicaFileAckSuffix)
}

func parseReplicaBatchTransportFile(entry replicaTransportDirectoryEntry) (replicaBatchTransportFile, error) {
	base := strings.TrimSuffix(entry.name, replicaFileBatchSuffix)
	parts := strings.Split(base, "-")
	if len(parts) != 5 {
		return replicaBatchTransportFile{}, replicaMessageError("invalid batch filename %q", entry.name)
	}
	if err := validateReplicaDatabaseID(parts[0]); err != nil {
		return replicaBatchTransportFile{}, err
	}
	fromTransaction, err := parseReplicaFilenameHex(parts[1], 16)
	if err != nil {
		return replicaBatchTransportFile{}, replicaMessageError("invalid batch filename %q", entry.name)
	}
	fromChecksum, err := parseReplicaFilenameHex(parts[2], 8)
	if err != nil {
		return replicaBatchTransportFile{}, replicaMessageError("invalid batch filename %q", entry.name)
	}
	toTransaction, err := parseReplicaFilenameHex(parts[3], 16)
	if err != nil {
		return replicaBatchTransportFile{}, replicaMessageError("invalid batch filename %q", entry.name)
	}
	toChecksum, err := parseReplicaFilenameHex(parts[4], 8)
	if err != nil {
		return replicaBatchTransportFile{}, replicaMessageError("invalid batch filename %q", entry.name)
	}
	return replicaBatchTransportFile{
		name: entry.name, path: entry.path, databaseID: parts[0],
		from: HistoryCursor{DatabaseID: parts[0], Transaction: fromTransaction, Checksum: uint32(fromChecksum)},
		to:   HistoryCursor{DatabaseID: parts[0], Transaction: toTransaction, Checksum: uint32(toChecksum)},
	}, nil
}

func parseReplicaAcknowledgementTransportFile(entry replicaTransportDirectoryEntry) (replicaAcknowledgementTransportFile, error) {
	base := strings.TrimSuffix(entry.name, replicaFileAckSuffix)
	parts := strings.Split(base, "-")
	if len(parts) != 3 {
		return replicaAcknowledgementTransportFile{}, replicaMessageError("invalid acknowledgement filename %q", entry.name)
	}
	if err := validateReplicaDatabaseID(parts[0]); err != nil {
		return replicaAcknowledgementTransportFile{}, err
	}
	transaction, err := parseReplicaFilenameHex(parts[1], 16)
	if err != nil {
		return replicaAcknowledgementTransportFile{}, replicaMessageError("invalid acknowledgement filename %q", entry.name)
	}
	checksum, err := parseReplicaFilenameHex(parts[2], 8)
	if err != nil {
		return replicaAcknowledgementTransportFile{}, replicaMessageError("invalid acknowledgement filename %q", entry.name)
	}
	return replicaAcknowledgementTransportFile{
		name: entry.name, path: entry.path, databaseID: parts[0],
		cursor: HistoryCursor{DatabaseID: parts[0], Transaction: transaction, Checksum: uint32(checksum)},
	}, nil
}

func parseReplicaFilenameHex(value string, width int) (uint64, error) {
	if len(value) != width || value != strings.ToLower(value) {
		return 0, fmt.Errorf("invalid hexadecimal width")
	}
	parsed, err := strconv.ParseUint(value, 16, width*4)
	if err != nil {
		return 0, err
	}
	if fmt.Sprintf("%0*x", width, parsed) != value {
		return 0, fmt.Errorf("non-canonical hexadecimal value")
	}
	return parsed, nil
}

func readReplicaBatchFile(ctx context.Context, path string) (ReplicaBatch, error) {
	file, _, err := openReplicaTransportFile(path, int64(MaxReplicaBatchMessageBytes))
	if err != nil {
		return ReplicaBatch{}, err
	}
	batch, decodeErr := ReadReplicaBatchMessage(ctx, file, ReplicaBatchLimits{
		MaxTransactions: MaxReplicaBatchTransactions,
		MaxBytes:        MaxReplicaBatchBytes,
	})
	return batch, errors.Join(decodeErr, file.Close())
}

func readReplicaAcknowledgementFile(ctx context.Context, path string) (ReplicaAcknowledgement, error) {
	file, size, err := openReplicaTransportFile(path, int64(ReplicaAcknowledgementMessageBytes))
	if err != nil {
		return ReplicaAcknowledgement{}, err
	}
	if size != int64(ReplicaAcknowledgementMessageBytes) {
		_ = file.Close()
		return ReplicaAcknowledgement{}, replicaMessageError("acknowledgement file has %d bytes, want %d", size, ReplicaAcknowledgementMessageBytes)
	}
	acknowledgement, decodeErr := ReadReplicaAcknowledgementMessage(ctx, file)
	return acknowledgement, errors.Join(decodeErr, file.Close())
}

func openReplicaTransportFile(path string, maximum int64) (*os.File, int64, error) {
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("kitdb: inspect replica transport file: %w", err)
	}
	if !linkInfo.Mode().IsRegular() {
		return nil, 0, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica transport path %q is not a regular file", path))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("kitdb: open replica transport file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("kitdb: stat replica transport file: %w", err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(linkInfo, info) {
		_ = file.Close()
		return nil, 0, errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica transport file changed while opening"))
	}
	if info.Size() < 0 || info.Size() > maximum {
		_ = file.Close()
		return nil, 0, errors.Join(ErrReplicaTransportLimit, fmt.Errorf("kitdb: replica transport file has %d bytes", info.Size()))
	}
	return file, info.Size(), nil
}

func readMatchingReplicaAcknowledgement(
	ctx context.Context,
	path string,
	expected ReplicaAcknowledgement,
) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("kitdb: inspect existing replica acknowledgement: %w", err)
	}
	existing, err := readReplicaAcknowledgementFile(ctx, path)
	if err != nil {
		return false, err
	}
	if existing != expected {
		return false, errors.Join(ErrReplicaTransportConflict, fmt.Errorf("kitdb: replica acknowledgement destination contains a different message"))
	}
	return true, nil
}

func validateReplicaBatchFilename(file replicaBatchTransportFile, batch ReplicaBatch) error {
	if batch.DatabaseID != file.databaseID || batch.From != file.from || batch.To != file.to {
		return errors.Join(ErrReplicaTransportConflict, fmt.Errorf("kitdb: replica batch filename does not match its message"))
	}
	return nil
}

func replicaBatchesEqual(left, right ReplicaBatch) bool {
	if left.Version != right.Version || left.DatabaseID != right.DatabaseID || left.From != right.From ||
		left.To != right.To || left.SourceBoundary != right.SourceBoundary || left.Bytes != right.Bytes ||
		len(left.Transactions) != len(right.Transactions) {
		return false
	}
	for index := range left.Transactions {
		leftEvent := left.Transactions[index]
		rightEvent := right.Transactions[index]
		if leftEvent.Transaction != rightEvent.Transaction || leftEvent.Checksum != rightEvent.Checksum ||
			len(leftEvent.Operations) != len(rightEvent.Operations) {
			return false
		}
		for operationIndex := range leftEvent.Operations {
			leftOperation := leftEvent.Operations[operationIndex]
			rightOperation := rightEvent.Operations[operationIndex]
			if leftOperation.Kind != rightOperation.Kind || !bytes.Equal(leftOperation.Key, rightOperation.Key) ||
				!bytes.Equal(leftOperation.Value, rightOperation.Value) {
				return false
			}
		}
	}
	return true
}

func replicaFilePublication(batch ReplicaBatch) ReplicaFilePublication {
	return ReplicaFilePublication{
		DatabaseID: batch.DatabaseID, From: batch.From, To: batch.To,
		SourceBoundary: batch.SourceBoundary, Transactions: uint32(len(batch.Transactions)),
	}
}

func removeReplicaTransportFile(path, directory string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("kitdb: remove replica transport file %q: %w", path, err)
	}
	if err := syncDirectory(directory); err != nil {
		return errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: sync replica transport cleanup: %w", err))
	}
	return nil
}

func ensureReplicaTransportDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("kitdb: create replica transport directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("kitdb: inspect replica transport directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("kitdb: replica transport path %q is not a real directory", path)
	}
	return nil
}

func normalizeReplicaFileTransportLimits(limits ReplicaFileTransportLimits) (ReplicaFileTransportLimits, error) {
	if limits.MaxPendingBatches == 0 {
		limits.MaxPendingBatches = DefaultReplicaFilePendingBatches
	}
	if limits.MaxPendingBytes == 0 {
		limits.MaxPendingBytes = DefaultReplicaFilePendingBytes
	}
	if limits.MaxPendingBatches > MaxReplicaFilePendingBatches {
		return ReplicaFileTransportLimits{}, errors.Join(ErrReplicaTransportLimit, fmt.Errorf("kitdb: replica mailbox batch limit %d exceeds maximum %d", limits.MaxPendingBatches, MaxReplicaFilePendingBatches))
	}
	minimumMessageBytes := uint64(replicaBatchMessageHeaderSize+replicaBatchMessageTrailerSize) + ReplicaAcknowledgementMessageBytes
	if limits.MaxPendingBytes < minimumMessageBytes {
		return ReplicaFileTransportLimits{}, errors.Join(ErrReplicaTransportLimit, fmt.Errorf("kitdb: replica mailbox byte limit %d is below minimum %d", limits.MaxPendingBytes, minimumMessageBytes))
	}
	if limits.MaxPendingBytes > MaxReplicaFilePendingBytes {
		return ReplicaFileTransportLimits{}, errors.Join(ErrReplicaTransportLimit, fmt.Errorf("kitdb: replica mailbox byte limit %d exceeds maximum %d", limits.MaxPendingBytes, MaxReplicaFilePendingBytes))
	}
	return limits, nil
}

func reservedReplicaTransportBytes(stats ReplicaFileTransportStats) (uint64, error) {
	reserved := stats.PendingBytes
	if stats.PendingBatches <= stats.PendingAcknowledgements {
		return reserved, nil
	}
	missing := uint64(stats.PendingBatches - stats.PendingAcknowledgements)
	acknowledgementBytes := missing * ReplicaAcknowledgementMessageBytes
	if acknowledgementBytes > ^uint64(0)-reserved {
		return 0, ErrReplicaTransportLimit
	}
	return reserved + acknowledgementBytes, nil
}

func syncReplicaTransportPublication(directory, kind string) error {
	if err := syncDirectory(directory); err != nil {
		return errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: sync existing replica %s directory entry: %w", kind, err))
	}
	return nil
}
