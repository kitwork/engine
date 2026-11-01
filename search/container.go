package search

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/internal/snapshotfile"
)

const (
	containerFormat      = "kitwork-search/1"
	containerReadHandles = 8
	// containerMaximumSegments bounds one index's segments; a fold that
	// would pass it first merges a range of them.
	containerMaximumSegments = 48
)

// Container keeps the full-text indexes of one database in a single file, a
// KSNAP002 container (internal/snapshotfile). A generation of the file holds,
// for every named index, its segments, identifier sidecars and tombstones as
// sections. Replace and Fold append a generation: new sections for what
// changed, references to the bytes of everything else, then a root switch,
// so a crash leaves the previous generation. Compaction writes a fresh file
// beside it and moves it over the path once every index of the old one has
// closed. One process owns a container; Fold, Replace and compaction are
// serialized.
type Container struct {
	path string

	mu          sync.Mutex
	current     *containerGeneration
	directories map[string]*identifierDirectory
	closed      bool
}

type containerSegment struct {
	Name            string `json:"name"`
	Documents       uint32 `json:"documents"`
	Bytes           uint64 `json:"bytes"`
	IdentifierBytes uint64 `json:"identifierBytes"`
	Deleted         uint32 `json:"deleted,omitempty"`
	DeletionBytes   uint64 `json:"deletionBytes,omitempty"`
}

type containerIndex struct {
	Schema   string             `json:"schema"`
	State    json.RawMessage    `json:"state,omitempty"`
	Segments []containerSegment `json:"segments"`
}

type containerMetadata struct {
	Format  string                    `json:"format"`
	Next    uint64                    `json:"next"`
	Indexes map[string]containerIndex `json:"indexes"`
}

func (metadata containerMetadata) clone() containerMetadata {
	copied := metadata
	copied.Indexes = maps.Clone(metadata.Indexes)
	if copied.Indexes == nil {
		copied.Indexes = map[string]containerIndex{}
	}
	return copied
}

// containerGeneration is one opened generation of the file. The container
// holds one reference while it is current; every Index opened from it holds
// one more. The last release closes its read handles.
type containerGeneration struct {
	reader     *snapshotfile.Reader
	metadata   containerMetadata
	references atomic.Int64
	drained    chan struct{}
}

func openContainerGeneration(path string) (*containerGeneration, error) {
	reader, err := snapshotfile.OpenRenamable(path, containerReadHandles)
	if err != nil {
		return nil, err
	}
	var metadata containerMetadata
	if err := reader.Metadata(&metadata); err != nil || metadata.Format != containerFormat {
		_ = reader.Close()
		return nil, fmt.Errorf("%w: %s is not a %s container", ErrCorruptIndex, path, containerFormat)
	}
	if metadata.Indexes == nil {
		metadata.Indexes = map[string]containerIndex{}
	}
	generation := &containerGeneration{reader: reader, metadata: metadata, drained: make(chan struct{})}
	generation.references.Store(1)
	return generation, nil
}

func (generation *containerGeneration) acquire() { generation.references.Add(1) }

func (generation *containerGeneration) release() {
	if generation.references.Add(-1) == 0 {
		_ = generation.reader.Close()
		close(generation.drained)
	}
}

func (generation *containerGeneration) section(name string) (*io.SectionReader, error) {
	section, err := generation.reader.Section(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorruptIndex, err)
	}
	return section, nil
}

// OpenContainer opens the container at path, creating an empty one. A
// compaction that never moved over the path is discarded.
func OpenContainer(ctx context.Context, path string) (*Container, error) {
	if ctx == nil {
		return nil, fmt.Errorf("search: container context is nil")
	}
	path = filepath.Clean(path)
	_ = os.Remove(path + ".compact")
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		writer, err := snapshotfile.CreateGeneration(path)
		if err != nil {
			return nil, err
		}
		err = writer.Publish(ctx, containerMetadata{Format: containerFormat, Next: 1, Indexes: map[string]containerIndex{}})
		_ = writer.Close()
		if err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("search: container path %s is not a regular file", path)
	}
	generation, err := openContainerGeneration(path)
	if err != nil {
		return nil, err
	}
	return &Container{path: path, current: generation, directories: map[string]*identifierDirectory{}}, nil
}

