package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// grammarRun compiles and runs source with the JavaScript-compatible globals (fail, JSON…) and the
// given ones, and gives `result` as JSON, or the run's failure.
func grammarRun(t *testing.T, source string, globals map[string]value.Value) (string, *runtime.Diagnostic) {
	t.Helper()
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 10_000_000
	builtins.InjectJSCompat(vm.Globals)
	for name, item := range globals {
		vm.Globals[name] = item
	}
	if returned := vm.Run(); returned.IsInvalid() {
		diagnostic, ok := runtime.DiagnosticFrom(returned)
		if !ok {
			t.Fatalf("run %q: %s", source, returned.Text())
		}
		return "", diagnostic
	}
	data, err := json.Marshal(vm.Vars["result"].Interface())
	if err != nil {
		t.Fatalf("encode result of %q: %v", source, err)
	}
	return string(data), nil
}

func grammarResult(t *testing.T, source string) string {
	t.Helper()
	got, failure := grammarRun(t, source, nil)
	if failure != nil {
		t.Fatalf("run %q: %v", source, failure)
	}
	return got
}

// grammarCompileError compiles source, which must fail, and gives the error.
func grammarCompileError(t *testing.T, source string) string {
	t.Helper()
	_, err := CompileSource(source)
	if err == nil {
		t.Fatalf("%q compiled", source)
	}
	return err.Error()
}

// countOps counts the instructions of each opcode in a program.
func countOps(t *testing.T, bytecode *Bytecode) map[runtime.Opcode]int {
	t.Helper()
	code := bytecode.Instructions()
	counts := map[runtime.Opcode]int{}
	for ip := 0; ip < len(code); {
		op := runtime.Opcode(code[ip])
		spec, ok := runtime.LookupInstruction(op)
		if !ok {
			t.Fatalf("unknown opcode %d at %d", op, ip)
		}
		counts[op]++
		ip += 1 + spec.OperandSize()
	}
	return counts
}

const touchCounter = "let calls = 0;\nconst touch = (v) => { calls = calls + 1; return v; };\n"

// && and || run their right side only when the left does not decide, and give the operand that
// decided, as in JavaScript (14/10/2026). Until then both sides always ran: false && save() saved.
func TestLogicalRightSideRunsOnlyWhenNeeded(t *testing.T) {
	rows := []struct{ name, source, want string }{
		{"and-skips", "const r = false && touch(1);\nconst result = [r, calls];", `[false,0]`},
		{"or-skips", "const r = \"x\" || touch(1);\nconst result = [r, calls];", `["x",0]`},
		{"and-runs", "const r = true && touch(7);\nconst result = [r, calls];", `[7,1]`},
		{"or-runs", "const r = 0 || touch(7);\nconst result = [r, calls];", `[7,1]`},
		{"statement", "false && touch(1);\ntrue || touch(1);\nconst result = calls;", `0`},
		{"chain", "const r = null || 0 || touch(\"\") || touch(5) || touch(6);\nconst result = [r, calls];", `[5,2]`},
		{"deciding-operand", "const result = [0 && touch(1), \"\" || null, 1 && \"a\" && 0, null || \"b\", calls];", `[0,null,0,"b",0]`},
		{"in-argument", "const f = (a, b) => [a, b];\nconst result = [f(false && touch(1), true || touch(2)), calls];", `[[false,true],0]`},
		{"in-return", "const g = (x) => x && touch(x);\nconst result = [g(0), g(3), calls];", `[0,3,1]`},
		// f && f(): a missing callback is not called, so calling null is no error any more.
		{"guarded-call", "const f = null;\nconst result = [f && f(), calls];", `[null,0]`},
		// a constant or a name on the right compiles to AND/OR and gives the same values
		{"plain-right", "const a = 0;\nconst b = 2;\nconst result = [a && b, b && a, a || b, b || \"d\", null || undefined, a && null];", `[0,0,2,2,null,0]`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := grammarResult(t, touchCounter+row.source); got != row.want {
				t.Fatalf("%s\ngot  %s\nwant %s", row.source, got, row.want)
			}
		})
	}
}

