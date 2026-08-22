package search

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// WriterOptions configure each in-memory segment built by an IndexWriter.
type WriterOptions struct {
	Segment       BuildOptions
	fault         writerFaultInjector
	directorySync func(string) error
}

// IndexWriter builds and compacts bounded immutable segments, publishing every
// change through immutable generation manifests. It is not safe for concurrent
// use, and only one process may write an index directory at a time.
type IndexWriter struct {
	directory      string
	schema         Schema
	segmentOptions BuildOptions
	builder        *Builder
	committed      indexManifest
	snapshot       *Index
	identifier     map[int]*identifierIndex
	pending        []manifestSegment
	pendingDeletes map[int]map[uint32]struct{}
	pendingIDs     map[string]struct{}
	replacement    *Replacement
	lock           *writerLock
	fault          writerFaultInjector
	directorySync  func(string) error
	closed         bool
}

// NewIndexWriter creates or resumes an immutable-generation index directory.
func NewIndexWriter(directory string, schema Schema, options WriterOptions) (_ *IndexWriter, returnErr error) {
	if !schema.valid() {
		return nil, fmt.Errorf("search: invalid schema")
	}
	if directory == "" {
		return nil, fmt.Errorf("search: index directory is empty")
	}
	directory = filepath.Clean(directory)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	stat, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("search: index path is not a directory")
	}
	lock, err := acquireWriterLock(directory)
	if err != nil {
		return nil, err
	}
	keepLock := false
	defer func() {
		if !keepLock {
			returnErr = errors.Join(returnErr, lock.close())
		}
	}()

	builder, err := NewBuilder(schema, options.Segment)
	if err != nil {
		return nil, err
	}
	directorySync := options.directorySync
	if directorySync == nil {
		directorySync = syncDirectory
	}
	writer := &IndexWriter{
		directory: directory, schema: schema, segmentOptions: builder.options,
		builder: builder, committed: indexManifest{schemaHash: schema.fingerprint},
		identifier: make(map[int]*identifierIndex), pendingDeletes: make(map[int]map[uint32]struct{}),
		pendingIDs: make(map[string]struct{}), lock: lock, fault: options.fault,
		directorySync: directorySync,
	}
	manifest, exists, err := readLatestManifest(directory)
	if err != nil {
		return nil, err
	}
	if !exists {
		keepLock = true
		return writer, nil
	}
	index, err := openIndexManifest(directory, schema, manifest)
	if err != nil {
		return nil, err
	}
	writer.snapshot = index
	writer.committed = manifest
	writer.committed.segments = append([]manifestSegment(nil), manifest.segments...)
	keepLock = true
	return writer, nil
}

func (writer *IndexWriter) ensureOpen() error {
	if writer == nil || writer.closed {
		return ErrClosed
	}
	return nil
}

// Add indexes one document. If the current builder is full, Add synchronously
// flushes it to an uncommitted immutable segment before retrying the document.
func (writer *IndexWriter) Add(ctx context.Context, document Document) error {
	if err := writer.ensureOpen(); err != nil {
		return err
	}
	if writer.replacement != nil {
		return ErrReplacementActive
	}
	return writer.add(ctx, document, true)
}

