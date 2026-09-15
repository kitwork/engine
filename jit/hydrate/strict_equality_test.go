package hydrate

import (
	"strings"
	"testing"
)

// `===` and `!==` are what a JS-familiar author writes first — 450 directives across 23 tenant
// files had them before the grammar did, every one silently ignored by the kernel (a parse error
// caches as null) and reported by render as "unexpected token '='" (the lexer cut `===` into `==`
// and `=`). They are strict: no coercion, unlike `==`, matching JavaScript.
func TestStrictEqualityCompiles(t *testing.T) {
	for src, want := range map[string]string{
		"mode === 'dark'": `["===",["$","mode"],["#","dark"]]`,
		"mode !== 'dark'": `["!==",["$","mode"],["#","dark"]]`,
		"a === b && c":    `["&&",["===",["$","a"],["$","b"]],["$","c"]]`, // same precedence rung as ==
		"n === 1 + 1":     `["===",["$","n"],["+",["#",1],["#",1]]]`,      // binds looser than +
	} {
		got, err := CompileJSON(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if got != want {
			t.Errorf("%s\n got %s\nwant %s", src, got, want)
		}
	}
	// The lexer must not read `==` + `=`: a stray single `=` after `==` is still an error.
	if _, err := Compile("a == = b"); err == nil || !strings.Contains(err.Error(), "unexpected token '='") {
		t.Errorf("`a == = b` must still fail as before, got %v", err)
	}
}

func TestStrictEqualityEvaluates(t *testing.T) {
	for _, c := range []struct {
		src   string
		scope map[string]any
		want  any
	}{
		{"n === 1", map[string]any{"n": 1.0}, true},
		{"n === '1'", map[string]any{"n": 1.0}, false},
		{"n == '1'", map[string]any{"n": 1.0}, true},
		{"s !== 'x'", map[string]any{"s": "x"}, false},
		{"b === true", map[string]any{"b": true}, true},
		{"b === 1", map[string]any{"b": true}, false},
		{"missing === null", map[string]any{}, false}, // a missing key reads as 0, like the client
		{"v === null", map[string]any{"v": nil}, true},
	} {
		ir, err := Compile(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		got, err := Eval(ir, c.scope)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("%s with %v = %v, want %v", c.src, c.scope, got, c.want)
		}
	}
}
