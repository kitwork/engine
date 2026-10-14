package compiler

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// ?. reads or calls only when what is on its left is not null or undefined; otherwise the rest of the
// chain does not run, arguments included, and the chain is null, as in JavaScript (14/10/2026).
func TestOptionalChainSkipsTheRestWhenNull(t *testing.T) {
	rows := []struct{ name, source, want string }{
		{"member", "const o = null;\nconst result = [o?.a, o?.a.b.c, o?.[\"a\"], o?.a?.b];", `[null,null,null,null]`},
		{"method-arguments-skipped", "const o = null;\nconst r = [o?.m(touch(1)), o?.a.m(touch(2)), o?.[\"m\"](touch(3))];\nconst result = [r, calls];", `[[null,null,null],0]`},
		{"call", "const f = null;\nconst result = [f?.(touch(1)), calls];", `[null,0]`},
		{"method-call", "const o = { a: 1 };\nconst r = [o.m?.(touch(1)), o?.m?.(touch(2)), o.a.m?.()];\nconst result = [r, calls];", `[[null,null,null],0]`},
		{"present", "const o = { a: { b: 1, list: [10, 20] }, m: (x) => x * 2 };\nconst result = [o?.a?.b, o?.a.list?.[1], o?.m(3), o.m?.(4), o?.m?.(5)];", `[1,20,6,8,10]`},
		{"present-function", "const f = (x) => x + 1;\nconst result = [f?.(1), [f]?.[0]?.(2)];", `[2,3]`},
		{"native-methods", "const s = \"abc\";\nconst result = [s?.toUpperCase(), s.toUpperCase?.(), s?.length, [3, 1, 2]?.join(\"-\")];", `["ABC","ABC",3,"3-1-2"]`},
		{"falsy-is-not-null", "const z = 0;\nconst e = \"\";\nconst result = [z?.toString(), e?.length, false?.x];", `["0",0,null]`},
		{"keyword-names", "const o = { default: 1, function: 2 };\nconst result = [o?.default, o?.function, null?.default];", `[1,2,null]`},
		{"in-arguments-and-conditions", "const o = null;\nconst f = (x) => x;\nlet r = \"no\";\nif (o?.ready) { r = \"yes\"; }\nconst result = [f(o?.a), r, o?.a ?? \"d\", !o?.a];", `[null,"no","d",true]`},
		{"template", "const o = { name: \"Ann\" };\nconst n = null;\nconst result = `${o?.name}/${n?.name ?? \"-\"}`;", `"Ann/-"`},
		{"each-link-runs-once", "const o = { a: { b: 5 } };\nconst result = [touch(o)?.a?.b, calls];", `[5,1]`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := grammarResult(t, touchCounter+row.source); got != row.want {
				t.Fatalf("%s\ngot  %s\nwant %s", row.source, got, row.want)
			}
		})
	}
}

// Parentheses end a chain, as in JavaScript: (o?.m)() calls what (o?.m) is, null here, which is an
// error, where o?.m() is null.
func TestParenthesesEndAnOptionalChain(t *testing.T) {
	if got := grammarResult(t, "const o = null;\nconst result = o?.m();"); got != "null" {
		t.Fatalf("o?.m() gives %s, want null", got)
	}
	_, failure := grammarRun(t, "const o = null;\nconst result = (o?.m)();", nil)
	if failure == nil || failure.CauseCode != value.NotAFunctionCode {
		t.Fatalf("(o?.m)(): %v, want NOT_A_FUNCTION", failure)
	}
}

// A call through ?. that finds something which is not a function is the usual error, named as the
// source names it (the two paths a ?. makes both lead back to o) and placed at the call, which its
// printed text leads with.
func TestOptionalCallOfANonFunctionIsNamed(t *testing.T) {
	rows := []struct{ source, want string }{
		{"const o = { x: 3 }\nconst r = o?.x()", "<source>:2:11: o.x không phải hàm (là số)"},
		{"const o = { x: 3 }\nconst r = o.x?.()", "<source>:2:11: o.x không phải hàm (là số)"},
		{"const o = { a: { x: 3 } }\nconst r = o?.a?.x()", "<source>:2:11: o.a.x không phải hàm (là số)"},
		{"const o = { x: 3 }\nconst r = o?.[\"x\"]()", "<source>:2:11: o[\"x\"] không phải hàm (là số)"},
		{"const f = 3\nconst r = f?.()", "<source>:2:11: f không phải hàm (là số)"},
	}
	for _, row := range rows {
		_, failure := grammarRun(t, row.source, nil)
		if failure == nil || failure.CauseCode != value.NotAFunctionCode || !strings.Contains(failure.Error(), row.want) {
			t.Errorf("%q: %v\nwant NOT_A_FUNCTION with %s", row.source, failure, row.want)
		}
	}
}

// ?. only reads: assigning through it is a compile error, as in JavaScript, and so is a ?. with nothing
// readable after it.
func TestOptionalChainIsNotAnAssignmentTarget(t *testing.T) {
	rows := []struct{ source, want string }{
		{"const o = {}\no?.a = 1", "<source>:2:6: không gán được vào o?.a: ?. chỉ để đọc"},
		{"const o = {}\no?.a.b += 1", "<source>:2:8: không gán được vào o?.a.b: ?. chỉ để đọc"},
		{"const o = {}\no?.n++", "<source>:2:5: không gán được vào o?.n: ?. chỉ để đọc"},
		{"const o = {};\n++o?.n", "<source>:2:1: không gán được vào o?.n: ?. chỉ để đọc"},
		{"const o = {}\nconst r = o?.+1", "<source>:2:12: sau ?. là một tên, [ hay ("},
	}
	for _, row := range rows {
		if err := grammarCompileError(t, row.source); !strings.Contains(err, row.want) {
			t.Fatalf("%q\nerror: %s\nwant it to contain: %s", row.source, err, row.want)
		}
	}
	// Control: the same reads and writes without ?. compile.
	for _, source := range []string{"const o = { a: {} }\no.a = 1", "const o = { a: { b: 1 } }\no.a.b += 1", "const o = { n: 1 }\no.n++"} {
		if _, err := CompileSource(source); err != nil {
			t.Fatalf("%q: %v", source, err)
		}
	}
}

// a ? .5 : 1 — a ?. followed by a digit is the ternary, as in JavaScript; a ?.b is the chain.
func TestQuestionDotBeforeADigitIsTheTernary(t *testing.T) {
	tokens := func(source string) []Kind {
		lexer := NewLexer(source)
		var kinds []Kind
		for {
			token := lexer.NextToken()
			if token.Kind == EOF {
				return kinds
			}
			kinds = append(kinds, token.Kind)
		}
	}
	if got := tokens("a?.5"); len(got) < 2 || got[1] != Question {
		t.Fatalf("a?.5 lexes as %v, want a then ? (a ternary)", got)
	}
	if got := tokens("a?.b"); len(got) != 3 || got[1] != OptionalChain {
		t.Fatalf("a?.b lexes as %v, want a, ?., b", got)
	}
	if got := tokens("a??b"); len(got) != 3 || got[1] != NullCoalescing {
		t.Fatalf("a??b lexes as %v, want a, ??, b", got)
	}
}
