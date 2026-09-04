package kitdb

import (
	"errors"
	"fmt"
)

var (
	ErrClosed           = errors.New("kitdb: database is closed")
	ErrUnavailable      = errors.New("kitdb: database is unavailable")
	ErrDatabaseNotFound = errors.New("kitdb: database does not exist")
	ErrCorrupt          = errors.New("kitdb: database is corrupt")
	ErrWriterLocked     = errors.New("kitdb: database already has a writer")
	// ErrTransactionActive is retained for source compatibility. Begin now
	// permits a bounded set of concurrently prepared transactions.
	ErrTransactionActive        = errors.New("kitdb: a write transaction is already active")
	ErrTooManyTransactions      = errors.New("kitdb: active transaction limit reached")
	ErrActiveTransactionMemory  = errors.New("kitdb: active transactions exceed the memory limit")
	ErrTransactionClosed        = errors.New("kitdb: transaction is closed")
	ErrEmptyTransaction         = errors.New("kitdb: transaction has no operations")
	ErrTransactionTooLarge      = errors.New("kitdb: transaction exceeds the current format limit")
	ErrEmptyKey                 = errors.New("kitdb: key is empty")
	ErrKeyTooLarge              = errors.New("kitdb: key exceeds the current format limit")
	ErrValueTooLarge            = errors.New("kitdb: value exceeds the current format limit")
	ErrDurabilityUncertain      = errors.New("kitdb: commit durability is uncertain")
	ErrTransactionIDOverflow    = errors.New("kitdb: transaction ID overflow")
	ErrSnapshotClosed           = errors.New("kitdb: snapshot is closed")
	ErrTooManySnapshots         = errors.New("kitdb: active snapshot limit reached")
	ErrSnapshotsActive          = errors.New("kitdb: active snapshots prevent compaction")
	ErrHistoryDisabled          = errors.New("kitdb: retained history is disabled")
	ErrHistoryGap               = errors.New("kitdb: retained history has a transaction gap")
	ErrHistoryLimit             = errors.New("kitdb: retained history reached the segment limit")
	ErrHistoryCursor            = errors.New("kitdb: invalid history cursor")
	ErrHistoryPinned            = errors.New("kitdb: retained history is protected by a pin")
	ErrHistoryPinLimit          = errors.New("kitdb: retained history reached the pin limit")
	ErrHistoryRetentionPolicy   = errors.New("kitdb: invalid history retention policy")
	ErrReplicaReadOnly          = errors.New("kitdb: replica handle is read-only")
	ErrReplicaModeRequired      = errors.New("kitdb: replica mode is required")
	ErrReplicaBusy              = errors.New("kitdb: replica catch-up is already running")
	ErrReplicaDiverged          = errors.New("kitdb: replica diverged from its source")
	ErrReplicaProtocol          = errors.New("kitdb: invalid replica protocol message")
	ErrReplicaBatchLimit        = errors.New("kitdb: replica batch exceeds its configured limit")
	ErrReplicaTransportLimit    = errors.New("kitdb: replica transport exceeds its configured limit")
	ErrReplicaTransportConflict = errors.New("kitdb: replica transport message conflicts with existing state")
	ErrReplicaPinExists         = errors.New("kitdb: replica pin already exists")
	ErrBackupExists             = errors.New("kitdb: backup anchor already exists")
	ErrRestoreExists            = errors.New("kitdb: restore destination already exists")
	ErrRecoveryTimeUnavailable  = errors.New("kitdb: point-in-time recovery metadata is unavailable")
	ErrRecoveryTargetTooOld     = errors.New("kitdb: recovery target precedes retained history")
	ErrInvalidCatalog           = errors.New("kitdb: invalid schema catalog")
)

// CorruptionError identifies the durable byte offset at which validation
// failed. It unwraps to ErrCorrupt for errors.Is checks.
type CorruptionError struct {
	File   string
	Offset int64
	Reason string
}

func (e *CorruptionError) Error() string {
	file := e.File
	if file == "" {
		file = walFilename
	}
	return fmt.Sprintf("kitdb: corruption in %s at offset %d: %s", file, e.Offset, e.Reason)
}

func (e *CorruptionError) Unwrap() error { return ErrCorrupt }

func corruptAt(offset int64, format string, args ...any) error {
	return corruptFileAt(walFilename, offset, format, args...)
}

func corruptFileAt(file string, offset int64, format string, args ...any) error {
	return &CorruptionError{File: file, Offset: offset, Reason: fmt.Sprintf(format, args...)}
}
