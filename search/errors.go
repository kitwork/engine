package search

import "errors"

var (
	// ErrSegmentFull asks the caller to flush the current builder and retry the
	// document in a new segment.
	ErrSegmentFull = errors.New("search: segment builder reached its flush threshold")
	// ErrBuilderSealed is returned when a document is added after a segment was written.
	ErrBuilderSealed = errors.New("search: segment builder is sealed")
	// ErrClosed is returned when an operation uses a closed search resource.
	ErrClosed = errors.New("search: resource is closed")
	// ErrCorruptSegment marks malformed or inconsistent on-disk data.
	ErrCorruptSegment = errors.New("search: corrupt segment")
	// ErrSchemaMismatch means the supplied schema is not the schema that built the segment.
	ErrSchemaMismatch = errors.New("search: schema does not match segment")
	// ErrUnsupportedVersion means an on-disk format is newer or otherwise unknown.
	ErrUnsupportedVersion = errors.New("search: unsupported segment version")
	// ErrIndexNotFound means an index directory has no committed manifest.
	ErrIndexNotFound = errors.New("search: index has no committed manifest")
	// ErrCorruptIndex marks malformed or inconsistent index metadata.
	ErrCorruptIndex = errors.New("search: corrupt index")
	// ErrTooManySegments means the append-only index must be merged before it can grow.
	ErrTooManySegments = errors.New("search: index reached its segment limit")
	// ErrDocumentNotFound means an update target is not live in the committed snapshot.
	ErrDocumentNotFound = errors.New("search: document not found")
	// ErrPendingDocument means the same identifier already exists in the current uncommitted batch.
	ErrPendingDocument = errors.New("search: document identifier is already pending")
	// ErrPendingChanges means a whole-index replacement cannot start while the
	// writer has an uncommitted add, update, delete, or flushed segment.
	ErrPendingChanges = errors.New("search: writer has pending changes")
	// ErrReplacementActive means a streaming whole-index replacement owns the
	// writer until that replacement commits or aborts.
	ErrReplacementActive = errors.New("search: replacement is active")
	// ErrManagerCapacity means the bounded manager cannot open another index.
	ErrManagerCapacity = errors.New("search: manager reached its open-index limit")
	// ErrSearchOverloaded means a bounded search admission queue is full.
	ErrSearchOverloaded = errors.New("search: search admission queue is full")
	// ErrIndexUnavailable means a managed writer encountered a terminal publication failure.
	ErrIndexUnavailable = errors.New("search: managed index is unavailable")
	// ErrMutationTooLarge means one mutation cannot fit a configured byte budget.
	ErrMutationTooLarge = errors.New("search: mutation exceeds its byte budget")
	// ErrWriterLocked means another writer owns the same index directory.
	ErrWriterLocked = errors.New("search: index writer is already active")
	// ErrDurabilityUncertain means a manifest became visible but its directory
	// sync failed. The returned generation was adopted and must not be retried
	// as though the mutation had not been published.
	ErrDurabilityUncertain = errors.New("search: published generation durability is uncertain")
)

func durabilityUncertain(err error) error {
	if err == nil {
		return nil
	}
	return errors.Join(ErrDurabilityUncertain, err)
}
