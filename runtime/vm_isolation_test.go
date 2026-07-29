package runtime_test

import (
	"sync"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// STABILITY.md §1 — app and site isolation.
//
// These tests run on the PRODUCTION path: the same app.Pool that work.enginePool uses, reset the
// same way a tenant resets it (FastReset with the app's own bytecode/globals, then Run). §1 requires
// cross-app isolation to be proven "with concurrent execution, not only with sequential unit tests",
// so the second test hammers one shared pool from many goroutines.

type appFixture struct {
	name       string
	source     string
	ownKey     string // a top-level var only THIS app defines
	foreignKey string // the other app's var — must never appear in this app's VM
	wantTotal  float64
	builtins   int
	maxEnergy  uint64
}

func appFixtures() []appFixture {
	return []appFixture{
		{
			name: "alpha",
			source: `
var owner = "alpha";
var alphaOnly = 7;
var total = 0;
for (let i = 0; i < 3; i++) { total = total + 1; }
`,
			ownKey:     "alphaOnly",
			foreignKey: "betaOnly",
			wantTotal:  3,
			builtins:   1,
			maxEnergy:  1_000_000,
		},
		{
			name: "beta",
			source: `
var owner = "beta";
var betaOnly = 9;
var total = 0;
for (let i = 0; i < 5; i++) { total = total + 1; }
`,
			ownKey:     "betaOnly",
			foreignKey: "alphaOnly",
			wantTotal:  5,
			builtins:   2,
			maxEnergy:  2_000_000,
		},
	}
}

func compileFixtures(t *testing.T) ([]appFixture, []*compiler.Bytecode) {
	t.Helper()
	fixtures := appFixtures()
	programs := make([]*compiler.Bytecode, len(fixtures))
	for i, f := range fixtures {
		bc, err := compiler.CompileSource(f.source)
		if err != nil {
			t.Fatalf("compile %s: %v", f.name, err)
		}
		programs[i] = bc
	}
	return fixtures, programs
}

func builtinSlice(n int) []value.Value {
	out := make([]value.Value, n)
	for i := range out {
		out[i] = value.NewFunc(func(args ...value.Value) value.Value { return value.NewNil() })
	}
	return out
}

// TestPooledVMDropsPreviousOwnerState asserts the §1 clause in full: a VM handed out by the pool
// retains NO bytecode, constants, globals, request variables, builtins, execution hooks, or gas
// configuration from its previous owner. Every VM the pool hands out must be clean, so we drain
// several at once rather than assuming sync.Pool returns the same instance.
func TestPooledVMDropsPreviousOwnerState(t *testing.T) {
	pool := app.NewPool()

	bc, err := compiler.CompileSource(`var secret = "tenant-a"; var n = 0; for (let i = 0; i < 4; i++) { n = n + 1; }`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	dirty := pool.Acquire()
	dirty.Builtins = builtinSlice(3)
	dirty.Spawner = func(*value.Lambda) {}
	dirty.FastReset(bc.Program, map[string]value.Value{"appName": value.New("tenant-a")})
	dirty.MaxEnergy = 500_000
	if res := dirty.Run(); res.K == value.Invalid {
		t.Fatalf("run fixture: %v", res.V)
	}
	if dirty.Energy == 0 {
		t.Fatal("fixture consumed no energy; the isolation check would be vacuous")
	}
	pool.Release(dirty)

	const drain = 8
	acquired := make([]*runtime.VM, 0, drain)
	for i := 0; i < drain; i++ {
		vm := pool.Acquire()
		acquired = append(acquired, vm)

		if vm.Program() != nil {
			t.Errorf("acquire %d: program retained from previous owner", i)
		}
		// A freshly-constructed VM carries an empty (non-nil) Globals map, so the invariant is
		// "no entries survive", not "the map is nil".
		if len(vm.Globals) != 0 {
			t.Errorf("acquire %d: globals retained from previous owner (%d keys)", i, len(vm.Globals))
		}
		if vm.Builtins != nil {
			t.Errorf("acquire %d: builtins retained from previous owner", i)
		}
		if vm.Spawner != nil {
			t.Errorf("acquire %d: execution hook (Spawner) retained from previous owner", i)
		}
		if vm.MaxEnergy != 0 {
			t.Errorf("acquire %d: gas configuration retained (MaxEnergy=%d)", i, vm.MaxEnergy)
		}
		if vm.Energy != 0 {
			t.Errorf("acquire %d: energy counter retained (%d)", i, vm.Energy)
		}
		if len(vm.Vars) != 0 {
			t.Errorf("acquire %d: request variables retained (%d keys)", i, len(vm.Vars))
		}
	}
	for _, vm := range acquired {
		pool.Release(vm)
	}
}

// TestPooledVMIsolationAcrossAppsConcurrent runs two apps against ONE shared pool from many
// goroutines at once. Each run must see only its own variables, globals, builtins and gas ceiling —
// never the other app's. This is the concurrent proof §1 demands.
func TestPooledVMIsolationAcrossAppsConcurrent(t *testing.T) {
	fixtures, programs := compileFixtures(t)
	pool := app.NewPool()

	const workers = 16
	const iterations = 25

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				idx := (worker + i) % len(fixtures)
				f := fixtures[idx]
				bc := programs[idx]

				vm := pool.Acquire()
				vm.Builtins = builtinSlice(f.builtins)
				vm.FastReset(bc.Program,
					map[string]value.Value{"appName": value.New(f.name)})
				vm.MaxEnergy = f.maxEnergy

				res := vm.Run()
				if res.K == value.Invalid {
					t.Errorf("%s: run failed: %v", f.name, res.V)
					pool.Release(vm)
					return
				}

				if owner := vm.Vars["owner"].String(); owner != f.name {
					t.Errorf("%s: owner leaked across apps: got %q", f.name, owner)
				}
				if total := vm.Vars["total"].N; total != f.wantTotal {
					t.Errorf("%s: total = %v, want %v", f.name, total, f.wantTotal)
				}
				if _, ok := vm.Vars[f.ownKey]; !ok {
					t.Errorf("%s: own variable %q missing", f.name, f.ownKey)
				}
				if _, ok := vm.Vars[f.foreignKey]; ok {
					t.Errorf("%s: FOREIGN variable %q visible — cross-app contamination", f.name, f.foreignKey)
				}
				if got := vm.Globals["appName"].String(); got != f.name {
					t.Errorf("%s: globals leaked across apps: appName=%q", f.name, got)
				}
				if len(vm.Builtins) != f.builtins {
					t.Errorf("%s: builtins leaked: got %d, want %d", f.name, len(vm.Builtins), f.builtins)
				}
				if vm.MaxEnergy != f.maxEnergy {
					t.Errorf("%s: gas ceiling leaked: got %d, want %d", f.name, vm.MaxEnergy, f.maxEnergy)
				}

				pool.Release(vm)
			}
		}(w)
	}
	wg.Wait()
}
