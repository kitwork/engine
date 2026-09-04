//go:build windows

package snapshotfile

// Windows has no portable directory fsync. A lost publication is rebuilt, never
// used as evidence that the source database transaction was durable.
func syncParent(string) error { return nil }