// A condition (if, ternary) branches on && and || without making their value: same truth, same order,
// the right side still runs only when needed, nested and under !.
func TestLogicalConditionShortCircuits(t *testing.T) {
	rows := []struct{ name, source, want string }{
		{"if-and-false", "let r = \"no\";\nif (false && touch(true)) { r = \"yes\"; }\nconst result = [r, calls];", `["no",0]`},
		{"if-and-true", "let r = \"no\";\nif (true && touch(true)) { r = \"yes\"; }\nconst result = [r, calls];", `["yes",1]`},
		{"if-or-true", "let r = \"no\";\nif (1 || touch(false)) { r = \"yes\"; } else { r = \"else\"; }\nconst result = [r, calls];", `["yes",0]`},
		{"if-or-false", "let r = \"no\";\nif (0 || touch(0)) { r = \"yes\"; } else { r = \"else\"; }\nconst result = [r, calls];", `["else",1]`},
		{"not-and", "let r = \"no\";\nif (!(false && touch(true))) { r = \"yes\"; }\nconst result = [r, calls];", `["yes",0]`},
		{"not-or", "let r = \"no\";\nif (!(true || touch(true))) { r = \"yes\"; } else { r = \"else\"; }\nconst result = [r, calls];", `["else",0]`},
		{"nested", "let r = 0;\nif ((touch(1) && touch(0)) || (touch(1) && touch(2))) { r = 1; }\nconst result = [r, calls];", `[1,4]`},
		{"nested-skip", "let r = 0;\nif (touch(0) && (touch(1) || touch(2))) { r = 1; } else { r = 2; }\nconst result = [r, calls];", `[2,1]`},
		{"ternary", "const r = (null && touch(1)) ? \"a\" : (touch(1) || touch(2)) ? \"b\" : \"c\";\nconst result = [r, calls];", `["b",1]`},
		{"else-if", "let r = 0;\nif (false || touch(0)) { r = 1; } else if (touch(1) && true) { r = 2; }\nconst result = [r, calls];", `[2,2]`},
		{"mixed-plain", "const a = 1;\nlet r = 0;\nif (a && touch(0) || a) { r = 1; }\nconst result = [r, calls];", `[1,1]`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := grammarResult(t, touchCounter+row.source); got != row.want {
				t.Fatalf("%s\ngot  %s\nwant %s", row.source, got, row.want)
			}
		})
	}
}

// The bytes matter: one program is at most 65,535 bytes, and the largest app bundle was 1,905 bytes
// under it. A constant or a name on the right keeps AND/OR (1 byte); a condition jumps on each side
// (no DUP, no POP); only a value made from a right side that runs takes DUP/FALSE/POP.
func TestLogicalLoweringKeepsBytecodeSmall(t *testing.T) {
	compile := func(source string) map[runtime.Opcode]int {
		bytecode, err := CompileSource(source)
		if err != nil {
			t.Fatalf("compile %q: %v", source, err)
		}
		return countOps(t, bytecode)
	}
	plain := compile("const a = 1;\nconst b = 2;\nconst r = [a && b, a || \"x\", a && null];")
	if plain[runtime.AND] != 2 || plain[runtime.OR] != 1 || plain[runtime.DUP] != 3 {
		// the array literal's three SETs take one DUP each; a jump form would add more
		t.Fatalf("a constant or a name on the right: AND=%d OR=%d DUP=%d, want 2, 1, 3", plain[runtime.AND], plain[runtime.OR], plain[runtime.DUP])
	}
	condition := compile("const f = () => 1;\nlet r = 0;\nif (r && f() || f()) { r = 1; }")
	if condition[runtime.DUP] != 0 || condition[runtime.AND] != 0 || condition[runtime.OR] != 0 {
		t.Fatalf("a condition: DUP=%d AND=%d OR=%d, want none (jumps only)", condition[runtime.DUP], condition[runtime.AND], condition[runtime.OR])
	}
	valued := compile("const f = () => 1;\nconst r = f() && f();")
	if valued[runtime.DUP] != 1 || valued[runtime.AND] != 0 {
		t.Fatalf("a value: DUP=%d AND=%d, want 1 and 0", valued[runtime.DUP], valued[runtime.AND])
	}
}

