// Package search provides an embedded, pure-Go full-text search engine.
//
// The package is experimental. Its storage model uses immutable, independently
// verifiable segments, exact single- and multi-field BM25, bounded frequency
// caches, presentation-neutral highlighting, and generation manifests for
// multi-segment snapshots. Its multi-tenant manager bounds execution,
// admission queues, mutation memory, replacement builds, and maintenance
// concurrency. It deliberately has no dependency on Kitwork runtime packages,
// SQL drivers, or a network service.
package search
