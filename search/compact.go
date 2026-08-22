package search

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
)

const (
	defaultCompactMaximumSegments       = 8
	defaultCompactMaximumInputSegments  = 8
	defaultCompactMaximumInputDocuments = uint64(1_000_000)
	defaultCompactDeletionRatio         = 0.10
	maximumCompactInputSegments         = 32
)

// CompactOptions bound one tiered compaction. Zero values select conservative
// defaults. Force still honors the input segment and document limits.
type CompactOptions struct {
	MaximumSegments       int
	MaximumInputSegments  int
	MaximumInputDocuments uint64
	DeletionRatio         float64
	Force                 bool
}

// Compact publishes at most one tiered merge generation. It first commits any
// pending mutations, verifies the source snapshot, streams live postings into
// one immutable segment, and purges tombstones in the selected range.
func (writer *IndexWriter) Compact(ctx context.Context, options CompactOptions) (IndexInfo, bool, error) {
	if err := writer.ensureOpen(); err != nil {
		return IndexInfo{}, false, err
	}
	if writer.replacement != nil {
		return IndexInfo{}, false, ErrReplacementActive
	}
	if ctx == nil {
		return IndexInfo{}, false, fmt.Errorf("search: compact context is nil")
	}
	if err := ctx.Err(); err != nil {
		return IndexInfo{}, false, err
	}
	if writer.builder.DocumentCount() != 0 || len(writer.pending) != 0 || len(writer.pendingDeletes) != 0 || writer.committed.generation == 0 {
		if _, err := writer.Commit(ctx); err != nil {
			return IndexInfo{}, false, err
		}
	}
	current, err := manifestIndexInfo(writer.directory, writer.committed)
	if err != nil {
		return IndexInfo{}, false, err
	}
	normalized, err := normalizeCompactOptions(options)
	if err != nil {
		return IndexInfo{}, false, err
	}
	if writer.snapshot == nil || len(writer.snapshot.segments) == 0 {
		return current, false, nil
	}
	start, end, selected := selectCompactRange(writer.committed.segments, normalized)
	if !selected {
		return current, false, nil
	}
	if writer.committed.generation == math.MaxUint64 {
		return IndexInfo{}, false, fmt.Errorf("search: manifest generation overflow")
	}
	if err := writer.verifyCompactRange(ctx, start, end); err != nil {
		return IndexInfo{}, false, err
	}

	selectedSegments := writer.snapshot.segments[start:end]
	selectedDeletions := writer.snapshot.deletions[start:end]
	var liveDocuments uint64
	for _, entry := range writer.committed.segments[start:end] {
		liveDocuments += uint64(entry.documents - entry.deleted)
	}
	created := make([]string, 0, 2)
	keepCreated := false
	defer func() {
		if !keepCreated {
			removeArtifactPaths(created)
		}
	}()

	nextGeneration := writer.committed.generation + 1
	var mergedEntry manifestSegment
	if liveDocuments != 0 {
		name, err := newArtifactFilename("segment", nextGeneration, ".ks")
		if err != nil {
			return IndexInfo{}, false, err
		}
		segmentInfo, err := writeMergedSegment(
			ctx, filepath.Join(writer.directory, name), writer.schema,
			selectedSegments, selectedDeletions,
		)
		if err != nil {
			return IndexInfo{}, false, err
		}
		created = append(created, segmentInfo.Path)
		merged, err := OpenSegment(segmentInfo.Path, writer.schema)
		if err != nil {
			return IndexInfo{}, false, err
		}
		if err := merged.Verify(ctx); err != nil {
			_ = merged.Close()
			return IndexInfo{}, false, err
		}
		identifierName := segmentArtifactFilename(name, ".ki")
		identifierInfo, err := writeIdentifierIndexForSegment(
			ctx, filepath.Join(writer.directory, identifierName), merged,
		)
		if err != nil {
			_ = merged.Close()
			return IndexInfo{}, false, err
		}
		created = append(created, identifierInfo.Path)
		identifierIndex, err := openIdentifierIndex(
			identifierInfo.Path, writer.schema.fingerprint, segmentInfo.Documents, uint64(identifierInfo.Bytes),
		)
		if err != nil {
			_ = merged.Close()
			return IndexInfo{}, false, err
		}
		verifyErr := identifierIndex.Verify(ctx, merged)
		closeIdentifierErr := identifierIndex.Close()
		closeSegmentErr := merged.Close()
		if verifyErr != nil {
			return IndexInfo{}, false, verifyErr
		}
		if closeIdentifierErr != nil {
			return IndexInfo{}, false, closeIdentifierErr
		}
		if closeSegmentErr != nil {
			return IndexInfo{}, false, closeSegmentErr
		}
		mergedEntry = manifestSegment{
			name: name, documents: segmentInfo.Documents, bytes: uint64(segmentInfo.Bytes),
			identifierName: identifierName, identifierBytes: uint64(identifierInfo.Bytes),
		}
	}

	segmentCapacity := len(writer.committed.segments) - (end - start)
	if liveDocuments != 0 {
		segmentCapacity++
	}
	segments := make([]manifestSegment, 0, segmentCapacity)
	segments = append(segments, writer.committed.segments[:start]...)
	if liveDocuments != 0 {
		segments = append(segments, mergedEntry)
	}
	segments = append(segments, writer.committed.segments[end:]...)
	manifest := indexManifest{
		generation: nextGeneration, schemaHash: writer.schema.fingerprint, segments: segments,
	}
	info, err := manifestIndexInfo(writer.directory, manifest)
	if err != nil {
		return IndexInfo{}, false, err
	}
	candidate, err := openIndexManifest(writer.directory, writer.schema, manifest)
	if err != nil {
		return IndexInfo{}, false, err
	}
	writer.injectFault(writerFaultBeforeManifest)
	publication, publishErr := writeManifest(ctx, writer.directory, manifest, writer.directorySync)
	if publishErr != nil && !publication.published {
		_ = candidate.Close()
		return IndexInfo{}, false, publishErr
	}
	if !publication.published {
		_ = candidate.Close()
		return IndexInfo{}, false, fmt.Errorf("search: manifest was not published")
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
	keepCreated = true
	if previous != nil {
		_ = previous.Close()
	}
	info.Manifest = publication.path
	return info, true, durabilityUncertain(publishErr)
}

func (writer *IndexWriter) verifyCompactRange(ctx context.Context, start, end int) error {
	for position := start; position < end; position++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		segment := writer.snapshot.segments[position]
		if err := segment.Verify(ctx); err != nil {
			return fmt.Errorf("search: verify merge source %d: %w", position, err)
		}
		if err := writer.snapshot.deletions[position].Verify(ctx, segment); err != nil {
			return fmt.Errorf("search: verify merge deletions %d: %w", position, err)
		}
		if writer.snapshot.entries[position].identifierName != "" {
			if _, err := writer.identifierIndex(ctx, position); err != nil {
				return fmt.Errorf("search: verify merge identifier index %d: %w", position, err)
			}
		}
	}
	return nil
}

