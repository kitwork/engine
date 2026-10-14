package compiler

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// runOwnKey runs source in a fresh VM, with globals set before it starts, and gives `result` as JSON.
func runOwnKey(t *testing.T, source string, globals map[string]value.Value) string {
	t.Helper()
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	return runCompiledResult(t, bytecode, globals, source)
}

// runCompiledResult runs compiled bytecode in a fresh VM, with globals set before it starts, and gives
// `result` as JSON. label names the program in a failure.
func runCompiledResult(t *testing.T, bytecode *Bytecode, globals map[string]value.Value, label string) string {
	t.Helper()
	vm := runtime.New(bytecode.Program)
	for name, item := range globals {
		vm.Globals[name] = item
	}
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatalf("run %q: %s", label, returned.Text())
	}
	data, err := json.Marshal(vm.Vars["result"].Interface())
	if err != nil {
		t.Fatalf("encode result of %q: %v", label, err)
	}
	return string(data)
}

// An object's own key wins over the builtin method of the same name on every READ the VM makes: a
// read after a dot (GET), a read by bracket (GET) and destructuring. A CALL reaches the own key only
// when it holds a function (value.CallsOwn): a lambda is called after a dot (INVOKE) and through a
// bracket (GET then CALL). An own key holding data never answers a call (13/10/2026, "khoá riêng là
// hàm mới gọi nó"): after a dot the call goes to the builtin, as on an object without that key.
func TestOwnKeyWinsOverBuiltinInTheVM(t *testing.T) {
	names := []string{"keys", "delete", "has", "merge", "pick", "omit", "text", "string", "toString", "json", "int", "float", "len", "length"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			builtin, ok := value.Map.Method(name)
			if !ok {
				t.Fatalf("%s is not a method of an object: the row tests nothing", name)
			}

			// destructured under a name of its own: delete is no variable name (14/10/2026), as in JavaScript
			read := fmt.Sprintf("const o = { %s: 3 }\nconst { %s: own } = o\nconst result = [o.%s, o[%q], own]", name, name, name, name)
			if got := runOwnKey(t, read, nil); got != "[3,3,3]" {
				t.Fatalf("%q = %s, want [3,3,3]", read, got)
			}

			call := fmt.Sprintf("const o = { %s: (x) => x + 1 }\nconst result = [o.%s(41), o[%q](1)]", name, name, name)
			if got := runOwnKey(t, call, nil); got != "[42,2]" {
				t.Fatalf("%q = %s, want [42,2]", call, got)
			}

			// An own key holding data, called after a dot: the builtin, on the same object, exactly as
			// the kind's method answers it. Never the data (3).
			data := fmt.Sprintf("const o = { %s: 3 }\nconst result = o.%s(\"k\")", name, name)
			expected, err := json.Marshal(builtin(value.New(map[string]value.Value{name: value.New(3)}), value.NewString("k")).Interface())
			if err != nil {
				t.Fatal(err)
			}
			if string(expected) == "3" {
				t.Fatalf("the builtin %s gives 3 on { %s: 3 }: the row cannot tell it from the data", name, name)
			}
			if got := runOwnKey(t, data, nil); got != string(expected) {
				t.Fatalf("%q = %s, want the builtin's %s", data, got, expected)
			}

			// Through a bracket the call is compiled as a read then a call, so the object is gone when
			// the call runs: the data, called, is a value that is not a function, and calling it is the
			// runtime error (14/10/2026; until then null). It is never the data. (A builtin read through
			// a bracket is called without its object: o["keys"]() gives [] on any object.)
			bracket := fmt.Sprintf("const o = { %s: 3 }\nconst r = safe(() => o[%q](\"k\"))\nconst result = [r.error.at, r.error.message]", name, name)
			want := fmt.Sprintf(`["\u003csource\u003e:2:22","o[\"%s\"] không phải hàm (là số): không gọi được"]`, name)
			if got := runOwnKey(t, bracket, safeGlobal()); got != want {
				t.Fatalf("%q = %s, want %s", bracket, got, want)
			}
		})
	}

	// A key that names no builtin, holding data and called, is not a function after a dot or through
	// a bracket: a runtime error (14/10/2026; until then the value itself after a dot and null through
	// a bracket).
	source := "const o = { x: 3 }\nconst a = safe(() => o.x(\"k\"))\nconst b = safe(() => o[\"x\"](\"k\"))\nconst result = [a.error.message, b.error.message]"
	if got, want := runOwnKey(t, source, safeGlobal()), `["o.x không phải hàm (là số): không gọi được","o[\"x\"] không phải hàm (là số): không gọi được"]`; got != want {
		t.Fatalf("{ x: 3 } called = %s, want %s", got, want)
	}

	// Control: objects without those keys keep every builtin.
	builtins := `
const o = { a: 1, b: 2 }
const merged = { a: 1 }.merge({ b: 2 })
const result = [o.keys().length, o.has("a"), o.pick("a").b, o.omit("a").b, merged.b, o.len(), { a: 1, b: 2 }.delete("a").b]
`
	if got := runOwnKey(t, builtins, nil); got != "[2,true,null,2,2,2,2]" {
		t.Fatalf("builtins on objects without own keys = %s, want [2,true,null,2,2,2,2]", got)
	}

	// An own key holding a host function, called through the dot: the shape of new Date(), whose
	// toString gave the object's own text before 13/10/2026.
	date := value.New(map[string]value.Value{
		"toString": value.NewFunc(func(...value.Value) value.Value { return value.NewString("Thu Jan 01 1970") }),
	})
	if got := runOwnKey(t, "const result = date.toString()", map[string]value.Value{"date": date}); got != `"Thu Jan 01 1970"` {
		t.Fatalf("date.toString() = %s, want the date", got)
	}

	// A request body read as JSON is an object too: its "text" field is the text.
	if got := runOwnKey(t, "const body = { text: \"chào\" }\nconst result = `${body.text}!`", nil); got != `"chào!"` {
		t.Fatalf("body.text = %s, want \"chào!\"", got)
	}
}

