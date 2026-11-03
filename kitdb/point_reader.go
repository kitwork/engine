package kitdb

import (
	"bytes"
	"sync"
)

// PointReader amortizes small-page decoding buffers over a group of snapshot
// lookups. It owns at most one 128 KiB page and 128 row positions, never publishes
// that mutable page to the shared cache, and always returns caller-owned values.
// Oversized pages retain the ordinary read path. Close releases private buffers;
// closing the snapshot invalidates this reader too. A reader must not be copied.
// The first cacheable miss still warms the shared cache, so repeated small
// working sets converge to cache hits instead of remaining permanently cold.
type PointReader struct {
	mu       sync.Mutex
	snapshot *Snapshot
	page     rowPage
	warmed   bool
}

// Writers finish a page after appending a record, so the 64 KiB target is not
// a hard maximum. Larger individual values still use the ordinary read path.
const pointReaderPageBytes = 2 * mainIndexBlockBytes

// PointReader creates a query-local reader without opening another file or
// pinning another generation. The caller still owns the snapshot's lifetime.
func (snapshot *Snapshot) PointReader() (*PointReader, error) {
	if snapshot == nil {
		return nil, ErrSnapshotClosed
	}
	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	if snapshot.closed || snapshot.main == nil {
		return nil, ErrSnapshotClosed
	}
	return &PointReader{snapshot: snapshot}, nil
}

func (reader *PointReader) Close() {
	if reader == nil {
		return
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.page = rowPage{}
	reader.snapshot = nil
	reader.warmed = false
}

func (reader *PointReader) Get(key []byte) ([]byte, bool, error) {
	return reader.get(key, nil)
}

// GetWithStats reports actual cache probes and page reads, not buffer reuse as
// a cache hit. The private buffer is revalidated on every miss, including errors.
func (reader *PointReader) GetWithStats(key []byte) ([]byte, bool, CursorStats, error) {
	var stats CursorStats
	value, found, err := reader.get(key, &stats)
	return value, found, stats, err
}

func (reader *PointReader) get(key []byte, stats *CursorStats) ([]byte, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	if reader == nil {
		return nil, false, ErrSnapshotClosed
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	snapshot := reader.snapshot
	if snapshot == nil {
		return nil, false, ErrSnapshotClosed
	}
	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	if snapshot.closed || snapshot.main == nil {
		return nil, false, ErrSnapshotClosed
	}
	if mutation, found := snapshot.overlay[string(key)]; found {
		if stats != nil {
			stats.OverlayEntriesVisited++
		}
		if mutation.deleted {
			return nil, false, nil
		}
		return bytes.Clone(mutation.value), true, nil
	}
	main := snapshot.main
	if main.formatVersion == mainFormatVersion {
		for i := len(main.segments) - 1; i >= 0; i-- {
			segment := main.segments[i]
			value, found, deleted, err := main.getFromBlocksWithReader(key, segment.firstBlock, segment.blockCount, stats, reader)
			if err != nil {
				return nil, false, err
			}
			if found {
				if deleted {
					return nil, false, nil
				}
				return value, true, nil
			}
		}
		return nil, false, nil
	}
	value, found, _, err := main.getFromBlocksWithReader(key, 0, len(main.blocks), stats, reader)
	return value, found, err
}
