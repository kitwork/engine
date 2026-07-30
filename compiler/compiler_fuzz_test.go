package compiler

import (
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func FuzzCompiler(f *testing.F) {
	seeds := []string{
		`const x = 10;`,
		`let f = (a, b) => a + b;`,
		`import { router } from "kitwork"; router.get("/test", ctx => ctx.text("ok"));`,
		`var obj = { name: "test", count: 123 };`,
		`..`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		_, _ = CompileSource(input)
	})
}

// FuzzCompileVerifyExecute covers the complete untrusted-source pipeline.
// Accepted source must publish a verified immutable Program, execute within a
// finite energy budget, and expose every runtime failure as a Diagnostic.
func FuzzCompileVerifyExecute(f *testing.F) {
	seeds := []string{
		`const result = [1, 2, 3].map((item) => item * 2);`,
		`const result = [3, 1, 2].sort((a, b) => a - b);`,
		`const add = (a, b) => a + b; const result = add(2, 3);`,
		`let total = 0; for (let i = 0; i < 10; i++) { total = total + i; }`,
		`const nested = { value: { count: 3 } }; const result = nested.value.count;`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 32*1024 {
			t.Skip()
		}

		bytecode, err := CompileSource(input)
		if err != nil {
			return
		}
		if bytecode.Program == nil {
			t.Fatal("compiler accepted source without publishing a Program")
		}
		if err := runtime.Verify(
			bytecode.Program.Instructions(),
			bytecode.Program.Constants(),
		); err != nil {
			t.Fatalf("published Program failed verification: %v", err)
		}

		vm := runtime.New(bytecode.Program)
		vm.MaxEnergy = 20_000
		result := vm.Run()
		if result.K == value.Invalid {
			if _, ok := runtime.DiagnosticFrom(result); !ok {
				t.Fatalf("runtime returned an unstructured failure: %#v", result)
			}
		}
	})
}
