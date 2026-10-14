package builtins_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// run compiles and runs source with the JavaScript globals, and gives what it ended with and its
// variables.
func run(t *testing.T, source string) (value.Value, map[string]value.Value) {
	t.Helper()
	bytecode, err := compiler.CompileSource(source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	vm := runtime.New(bytecode.Program)
	builtins.InjectJSCompat(vm.Globals)
	returned := vm.Run()
	return returned, vm.Vars
}

// JSON.stringify of an object that holds itself is JavaScript's TypeError: inside safe(() => …) the
// error (code TYPE, its message saying where the cycle is), anywhere else the end of the run, at the
// call's place. Until 14/10/2026 it was a Go stack overflow, which stopped the whole host: every site,
// for one tenant's object.
func TestStringifyOfAnObjectThatHoldsItselfIsATypeError(t *testing.T) {
	cases := []struct {
		label, source, want string
	}{
		{"a map", "const o = { a: 1 }\no.self = o",
			"JSON.stringify: object tự tham chiếu (vòng ở self, quay về gốc)"},
		{"an array", "const o = [1]\no.push(o)",
			"JSON.stringify: mảng tự tham chiếu (vòng ở [1], quay về gốc)"},
		{"nested maps", "const o = { b: { c: {} } }\no.b.c.back = o.b",
			"JSON.stringify: object tự tham chiếu (vòng ở b.c.back, quay về b)"},
		{"an array in a map", "const o = { list: [] }\no.list.push({ owner: o })",
			"JSON.stringify: object tự tham chiếu (vòng ở list[0].owner, quay về gốc)"},
	}
	for _, c := range cases {
		returned, vars := run(t, c.source+"\nconst r = safe(() => JSON.stringify(o))\nconst result = [r.error != null, r.error.message, r.error.code]")
		if returned.IsInvalid() {
			t.Fatalf("%s: the run failed: %s", c.label, returned.Text())
		}
		result := vars["result"]
		if !result.At(0).Truthy() || result.At(1).Text() != c.want || result.At(2).Text() != value.TypeCode {
			t.Errorf("%s: safe() = %s, want [true, %q, %s]", c.label, result.Text(), c.want, value.TypeCode)
		}

		// not caught: the run ends there, saying where
		returned, _ = run(t, c.source+"\nconst s = JSON.stringify(o)")
		if !returned.IsInvalid() || !strings.Contains(returned.Text(), c.want) || !strings.Contains(returned.Text(), "<source>:3:") {
			t.Errorf("%s: uncaught = %s, want the failure at <source>:3", c.label, returned.Text())
		}
	}
}

// The same object twice is not a cycle; 10000 levels are written and read back; 10001 are JavaScript's
// RangeError, an operation past its bound: INVALID_ARGUMENT.
func TestStringifySharedAndDeepValues(t *testing.T) {
	returned, vars := run(t, "const x = { n: 1 }\nconst s = JSON.stringify({ p: x, q: [x, x] })")
	if returned.IsInvalid() || vars["s"].Text() != `{"p":{"n":1},"q":[{"n":1},{"n":1}]}` {
		t.Fatalf("shared object: %s, %s", returned.Text(), vars["s"].Text())
	}

	deep := func(levels int) string {
		return "let v = 0\nfor (let i = 0; i < " + strconv.Itoa(levels) + "; i++) {\n  v = [v]\n}\n"
	}
	returned, vars = run(t, deep(value.MaxDepth)+"const s = JSON.stringify(v)\nconst back = JSON.parse(s)\nconst same = back == v")
	if returned.IsInvalid() || len(vars["s"].Text()) != 2*value.MaxDepth+1 || !vars["same"].Truthy() {
		t.Fatalf("%d levels: %.200s (%d bytes, read back equal: %v)", value.MaxDepth, returned.Text(), len(vars["s"].Text()), vars["same"].Truthy())
	}
	returned, vars = run(t, deep(value.MaxDepth+1)+"const r = safe(() => JSON.stringify(v))\nconst result = [r.error != null, r.error.code]")
	if returned.IsInvalid() || vars["result"].Text() != "[true, "+value.InvalidArgumentCode+"]" {
		t.Fatalf("%d levels: %.200s, %s", value.MaxDepth+1, returned.Text(), vars["result"].Text())
	}
}

// What else walks the object: its text, equality, flat(), .json("string").
func TestOtherWalksOfAnObjectThatHoldsItself(t *testing.T) {
	source := "const o = { a: 1 }\no.self = o\n" +
		"const text = \"\" + o.self.self\n" +
		"const twin = { a: 1 }\ntwin.self = twin\nconst equal = o == twin\n" +
		"const list = [1]\nlist.push(list)\nconst f = safe(() => list.flat(1000000000))\nconst flat = [f.error.code, f.error.message]\n" +
		"const j = safe(() => o.json(\"string\"))\nconst json = [j.error.code, j.error.message]"
	returned, vars := run(t, source)
	if returned.IsInvalid() {
		t.Fatalf("the run failed: %s", returned.Text())
	}
	if got := vars["text"].Text(); got != "{a: 1, self: [tự tham chiếu]}" && got != "{self: [tự tham chiếu], a: 1}" {
		t.Errorf("\"\" + o = %q", got)
	}
	if !vars["equal"].Truthy() {
		t.Error("two objects that hold themselves alike are not ==")
	}
	if got := vars["flat"].Text(); got != "[TYPE, flat: mảng tự tham chiếu (vòng ở [1], quay về gốc)]" {
		t.Errorf("safe(() => list.flat(1e9)) = %s", got)
	}
	if got := vars["json"].Text(); got != "[TYPE, json: object tự tham chiếu (vòng ở self, quay về gốc)]" {
		t.Errorf("safe(() => o.json(\"string\")) = %s", got)
	}
}
