package relational

import (
	"bytes"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

const (
	maximumTransactionDependencyKeys   = 4096
	maximumTransactionDependencyRanges = 256
	maximumTransactionDependencyBytes  = 1 << 20
)

type dependencyAccess uint8

const (
	dependencyRead dependencyAccess = 1 << iota
	dependencyWrite
)

// Guarded by Transaction.mu. Unsupported readers keep the global fence.
type transactionDependencies struct {
	keys   map[string]dependencyAccess
	ranges []kitdbengine.RangeOptions
	bytes  int
	coarse bool
}

func (dependencies *transactionDependencies) recordKey(key []byte, access dependencyAccess) {
	if dependencies.coarse {
		return
	}
	if existing, found := dependencies.keys[string(key)]; found {
		if existing|access != existing {
			dependencies.keys[string(key)] = existing | access
		}
		return
	}
	if len(dependencies.keys) >= maximumTransactionDependencyKeys ||
		len(key) > maximumTransactionDependencyBytes-dependencies.bytes {
		dependencies.fallback()
		return
	}
	if dependencies.keys == nil {
		dependencies.keys = make(map[string]dependencyAccess)
	}
	dependencies.keys[string(key)] = access
	dependencies.bytes += len(key)
}

func (dependencies *transactionDependencies) recordRange(options kitdbengine.RangeOptions) {
	if dependencies.coarse {
		return
	}
	// Direction and LIMIT do not narrow the protected physical keyspace.
	for _, existing := range dependencies.ranges {
		if bytes.Equal(existing.Start, options.Start) && bytes.Equal(existing.End, options.End) && bytes.Equal(existing.Prefix, options.Prefix) {
			return
		}
	}
	remaining := maximumTransactionDependencyBytes - dependencies.bytes
	for _, bound := range [][]byte{options.Start, options.End, options.Prefix} {
		if len(bound) > remaining {
			dependencies.fallback()
			return
		}
		remaining -= len(bound)
	}
	if len(dependencies.ranges) >= maximumTransactionDependencyRanges {
		dependencies.fallback()
		return
	}
	dependencies.ranges = append(dependencies.ranges, kitdbengine.RangeOptions{
		Start: bytes.Clone(options.Start), End: bytes.Clone(options.End), Prefix: bytes.Clone(options.Prefix),
	})
	dependencies.bytes = maximumTransactionDependencyBytes - remaining
}

func (dependencies *transactionDependencies) fallback() {
	*dependencies = transactionDependencies{coarse: true}
}

func (dependencies *transactionDependencies) commitDependencies() kitdbengine.CommitDependencies {
	if dependencies.coarse {
		return kitdbengine.CommitDependencies{Coarse: true}
	}
	keys := make([][]byte, 0, len(dependencies.keys))
	for key := range dependencies.keys {
		keys = append(keys, []byte(key))
	}
	return kitdbengine.CommitDependencies{Keys: keys, Ranges: dependencies.ranges}
}

func (transaction *Transaction) requireCoarseValidation() {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if !transaction.done && !transaction.readOnly {
		transaction.dependencies.fallback()
	}
}
