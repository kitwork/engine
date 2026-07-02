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
