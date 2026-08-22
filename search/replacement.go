package search

import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
	"io"
	"path/filepath"
)

const replacementIdentifierBufferBytes = 32 << 10

// Replacement incrementally builds one complete copy-on-write index
// generation. It owns its IndexWriter until Commit or Abort. Replacement is
// not safe for concurrent use.
type Replacement struct {
	writer *IndexWriter
	active bool
}

// BeginReplacement starts an empty streaming replacement. Existing committed
// readers remain unchanged until Replacement.Commit publishes one manifest.
// The writer must not contain any other uncommitted mutation.
func (writer *IndexWriter) BeginReplacement() (*Replacement, error) {
	if err := writer.ensureOpen(); err != nil {
		return nil, err
	}
	if writer.replacement != nil {
		return nil, ErrReplacementActive
	}
	if writer.hasPendingChanges() {
		return nil, ErrPendingChanges
	}
	replacement := &Replacement{writer: writer, active: true}
	writer.replacement = replacement
	return replacement, nil
}

func (replacement *Replacement) activeWriter() (*IndexWriter, error) {
	if replacement == nil || !replacement.active || replacement.writer == nil {
		return nil, ErrClosed
	}
	writer := replacement.writer
	if err := writer.ensureOpen(); err != nil {
		return nil, err
	}
	if writer.replacement != replacement {
		return nil, ErrClosed
	}
	return writer, nil
}

// Add analyzes one document immediately. Only the current bounded segment is
// retained in memory; full replacement payloads are never accumulated here.
func (replacement *Replacement) Add(ctx context.Context, document Document) error {
	writer, err := replacement.activeWriter()
	if err != nil {
		return err
	}
	return writer.add(ctx, document, false)
}

