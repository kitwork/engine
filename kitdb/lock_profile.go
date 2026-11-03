package kitdb

type lockSite uint8

const (
	lockSnapshot lockSite = iota
	lockSnapshotReserve
	lockSnapshotCapture
	lockCheckpointRead
	lockCheckpointInstall
	lockCheckpointCompact
	lockWALDetach
	lockWALInstall
)

func (db *DB) profileRLock(site lockSite) lockSpan {
	span := db.lockProfile.start(site)
	db.mu.RLock()
	span.acquired()
	return span
}

func (db *DB) profileRUnlock(span *lockSpan) {
	span.releasing()
	db.mu.RUnlock()
	span.end()
}

func (db *DB) profileLock(site lockSite) lockSpan {
	span := db.lockProfile.start(site)
	db.mu.Lock()
	span.acquired()
	return span
}

func (db *DB) profileUnlock(span *lockSpan) {
	span.releasing()
	db.mu.Unlock()
	span.end()
}
