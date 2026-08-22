package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync/atomic"
)

// IndexInfo describes one committed immutable index snapshot.
type IndexInfo struct {
	Path              string
	Manifest          string
	Generation        uint64
	Segments          int
	Documents         uint64
	PhysicalDocuments uint64
	Deleted           uint64
	Bytes             int64
}

// Index is a concurrent read-only snapshot over committed immutable segments.
// A newer commit does not change an already-open Index. Close must run only
// after in-flight searches have drained.
type Index struct {
	directory  string
	manifest   string
	generation uint64
	schema     Schema
	entries    []manifestSegment
	segments   []*Segment
	deletions  []*deletedDocuments
	bases      []uint64
	documents  uint64
	physical   uint64
	deleted    uint64
	bytes      int64
	fieldStats []uint64
	multiFreq  multiFrequencyCache
	closed     atomic.Bool
}

// OpenIndex opens the newest committed manifest in directory.
func OpenIndex(directory string, schema Schema) (*Index, error) {
	if !schema.valid() {
		return nil, fmt.Errorf("search: invalid schema")
	}
	if directory == "" {
		return nil, fmt.Errorf("search: index directory is empty")
	}
	directory = filepath.Clean(directory)
	manifest, exists, err := readLatestManifest(directory)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrIndexNotFound
	}
	return openIndexManifest(directory, schema, manifest)
}

func openIndexManifest(directory string, schema Schema, manifest indexManifest) (*Index, error) {
	if manifest.schemaHash != schema.fingerprint {
		return nil, ErrSchemaMismatch
	}
	index := &Index{
		directory: directory, manifest: manifest.path, generation: manifest.generation,
		schema: schema, entries: append([]manifestSegment(nil), manifest.segments...),
		segments:  make([]*Segment, 0, len(manifest.segments)),
		deletions: make([]*deletedDocuments, 0, len(manifest.segments)),
		bases:     make([]uint64, 0, len(manifest.segments)), fieldStats: make([]uint64, len(schema.fields)),
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = index.Close()
		}
	}()

	for position, entry := range manifest.segments {
		segmentPath := filepath.Join(directory, entry.name)
		segment, err := OpenSegment(segmentPath, schema)
		if err != nil {
			return nil, fmt.Errorf("%w: open manifest segment %q: %w", ErrCorruptIndex, entry.name, err)
		}
		info := segment.Info()
		if info.Documents != entry.documents || info.Bytes < 0 || uint64(info.Bytes) != entry.bytes {
			_ = segment.Close()
			return nil, corruptIndexf("segment %q metadata does not match manifest", entry.name)
		}
		if entry.identifierName != "" {
			identifier, err := openIdentifierIndex(
				filepath.Join(directory, entry.identifierName), schema.fingerprint, entry.documents, entry.identifierBytes,
			)
			if err != nil {
				_ = segment.Close()
				return nil, fmt.Errorf("%w: open identifier index %q: %w", ErrCorruptIndex, entry.identifierName, err)
			}
			if err := identifier.Close(); err != nil {
				_ = segment.Close()
				return nil, err
			}
		}
		var deleted *deletedDocuments
		if entry.deleted != 0 {
			deleted, err = openDeletionFile(
				filepath.Join(directory, entry.deletionName), schema.fingerprint, entry.documents,
				entry.deleted, uint16(len(schema.fields)), entry.deletionBytes,
			)
			if err != nil {
				_ = segment.Close()
				return nil, fmt.Errorf("%w: open deletion file %q: %w", ErrCorruptIndex, entry.deletionName, err)
			}
		}
		if index.physical > math.MaxUint64-uint64(info.Documents) ||
			index.documents > math.MaxUint64-uint64(info.Documents-entry.deleted) ||
			index.deleted > math.MaxUint64-uint64(entry.deleted) {
			_ = segment.Close()
			return nil, corruptIndexf("document count overflows at segment %d", position)
		}
		artifactBytes := entry.identifierBytes + entry.deletionBytes
		if artifactBytes > math.MaxInt64 || info.Bytes > math.MaxInt64-int64(artifactBytes) ||
			info.Bytes+int64(artifactBytes) > math.MaxInt64-index.bytes {
			_ = segment.Close()
			return nil, corruptIndexf("byte count overflows at segment %d", position)
		}
		for field, total := range segment.fieldStats {
			deletedTokens := uint64(0)
			if deleted != nil {
				deletedTokens = deleted.tokenTotals[field]
			}
			if deletedTokens > total {
				_ = segment.Close()
				return nil, corruptIndexf("deleted field statistics exceed segment %d", position)
			}
			liveTotal := total - deletedTokens
			if index.fieldStats[field] > math.MaxUint64-liveTotal {
				_ = segment.Close()
				return nil, corruptIndexf("field statistics overflow at segment %d", position)
			}
			index.fieldStats[field] += liveTotal
		}
		index.bases = append(index.bases, index.physical)
		index.physical += uint64(info.Documents)
		index.documents += uint64(info.Documents - entry.deleted)
		index.deleted += uint64(entry.deleted)
		index.bytes += info.Bytes + int64(artifactBytes)
		index.segments = append(index.segments, segment)
		index.deletions = append(index.deletions, deleted)
	}
	closeOnError = false
	return index, nil
}

// Info reports stable metadata for this snapshot.
func (index *Index) Info() IndexInfo {
	if index == nil {
		return IndexInfo{}
	}
	return IndexInfo{
		Path: index.directory, Manifest: index.manifest, Generation: index.generation,
		Segments: len(index.segments), Documents: index.documents,
		PhysicalDocuments: index.physical, Deleted: index.deleted, Bytes: index.bytes,
	}
}

func (index *Index) ensureOpen() error {
	if index == nil || index.closed.Load() {
		return ErrClosed
	}
	return nil
}

// Verify performs a complete verification of every segment in the snapshot.
func (index *Index) Verify(ctx context.Context) error {
	if err := index.ensureOpen(); err != nil {
		return err
	}
	if ctx == nil {
		return fmt.Errorf("search: verify context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for position, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := segment.Verify(ctx); err != nil {
			return fmt.Errorf("search: verify segment %d: %w", position, err)
		}
		if err := index.deletions[position].Verify(ctx, segment); err != nil {
			return fmt.Errorf("search: verify deletions %d: %w", position, err)
		}
		entry := index.entries[position]
		if entry.identifierName != "" {
			identifier, err := openIdentifierIndex(
				filepath.Join(index.directory, entry.identifierName), index.schema.fingerprint,
				entry.documents, entry.identifierBytes,
			)
			if err != nil {
				return fmt.Errorf("search: verify identifier index %d: %w", position, err)
			}
			verifyErr := identifier.Verify(ctx, segment)
			closeErr := identifier.Close()
			if verifyErr != nil {
				return fmt.Errorf("search: verify identifier index %d: %w", position, verifyErr)
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}

// Close releases all segment file handles owned by this snapshot.
func (index *Index) Close() error {
	if index == nil || !index.closed.CompareAndSwap(false, true) {
		return nil
	}
	var closeErrors []error
	for _, segment := range index.segments {
		if err := segment.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}
