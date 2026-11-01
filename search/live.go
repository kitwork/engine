package search

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"sync"
	"sync/atomic"
)

var (
	// ErrRevisionUnavailable means a live view was asked for a revision older
	// than its base: the changes before the base are no longer kept.
	ErrRevisionUnavailable = errors.New("search: revision is older than the live base")
	// ErrRevisionAhead means a live view was asked for a revision that has not
	// been applied yet. The caller applies the missing changes and retries.
	ErrRevisionAhead = errors.New("search: revision has not been applied to the live index")
)

// Change is one source mutation. Delete removes ID; otherwise Fields is the
// document's complete new content, inserted or replacing every earlier version.
type Change struct {
	ID     string
	Fields map[string]string
	Delete bool
}

// LiveOptions bound the in-memory part of a Live index.
type LiveOptions struct {
	// Segment bounds each in-memory piece of changed documents. A view whose
	// changes no longer fit fails with ErrSegmentFull; fold the changes into a
	// committed generation and Rebase before that point.
	Segment BuildOptions
}

// Live answers searches at an exact source revision without publishing a
// generation for every change. It keeps a committed base Index, which reflects
// the source up to its revision, and every change applied after it in memory.
//
// View(revision) assembles one immutable snapshot from the base's segments,
// in-memory pieces holding the changed documents, and in-memory tombstones for
// every version a later change replaced or deleted, in the base or in an
// earlier piece. Pieces are shared by views: a view encodes only the changes
// no piece covers yet, and more than a few pieces are merged into one.
// Document frequency, average field length and therefore every BM25 score are
// those of an index rebuilt from the source at that revision, and every query
// shape of Index runs on it unchanged.
//
// Changes are folded into a committed generation by the caller, who owns the
// IndexWriter: Upsert or Delete what Changes returns, Commit, open the new
// Index and pass it to Rebase with the same revision. Open views keep the
// base they were built on until they are closed.
type Live struct {
	schema  Schema
	options BuildOptions

	mu       sync.Mutex
	base     *liveBase
	changes  []liveChange
	sequence uint64
	pieces   []*livePiece
	latest   uint64
	cached   *liveSnapshot
	closed   bool
}

type liveChange struct {
	sequence uint64
	revision uint64
	change   Change
	analyzed analyzedDocument
}

// livePiece is one immutable in-memory segment built from the contiguous run
// of changes [from, to), by sequence. It holds, for every document the run
// changed, the latest version inside the run unless that was a delete.
type livePiece struct {
	from, to  uint64
	segment   *Segment
	documents map[string]uint32
}

const maximumLivePieces = 8

// liveBase is one committed Index shared by Live and the snapshots built on
// it. The last release closes it.
type liveBase struct {
	index      *Index
	revision   uint64
	references atomic.Int64

	mu          sync.Mutex
	directories []*identifierDirectory
	// owned holds the directories this base closes on its last release, by
	// identifier file name. Rebase moves the ones its successor keeps.
	owned     map[string]*identifierDirectory
	locations map[string][]documentLocation
}

// liveSnapshot is one assembled view, shared by every LiveView of the same
// change set. The last release closes its in-memory segment and releases its base.
type liveSnapshot struct {
	index      *Index
	base       *liveBase
	included   int
	references atomic.Int64
}

// LiveView is one immutable snapshot at Revision. Every Index query runs on it.
// Close releases it; the view never changes while it is open.
type LiveView struct {
	*Index
	Revision uint64
	snapshot *liveSnapshot
	closed   atomic.Bool
}

// NewLive serves base, which reflects the source up to revision. Live takes
// ownership of base and closes it once neither Live nor any view uses it.
func NewLive(base *Index, revision uint64, options LiveOptions) (*Live, error) {
	if err := base.ensureOpen(); err != nil {
		return nil, err
	}
	if base.shared != 0 {
		return nil, fmt.Errorf("search: a live base must be a committed index, not a view")
	}
	normalized, err := normalizeBuildOptions(options.Segment)
	if err != nil {
		return nil, err
	}
	return &Live{
		schema: base.schema, options: normalized,
		base: newLiveBase(base, revision), latest: revision,
	}, nil
}

