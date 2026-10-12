package kitdb

import (
	"bytes"
	"errors"
	"sync"
)

var ErrTransactionConflict = errors.New("kitdb: transaction snapshot changed")

type operationKind byte

const (
	operationPut    operationKind = 1
	operationDelete operationKind = 2
)

type operation struct {
	kind  operationKind
	key   []byte
	value []byte
}

type rowMutation struct {
	value   []byte
	deleted bool
}

// Tx accumulates one bounded write transaction. A transaction is not safe for
// simultaneous method calls; the mutex makes accidental races deterministic.
type Tx struct {
	mu          sync.Mutex
	db          *DB
	operations  []operation
	payloadSize int
	done        bool
}

// Put stores a caller-independent copy of key and value in the transaction.
func (tx *Tx) Put(key, value []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if len(value) > maxValueSize {
		return ErrValueTooLarge
	}
	return tx.add(operation{kind: operationPut, key: bytes.Clone(key), value: bytes.Clone(value)})
}

// Delete removes key when the transaction commits.
func (tx *Tx) Delete(key []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	return tx.add(operation{kind: operationDelete, key: bytes.Clone(key)})
}

func (tx *Tx) add(op operation) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.done {
		return ErrTransactionClosed
	}
	if len(tx.operations) >= maxOperations {
		return ErrTransactionTooLarge
	}
	size := operationHeaderSize + len(op.key) + len(op.value)
	if size > maxPayloadSize-tx.payloadSize {
		return ErrTransactionTooLarge
	}
	if err := tx.db.reserveTransactionBytes(tx, size); err != nil {
		return err
	}
	tx.operations = append(tx.operations, op)
	tx.payloadSize += size
	return nil
}

// Commit appends and syncs the complete transaction frame. The returned ID is
// meaningful even with ErrDurabilityUncertain and can be inspected after the
// database is reopened.
func (tx *Tx) Commit() (uint64, error) {
	return tx.commit(nil, false)
}

// CommitWithDependencies validates a complete physical dependency set at the
// commit writer. It is for trusted adapters, not automatic SQL isolation.
// Missing journal coverage or oversized dependencies uses the coarse fence.
func (tx *Tx) CommitWithDependencies(snapshot uint64, dependencies CommitDependencies) (uint64, error) {
	return tx.commitPrepared(&snapshot, false, &dependencies)
}

// CommitIfUnchanged checks the snapshot boundary inside the commit writer.
// Only kernel-owned sequence value reservations are excluded from conflicts;
// catalog changes and all caller-authored writes still invalidate the snapshot.
func (tx *Tx) CommitIfUnchanged(snapshot uint64) (uint64, error) {
	return tx.commit(&snapshot, false)
}

func (tx *Tx) commit(snapshot *uint64, sequenceOnly bool) (uint64, error) {
	return tx.commitPrepared(snapshot, sequenceOnly, nil)
}

func (tx *Tx) commitPrepared(snapshot *uint64, sequenceOnly bool, dependencies *CommitDependencies) (uint64, error) {
	tx.mu.Lock()
	if tx.done {
		tx.mu.Unlock()
		return 0, ErrTransactionClosed
	}
	var validation *commitValidation
	extraBytes := 0
	if dependencies != nil {
		validation = prepareCommitValidation(*dependencies, tx.operations)
		if validation != nil {
			// Charge key/bound bytes plus a fixed map/slice metadata allowance
			// against the same aggregate admission budget as pending mutations.
			extraBytes = validation.bytes + len(validation.keys)*128 + len(validation.ranges)*128
			if err := tx.db.reserveTransactionBytes(tx, extraBytes); err != nil {
				tx.mu.Unlock()
				return 0, err
			}
		}
	}
	tx.done = true
	operations := tx.operations
	payloadSize := tx.payloadSize + extraBytes
	tx.operations = nil
	tx.mu.Unlock()

	defer tx.db.finishTransaction(tx, payloadSize)
	return tx.db.submitCommit(&commitRequest{operations: operations, snapshot: snapshot, sequenceOnly: sequenceOnly, validation: validation})
}

// Rollback discards every uncommitted operation.
func (tx *Tx) Rollback() error {
	tx.mu.Lock()
	if tx.done {
		tx.mu.Unlock()
		return ErrTransactionClosed
	}
	tx.done = true
	payloadSize := tx.payloadSize
	tx.operations = nil
	tx.mu.Unlock()
	tx.db.finishTransaction(tx, payloadSize)
	return nil
}

func (tx *Tx) invalidate() {
	tx.mu.Lock()
	if !tx.done {
		tx.done = true
		tx.operations = nil
	}
	tx.mu.Unlock()
}

func validateKey(key []byte) error {
	switch {
	case len(key) == 0:
		return ErrEmptyKey
	case len(key) > maxKeySize:
		return ErrKeyTooLarge
	default:
		return nil
	}
}

func applyOperations(overlay map[string]rowMutation, operations []operation) {
	for _, op := range operations {
		switch op.kind {
		case operationPut:
			overlay[string(op.key)] = rowMutation{value: bytes.Clone(op.value)}
		case operationDelete:
			overlay[string(op.key)] = rowMutation{deleted: true}
		}
	}
}