// ?? gives its right side only when the left is null or undefined: 0, "", false and NaN stay, as in
// JavaScript; the right side runs only then.
func TestNullishCoalescing(t *testing.T) {
	rows := []struct{ name, source, want string }{
		{"null", "const result = [null ?? 1, undefined ?? 2];", `[1,2]`},
		{"falsy-stays", "const result = [0 ?? 1, \"\" ?? 1, false ?? 1];", `[0,"",false]`},
		{"missing-key", "const o = { a: 0 };\nconst result = [o.a ?? 5, o.b ?? 5, o.b?.c ?? 6];", `[0,5,6]`},
		{"right-runs-only-when-null", "const r = [3 ?? touch(1), null ?? touch(2)];\nconst result = [r, calls];", `[[3,2],1]`},
		{"chained", "const result = null ?? undefined ?? 0 ?? 9;", `0`},
		{"precedence", "const a = null;\nconst result = [a ?? 1 + 1, (a ?? 2) == 2 ? \"y\" : \"n\", a ?? null ?? \"z\"];", `[2,"y","z"]`},
		{"grouped-with-or", "const a = null;\nconst result = [(a ?? 0) || 7, a ?? (0 || 8), (a || null) ?? 9];", `[7,8,9]`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := grammarResult(t, touchCounter+row.source); got != row.want {
				t.Fatalf("%s\ngot  %s\nwant %s", row.source, got, row.want)
			}
		})
	}
}

// JavaScript refuses a ?? next to && or || without parentheses; so does Kitwork, saying how to write it.
func TestNullishMixedWithLogicalNeedsParentheses(t *testing.T) {
	for _, row := range []struct{ source, at string }{
		{"const r = a ?? b || c", "<source>:1:18:"},
		{"const r = a || b ?? c", "<source>:1:18:"},
		{"const r = a ?? b && c", "<source>:1:13:"},
		{"const r = a && b ?? c", "<source>:1:18:"},
		{"const r = a ?? (b) || c", "<source>:1:20:"},
	} {
		err := grammarCompileError(t, row.source)
		want := row.at + " không trộn ?? với && hay || khi không có ngoặc"
		if !strings.Contains(err, want) {
			t.Fatalf("%q\nerror: %s\nwant it to contain: %s", row.source, err, want)
		}
	}
	// Control: parentheses say which goes first.
	for _, source := range []string{"const r = (a ?? b) || c", "const r = a ?? (b || c)", "const r = (a && b) ?? c", "const r = a ?? (b && c)"} {
		if _, err := CompileSource(source); err != nil {
			t.Fatalf("%q: %v", source, err)
		}
	}
}

// countingProxy is a host value like the query builder's row: a comparison asks it to build something.
type countingProxy struct{ compares *int }

func (p countingProxy) OnGet(key string) value.Value { return value.NewString("column " + key) }
func (p countingProxy) OnCompare(string, value.Value) value.Value {
	*p.compares++
	return value.ToBool(true)
}
func (p countingProxy) OnInvoke(string, ...value.Value) value.Value { return value.Value{K: value.Nil} }

// ?? and ?. test for null without comparing a proxy with null: comparing it would ask the proxy to build
// a condition (row.x == null in a where), and a proxy is never null.
func TestNullTestNeverComparesAProxy(t *testing.T) {
	compares := 0
	row := value.Value{K: value.Proxy, V: countingProxy{compares: &compares}}
	globals := map[string]value.Value{"row": row}
	got, failure := grammarRun(t, "const kept = row ?? \"fallback\";\nconst { title = \"none\" } = row;\nconst result = [kept.id, row?.name, row?.[\"slug\"], title];", globals)
	if failure != nil {
		t.Fatal(failure)
	}
	if got != `["column id","column name","column slug","column title"]` || compares != 0 {
		t.Fatalf("got %s with %d comparisons; want the proxy kept and read, never compared", got, compares)
	}
	// Control: a comparison written in the source does ask the proxy.
	if _, failure := grammarRun(t, "const result = row == null;", globals); failure != nil || compares != 1 {
		t.Fatalf("row == null: %v, %d comparisons, want 1", failure, compares)
	}
}
