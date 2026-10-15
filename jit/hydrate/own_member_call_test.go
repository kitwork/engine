package hydrate

import "testing"

// A call through the dot reaches an object's own key when the key holds a function, as in the VM
// (value.CallsOwn, 13/10/2026): a helper the host put in an object (a Func, the shape a router.view
// lambda takes in a .ui scope) is called, and so is a lambda value. An own key holding data never
// answers a call: an object has no methods in this evaluator, so the call is nil, as without the key.
func TestOwnFunctionMemberIsCalledThroughTheDot(t *testing.T) {
	scope := map[string]any{
		"fns":  map[string]any{"has": Func(func(args []any) any { return num(args[0]) + 1 })},
		"data": map[string]any{"has": true, "keys": []any{"x"}},
	}
	cases := []struct {
		src  string
		want any
	}{
		{"fns.has(1)", float64(2)},
		{"fns['has'](1)", float64(2)},
		{"data.has('a') ?? 'none'", "none"},
		{"data.has", true},
	}
	for _, c := range cases {
		ir, err := Compile(c.src)
		if err != nil {
			t.Fatalf("compile %q: %v", c.src, err)
		}
		got, err := Eval(ir, scope)
		if err != nil {
			t.Fatalf("eval %q: %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("%s = %#v, want %#v", c.src, got, c.want)
		}
	}
}
