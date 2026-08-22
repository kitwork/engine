package kitdb

import (
	"errors"
	"fmt"
)

var (
	ErrClosed                = errors.New("kitdb: database is closed")
	ErrUnavailable           = errors.New("kitdb: database is unavailable")
	ErrCorrupt               = errors.New("kitdb: database is corrupt")
	ErrWriterLocked          = errors.New("kitdb: database already has a writer")
	ErrTransactionActive     = errors.New("kitdb: a write transaction is already active")
	ErrTransactionClosed     = errors.New("kitdb: transaction is closed")
	ErrEmptyTransaction      = errors.New("kitdb: transaction has no operations")
	ErrTransactionTooLarge   = errors.New("kitdb: transaction exceeds the current format limit")
	ErrEmptyKey              = errors.New("kitdb: key is empty")
	ErrKeyTooLarge           = errors.New("kitdb: key exceeds the current format limit")
	ErrValueTooLarge         = errors.New("kitdb: value exceeds the current format limit")
	ErrDurabilityUncertain   = errors.New("kitdb: commit durability is uncertain")
	ErrTransactionIDOverflow = errors.New("kitdb: transaction ID overflow")
	ErrSnapshotClosed        = errors.New("kitdb: snapshot is closed")
	ErrTooManySnapshots      = errors.New("kitdb: active snapshot limit reached")
	ErrSnapshotsActive       = errors.New("kitdb: active snapshots prevent compaction")
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
