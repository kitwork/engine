package runtime_test

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func oversizedVariablesSource(variables int) string {
	var source strings.Builder
	source.Grow(variables * 24)
	for index := 0; index < variables; index++ {
		source.WriteString("var retained_")
		source.WriteString(strconv.Itoa(index))
		source.WriteString(" = payload;\n")
	}
	source.WriteString("var result = retained_0;\n")
	return source.String()
}

func oversizedStackProgram(t testing.TB, values int) *runtime.Program {
	t.Helper()
	code := make([]byte, 0, values*4)
	for index := 0; index < values; index++ {
		code = append(code, byte(runtime.PUSH), 0, 0)
	}
	for index := 1; index < values; index++ {
		code = append(code, byte(runtime.POP))
	}
	code = append(code, byte(runtime.RETURN))

	program, err := runtime.NewProgram(
		code,
		[]value.Value{value.New(42)},
		nil,
	)
	if err != nil {
		t.Fatalf("build oversized stack Program: %v", err)
	}
	return program
}

func TestPooledVMReleasesOversizedVerifiedWorkload(t *testing.T) {
	const (
		stackValues = 5_000
		variables   = 1_100
		stackLimit  = 4_096
	)
	stackProgram := oversizedStackProgram(t, stackValues)
	variableProgram, err := compiler.CompileSource(
		oversizedVariablesSource(variables),
	)
	if err != nil {
		t.Fatalf("compile oversized variable workload: %v", err)
	}
	small, err := compiler.CompileSource(`var result = 40 + 2;`)
	if err != nil {
		t.Fatalf("compile small workload: %v", err)
	}
	smallBaseline := runFingerprint(
		runtime.New(small.Program),
		small.Program,
		nil,
		nil,
		100_000,
	)

	iterations := 10
	if os.Getenv("KITWORK_SOAK") == "1" {
		iterations = 250
	}

	pool := app.NewPool()
	for iteration := 0; iteration < iterations; iteration++ {
		vm := pool.Acquire()
		vm.FastReset(stackProgram, nil)
		vm.MaxEnergy = 10_000_000
		result := vm.Run()
		if result.K == value.Invalid {
			pool.Release(vm)
			t.Fatalf("iteration %d: %s", iteration, result.Text())
		}
		if got := result.Int(); got != 42 {
			pool.Release(vm)
			t.Fatalf("iteration %d: result=%d", iteration, got)
		}
		if capacity := vm.Stats().StackCapacity; capacity <= stackLimit {
			pool.Release(vm)
			t.Fatalf(
				"iteration %d: workload did not exceed pooled stack limit: %d",
				iteration,
				capacity,
			)
		}
		pool.Release(vm)
		if vm.Program() != nil ||
			len(vm.Vars) != 0 ||
			vm.Stats().StackCapacity > stackLimit {
			t.Fatalf("iteration %d: pool retained oversized VM state", iteration)
		}

		vm = pool.Acquire()
		vm.FastReset(
			variableProgram.Program,
			map[string]value.Value{"payload": value.NewString("previous-request")},
		)
		vm.MaxEnergy = 10_000_000
		result = vm.Run()
		if result.K == value.Invalid {
			pool.Release(vm)
			t.Fatalf("iteration %d: %s", iteration, result.Text())
		}
		if count := len(vm.Vars); count <= variables {
			pool.Release(vm)
			t.Fatalf(
				"iteration %d: workload did not create enough variables: %d",
				iteration,
				count,
			)
		}
		pool.Release(vm)
		if vm.Program() != nil || len(vm.Vars) != 0 {
			t.Fatalf("iteration %d: pool retained oversized variable state", iteration)
		}

		vm = pool.Acquire()
		fingerprint := runFingerprint(vm, small.Program, nil, nil, 100_000)
		assertFingerprintEqual(t, smallBaseline, fingerprint)
		pool.Release(vm)
	}

	if active := pool.Active(); active != 0 {
		t.Fatalf("pool retained %d active VM leases", active)
	}
}
