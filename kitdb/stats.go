package kitdb

// Stats is a point-in-time view of one open database's bounded storage state.
// It contains no user keys or values and is safe to collect concurrently.
type Stats struct {
	LastTransaction       uint64
	CheckpointTransaction uint64
	WALBaseTransaction    uint64
	WALBytes              int64
	MainFormatVersion     uint16
	MainGeneration        uint64
	MainRecords           uint64
	MainMutations         uint64
	MainSegments          int
	MainBlocks            int
	MainDataBytes         int64
	MainDirectoryBytes    int64
	MainFileBytes         int64
	OverlayMutations      int
	ActiveSnapshots       int
	CachedPages           int
	CachedBytes           int64
	PageCacheLimitBytes   int64
}

// Stats returns storage and cache counters for admission, diagnostics, and
// maintenance decisions.
func (db *DB) Stats() (Stats, error) {
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
	return Stats{
		LastTransaction:       db.lastTx,
		CheckpointTransaction: db.checkpointTx,
		WALBaseTransaction:    db.walBaseTx,
		WALBytes:              db.walEnd,
		MainFormatVersion:     db.main.formatVersion,
		MainGeneration:        db.main.generation,
		MainRecords:           db.main.records,
		MainMutations:         mutations,
		MainSegments:          len(db.main.segments),
		MainBlocks:            len(db.main.blocks),
		MainDataBytes:         db.main.dataBytes,
		MainDirectoryBytes:    db.main.directoryBytes,
		MainFileBytes:         db.main.fileEnd,
		OverlayMutations:      len(db.overlay),
		ActiveSnapshots:       db.activeSnapshotCount(),
		CachedPages:           pages,
		CachedBytes:           used,
		PageCacheLimitBytes:   db.main.cache.maximum,
	}, nil
}
