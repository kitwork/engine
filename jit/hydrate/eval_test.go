package hydrate

import (
	"encoding/json"
	"testing"
)

// compileEval compiles an authored expression and evaluates it against scope — the exact path the
// SERVER takes when it re-checks a rule the client already checked with the same IR.
func compileEval(t *testing.T, expr string, scope map[string]any) any {
	t.Helper()
	node, err := Compile(expr)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
	v, err := Eval(node, scope)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expr, err)
	}
	return v
}

func TestEvalBasics(t *testing.T) {
	cases := []struct {
		expr  string
		scope map[string]any
		want  any
	}{
		{"n * qty", map[string]any{"n": 3.0, "qty": 2.0}, 6.0},
		{"n > 3", map[string]any{"n": 5.0}, true},
		{"n > 3", map[string]any{"n": 2.0}, false},
		{"n + 1", map[string]any{}, 1.0}, // missing key reads as 0, like the client Proxy
		{"'Chào ' + name", map[string]any{"name": "Quốc"}, "Chào Quốc"},
		{"name ? 'Chào ' + name : 'Nhập tên'", map[string]any{"name": ""}, "Nhập tên"},
		{"!open", map[string]any{"open": false}, true},
		{"-n", map[string]any{"n": 4.0}, -4.0},
		{"(n * qty).toFixed(2)", map[string]any{"n": 1.5, "qty": 2.0}, "3.00"},
		{"email.includes('@')", map[string]any{"email": "a@b.vn"}, true},
		{"email.includes('@')", map[string]any{"email": "khong-co"}, false},
		{"password.length", map[string]any{"password": "chào1"}, 5.0}, // runes, not bytes
	}
	for _, c := range cases {
		if got := compileEval(t, c.expr, c.scope); got != c.want {
			t.Errorf("Eval(%q) = %#v, want %#v", c.expr, got, c.want)
		}
	}
}

// $ addresses the page scope. On the server the scope IS the page scope (flat), so $.n and n read
// the same map — the verdicts stay identical to a client whose page scope holds the same data.
func TestEvalPageScopeDollar(t *testing.T) {
	scope := map[string]any{"total": 1.0, "n": 2.0}
	if got := compileEval(t, "$.total", scope); got != 1.0 {
		t.Errorf("$.total = %#v, want 1", got)
	}
	if got := compileEval(t, "$.total = $.total + n", scope); got != 3.0 {
		t.Errorf("$.total assign = %#v, want 3", got)
	}
	if scope["total"] != 3.0 {
		t.Errorf("scope not updated: %#v", scope["total"])
	}
	// a missing page-scope key reads as undefined/nil — any comparison against it is false,
	// the same verdict the client walker reaches (2 > undefined → false).
	if got := compileEval(t, "n > $.limit", scope); got != false {
		t.Errorf("n > $.limit = %#v, want false (limit missing)", got)
	}
}

// Blueprint values: a lambda is code-as-data walked by the same budgeted walker — no eval, no loops.
func TestEvalBlueprint(t *testing.T) {
	// object + array literals evaluate their values
	if v := compileEval(t, "{ a: 1 + 1, b: 'x' }", map[string]any{}); v.(map[string]any)["a"] != 2.0 {
		t.Errorf("object literal: %#v", v)
	}
	if v := compileEval(t, "[qty, 2]", map[string]any{"qty": 3.0}); v.([]any)[0] != 3.0 {
		t.Errorf("array literal: %#v", v)
	}
	// a bare-called lambda mutates the surrounding scope lexically
	scope := map[string]any{"n": 0.0}
	if v := compileEval(t, "inc = () => n = n + 1; inc(); inc(); n", scope); v != 2.0 {
		t.Errorf("lambda call: %#v", v)
	}
	if scope["n"] != 2.0 {
		t.Errorf("lambda did not write through: %#v", scope["n"])
	}
	// params overlay locally and never leak; non-param writes flow out
	s2 := map[string]any{"total": 1.0}
	if v := compileEval(t, "f = (x) => total = total + x; f(5); total", s2); v != 6.0 {
		t.Errorf("param lambda: %#v", v)
	}
	if _, leaked := s2["x"]; leaked {
		t.Error("param x must not leak into the caller scope")
	}
	// calling a non-lambda yields nil (parity with the client returning undefined)
	if v := compileEval(t, "notafn()", map[string]any{"notafn": 5.0}); v != nil {
		t.Errorf("calling a non-lambda should be nil, got %#v", v)
	}
}

