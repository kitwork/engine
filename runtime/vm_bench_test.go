package runtime_test

import (
	"strconv"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

var benchmarkResult value.Value

func compileBenchmarkProgram(b *testing.B, source string) *runtime.Program {
	b.Helper()
	bytecode, err := compiler.CompileSource(source)
	if err != nil {
		b.Fatal(err)
	}
	return bytecode.Program
}

func benchmarkRun(b *testing.B, source string) {
	b.Helper()
	program := compileBenchmarkProgram(b, source)
	vm := runtime.New(program)
	vm.MaxEnergy = 10_000_000

	vm.FastReset(program, nil)
	benchmarkResult = vm.Run()
	if benchmarkResult.K == value.Invalid {
		b.Fatal(benchmarkResult.Text())
	}
	instructions := vm.Stats().Instructions

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.FastReset(program, nil)
		benchmarkResult = vm.Run()
		if benchmarkResult.K == value.Invalid {
			b.Fatal(benchmarkResult.Text())
		}
	}
	b.ReportMetric(float64(program.Len()), "bytecode_B")
	b.ReportMetric(float64(instructions), "instructions/op")
}

func BenchmarkVMDispatchArithmetic(b *testing.B) {
	benchmarkRun(b, `
var total = 0;
for (let i = 0; i < 100; i++) {
	total = total + i * 2;
}
`)
}

func BenchmarkVMFunctionCalls(b *testing.B) {
	benchmarkRun(b, `
const add = (left, right) => left + right;
var total = 0;
for (let i = 0; i < 100; i++) {
	total = add(total, i);
}
`)
}

func BenchmarkVMArrayCallbacks(b *testing.B) {
	benchmarkRun(b, `
const items = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16];
var total = items
	.map((item) => item * 2)
	.filter((item) => item > 10)
	.reduce((sum, item) => sum + item, 0);
`)
}

func BenchmarkVMFastReset(b *testing.B) {
	program := compileBenchmarkProgram(b, `var result = 40 + 2;`)
	vm := runtime.New(program)
	globals := map[string]value.Value{"fixture": value.New(1)}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.FastReset(program, globals)
	}
	b.ReportMetric(float64(program.Len()), "bytecode_B")
}

func BenchmarkVMPoolAcquireRelease(b *testing.B) {
	pool := app.NewPool()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := pool.Acquire()
		pool.Release(vm)
	}
}

func BenchmarkVMExceptionalStateRelease(b *testing.B) {
	program := compileBenchmarkProgram(b, `var result = 40 + 2;`)
	vm := runtime.New(program)
	payload := value.NewString("previous-request")
	deferred := &value.Lambda{Address: 0, Program: program}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		vm.Stack = make([]value.Value, 32_000)
		for index := 0; index < 1_500; index++ {
			vm.Vars["retained_"+strconv.Itoa(index)] = payload
		}
		for index := range vm.Frames {
			vm.Frames[index].Vars = map[string]value.Value{"payload": payload}
			vm.Frames[index].Defers = make([]*value.Lambda, 128)
			for deferIndex := range vm.Frames[index].Defers {
				vm.Frames[index].Defers[deferIndex] = deferred
			}
		}
		vm.ResetForPool()
	}
	b.ReportMetric(32_000*24, "exceptional_stack_B")
}
