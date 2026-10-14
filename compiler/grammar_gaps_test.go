package compiler

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// compileError compiles source and gives its error text, "" when it compiled.
func compileError(source string) string {
	if _, err := CompileSource(source); err != nil {
		return err.Error()
	}
	return ""
}

// A return inside for…of compiles and returns (14/10/2026): the loop's iterable and index go first,
// as a break drops them. It was the verifier's refusal ("RETURN retains 3 stack values") for the
// commonest search a function writes. Nested loops, a switch inside the loop and a closure too.
func TestReturnInsideForOf(t *testing.T) {
	source := `const first = (list) => {
  for (const y of list) {
    if (y) return y;
  }
  return null;
};
const nested = (rows) => {
  for (const row of rows) {
    for (const cell of row) {
      if (cell > 2) return cell;
    }
  }
  return -1;
};
const picked = (list) => {
  for (const item of list) {
    switch (item) {
    case "b":
      return "found " + item;
    }
  }
  return "none";
};
const later = (list) => {
  for (const item of list) {
    const read = () => item;
    if (item == 2) return read();
  }
  return 0;
};
const result = [first([0, 3, 4]), first([0]), nested([[1], [2, 5]]), nested([[1]]), picked(["a", "b"]), picked([]), later([1, 2, 3])];`
	if got, want := runOwnKey(t, source, nil), `[3,null,5,-1,"found b","none",2]`; got != want {
		t.Fatalf("result = %s, want %s", got, want)
	}
}

// continue is a compile error saying how to write the loop (14/10/2026): it was a name nothing
// declares, so continue; did nothing and the rest of the body ran. CONTROL: it stays a property name.
func TestContinueIsACompileError(t *testing.T) {
	for _, source := range []string{
		"const out = [];\nfor (const x of [1, 2, 3]) {\n  if (x == 1) { continue; }\n  out.push(x);\n}",
		"for (let i = 0; i < 3; i++) {\n  if (i == 1) { continue }\n}",
	} {
		if got := compileError(source); !strings.Contains(got, "<source>:") || !strings.Contains(got, "Kitwork chưa có continue: viết phần còn lại của thân vòng trong if") {
			t.Errorf("%q = %q, want the continue error at file:line:column", source, got)
		}
	}
	if got := compileError("const out = [];\nfor (const x of [1]) {\n  if (x == 1) { continue; }\n}"); !strings.HasPrefix(strings.TrimPrefix(got, "assemble error: "), "<source>:3:17:") {
		t.Errorf("the place of continue = %q, want <source>:3:17", got)
	}
	if got, want := runOwnKey(t, "const o = { continue: 1 }\nconst result = [o.continue, o[\"continue\"]]", nil), "[1,1]"; got != want {
		t.Fatalf("CONTROL: continue as a key = %s, want %s", got, want)
	}
}

// A const is never assigned (14/10/2026), as in JavaScript, where the write throws: it is a compile
// error naming the write and the declaration. The write used to happen and the program went on.
// CONTROLS: let, for (let … of …), a parameter and a function declaration may be assigned.
func TestConstIsNeverAssigned(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const a = 1;\na = 2;", "<source>:2:1: a là hằng (const), khai báo ở <source>:1:7, không gán lại được"},
		{"const a = 1;\na++;", "<source>:2:1: a là hằng (const)"},
		{"const a = 1;\na += 5;", "<source>:2:1: a là hằng (const)"},
		{"for (const i = 0; i < 3; i++) {}", "i là biến đếm khai báo bằng const, khai báo ở <source>:1:12"},
		{"for (const x of [1, 2]) {\n  x = x * 10;\n}", "<source>:2:3: x là biến của for (const … of …), khai báo ở <source>:1:12"},
		{"const a = 1;\n({ a } = { a: 2 });", "<source>:2:4: a là hằng (const)"},
		{"const limit = 10;\nconst reset = () => { limit = 0; };", "<source>:2:23: limit là hằng (const)"},
		{"const { a, b: c } = { a: 1, b: 2 };\nc = 3;", "<source>:2:1: c là hằng (const), khai báo ở <source>:1:15"},
	} {
		if got := compileError(c.source); !strings.Contains(got, c.want) {
			t.Errorf("%q = %q\nwant %s", c.source, got, c.want)
		}
	}
	controls := "let a = 1;\na = 2;\na++;\nconst seen = [];\nfor (let x of [1, 2]) {\n  x = x * 10;\n  seen.push(x);\n}\n" +
		"const f = (p) => { p = p + 1; return p; };\nfunction g() { return 1; }\ng = () => 7;\nconst result = [a, seen, f(1), g()];"
	if got, want := runOwnKey(t, controls, nil), "[3,[10,20],2,7]"; got != want {
		t.Fatalf("CONTROLS = %s, want %s", got, want)
	}
}

