package kitdb

import "bytes"

const (
	maximumCommitDependencyKeys   = 4096
	maximumCommitDependencyRanges = 256
	maximumCommitDependencyBytes  = 1 << 20
	maximumConflictJournalFrames  = 512
	maximumConflictJournalKeys    = 8192
	maximumConflictJournalBytes   = 1 << 20
)

// CommitDependencies is a trusted kernel caller's complete physical read set.
// Include point misses and full predicate ranges. Write keys are added by Tx.
// Coarse requests the existing database-wide fence. SQL does not use this yet.
type CommitDependencies struct {
	Keys   [][]byte
	Ranges []RangeOptions
	Coarse bool
}

type commitValidation struct {
	keys   map[string]struct{}
	ranges []RangeOptions
	bytes  int
}

func prepareCommitValidation(dependencies CommitDependencies, operations []operation) *commitValidation {
	if dependencies.Coarse || len(dependencies.Keys) > maximumCommitDependencyKeys || len(dependencies.Ranges) > maximumCommitDependencyRanges {
		return nil
	}
	validation := &commitValidation{keys: make(map[string]struct{})}
	add := func(key []byte) bool {
		if _, found := validation.keys[string(key)]; found {
			return true
		}
		if len(validation.keys) >= maximumCommitDependencyKeys || len(key) > maximumCommitDependencyBytes-validation.bytes {
			return false
		}
		validation.keys[string(key)] = struct{}{}
		validation.bytes += len(key)
		return true
	}
	for _, key := range dependencies.Keys {
		if !add(key) {
			return nil
		}
	}
	for _, operation := range operations {
		if !add(operation.key) {
			return nil
		}
	}
	for _, options := range dependencies.Ranges {
		for _, bound := range [][]byte{options.Start, options.End, options.Prefix} {
			if len(bound) > maximumCommitDependencyBytes-validation.bytes {
				return nil
			}
			validation.bytes += len(bound)
		}
		validation.ranges = append(validation.ranges, RangeOptions{Start: bytes.Clone(options.Start), End: bytes.Clone(options.End), Prefix: bytes.Clone(options.Prefix)})
	}
	return validation
}

func (validation *commitValidation) touches(key []byte) bool {
	if _, found := validation.keys[string(key)]; found {
		return true
	}
	for _, interval := range validation.ranges {
		if (len(interval.Start) == 0 || bytes.Compare(key, interval.Start) >= 0) &&
			(len(interval.End) == 0 || bytes.Compare(key, interval.End) < 0) && bytes.HasPrefix(key, interval.Prefix) {
			return true
		}
	}
	return false
}

func (validation *commitValidation) conflicts(request *commitRequest) bool {
	if request.sequenceOnly {
		return false
	}
	if len(request.operations) > maximumConflictJournalKeys || requestTouchesCatalog(request) {
		return true
	}
	for _, operation := range request.operations {
		if validation.touches(operation.key) {
			return true
		}
	}
	return false
}

type conflictMutation struct {
	transaction uint64
	keys        []string
	bytes       int
	catalog     bool
}

// Owned exclusively by commitMu, activated only by a dependency-aware commit.
// No WAL bodies or row values are retained and no metadata is persisted.
type conflictJournal struct {
	entries                  []conflictMutation
	head, count, keys, bytes int
	floor                    uint64
}

func (journal *conflictJournal) activate(boundary uint64) {
	if journal.entries == nil {
		journal.entries = make([]conflictMutation, maximumConflictJournalFrames)
		journal.floor = boundary
	}
}

func (journal *conflictJournal) conflicts(base uint64, validation *commitValidation) bool {
	if base < journal.floor {
		return true
	}
	for i := 0; i < journal.count; i++ {
		entry := &journal.entries[(journal.head+i)%len(journal.entries)]
		if entry.transaction <= base {
			continue
		}
		if entry.catalog {
			return true
		}
		for _, key := range entry.keys {
			if validation.touches([]byte(key)) {
				return true
			}
		}
	}
	return false
}

func (journal *conflictJournal) evict() {
	entry := &journal.entries[journal.head]
	journal.floor = entry.transaction
	journal.keys -= len(entry.keys)
	journal.bytes -= entry.bytes
	*entry = conflictMutation{}
	journal.head = (journal.head + 1) % len(journal.entries)
	journal.count--
}

func (journal *conflictJournal) record(transaction uint64, request *commitRequest) {
	if journal.entries == nil || request.sequenceOnly {
		return
	}
	entry := conflictMutation{transaction: transaction, catalog: requestTouchesCatalog(request)}
	if !entry.catalog {
		for _, operation := range request.operations {
			if len(request.operations) > maximumConflictJournalKeys || len(operation.key) > maximumConflictJournalBytes-entry.bytes {
				for journal.count > 0 {
					journal.evict()
				}
				journal.floor = transaction
				return
			}
			entry.bytes += len(operation.key)
		}
		entry.keys = make([]string, len(request.operations))
		for i, operation := range request.operations {
			entry.keys[i] = string(operation.key)
		}
	}
	for journal.count > 0 && (journal.count == len(journal.entries) || journal.keys+len(entry.keys) > maximumConflictJournalKeys || journal.bytes+entry.bytes > maximumConflictJournalBytes) {
		journal.evict()
	}
	journal.entries[(journal.head+journal.count)%len(journal.entries)] = entry
	journal.count++
	journal.keys += len(entry.keys)
	journal.bytes += entry.bytes
}
