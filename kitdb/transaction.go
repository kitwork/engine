package kitdb

import (
	"bytes"
	"sync"
)

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
	if err := tx.db.available(); err != nil {
		return err
	}
	if len(tx.operations) >= maxOperations {
		return ErrTransactionTooLarge
	}
	size := operationHeaderSize + len(op.key) + len(op.value)
	if size > maxPayloadSize-tx.payloadSize {
		return ErrTransactionTooLarge
	}
	tx.operations = append(tx.operations, op)
	tx.payloadSize += size
	return nil
}

// Commit appends and syncs the complete transaction frame. The returned ID is
// meaningful even with ErrDurabilityUncertain and can be inspected after the
// database is reopened.
func (tx *Tx) Commit() (uint64, error) {
	tx.mu.Lock()
	if tx.done {
		tx.mu.Unlock()
		return 0, ErrTransactionClosed
	}
	tx.done = true
	operations := tx.operations
	tx.operations = nil
	tx.mu.Unlock()

	defer tx.db.finishTransaction(tx)
	return tx.db.commit(operations)
}

// Rollback discards every uncommitted operation.
func (tx *Tx) Rollback() error {
	tx.mu.Lock()
	if tx.done {
		tx.mu.Unlock()
		return ErrTransactionClosed
	}
	tx.done = true
	tx.operations = nil
	tx.mu.Unlock()
	tx.db.finishTransaction(tx)
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