// Path is the container file.
func (container *Container) Path() string { return container.path }

// Info reports the current generation and how much of the file it no longer uses.
func (container *Container) Info() snapshotfile.GenerationInfo {
	container.mu.Lock()
	defer container.mu.Unlock()
	return container.current.reader.GenerationInfo()
}

// NeedsCompaction reports that obsolete bytes exceed the live ones.
func (container *Container) NeedsCompaction() bool {
	container.mu.Lock()
	defer container.mu.Unlock()
	return container.current.reader.NeedsCompaction()
}

// Index opens the named index of the current generation, with the state its
// owner stored with it. The Index keeps its generation readable until closed.
func (container *Container) Index(name string, schema Schema) (*Index, json.RawMessage, error) {
	container.mu.Lock()
	if container.closed {
		container.mu.Unlock()
		return nil, nil, ErrClosed
	}
	generation := container.current
	generation.acquire()
	container.mu.Unlock()
	return openGenerationIndex(container.path, generation, name, schema)
}

func openGenerationIndex(path string, generation *containerGeneration, name string, schema Schema) (*Index, json.RawMessage, error) {
	entry, exists := generation.metadata.Indexes[name]
	if !exists {
		generation.release()
		return nil, nil, ErrIndexNotFound
	}
	if entry.Schema != hex.EncodeToString(schema.fingerprint[:]) {
		generation.release()
		return nil, nil, ErrSchemaMismatch
	}
	index, err := buildGenerationIndex(path, generation, entry, schema)
	if err != nil {
		generation.release()
		return nil, nil, err
	}
	return index, entry.State, nil
}

// buildGenerationIndex assembles an Index over the sections of one generation,
// counting documents and field statistics as openIndexManifest does.
func buildGenerationIndex(path string, generation *containerGeneration, entry containerIndex, schema Schema) (*Index, error) {
	info := generation.reader.GenerationInfo()
	index := &Index{
		directory: path, generation: info.Number, schema: schema,
		entries:    make([]manifestSegment, 0, len(entry.Segments)),
		segments:   make([]*Segment, 0, len(entry.Segments)),
		deletions:  make([]*deletedDocuments, 0, len(entry.Segments)),
		bases:      make([]uint64, 0, len(entry.Segments)),
		fieldStats: make([]uint64, len(schema.fields)),
	}
	for _, segmentEntry := range entry.Segments {
		section, err := generation.section(segmentEntry.Name + ".ks")
		if err != nil {
			return nil, err
		}
		segment, err := openSegmentReader(segmentEntry.Name, section, section.Size(), schema)
		if err != nil {
			return nil, fmt.Errorf("%w: segment %q: %w", ErrCorruptIndex, segmentEntry.Name, err)
		}
		if segment.header.documentN != segmentEntry.Documents || uint64(section.Size()) != segmentEntry.Bytes {
			return nil, corruptIndexf("segment %q does not match the container directory", segmentEntry.Name)
		}
		var deleted *deletedDocuments
		if segmentEntry.Deleted != 0 {
			deletionSection, err := generation.section(segmentEntry.Name + ".kd")
			if err != nil {
				return nil, err
			}
			if deletionSection.Size() > int64(maxIntValue()) || uint64(deletionSection.Size()) != segmentEntry.DeletionBytes {
				return nil, corruptIndexf("tombstones of %q do not match the container directory", segmentEntry.Name)
			}
			data := make([]byte, int(deletionSection.Size()))
			if err := readAtFull(deletionSection, data, 0); err != nil {
				return nil, err
			}
			deleted, err = decodeDeletion(data, schema.fingerprint, segmentEntry.Documents, segmentEntry.Deleted, uint16(len(schema.fields)))
			if err != nil {
				return nil, err
			}
		}
		for field, total := range segment.fieldStats {
			if deleted != nil {
				if deleted.tokenTotals[field] > total {
					return nil, corruptIndexf("deleted field statistics exceed segment %q", segmentEntry.Name)
				}
				total -= deleted.tokenTotals[field]
			}
			if index.fieldStats[field] > math.MaxUint64-total {
				return nil, corruptIndexf("field statistics overflow")
			}
			index.fieldStats[field] += total
		}
		index.bases = append(index.bases, index.physical)
		index.physical += uint64(segmentEntry.Documents)
		index.documents += uint64(segmentEntry.Documents - deleted.Count())
		index.deleted += uint64(deleted.Count())
		index.bytes += section.Size() + int64(segmentEntry.IdentifierBytes+segmentEntry.DeletionBytes)
		index.entries = append(index.entries, manifestSegment{
			name: segmentEntry.Name, documents: segmentEntry.Documents, bytes: segmentEntry.Bytes,
			identifierName: segmentEntry.Name + ".ki", identifierBytes: segmentEntry.IdentifierBytes,
			deleted: segmentEntry.Deleted, deletionBytes: segmentEntry.DeletionBytes,
		})
		index.segments = append(index.segments, segment)
		index.deletions = append(index.deletions, deleted)
	}
	index.identifiers = func(position int) (*identifierIndex, error) {
		entry := index.entries[position]
		section, err := generation.section(entry.identifierName)
		if err != nil {
			return nil, err
		}
		return readIdentifierIndex(entry.identifierName, section, section.Size(), schema.fingerprint, entry.documents, entry.identifierBytes)
	}
	index.onClose = generation.release
	return index, nil
}

