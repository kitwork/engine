package value

import "testing"

var textResult string

func TestTextReturnsStringPayloadWithoutAllocation(t *testing.T) {
	value := NewString("runtime")
	if got := value.Text(); got != "runtime" {
		t.Fatalf("Text() = %q, want %q", got, "runtime")
	}

	allocs := testing.AllocsPerRun(1_000, func() {
		textResult = value.Text()
	})
	if allocs != 0 {
		t.Fatalf("Text() allocations = %.2f, want 0", allocs)
	}
}

func TestTextPreservesInvalidMessage(t *testing.T) {
	value := Value{K: Invalid, V: "invalid message"}
	if got := value.Text(); got != "invalid message" {
		t.Fatalf("Text() = %q, want %q", got, "invalid message")
	}
}
