package runtime

import (
	"testing"

	"github.com/kitwork/engine/value"
)

type countingCommitter struct {
	commits int
}

func (c *countingCommitter) Commit() value.CommitResult {
	c.commits++
	return value.CommitResult{}
}

func TestCommitExecutesEffectAndPreservesValue(t *testing.T) {
	effect := &countingCommitter{}
	vm := New(mustProgram(t,
		[]byte{byte(LOAD), 0, 0, byte(COMMIT), byte(RETURN)},
		[]value.Value{value.NewString("effect")},
	))
	vm.Globals["effect"] = value.New(effect)

	got := vm.Run()
	if effect.commits != 1 {
		t.Fatalf("commits = %d, want 1", effect.commits)
	}
	if got.V != effect {
		t.Fatalf("COMMIT changed the value: got %T %v", got.V, got.V)
	}
}

func TestCommitIsNoOpForOrdinaryValue(t *testing.T) {
	vm := New(mustProgram(t,
		[]byte{byte(PUSH), 0, 0, byte(COMMIT), byte(RETURN)},
		[]value.Value{value.New(42)},
	))

	if got := vm.Run(); got.Int() != 42 {
		t.Fatalf("result = %v, want 42", got.Interface())
	}
}