// containerBuild is one segment encoded in memory, with its identifier sidecar.
type containerBuild struct {
	segment     []byte
	identifiers []byte
	documents   uint32
}

func buildContainerSegment(ctx context.Context, builder *Builder) (containerBuild, error) {
	identifiers, err := encodeIdentifierIndex(builder.schema.fingerprint, builder.identifiers)
	if err != nil {
		return containerBuild{}, err
	}
	documents := builder.DocumentCount()
	segment, err := builder.encodeBytes(ctx)
	if err != nil {
		return containerBuild{}, err
	}
	return containerBuild{segment: segment, identifiers: identifiers, documents: documents}, nil
}

// containerPublication is one generation being appended.
type containerPublication struct {
	container *Container
	base      *containerGeneration
	writer    *snapshotfile.GenerationWriter
	metadata  containerMetadata
}

func (container *Container) beginLocked() (*containerPublication, error) {
	if container.closed {
		return nil, ErrClosed
	}
	writer, err := snapshotfile.AppendGeneration(container.path, container.current.reader)
	if err != nil {
		return nil, err
	}
	return &containerPublication{
		container: container, base: container.current, writer: writer, metadata: container.current.metadata.clone(),
	}, nil
}

func (publication *containerPublication) reference(name string) error {
	section, err := publication.base.section(name)
	if err != nil {
		return err
	}
	return publication.writer.Add(name, func(output io.Writer) error {
		return snapshotfile.Reference(output, publication.base.reader, name, 0, section.Size())
	})
}

func (publication *containerPublication) add(name string, data []byte) error {
	return publication.writer.Add(name, func(output io.Writer) error {
		_, err := output.Write(data)
		return err
	})
}

func (publication *containerPublication) addBuild(build containerBuild) (containerSegment, error) {
	name := strconv.FormatUint(publication.metadata.Next, 10)
	publication.metadata.Next++
	return publication.addNamedBuild(name, build)
}

func (publication *containerPublication) addNamedBuild(name string, build containerBuild) (containerSegment, error) {
	if err := publication.add(name+".ks", build.segment); err != nil {
		return containerSegment{}, err
	}
	if err := publication.add(name+".ki", build.identifiers); err != nil {
		return containerSegment{}, err
	}
	return containerSegment{
		Name: name, Documents: build.documents, Bytes: uint64(len(build.segment)), IdentifierBytes: uint64(len(build.identifiers)),
	}, nil
}

