package kitdb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"time"
)

const (
	maximumHistoryRetentionAge   = 100 * 365 * 24 * time.Hour
	maximumHistoryRetentionBytes = int64(1 << 60)
)

// HistoryRetentionPolicy bounds retained history by durable commit age, active
// segment bytes, or both. Zero values disable the corresponding bound. Policy
// is process configuration; durable history metadata and pins remain the only
// recovery authority stored beside the database.
type HistoryRetentionPolicy struct {
	MaxAge   time.Duration
	MaxBytes int64
}

// Enabled reports whether at least one retention bound is configured.
func (policy HistoryRetentionPolicy) Enabled() bool {
	return policy.MaxAge != 0 || policy.MaxBytes != 0
}

// Validate checks resource bounds without opening a database.
func (policy HistoryRetentionPolicy) Validate() error {
	switch {
	case policy.MaxAge < 0:
		return errors.Join(ErrHistoryRetentionPolicy, fmt.Errorf("kitdb: history retention MaxAge cannot be negative"))
	case policy.MaxAge > maximumHistoryRetentionAge:
		return errors.Join(
			ErrHistoryRetentionPolicy,
			fmt.Errorf("kitdb: history retention MaxAge exceeds %s", maximumHistoryRetentionAge),
		)
	case policy.MaxBytes < 0:
		return errors.Join(ErrHistoryRetentionPolicy, fmt.Errorf("kitdb: history retention MaxBytes cannot be negative"))
	case policy.MaxBytes > maximumHistoryRetentionBytes:
		return errors.Join(
			ErrHistoryRetentionPolicy,
			fmt.Errorf("kitdb: history retention MaxBytes exceeds %d", maximumHistoryRetentionBytes),
		)
	default:
		return nil
	}
}

// HistoryRetentionResult describes one policy evaluation and its whole-segment
// prune. LimitedByPin is pressure evidence, not an error: the oldest durable pin
// always wins over configured age or byte limits.
type HistoryRetentionResult struct {
	DatabaseID          string
	Policy              HistoryRetentionPolicy
	EvaluatedAt         time.Time
	AgeCutoff           time.Time
	BeforeSegments      int
	BeforeBytes         int64
	DesiredSegments     int
	DesiredThrough      uint64
	AppliedSegments     int
	AppliedThrough      uint64
	PinnedBy            string
	PinnedAt            uint64
	LimitedByPin        bool
	RetainedSegments    int
	RetainedBytes       int64
	PhysicalBytes       int64
	BytesOverLimit      int64
	CleanupPendingBytes int64
	Prune               HistoryPruneResult
}

