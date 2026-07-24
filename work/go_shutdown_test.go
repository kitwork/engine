package work

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// STABILITY.md §4 — detached background work:
// "App shutdown stops accepting tasks, cancels pending work, and waits for accepted work to finish
// before closing app resources."
//
// These tests drive the real path: a compiled lambda handed to KitWork.Go, then Tenant.Close().

// backgroundHarness compiles a fixture whose returned closure calls block() then finish(), so a test
// can hold a detached task open and observe shutdown behaviour around it.
func backgroundHarness(t *testing.T) (*Tenant, func(block, finish value.Value) value.Value) {
	t.Helper()

	bc, err := compiler.CompileSource(`
var makeTask = (block, finish) => {
    return () => {
        block();
        finish();
    };
};
`)
	if err != nil {
		t.Fatalf("compile fixture: %v", err)
	}

	vm := runtime.New(bc.Instructions, bc.Constants)
	vm.Globals = make(map[string]value.Value)
	if res := vm.Run(); res.K == value.Invalid {
		t.Fatalf("run fixture: %v", res.V)
	}

	makeTask, ok := vm.Vars["makeTask"]
	if !ok || !makeTask.IsCallable() {
		t.Fatal("makeTask was not defined")
	}

	tenant := &Tenant{bytecode: &compiler.Bytecode{}, vm: vm, MaxEnergy: 1_000_000}
	build := func(block, finish value.Value) value.Value {
		return vm.ExecuteLambda(makeTask.V.(*value.Lambda), []value.Value{block, finish})
	}
	return tenant, build
}

// TestCloseWaitsForAcceptedBackgroundWork proves Close() BLOCKS until an accepted task finishes —
// the "waits for accepted work to finish before closing app resources" half of §4. If Close returned
// early, the tenant's databases could be closed underneath a running task.
func TestCloseWaitsForAcceptedBackgroundWork(t *testing.T) {
	tenant, build := backgroundHarness(t)

	started := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool

	block := value.NewFunc(func(args ...value.Value) value.Value {
		close(started)
		<-release
		return value.NewNil()
	})
	finish := value.NewFunc(func(args ...value.Value) value.Value {
		finished.Store(true)
		return value.NewNil()
	})

	(&KitWork{tenant: tenant}).Go(build(block, finish))

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background task did not start")
	}

	closed := make(chan struct{})
	go func() {
		tenant.Close()
		close(closed)
	}()

	// The task is parked inside block(); Close must still be waiting on it.
	select {
	case <-closed:
		t.Fatal("Close returned while an accepted task was still running")
	case <-time.After(150 * time.Millisecond):
	}
	if finished.Load() {
		t.Fatal("task reported finished before it was released")
	}

	close(release)

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the task completed")
	}
	if !finished.Load() {
		t.Fatal("Close returned before the accepted task finished")
	}
}

// TestCloseStopsAcceptingNewBackgroundWork proves the other half of §4: once shutdown starts, new
// tasks are refused outright rather than launched against a closing tenant.
func TestCloseStopsAcceptingNewBackgroundWork(t *testing.T) {
	tenant, build := backgroundHarness(t)
	tenant.Close()

	if _, _, ok := tenant.startBackgroundTask(); ok {
		t.Fatal("tenant accepted a background task after Close")
	}

	var ran atomic.Bool
	noop := value.NewFunc(func(args ...value.Value) value.Value { return value.NewNil() })
	mark := value.NewFunc(func(args ...value.Value) value.Value {
		ran.Store(true)
		return value.NewNil()
	})

	(&KitWork{tenant: tenant}).Go(build(noop, mark))

	time.Sleep(150 * time.Millisecond)
	if ran.Load() {
		t.Fatal("a background task launched after Close")
	}
}