// referenceOthers carries every index but name into the new generation unchanged.
func (publication *containerPublication) referenceOthers(name string) error {
	for other, entry := range publication.base.metadata.Indexes {
		if other == name {
			continue
		}
		for _, segment := range entry.Segments {
			if err := publication.referenceSegment(segment); err != nil {
				return err
			}
		}
	}
	return nil
}

func (publication *containerPublication) referenceSegment(segment containerSegment) error {
	if err := publication.reference(segment.Name + ".ks"); err != nil {
		return err
	}
	if err := publication.reference(segment.Name + ".ki"); err != nil {
		return err
	}
	if segment.Deleted != 0 {
		return publication.reference(segment.Name + ".kd")
	}
	return nil
}

// publishLocked writes the directory and switches the root, then makes the
// new generation current. The previous one closes when its indexes have.
func (publication *containerPublication) publishLocked(ctx context.Context) error {
	err := publication.writer.Publish(ctx, publication.metadata)
	_ = publication.writer.Close()
	if err != nil {
		return err
	}
	generation, err := openContainerGeneration(publication.container.path)
	if err != nil {
		return err
	}
	container := publication.container
	previous := container.current
	container.current = generation
	previous.release()
	live := map[string]bool{}
	for _, entry := range generation.metadata.Indexes {
		for _, segment := range entry.Segments {
			live[segment.Name] = true
		}
	}
	for name := range container.directories {
		if !live[name] {
			delete(container.directories, name)
		}
	}
	return nil
}

func (publication *containerPublication) abort() { _ = publication.writer.Close() }

