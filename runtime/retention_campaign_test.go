package runtime_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	kitruntime "github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

type retentionCampaignContextKey struct{}

type retentionCampaignPayload struct {
	Marker string
	Bytes  []byte
}

func TestPooledVMReleasesClosureHeavyPrograms(t *testing.T) {
	sources := []struct {
		name string
		body string
		want string
	}{
		{
			name: "closure-factory",
			body: `
const retained = payload;
const make = (base) => (number) => base + number;
const functions = [1, 2, 3, 4].map((base) => make(base));
var result = functions.map((fn) => fn(10)).join(",");
`,
			want: "11,12,13,14",
		},
		{
			name: "nested-closure-chain",
			body: `
const retained = payload;
const build = (a) => (b) => (c) => a + b + c;
var result = build(10)(20)(12);
`,
			want: "42",
		},
		{
			name: "array-callback-chain",
			body: `
const retained = payload;
const values = [1, 2, 3, 4, 5, 6];
var result = values.filter((item) => item > 2).map((item) => item * 2).reduce((sum, item) => sum + item, 0);
`,
			want: "36",
		},
	}

	programs := make([]*compiler.Bytecode, len(sources))
	for index, fixture := range sources {
		compiled, err := compiler.CompileSource(fixture.body)
		if err != nil {
			t.Fatalf("compile %s: %v", fixture.name, err)
		}
		programs[index] = compiled
	}

	pool := app.NewPool()
	const rounds = 96
	for round := 0; round < rounds; round++ {
		fixtureIndex := round % len(sources)
		fixture := sources[fixtureIndex]
		program := programs[fixtureIndex].Program
		payload := &retentionCampaignPayload{
			Marker: fmt.Sprintf("request-%d", round),
			Bytes:  []byte(strings.Repeat("x", 256<<10)),
		}
		hostValue := value.New(payload)

		vm := pool.Acquire()
		vm.Context = context.WithValue(
			context.Background(),
			retentionCampaignContextKey{},
			payload,
		)
		vm.PrepareHostState(
			map[string]value.Value{"payload": hostValue},
			[]value.Value{hostValue},
		)
		vm.FastResetPrepared(program)
		vm.MaxEnergy = 1_000_000

		result := vm.Run()
		if result.K == value.Invalid {
			pool.Release(vm)
			t.Fatalf("round %d (%s): %s", round, fixture.name, result.Text())
		}
		if got := vm.Vars["result"].String(); got != fixture.want {
			pool.Release(vm)
			t.Fatalf(
				"round %d (%s): result=%q, want %q",
				round,
				fixture.name,
				got,
				fixture.want,
			)
		}

		pool.Release(vm)
		assertReleasedVMOwners(t, round, vm)
	}

	if active := pool.Active(); active != 0 {
		t.Fatalf("pool retained %d active VM leases", active)
	}
}

func assertReleasedVMOwners(t testing.TB, round int, vm *kitruntime.VM) {
	t.Helper()
	if vm.Program() != nil {
		t.Fatalf("round %d: pooled VM retained its Program", round)
	}
	if vm.Context != nil || vm.Globals != nil || vm.Builtins != nil || vm.Spawner != nil {
		t.Fatalf("round %d: pooled VM retained request or host ownership", round)
	}
	if len(vm.Stack) != 0 || len(vm.Vars) != 0 {
		t.Fatalf("round %d: pooled VM retained stack or root variables", round)
	}
	for index, item := range vm.Stack[:cap(vm.Stack)] {
		if item.K != value.Invalid || item.N != 0 || item.V != nil ||
			item.IsError || item.ErrorVal != nil || item.Raw {
			t.Fatalf("round %d: pooled VM stack storage retained slot %d", round, index)
		}
	}
	if vm.FrameIdx != 0 {
		t.Fatalf("round %d: pooled VM frame index=%d, want 0", round, vm.FrameIdx)
	}
	for index := range vm.Frames {
		frame := &vm.Frames[index]
		if frame.Fn != nil || len(frame.Vars) != 0 || len(frame.Defers) != 0 {
			t.Fatalf("round %d: frame %d retained execution owners", round, index)
		}
	}
}
