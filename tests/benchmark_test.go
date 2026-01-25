package tests

import (
	"os"
	"testing"

	"github.com/kitwork/engine"
)

// BenchmarkEngine performance using the new high-level API
func TestGlobalBenchmarks(t *testing.T) {
	// 1. Simple expression
	t.Run("Simple Expression", func(t *testing.T) {
		engine.Test("1 + 1", 100_000)
	})

	// 2. Object & Array manipulation
	t.Run("Data Manipulation", func(t *testing.T) {
		source := `
			let arr = [1, 2, 3, 4, 5];
			let obj = { a: 1, b: 2 };
			arr.push(obj.a + obj.b);
			arr;
		`
		engine.Test(source, 100_000)
	})

	// 3. Real world demo script (if exists)
	if _, err := os.Stat("demo/api/shorthand.js"); err == nil {
		t.Run("Shorthand API Logic", func(t *testing.T) {
			content, _ := os.ReadFile("demo/api/shorthand.js")
			engine.Test(string(content), 10_000)
		})
	}
}

// Giữ lại Go Benchmarks chuẩn cho việc tích hợp CI/CD
func BenchmarkCoreScript(b *testing.B) {
	source := "1 + 1"
	for i := 0; i < b.N; i++ {
		_ = engine.Script(source)
	}
}