// Replace builds the named index from scratch out of what feed adds, and
// stores state with it. Other indexes of the container are untouched.
// Identifiers must be unique across the whole feed.
func (container *Container) Replace(
	ctx context.Context,
	name string,
	schema Schema,
	state any,
	options BuildOptions,
	feed func(add func(Document) error) error,
) error {
	if ctx == nil || name == "" || feed == nil {
		return fmt.Errorf("search: replace needs a context, a name and a feed")
	}
	encodedState, err := json.Marshal(state)
	if err != nil {
		return err
	}
	normalized, err := normalizeBuildOptions(options)
	if err != nil {
		return err
	}
	container.mu.Lock()
	defer container.mu.Unlock()
	publication, err := container.beginLocked()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			publication.abort()
		}
	}()
	if err := publication.referenceOthers(name); err != nil {
		return err
	}
	builder, err := NewBuilder(schema, normalized)
	if err != nil {
		return err
	}
	var segments []containerSegment
	flush := func() error {
		if builder.DocumentCount() == 0 {
			return nil
		}
		build, err := buildContainerSegment(ctx, builder)
		if err != nil {
			return err
		}
		segment, err := publication.addBuild(build)
		if err != nil {
			return err
		}
		segments = append(segments, segment)
		builder, err = NewBuilder(schema, normalized)
		return err
	}
	err = feed(func(document Document) error {
		err := builder.AddContext(ctx, document)
		if errors.Is(err, ErrSegmentFull) && builder.DocumentCount() != 0 {
			if err := flush(); err != nil {
				return err
			}
			err = builder.AddContext(ctx, document)
		}
		return err
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if len(segments) > manifestMaximumSegments {
		return fmt.Errorf("%w: %d segments; raise BuildOptions.FlushThresholdBytes", ErrTooManySegments, len(segments))
	}
	publication.metadata.Indexes[name] = containerIndex{
		Schema: hex.EncodeToString(schema.fingerprint[:]), State: encodedState, Segments: segments,
	}
	committed = true
	return publication.publishLocked(ctx)
}

// directoryLocked returns the identifier directory of a segment of index,
// loading its sidecar once per segment and binding the cached directory to
// index's generation.
func (container *Container) directoryLocked(ctx context.Context, index *Index, position int) (*identifierDirectory, error) {
	lookup, err := index.identifiers(position)
	if err != nil {
		return nil, err
	}
	name := index.entries[position].name
	if cached := container.directories[name]; cached != nil {
		return cached.rebind(lookup), nil
	}
	directory, err := loadIdentifierDirectory(ctx, lookup)
	if err != nil {
		return nil, err
	}
	container.directories[name] = directory
	return directory, nil
}

// Fold applies changes, the latest change of each identifier, to the named
// index: every live occurrence of a changed identifier is tombstoned and every
// document that is not a delete is added in new segments. Unchanged sections
// are referenced, not copied. When the index would pass
// containerMaximumSegments, a range of its segments is merged first.
func (container *Container) Fold(
	ctx context.Context,
	name string,
	schema Schema,
	state any,
	options BuildOptions,
	changes []Change,
) error {
	if ctx == nil {
		return fmt.Errorf("search: fold context is nil")
	}
	encodedState, err := json.Marshal(state)
	if err != nil {
		return err
	}
	normalized, err := normalizeBuildOptions(options)
	if err != nil {
		return err
	}
	container.mu.Lock()
	defer container.mu.Unlock()
	if container.closed {
		return ErrClosed
	}
	generation := container.current
	generation.acquire()
	index, _, err := openGenerationIndex(container.path, generation, name, schema)
	if err != nil {
		return err
	}
	defer index.Close()

	staged := make([]map[uint32]struct{}, len(index.segments))
	seen := make(map[string]bool, len(changes))
	for _, change := range changes {
		if seen[change.ID] {
			return fmt.Errorf("%w: %q changes twice in one fold", ErrPendingDocument, change.ID)
		}
		seen[change.ID] = true
		for position, segment := range index.segments {
			directory, err := container.directoryLocked(ctx, index, position)
			if err != nil {
				return err
			}
			document, found, err := directory.Find(ctx, segment, change.ID)
			if err != nil {
				return err
			}
			if found && !index.deletions[position].Contains(document) {
				if staged[position] == nil {
					staged[position] = map[uint32]struct{}{}
				}
				staged[position][document] = struct{}{}
			}
		}
	}
	var builds []containerBuild
	builder, err := NewBuilder(schema, normalized)
	if err != nil {
		return err
	}
	flush := func() error {
		if builder.DocumentCount() == 0 {
			return nil
		}
		build, err := buildContainerSegment(ctx, builder)
		if err != nil {
			return err
		}
		builds = append(builds, build)
		builder, err = NewBuilder(schema, normalized)
		return err
	}
	for _, change := range changes {
		if change.Delete {
			continue
		}
		document := Document{ID: change.ID, Fields: change.Fields}
		err := builder.AddContext(ctx, document)
		if errors.Is(err, ErrSegmentFull) && builder.DocumentCount() != 0 {
			if err := flush(); err != nil {
				return err
			}
			err = builder.AddContext(ctx, document)
		}
		if err != nil {
			return err
		}
	}
	if err := flush(); err != nil {
		return err
	}

	// Tombstones after this fold, per existing segment.
	entry := generation.metadata.Indexes[name]
	deletions := make([]*deletedDocuments, len(index.segments))
	for position, segment := range index.segments {
		deletions[position] = index.deletions[position]
		if len(staged[position]) == 0 {
			continue
		}
		merged, err := tombstoneDocuments(segment, index.deletions[position], staged[position])
		if err != nil {
			return err
		}
		deletions[position] = merged
	}
	kept := make([]bool, len(index.segments))
	survivors := 0
	for position := range index.segments {
		kept[position] = deletions[position].Count() < index.segments[position].header.documentN
		if kept[position] {
			survivors++
		}
	}
	merge := []int(nil)
	if survivors+len(builds) > containerMaximumSegments {
		merge = selectContainerMerge(entry.Segments, kept, deletions, survivors+len(builds)-containerMaximumSegments+1)
	}

	publication, err := container.beginLocked()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			publication.abort()
		}
	}()
	if err := publication.referenceOthers(name); err != nil {
		return err
	}
	merging := map[int]bool{}
	for _, position := range merge {
		merging[position] = true
	}
	var segments []containerSegment
	for position, segmentEntry := range entry.Segments {
		if !kept[position] || merging[position] {
			continue
		}
		if err := publication.reference(segmentEntry.Name + ".ks"); err != nil {
			return err
		}
		if err := publication.reference(segmentEntry.Name + ".ki"); err != nil {
			return err
		}
		segmentEntry.Deleted, segmentEntry.DeletionBytes = 0, 0
		if deleted := deletions[position]; deleted.Count() != 0 {
			if len(staged[position]) == 0 {
				if err := publication.reference(entry.Segments[position].Name + ".kd"); err != nil {
					return err
				}
				segmentEntry.DeletionBytes = entry.Segments[position].DeletionBytes
			} else {
				encoded, err := encodeDeletion(schema.fingerprint, deleted)
				if err != nil {
					return err
				}
				if err := publication.add(segmentEntry.Name+".kd", encoded); err != nil {
					return err
				}
				segmentEntry.DeletionBytes = uint64(len(encoded))
			}
			segmentEntry.Deleted = deleted.Count()
		}
		segments = append(segments, segmentEntry)
	}
	if len(merge) > 0 {
		build, err := container.mergeSegments(ctx, schema, index, deletions, merge)
		if err != nil {
			return err
		}
		segment, err := publication.addBuild(build)
		if err != nil {
			return err
		}
		segments = append(segments, segment)
	}
	for _, build := range builds {
		segment, err := publication.addBuild(build)
		if err != nil {
			return err
		}
		segments = append(segments, segment)
	}
	if len(segments) > manifestMaximumSegments {
		return ErrTooManySegments
	}
	publication.metadata.Indexes[name] = containerIndex{Schema: entry.Schema, State: encodedState, Segments: segments}
	committed = true
	return publication.publishLocked(ctx)
}