// Lambdas cannot loop, so the only runaway is recursion — the op budget stops it (never a crash).
func TestEvalLambdaRecursionBudget(t *testing.T) {
	node, err := Compile("f = () => f(); f()")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Eval(node, map[string]any{}); err == nil {
		t.Error("expected budget error on infinite recursion")
	}
}

// Sandbox: the walker must never reach the Function constructor (i.e. eval) or prototype pollution,
// so constructor/__proto__/prototype resolve to nil in reads, calls and writes. This is what makes
// "no eval" true by construction — essential before any client-sent expression (capsule) runs.
func TestEvalSandboxBlocksConstructor(t *testing.T) {
	cases := []string{
		"''.constructor",                          // → String, must be nil
		"''.constructor.constructor",              // → Function, must be nil
		"''.constructor.constructor('x')",         // building Function('x'), must be nil
		"x.__proto__",                             // prototype access
		"x.prototype",                             // prototype access
	}
	for _, expr := range cases {
		v := compileEval(t, expr, map[string]any{"x": map[string]any{}})
		if v != nil {
			t.Errorf("sandbox breach: %q returned %#v, want nil", expr, v)
		}
	}
	// a write to a blocked target is a no-op (no prototype pollution)
	scope := map[string]any{}
	compileEval(t, "__proto__ = 1", scope)
	if _, leaked := scope["__proto__"]; leaked {
		t.Error("write to __proto__ must be blocked")
	}
	// the legitimate method set still works (regression guard)
	if compileEval(t, "'a@b'.includes('@')", map[string]any{}) != true {
		t.Error("legit method call broke")
	}
}

func TestEvalAssignment(t *testing.T) {
	scope := map[string]any{"n": 1.0}
	if got := compileEval(t, "n = n + 1", scope); got != 2.0 {
		t.Errorf("assignment returned %#v, want 2", got)
	}
	if scope["n"] != 2.0 {
		t.Errorf("scope not updated: %#v", scope["n"])
	}
}

// The isomorphic-validation guarantee: ONE authored rule, compiled once, must produce the same
// verdict the client walker produces — this is what lets the server trust "re-check the same IR".
func TestEvalIsomorphicValidate(t *testing.T) {
	rule := "password.length >= 6 && confirm == password && email.includes('@')"
	node, err := Compile(rule)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		password, confirm, email string
		want                     bool
	}{
		{"secret1", "secret1", "a@b.vn", true},
		{"abc", "abc", "a@b.vn", false},          // too short
		{"secret1", "secret2", "a@b.vn", false},  // mismatch
		{"secret1", "secret1", "khong-co", false}, // no @
		{"", "", "", false},                       // untouched form
	}
	for _, c := range cases {
		got, err := Eval(node, map[string]any{"password": c.password, "confirm": c.confirm, "email": c.email})
		if err != nil {
			t.Fatal(err)
		}
		if truthy(got) != c.want {
			t.Errorf("validate(%q,%q,%q) = %v, want %v", c.password, c.confirm, c.email, truthy(got), c.want)
		}
	}
}

// The wire round-trip: the page carries IR as JSON text; the server can pick that exact text back
// up and evaluate it (what a form-submit re-check does).
func TestEvalWireIR(t *testing.T) {
	js, err := CompileJSON("n * qty")
	if err != nil {
		t.Fatal(err)
	}
	var node any
	if err := json.Unmarshal([]byte(js), &node); err != nil {
		t.Fatal(err)
	}
	v, err := Eval(node, map[string]any{"n": 3.0, "qty": 2.0})
	if err != nil {
		t.Fatal(err)
	}
	if v != 6.0 {
		t.Errorf("wire IR eval = %#v, want 6", v)
	}
}

// The budget is the gas pedal: a hostile/pathological IR stops with an error instead of spinning.
func TestEvalBudget(t *testing.T) {
	x := any([]any{"#", 1.0})
	for i := 0; i < evalBudget+10; i++ {
		x = []any{"u!", x}
	}
	if _, err := Eval(x, map[string]any{}); err == nil {
		t.Error("expected budget error, got none")
	}
}
