package hydrate

import "testing"

// The compiled IR must match, byte-for-byte, what the client interpreter (hydrate.v1.js) expects.
func TestCompileJSON(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"n", `["$","n"]`},
		{"n = n + 1", `["=","n",["+",["$","n"],["#",1]]]`},
		{"n = n - 1", `["=","n",["-",["$","n"],["#",1]]]`},
		{"n > 3", `[">",["$","n"],["#",3]]`},
		{"n * qty", `["*",["$","n"],["$","qty"]]`},
		{"(n * qty).toFixed(2)", `["()",["*",["$","n"],["$","qty"]],"toFixed",[["#",2]]]`},
		{"!open", `["u!",["$","open"]]`},
		{"a && b || c", `["||",["&&",["$","a"],["$","b"]],["$","c"]]`},
		// UTF-8 (Vietnamese + emoji) must survive verbatim, operators must stay literal (no HTML escaping):
		{"name ? 'Chào ' + name + ' 👋' : 'x'", `["?",["$","name"],["+",["+",["#","Chào "],["$","name"]],["#"," 👋"]],["#","x"]]`},
	}
	for _, c := range cases {
		got, err := CompileJSON(c.expr)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.expr, err)
		}
		if got != c.want {
			t.Errorf("Compile(%q)\n got:  %s\n want: %s", c.expr, got, c.want)
		}
	}
}

func TestCompilePrecedence(t *testing.T) {
	// 1 + 2 * 3  →  1 + (2*3), not (1+2)*3
	got, err := CompileJSON("1 + 2 * 3")
	if err != nil {
		t.Fatal(err)
	}
	if want := `["+",["#",1],["*",["#",2],["#",3]]]`; got != want {
		t.Errorf("precedence\n got:  %s\n want: %s", got, want)
	}
}

func TestCompileErrors(t *testing.T) {
	for _, e := range []string{"n +", "1 2", ")", "* 3", "n = "} {
		if _, err := CompileJSON(e); err == nil {
			t.Errorf("expected error for %q, got none", e)
		}
	}
}