func tombstoneDocuments(segment *Segment, existing *deletedDocuments, documents map[uint32]struct{}) (*deletedDocuments, error) {
	fields := int(segment.header.fieldN)
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

// selectContainerMerge picks the count smallest surviving segments, by live
// documents, to merge into one.
func selectContainerMerge(segments []containerSegment, kept []bool, deletions []*deletedDocuments, count int) []int {
	var candidates []int
	for position := range segments {
		if kept[position] {
			candidates = append(candidates, position)
		}
	}
	live := func(position int) uint32 { return segments[position].Documents - deletions[position].Count() }
	slices.SortFunc(candidates, func(left, right int) int {
		if live(left) != live(right) {
			return int(live(left)) - int(live(right))
		}
		return left - right
	})
	count = max(count, 2)
	if count > len(candidates) {
		count = len(candidates)
	}
	chosen := candidates[:count]
	slices.Sort(chosen)
	return chosen
}

// mergeSegments writes the live documents of the chosen segments as one
// segment through a temporary file beside the container, then reads it back
// into memory with its identifier sidecar.
func (container *Container) mergeSegments(
	ctx context.Context,
	schema Schema,
	index *Index,
	deletions []*deletedDocuments,
	positions []int,
) (containerBuild, error) {
	sources := make([]*Segment, len(positions))
	sourceDeletions := make([]*deletedDocuments, len(positions))
	for slot, position := range positions {
		sources[slot] = index.segments[position]
		sourceDeletions[slot] = deletions[position]
	}
	temporary, err := os.CreateTemp(filepath.Dir(container.path), ".search-merge-*")
	if err != nil {
		return containerBuild{}, err
	}
	temporaryPath := temporary.Name()
	_ = temporary.Close()
	_ = os.Remove(temporaryPath)
	defer os.Remove(temporaryPath)
	if _, err := writeMergedSegment(ctx, temporaryPath, schema, sources, sourceDeletions); err != nil {
		return containerBuild{}, err
	}
	data, err := os.ReadFile(temporaryPath)
	if err != nil {
		return containerBuild{}, err
	}
	merged, err := openSegmentReader("<merged>", bytesReaderAt(data), int64(len(data)), schema)
	if err != nil {
		return containerBuild{}, err
	}
	identifiers := make([]string, merged.header.documentN)
	for document := range identifiers {
		if identifiers[document], err = merged.documentIdentifier(uint32(document)); err != nil {
			return containerBuild{}, err
		}
	}
	encoded, err := encodeIdentifierIndex(schema.fingerprint, identifiers)
	if err != nil {
		return containerBuild{}, err
	}
	return containerBuild{segment: data, identifiers: encoded, documents: merged.header.documentN}, nil
}

// Compaction is a fresh copy of the container's live sections, written beside
// it. Between PrepareCompaction and Commit or Abort no fold can publish.
type Compaction struct {
	container  *Container
	generation *containerGeneration
	path       string
	finished   bool
}

// PrepareCompaction copies every live section of the current generation into
// path + ".compact". Open the new indexes with Index, hand them to their
// owners, close every index of the old file, then Commit.
func (container *Container) PrepareCompaction(ctx context.Context) (*Compaction, error) {
	container.mu.Lock()
	if container.closed {
		container.mu.Unlock()
		return nil, ErrClosed
	}
	target := container.path + ".compact"
	_ = os.Remove(target)
	compaction, err := container.writeCompactedLocked(ctx, target)
	if err != nil {
		_ = os.Remove(target)
		container.mu.Unlock()
		return nil, err
	}
	return compaction, nil
}

func (container *Container) writeCompactedLocked(ctx context.Context, target string) (*Compaction, error) {
	writer, err := snapshotfile.CreateGeneration(target)
	if err != nil {
		return nil, err
	}
	defer writer.Close()
	current := container.current
	for _, entry := range current.metadata.Indexes {
		for _, segment := range entry.Segments {
			names := []string{segment.Name + ".ks", segment.Name + ".ki"}
			if segment.Deleted != 0 {
				names = append(names, segment.Name+".kd")
			}
			for _, name := range names {
				section, err := current.section(name)
				if err != nil {
					return nil, err
				}
				if err := writer.Add(name, func(output io.Writer) error {
					return snapshotfile.Copy(ctx, output, io.NewSectionReader(section, 0, section.Size()))
				}); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := writer.Publish(ctx, current.metadata); err != nil {
		return nil, err
	}
	generation, err := openContainerGeneration(target)
	if err != nil {
		return nil, err
	}
	return &Compaction{container: container, generation: generation, path: target}, nil
}

// Index opens the named index of the compacted file.
func (compaction *Compaction) Index(name string, schema Schema) (*Index, json.RawMessage, error) {
	compaction.generation.acquire()
	return openGenerationIndex(compaction.container.path, compaction.generation, name, schema)
}

// Commit waits until every index of the old file has closed, then moves the
// compacted file over the path; its open handles keep reading it there.
func (compaction *Compaction) Commit(ctx context.Context) error {
	if compaction.finished {
		return fmt.Errorf("search: compaction already finished")
	}
	container := compaction.container
	previous := container.current
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	compaction.finished = true
	defer container.mu.Unlock()
	previous.release()
	<-previous.drained
	if err := os.Rename(compaction.path, container.path); err != nil {
		// The old file is still at the path, unchanged: serve it again. Indexes
		// already opened from the compacted file keep reading it until closed.
		restored, openErr := openContainerGeneration(container.path)
		compaction.generation.release()
		_ = os.Remove(compaction.path)
		if openErr != nil {
			container.closed = true
			return errors.Join(err, openErr)
		}
		container.current = restored
		return err
	}
	container.current = compaction.generation
	clear(container.directories)
	return nil
}

// Abort discards the compacted file.
func (compaction *Compaction) Abort() {
	if compaction.finished {
		return
	}
	compaction.finished = true
	compaction.generation.release()
	<-compaction.generation.drained
	_ = os.Remove(compaction.path)
	compaction.container.mu.Unlock()
}

// Close releases the container. Indexes opened from it keep working until closed.
func (container *Container) Close() error {
	container.mu.Lock()
	defer container.mu.Unlock()
	if container.closed {
		return nil
	}
	container.closed = true
	container.current.release()
	return nil
}

type bytesReaderAt []byte

func (data bytesReaderAt) ReadAt(destination []byte, offset int64) (int, error) {
	if offset < 0 || offset > int64(len(data)) {
		return 0, io.EOF
	}
	count := copy(destination, data[offset:])
	if count < len(destination) {
		return count, io.EOF
	}
	return count, nil
}
