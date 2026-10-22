package compiler

import (
	"strings"
	"testing"
)

// An arrow function's parameter may be an object pattern, ({ request, response }) => …, as in
// JavaScript (19/10/2026): it binds like (o) => { let { request, response } = o; … }, with the pattern's
// renames, defaults and nesting, and the names are the function's own, read by its closures.
func TestObjectPatternParameter(t *testing.T) {
	rows := []struct{ name, source, want string }{
		{"shorthand", "const f = ({ a, b }) => a + b;\nconst result = f({ a: 1, b: 2 });", `3`},
		{"block-body", "const f = ({ request, response }) => { const sum = request + response; return sum; };\nconst result = f({ request: 1, response: 2 });", `3`},
		{"rename-default-nested", "const f = ({ value: v, error = \"none\", page: { size } }) => [v, error, size];\nconst result = f({ value: 1, page: { size: 9 } });", `[1,"none",9]`},
		{"second-parameter", "const f = (x, { y }) => x * y;\nconst result = f(3, { y: 4 });", `12`},
		{"two-patterns", "const f = ({ a }, { b }) => a - b;\nconst result = f({ a: 5 }, { b: 2 });", `3`},
		{"mutable-like-a-parameter", "const f = ({ a }) => { a = a + 1; return a; };\nconst result = f({ a: 1 });", `2`},
		{"null-argument", "const f = ({ a, b = 2 }) => [a, b];\nconst result = f(null);", `[null,2]`},
		{"closure-reads-it", "const f = ({ response }) => () => response.code;\nconst result = f({ response: { code: 201 } })();", `201`},
		{"callback", "const result = [{ id: 1 }, { id: 2 }].map(({ id }) => id * 10);", `[10,20]`},
		{"shadows-outer", "const response = \"outer\";\nconst f = ({ response }) => response;\nconst result = [f({ response: \"inner\" }), response];", `["inner","outer"]`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := grammarResult(t, row.source); got != row.want {
				t.Fatalf("%s\ngot  %s\nwant %s", row.source, got, row.want)
			}
		})
	}
}

// The parameter's name is the pattern as written: no source can declare it, so it never meets a name of
// the body, and a host reading Params sees which names were taken.
func TestObjectPatternParameterName(t *testing.T) {
	prog, p := parseSrc(`const f = ({ request, response }) => response;`)
	if len(p.Errors()) > 0 {
		t.Fatalf("parse: %v", p.Errors())
	}
	vs := prog.Statements[0].(*VarStatement)
	fn := vs.Value.(*FunctionLiteral)
	if len(fn.Parameters) != 1 || fn.Parameters[0].Value != "{ request, response }" {
		t.Fatalf("parameters %v", fn.Parameters)
	}
	if got := DestructuredNames(fn.Parameters[0].Value); strings.Join(got, ",") != "request,response" {
		t.Fatalf("DestructuredNames = %v", got)
	}
}

// What a pattern parameter still does not have says so at its place.
func TestObjectPatternParameterNotYet(t *testing.T) {
	rows := []struct{ source, want string }{
		{"const f = ([a, b]) => a", "<source>:1:13: Kitwork chưa có tách mảng trong tham số"},
		{"function f({ id }) { return id }", "<source>:1:12: Kitwork chưa có tách object trong tham số"},
		{"const f = ({ a } = {}) => a", "Kitwork chưa có tách object trong tham số"},
		{"const f = ({ a, ...rest }) => a", "Kitwork chưa có phần còn lại khi tách (...tên)"},
	}
	for _, row := range rows {
		if err := grammarCompileError(t, row.source); !strings.Contains(err, row.want) {
			t.Errorf("%q\nerror: %s\nwant it to contain: %s", row.source, err, row.want)
		}
	}
}

// DestructuredEntries reads the top level of the pattern back, nested parts and defaults kept apart.
func TestDestructuredEntries(t *testing.T) {
	prog, p := parseSrc(`const f = ({ request: { body }, response: res, job = "a, b", "x, y": z, data: [x] }) => 1;`)
	if len(p.Errors()) > 0 {
		t.Fatalf("parse: %v", p.Errors())
	}
	name := prog.Statements[0].(*VarStatement).Value.(*FunctionLiteral).Parameters[0].Value
	got := DestructuredEntries(name)
	want := []DestructuredEntry{{"request", true, "{ body }"}, {"response", false, "res"}, {"job", false, "job"}, {"x, y", false, "z"}, {"data", true, "[ x ]"}}
	if len(got) != len(want) {
		t.Fatalf("%q → %v, want %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%q → %v, want %v", name, got, want)
		}
	}
	if DestructuredEntries("context") != nil {
		t.Fatal("a plain name has no entries")
	}
}
