package kitdb

import (
	"bytes"
	"fmt"
	"os"
	"sync"
)

const maxActiveSnapshots = 32

// Snapshot is an immutable logical view at one committed transaction. It owns
// a read handle to the captured main generation and a copy of the WAL overlay.
// Close releases that handle and must not be deferred indefinitely because an
// active snapshot prevents full compaction of its database file.
type Snapshot struct {
	mu          sync.RWMutex
	owner       *DB
	main        *mainImage
	overlay     map[string]rowMutation
	transaction uint64
	closed      bool
}

// RangeOptions bounds an ascending cursor. Start is inclusive, End is
// exclusive, Prefix intersects both bounds, and Limit zero means unbounded.
// All option bytes are copied when the cursor is created.
type RangeOptions struct {
	Start  []byte
	End    []byte
	Prefix []byte
	Limit  int
}

// Cursor streams one snapshot range in raw-key order. A cursor is owned by one
// goroutine; Key and Value return caller-owned copies of the current entry.
type Cursor struct {
	snapshot *Snapshot
	iterator *logicalRowIterator
	end      []byte
	prefix   []byte
	limit    int
	emitted  int
	key      []byte
	value    []byte
	err      error
	done     bool
}

// Snapshot captures the latest committed logical state without retaining the
// database state lock. Commits and incremental checkpoints can continue while
// the snapshot is read. At most 32 snapshots may be active per database.
func (db *DB) Snapshot() (*Snapshot, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return nil, err
	}

	db.snapshotMu.Lock()
	defer db.snapshotMu.Unlock()
	if len(db.snapshots) >= maxActiveSnapshots {
		return nil, ErrTooManySnapshots
	}
	file, err := os.Open(db.path)
	if err != nil {
		return nil, fmt.Errorf("kitdb: open snapshot generation: %w", err)
	}
	main := *db.main
	main.file = file
	snapshot := &Snapshot{
		owner:       db,
		main:        &main,
		overlay:     cloneSnapshotOverlay(db.overlay),
		transaction: db.lastTx,
	}
	if db.snapshots == nil {
		db.snapshots = make(map[*Snapshot]struct{})
	}
	db.snapshots[snapshot] = struct{}{}
	return snapshot, nil
}

// Transaction returns the latest committed transaction visible to the
// snapshot. It remains available after Close as immutable metadata.
func (snapshot *Snapshot) Transaction() uint64 {
	if snapshot == nil {
		return 0
	}
	return snapshot.transaction
}

// Get reads from the immutable snapshot and returns a caller-owned value.
func (snapshot *Snapshot) Get(key []byte) ([]byte, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	if snapshot == nil {
		return nil, false, ErrSnapshotClosed
	}
	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	if snapshot.closed || snapshot.main == nil {
		return nil, false, ErrSnapshotClosed
	}
	if mutation, found := snapshot.overlay[string(key)]; found {
		if mutation.deleted {
			return nil, false, nil
		}
		return bytes.Clone(mutation.value), true, nil
	}
	return snapshot.main.get(key)
}

// Cursor creates an ascending, seekable cursor over this immutable snapshot.
func (snapshot *Snapshot) Cursor(options RangeOptions) (*Cursor, error) {
	if snapshot == nil {
		return nil, ErrSnapshotClosed
	}
	if options.Limit < 0 {
		return nil, fmt.Errorf("kitdb: range limit cannot be negative")
	}
	if err := validateRangeBound("start", options.Start); err != nil {
		return nil, err
	}
	if err := validateRangeBound("end", options.End); err != nil {
		return nil, err
	}
	if err := validateRangeBound("prefix", options.Prefix); err != nil {
		return nil, err
	}

	start := bytes.Clone(options.Start)
	prefix := bytes.Clone(options.Prefix)
	if len(prefix) != 0 && (len(start) == 0 || bytes.Compare(start, prefix) < 0) {
		start = bytes.Clone(prefix)
	}
	end := bytes.Clone(options.End)
	if prefixEnd := rangePrefixEnd(prefix); prefixEnd != nil && (len(end) == 0 || bytes.Compare(prefixEnd, end) < 0) {
		end = prefixEnd
	}

	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	if snapshot.closed || snapshot.main == nil {
		return nil, ErrSnapshotClosed
	}
	cursor := &Cursor{
		snapshot: snapshot,
		iterator: newLogicalRowIterator(snapshot.main, snapshot.overlay, start),
		end:      end,
		prefix:   prefix,
		limit:    options.Limit,
	}
	if len(end) != 0 && bytes.Compare(start, end) >= 0 {
		cursor.done = true
	}
	return cursor, nil
}

