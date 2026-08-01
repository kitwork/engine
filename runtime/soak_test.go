package runtime_test

import (
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func TestPooledVMSoakAcrossPrograms(t *testing.T) {
	sources := []string{
		`
var owner = "callbacks";
const input = [3, 1, 2];
var result = input.sort((a, b) => a - b).map((item) => item * 2).join(",");
`,
		`
var owner = "closure";
const make = (base) => (number) => base + number;
var result = make(40)(2);
`,
		`
var owner = "loop";
var result = 0;
for (let i = 0; i < 20; i++) { result = result + i; }
`,
	}
	wantOwners := []string{"callbacks", "closure", "loop"}
	wantResults := []string{"2,4,6", "42", "190"}
	programs := make([]*compiler.Bytecode, len(sources))
	fingerprints := make([]executionFingerprint, len(sources))
	for index, source := range sources {
		bytecode, err := compiler.CompileSource(source)
		if err != nil {
			t.Fatalf("compile fixture %d: %v", index, err)
		}
		programs[index] = bytecode
		fingerprints[index] = runFingerprint(
			runtime.New(bytecode.Program),
			bytecode.Program,
			map[string]value.Value{"fixture": value.New(index)},
			nil,
			100_000,
		)
	}

	const workers = 16
	iterations := 250
	if os.Getenv("KITWORK_SOAK") == "1" {
		iterations = 6_250
	}

	pool := app.NewPool()
	failures := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				index := (worker + iteration) % len(programs)
				vm := pool.Acquire()
				fingerprint := runFingerprint(
					vm,
					programs[index].Program,
					map[string]value.Value{"fixture": value.New(index)},
					nil,
					100_000,
				)
				if !reflect.DeepEqual(fingerprint, fingerprints[index]) {
					pool.Release(vm)
					failures <- fmt.Errorf(
						"worker %d iteration %d: execution fingerprint changed",
						worker,
						iteration,
					)
					return
				}
				if got := vm.Vars["owner"].String(); got != wantOwners[index] {
					pool.Release(vm)
					failures <- fmt.Errorf(
						"worker %d iteration %d: owner %q, want %q",
						worker,
						iteration,
						got,
						wantOwners[index],
					)
					return
				}
				if got := vm.Vars["result"].String(); got != wantResults[index] {
					pool.Release(vm)
					failures <- fmt.Errorf(
						"worker %d iteration %d: result %q, want %q",
						worker,
						iteration,
						got,
						wantResults[index],
					)
					return
				}
				pool.Release(vm)
			}
		}(worker)
	}
	wait.Wait()
	close(failures)

	for failure := range failures {
		t.Error(failure)
	}
	if active := pool.Active(); active != 0 {
		t.Fatalf("pool retained %d active VM leases", active)
	}
}
