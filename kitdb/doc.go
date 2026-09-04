// Package kitdb implements an embedded, pure-Go database kernel under KitDB
// 1.0 release qualification.
//
// The current milestone combines append-only immutable generations, dual
// checksummed superblocks, metadata-only startup, sparse point and range lookup,
// bounded read snapshots, bounded concurrent transaction preparation,
// single-owner group commit, segment compaction and page caching, an append-only
// transactional WAL, a core-owned transactional schema catalog, opt-in
// immutable transaction history, durable retention pins, safe whole-segment
// pruning, verified standalone backup anchors, exact restore to a retained
// transaction, explicit one-way replica bootstrap and catch-up, bounded replica
// wire messages, a crash-safe filesystem mailbox, and deterministic recovery.
// It is intentionally independent of the Kitwork runtime. Its bounded
// single-node release-candidate profile is defined in RELEASE_1_0.md; broader
// database and network claims remain explicitly unsupported.
package kitdb
