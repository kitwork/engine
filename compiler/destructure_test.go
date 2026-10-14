package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// const { value: user, error: userError } = …: renaming, reserved-word and string keys, defaults and
// nested patterns, as in JavaScript (14/10/2026), with Kitwork's two rules: a default applies when
// the key is missing OR null (there is no undefined of its own), and a nested pattern over null gives
// null to every name in it (a member of null is null).
func TestDestructuringRenamesDefaultsAndNests(t *testing.T) {
	rows := []struct{ name, source, want string }{
		{"rename", "const { value: user, error: userError } = { value: 1, error: null };\nconst result = [user, userError];", `[1,null]`},
		{"mixed", "const { value: orders, error } = { value: [1], error: \"x\" };\nconst result = [orders, error];", `[[1],"x"]`},
		{"reserved-and-string-keys", "const { default: d, \"content-type\": type, function: f } = { default: 2, \"content-type\": \"text/html\", function: 3 };\nconst result = [d, type, f];", `[2,"text/html",3]`},
		{"let", "let { a: x, b: y = 4 } = { a: 1 };\nx = x + 1;\nconst result = [x, y];", `[2,4]`},
		{"default-missing-or-null", "const { a = 1, b = 2, c = 3, d = 4, e = 5 } = { b: null, c: 0, d: \"\", e: false };\nconst result = [a, b, c, d, e];", `[1,2,0,"",false]`},
		{"default-runs-only-when-needed", "const { a = touch(9) } = { a: 1 };\nconst { b = touch(8) } = {};\nconst result = [a, b, calls];", `[1,8,1]`},
		{"default-reads-earlier-names", "const { x = 1, y = x + 1, z: w = y * 10 } = {};\nconst result = [x, y, w];", `[1,2,20]`},
		{"nested", "const { a: { b, c: [first, second = 9] } } = { a: { b: 1, c: [5] } };\nconst result = [b, first, second];", `[1,5,9]`},
		{"nested-over-null", "const { a: { b }, c: [d] } = { a: null };\nconst result = [b, d];", `[null,null]`},
		{"nested-default", "const { a: { b } = { b: 7 }, c: { e } = { e: 8 } } = { c: null };\nconst result = [b, e];", `[7,8]`},
		{"array", "const [p = 1, q = 2, { r }] = [null, 0, { r: 3 }];\nconst result = [p, q, r];", `[1,0,3]`},
		{"value-null", "const { a, b: c = 1 } = null;\nconst result = [a, c];", `[null,1]`},
		{"right-side-runs-once", "const { a, b } = touch({ a: 1, b: 2 });\nconst result = [a, b, calls];", `[1,2,1]`},
		{"assignment", "let user = 0;\nlet note = 0;\n;({ value: user, error: note = \"none\" } = { value: 3 });\nconst result = [user, note];", `[3,"none"]`},
		{"assignment-nested", "let b = 0;\nlet c = 0;\n;({ a: { b }, list: [c = 6] } = { a: { b: 4 }, list: [] });\nconst result = [b, c];", `[4,6]`},
		{"assignment-swap", "let a = 1;\nlet b = 2;\n;[a, b] = [b, a];\nconst result = [a, b];", `[2,1]`},
		{"assignment-value", "let a = 0;\nconst o = ({ a } = { a: 5, z: 1 });\nconst result = [a, o.z];", `[5,1]`},
		{"export", "export const { a: renamed, b = 2 } = { a: 1 };\nconst result = [renamed, b];", `[1,2]`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := grammarResult(t, touchCounter+row.source); got != row.want {
				t.Fatalf("%s\ngot  %s\nwant %s", row.source, got, row.want)
			}
		})
	}
}

// A function given as a default takes the name it is bound to, as `const f = () => …` does.
func TestDestructuringDefaultNamesItsFunction(t *testing.T) {
	bytecode, err := CompileSource("const { handler = () => 1 } = {};\n;({ other = () => 2 } = {});")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, constant := range bytecode.Constants() {
		if lambda, ok := constant.V.(*value.Lambda); ok {
			names = append(names, lambda.Name)
		}
	}
	if !reflect.DeepEqual(names, []string{"handler", "other"}) {
		t.Fatalf("lambda names %v, want [handler other]", names)
	}
}

