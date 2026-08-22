// Package analytics provides an experimental, embedded, pure-Go analytics core.
//
// The package is intentionally small and deterministic: it models immutable
// column segments, zone-map pruning, streaming scans, grouped aggregates, and
// durable segment files without depending on Kitwork runtime packages or any
// external database driver. It is the first slice of a larger analytics engine
// that can later be backed by KitDB change feeds and a richer execution model.
package analytics
