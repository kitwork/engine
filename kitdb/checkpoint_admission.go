package kitdb

import (
	"context"
	"fmt"
	"time"
)

// CheckpointContext queues new readers only when full compaction is required.
// Admission and commit-lock acquisition are bounded by two seconds and ctx.
// Once publication starts, it finishes the durable boundary without cancellation.
// Checkpoint retains its legacy nonblocking-reader behavior.
func (db *DB) CheckpointContext(ctx context.Context) (uint64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("kitdb: checkpoint context is nil")
	}
	waitCtx, cancel := context.WithTimeout(ctx, snapshotAdmissionWait)
	defer cancel()
	var resume func()
	defer func() {
		if resume != nil {
			resume()
		}
	}()
	for {
		if err := db.lockCheckpointContext(waitCtx); err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, ErrSnapshotAdmissionTimeout
		}
		db.mu.RLock()
		err := db.stateErrorLocked()
		full := err == nil && (db.checkpointTx != db.lastTx || db.main.formatVersion != mainFormatVersion) &&
			!(db.main.formatVersion == mainFormatVersion && len(db.main.segments) < maxMainSegments && len(db.overlay) != 0)
		db.mu.RUnlock()
		if err != nil {
			db.commitMu.Unlock()
			return 0, err
		}
		if !full || resume != nil {
			transaction, err := db.checkpointLocked()
			db.commitMu.Unlock()
			return transaction, err
		}
		// Existing SQL transactions need commitMu before releasing their snapshot.
		db.commitMu.Unlock()
		resume, err = db.pauseSnapshotAdmission(waitCtx)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if waitCtx.Err() != nil {
				return 0, ErrSnapshotsActive
			}
			return 0, err
		}
	}
}

func (db *DB) lockCheckpointContext(ctx context.Context) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if db.commitMu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (db *DB) pauseSnapshotAdmission(ctx context.Context) (func(), error) {
	for {
		db.snapshotMu.Lock()
		if db.snapshotAdmissionClosed {
			db.snapshotMu.Unlock()
			return nil, ErrClosed
		}
		if done := db.snapshotPauseDone; done != nil {
			db.snapshotMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		db.snapshotPauseDone = make(chan struct{})
		db.snapshotDrain = make(chan struct{})
		db.snapshotDrainReady = false
		db.notifySnapshotDrainLocked()
		drain := db.snapshotDrain
		db.snapshotMu.Unlock()
		resume := func() {
			db.snapshotMu.Lock()
			close(db.snapshotPauseDone)
			db.snapshotPauseDone = nil
			db.snapshotDrain = nil
			db.snapshotDrainReady = false
			db.grantSnapshotWaitersLocked()
			db.snapshotMu.Unlock()
		}
		select {
		case <-drain:
			db.snapshotMu.Lock()
			closed := db.snapshotAdmissionClosed
			db.snapshotMu.Unlock()
			if closed {
				resume()
				return nil, ErrClosed
			}
			return resume, nil
		case <-ctx.Done():
			resume()
			return nil, ctx.Err()
		}
	}
}
