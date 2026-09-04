package search

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

const (
	replacementCompactionTriggerSegments      = 128
	replacementCompactionIngestTargetSegments = 96
	replacementCompactionFinalTargetSegments  = 64
	replacementCompactionMaximumInputSegments = 8
	replacementCompactionMaximumDocuments     = uint64(1_000_000)
)

// compactPendingReplacement bounds a copy-on-write replacement before its
// one manifest publication. It only merges contiguous unpublished segments,
// preserving document order and leaving the current committed index untouched.
func (writer *IndexWriter) compactPendingReplacement(ctx context.Context, target int) error {
	if writer.replacement == nil || len(writer.pending) <= target {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("search: replacement compaction context is nil")
	}
	if target < 1 || target >= manifestMaximumSegments {
		return fmt.Errorf("search: invalid replacement compaction target %d", target)
	}
	options := CompactOptions{
		MaximumSegments:       target,
		MaximumInputSegments:  replacementCompactionMaximumInputSegments,
		MaximumInputDocuments: replacementCompactionMaximumDocuments,
		Force:                 true,
	}
	for len(writer.pending) > target {
		if err := ctx.Err(); err != nil {
			return err
		}
		start, end, selected := selectCompactRange(writer.pending, options)
		if !selected || end-start < 2 {
			break
		}
		if err := writer.mergePendingReplacementRange(ctx, start, end); err != nil {
			return err
		}
	}
	if len(writer.pending) > manifestMaximumSegments {
		return ErrTooManySegments
	}
	return nil
}

func (writer *IndexWriter) mergePendingReplacementRange(ctx context.Context, start, end int) (returnErr error) {
	if writer.replacement == nil || start < 0 || end > len(writer.pending) || end-start < 2 {
		return fmt.Errorf("search: invalid pending replacement merge range [%d,%d)", start, end)
	}
	entries := append([]manifestSegment(nil), writer.pending[start:end]...)
	segments := make([]*Segment, len(entries))
	deletions := make([]*deletedDocuments, len(entries))
	sourcesClosed := false
	defer func() {
		if !sourcesClosed {
			returnErr = errors.Join(returnErr, closePendingMergeSources(segments))
		}
	}()
	for position, entry := range entries {
		segment, err := OpenSegment(filepath.Join(writer.directory, entry.name), writer.schema)
		if err != nil {
			return err
		}
		segments[position] = segment
		if err := segment.Verify(ctx); err != nil {
			return fmt.Errorf("search: verify pending merge source %d: %w", start+position, err)
		}
	}

	name, err := newArtifactFilename("segment", writer.committed.generation+1, ".ks")
	if err != nil {
		return err
	}
	created := make([]string, 0, 2)
	keepCreated := false
	defer func() {
		if !keepCreated {
			removeArtifactPaths(created)
		}
	}()
	segmentInfo, err := writeMergedSegment(
		ctx, filepath.Join(writer.directory, name), writer.schema, segments, deletions,
	)
	if err != nil {
		return err
	}
	created = append(created, segmentInfo.Path)

	merged, err := OpenSegment(segmentInfo.Path, writer.schema)
	if err != nil {
		return err
	}
	if err := merged.Verify(ctx); err != nil {
		_ = merged.Close()
		return err
	}
	identifierName := segmentArtifactFilename(name, ".ki")
	identifierInfo, err := writeIdentifierIndexForSegment(
		ctx, filepath.Join(writer.directory, identifierName), merged,
	)
	if err != nil {
		_ = merged.Close()
		return err
	}
	created = append(created, identifierInfo.Path)
	identifier, err := openIdentifierIndex(
		identifierInfo.Path, writer.schema.fingerprint, segmentInfo.Documents, uint64(identifierInfo.Bytes),
	)
	if err != nil {
		_ = merged.Close()
		return err
	}
	verifyErr := identifier.Verify(ctx, merged)
	closeIdentifierErr := identifier.Close()
	closeSegmentErr := merged.Close()
	if verifyErr != nil || closeIdentifierErr != nil || closeSegmentErr != nil {
		return errors.Join(verifyErr, closeIdentifierErr, closeSegmentErr)
	}
	if err := closePendingMergeSources(segments); err != nil {
		sourcesClosed = true
		return err
	}
	sourcesClosed = true

	mergedEntry := manifestSegment{
		name: name, documents: segmentInfo.Documents, bytes: uint64(segmentInfo.Bytes),
		identifierName: identifierName, identifierBytes: uint64(identifierInfo.Bytes),
	}
	pending := make([]manifestSegment, 0, len(writer.pending)-(end-start)+1)
	pending = append(pending, writer.pending[:start]...)
	pending = append(pending, mergedEntry)
	pending = append(pending, writer.pending[end:]...)
	writer.pending = pending
	writer.pendingObsolete = append(writer.pendingObsolete, entries...)
	keepCreated = true
	_ = writer.cleanupObsoletePendingArtifacts()
	return nil
}

func closePendingMergeSources(segments []*Segment) error {
	var closeErrors []error
	for position, segment := range segments {
		if segment == nil {
			continue
		}
		closeErrors = append(closeErrors, segment.Close())
		segments[position] = nil
	}
	return errors.Join(closeErrors...)
}

func (writer *IndexWriter) cleanupObsoletePendingArtifacts() error {
	if len(writer.pendingObsolete) == 0 {
		return nil
	}
	remaining := writer.pendingObsolete[:0]
	var cleanupErrors []error
	for _, segment := range writer.pendingObsolete {
		if err := writer.removeSegmentArtifacts([]manifestSegment{segment}); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			remaining = append(remaining, segment)
		}
	}
	writer.pendingObsolete = remaining
	return errors.Join(cleanupErrors...)
}
