package kitdb

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type commitWAL interface {
	io.Writer
	io.Seeker
	Sync() error
	Close() error
}

// DB owns one exclusive writer and an in-memory view rebuilt from an immutable
// checkpoint plus its WAL tail.
type DB struct {
	path           string
	identity       [16]byte
	pageCacheBytes int64

	commitMu     sync.Mutex
	mu           sync.RWMutex
	main         *mainImage
	overlay      map[string]rowMutation
	lastTx       uint64
	walEnd       int64
	walChecksum  uint32
	walBaseTx    uint64
	checkpointTx uint64
	activeTx     *Tx
	closed       bool
	fatal        error

	listenerMu      sync.RWMutex
	nextListenerID  uint64
	commitListeners map[uint64]CommitListener

	snapshotMu sync.Mutex
	snapshots  map[*Snapshot]struct{}

	wal  commitWAL
	lock *writerLock
}

// OpenOptions controls bounded resources and startup verification owned by one
// database handle. A zero PageCacheBytes uses the default; a negative value
// disables page caching. VerifyOnOpen scans and checksums every persisted page
// before WAL recovery instead of verifying pages lazily on first access.
type OpenOptions struct {
	PageCacheBytes int64
	VerifyOnOpen   bool
}

// Open creates or recovers a KitDB file with default bounded resources.
func Open(path string) (*DB, error) {
	return OpenWithOptions(path, OpenOptions{})
}

// OpenWithOptions creates or recovers a KitDB file. It holds an exclusive
// process-level writer lock until Close.
func OpenWithOptions(path string, options OpenOptions) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("kitdb: open: empty database path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("kitdb: resolve database path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	pageCacheBytes := options.PageCacheBytes
	if pageCacheBytes == 0 {
		pageCacheBytes = defaultMainPageCacheBytes
	} else if pageCacheBytes < 0 {
		pageCacheBytes = 0
	}

	parent := filepath.Dir(absolute)
	createdParent := false
	parentInfo, err := os.Stat(parent)
	switch {
	case err == nil && !parentInfo.IsDir():
		return nil, fmt.Errorf("kitdb: database parent %q is not a directory", parent)
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, fmt.Errorf("kitdb: create database parent: %w", err)
		}
		createdParent = true
	default:
		return nil, fmt.Errorf("kitdb: inspect database parent: %w", err)
	}
	if createdParent {
		if err := syncDirectory(filepath.Dir(parent)); err != nil {
			return nil, fmt.Errorf("kitdb: sync database parent directory: %w", err)
		}
	}

	info, err := os.Stat(absolute)
	switch {
	case err == nil && !info.Mode().IsRegular():
		return nil, fmt.Errorf("kitdb: database path %q is not a regular file", absolute)
	case err == nil || errors.Is(err, os.ErrNotExist):
	default:
		return nil, fmt.Errorf("kitdb: inspect database file: %w", err)
	}

	lock, err := acquireWriterLock(absolute)
	if err != nil {
		return nil, err
	}
	main, err := loadOrCreateMain(absolute, pageCacheBytes)
	if err != nil {
		_ = lock.close()
		return nil, err
	}
	if options.VerifyOnOpen {
		if err := main.verify(); err != nil {
			_ = main.close()
			_ = lock.close()
			return nil, err
		}
	}
	recovered, err := openAndRecoverWAL(absolute, main)
	if err != nil {
		_ = main.close()
		_ = lock.close()
		return nil, err
	}
	return &DB{
		path: absolute, identity: recovered.identity, pageCacheBytes: pageCacheBytes,
		main: main, overlay: recovered.overlay, lastTx: recovered.lastTx,
		walEnd: recovered.walEnd, walChecksum: recovered.walChecksum,
		walBaseTx: recovered.walBaseTx, checkpointTx: main.transaction,
		wal: recovered.file, lock: lock,
	}, nil
}

// Path returns the resolved main database file.
func (db *DB) Path() string { return db.path }

// ID returns the stable identity stored in the main-file and WAL headers.
func (db *DB) ID() string { return hex.EncodeToString(db.identity[:]) }

// LastTransaction returns the latest committed transaction visible to this
// handle.
func (db *DB) LastTransaction() (uint64, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return 0, err
	}
	return db.lastTx, nil
}

