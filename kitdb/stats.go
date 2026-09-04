package kitdb

import "time"

// Stats is a point-in-time view of one open database's bounded storage state.
// It contains no user keys or values and is safe to collect concurrently.
type Stats struct {
	ReplicaMode                 bool
	LastTransaction             uint64
	CheckpointTransaction       uint64
	WALBaseTransaction          uint64
	WALBytes                    int64
	MainFormatVersion           uint16
	MainGeneration              uint64
	MainRecords                 uint64
	MainMutations               uint64
	MainSegments                int
	MainBlocks                  int
	MainDataBytes               int64
	MainDirectoryBytes          int64
	MainFileBytes               int64
	OverlayMutations            int
	ActiveSnapshots             int
	ActiveTransactions          int
	ActiveTransactionBytes      int
	PendingCommits              int
	CommitQueueCapacity         int
	MaxCommitBatch              int
	GroupCommitDelay            time.Duration
	CommitBatches               uint64
	CommittedTransactions       uint64
	CommitSyncs                 uint64
	LargestCommitBatch          int
	CatalogStructs              int
	CatalogBytes                int
	CatalogRevision             string
	HistoryEnabled              bool
	HistoryBaseTransaction      uint64
	HistorySegments             int
	HistoryBytes                int64
	HistoryRetiredSegments      int
	HistoryRetiredBytes         int64
	HistoryPins                 int
	HistoryOldestPinTransaction uint64
	CachedPages                 int
	CachedBytes                 int64
	PageCacheLimitBytes         int64
}

// Stats returns storage and cache counters for admission, diagnostics, and
// maintenance decisions.
func (db *DB) Stats() (Stats, error) {
	if err := db.ensureCatalogLoaded(); err != nil {
		return Stats{}, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return Stats{}, err
	}
	used, pages := db.main.cache.usage()
	mutations := uint64(0)
	for _, segment := range db.main.segments {
		mutations += segment.mutations
	}
	if db.main.formatVersion != mainFormatVersion {
		mutations = db.main.records
	}
	stats := Stats{
		ReplicaMode:            db.replicaMode,
		LastTransaction:        db.lastTx,
		CheckpointTransaction:  db.checkpointTx,
		WALBaseTransaction:     db.walBaseTx,
		WALBytes:               db.walEnd,
		MainFormatVersion:      db.main.formatVersion,
		MainGeneration:         db.main.generation,
		MainRecords:            db.main.records,
		MainMutations:          mutations,
		MainSegments:           len(db.main.segments),
		MainBlocks:             len(db.main.blocks),
		MainDataBytes:          db.main.dataBytes,
		MainDirectoryBytes:     db.main.directoryBytes,
		MainFileBytes:          db.main.fileEnd,
		OverlayMutations:       len(db.overlay),
		ActiveSnapshots:        db.activeSnapshotCount(),
		ActiveTransactions:     len(db.activeTx),
		ActiveTransactionBytes: db.activeTxBytes,
		PendingCommits:         len(db.commitQueue),
		CommitQueueCapacity:    cap(db.commitQueue),
		MaxCommitBatch:         db.maxCommitBatch,
		GroupCommitDelay:       db.groupCommitDelay,
		CommitBatches:          db.commitBatches,
		CommittedTransactions:  db.committedTransactions,
		CommitSyncs:            db.commitSyncs,
		LargestCommitBatch:     db.largestCommitBatch,
		CatalogStructs:         len(db.catalog.byID),
		CatalogBytes:           db.catalog.bytes,
		CatalogRevision:        db.catalog.revision,
		CachedPages:            pages,
		CachedBytes:            used,
		PageCacheLimitBytes:    db.main.cache.maximum,
	}
	if db.history != nil {
		stats.HistoryEnabled = true
		stats.HistoryBaseTransaction = db.history.baseTx
		stats.HistorySegments = db.history.segments
		stats.HistoryBytes = db.history.bytes
		stats.HistoryRetiredSegments = db.history.retiredSegments
		stats.HistoryRetiredBytes = db.history.retiredBytes
		stats.HistoryPins = len(db.history.pins)
		if _, cursor, found := oldestHistoryPin(db.history.pins); found {
			stats.HistoryOldestPinTransaction = cursor.Transaction
		}
	}
	return stats, nil
}
