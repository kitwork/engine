package kitdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const historyBaseAnchorSuffix = ".kbase"

// RecoveryPoint is one exact transaction selected from a wall-clock target.
// Transaction order remains authoritative; CommittedAt is the durable lookup
// key carried by the matching WAL frame.
type RecoveryPoint struct {
	Transaction uint64
	Checksum    uint32
	CommittedAt time.Time
}

// TimeRestoreResult describes a restore requested by time and the exact
// transaction boundary selected for it.
type TimeRestoreResult struct {
	RestoreResult
	RequestedAt time.Time
	ResolvedAt  time.Time
}

// ForkToTime creates an independently writable database from the last
// transaction committed at or before target. The fork receives a new database
// identity while preserving the selected transaction and checksum as its
// provenance boundary.
func (db *DB) ForkToTime(ctx context.Context, destination string, target time.Time) (TimeRestoreResult, error) {
	var result TimeRestoreResult
	if ctx == nil {
		return result, fmt.Errorf("kitdb: nil recovery context")
	}
	resolved, err := resolveBackupAnchorPath(destination)
	if err != nil {
		return result, err
	}
	if err := validateRestoreDestination(resolved); err != nil {
		return result, err
	}

	temporary, err := reserveRecoveryTemporaryPath(filepath.Dir(resolved))
	if err != nil {
		return result, err
	}
	defer os.Remove(temporary)

	result, err = db.RestoreToTime(ctx, temporary, target)
	if err != nil {
		return TimeRestoreResult{}, err
	}
	main, err := readMainSnapshotWithCache(temporary, 0)
	if err != nil {
		return TimeRestoreResult{}, err
	}
	defer main.close()

	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return TimeRestoreResult{}, fmt.Errorf("kitdb: create fork identity: %w", err)
	}
	generation := uint64(1)
	if result.Transaction == 0 {
		generation = 0
	}
	stagingPath, err := prepareCompactedGeneration(
		resolved,
		identity,
		result.Transaction,
		result.BoundaryChecksum,
		generation,
		func(emit func(key, value []byte) error) error {
			return walkMergedRows(main, nil, func(key, value []byte) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				return emit(key, value)
			})
		},
	)
	if err != nil {
		return TimeRestoreResult{}, err
	}
	defer os.Remove(stagingPath)

	fork, err := verifyBackupAnchorPath(ctx, stagingPath)
	if err != nil {
		return TimeRestoreResult{}, err
	}
	if fork.DatabaseID != hex.EncodeToString(identity[:]) ||
		fork.DatabaseID == result.DatabaseID ||
		fork.Transaction != result.Transaction ||
		fork.BoundaryChecksum != result.BoundaryChecksum ||
		fork.Records != result.Records {
		return TimeRestoreResult{}, corruptFileAt(stagingPath, 0, "recovery fork does not match its selected source boundary")
	}
	if err := ctx.Err(); err != nil {
		return TimeRestoreResult{}, err
	}
	if err := publishRestoreImage(stagingPath, resolved); err != nil {
		return TimeRestoreResult{}, err
	}
	fork.Path = resolved
	result.RestoreResult.BackupAnchor = fork
	return result, nil
}

