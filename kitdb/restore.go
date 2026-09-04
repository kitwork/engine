package kitdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	restoreOverlayBytesLimit    int64 = 32 << 20
	restoreOverlayMutationLimit       = 1 << 18
	restoreMutationOverhead     int64 = 32
)

// RestoreResult describes one immutable restored main file. AnchorTransaction
// is the source anchor boundary; Transaction is the exact requested boundary.
type RestoreResult struct {
	BackupAnchor
	AnchorTransaction   uint64
	AppliedTransactions uint64
	HistorySegments     int
}

// RestoreToTransaction builds a new standalone main file from a verified
// backup anchor and the source retained-history directory. The destination is
// never overwritten and is published only after the complete restored image
// has been verified. Passing an empty history path is valid when transaction
// equals the anchor transaction.
func RestoreToTransaction(ctx context.Context, anchorPath, historyPath, destinationPath string, transaction uint64) (RestoreResult, error) {
	if ctx == nil {
		return RestoreResult{}, fmt.Errorf("kitdb: nil restore context")
	}
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, err
	}
	destination, err := resolveBackupAnchorPath(destinationPath)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := validateRestoreDestination(destination); err != nil {
		return RestoreResult{}, err
	}

	anchor, err := VerifyBackupAnchor(ctx, anchorPath)
	if err != nil {
		return RestoreResult{}, err
	}
	if anchor.FormatVersion != mainFormatVersion {
		return RestoreResult{}, fmt.Errorf("kitdb: restore requires a format-v%d backup anchor", mainFormatVersion)
	}
	if sameBackupPath(anchor.Path, destination) {
		return RestoreResult{}, fmt.Errorf("kitdb: restore destination is the source backup anchor")
	}
	if transaction < anchor.Transaction {
		return RestoreResult{}, fmt.Errorf("kitdb: restore transaction %d precedes anchor transaction %d", transaction, anchor.Transaction)
	}

	stagingPath, err := stageRestoreAnchor(ctx, anchor, destination)
	if err != nil {
		return RestoreResult{}, err
	}
	defer os.Remove(stagingPath)

	main, err := readMainSnapshotWithCache(stagingPath, 0)
	if err != nil {
		return RestoreResult{}, err
	}
	image := &restoreImage{
		path: stagingPath, main: main, overlay: make(map[string]rowMutation),
		transaction: anchor.Transaction, checksum: anchor.BoundaryChecksum,
	}
	defer image.close()

	historySegments := 0
	if transaction > anchor.Transaction {
		history, err := resolveRestoreHistoryPath(historyPath)
		if err != nil {
			return RestoreResult{}, err
		}
		historySegments, err = image.replayHistory(ctx, history, transaction)
		if err != nil {
			return RestoreResult{}, err
		}
	}
	if err := image.flush(ctx); err != nil {
		return RestoreResult{}, err
	}
	if image.transaction != transaction {
		return RestoreResult{}, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: restore ended at transaction %d before target %d", image.transaction, transaction))
	}
	if err := image.close(); err != nil {
		return RestoreResult{}, fmt.Errorf("kitdb: close restored staging image: %w", err)
	}

	restored, err := verifyBackupAnchorPath(ctx, stagingPath)
	if err != nil {
		return RestoreResult{}, err
	}
	if restored.DatabaseID != anchor.DatabaseID || restored.Transaction != transaction || restored.BoundaryChecksum != image.checksum {
		return RestoreResult{}, corruptFileAt(stagingPath, 0, "restored image does not match its source identity and target boundary")
	}
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, err
	}
	if err := publishRestoreImage(stagingPath, destination); err != nil {
		return RestoreResult{}, err
	}
	restored.Path = destination
	return RestoreResult{
		BackupAnchor:        restored,
		AnchorTransaction:   anchor.Transaction,
		AppliedTransactions: transaction - anchor.Transaction,
		HistorySegments:     historySegments,
	}, nil
}

type restoreImage struct {
	path         string
	main         *mainImage
	overlay      map[string]rowMutation
	overlayBytes int64
	transaction  uint64
	checksum     uint32
}

func (image *restoreImage) close() error {
	if image == nil || image.main == nil {
		return nil
	}
	err := image.main.close()
	image.main = nil
	return err
}

