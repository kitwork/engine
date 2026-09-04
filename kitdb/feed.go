package kitdb

import (
	"bytes"
	"fmt"
	"time"
)

// CommitOperationKind describes one committed mutation.
type CommitOperationKind byte

const (
	CommitOperationPut    CommitOperationKind = 1
	CommitOperationDelete CommitOperationKind = 2
)

// CommitOperation is one immutable change retained for observers.
type CommitOperation struct {
	Kind  CommitOperationKind
	Key   []byte
	Value []byte
}

// CommitEvent is one committed transaction published to listeners. Checksum
// identifies its exact durable WAL-frame boundary.
type CommitEvent struct {
	Transaction uint64
	Checksum    uint32
	CommittedAt time.Time
	Operations  []CommitOperation
}

// CommitListener receives committed transactions after the WAL sync succeeds
// and the in-memory state is visible.
type CommitListener func(CommitEvent)

// AddCommitListener registers one observer for future commits.
func (db *DB) AddCommitListener(listener CommitListener) (func(), error) {
	if listener == nil {
		return nil, fmt.Errorf("kitdb: nil commit listener")
	}
	db.mu.RLock()
	stateErr := db.stateErrorLocked()
	db.mu.RUnlock()
	if stateErr != nil {
		return nil, stateErr
	}
	db.listenerMu.Lock()
	defer db.listenerMu.Unlock()
	if db.commitListeners == nil {
		db.commitListeners = make(map[uint64]CommitListener)
	}
	db.nextListenerID++
	id := db.nextListenerID
	db.commitListeners[id] = listener
	return func() {
		db.listenerMu.Lock()
		delete(db.commitListeners, id)
		db.listenerMu.Unlock()
	}, nil
}

func (db *DB) dispatchCommit(transaction uint64, checksum uint32, committedAt int64, operations []operation) {
	listeners := db.snapshotCommitListeners()
	if len(listeners) == 0 {
		return
	}
	event := CommitEvent{
		Transaction: transaction,
		Checksum:    checksum,
		CommittedAt: commitTimeValue(committedAt),
		Operations:  cloneCommitOperations(operations),
	}
	for _, listener := range listeners {
		listener(event)
	}
}

func commitTimeValue(unixNano int64) time.Time {
	if unixNano <= 0 {
		return time.Time{}
	}
	return time.Unix(0, unixNano).UTC()
}

func commitTimeUnixNano(committedAt time.Time) int64 {
	if committedAt.IsZero() {
		return 0
	}
	return committedAt.UTC().UnixNano()
}

func (db *DB) snapshotCommitListeners() []CommitListener {
	db.listenerMu.RLock()
	defer db.listenerMu.RUnlock()
	if len(db.commitListeners) == 0 {
		return nil
	}
	listeners := make([]CommitListener, 0, len(db.commitListeners))
	for _, listener := range db.commitListeners {
		listeners = append(listeners, listener)
	}
	return listeners
}

func cloneCommitOperations(operations []operation) []CommitOperation {
	cloned := make([]CommitOperation, len(operations))
	for index, op := range operations {
		kind := CommitOperationDelete
		if op.kind == operationPut {
			kind = CommitOperationPut
		}
		cloned[index] = CommitOperation{
			Kind:  kind,
			Key:   bytes.Clone(op.key),
			Value: bytes.Clone(op.value),
		}
	}
	return cloned
}