// x.safe() with no argument is a compile error since 14/10/2026 (it was the protected evaluator),
// whatever x carries: an object's own function safe is reached with an argument, o.safe(1), or
// through a bracket, o["safe"](). Neither is the global safe.
func TestZeroArgumentSafeIsACompileErrorOverAnOwnFunction(t *testing.T) {
	source := "const o = { safe: (x) => \"own:\" + x, a: 1 }\nconst result = [o.safe(1), o[\"safe\"](2)]"
	if got, want := runOwnKey(t, source, nil), `["own:1","own:2"]`; got != want {
		t.Fatalf("%q = %s, want %s", source, got, want)
	}
	removed := "const o = { safe: (x) => \"own:\" + x, a: 1 }\nconst r = o.safe()"
	if _, err := CompileSource(removed); err == nil || !strings.Contains(err.Error(), "<source>:2:13: .safe() đã bỏ (14/10/2026): viết safe(() => o)") {
		t.Fatalf("%q compiled or said %v, want the error of the removed .safe()", removed, err)
	}
}

// safeGlobal is the global safe, for a run without the JavaScript globals.
func safeGlobal() map[string]value.Value {
	return map[string]value.Value{"safe": value.NewSafeFunction()}
}

// An own key named error, length or isError is data like any other key (13/10/2026, "sửa: khoá
// riêng thắng"): { ok: false, error: "x" }.error is "x", where it read null before. An object without
// those keys has the engine's length, and no error or isError (since 14/10/2026: the engine attaches
// no error to data, so it has none to answer with). An own error or length that holds a function is
// called.
func TestOwnErrorLengthAndIsErrorAreDataOnAPlainObject(t *testing.T) {
	cases := []struct{ source, want string }{
		{"const r = { ok: false, error: \"x\", length: 5, isError: \"I\" }\nconst result = [r.error, r.length, r.isError, r[\"error\"]]", `["x",5,"I","x"]`},
		{"const r = { ok: false, error: \"x\" }\nconst { error } = r\nconst result = [error, r.error || \"fallback\"]", `["x","x"]`},
		{"const r = JSON.parse('{\"error\":\"e\",\"length\":3}')\nconst result = [r.error, r.length, r.length()]", `["e",3,2]`},
		{"const o = { error: (m) => \"E:\" + m, length: () => 7 }\nconst result = [o.error(\"x\"), o.length()]", `["E:x",7]`},
		// Control: without those keys the engine answers, as before.
		{"const r = { a: 1, b: 2 }\nconst result = [r.error, r.length, r.isError, \"abc\".length, [1, 2, 3].length]", `[null,2,null,3,3]`},
	}
	globals := map[string]value.Value{}
	builtins.InjectJSCompat(globals)
	for _, c := range cases {
		if got := runOwnKey(t, c.source, globals); got != c.want {
			t.Errorf("%q = %s, want %s", c.source, got, c.want)
		}
	}
}
