package compiler

import (
	"context"
	"fmt"
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

var callbackMethods = []string{
	"map",
	"filter",
	"find",
	"forEach",
	"some",
	"every",
	"findIndex",
	"reduce",
	"sort",
	"group",
	"groupBy",
	"sortBy",
	"unique",
}

func callbackInvocation(method, callback string) string {
	if method == "reduce" {
		return "[2, 1, 2].reduce(" + callback + ", 0)"
	}
	return "[2, 1, 2]." + method + "(" + callback + ")"
}

func runCallbackSource(t *testing.T, source string) (*runtime.VM, value.Value) {
	t.Helper()
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile source: %v", err)
	}
	vm := runtime.New(bytecode.Program)
	return vm, vm.Run()
}

func TestArrayCallbackConformance(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"map", `const result = [1, 2, 3].map((item) => item * 2)`, `[2, 4, 6]`},
		{"filter", `const result = [1, 2, 3].filter((item) => item > 1)`, `[2, 3]`},
		{"find", `const result = [1, 2, 3].find((item) => item > 1)`, `2`},
		{"forEach", `let total = 0; [1, 2, 3].forEach((item) => { total = total + item; }); const result = total`, `6`},
		{"some", `const result = [1, 2, 3].some((item) => item == 2)`, `true`},
		{"every", `const result = [1, 2, 3].every((item) => item > 0)`, `true`},
		{"findIndex", `const result = [1, 2, 3].findIndex((item) => item == 2)`, `1`},
		{"reduce", `const result = [1, 2, 3].reduce((total, item) => total + item, 0)`, `6`},
		{"sort", `const result = [1, 3, 2].sort((a, b) => b - a)`, `[3, 2, 1]`},
		{"sortBy", `const result = [1, 3, 2].sortBy((item) => 0 - item)`, `[3, 2, 1]`},
		{"unique", `const result = [1, 2, 1, 3, 2].unique((item) => item)`, `[1, 2, 3]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vm, result := runCallbackSource(t, test.source)
			if result.K == value.Invalid {
				t.Fatalf("execution failed: %s", result.Text())
			}
			if got := vm.Vars["result"].Text(); got != test.want {
				t.Fatalf("result = %q, want %q", got, test.want)
			}
		})
	}

	for _, method := range []string{"group", "groupBy"} {
		t.Run(method, func(t *testing.T) {
			vm, result := runCallbackSource(
				t,
				"const result = "+callbackInvocation(method, "(item) => item % 2"),
			)
			if result.K == value.Invalid {
				t.Fatalf("execution failed: %s", result.Text())
			}
			grouped := vm.Vars["result"].Map()
			if got := grouped["0"].Text(); got != "[2, 2]" {
				t.Fatalf("even group = %q, want [2, 2]", got)
			}
			if got := grouped["1"].Text(); got != "[1]" {
				t.Fatalf("odd group = %q, want [1]", got)
			}
		})
	}
}

func TestArrayCallbacksPropagateFailures(t *testing.T) {
	for _, method := range callbackMethods {
		t.Run(method+"/invalid", func(t *testing.T) {
			source := "const result = " + callbackInvocation(method, "() => failHost()")
			bytecode, err := CompileSource(source)
			if err != nil {
				t.Fatalf("compile source: %v", err)
			}
			vm := runtime.New(bytecode.Program)
			vm.Globals["failHost"] = value.NewFunc(func(...value.Value) value.Value {
				return value.Value{K: value.Invalid, V: "callback failed"}
			})

			result := vm.Run()
			diagnostic, ok := runtime.DiagnosticFrom(result)
			if !ok || diagnostic.Code != runtime.DiagnosticRuntimeError {
				t.Fatalf("result = %#v", result)
			}
		})

		t.Run(method+"/panic", func(t *testing.T) {
			source := "const result = " + callbackInvocation(method, "() => explode()")
			bytecode, err := CompileSource(source)
			if err != nil {
				t.Fatalf("compile source: %v", err)
			}
			vm := runtime.New(bytecode.Program)
			vm.Globals["explode"] = value.NewFunc(func(...value.Value) value.Value {
				panic("callback exploded")
			})

			result := vm.Run()
			diagnostic, ok := runtime.DiagnosticFrom(result)
			if !ok || diagnostic.Code != runtime.DiagnosticNativePanic {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestArrayCallbacksRespectEnergyAndCancellation(t *testing.T) {
	const callback = `() => {
		let total = 0;
		for (let i = 0; i < 10000; i++) {
			total = total + i;
		}
		return total;
	}`

	for _, method := range callbackMethods {
		source := fmt.Sprintf(
			"const result = %s",
			callbackInvocation(method, callback),
		)

		t.Run(method+"/energy", func(t *testing.T) {
			bytecode, err := CompileSource(source)
			if err != nil {
				t.Fatalf("compile source: %v", err)
			}
			vm := runtime.New(bytecode.Program)
			vm.MaxEnergy = 1_000

			result := vm.Run()
			diagnostic, ok := runtime.DiagnosticFrom(result)
			if !ok || diagnostic.Code != runtime.DiagnosticEnergyLimit {
				t.Fatalf("result = %#v", result)
			}
		})

		t.Run(method+"/cancel", func(t *testing.T) {
			bytecode, err := CompileSource(source)
			if err != nil {
				t.Fatalf("compile source: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			vm := runtime.New(bytecode.Program)
			vm.Context = ctx

			result := vm.Run()
			diagnostic, ok := runtime.DiagnosticFrom(result)
			if !ok || diagnostic.Code != runtime.DiagnosticCancelled {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}