func (writer *IndexWriter) add(ctx context.Context, document Document, trackPending bool) error {
	if ctx == nil {
		return fmt.Errorf("search: add context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if trackPending {
		if _, pending := writer.pendingIDs[document.ID]; pending {
			return ErrPendingDocument
		}
	}
	if writer.pendingSegmentCount() >= manifestMaximumSegments && writer.builder.DocumentCount() == 0 {
		return ErrTooManySegments
	}
	err := writer.builder.AddContext(ctx, document)
	if !errors.Is(err, ErrSegmentFull) {
		if err == nil && trackPending {
			writer.pendingIDs[document.ID] = struct{}{}
		}
		return err
	}
	if writer.builder.DocumentCount() == 0 {
		return ErrSegmentFull
	}
	if err := writer.flush(ctx); err != nil {
		return err
	}
	err = writer.builder.AddContext(ctx, document)
	if err == nil && trackPending {
		writer.pendingIDs[document.ID] = struct{}{}
	}
	return err
}

// ReplaceAll builds a complete replacement snapshot and publishes it with one
// manifest swap. Existing readers keep their old immutable snapshot. Any error
// before publication discards replacement artifacts and leaves the committed
// generation unchanged.
//
// ReplaceAll rejects a writer that already has uncommitted mutations so it can
// never silently discard caller work. The input is consumed in order and may
// be empty, in which case an empty committed generation is published.
func (writer *IndexWriter) ReplaceAll(ctx context.Context, documents []Document) (_ IndexInfo, returnErr error) {
	replacement, err := writer.BeginReplacement()
	if err != nil {
		return IndexInfo{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, replacement.Abort()) }()
	for _, document := range documents {
		if err := replacement.Add(ctx, document); err != nil {
			return IndexInfo{}, err
		}
	}
	return replacement.Commit(ctx)
}

// Reindex rebuilds the current writer contents from a streaming source
// callback without materializing all documents in memory.
func (writer *IndexWriter) Reindex(
	ctx context.Context,
	visit func(add func(Document) error) error,
) (_ IndexInfo, returnErr error) {
	if visit == nil {
		return IndexInfo{}, fmt.Errorf("search: reindex visitor is nil")
	}
	replacement, err := writer.BeginReplacement()
	if err != nil {
		return IndexInfo{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, replacement.Abort()) }()
	add := func(document Document) error {
		return replacement.Add(ctx, document)
	}
	if err := visit(add); err != nil {
		return IndexInfo{}, err
	}
	return replacement.Commit(ctx)
}

func (writer *IndexWriter) hasPendingChanges() bool {
	return writer.builder.DocumentCount() != 0 || len(writer.pending) != 0 ||
		len(writer.pendingDeletes) != 0 || len(writer.pendingIDs) != 0
}

func (writer *IndexWriter) pendingSegmentCount() int {
	count := len(writer.pending)
	if writer.replacement == nil {
		count += len(writer.committed.segments)
	}
	return count
}

// Update replaces every live committed occurrence of document.ID in the next
// snapshot. The replacement and tombstones become visible in one Commit.
func (writer *IndexWriter) Update(ctx context.Context, document Document) error {
	if err := writer.ensureOpen(); err != nil {
		return err
	}
	if writer.replacement != nil {
		return ErrReplacementActive
	}
	if ctx == nil {
		return fmt.Errorf("search: update context is nil")
	}
	if _, pending := writer.pendingIDs[document.ID]; pending {
		return ErrPendingDocument
	}
	locations, err := writer.findCommittedDocuments(ctx, document.ID)
	if err != nil {
		return err
	}
	if len(locations) == 0 {
		return ErrDocumentNotFound
	}
	if err := writer.Add(ctx, document); err != nil {
		return err
	}
	writer.stageDeletions(locations)
	return nil
}

// Delete tombstones every live committed occurrence of identifier in the next
// snapshot. It reports false when the identifier is not currently live.
func (writer *IndexWriter) Delete(ctx context.Context, identifier string) (bool, error) {
	if err := writer.ensureOpen(); err != nil {
		return false, err
	}
	if writer.replacement != nil {
		return false, ErrReplacementActive
	}
	if ctx == nil {
		return false, fmt.Errorf("search: delete context is nil")
	}
	if identifier == "" {
		return false, fmt.Errorf("search: document identifier is empty")
	}
	if _, pending := writer.pendingIDs[identifier]; pending {
		return false, ErrPendingDocument
	}
	locations, err := writer.findCommittedDocuments(ctx, identifier)
	if err != nil {
		return false, err
	}
	if len(locations) == 0 {
		return false, nil
	}
	writer.stageDeletions(locations)
	return true, nil
}

type documentLocation struct {
	segment  int
	document uint32
}

func (writer *IndexWriter) findCommittedDocuments(ctx context.Context, identifier string) ([]documentLocation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if writer.snapshot == nil {
		return nil, nil
	}
	locations := make([]documentLocation, 0, 1)
	for position := len(writer.snapshot.segments) - 1; position >= 0; position-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		segment := writer.snapshot.segments[position]
		var document uint32
		var found bool
		entry := writer.snapshot.entries[position]
		if entry.identifierName != "" {
			identifierIndex, err := writer.identifierIndex(ctx, position)
			if err != nil {
				return nil, err
			}
			document, found, err = identifierIndex.Find(ctx, segment, identifier)
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
		if !found || writer.snapshot.deletions[position].Contains(document) {
			continue
		}
		if pending := writer.pendingDeletes[position]; pending != nil {
			if _, exists := pending[document]; exists {
				continue
			}
		}
		locations = append(locations, documentLocation{segment: position, document: document})
	}
	return locations, nil
}

func (writer *IndexWriter) identifierIndex(ctx context.Context, position int) (*identifierIndex, error) {
	if index := writer.identifier[position]; index != nil {
		return index, nil
	}
	entry := writer.snapshot.entries[position]
	index, err := openIdentifierIndex(
		filepath.Join(writer.directory, entry.identifierName), writer.schema.fingerprint,
		entry.documents, entry.identifierBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: open identifier index %q: %w", ErrCorruptIndex, entry.identifierName, err)
	}
	if err := index.Verify(ctx, writer.snapshot.segments[position]); err != nil {
		_ = index.Close()
		return nil, err
	}
	writer.identifier[position] = index
	return index, nil
}

func (writer *IndexWriter) stageDeletions(locations []documentLocation) {
	for _, location := range locations {
		pending := writer.pendingDeletes[location.segment]
		if pending == nil {
			pending = make(map[uint32]struct{})
			writer.pendingDeletes[location.segment] = pending
		}
		pending[location.document] = struct{}{}
	}
}

// Flush writes the current in-memory builder without publishing a manifest.
// A later Commit makes all pending segments visible together.
func (writer *IndexWriter) Flush(ctx context.Context) error {
	if err := writer.ensureOpen(); err != nil {
		return err
	}
	if writer.replacement != nil {
		return ErrReplacementActive
	}
	if ctx == nil {
		return fmt.Errorf("search: flush context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writer.flush(ctx)
}

func (writer *IndexWriter) flush(ctx context.Context) error {
	if writer.builder.DocumentCount() == 0 {
		return nil
	}
	if writer.pendingSegmentCount() >= manifestMaximumSegments {
		return ErrTooManySegments
	}
	if writer.committed.generation == math.MaxUint64 {
		return fmt.Errorf("search: manifest generation overflow")
	}
	next, err := NewBuilder(writer.schema, writer.segmentOptions)
	if err != nil {
		return err
	}
	name, err := newSegmentFilename(writer.committed.generation + 1)
	if err != nil {
		return err
	}
	identifierName := segmentArtifactFilename(name, ".ki")
	identifierInfo, err := writeIdentifierIndex(
		ctx,
		filepath.Join(writer.directory, identifierName),
		writer.schema.fingerprint,
		writer.builder.identifiers,
	)
	if err != nil {
		return err
	}
	info, err := writer.builder.Write(ctx, filepath.Join(writer.directory, name))
	if err != nil {
		_ = os.Remove(identifierInfo.Path)
		return err
	}
	writer.pending = append(writer.pending, manifestSegment{
		name: name, documents: info.Documents, bytes: uint64(info.Bytes),
		identifierName: identifierName, identifierBytes: uint64(identifierInfo.Bytes),
	})
	writer.builder = next
	return nil
}

// Commit flushes the active builder and atomically publishes a new immutable
// manifest generation. Already-open readers keep their previous snapshot.
func (writer *IndexWriter) Commit(ctx context.Context) (IndexInfo, error) {
	if err := writer.ensureOpen(); err != nil {
		return IndexInfo{}, err
	}
	if writer.replacement != nil {
		return IndexInfo{}, ErrReplacementActive
	}
	return writer.commit(ctx)
}

func (writer *IndexWriter) commit(ctx context.Context) (IndexInfo, error) {
	if ctx == nil {
		return IndexInfo{}, fmt.Errorf("search: commit context is nil")
	}
	if err := ctx.Err(); err != nil {
		return IndexInfo{}, err
	}
	if err := writer.flush(ctx); err != nil {
		return IndexInfo{}, err
	}
	if writer.replacement != nil {
		if err := writer.validatePendingIdentifiers(ctx); err != nil {
			return IndexInfo{}, err
		}
	}
	if writer.replacement == nil && len(writer.pending) == 0 && len(writer.pendingDeletes) == 0 && writer.committed.generation != 0 {
		return manifestIndexInfo(writer.directory, writer.committed)
	}
	if writer.replacement != nil && len(writer.pendingDeletes) != 0 {
		return IndexInfo{}, corruptIndexf("replacement contains pending deletions")
	}
	if writer.committed.generation == math.MaxUint64 {
		return IndexInfo{}, fmt.Errorf("search: manifest generation overflow")
	}
	segmentCapacity := len(writer.pending)
	if writer.replacement == nil {
		segmentCapacity += len(writer.committed.segments)
	}
	segments := make([]manifestSegment, 0, segmentCapacity)
	if writer.replacement == nil {
		segments = append(segments, writer.committed.segments...)
	}
	segments = append(segments, writer.pending...)
	createdDeletions, err := writer.applyPendingDeletions(ctx, segments, writer.committed.generation+1)
	if err != nil {
		return IndexInfo{}, err
	}
	keepDeletions := false
	defer func() {
		if !keepDeletions {
			removeArtifactPaths(createdDeletions)
		}
	}()
	manifest := indexManifest{
		generation: writer.committed.generation + 1,
		schemaHash: writer.schema.fingerprint,
		segments:   segments,
	}
	info, err := manifestIndexInfo(writer.directory, manifest)
	if err != nil {
		return IndexInfo{}, err
	}
	candidate, err := openIndexManifest(writer.directory, writer.schema, manifest)
	if err != nil {
		return IndexInfo{}, err
	}
	writer.injectFault(writerFaultBeforeManifest)
	publication, publishErr := writeManifest(ctx, writer.directory, manifest, writer.directorySync)
	if publishErr != nil && !publication.published {
		_ = candidate.Close()
		return IndexInfo{}, publishErr
	}
	if !publication.published {
		_ = candidate.Close()
		return IndexInfo{}, fmt.Errorf("search: manifest was not published")
	}
	if publishErr == nil {
		writer.injectFault(writerFaultAfterManifest)
	}
	manifest.path = publication.path
	candidate.manifest = publication.path
	previous := writer.snapshot
	writer.closeIdentifierIndexes()
	writer.committed = manifest
	writer.snapshot = candidate
	writer.pending = nil
	writer.pendingDeletes = make(map[int]map[uint32]struct{})
	writer.pendingIDs = make(map[string]struct{})
	writer.finishReplacement()
	keepDeletions = true
	if previous != nil {
		_ = previous.Close()
	}
	info.Manifest = publication.path
	return info, durabilityUncertain(publishErr)
}

func (writer *IndexWriter) abortReplacement() error {
	removeErr := writer.removePendingArtifacts()
	builder, builderErr := NewBuilder(writer.schema, writer.segmentOptions)
	writer.pending = nil
	writer.pendingDeletes = make(map[int]map[uint32]struct{})
	writer.pendingIDs = make(map[string]struct{})
	writer.finishReplacement()
	if builderErr == nil {
		writer.builder = builder
	}
	return errors.Join(removeErr, builderErr)
}

func (writer *IndexWriter) removePendingArtifacts() error {
	var removeErrors []error
	for _, segment := range writer.pending {
		for _, name := range []string{segment.name, segment.identifierName, segment.deletionName} {
			if name == "" {
				continue
			}
			if err := os.Remove(filepath.Join(writer.directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				removeErrors = append(removeErrors, err)
			}
		}
	}
	return errors.Join(removeErrors...)
}

func (writer *IndexWriter) applyPendingDeletions(
	ctx context.Context,
	segments []manifestSegment,
	generation uint64,
) ([]string, error) {
	if len(writer.pendingDeletes) == 0 {
		return nil, nil
	}
	if writer.snapshot == nil {
		return nil, corruptIndexf("pending deletions have no committed snapshot")
	}
	created := make([]string, 0, len(writer.pendingDeletes))
	for position, additions := range writer.pendingDeletes {
		if err := ctx.Err(); err != nil {
			removeArtifactPaths(created)
			return nil, err
		}
		if position < 0 || position >= len(writer.snapshot.segments) || len(additions) == 0 {
			removeArtifactPaths(created)
			return nil, corruptIndexf("pending deletion segment %d is invalid", position)
		}
		segment := writer.snapshot.segments[position]
		existing := writer.snapshot.deletions[position]
		var existingIDs []uint32
		totals := make([]uint64, len(writer.schema.fields))
		if existing != nil {
			existingIDs = existing.ids
			copy(totals, existing.tokenTotals)
		}
		for document := range additions {
			if existing.Contains(document) {
				continue
			}
			for field := uint16(0); field < segment.header.fieldN; field++ {
				norm, err := segment.fieldNorm(field, document)
				if err != nil {
					removeArtifactPaths(created)
					return nil, err
				}
				if totals[field] > math.MaxUint64-uint64(norm) {
					removeArtifactPaths(created)
					return nil, corruptIndexf("deletion token total overflows for segment %d field %d", position, field)
				}
				totals[field] += uint64(norm)
			}
		}
		deleted, err := newDeletedDocuments(
			segment.header.documentN, len(writer.schema.fields),
			mergeDeletedDocumentIDs(existingIDs, additions), totals,
		)
		if err != nil {
			removeArtifactPaths(created)
			return nil, err
		}
		name, err := newArtifactFilename("delete", generation, ".kd")
		if err != nil {
			removeArtifactPaths(created)
			return nil, err
		}
		info, err := writeDeletionFile(ctx, filepath.Join(writer.directory, name), writer.schema.fingerprint, deleted)
		if err != nil {
			removeArtifactPaths(created)
			return nil, err
		}
		created = append(created, info.Path)
		segments[position].deletionName = name
		segments[position].deletionBytes = uint64(info.Bytes)
		segments[position].deleted = info.Deleted
	}
	return created, nil
}

func removeArtifactPaths(paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

func (writer *IndexWriter) closeIdentifierIndexes() {
	for position, index := range writer.identifier {
		_ = index.Close()
		delete(writer.identifier, position)
	}
}

// Close discards in-memory documents and removes segments that were flushed
// but never committed. Committed generations are never modified.
func (writer *IndexWriter) Close() error {
	if writer == nil || writer.closed {
		return nil
	}
	writer.closed = true
	var removeErrors []error
	writer.closeIdentifierIndexes()
	if writer.snapshot != nil {
		if err := writer.snapshot.Close(); err != nil {
			removeErrors = append(removeErrors, err)
		}
		writer.snapshot = nil
	}
	if err := writer.removePendingArtifacts(); err != nil {
		removeErrors = append(removeErrors, err)
	}
	writer.pending = nil
	writer.pendingDeletes = nil
	writer.pendingIDs = nil
	writer.finishReplacement()
	writer.builder = nil
	if writer.lock != nil {
		removeErrors = append(removeErrors, writer.lock.close())
		writer.lock = nil
	}
	return errors.Join(removeErrors...)
}

func manifestIndexInfo(directory string, manifest indexManifest) (IndexInfo, error) {
	info := IndexInfo{
		Path: directory, Manifest: manifest.path, Generation: manifest.generation,
		Segments: len(manifest.segments),
	}
	for position, segment := range manifest.segments {
		if info.PhysicalDocuments > math.MaxUint64-uint64(segment.documents) ||
			info.Documents > math.MaxUint64-uint64(segment.documents-segment.deleted) ||
			info.Deleted > math.MaxUint64-uint64(segment.deleted) {
			return IndexInfo{}, corruptIndexf("document count overflows at segment %d", position)
		}
		artifactBytes := segment.bytes
		if artifactBytes > math.MaxUint64-segment.identifierBytes {
			return IndexInfo{}, corruptIndexf("byte count overflows at segment %d", position)
		}
		artifactBytes += segment.identifierBytes
		if artifactBytes > math.MaxUint64-segment.deletionBytes {
			return IndexInfo{}, corruptIndexf("byte count overflows at segment %d", position)
		}
		artifactBytes += segment.deletionBytes
		if artifactBytes > math.MaxInt64 || int64(artifactBytes) > math.MaxInt64-info.Bytes {
			return IndexInfo{}, corruptIndexf("byte count overflows at segment %d", position)
		}
		info.PhysicalDocuments += uint64(segment.documents)
		info.Documents += uint64(segment.documents - segment.deleted)
		info.Deleted += uint64(segment.deleted)
		info.Bytes += int64(artifactBytes)
	}
	return info, nil
}

func newSegmentFilename(generation uint64) (string, error) {
	return newArtifactFilename("segment", generation, ".ks")
}

func newArtifactFilename(prefix string, generation uint64, suffix string) (string, error) {
	if generation == 0 {
		return "", fmt.Errorf("search: artifact generation must be positive")
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%020d-%s%s", prefix, generation, hex.EncodeToString(random[:]), suffix), nil
}

func segmentArtifactFilename(segmentName, suffix string) string {
	return segmentName[:len(segmentName)-len(".ks")] + suffix
}
