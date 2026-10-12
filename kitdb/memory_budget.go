package kitdb

import (
	"fmt"
	"time"
)

// Conservative accounting units, not allocator/RSS measurements. Key storage
// is charged even though snapshot map keys can share immutable string bytes.
const snapshotBaseCost int64 = 256
const snapshotEntryCost int64 = 128

func snapshotMutationCost(key string, mutation rowMutation) int64 {
	return snapshotEntryCost + int64(len(key)) + int64(len(mutation.value))
}

func snapshotOverlayCost(overlay map[string]rowMutation) int64 {
	cost := snapshotBaseCost
	for key, mutation := range overlay {
		cost += snapshotMutationCost(key, mutation)
	}
	return cost
}

// Caller holds db.mu. Keep admission checks O(1) instead of scanning the WAL
// overlay on every request; recovery computes the initial total once.
func (db *DB) applyOverlayOperations(operations []operation) {
	if len(db.overlay) == 0 && len(operations) != 0 {
		db.overlaySince = time.Now()
	}
	cost := db.snapshotOverlayBytes.Load()
	// Repeated keys in a single commit need the updated map for each delta.
	// applyOperations is kept as the canonical recovery mutation primitive.
	if len(operations) > 0 {
		for _, op := range operations {
			key := string(op.key)
			if old, exists := db.overlay[key]; exists {
				cost -= snapshotMutationCost(key, old)
			}
			applyOperations(db.overlay, []operation{op})
			cost += snapshotMutationCost(key, db.overlay[key])
		}
	}
	db.snapshotOverlayBytes.Store(cost)
	db.snapshotMu.Lock()
	db.grantSnapshotWaitersLocked()
	db.snapshotMu.Unlock()
}

func (db *DB) resetOverlayMemory() {
	db.overlaySince = time.Time{}
	db.lastCheckpointAt = time.Now()
	db.snapshotOverlayBytes.Store(snapshotBaseCost)
	db.snapshotMu.Lock()
	db.grantSnapshotWaitersLocked()
	db.snapshotMu.Unlock()
}

// ReserveTransactionMemory charges frontend mutation storage before allocation.
// The charge shares the kernel transaction budget and lives with this snapshot.
func (snapshot *Snapshot) ReserveTransactionMemory(size int64) error {
	if snapshot == nil {
		return ErrSnapshotClosed
	}
	if size < 0 {
		return fmt.Errorf("kitdb: negative transaction memory charge")
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if snapshot.closed || snapshot.owner == nil {
		return ErrSnapshotClosed
	}
	db := snapshot.owner
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.stateErrorLocked(); err != nil {
		return err
	}
	if size > db.transactionMemoryLimit-int64(db.activeTxBytes)-db.frontendTxBytes {
		return ErrActiveTransactionMemory
	}
	db.frontendTxBytes += size
	snapshot.transactionBytes += size
	return nil
}

// ReleaseTransactionMemory returns discarded savepoint writes. Close releases
// every remaining charge, including on rollback and database shutdown.
func (snapshot *Snapshot) ReleaseTransactionMemory(size int64) {
	if snapshot == nil {
		return
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if snapshot.closed || snapshot.owner == nil || size <= 0 {
		return
	}
	if size > snapshot.transactionBytes {
		size = snapshot.transactionBytes
	}
	db := snapshot.owner
	db.mu.Lock()
	db.frontendTxBytes -= size
	snapshot.transactionBytes -= size
	db.mu.Unlock()
}