// EnforceHistoryRetention evaluates one bounded policy against verified history
// segment metadata and advances the durable base through complete segments only.
// It shares the same lock order and publication primitive as PruneHistory.
func (db *DB) EnforceHistoryRetention(
	ctx context.Context,
	policy HistoryRetentionPolicy,
	now time.Time,
) (HistoryRetentionResult, error) {
	result := HistoryRetentionResult{Policy: policy, EvaluatedAt: now.UTC()}
	if ctx == nil {
		return result, fmt.Errorf("kitdb: nil history retention context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := policy.Validate(); err != nil {
		return result, err
	}
	if !policy.Enabled() {
		return result, errors.Join(
			ErrHistoryRetentionPolicy,
			fmt.Errorf("kitdb: history retention requires MaxAge or MaxBytes"),
		)
	}
	if now.IsZero() {
		return result, errors.Join(
			ErrHistoryRetentionPolicy,
			fmt.Errorf("kitdb: history retention evaluation time is zero"),
		)
	}

	db.commitMu.Lock()
	defer db.commitMu.Unlock()
	db.historyMu.Lock()
	defer db.historyMu.Unlock()

	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return result, err
	}
	history := db.history
	identity := db.identity
	if history == nil {
		db.mu.RUnlock()
		return result, ErrHistoryDisabled
	}
	pins := cloneHistoryCursors(history.pins)
	db.mu.RUnlock()

	metadataPath := filepath.Join(history.path, historyMetadataFilename)
	metadata, err := readHistoryMetadata(metadataPath)
	if err != nil {
		return result, err
	}
	if metadata.identity != identity {
		return result, corruptFileAt(metadataPath, 16, "history belongs to a different database")
	}
	listed, err := listHistorySegments(history.path)
	if err != nil {
		return result, err
	}
	segments, _, err := activeHistorySegments(metadata.baseTx, listed)
	if err != nil {
		return result, err
	}
	result.DatabaseID = db.ID()
	result.BeforeSegments = len(segments)
	result.BeforeBytes = historySegmentsBytes(segments)

	ageSegments := 0
	if policy.MaxAge != 0 {
		result.AgeCutoff = now.UTC().Add(-policy.MaxAge)
		cutoff := result.AgeCutoff.UnixNano()
		expectedTx := metadata.baseTx
		expectedChecksum := metadata.baseChecksum
		previousCommitTime := metadata.baseCommitTime
		for index, segment := range segments {
			info, inspectErr := inspectHistorySegmentFrames(
				segment.path,
				historySegmentExpectation{
					identity: identity, baseTx: expectedTx,
					baseChecksum: expectedChecksum, verifyBaseChecksum: true,
					lastTx: segment.last, bytes: segment.bytes,
				},
				func(_ uint64, _ uint32, committedAt int64, _ []operation) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					if committedAt == 0 {
						return ErrRecoveryTimeUnavailable
					}
					return nil
				},
			)
			if inspectErr != nil {
				return result, inspectErr
			}
			if info.lastCommitTime == 0 {
				return result, ErrRecoveryTimeUnavailable
			}
			if previousCommitTime != 0 && info.lastCommitTime <= previousCommitTime {
				return result, corruptFileAt(
					segment.path,
					0,
					"history segment commit timestamp %d does not follow %d",
					info.lastCommitTime,
					previousCommitTime,
				)
			}
			expectedTx = info.lastTx
			expectedChecksum = info.lastChecksum
			previousCommitTime = info.lastCommitTime
			if info.lastCommitTime > cutoff {
				break
			}
			ageSegments = index + 1
		}
	}

	byteSegments := 0
	if policy.MaxBytes != 0 {
		remaining := result.BeforeBytes
		for byteSegments < len(segments) && remaining > policy.MaxBytes {
			remaining -= segments[byteSegments].bytes
			byteSegments++
		}
	}
	result.DesiredSegments = max(ageSegments, byteSegments)
	if result.DesiredSegments != 0 {
		result.DesiredThrough = segments[result.DesiredSegments-1].last
	} else {
		result.DesiredThrough = metadata.baseTx
	}

	result.AppliedSegments = result.DesiredSegments
	if pinName, pin, found := oldestHistoryPin(pins); found {
		result.PinnedBy = pinName
		result.PinnedAt = pin.Transaction
		allowed := 0
		for allowed < len(segments) && segments[allowed].last <= pin.Transaction {
			allowed++
		}
		if result.AppliedSegments > allowed {
			result.AppliedSegments = allowed
			result.LimitedByPin = true
		}
	}
	through := metadata.baseTx
	if result.AppliedSegments != 0 {
		through = segments[result.AppliedSegments-1].last
	}
	prune, err := db.pruneHistoryLocked(ctx, through)
	result.Prune = prune
	result.AppliedThrough = prune.BaseTransaction

	db.mu.RLock()
	result.RetainedSegments = history.segments
	result.RetainedBytes = history.bytes
	result.CleanupPendingBytes = history.retiredBytes
	if history.retiredBytes <= math.MaxInt64-history.bytes {
		result.PhysicalBytes = history.bytes + history.retiredBytes
	} else {
		result.PhysicalBytes = math.MaxInt64
	}
	db.mu.RUnlock()
	if policy.MaxBytes != 0 && result.PhysicalBytes > policy.MaxBytes {
		result.BytesOverLimit = result.PhysicalBytes - policy.MaxBytes
	}
	if err != nil {
		return result, err
	}
	if result.LimitedByPin && result.PinnedBy == "" {
		return result, errors.Join(ErrHistoryPinned, fmt.Errorf("kitdb: history retention was limited by an unknown pin"))
	}
	return result, nil
}
