package compiler

import "testing"

func TestComputedIndexAssignmentInsideIf(t *testing.T) {
	got := runResult(t, `
		const cache = {}
		const key = "topic"
		const enabled = true
		if (enabled) cache[key] = 42
		const result = cache[key]
	`)

	wantNum(t, got, 42, "computed index assignment inside if")
}