// The destructuring written before 14/10/2026 compiles to the same bytes: { a } still uses one
// constant for the key and the name. Every app source compiled the same on the day (that proof ran
// outside the tests); this pins it. The names are let: a const is never assigned (14/10/2026), and let
// compiles to the same bytes.
func TestShorthandDestructuringKeepsItsBytecode(t *testing.T) {
	bytecode, err := CompileSource("let { a, b } = o;\nlet [c, d] = list;\n;({ a, b } = o);\n;[c, d] = list;")
	if err != nil {
		t.Fatal(err)
	}
	constant := func(text string) value.Value { return value.NewString(text) }
	number := func(n int) value.Value { return value.New(n) }
	want := []value.Value{
		constant("o"), constant("a"), constant("b"),
		constant("list"), number(0), constant("c"), number(1), constant("d"),
		constant("o"), constant("a"), constant("b"),
		constant("list"), number(0), constant("c"), number(1), constant("d"),
	}
	if got := bytecode.Constants(); !reflect.DeepEqual(got, want) {
		t.Fatalf("constants %v\nwant %v", got, want)
	}
	ops := []runtime.Opcode{}
	code := bytecode.Instructions()
	for ip := 0; ip < len(code); {
		op := runtime.Opcode(code[ip])
		spec, _ := runtime.LookupInstruction(op)
		ops = append(ops, op)
		ip += 1 + spec.OperandSize()
	}
	element := []runtime.Opcode{runtime.DUP, runtime.PUSH, runtime.GET, runtime.STORE, runtime.POP}
	declaration := append([]runtime.Opcode{runtime.LOAD}, append(append(append([]runtime.Opcode{}, element...), element...), runtime.COMMIT, runtime.POP)...)
	assignment := append([]runtime.Opcode{runtime.LOAD}, append(append(append([]runtime.Opcode{}, element...), element...), runtime.COMMIT, runtime.POP)...)
	wantOps := append(append(append(append(append([]runtime.Opcode{}, declaration...), declaration...), assignment...), assignment...), runtime.RETURN)
	if !reflect.DeepEqual(ops, wantOps) {
		t.Fatalf("instructions %v\nwant %v", ops, wantOps)
	}
}

// What destructuring does not have yet is a compile error at its place that says how to write it.
func TestDestructuringNotYetSaysHowToWriteIt(t *testing.T) {
	rows := []struct{ source, want string }{
		{"const o = {}\nconst { a, ...others } = o", "<source>:2:12: Kitwork chưa có phần còn lại khi tách (...tên)"},
		{"const list = []\nconst [a, ...others] = list", "<source>:2:11: Kitwork chưa có phần còn lại khi tách (...tên)"},
		{"const list = []\nconst [, b] = list", "<source>:2:8: Kitwork chưa có chỗ trống khi tách mảng ([, b]); viết const b = arr[1]"},
		{"const o = {}\nconst k = \"a\"\nconst { [k]: v } = o", "<source>:3:9: Kitwork chưa có khoá tính toán khi tách object ({ [k]: v }); viết const v = o[k]"},
		{"const o = {}\nconst { 0: first } = o", "<source>:2:9: khoá số khi tách object chưa có; viết khoá chuỗi { \"0\": tên }"},
		{"const o = {}\nconst { \"a-b\" } = o", "<source>:2:9: khoá chuỗi cần một tên đích: { \"a-b\": tên }"},
		{"const o = {}\nconst { a b } = o", "<source>:2:11: các phần của mẫu tách ngăn bằng dấu phẩy"},
		{"const o = {}\nconst { default } = o", "<source>:2:9: default là từ khoá của ngôn ngữ, không làm tên biến được"},
		{"const f = ({ id }) => id", "<source>:1:12: Kitwork chưa có tách object trong tham số; nhận cả giá trị rồi tách trong thân"},
		{"const f = ([a, b]) => a", "<source>:1:13: Kitwork chưa có tách mảng trong tham số"},
		{"function f({ id }) { return id }", "<source>:1:12: Kitwork chưa có tách object trong tham số"},
		{"const f = (a = 1) => a", "<source>:1:12: Kitwork chưa có giá trị mặc định cho tham số ((a = 1))"},
		{"const list = []\nfor (const { a } of list) { }", "<source>:2:12: Kitwork chưa có tách trong for"},
		{"const o = {}\nconst p = {}\n;({ a: o.x } = p)", "<source>:3:8: Kitwork chưa có gán vào o.x khi tách"},
		{"const o = {}\nconst p = {}\n;({ ...o } = p)", "<source>:3:8: Kitwork chưa có phần còn lại khi tách"},
		{"const r = [...list]", "<source>:1:12: Kitwork chưa có ... ở chỗ này"},
	}
	for _, row := range rows {
		if err := grammarCompileError(t, row.source); !strings.Contains(err, row.want) {
			t.Errorf("%q\nerror: %s\nwant it to contain: %s", row.source, err, row.want)
		}
	}
}