// { a = 1 } in an object (not a pattern) is a compile error (14/10/2026), JavaScript's SyntaxError: it
// assigned the variable a and made a key "1". CONTROL: the same text as a pattern is a default.
func TestObjectShorthandWithDefaultIsAnError(t *testing.T) {
	if got := compileError("let a = 0;\nconst o = { a = 1 };"); !strings.Contains(got, "<source>:2:13: { a = … } chỉ là giá trị mặc định khi tách (const { a = … } = o); trong một object viết { a: … }") {
		t.Fatalf("{ a = 1 } = %q", got)
	}
	if got, want := runOwnKey(t, "const { a = 1 } = {};\nlet b = 0;\n({ b = 5 } = {});\nconst result = [a, b];", nil), "[1,5]"; got != want {
		t.Fatalf("CONTROL: the pattern = %s, want %s", got, want)
	}
}

// A template is text (14/10/2026): one that starts with two values, or is one value, joins text
// instead of adding numbers or staying a number; a brace inside a string of ${…} is text.
// CONTROL: a template that starts with text, as before.
func TestTemplateIsText(t *testing.T) {
	source := "const x = 1;\nconst y = 2;\nconst a = null;\nconst result = [`${x}${y}`, `${5}`, `${a ?? 1}${a || 2}`, `a${\"}\"}b`, `a${\"{\"}b`, `p${x}${y}`, `${x} and ${y}`];"
	if got, want := runOwnKey(t, source, nil), `["12","5","12","a}b","a{b","p12","1 and 2"]`; got != want {
		t.Fatalf("templates = %s, want %s", got, want)
	}
}

// Kitwork has no comma operator (14/10/2026): (a, b) not followed by => is a compile error. It kept a
// and dropped b without a word. CONTROL: (a, b) => … is a function of two parameters.
func TestCommaOperatorIsACompileError(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const r = (1, 2);", "<source>:1:15: Kitwork không có toán tử dấu phẩy"},
		{"let n = 0;\nconst next = () => (n = n + 1, n * 10);", "<source>:2:32: Kitwork không có toán tử dấu phẩy"},
		{"let f = null;\nfor (let i = (f = () => 0, 0); i < 2; i++) {}", "<source>:2:28: Kitwork không có toán tử dấu phẩy"},
	} {
		if got := compileError(c.source); !strings.Contains(got, c.want) {
			t.Errorf("%q = %q\nwant %s", c.source, got, c.want)
		}
	}
	if got, want := runOwnKey(t, "const add = (a, b) => a + b;\nconst result = add(2, 3);", nil), "5"; got != want {
		t.Fatalf("CONTROL: (a, b) => … = %s, want %s", got, want)
	}
}

// typeof, instanceof, delete, in and for…in are compile errors saying how to write them (14/10/2026):
// each was a name nothing declares, and the rest of the line another statement. CONTROL: they stay
// property names, and o.delete("k") is the method.
func TestWordsKitworkLacksAreCompileErrors(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const x = \"s\";\nconst r = typeof x === \"string\";", "<source>:2:11: Kitwork chưa có typeof"},
		{"const x = 1;\nif (typeof x === \"number\") {}", "<source>:2:5: Kitwork chưa có typeof"},
		{"const r = [] instanceof Array;", "<source>:1:14: Kitwork chưa có instanceof"},
		{"const o = { a: 1 };\ndelete o.a;", "<source>:2:1: Kitwork chưa có delete: bỏ một khoá thì o.delete(\"khoá\")"},
		{"const o = { a: 1 };\nconst r = \"a\" in o;", "<source>:2:15: Kitwork chưa có toán tử in"},
		{"const o = { a: 1 };\nfor (const k in o) {}", "<source>:2:14: Kitwork chưa có for…in"},
	} {
		if got := compileError(c.source); !strings.Contains(got, c.want) {
			t.Errorf("%q = %q\nwant %s", c.source, got, c.want)
		}
	}
	source := "const o = { typeof: 1, instanceof: 2, in: 3, a: 4 };\no.delete(\"a\");\nconst result = [o.typeof, o.instanceof, o.in, o.has(\"a\")];"
	if got, want := runOwnKey(t, source, nil), "[1,2,3,false]"; got != want {
		t.Fatalf("CONTROL: the words as keys = %s, want %s", got, want)
	}
}

