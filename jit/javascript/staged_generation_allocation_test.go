//go:build !race

package javascript

import "testing"

func requireGenerationAllocationWithin(t *testing.T, label string, allocated, limit uint64) {
	t.Helper()
	if allocated > limit {
		t.Fatalf("%s allocated %d bytes, limit %d", label, allocated, limit)
	}
}
