package compiler

import "testing"

// ExecuteLambda runs on the shared vm.Stack; a forEach-using function called mid-
// expression must not let its callbacks pop the caller's pending value.
func TestExecuteLambdaStackIsolation(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want float64
	}{
		{"two_foreach_funcs_in_expr",
			`const f = () => { let s = 0; [1,2].forEach((n) => { s = s + n; }); return s; };
const g = () => { let s = 0; [10].forEach((n) => { s = s + n; }); return s; };
const result = f() * 100 + g();`, 310},
		{"foreach_func_first_arg",
			`const sum = (a) => { let s = 0; a.forEach((n) => { s = s + n; }); return s; };
const result = sum([1,2,3]) * 1000 + sum([4,5]) * 10 + sum([6]);`, 6096},
		{"three_bags_object_methods",
			`const makeBag = () => { const items = []; const push = (v) => { items.push(v); return items.length; }; const sum = () => { let s = 0; items.forEach((it) => { s = s + it; }); return s; }; return ({ push: push, sum: sum }); };
const a = makeBag(); const b = makeBag(); const c = makeBag();
a.push(1); a.push(2); b.push(10); c.push(100); c.push(200);
const result = a.sum() + b.sum() + c.sum();`, 313},
	}
	for _, c := range cases {
		got, err := tryRun(c.src)
		if err != nil {
			t.Errorf("%-28s ERROR: %v", c.name, err)
		} else if got.K != 2 /*Number*/ && got.N != c.want {
			t.Errorf("%-28s got %v (kind %v) want %v", c.name, got.N, got.K, c.want)
		} else if got.N != c.want {
			t.Errorf("%-28s got %v want %v", c.name, got.N, c.want)
		} else {
			t.Logf("%-28s OK = %v", c.name, got.N)
		}
	}
}
