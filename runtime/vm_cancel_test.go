package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// TestVMCancellation verifies STABILITY.md §2:
// "Cancellation prevents work that has not started. Running script execution is
// bounded by energy until VM-level context cancellation is implemented."
// With VM-level context cancellation now implemented, a cancelled context halts
// execution immediately.
func TestVMCancellation(t *testing.T) {
	// Loop: JUMP to 0 endlessly (infinite loop)
	code := []byte{
		byte(JUMP), 0, 0,
	}
	vm := New(code, []value.Value{})
	vm.MaxEnergy = 1_000_000_000 // Very large energy ceiling

	ctx, cancel := context.WithCancel(context.Background())
	vm.Context = ctx

	// Cancel context asynchronously after 10ms
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res := vm.Run()
	elapsed := time.Since(start)

	if res.K != value.Invalid {
		t.Fatalf("Expected invalid return value on context cancellation, got %v", res)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Execution took too long to halt after cancellation: %v", elapsed)
	}
}
