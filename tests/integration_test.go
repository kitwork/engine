package tests

import (
	"fmt"
	"testing"

	"github.com/kitwork/engine"
)

// --- 1. CORE FUNCTIONAL TESTS ---

func TestCoreLogic(t *testing.T) {
	t.Run("Basic Math & Variable", func(t *testing.T) {
		res := engine.Script(`
			let a = 100;
			let b = 200;
			a + b;
		`)
		if res.Error() != nil || res.Value().(float64) != 300 {
			t.Errorf("Expected 300, got %v (err: %v)", res.Value(), res.Error())
		}
	})

	t.Run("String Manipulation", func(t *testing.T) {
		res := engine.Script(`
			let name = "Kitwork";
			"Hello " + name;
		`)
		if res.Error() != nil || res.Value().(string) != "Hello Kitwork" {
			t.Errorf("Expected 'Hello Kitwork', got %v", res.Value())
		}
	})

	t.Run("Query & Params Access", func(t *testing.T) {
		// Code here needs imports or use high-level API
	})
}

// --- 2. ADVANCED FEATURES ---

func TestAdvancedFeatures(t *testing.T) {
	t.Run("HTML Rendering DX", func(t *testing.T) {
		res := engine.Script(`
			let template = "<h1>Hello {{name}}</h1>";
			template.render({ name: "User" });
		`)
		if res.Error() != nil {
			t.Fatalf("Script failed: %v", res.Error())
		}
		fmt.Printf("HTML Result: %v\n", res.Value())
	})
}

// --- 3. PERFORMANCE & STRESS TESTS ---

func TestEnginePerformance(t *testing.T) {
	// Sử dụng hàm engine.Test mới để báo cáo đầy đủ thông tin hiệu năng
	source := "let price = 100; let tax = 0.1; price * (1 + tax)"

	t.Log("Running Stress Test with 100,000 iterations...")
	engine.Test(source, 1_000_000)
}

func TestComplexWorkflowStress(t *testing.T) {
	source := `
		let calculate = (n) => {
			if (n <= 1) return 1;
			return n * calculate(n - 1);
		};
		calculate(10);
	`
	t.Log("Running Recursive Stress Test...")
	engine.Test(source, 50_000)
}
