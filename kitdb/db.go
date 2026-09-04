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
	"time"
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

	closeMu        sync.Mutex
	submitMu       sync.Mutex
	commitMu       sync.Mutex
	historyMu      sync.Mutex
	replicaMu      sync.Mutex
	mu             sync.RWMutex
	catalogLoadMu  sync.Mutex
	sequenceMu     sync.Mutex
	sequenceLeases map[string]sequenceLease

	main                  *mainImage
	overlay               map[string]rowMutation
	lastTx                uint64
	lastDataTx            uint64
	walEnd                int64
	walChecksum           uint32
	walBaseTx             uint64
	lastCommitTime        int64
	checkpointTx          uint64
	activeTx              map[*Tx]struct{}
	activeTxBytes         int
	closing               bool
	closed                bool
	fatal                 error
	commitBatches         uint64
	committedTransactions uint64
	commitSyncs           uint64
	largestCommitBatch    int
	commitQueue           chan *commitRequest
	commitWriterDone      chan struct{}
	maxCommitBatch        int
	groupCommitDelay      time.Duration
	replicaMode           bool
	history               *historyState
	catalog               catalogState
	catalogLoaded         bool
	catalogErr            error

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
// CommitQueueSize bounds concurrently prepared transactions. MaxCommitBatch
// bounds the number sharing one WAL sync. GroupCommitDelay optionally waits
// for peers; zero only groups requests already queued. RetainHistory preserves
// checkpointed WAL ranges as immutable segments for ordered change replay.
// HistoryRetention is a process-local policy enforced by node maintenance;
// it requires RetainHistory and never weakens durable history pins. Replica
// makes this handle read-only to Begin while allowing verified one-way
// catch-up through CatchUpReplica.
type OpenOptions struct {
	PageCacheBytes   int64
	VerifyOnOpen     bool
	CommitQueueSize  int
	MaxCommitBatch   int
	GroupCommitDelay time.Duration
	RetainHistory    bool
	HistoryRetention HistoryRetentionPolicy
	Replica          bool
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
	if err := options.HistoryRetention.Validate(); err != nil {
		return nil, err
	}
	if options.HistoryRetention.Enabled() && !options.RetainHistory {
		return nil, errors.Join(
			ErrHistoryRetentionPolicy,
			fmt.Errorf("kitdb: bounded history retention requires RetainHistory"),
		)
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
	commitQueueSize, maxCommitBatch, groupCommitDelay, err := normalizeCommitOptions(options)
	if err != nil {
		return nil, err
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
	// Existing history is sticky and must participate in WAL recovery. New
	// retention is initialized only after the old WAL has been recovered and
	// checkpointed, so its wall-clock baseline never claims earlier frames.
	history, err := prepareHistory(absolute, main, false, options.Replica, 0)
	if err != nil {
		_ = main.close()
		_ = lock.close()
		return nil, err
	}
	recovered, err := openAndRecoverWAL(absolute, main, history)
	if err != nil {
		_ = main.close()
		_ = lock.close()
		return nil, err
	}
	database := &DB{
		path: absolute, identity: recovered.identity, pageCacheBytes: pageCacheBytes,
		main: main, overlay: recovered.overlay, lastTx: recovered.lastTx, lastDataTx: recovered.lastTx,
		walEnd: recovered.walEnd, walChecksum: recovered.walChecksum,
		walBaseTx: recovered.walBaseTx, lastCommitTime: recovered.lastCommitTime,
		checkpointTx:     main.transaction,
		activeTx:         make(map[*Tx]struct{}),
		commitQueue:      make(chan *commitRequest, commitQueueSize),
		commitWriterDone: make(chan struct{}), maxCommitBatch: maxCommitBatch,
		groupCommitDelay: groupCommitDelay,
		replicaMode:      options.Replica,
		history:          history,
		catalog:          newCatalogState(),
		wal:              recovered.file, lock: lock,
	}
	if options.VerifyOnOpen {
		catalog, err := loadCatalogState(main, recovered.overlay)
		if err != nil {
			_ = recovered.file.Close()
			_ = main.close()
			_ = lock.close()
			return nil, err
		}
		database.catalog = catalog
		database.catalogLoaded = true
	}
	go database.runCommitWriter()
	if options.RetainHistory && history == nil {
		if _, err := database.Checkpoint(); err != nil {
			return nil, errors.Join(err, database.Close())
		}
		database.mu.RLock()
		currentMain := database.main
		minimumBaseCommitTime := database.lastCommitTime
		database.mu.RUnlock()
		initialized, err := prepareHistory(
			absolute, currentMain, true, options.Replica, minimumBaseCommitTime,
		)
		if err != nil {
			return nil, errors.Join(err, database.Close())
		}
		database.mu.Lock()
		database.history = initialized
		database.lastCommitTime = initialized.baseCommitTime
		database.mu.Unlock()
	}
	return database, nil
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

// CurrentCursor returns the exact durable transaction/checksum boundary
// visible to this handle. It is also the restart watermark of a replica.
func (db *DB) CurrentCursor() (HistoryCursor, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return HistoryCursor{}, err
	}
	return HistoryCursor{
		DatabaseID:  hex.EncodeToString(db.identity[:]),
		Transaction: db.lastTx,
		Checksum:    db.walChecksum,
	}, nil
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

// Begin creates one bounded write transaction. Transactions may be prepared
// concurrently; one database-owned writer assigns their commit order.
func (db *DB) Begin() (*Tx, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.stateErrorLocked(); err != nil {
		return nil, err
	}
	if db.replicaMode {
		return nil, ErrReplicaReadOnly
	}
	if len(db.activeTx) >= cap(db.commitQueue) {
		return nil, ErrTooManyTransactions
	}
	if db.activeTxBytes > maxPayloadSize-timedPayloadPrefixSize {
		return nil, ErrActiveTransactionMemory
	}
	tx := &Tx{db: db, payloadSize: timedPayloadPrefixSize}
	db.activeTx[tx] = struct{}{}
	db.activeTxBytes += timedPayloadPrefixSize
	return tx, nil
}

// Close stops admission, drains commits already accepted by the writer,
// invalidates uncommitted transactions, closes snapshots and durable files,
// and releases the process writer lock.
func (db *DB) Close() error {
	db.closeMu.Lock()
	defer db.closeMu.Unlock()

	db.submitMu.Lock()
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		db.submitMu.Unlock()
		return nil
	}
	db.closing = true
	activeTransactions := make([]*Tx, 0, len(db.activeTx))
	for transaction := range db.activeTx {
		activeTransactions = append(activeTransactions, transaction)
	}
	commitQueue := db.commitQueue
	commitWriterDone := db.commitWriterDone
	db.mu.Unlock()
	close(commitQueue)
	db.submitMu.Unlock()

	for _, transaction := range activeTransactions {
		transaction.invalidate()
	}
	<-commitWriterDone

	db.commitMu.Lock()
	defer db.commitMu.Unlock()

	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.closed = true
	db.activeTx = nil
	db.activeTxBytes = 0
	wal := db.wal
	main := db.main
	lock := db.lock
	db.wal = nil
	db.main = nil
	db.lock = nil
	db.mu.Unlock()
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

func (db *DB) stateErrorLocked() error {
	if db.closing || db.closed {
		return ErrClosed
	}
	if db.fatal != nil {
		return errors.Join(ErrUnavailable, db.fatal)
	}
	return nil
}

func (db *DB) finishTransaction(tx *Tx, payloadSize int) {
	db.mu.Lock()
	if _, active := db.activeTx[tx]; active {
		delete(db.activeTx, tx)
		db.activeTxBytes -= payloadSize
		if db.activeTxBytes < 0 {
			db.activeTxBytes = 0
		}
	}
	db.mu.Unlock()
}

func (db *DB) reserveTransactionBytes(tx *Tx, size int) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if err := db.stateErrorLocked(); err != nil {
		return err
	}
	if _, active := db.activeTx[tx]; !active {
		return ErrTransactionClosed
	}
	if size > maxPayloadSize-db.activeTxBytes {
		return ErrActiveTransactionMemory
	}
	db.activeTxBytes += size
	return nil
}

func (db *DB) markUnavailable(err error) {
	db.mu.Lock()
	if db.fatal == nil {
		db.fatal = err
	}
	db.mu.Unlock()
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
