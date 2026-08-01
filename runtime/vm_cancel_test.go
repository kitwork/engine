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
	vm := New(mustProgram(t, code, nil))
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

func TestVMCancellationCrossesNestedCallbackExecutions(t *testing.T) {
	const (
		itemName = iota
		one
		itemsName
		callback
		forEachName
		cancelName
	)
	program := mustProgram(t,
		[]byte{
			byte(JUMP), 0, 17,
			byte(LOAD), 0, cancelName,
			byte(CALL), 0,
			byte(POP),
			byte(LOAD), 0, itemName,
			byte(PUSH), 0, one,
			byte(ADD),
			byte(RETURN),
			byte(LOAD), 0, itemsName,
			byte(PUSH), 0, callback,
			byte(PUSH), 0, forEachName,
			byte(INVOKE), 1,
			byte(RETURN),
		},
		[]value.Value{
			value.NewString("item"),
			value.New(1),
			value.NewString("items"),
			value.New(&value.Lambda{Address: 3, Params: []string{"item"}}),
			value.NewString("forEach"),
			value.NewString("cancel"),
		},
	)

	items := make([]value.Value, 10_000)
	for index := range items {
		items[index] = value.New(index)
	}

	ctx, cancel := context.WithCancel(context.Background())

	vm := New(program)
	vm.Context = ctx
	vm.MaxEnergy = 10_000_000
	vm.Globals["items"] = value.New(items)
	vm.Globals["cancel"] = value.NewFunc(func(...value.Value) value.Value {
		cancel()
		return value.Value{K: value.Nil}
	})

	result := vm.Run()
	diagnostic, ok := DiagnosticFrom(result)
	if !ok || diagnostic.Code != DiagnosticCancelled {
		t.Fatalf("nested callbacks ignored cancellation: %#v", result)
	}
}