func (image *restoreImage) replayHistory(ctx context.Context, historyPath string, target uint64) (int, error) {
	metadataPath := filepath.Join(historyPath, historyMetadataFilename)
	metadata, err := readHistoryMetadata(metadataPath)
	if err != nil {
		return 0, err
	}
	if metadata.identity != image.main.identity {
		return 0, corruptFileAt(metadataPath, 16, "restore history belongs to a different database")
	}
	if image.transaction < metadata.baseTx {
		return 0, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: anchor transaction %d precedes retained history base %d", image.transaction, metadata.baseTx))
	}

	listed, err := listHistorySegments(historyPath)
	if err != nil {
		return 0, err
	}
	segments, _, err := activeHistorySegments(metadata.baseTx, listed)
	if err != nil {
		return 0, err
	}
	if err := requireHistoryNamesThrough(metadata.baseTx, segments, target); err != nil {
		return 0, err
	}

	inspected := 0
	for _, segment := range segments {
		if segment.last <= image.transaction {
			continue
		}
		if image.transaction == math.MaxUint64 || segment.first > image.transaction+1 {
			return inspected, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history segment %q starts after restore transaction %d", segment.name, image.transaction))
		}

		segmentStartTransaction := image.transaction
		segmentStartChecksum := image.checksum
		anchorVerified := segment.first == segmentStartTransaction+1
		expected := historySegmentExpectation{
			identity: image.main.identity, baseTx: segment.first - 1,
			lastTx: segment.last, bytes: segment.bytes,
		}
		if anchorVerified {
			expected.baseChecksum = segmentStartChecksum
			expected.verifyBaseChecksum = true
		}
		info, err := inspectHistorySegmentFrames(segment.path, expected, func(frameTransaction uint64, frameChecksum uint32, _ int64, operations []operation) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			switch {
			case frameTransaction < segmentStartTransaction:
				return nil
			case frameTransaction == segmentStartTransaction:
				if frameChecksum != segmentStartChecksum {
					return errors.Join(ErrHistoryGap, corruptFileAt(segment.path, 0, "anchor transaction %d checksum is %08x, want %08x", frameTransaction, frameChecksum, segmentStartChecksum))
				}
				anchorVerified = true
				return nil
			case frameTransaction > target:
				return nil
			case !anchorVerified:
				return errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history segment %q does not contain anchor transaction %d", segment.name, segmentStartTransaction))
			default:
				return image.applyTransaction(ctx, frameTransaction, frameChecksum, operations)
			}
		})
		if err != nil {
			return inspected, err
		}
		inspected++
		if !anchorVerified {
			return inspected, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history segment %q does not prove anchor transaction %d", segment.name, segmentStartTransaction))
		}
		if target <= segment.last {
			if image.transaction != target {
				return inspected, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history segment %q did not reach target transaction %d", segment.name, target))
			}
			return inspected, nil
		}
		if image.transaction != info.lastTx || image.checksum != info.lastChecksum {
			return inspected, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: restored history boundary does not match segment %q", segment.name))
		}
	}
	return inspected, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: retained history ends at transaction %d before target %d", image.transaction, target))
}

func (image *restoreImage) applyTransaction(ctx context.Context, transaction uint64, checksum uint32, operations []operation) error {
	if image.transaction == math.MaxUint64 || transaction != image.transaction+1 {
		return errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: restore received transaction %d after %d", transaction, image.transaction))
	}
	for _, operation := range operations {
		key := string(operation.key)
		if previous, found := image.overlay[key]; found {
			image.overlayBytes -= restoreMutationWeight(key, previous)
		}
		mutation := rowMutation{deleted: operation.kind == operationDelete}
		if operation.kind == operationPut {
			mutation.value = append([]byte(nil), operation.value...)
		}
		image.overlay[key] = mutation
		image.overlayBytes += restoreMutationWeight(key, mutation)
	}
	image.transaction = transaction
	image.checksum = checksum
	if image.overlayBytes >= restoreOverlayBytesLimit || len(image.overlay) >= restoreOverlayMutationLimit {
		return image.flush(ctx)
	}
	return nil
}

func restoreMutationWeight(key string, mutation rowMutation) int64 {
	return int64(len(key)+len(mutation.value)) + restoreMutationOverhead
}

func (image *restoreImage) flush(ctx context.Context) error {
	if len(image.overlay) == 0 {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	logicalRecords, err := countMergedRecordsContext(ctx, image.main, image.overlay)
	if err != nil {
		return err
	}
	if len(image.main.segments) < maxMainSegments {
		result, err := appendIncrementalGeneration(image.path, image.main, image.overlay, image.transaction, image.checksum, logicalRecords)
		if err != nil {
			return err
		}
		newMain, err := openAppendedGeneration(image.path, image.main, result, 0)
		if err != nil {
			return err
		}
		oldMain := image.main
		image.main = newMain
		if err := oldMain.close(); err != nil {
			return fmt.Errorf("kitdb: close previous restore generation: %w", err)
		}
	} else {
		if image.main.generation == math.MaxUint64 {
			return fmt.Errorf("kitdb: generation ID overflow")
		}
		stagingPath, err := prepareCompactedGeneration(
			image.path,
			image.main.identity,
			image.transaction,
			image.checksum,
			image.main.generation+1,
			func(emit func(key, value []byte) error) error {
				return walkMergedRows(image.main, image.overlay, func(key, value []byte) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					return emit(key, value)
				})
			},
		)
		if err != nil {
			return err
		}
		defer os.Remove(stagingPath)
		if err := image.close(); err != nil {
			return fmt.Errorf("kitdb: close restore generation before compaction: %w", err)
		}
		published, err := publishPreparedMainSnapshot(stagingPath, image.path)
		if err != nil {
			return err
		}
		if !published {
			return fmt.Errorf("kitdb: compacted restore generation was not published")
		}
		image.main, err = readMainSnapshotWithCache(image.path, 0)
		if err != nil {
			return err
		}
	}
	image.overlay = make(map[string]rowMutation)
	image.overlayBytes = 0
	return nil
}