func reserveRecoveryTemporaryPath(directory string) (string, error) {
	temporary, err := os.CreateTemp(directory, ".kitdb-recovery-source-*.tmp")
	if err != nil {
		return "", fmt.Errorf("kitdb: reserve recovery staging path: %w", err)
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("kitdb: close recovery staging reservation: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("kitdb: release recovery staging reservation: %w", err)
	}
	return path, nil
}

// RestoreToTime creates a new standalone database at the last transaction
// committed at or before target. Retained history must have been enabled no
// later than target. The live database is never overwritten.
func (db *DB) RestoreToTime(ctx context.Context, destination string, target time.Time) (TimeRestoreResult, error) {
	var result TimeRestoreResult
	if db == nil {
		return result, fmt.Errorf("kitdb: nil recovery database")
	}
	if ctx == nil {
		return result, fmt.Errorf("kitdb: nil recovery context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if target.IsZero() {
		return result, fmt.Errorf("kitdb: recovery target time is zero")
	}
	target = target.UTC()
	result.RequestedAt = target

	boundary, err := db.Checkpoint()
	if err != nil {
		return result, err
	}

	// Pruning and another history publication cannot change the selected file
	// set while it is being verified and replayed. Ordinary commits continue in
	// the newly rotated WAL.
	db.historyMu.Lock()
	defer db.historyMu.Unlock()

	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return result, err
	}
	history := db.history
	db.mu.RUnlock()
	if history == nil {
		return result, ErrHistoryDisabled
	}

	metadata, err := readHistoryMetadata(filepath.Join(history.path, historyMetadataFilename))
	if err != nil {
		return result, err
	}
	if metadata.identity != db.identity {
		return result, corruptFileAt(history.path, 16, "recovery history belongs to a different database")
	}
	if metadata.baseCommitTime == 0 {
		return result, ErrRecoveryTimeUnavailable
	}
	if err := ensureHistoryBaseAnchor(ctx, history.path, nil, metadata); err != nil {
		return result, err
	}

	point, err := resolveRecoveryPoint(ctx, history.path, metadata, boundary, target)
	if err != nil {
		return result, err
	}
	anchorPath := historyBaseAnchorPath(history.path, metadata.baseTx)
	restored, err := RestoreToTransaction(ctx, anchorPath, history.path, destination, point.Transaction)
	if err != nil {
		return result, err
	}
	result.RestoreResult = restored
	result.ResolvedAt = point.CommittedAt
	return result, nil
}

func resolveRecoveryPoint(
	ctx context.Context,
	historyPath string,
	metadata historyMetadata,
	boundary uint64,
	target time.Time,
) (RecoveryPoint, error) {
	baseTime := commitTimeValue(metadata.baseCommitTime)
	if target.Before(baseTime) {
		return RecoveryPoint{}, errors.Join(
			ErrRecoveryTargetTooOld,
			fmt.Errorf("kitdb: earliest recoverable time is %s", baseTime.Format(time.RFC3339Nano)),
		)
	}
	point := RecoveryPoint{
		Transaction: metadata.baseTx,
		Checksum:    metadata.baseChecksum,
		CommittedAt: baseTime,
	}
	if boundary == metadata.baseTx {
		return point, nil
	}
	if boundary < metadata.baseTx {
		return RecoveryPoint{}, errors.Join(
			ErrHistoryGap,
			fmt.Errorf("kitdb: recovery boundary %d precedes retained base %d", boundary, metadata.baseTx),
		)
	}

	listed, err := listHistorySegments(historyPath)
	if err != nil {
		return RecoveryPoint{}, err
	}
	segments, _, err := activeHistorySegments(metadata.baseTx, listed)
	if err != nil {
		return RecoveryPoint{}, err
	}
	expectedTx := metadata.baseTx
	expectedChecksum := metadata.baseChecksum
	lastCommitTime := metadata.baseCommitTime
	for _, segment := range segments {
		if segment.first > boundary {
			break
		}
		if segment.last > boundary {
			return RecoveryPoint{}, errors.Join(
				ErrHistoryGap,
				fmt.Errorf("kitdb: history segment %q crosses recovery boundary %d", segment.name, boundary),
			)
		}
		info, err := inspectHistorySegmentFrames(
			segment.path,
			historySegmentExpectation{
				identity: metadata.identity,
				baseTx:   expectedTx, baseChecksum: expectedChecksum, verifyBaseChecksum: true,
				lastTx: segment.last, bytes: segment.bytes,
			},
			func(transaction uint64, checksum uint32, committedAt int64, _ []operation) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if committedAt == 0 {
					return errors.Join(
						ErrRecoveryTimeUnavailable,
						fmt.Errorf("kitdb: transaction %d predates durable commit timestamps", transaction),
					)
				}
				if committedAt <= lastCommitTime {
					return corruptFileAt(segment.path, 0, "commit timestamp %d does not follow %d", committedAt, lastCommitTime)
				}
				lastCommitTime = committedAt
				committedTime := commitTimeValue(committedAt)
				if !committedTime.After(target) {
					point = RecoveryPoint{
						Transaction: transaction,
						Checksum:    checksum,
						CommittedAt: committedTime,
					}
				}
				return nil
			},
		)
		if err != nil {
			return RecoveryPoint{}, err
		}
		expectedTx = info.lastTx
		expectedChecksum = info.lastChecksum
	}
	if expectedTx != boundary {
		return RecoveryPoint{}, errors.Join(
			ErrHistoryGap,
			fmt.Errorf("kitdb: retained history ends at transaction %d before recovery boundary %d", expectedTx, boundary),
		)
	}
	return point, nil
}

