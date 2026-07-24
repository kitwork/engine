package compiler

import (
	"testing"
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