// Flush writes the current bounded builder as an immutable, uncommitted
// segment. It is optional because Add flushes automatically at its thresholds.
func (replacement *Replacement) Flush(ctx context.Context) error {
	writer, err := replacement.activeWriter()
	if err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("search: replacement flush context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writer.flush(ctx)
}

// Commit validates identifiers across every replacement segment and atomically
// publishes the new manifest. On a pre-publication error the replacement stays
// active, allowing the caller to retry Commit or Abort it. A published
// ErrDurabilityUncertain generation is adopted and closes the replacement.
func (replacement *Replacement) Commit(ctx context.Context) (IndexInfo, error) {
	writer, err := replacement.activeWriter()
	if err != nil {
		return IndexInfo{}, err
	}
	return writer.commit(ctx)
}

// Abort discards every uncommitted replacement artifact. It is idempotent and
// never modifies the last committed generation.
func (replacement *Replacement) Abort() error {
	if replacement == nil || !replacement.active || replacement.writer == nil {
		return nil
	}
	writer := replacement.writer
	if writer.replacement != replacement {
		replacement.active = false
		replacement.writer = nil
		return nil
	}
	return writer.abortReplacement()
}

func (writer *IndexWriter) finishReplacement() {
	if writer == nil || writer.replacement == nil {
		return
	}
	replacement := writer.replacement
	writer.replacement = nil
	replacement.active = false
	replacement.writer = nil
}

// validatePendingIdentifiers performs a bounded k-way merge over sorted .ki
// sidecars. It detects exact duplicate identifiers across immutable segments
// without retaining a set proportional to the document count.
func (writer *IndexWriter) validatePendingIdentifiers(ctx context.Context) (returnErr error) {
	if len(writer.pending) < 2 {
		return nil
	}
	if ctx == nil {
		return errors.New("search: identifier validation context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	cursors := make([]pendingIdentifierCursor, len(writer.pending))
	segments := make([]*Segment, len(writer.pending))
	defer func() {
		for position := range cursors {
			if cursors[position].index != nil {
				returnErr = errors.Join(returnErr, cursors[position].index.Close())
			}
			if segments[position] != nil {
				returnErr = errors.Join(returnErr, segments[position].Close())
			}
		}
	}()

	items := make(pendingIdentifierHeap, 0, len(writer.pending))
	for position, pending := range writer.pending {
		identifier, err := openIdentifierIndex(
			filepath.Join(writer.directory, pending.identifierName), writer.schema.fingerprint,
			pending.documents, pending.identifierBytes,
		)
		if err != nil {
			return err
		}
		cursors[position] = newPendingIdentifierCursor(identifier)
		entry, more, err := cursors[position].next()
		if err != nil {
			return err
		}
		if more {
			heap.Push(&items, pendingIdentifierHeapItem{cursor: position, entry: entry})
		}
	}

	var groupHash [16]byte
	group := make([]pendingIdentifierLocation, 0, 2)
	var identifiers map[string]struct{}
	started := false
	processed := uint64(0)
	for items.Len() != 0 {
		if processed&8191 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		processed++
		item := heap.Pop(&items).(pendingIdentifierHeapItem)
		location := pendingIdentifierLocation{segment: item.cursor, document: item.entry.document}
		if !started || item.entry.hash != groupHash {
			started = true
			groupHash = item.entry.hash
			group = group[:0]
			identifiers = nil
		}
		if len(group) != 0 {
			if identifiers == nil {
				first, err := writer.pendingIdentifierValue(group[0], segments)
				if err != nil {
					return err
				}
				identifiers = map[string]struct{}{first: {}}
			}
			identifier, err := writer.pendingIdentifierValue(location, segments)
			if err != nil {
				return err
			}
			if _, duplicate := identifiers[identifier]; duplicate {
				return ErrPendingDocument
			}
			identifiers[identifier] = struct{}{}
		}
		group = append(group, location)

		next, more, err := cursors[item.cursor].next()
		if err != nil {
			return err
		}
		if more {
			heap.Push(&items, pendingIdentifierHeapItem{cursor: item.cursor, entry: next})
		}
	}
	return nil
}

func (writer *IndexWriter) pendingIdentifierValue(location pendingIdentifierLocation, segments []*Segment) (string, error) {
	segment := segments[location.segment]
	if segment == nil {
		opened, err := OpenSegment(filepath.Join(writer.directory, writer.pending[location.segment].name), writer.schema)
		if err != nil {
			return "", err
		}
		segments[location.segment] = opened
		segment = opened
	}
	return segment.documentIdentifier(location.document)
}

type pendingIdentifierLocation struct {
	segment  int
	document uint32
}

type pendingIdentifierCursor struct {
	index     *identifierIndex
	reader    *bufio.Reader
	checksum  hash.Hash32
	remaining uint32
	previous  identifierIndexEntry
	hasEntry  bool
}

func newPendingIdentifierCursor(index *identifierIndex) pendingIdentifierCursor {
	checksum := crc32.New(crcTable)
	section := io.NewSectionReader(index.file, identifierIndexHeaderSize, int64(index.bodyLength))
	return pendingIdentifierCursor{
		index:    index,
		reader:   bufio.NewReaderSize(io.TeeReader(section, checksum), replacementIdentifierBufferBytes),
		checksum: checksum, remaining: index.documents,
	}
}

func (cursor *pendingIdentifierCursor) next() (identifierIndexEntry, bool, error) {
	if cursor.remaining == 0 {
		return identifierIndexEntry{}, false, nil
	}
	var encoded [identifierIndexEntrySize]byte
	if _, err := io.ReadFull(cursor.reader, encoded[:]); err != nil {
		return identifierIndexEntry{}, false, corruptIndexf("read pending identifier index: %v", err)
	}
	var entry identifierIndexEntry
	copy(entry.hash[:], encoded[:16])
	entry.document = binary.LittleEndian.Uint32(encoded[16:20])
	if entry.document >= cursor.index.documents || (cursor.hasEntry && compareIdentifierIndexEntry(cursor.previous, entry) >= 0) {
		return identifierIndexEntry{}, false, corruptIndexf("pending identifier index is invalid or unordered")
	}
	cursor.previous = entry
	cursor.hasEntry = true
	cursor.remaining--
	if cursor.remaining == 0 && cursor.checksum.Sum32() != cursor.index.bodyCRC {
		return identifierIndexEntry{}, false, corruptIndexf("pending identifier index checksum does not match")
	}
	return entry, true, nil
}

type pendingIdentifierHeapItem struct {
	cursor int
	entry  identifierIndexEntry
}

type pendingIdentifierHeap []pendingIdentifierHeapItem

func (items pendingIdentifierHeap) Len() int { return len(items) }
func (items pendingIdentifierHeap) Less(left, right int) bool {
	if comparison := bytes.Compare(items[left].entry.hash[:], items[right].entry.hash[:]); comparison != 0 {
		return comparison < 0
	}
	if items[left].cursor != items[right].cursor {
		return items[left].cursor < items[right].cursor
	}
	return items[left].entry.document < items[right].entry.document
}
func (items pendingIdentifierHeap) Swap(left, right int) {
	items[left], items[right] = items[right], items[left]
}
func (items *pendingIdentifierHeap) Push(value any) {
	*items = append(*items, value.(pendingIdentifierHeapItem))
}
func (items *pendingIdentifierHeap) Pop() any {
	old := *items
	last := old[len(old)-1]
	*items = old[:len(old)-1]
	return last
}