func newLiveBase(index *Index, revision uint64) *liveBase {
	base := &liveBase{
		index: index, revision: revision,
		directories: make([]*identifierDirectory, len(index.segments)),
		owned:       make(map[string]*identifierDirectory),
		locations:   make(map[string][]documentLocation),
	}
	base.references.Store(1)
	return base
}

// BaseRevision is the revision the current base reflects.
func (live *Live) BaseRevision() uint64 {
	live.mu.Lock()
	defer live.mu.Unlock()
	return live.base.revision
}

// Revision is the latest applied revision.
func (live *Live) Revision() uint64 {
	live.mu.Lock()
	defer live.mu.Unlock()
	return live.latest
}

// Pending reports how many changes are kept in memory after the base.
func (live *Live) Pending() int {
	live.mu.Lock()
	defer live.mu.Unlock()
	return len(live.changes)
}

// Apply records the changes of one source revision, in order. revision must be
// greater than every revision applied before; an empty change list only
// advances the revision. Documents are analyzed here, once, so an invalid
// document is rejected before it can reach any view.
func (live *Live) Apply(ctx context.Context, revision uint64, changes []Change) error {
	if ctx == nil {
		return fmt.Errorf("search: live apply context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared := make([]liveChange, 0, len(changes))
	for _, change := range changes {
		if err := validateDocumentIdentifier(change.ID, live.options); err != nil {
			return err
		}
		entry := liveChange{revision: revision, change: Change{ID: change.ID, Delete: change.Delete}}
		if !change.Delete {
			entry.change.Fields = maps.Clone(change.Fields)
			analyzed, err := analyzeDocument(ctx, live.schema, live.options, Document{ID: change.ID, Fields: entry.change.Fields})
			if err != nil {
				return err
			}
			entry.analyzed = analyzed
		}
		prepared = append(prepared, entry)
	}

	live.mu.Lock()
	defer live.mu.Unlock()
	if live.closed {
		return ErrClosed
	}
	if revision <= live.latest {
		return fmt.Errorf("search: live revision %d is not after %d", revision, live.latest)
	}
	for position := range prepared {
		prepared[position].sequence = live.sequence
		live.sequence++
	}
	live.changes = append(live.changes, prepared...)
	live.latest = revision
	return nil
}

// View returns the snapshot at revision, which must lie between the base
// revision and the latest applied one. Views of the same change set share one
// assembled snapshot; the first view after a change pays the assembly.
func (live *Live) View(ctx context.Context, revision uint64) (*LiveView, error) {
	if ctx == nil {
		return nil, fmt.Errorf("search: live view context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	live.mu.Lock()
	if live.closed {
		live.mu.Unlock()
		return nil, ErrClosed
	}
	if revision < live.base.revision {
		live.mu.Unlock()
		return nil, fmt.Errorf("%w: %d is before %d", ErrRevisionUnavailable, revision, live.base.revision)
	}
	if revision > live.latest {
		live.mu.Unlock()
		return nil, fmt.Errorf("%w: %d is after %d", ErrRevisionAhead, revision, live.latest)
	}
	included := sort.Search(len(live.changes), func(position int) bool {
		return live.changes[position].revision > revision
	})
	if cached := live.cached; cached != nil && cached.base == live.base && cached.included == included {
		cached.references.Add(1)
		live.mu.Unlock()
		return &LiveView{Index: cached.index, Revision: revision, snapshot: cached}, nil
	}
	base := live.base
	base.references.Add(1)
	// Applied changes are never modified and Rebase replaces the slice, so this
	// prefix stays valid after the lock is released.
	changes := live.changes[:included:included]
	pieces := live.pieces
	live.mu.Unlock()

	snapshot, used, err := buildLiveSnapshot(ctx, live.schema, live.options, base, changes, pieces)
	base.release()
	if err != nil {
		return nil, err
	}

	live.mu.Lock()
	if !live.closed && live.base == base && pieceCoverage(used) > pieceCoverage(live.pieces) {
		live.pieces = used
	}
	if !live.closed && live.base == base && (live.cached == nil || live.cached.base != base || live.cached.included < included) {
		if live.cached != nil {
			live.cached.release()
		}
		snapshot.references.Add(1)
		live.cached = snapshot
	}
	live.mu.Unlock()
	return &LiveView{Index: snapshot.index, Revision: revision, snapshot: snapshot}, nil
}

// Changes returns, for every document changed after the base and up to
// revision, its latest change, in the order of those latest changes. Folding
// them into the base's IndexWriter (Upsert, or Delete) and committing yields the
// index that Rebase expects for revision. Replaying them is safe: Upsert and
// Delete replace every live occurrence.
func (live *Live) Changes(revision uint64) ([]Change, error) {
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.closed {
		return nil, ErrClosed
	}
	if revision < live.base.revision || revision > live.latest {
		return nil, fmt.Errorf("search: live revision %d is outside %d..%d", revision, live.base.revision, live.latest)
	}
	included := sort.Search(len(live.changes), func(position int) bool {
		return live.changes[position].revision > revision
	})
	latest := latestLiveChanges(live.changes[:included])
	changes := make([]Change, 0, len(latest))
	for _, entry := range latest {
		change := entry.change
		change.Fields = maps.Clone(change.Fields)
		changes = append(changes, change)
	}
	return changes, nil
}

// Rebase replaces the base with index, which must reflect the source up to
// revision. Changes up to revision are dropped; later ones stay. Live takes
// ownership of index; the previous base closes when its last view closes.
func (live *Live) Rebase(index *Index, revision uint64) error {
	if err := index.ensureOpen(); err != nil {
		return err
	}
	if index.shared != 0 {
		return fmt.Errorf("search: a live base must be a committed index, not a view")
	}
	if index.schema.fingerprint != live.schema.fingerprint {
		return ErrSchemaMismatch
	}
	live.mu.Lock()
	if live.closed {
		live.mu.Unlock()
		return ErrClosed
	}
	if revision < live.base.revision || revision > live.latest {
		live.mu.Unlock()
		return fmt.Errorf("search: live revision %d is outside %d..%d", revision, live.base.revision, live.latest)
	}
	kept := sort.Search(len(live.changes), func(position int) bool {
		return live.changes[position].revision > revision
	})
	previous, cached := live.base, live.cached
	live.base = newLiveBase(index, revision)
	previous.handOver(live.base)
	live.changes = append([]liveChange(nil), live.changes[kept:]...)
	live.cached, live.pieces = nil, nil
	live.mu.Unlock()
	if cached != nil {
		cached.release()
	}
	previous.release()
	return nil
}

// Close stops Live. Views that are still open keep working until they close.
func (live *Live) Close() error {
	live.mu.Lock()
	if live.closed {
		live.mu.Unlock()
		return nil
	}
	live.closed = true
	base, cached := live.base, live.cached
	live.cached, live.changes, live.pieces = nil, nil, nil
	live.mu.Unlock()
	if cached != nil {
		cached.release()
	}
	base.release()
	return nil
}

// Close releases the view. Searches on it must have finished.
func (view *LiveView) Close() error {
	if view == nil || !view.closed.CompareAndSwap(false, true) {
		return nil
	}
	view.snapshot.release()
	return nil
}

func (base *liveBase) release() {
	if base.references.Add(-1) != 0 {
		return
	}
	base.mu.Lock()
	for name, directory := range base.owned {
		_ = directory.index.Close()
		delete(base.owned, name)
	}
	clear(base.directories)
	base.mu.Unlock()
	_ = base.index.Close()
}

// handOver gives next the identifier directories of segments it still has, so
// a rebase does not reread them. Segment and identifier files never change, so
// a directory stays valid for every base that lists its file.
func (base *liveBase) handOver(next *liveBase) {
	base.mu.Lock()
	defer base.mu.Unlock()
	for position, entry := range next.index.entries {
		directory, owned := base.owned[entry.identifierName]
		if !owned || entry.identifierName == "" {
			continue
		}
		delete(base.owned, entry.identifierName)
		if next.index.identifiers != nil {
			// A container section belongs to its generation: read blocks through
			// the next base's opening, keep the filter and samples.
			lookup, err := next.index.openIdentifiers(position)
			if err != nil {
				continue
			}
			directory = directory.rebind(lookup)
		}
		next.directories[position] = directory
		next.owned[entry.identifierName] = directory
	}
}

func (snapshot *liveSnapshot) release() {
	if snapshot.references.Add(-1) != 0 {
		return
	}
	_ = snapshot.index.Close()
	snapshot.base.release()
}

// latestLiveChanges keeps the last change of every identifier, ordered by the
// position of that last change.
func latestLiveChanges(changes []liveChange) []*liveChange {
	last := make(map[string]int, len(changes))
	for position := range changes {
		last[changes[position].change.ID] = position
	}
	latest := make([]*liveChange, 0, len(last))
	for position := range changes {
		if last[changes[position].change.ID] == position {
			latest = append(latest, &changes[position])
		}
	}
	return latest
}

func pieceCoverage(pieces []*livePiece) uint64 {
	if len(pieces) == 0 {
		return 0
	}
	return pieces[len(pieces)-1].to
}

func buildLivePiece(ctx context.Context, schema Schema, options BuildOptions, changes []liveChange) (*livePiece, error) {
	piece := &livePiece{
		from: changes[0].sequence, to: changes[len(changes)-1].sequence + 1, documents: map[string]uint32{},
	}
	builder, err := NewBuilder(schema, options)
	if err != nil {
		return nil, err
	}
	for _, entry := range latestLiveChanges(changes) {
		if entry.change.Delete {
			continue
		}
		piece.documents[entry.change.ID] = builder.DocumentCount()
		if err := builder.addAnalyzed(ctx, entry.analyzed); err != nil {
			return nil, err
		}
	}
	if builder.DocumentCount() != 0 {
		if piece.segment, err = builder.memorySegment(ctx); err != nil {
			return nil, err
		}
	}
	return piece, nil
}

// buildLiveSnapshot assembles the view of changes, reusing the pieces that lie
// inside them and encoding one new piece for the rest. It returns the pieces
// the view used, which cover every change.
func buildLiveSnapshot(
	ctx context.Context,
	schema Schema,
	options BuildOptions,
	base *liveBase,
	changes []liveChange,
	pieces []*livePiece,
) (*liveSnapshot, []*livePiece, error) {
	var used []*livePiece
	if len(changes) > 0 {
		covered, end := changes[0].sequence, changes[len(changes)-1].sequence+1
		for _, piece := range pieces {
			if piece.from != covered || piece.to > end {
				break
			}
			used = append(used, piece)
			covered = piece.to
		}
		rest := sort.Search(len(changes), func(position int) bool { return changes[position].sequence >= covered })
		if rest < len(changes) && len(used) >= maximumLivePieces {
			used, rest = nil, 0 // merge every run into one piece
		}
		if rest < len(changes) {
			piece, err := buildLivePiece(ctx, schema, options, changes[rest:])
			if err != nil {
				return nil, nil, err
			}
			used = append(used, piece)
		}
	}

	// The latest change of an identifier decides the version a view shows;
	// every earlier version, in the base or in an earlier piece, is tombstoned.
	latest := make(map[string]int, len(changes))
	position := 0
	for pieceIndex, piece := range used {
		for ; position < len(changes) && changes[position].sequence < piece.to; position++ {
			latest[changes[position].change.ID] = pieceIndex
		}
	}
	replaced := make([]map[uint32]struct{}, len(base.index.segments))
	for identifier := range latest {
		locations, err := base.locate(ctx, identifier)
		if err != nil {
			return nil, nil, err
		}
		for _, location := range locations {
			if replaced[location.segment] == nil {
				replaced[location.segment] = make(map[uint32]struct{})
			}
			replaced[location.segment][location.document] = struct{}{}
		}
	}
	superseded := make([]map[uint32]struct{}, len(used))
	for pieceIndex, piece := range used {
		for identifier, document := range piece.documents {
			if latest[identifier] != pieceIndex {
				if superseded[pieceIndex] == nil {
					superseded[pieceIndex] = make(map[uint32]struct{})
				}
				superseded[pieceIndex][document] = struct{}{}
			}
		}
	}
	index, err := assembleLiveIndex(base.index, replaced, used, superseded)
	if err != nil {
		return nil, nil, err
	}
	snapshot := &liveSnapshot{index: index, base: base, included: len(changes)}
	snapshot.references.Store(1)
	base.references.Add(1)
	return snapshot, used, nil
}

// locate finds every live occurrence of identifier in the base, once per
// identifier; later views of the same base reuse the answer.
func (base *liveBase) locate(ctx context.Context, identifier string) ([]documentLocation, error) {
	base.mu.Lock()
	defer base.mu.Unlock()
	if locations, known := base.locations[identifier]; known {
		return locations, nil
	}
	index := base.index
	var locations []documentLocation
	for position, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var document uint32
		var found bool
		if entry := index.entries[position]; entry.identifierName != "" {
			directory := base.directories[position]
			if directory == nil {
				lookup, err := index.openIdentifiers(position)
				if err != nil {
					return nil, fmt.Errorf("%w: open identifier index %q: %w", ErrCorruptIndex, entry.identifierName, err)
				}
				directory, err = loadIdentifierDirectory(ctx, lookup)
				if err != nil {
					_ = lookup.Close()
					return nil, fmt.Errorf("%w: identifier index %q: %w", ErrCorruptIndex, entry.identifierName, err)
				}
				base.directories[position] = directory
				base.owned[entry.identifierName] = directory
			}
			var err error
			document, found, err = directory.Find(ctx, segment, identifier)
			if err != nil {
				return nil, err
			}
		} else {
			for candidate := uint32(0); candidate < segment.header.documentN; candidate++ {
				if candidate&8191 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				value, err := segment.documentIdentifier(candidate)
				if err != nil {
					return nil, err
				}
				if value == identifier {
					document, found = candidate, true
					break
				}
			}
		}
		if found && !index.deletions[position].Contains(document) {
			locations = append(locations, documentLocation{segment: position, document: document})
		}
	}
	base.locations[identifier] = locations
	return locations, nil
}

// assembleLiveIndex builds the view: the base's segments with replaced
// documents tombstoned in memory, then the pieces with their superseded
// documents tombstoned. Counts and field statistics are computed exactly as
// when an index opens its manifest. Every segment is shared, so closing the
// view closes none: the base belongs to Live, pieces to the garbage collector.
func assembleLiveIndex(base *Index, replaced []map[uint32]struct{}, pieces []*livePiece, superseded []map[uint32]struct{}) (*Index, error) {
	segments := len(base.segments)
	for _, piece := range pieces {
		if piece.segment != nil {
			segments++
		}
	}
	if segments > manifestMaximumSegments {
		return nil, ErrTooManySegments
	}
	fields := len(base.schema.fields)
	index := &Index{
		directory: base.directory, manifest: base.manifest, generation: base.generation, schema: base.schema,
		entries:    append(make([]manifestSegment, 0, segments), base.entries...),
		segments:   append(make([]*Segment, 0, segments), base.segments...),
		deletions:  make([]*deletedDocuments, 0, segments),
		bases:      make([]uint64, 0, segments),
		fieldStats: make([]uint64, fields),
	}
	add := func(segment *Segment, deleted *deletedDocuments) {
		for field, total := range segment.fieldStats {
			if deleted != nil {
				total -= deleted.tokenTotals[field]
			}
			index.fieldStats[field] += total
		}
		index.bases = append(index.bases, index.physical)
		index.physical += uint64(segment.header.documentN)
		index.documents += uint64(segment.header.documentN - deleted.Count())
		index.deleted += uint64(deleted.Count())
		index.bytes += int64(segment.header.fileSize)
		index.deletions = append(index.deletions, deleted)
	}
	tombstone := func(segment *Segment, existing *deletedDocuments, documents map[uint32]struct{}) (*deletedDocuments, error) {
		if len(documents) == 0 {
			return existing, nil
		}
		totals := make([]uint64, fields)
		var ids []uint32
		if existing != nil {
			copy(totals, existing.tokenTotals)
			ids = existing.ids
		}
		for field := range totals {
			norms, err := segment.fieldNorms(uint16(field))
			if err != nil {
				return nil, err
			}
			for document := range documents {
				totals[field] += uint64(norms[document])
			}
		}
		return newDeletedDocuments(segment.header.documentN, fields, mergeDeletedDocumentIDs(ids, documents), totals)
	}
	index.live = &liveFrequencies{base: base, replaced: make([][]uint32, len(base.segments))}
	for position, segment := range base.segments {
		if len(replaced[position]) != 0 {
			index.live.replaced[position] = mergeDeletedDocumentIDs(nil, replaced[position])
		}
		deleted, err := tombstone(segment, base.deletions[position], replaced[position])
		if err != nil {
			return nil, err
		}
		add(segment, deleted)
	}
	for pieceIndex, piece := range pieces {
		if piece.segment == nil {
			continue
		}
		deleted, err := tombstone(piece.segment, nil, superseded[pieceIndex])
		if err != nil {
			return nil, err
		}
		index.entries = append(index.entries, manifestSegment{
			documents: piece.segment.header.documentN, bytes: piece.segment.header.fileSize,
		})
		index.segments = append(index.segments, piece.segment)
		add(piece.segment, deleted)
	}
	index.shared = len(index.segments)
	return index, nil
}

// liveFrequencies lets a view answer a multi-field document frequency from its
// base: the base's exact count (cached once per base, so every later view
// shares it), minus the replaced base documents that contain the term, plus
// the count in the view's own in-memory segment.
type liveFrequencies struct {
	base     *Index
	replaced [][]uint32
}

func (index *Index) cachedMultiFrequency(
	ctx context.Context,
	prepared preparedMultiMatchQuery,
	term int,
	key string,
) (uint64, bool, error) {
	if frequency, cached := index.multiFreq.get(key); cached || index.live == nil {
		return frequency, cached, nil
	}
	source := index.live
	base := source.base
	var dictionaryBuffer []byte
	frequency, cached := base.multiFreq.get(key)
	if !cached {
		for position, segment := range base.segments {
			if err := ctx.Err(); err != nil {
				return 0, false, err
			}
			fields, err := segment.multiTermFields(prepared, prepared.terms[term], &dictionaryBuffer)
			if err != nil {
				return 0, false, err
			}
			if len(fields) == 0 {
				continue
			}
			count, err := segment.liveMultiDocumentFrequency(ctx, fields, base.deletions[position])
			if err != nil {
				return 0, false, err
			}
			frequency += uint64(count)
		}
		base.multiFreq.put(key, frequency)
	}
	for position, documents := range source.replaced {
		if len(documents) == 0 {
			continue
		}
		segment := index.segments[position]
		fields, err := segment.multiTermFields(prepared, prepared.terms[term], &dictionaryBuffer)
		if err != nil {
			return 0, false, err
		}
		removed, err := segment.countContaining(ctx, fields, documents)
		if err != nil {
			return 0, false, err
		}
		if uint64(removed) > frequency {
			return 0, false, corruptIndexf("replaced documents exceed the base frequency of %q", prepared.terms[term])
		}
		frequency -= uint64(removed)
	}
	for position := len(base.segments); position < len(index.segments); position++ {
		segment := index.segments[position]
		fields, err := segment.multiTermFields(prepared, prepared.terms[term], &dictionaryBuffer)
		if err != nil {
			return 0, false, err
		}
		if len(fields) == 0 {
			continue
		}
		count, err := segment.liveMultiDocumentFrequency(ctx, fields, index.deletions[position])
		if err != nil {
			return 0, false, err
		}
		frequency += uint64(count)
	}
	index.multiFreq.put(key, frequency)
	return frequency, true, nil
}

// countContaining counts the documents, ascending and distinct, that appear in
// at least one of the term's field postings.
func (segment *Segment) countContaining(
	ctx context.Context,
	fields []multiFieldTermRecord,
	documents []uint32,
) (uint32, error) {
	contained := make([]bool, len(documents))
	for _, field := range fields {
		iterator, err := newPostingIterator(
			ctx, segment.file, segment.header.version, field.record, segment.header.documentN,
			segment.header.sections[sectionPostings], false,
		)
		if err != nil {
			return 0, err
		}
		for position, document := range documents {
			more, err := iterator.Advance(document)
			if err != nil {
				return 0, err
			}
			if !more {
				break
			}
			if current, _, _ := iterator.Current(); current == document {
				contained[position] = true
			}
		}
	}
	count := uint32(0)
	for _, yes := range contained {
		if yes {
			count++
		}
	}
	return count, nil
}
