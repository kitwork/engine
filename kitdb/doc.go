// Package kitdb implements an experimental, embedded, pure-Go database kernel.
//
// The current milestone combines append-only immutable generations, dual
// checksummed superblocks, metadata-only startup, sparse point and range lookup,
// bounded read snapshots, segment compaction and page caching, an append-only
// transactional WAL, and deterministic recovery. It is intentionally
// independent of the Kitwork runtime and is not yet production-ready storage.
package kitdb