func normalizeCompactOptions(options CompactOptions) (CompactOptions, error) {
	if options.MaximumSegments == 0 {
		options.MaximumSegments = defaultCompactMaximumSegments
	}
	if options.MaximumInputSegments == 0 {
		options.MaximumInputSegments = defaultCompactMaximumInputSegments
	}
	if options.MaximumInputDocuments == 0 {
		options.MaximumInputDocuments = defaultCompactMaximumInputDocuments
	}
	if options.DeletionRatio == 0 {
		options.DeletionRatio = defaultCompactDeletionRatio
	}
	if options.MaximumSegments < 1 || options.MaximumInputSegments < 1 ||
		options.MaximumInputSegments > maximumCompactInputSegments ||
		options.MaximumInputDocuments == 0 || options.MaximumInputDocuments > math.MaxUint32 ||
		options.MaximumInputDocuments > uint64(maxIntValue()/identifierIndexEntrySize) ||
		options.DeletionRatio < 0 || options.DeletionRatio > 1 ||
		math.IsNaN(options.DeletionRatio) || math.IsInf(options.DeletionRatio, 0) {
		return CompactOptions{}, fmt.Errorf("search: invalid compact options")
	}
	return options, nil
}

func selectCompactRange(segments []manifestSegment, options CompactOptions) (int, int, bool) {
	if len(segments) == 0 {
		return 0, 0, false
	}
	if options.Force {
		if start, end, found := widestCompactWindow(segments, options, false); found &&
			(end-start > 1 || segments[start].deleted != 0) {
			return start, end, true
		}
		return 0, 0, false
	}

	best := -1
	bestRatio := options.DeletionRatio
	for position, segment := range segments {
		if uint64(segment.documents) > options.MaximumInputDocuments || segment.deleted == 0 {
			continue
		}
		ratio := float64(segment.deleted) / float64(segment.documents)
		if ratio >= bestRatio {
			best, bestRatio = position, ratio
		}
	}
	if best >= 0 {
		return best, best + 1, true
	}
	if len(segments) <= options.MaximumSegments {
		return 0, 0, false
	}
	if start, end, found := widestCompactWindow(segments, options, true); found && end-start > 1 {
		return start, end, true
	}
	if start, end, found := widestCompactWindow(segments, options, false); found && end-start > 1 {
		return start, end, true
	}
	return 0, 0, false
}

func widestCompactWindow(segments []manifestSegment, options CompactOptions, requireTier bool) (int, int, bool) {
	maximumWidth := min(len(segments), options.MaximumInputSegments)
	for width := maximumWidth; width >= 1; width-- {
		bestStart := -1
		bestBytes := uint64(math.MaxUint64)
		for start := 0; start+width <= len(segments); start++ {
			var documents uint64
			var bytes uint64
			minimumBytes := uint64(math.MaxUint64)
			maximumBytes := uint64(0)
			valid := true
			for _, segment := range segments[start : start+width] {
				if uint64(segment.documents) > options.MaximumInputDocuments ||
					documents > options.MaximumInputDocuments-uint64(segment.documents) ||
					bytes > math.MaxUint64-segment.bytes {
					valid = false
					break
				}
				documents += uint64(segment.documents)
				bytes += segment.bytes
				minimumBytes = min(minimumBytes, segment.bytes)
				maximumBytes = max(maximumBytes, segment.bytes)
			}
			if !valid || (requireTier && !sameCompactTier(minimumBytes, maximumBytes)) {
				continue
			}
			if bytes < bestBytes {
				bestStart, bestBytes = start, bytes
			}
		}
		if bestStart >= 0 {
			return bestStart, bestStart + width, true
		}
	}
	return 0, 0, false
}

func sameCompactTier(minimum, maximum uint64) bool {
	if minimum == 0 {
		return maximum == 0
	}
	if minimum > math.MaxUint64/8 {
		return true
	}
	return maximum <= minimum*8
}