// Get returns a caller-owned copy of a value.
func (db *DB) Get(key []byte) ([]byte, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return nil, false, err
	}
	if mutation, found := db.overlay[string(key)]; found {
		if mutation.deleted {
			return nil, false, nil
		}
		return bytes.Clone(mutation.value), true, nil
	}
	return db.main.get(key)
}

// Begin reserves the database's single write transaction.
func (db *DB) Begin() (*Tx, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.stateErrorLocked(); err != nil {
		return nil, err
	}
	if db.activeTx != nil {
		return nil, ErrTransactionActive
	}
	tx := &Tx{db: db, payloadSize: payloadPrefixSize}
	db.activeTx = tx
	return tx, nil
}

// Close waits for an active commit, invalidates any uncommitted transaction,
// closes active snapshots and durable files, and releases the process writer
// lock.
func (db *DB) Close() error {
	db.commitMu.Lock()
	defer db.commitMu.Unlock()

	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.closed = true
	activeTx := db.activeTx
	db.activeTx = nil
	wal := db.wal
	main := db.main
	lock := db.lock
	db.wal = nil
	db.main = nil
	db.lock = nil
	db.mu.Unlock()
	if activeTx != nil {
		activeTx.invalidate()
	}
	snapshots := db.detachSnapshots()
	var snapshotErr error
	for _, snapshot := range snapshots {
		snapshotErr = errors.Join(snapshotErr, snapshot.Close())
	}

	var walErr, mainErr, lockErr error
	if wal != nil {
		walErr = wal.Close()
	}
	if main != nil {
		mainErr = main.close()
	}
	if lock != nil {
		lockErr = lock.close()
	}
	return errors.Join(snapshotErr, walErr, mainErr, lockErr)
}

func (db *DB) available() error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.stateErrorLocked()
}

func (db *DB) stateErrorLocked() error {
	if db.closed {
		return ErrClosed
	}
	if db.fatal != nil {
		return errors.Join(ErrUnavailable, db.fatal)
	}
	return nil
}

func (db *DB) finishTransaction(tx *Tx) {
	db.mu.Lock()
	if db.activeTx == tx {
		db.activeTx = nil
	}
	db.mu.Unlock()
}

func (db *DB) markUnavailable(err error) {
	db.mu.Lock()
	if db.fatal == nil {
		db.fatal = err
	}
	db.mu.Unlock()
}

func (db *DB) commit(operations []operation) (uint64, error) {
	if len(operations) == 0 {
		return 0, ErrEmptyTransaction
	}

	db.commitMu.Lock()
	defer db.commitMu.Unlock()

	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return 0, err
	}
	if db.lastTx == ^uint64(0) {
		db.mu.RUnlock()
		return 0, ErrTransactionIDOverflow
	}
	transaction := db.lastTx + 1
	wal := db.wal
	expectedEnd := db.walEnd
	db.mu.RUnlock()

	frame, err := encodeFrame(transaction, operations)
	if err != nil {
		return 0, err
	}
	walPosition, err := wal.Seek(0, io.SeekEnd)
	if err != nil {
		db.markUnavailable(err)
		return transaction, errors.Join(ErrUnavailable, err)
	}
	if walPosition != expectedEnd {
		err := fmt.Errorf("kitdb: WAL end changed from %d to %d outside the database owner", expectedEnd, walPosition)
		db.markUnavailable(err)
		return transaction, errors.Join(ErrUnavailable, err)
	}
	if _, err := writeAll(wal, frame); err != nil {
		db.markUnavailable(err)
		return transaction, errors.Join(ErrDurabilityUncertain, err)
	}
	if err := wal.Sync(); err != nil {
		db.markUnavailable(err)
		return transaction, errors.Join(ErrDurabilityUncertain, err)
	}

	db.mu.Lock()
	applyOperations(db.overlay, operations)
	db.lastTx = transaction
	db.walEnd = walPosition + int64(len(frame))
	db.walChecksum = frameChecksum(frame)
	db.mu.Unlock()
	db.dispatchCommit(transaction, operations)
	return transaction, nil
}

func writeAll(writer io.Writer, data []byte) (int, error) {
	written := 0
	for written < len(data) {
		n, err := writer.Write(data[written:])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}
