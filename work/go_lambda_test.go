package work

import (
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func TestBackgroundLambdaSnapshotsClosureBeforeReturn(t *testing.T) {
	code := `
var counter = 0;
var makeBackground = (block, callback) => {
    return () => {
        block();
        counter = counter + 1;
        callback(counter);
    };
};
`
	bc, err := compiler.CompileSource(code)
	if err != nil {
		t.Fatalf("compile fixture: %v", err)
	}

	vm := runtime.New(bc.Program)
	vm.Globals = make(map[string]value.Value)
	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("run fixture: %v", result.V)
	}

	makeBackground, ok := vm.Vars["makeBackground"]
	if !ok || !makeBackground.IsCallable() {
		t.Fatal("makeBackground was not defined")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	block := value.NewFunc(func(args ...value.Value) value.Value {
		close(started)
		<-release
		return value.NewNil()
	})
	result := make(chan int, 1)
	callback := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) > 0 {
			result <- int(args[0].N)
		}
		return value.NewNil()
	})

	background := vm.ExecuteLambda(makeBackground.V.(*value.Lambda), []value.Value{block, callback})
	lambda, ok := background.V.(*value.Lambda)
	if !ok {
		t.Fatalf("expected background lambda, got %T", background.V)
	}

	// Filesystem route handlers do not execute from tenant.bytecode. The
	// detached closure must carry the folder program that owns its address.
	tenant := &Tenant{
		appRuntime: app.NewRuntime("background-test"),
		ownsApp:    true,
		bytecode:   &compiler.Bytecode{},
		vm:         vm,
		MaxEnergy:  1_000_000,
	}
	(&KitWork{tenant: tenant}).Go(background)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background lambda did not start")
	}

	// Simulate the request VM being reset and its captured source scope changing
	// while the detached task is still alive.
	if lambda.Parent != nil && lambda.Parent.Scope != nil {
		lambda.Parent.Scope["counter"] = value.New(99)
	}
	vm.FastReset(nil, nil)
	close(release)

	select {
	case got := <-result:
		if got != 1 {
			t.Fatalf("background closure leaked caller state: got %d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("background lambda did not complete")
	}

	tenant.Close()
}