// Next advances to the next matching entry.
func (cursor *Cursor) Next() bool {
	if cursor == nil || cursor.done || cursor.err != nil {
		return false
	}
	if cursor.limit != 0 && cursor.emitted >= cursor.limit {
		cursor.done = true
		return false
	}

	cursor.snapshot.mu.RLock()
	defer cursor.snapshot.mu.RUnlock()
	if cursor.snapshot.closed || cursor.snapshot.main == nil {
		cursor.err = ErrSnapshotClosed
		return false
	}
	for {
		key, value, found, err := cursor.iterator.next()
		if err != nil {
			cursor.err = err
			return false
		}
		if !found {
			cursor.done = true
			return false
		}
		if len(cursor.end) != 0 && bytes.Compare(key, cursor.end) >= 0 {
			cursor.done = true
			return false
		}
		if len(cursor.prefix) != 0 && !bytes.HasPrefix(key, cursor.prefix) {
			cursor.done = true
			return false
		}
		cursor.key = bytes.Clone(key)
		cursor.value = bytes.Clone(value)
		cursor.emitted++
		return true
	}
}

// Key returns a caller-owned copy of the current key.
func (cursor *Cursor) Key() []byte {
	if cursor == nil {
		return nil
	}
	return bytes.Clone(cursor.key)
}

// Value returns a caller-owned copy of the current value.
func (cursor *Cursor) Value() []byte {
	if cursor == nil {
		return nil
	}
	return bytes.Clone(cursor.value)
}

// Err reports the first iteration error, if any.
func (cursor *Cursor) Err() error {
	if cursor == nil {
		return nil
	}
	return cursor.err
}

// Close releases cursor-owned references. It does not close the snapshot.
func (cursor *Cursor) Close() error {
	if cursor == nil {
		return nil
	}
	cursor.done = true
	cursor.iterator = nil
	cursor.key = nil
	cursor.value = nil
	return nil
}

// Close releases the captured generation. It is safe to call more than once.
func (snapshot *Snapshot) Close() error {
	if snapshot == nil {
		return nil
	}
	snapshot.mu.Lock()
	if snapshot.closed {
		snapshot.mu.Unlock()
		return nil
	}
	snapshot.closed = true
	owner := snapshot.owner
	snapshot.owner = nil
	main := snapshot.main
	snapshot.main = nil
	snapshot.overlay = nil
	snapshot.mu.Unlock()

	if owner != nil {
		owner.unregisterSnapshot(snapshot)
	}
	return main.close()
}

func cloneSnapshotOverlay(source map[string]rowMutation) map[string]rowMutation {
	cloned := make(map[string]rowMutation, len(source))
	for key, mutation := range source {
		cloned[key] = rowMutation{value: bytes.Clone(mutation.value), deleted: mutation.deleted}
	}
	return cloned
}

func rangePrefixEnd(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	end := bytes.Clone(prefix)
	for index := len(end) - 1; index >= 0; index-- {
		if end[index] != 0xff {
			end[index]++
			return end[:index+1]
		}
	}
	return nil
}

func validateRangeBound(name string, bound []byte) error {
	if len(bound) > maxKeySize {
		return fmt.Errorf("kitdb: range %s exceeds the current key limit", name)
	}
	return nil
}

func (db *DB) activeSnapshotCount() int {
	db.snapshotMu.Lock()
	defer db.snapshotMu.Unlock()
	return len(db.snapshots)
}

func (db *DB) unregisterSnapshot(snapshot *Snapshot) {
	db.snapshotMu.Lock()
	delete(db.snapshots, snapshot)
	db.snapshotMu.Unlock()
}

func (db *DB) detachSnapshots() []*Snapshot {
	db.snapshotMu.Lock()
	snapshots := make([]*Snapshot, 0, len(db.snapshots))
	for snapshot := range db.snapshots {
		snapshots = append(snapshots, snapshot)
	}
	db.snapshots = nil
	db.snapshotMu.Unlock()
	return snapshots
}