// What the parser says it cannot read leads with file:line:column (14/10/2026; it gave a byte offset in
// English), and the forms JavaScript has that were generic errors now read: let y; is null, a block
// stands on its own, .5 is a number (a ?.5 : 1 is the ternary). A pattern or a const with no value,
// and a literal as the target of a pattern part, say what to write.
func TestParserSaysWhereAndReadsTheForms(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const r = 1 ??;", "<source>:1:15: không đọc được \";\" ở đầu một biểu thức"},
		{"const r = f(1;", "<source>:1:14: cần ) ở đây, gặp \";\""},
		{"const { a };", "<source>:1:12: mẫu tách cần một giá trị để tách: const { a } = …"},
		{"const y;", "<source>:1:8: const cần giá trị ngay khi khai báo, như JavaScript: const y = …"},
		{"const o = {};\n({ a: 1 } = o);", "<source>:2:7: không gán được vào 1 khi tách: sau dấu hai chấm là tên nhận giá trị"},
	} {
		if got := compileError(c.source); !strings.Contains(got, c.want) || strings.Contains(got, "at pos") {
			t.Errorf("%q = %q\nwant %s", c.source, got, c.want)
		}
	}
	source := "let y;\nlet z = 0;\n{\n  const z = 5;\n  y = z;\n}\nconst a = null;\nconst t = a ?.5 : 1;\nconst u = a?.5:1;\nswitch (1) {\ncase 1: {\n  const inner = 2;\n  z = inner;\n}\n}\nconst result = [y, z, t, u, .5 + 1, a?.b];"
	if got, want := runOwnKey(t, source, nil), "[5,2,1,1,1.5,null]"; got != want {
		t.Fatalf("the forms = %s, want %s", got, want)
	}
	if got, want := runOwnKey(t, "let y;\nconst result = y;", nil), "null"; got != want {
		t.Fatalf("let y; = %s, want null", got)
	}
}

// Code nested past nestingLimit is a compile error (14/10/2026), wherever it nests: parentheses, a
// chain, an operator run, ! after !, arrays in arrays. The parser and every walker after it recurse
// as deep as the code nests, and a file of nested parentheses ended the shared host with a Go stack
// overflow at boot. Under a 64 MiB stack the deep forms are a clean error. CONTROL: 900 levels compile.
func TestNestingIsBounded(t *testing.T) {
	saved := debug.SetMaxStack(64 << 20)
	defer debug.SetMaxStack(saved)
	deep := 200000
	for name, source := range map[string]string{
		"parentheses": "const x = " + strings.Repeat("(", deep) + "1" + strings.Repeat(")", deep) + ";",
		"chain":       "const o = {};\nconst x = o" + strings.Repeat(".a", deep) + ";",
		"sum":         "const x = 1" + strings.Repeat(" + 1", deep) + ";",
		"not":         "const x = " + strings.Repeat("!", deep) + "true;",
		"arrays":      "const x = " + strings.Repeat("[", deep) + strings.Repeat("]", deep) + ";",
		"blocks":      strings.Repeat("{", deep) + strings.Repeat("}", deep),
		"assignments": "let a = 0;\n" + strings.Repeat("a = ", deep) + "1;",
	} {
		if got := compileError(source); !strings.Contains(got, "mã lồng quá sâu (hơn 1000 tầng") || !strings.Contains(got, "<source>:") {
			t.Errorf("%s: %.200s, want the nesting error", name, got)
		}
	}
	control := "const x = " + strings.Repeat("(", 900) + "1" + strings.Repeat(")", 900) + " + 1;\nconst result = x;"
	if got := runOwnKey(t, control, nil); got != "2" {
		t.Fatalf("CONTROL: 900 parentheses = %s, want 2", got)
	}
}

// positionHost is a host object whose status read fails, as a request with no answer does.
type positionHost struct{ Inner *positionHost }

func (h *positionHost) Status() value.Value {
	return value.InvalidFailure(value.NetworkCode, "no answer")
}

// An operation that fails fails at its operator, and a read that fails at its dot (14/10/2026): they
// took the place of the last thing compiled before them, the right operand or where the chain started,
// a line away when the code is written over several. CONTROL: a failure on one line.
func TestFailureIsAtItsOperatorAndItsRead(t *testing.T) {
	host := map[string]value.Value{"host": value.New(&positionHost{Inner: &positionHost{}})}
	for _, c := range []struct{ setup, call, at string }{
		{"const o = {}", "o -\n  1", "<source>:2:24"},
		{"const o = {}\nconst g = (n) => n", "o - g(\n  2)", "<source>:3:24"},
		{"const o = {}", "o\n  * 2", "<source>:3:3"},
		{"const o = {}", "-o", "<source>:2:22"},
		{"", "host\n  .inner\n  .status", "<source>:3:3"},
		{"", "host.inner[\n  \"status\"]", "<source>:1:32"},
		{"const o = {}", "o - 1", "<source>:2:24"},
	} {
		_, code, at := runNotAFunction(t, c.setup, c.call, host)
		if at != c.at {
			t.Errorf("%q: %s at %s, want %s", c.call, code, at, c.at)
		}
	}
}