func countMergedRecordsContext(ctx context.Context, main *mainImage, overlay map[string]rowMutation) (uint64, error) {
	records := main.records
	for key, mutation := range overlay {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		_, existed, err := main.get([]byte(key))
		if err != nil {
			return 0, err
		}
		switch {
		case mutation.deleted && existed:
			records--
		case !mutation.deleted && !existed:
			if records == math.MaxUint64 {
				return 0, fmt.Errorf("kitdb: logical record count overflow")
			}
			records++
		}
	}
	return records, nil
}

func requireHistoryNamesThrough(base uint64, segments []historySegment, target uint64) error {
	last := base
	for _, segment := range segments {
		if last == math.MaxUint64 || segment.first != last+1 {
			return errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history segment %q starts at %d after %d", segment.name, segment.first, last))
		}
		last = segment.last
		if last >= target {
			return nil
		}
	}
	return errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: retained history ends at transaction %d before target %d", last, target))
}

func stageRestoreAnchor(ctx context.Context, anchor BackupAnchor, destination string) (stagingPath string, returnErr error) {
	source, err := os.Open(anchor.Path)
	if err != nil {
		return "", fmt.Errorf("kitdb: open restore anchor: %w", err)
	}
	defer source.Close()

	temporary, err := os.CreateTemp(filepath.Dir(destination), ".kitdb-restore-*.tmp")
	if err != nil {
		return "", fmt.Errorf("kitdb: create restore staging: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	ready := false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, temporary.Close())
		}
		if !ready {
			returnErr = errors.Join(returnErr, os.Remove(temporaryPath))
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", fmt.Errorf("kitdb: set restore staging permissions: %w", err)
	}
	written, err := io.CopyBuffer(temporary, &backupContextReader{ctx: ctx, reader: source}, make([]byte, backupHashBufferSize))
	if err != nil {
		return "", fmt.Errorf("kitdb: copy restore anchor: %w", err)
	}
	if written != anchor.Bytes {
		return "", corruptFileAt(anchor.Path, written, "restore anchor copied %d bytes, want %d", written, anchor.Bytes)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("kitdb: sync restore staging: %w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return "", fmt.Errorf("kitdb: close restore staging: %w", err)
	}
	closed = true
	verified, err := verifyBackupAnchorPath(ctx, temporaryPath)
	if err != nil {
		return "", err
	}
	if !sameRestoreImage(verified, anchor) {
		return "", corruptFileAt(temporaryPath, 0, "staged restore anchor differs from its verified source")
	}
	ready = true
	return temporaryPath, nil
}

func sameRestoreImage(left, right BackupAnchor) bool {
	return left.DatabaseID == right.DatabaseID &&
		left.FormatVersion == right.FormatVersion &&
		left.Generation == right.Generation &&
		left.Transaction == right.Transaction &&
		left.BoundaryChecksum == right.BoundaryChecksum &&
		left.Records == right.Records &&
		left.Bytes == right.Bytes &&
		left.SHA256 == right.SHA256
}

func resolveRestoreHistoryPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("kitdb: empty restore history path")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("kitdb: resolve restore history path: %w", err)
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("kitdb: inspect restore history path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("kitdb: restore history path %q is not a directory", resolved)
	}
	return resolved, nil
}

func validateRestoreDestination(path string) error {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return errors.Join(ErrRestoreExists, fmt.Errorf("kitdb: restore destination %q already exists", path))
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("kitdb: inspect restore destination: %w", err)
	}
}

func publishRestoreImage(stagingPath, destination string) error {
	if err := os.Link(stagingPath, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.Join(ErrRestoreExists, fmt.Errorf("kitdb: restore destination %q already exists", destination))
		}
		return fmt.Errorf("kitdb: publish restored image: %w", err)
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: sync restored-image directory: %w", err))
	}
	return nil
}
