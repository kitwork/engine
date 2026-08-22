//go:build race

package javascript

import "testing"

func requireGenerationAllocationWithin(t *testing.T, label string, allocated, limit uint64) {
	t.Helper()
	t.Logf("%s allocation under race instrumentation: %d bytes (normal-build limit %d)", label, allocated, limit)
}
