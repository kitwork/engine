package runtime_test

import (
	"testing"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func allocationProgram(t *testing.T, source string) *runtime.Program {
	t.Helper()
	bytecode, err := compiler.CompileSource(source)
	if err != nil {
		t.Fatal(err)
	}
	return bytecode.Program
}

func assertAllocationBudget(
	t *testing.T,
	source string,
	maximum float64,
) {
	t.Helper()
	program := allocationProgram(t, source)
	vm := runtime.New(program)
	vm.MaxEnergy = 10_000_000

	allocations := testing.AllocsPerRun(100, func() {
		vm.FastReset(program, nil)
		benchmarkResult = vm.Run()
	})
	if benchmarkResult.K == value.Invalid {
		t.Fatal(benchmarkResult.Text())
	}
	if allocations > maximum {
		t.Fatalf(
			"allocations/run = %.2f, budget is %.2f",
			allocations,
			maximum,
		)
	}
}

func TestVMAllocationBudgets(t *testing.T) {
	t.Run("dispatch", func(t *testing.T) {
		assertAllocationBudget(t, `
var total = 0;
for (let i = 0; i < 100; i++) {
	total = total + i * 2;
}
`, 0)
	})

	t.Run("function-calls", func(t *testing.T) {
		assertAllocationBudget(t, `
const add = (left, right) => left + right;
var total = 0;
for (let i = 0; i < 100; i++) {
	total = add(total, i);
}
`, 6)
	})

	t.Run("array-callbacks", func(t *testing.T) {
		assertAllocationBudget(t, `
const items = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];
var total = items
	.map((item) => item * 2)
	.filter((item) => item > 10)
	.reduce((sum, item) => sum + item, 0);
`, 25)
	})
}
