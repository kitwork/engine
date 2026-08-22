package kitdb

import (
	"errors"
	"fmt"
	"math"
	"os"
)

// Checkpoint publishes the latest committed state to the main database file,
// then rotates the WAL to that exact transaction boundary. Format-v3
// checkpoints append an immutable delta segment and publish one alternate
// superblock slot. Legacy migration and bounded compaction use atomic replace.
func (db *DB) Checkpoint() (uint64, error) {
	db.commitMu.Lock()
	defer db.commitMu.Unlock()

	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return 0, err
	}
	transaction := db.lastTx
	boundaryChecksum := db.walChecksum
	checkpointTransaction := db.checkpointTx
	walBaseTransaction := db.walBaseTx
	identity := db.identity
	main := db.main
	overlay := db.overlay
	needsCheckpoint := checkpointTransaction != transaction || main.formatVersion != mainFormatVersion
	if needsCheckpoint {
		useIncremental := main.formatVersion == mainFormatVersion && len(main.segments) < maxMainSegments && len(overlay) != 0
		if useIncremental {
			logicalRecords, err := countMergedRecords(main, overlay)
			if err != nil {
				db.mu.RUnlock()
				return transaction, err
			}
			appendResult, err := appendIncrementalGeneration(db.path, main, overlay, transaction, boundaryChecksum, logicalRecords)
			if err != nil {
				db.mu.RUnlock()
				if errors.Is(err, ErrDurabilityUncertain) {
					db.markUnavailable(err)
				}
				return transaction, err
			}
			if appendResult == nil {
				db.mu.RUnlock()
				return transaction, fmt.Errorf("kitdb: generation checkpoint was not published")
			}
			newMain, err := openAppendedGeneration(db.path, main, appendResult, db.pageCacheBytes)
			db.mu.RUnlock()
			if err != nil {
				db.markUnavailable(err)
				return transaction, errors.Join(ErrUnavailable, err)
			}
			if err := db.installAppendedMain(newMain, transaction); err != nil {
				return transaction, err
			}
		} else {
			if main.formatVersion == mainFormatVersion && main.generation == math.MaxUint64 {
				db.mu.RUnlock()
				return transaction, fmt.Errorf("kitdb: generation ID overflow")
			}
			if db.activeSnapshotCount() != 0 {
				db.mu.RUnlock()
				return transaction, ErrSnapshotsActive
			}
			generation := uint64(1)
			if main.formatVersion == mainFormatVersion {
				generation = main.generation + 1
			}
			stagingPath, err := prepareCompactedGeneration(db.path, identity, transaction, boundaryChecksum, generation, func(emit func(key, value []byte) error) error {
				return walkMergedRows(main, overlay, emit)
			})
			db.mu.RUnlock()
			if err != nil {
				return transaction, err
			}
			defer os.Remove(stagingPath)
			if err := db.publishCompactedMain(stagingPath, transaction); err != nil {
				return transaction, err
			}
		}
	} else {
		db.mu.RUnlock()
	}

	if walBaseTransaction == transaction {
		return transaction, nil
	}
	return transaction, db.rotateWALLocked(transaction, boundaryChecksum)
}

func countMergedRecords(main *mainImage, overlay map[string]rowMutation) (uint64, error) {
	records := main.records
	for key, mutation := range overlay {
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

func (db *DB) installAppendedMain(newMain *mainImage, transaction uint64) error {
	db.mu.Lock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.Unlock()
		_ = newMain.close()
		return err
	}
	oldMain := db.main
	newMain.cache = oldMain.cache
	db.main = newMain
	db.overlay = make(map[string]rowMutation)
	db.checkpointTx = transaction
	db.mu.Unlock()
	if err := oldMain.close(); err != nil {
		return fmt.Errorf("kitdb: close previous generation reader: %w", err)
	}
	return nil
}

func (db *DB) publishCompactedMain(stagingPath string, transaction uint64) error {
	db.mu.Lock()
	if db.activeSnapshotCount() != 0 {
		db.mu.Unlock()
		return ErrSnapshotsActive
	}
	oldMain := db.main
	if err := oldMain.close(); err != nil {
		restoreErr := db.restoreMainLocked(err)
		db.mu.Unlock()
		return restoreErr
	}
	published, publishErr := publishPreparedMainSnapshot(stagingPath, db.path)
	if !published {
		restoreErr := db.restoreMainLocked(publishErr)
		db.mu.Unlock()
		return restoreErr
	}
	newMain, openErr := readMainSnapshotWithCache(db.path, db.pageCacheBytes)
	if openErr != nil {
		fatal := errors.Join(publishErr, openErr)
		db.main = nil
		if db.fatal == nil {
			db.fatal = fatal
		}
		db.mu.Unlock()
		return errors.Join(ErrUnavailable, fatal)
	}
	db.main = newMain
	db.overlay = make(map[string]rowMutation)
	db.checkpointTx = transaction
	if publishErr != nil && db.fatal == nil {
		db.fatal = publishErr
	}
	db.mu.Unlock()
	return publishErr
}

// restoreMainLocked restores the path-backed reader after a pre-publication
// failure. The WAL and overlay remain authoritative and unchanged.
func (db *DB) restoreMainLocked(cause error) error {
	restored, err := readMainSnapshotWithCache(db.path, db.pageCacheBytes)
	if err == nil {
		db.main = restored
		return cause
	}
	fatal := errors.Join(cause, err)
	db.main = nil
	if db.fatal == nil {
		db.fatal = fatal
	}
	return errors.Join(ErrUnavailable, fatal)
}
