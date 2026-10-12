package kitdb

import (
	"context"
	"fmt"
	"time"
)

const maximumSnapshotWaiters = 128
const snapshotAdmissionWait = 2 * time.Second

// SnapshotReservation owns capacity, not a file or a copied overlay. Reserve
// before taking a frontend writer gate, then capture under that gate. Close an
// unused reservation. Its state belongs exclusively to owner.snapshotMu.
type SnapshotReservation struct{ owner *DB }

type snapshotWaiter struct {
	ready       chan struct{}
	reservation *SnapshotReservation
	err         error
}

// ReserveSnapshot admits against the shared 32-slot snapshot budget. Saturated
// callers wait FIFO, at most 128 queued callers and two seconds per attempt;
// the caller's context can shorten that wait. A reservation must be consumed
// with Snapshot or released with Close.
func (db *DB) ReserveSnapshot(ctx context.Context) (*SnapshotReservation, error) {
	if ctx == nil {
		return nil, fmt.Errorf("kitdb: snapshot context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return nil, err
	}
	db.snapshotMu.Lock()
	if db.snapshotOverlayBytes.Load() > db.snapshotMemoryLimit {
		db.snapshotMu.Unlock()
		db.mu.RUnlock()
		return nil, ErrSnapshotMemory
	}
	if len(db.snapshotWaiters) == 0 && db.snapshotCapacityLocked(db.snapshotOverlayBytes.Load()) {
		reservation := db.newSnapshotReservationLocked(db.snapshotOverlayBytes.Load())
		db.snapshotMu.Unlock()
		db.mu.RUnlock()
		return reservation, nil
	}
	if len(db.snapshotWaiters) >= maximumSnapshotWaiters {
		db.snapshotMu.Unlock()
		db.mu.RUnlock()
		return nil, ErrSnapshotAdmissionFull
	}
	waiter := &snapshotWaiter{ready: make(chan struct{})}
	db.snapshotWaiters = append(db.snapshotWaiters, waiter)
	db.snapshotMu.Unlock()
	db.mu.RUnlock()
	waitCtx, cancel := context.WithTimeout(ctx, snapshotAdmissionWait)
	defer cancel()
	select {
	case <-waiter.ready:
	case <-waitCtx.Done():
	}
	db.snapshotMu.Lock()
	defer db.snapshotMu.Unlock()
	if waitCtx.Err() != nil {
		for i, current := range db.snapshotWaiters {
			if current == waiter {
				copy(db.snapshotWaiters[i:], db.snapshotWaiters[i+1:])
				db.snapshotWaiters[len(db.snapshotWaiters)-1] = nil
				db.snapshotWaiters = db.snapshotWaiters[:len(db.snapshotWaiters)-1]
				break
			}
		}
		if waiter.reservation != nil {
			db.releaseSnapshotReservationLocked(waiter.reservation)
		}
		db.grantSnapshotWaitersLocked()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrSnapshotAdmissionTimeout
	}
	return waiter.reservation, waiter.err
}

func (db *DB) newSnapshotReservationLocked(cost int64) *SnapshotReservation {
	reservation := &SnapshotReservation{owner: db}
	if db.snapshotReservations == nil {
		db.snapshotReservations = make(map[*SnapshotReservation]int64)
	}
	db.snapshotReservations[reservation] = cost
	db.snapshotReservedBytes += cost
	return reservation
}

func (db *DB) grantSnapshotWaitersLocked() {
	db.notifySnapshotDrainLocked()
	if db.snapshotAdmissionClosed || db.snapshotPauseDone != nil {
		return
	}
	cost := db.snapshotOverlayBytes.Load()
	for len(db.snapshotWaiters) > 0 && (db.snapshotCapacityLocked(cost) || cost > db.snapshotMemoryLimit) {
		waiter := db.snapshotWaiters[0]
		copy(db.snapshotWaiters, db.snapshotWaiters[1:])
		db.snapshotWaiters[len(db.snapshotWaiters)-1] = nil
		db.snapshotWaiters = db.snapshotWaiters[:len(db.snapshotWaiters)-1]
		if cost > db.snapshotMemoryLimit {
			waiter.err = ErrSnapshotMemory
		} else {
			waiter.reservation = db.newSnapshotReservationLocked(cost)
		}
		close(waiter.ready)
	}
	if len(db.snapshotWaiters) == 0 {
		db.snapshotWaiters = nil
	}
}

func (reservation *SnapshotReservation) Snapshot() (*Snapshot, error) {
	if reservation == nil || reservation.owner == nil {
		return nil, ErrSnapshotClosed
	}
	db := reservation.owner
	defer reservation.Close()
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return nil, err
	}
	db.snapshotMu.Lock()
	defer db.snapshotMu.Unlock()
	if _, found := db.snapshotReservations[reservation]; !found {
		return nil, ErrSnapshotClosed
	}
	db.releaseSnapshotReservationLocked(reservation)
	snapshot, err := db.captureSnapshotLocked()
	db.grantSnapshotWaitersLocked()
	return snapshot, err
}

func (reservation *SnapshotReservation) Close() error {
	if reservation == nil || reservation.owner == nil {
		return nil
	}
	db := reservation.owner
	db.snapshotMu.Lock()
	db.releaseSnapshotReservationLocked(reservation)
	db.grantSnapshotWaitersLocked()
	db.snapshotMu.Unlock()
	return nil
}

// SnapshotContext waits without allocating a snapshot or spawning a goroutine.
// Snapshot remains a nonblocking primitive for existing trusted callers.
func (db *DB) SnapshotContext(ctx context.Context) (*Snapshot, error) {
	reservation, err := db.ReserveSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = reservation.Close()
		return nil, err
	}
	return reservation.Snapshot()
}

func (db *DB) closeSnapshotAdmission() {
	db.snapshotMu.Lock()
	defer db.snapshotMu.Unlock()
	db.snapshotAdmissionClosed = true
	for _, waiter := range db.snapshotWaiters {
		waiter.err = ErrClosed
		close(waiter.ready)
	}
	db.snapshotWaiters = nil
	db.snapshotReservations = nil
	db.snapshotReservedBytes = 0
	db.notifySnapshotDrainLocked()
}

func (db *DB) snapshotCapacityLocked(cost int64) bool {
	return db.snapshotPauseDone == nil && len(db.snapshots)+len(db.snapshotReservations) < maxActiveSnapshots &&
		cost <= db.snapshotMemoryLimit-db.snapshotBytes-db.snapshotReservedBytes
}

func (db *DB) notifySnapshotDrainLocked() {
	if db.snapshotDrain != nil && !db.snapshotDrainReady &&
		(db.snapshotAdmissionClosed || len(db.snapshots)+len(db.snapshotReservations) == 0) {
		close(db.snapshotDrain)
		db.snapshotDrainReady = true
	}
}

func (db *DB) releaseSnapshotReservationLocked(reservation *SnapshotReservation) {
	db.snapshotReservedBytes -= db.snapshotReservations[reservation]
	delete(db.snapshotReservations, reservation)
}