func historyBaseAnchorPath(historyPath string, transaction uint64) string {
	return filepath.Join(historyPath, fmt.Sprintf("base-%020d%s", transaction, historyBaseAnchorSuffix))
}

func prepareAdvancedHistoryBaseAnchor(
	ctx context.Context,
	historyPath string,
	metadata historyMetadata,
	target uint64,
) error {
	if target == metadata.baseTx {
		return nil
	}
	sourcePath := historyBaseAnchorPath(historyPath, metadata.baseTx)
	destinationPath := historyBaseAnchorPath(historyPath, target)
	if existing, err := VerifyBackupAnchor(ctx, destinationPath); err == nil {
		if existing.DatabaseID != hex.EncodeToString(metadata.identity[:]) || existing.Transaction != target {
			return corruptFileAt(destinationPath, 0, "advanced history base anchor does not match transaction %d", target)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := RestoreToTransaction(ctx, sourcePath, historyPath, destinationPath, target); err != nil {
		return fmt.Errorf("kitdb: advance history recovery anchor: %w", err)
	}
	return nil
}

func cleanupHistoryBaseAnchors(ctx context.Context, historyPath string, keep uint64) error {
	entries, err := os.ReadDir(historyPath)
	if err != nil {
		return fmt.Errorf("kitdb: list history recovery anchors: %w", err)
	}
	removed := false
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "base-") || !strings.HasSuffix(name, historyBaseAnchorSuffix) {
			continue
		}
		number := strings.TrimSuffix(strings.TrimPrefix(name, "base-"), historyBaseAnchorSuffix)
		transaction, err := strconv.ParseUint(number, 10, 64)
		if err != nil || transaction >= keep {
			continue
		}
		if err := os.Remove(filepath.Join(historyPath, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("kitdb: remove retired history recovery anchor %q: %w", name, err)
		}
		removed = true
	}
	if removed {
		if err := syncDirectory(historyPath); err != nil {
			return fmt.Errorf("kitdb: sync history recovery-anchor cleanup: %w", err)
		}
	}
	return nil
}

func ensureHistoryBaseAnchor(ctx context.Context, historyPath string, main *mainImage, metadata historyMetadata) error {
	anchorPath := historyBaseAnchorPath(historyPath, metadata.baseTx)
	anchor, err := VerifyBackupAnchor(ctx, anchorPath)
	if err == nil {
		if anchor.DatabaseID != hex.EncodeToString(metadata.identity[:]) ||
			anchor.Transaction != metadata.baseTx ||
			anchor.BoundaryChecksum != metadata.baseChecksum {
			return corruptFileAt(anchorPath, 0, "history base anchor does not match retained metadata")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if main == nil || main.identity != metadata.identity ||
		main.transaction != metadata.baseTx || main.boundaryChecksum != metadata.baseChecksum {
		return errors.Join(
			ErrRecoveryTimeUnavailable,
			fmt.Errorf("kitdb: retained base transaction %d has no verified anchor", metadata.baseTx),
		)
	}

	generation := uint64(1)
	if metadata.baseTx == 0 {
		generation = 0
	}
	stagingPath, err := prepareCompactedGeneration(
		anchorPath,
		metadata.identity,
		metadata.baseTx,
		metadata.baseChecksum,
		generation,
		func(emit func(key, value []byte) error) error {
			return walkMergedRows(main, nil, func(key, value []byte) error {
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
	if _, err := verifyBackupAnchorPath(ctx, stagingPath); err != nil {
		return err
	}
	if err := publishBackupAnchor(stagingPath, anchorPath); err != nil {
		if !errors.Is(err, ErrBackupExists) {
			return err
		}
		return ensureHistoryBaseAnchor(ctx, historyPath, main, metadata)
	}
	return nil
}
