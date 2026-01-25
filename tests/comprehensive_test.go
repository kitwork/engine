package tests

import (
	"testing"

	"github.com/kitwork/engine"
)

func TestComprehensiveOperators(t *testing.T) {
	// 1. Arithmetic Operators
	t.Run("Arithmetic", func(t *testing.T) {
		cases := []struct {
			name   string
			script string
			expect any
		}{
			{"Addition", "10 + 20", float64(30)},
			{"Subtraction", "50 - 20", float64(30)},
			{"Multiplication", "5 * 6", float64(30)},
			{"Division", "100 / 4", float64(25)},
			{"Complex Math", "(10 + 5) * 2 / 3", float64(10)},
			{"Negative Numbers", "-10 + 5", float64(-5)},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				res := engine.Script(tc.script)
				if res.Error() != nil {
					t.Fatalf("Script error: %v", res.Error())
				}
				if res.Value() != tc.expect {
					t.Errorf("Expected %v, got %v", tc.expect, res.Value())
				}
			})
		}
	})

	// 2. Comparison Operators
	t.Run("Comparison", func(t *testing.T) {
		cases := []struct {
			name   string
			script string
			expect bool
		}{
			{"Equal", "10 == 10", true},
			{"Not Equal", "10 != 5", true},
			{"Greater Than", "20 > 10", true},
			{"Less Than", "5 < 10", true},
			{"Greater Equal", "10 >= 10", true},
			{"Less Equal", "5 <= 10", true},
			{"String Equal", "'abc' == 'abc'", true},
			{"Boolean Equal", "true == true", true},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				res := engine.Script(tc.script)
				if res.Error() != nil {
					t.Fatalf("Script error: %v", res.Error())
				}
				if res.Value().(bool) != tc.expect {
					t.Errorf("Expected %v, got %v", tc.expect, res.Value())
				}
			})
		}
	})

	// 3. Logical Operators
	t.Run("Logical", func(t *testing.T) {
		cases := []struct {
			name   string
			script string
			expect bool
		}{
			{"AND True", "true && true", true},
			{"AND False", "true && false", false},
			{"OR True", "true || false", true},
			{"OR False", "false || false", false},
			{"NOT True", "!true", false},
			{"NOT False", "!false", true},
			{"Complex Logic", "(true && false) || (true && !false)", true},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				res := engine.Script(tc.script)
				if res.Error() != nil {
					t.Fatalf("Script error: %v", res.Error())
				}
				if res.Value().(bool) != tc.expect {
					t.Errorf("Expected %v, got %v", tc.expect, res.Value())
				}
			})
		}
	})
}

func TestComprehensiveDataStructures(t *testing.T) {
	// 1. Arrays
	t.Run("Arrays", func(t *testing.T) {
		script := `
			let a = [1, 2, 3];
			a.push(4);
			a.length;
		`
		res := engine.Script(script)
		if res.Value().(float64) != 4 {
			t.Errorf("Expected length 4, got %v", res.Value())
		}
	})

	// 2. Objects/Maps
	t.Run("Objects", func(t *testing.T) {
		script := `
			let user = { name: "Bob", age: 25 };
			user.age = 26;
			user.age;
		`
		res := engine.Script(script)
		if res.Value().(float64) != 26 {
			t.Errorf("Expected age 26, got %v", res.Value())
		}
	})
}

func TestComprehensiveControlFlow(t *testing.T) {
	// 1. If-Else
	t.Run("If-Else", func(t *testing.T) {
		script := `
			let x = 10;
			let res = "";
			if (x > 5) {
				res = "greater";
			} else {
				res = "smaller";
			}
			res;
		`
		res := engine.Script(script)
		if res.Value().(string) != "greater" {
			t.Errorf("Expected 'greater', got %v", res.Value())
		}
	})

	// 2. Loops
	t.Run("Each Loop", func(t *testing.T) {
		script := `
			let total = 0;
			let nums = [1, 2, 3, 4, 5];
			nums.each((i) => {
				total = total + i;
			});
			total;
		`
		res := engine.Script(script)
		if res.Value().(float64) != 15 {
			t.Errorf("Expected 15, got %v", res.Value())
		}
	})
}

func TestComprehensiveFunctions(t *testing.T) {
	// 1. Basic Functions
	t.Run("Functions", func(t *testing.T) {
		script := `
			let add = (a, b) => {
				return a + b;
			};
			add(5, 10);
		`
		res := engine.Script(script)
		if res.Value().(float64) != 15 {
			t.Errorf("Expected 15, got %v", res.Value())
		}
	})

	// 2. Closures
	t.Run("Closures", func(t *testing.T) {
		script := `
			let counter = () => {
				let count = 0;
				return () => {
					count = count + 1;
					return count;
				};
			};
			let c = counter();
			c();
			c();
		`
		res := engine.Script(script)
		if res.Value().(float64) != 2 {
			t.Errorf("Expected 2, got %v", res.Value())
		}
	})

	// 3. Recursion
	t.Run("Recursion", func(t *testing.T) {
		script := `
			let fact = (n) => {
				if (n <= 1) return 1;
				return n * fact(n - 1);
			};
			fact(5);
		`
		res := engine.Script(script)
		if res.Value().(float64) != 120 {
			t.Errorf("Expected 120, got %v", res.Value())
		}
	})
}

func TestComprehensiveTruthiness(t *testing.T) {
	cases := []struct {
		name   string
		script string
		expect bool
	}{
		{"Number 1", "if (1) { true } else { false }", true},
		{"Number 0", "if (0) { true } else { false }", false},
		{"String Not Empty", "if ('hi') { true } else { false }", true},
		{"String Empty", "if ('') { true } else { false }", false}, // Engine now treats empty string as falsy
		{"Null", "if (null) { true } else { false }", false},
		{"Array", "if ([]) { true } else { false }", true},
		{"Object", "if ({}) { true } else { false }", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := engine.Script(tc.script)
			if res.Value().(bool) != tc.expect {
				t.Errorf("Expected %v, got %v for %s", tc.expect, res.Value(), tc.script)
			}
		})
	}
}
