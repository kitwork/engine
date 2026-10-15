package compiler

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
)

// The reserved words of the language: every keyword of the lexer plus the words the parser gives a
// meaning at the start of a statement (continue since 14/10/2026) or before an operand (typeof, the same
// day). The tables below run over this list, and TestReservedWordListIsPinned holds it equal to the
// parser's own maps, so a keyword added or removed shows up as a red test that names it, not as a word
// nobody checked.
var pinnedReservedWords = []string{
	"break", "case", "catch", "class", "const", "continue", "default", "do", "else", "export", "false",
	"finally", "for", "function", "if", "import", "let", "new", "null", "return", "switch",
	"throw", "true", "try", "typeof", "undefined", "void", "while",
}

func reservedWords() []string {
	words := make([]string, 0, len(Keywords)+len(statementWords)+len(operatorWords))
	for word := range Keywords {
		words = append(words, word)
	}
	for word := range statementWords {
		words = append(words, word)
	}
	for word := range operatorWords {
		words = append(words, word)
	}
	sort.Strings(words)
	return words
}

func TestReservedWordListIsPinned(t *testing.T) {
	if got := reservedWords(); !reflect.DeepEqual(got, pinnedReservedWords) {
		t.Fatalf("reserved words = %q\nwant %q\n(a keyword was added or removed: cover it in the tables below)", got, pinnedReservedWords)
	}
}

// compileBoth compiles two sources that must mean the same program.
func compileBoth(t *testing.T, got, want string) (*Bytecode, *Bytecode) {
	t.Helper()
	gotBytecode, err := CompileSource(got)
	if err != nil {
		t.Fatalf("compile %q: %v", got, err)
	}
	wantBytecode, err := CompileSource(want)
	if err != nil {
		t.Fatalf("compile %q: %v", want, err)
	}
	return gotBytecode, wantBytecode
}

func assertSameCode(t *testing.T, got, want *Bytecode, gotSource, wantSource string) {
	t.Helper()
	if !reflect.DeepEqual(got.Instructions(), want.Instructions()) {
		t.Fatalf("instructions differ\n%q: % x\n%q: % x", gotSource, got.Instructions(), wantSource, want.Instructions())
	}
	if !reflect.DeepEqual(got.Constants(), want.Constants()) {
		t.Fatalf("constants differ\n%q: %v\n%q: %v", gotSource, got.Constants(), wantSource, want.Constants())
	}
}

func resultJSON(t *testing.T, source string) string {
	t.Helper()
	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile %q: %v", source, err)
	}
	vm := runtime.New(bytecode.Program)
	if returned := vm.Run(); returned.IsInvalid() {
		t.Fatalf("run %q: %s", source, returned.Text())
	}
	data, err := json.Marshal(vm.Vars["result"].Interface())
	if err != nil {
		t.Fatalf("encode result of %q: %v", source, err)
	}
	return string(data)
}

// A reserved word is a property name, as in JavaScript: before ':' in an object and after a dot it
// compiles exactly as the quoted key / the bracket access. The control rows (delete, var, this…:
// identifiers in Kitwork, reserved in JavaScript) always compiled this way.
func TestReservedWordIsAPropertyName(t *testing.T) {
	reserved := pinnedReservedWords
	controls := []string{"delete", "var", "this", "in", "of", "as", "from", "name"}
	for _, word := range append(append([]string{}, reserved...), controls...) {
		word := word
		t.Run(word, func(t *testing.T) {
			// Object key. Two spaces pad the bare key to the quoted key's width, so every position after
			// it lines up. A keyword of the lexer becomes the quoted key's own node, so even the debug
			// table matches and the whole Program checksum is compared; an identifier key (break, case,
			// default and the control rows) keeps its older node, which has no debug entry of its own.
			bare := fmt.Sprintf("const o = { %s  : 1 }", word)
			quoted := fmt.Sprintf("const o = { %q: 1 }", word)
			got, want := compileBoth(t, bare, quoted)
			assertSameCode(t, got, want, bare, quoted)
			if _, keyword := Keywords[word]; keyword && got.Checksum() != want.Checksum() {
				t.Fatalf("checksum of %q = %s, of %q = %s", bare, got.Checksum(), quoted, want.Checksum())
			}

			// Read after a dot, and write through one.
			dot := fmt.Sprintf("const o = { a: 1 }\no.%s = 2\nconst result = o.%s", word, word)
			bracket := fmt.Sprintf("const o = { a: 1 }\no[%q] = 2\nconst result = o[%q]", word, word)
			got, want = compileBoth(t, dot, bracket)
			assertSameCode(t, got, want, dot, bracket)

			// The key is the word as written (undefined stays "undefined", not null), and a method under
			// it is called through the dot. The control rows too: delete is also a builtin method of
			// every object (value/kind.go), and since 13/10/2026 an object's own key wins over it, so
			// o.delete and o["delete"] read the key back.
			if got, want := resultJSON(t, fmt.Sprintf("const result = { %s: 1 }", word)), fmt.Sprintf(`{"%s":1}`, word); got != want {
				t.Fatalf("{ %s: 1 } = %s, want %s", word, got, want)
			}
			method := fmt.Sprintf("const o = { %s: (x) => x + 1 }\nconst result = [o.%s(41), o[%q](1)]", word, word, word)
			if got := resultJSON(t, method); got != "[42,2]" {
				t.Fatalf("%q = %s, want [42,2]", method, got)
			}

			// An import names a property of the module: `import { word as f }` is `const f = ….word`.
			imported := fmt.Sprintf("import { %s as f } from \"kitwork/http\"", word)
			member := fmt.Sprintf("const f = kitwork().http.%s", word)
			got, want = compileBoth(t, imported, member)
			assertSameCode(t, got, want, imported, member)
		})
	}
}

// The case that made every page of a site 404: a router call with a keyword key.
func TestReservedWordKeyInARouterCall(t *testing.T) {
	got := resultJSON(t, `
const router = { highlight: (theme) => theme }
const theme = router.highlight({ function: { color: "brand" }, default: { color: "ink" } })
const result = theme.function.color + "/" + theme.default.color
`)
	if got != `"brand/ink"` {
		t.Fatalf("result = %s, want \"brand/ink\"", got)
	}
}

// true/false/null/undefined are values first: they still pass as arguments and elements. Control for
// the parameter check, which must not catch a literal followed by ',' or ')'.
func TestLiteralKeywordsAreStillValues(t *testing.T) {
	got := resultJSON(t, `
const pick = (a, b, c, d) => [a, b, c, d]
const result = pick(true, false, null, undefined)
`)
	if got != "[true,false,null,null]" {
		t.Fatalf("result = %s, want [true,false,null,null]", got)
	}
	// Every position where another keyword is an error (TestReservedWordIsNotAValue) still takes a
	// literal word as a value.
	for word := range literalWords {
		for _, form := range valueForms {
			source := strings.ReplaceAll(form.template, "@", word)
			if _, err := CompileSource(source); err != nil {
				t.Fatalf("%s: %q: %v", form.name, source, err)
			}
		}
	}
	if got := resultJSON(t, "const result = [true, `x${false}`, [null], undefined]"); got != `[true,"xfalse",[null],null]` {
		t.Fatalf("literal words as values = %s", got)
	}
}

// reservedForm is a source template: the first '@' marks where the reserved word goes and where the
// error must point; hint is how the message says to write it ('@' again stands for the word).
type reservedForm struct {
	name     string
	template string
	hint     string
}

const propertyHint = `làm khoá object thì viết thẳng { @: … } hoặc { "@": … }, đọc thì o.@`

// assertReservedWordError compiles the form with word in place and asserts the reserved-word message,
// with the hint, at the marker's line:column.
func assertReservedWordError(t *testing.T, form reservedForm, word string) {
	t.Helper()
	marker := strings.Index(form.template, "@")
	line := 1 + strings.Count(form.template[:marker], "\n")
	column := marker - strings.LastIndex(form.template[:marker], "\n")
	source := strings.ReplaceAll(form.template, "@", word)

	_, err := CompileSource(source)
	if err == nil {
		t.Fatalf("%q compiled; a reserved word cannot name a variable", source)
	}
	hint := form.hint
	if hint == "" {
		hint = propertyHint
	}
	want := fmt.Sprintf("<source>:%d:%d: %s là từ khoá của ngôn ngữ, không làm tên biến được; %s",
		line, column, word, strings.ReplaceAll(hint, "@", word))
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%q\nerror: %v\nwant it to contain: %s", source, err, want)
	}
}

// Where a reserved word would name a variable it stays a compile error, which says the word is reserved,
// how to write it instead, and where it stands as file:line:col.
func TestReservedWordIsNotABindingName(t *testing.T) {
	forms := []reservedForm{
		{"const", "const @ = 1", ""},
		{"let", "let @ = 1", ""},
		{"destructured-object", "const { a, @ } = { a: 1 }", ""},
		{"destructured-array", "const [a, @] = [1, 2]", ""},
		{"arrow-parameter", "const f = (@) => 1", ""},
		{"arrow-second-parameter", "const f = (a, @) => 1", ""},
		{"arrow-bare-parameter", "const f = @ => 1", ""},
		{"function-parameter", "function f(a, @) { return 1 }", ""},
		{"function-expression-parameter", "const f = function (@) { return 1 }", ""},
		{"function-name", "function @() { return 1 }", ""},
		{"function-expression-name", "const f = function @() { return 1 }", ""},
		{"for-of", "for (const @ of [1]) { }", ""},
		{"for-counter", "for (let @ = 0; @ < 1; @++) { }", ""},
		// An import names a variable too, but the way out is another name, not an object key.
		{"import-named", "import { @ } from \"kitwork\"", "đặt tên khác: import { @ as tên_khác } from …"},
		{"import-alias", "import { http as @ } from \"kitwork\"", "đặt tên khác: import { http as tên_khác } from …"},
		{"import-default", "import @ from \"./module.kitwork.js\"", "đặt tên khác: import tên_khác from …"},
		{"template-interpolation", "const s = `${ (@) => 1 }`", ""},
		// An escape is shorter once unescaped; the column still counts the source as written.
		{"template-after-escape", "const s = `\\u00e9\\u00e9${ (@) => 1 }`", ""},
		{"template-next-line-after-escape", "const s = `\\n\\n\n${ (@) => 1 }`", ""},
		{"second-line", "const a = 1\nconst @ = 2", ""},
	}
	for _, word := range pinnedReservedWords {
		for _, form := range forms {
			word, form := word, form
			t.Run(word+"/"+form.name, func(t *testing.T) {
				assertReservedWordError(t, form, word)
			})
		}
	}
}

// The words that are not values (all but true/false/null/undefined) cannot stand alone in an expression
// either: as an element, a spread, a computed key, a bare interpolation or a whole value, they would read
// a variable that cannot exist. Every one of them gets the reserved-word message, not a parser error.
func TestReservedWordIsNotAValue(t *testing.T) {
	for _, word := range pinnedReservedWords {
		if literalWords[word] {
			continue
		}
		for _, form := range valueForms {
			word, form := word, form
			t.Run(word+"/"+form.name, func(t *testing.T) {
				assertReservedWordError(t, form, word)
			})
		}
	}
}

var literalWords = map[string]bool{"true": true, "false": true, "null": true, "undefined": true}

var valueForms = []reservedForm{
	{"whole-value", "const r = @", ""},
	{"before-semicolon", "const r = @;", ""},
	{"array-element", "const r = [@]", ""},
	{"array-first-element", "const r = [@, 1]", ""},
	{"call-argument", "const r = f(@)", ""},
	{"spread", "const r = { ...@ }", ""},
	{"computed-key", "const r = { [@]: 1 }", ""},
	{"template-bare", "const r = `${@}`", ""},
}

// break, case and default are identifiers to the lexer, so the parser could read and assign them as
// variables: y = default = 5 compiled and stored 5. They mean something only at the start of a statement.
func TestStatementWordIsNotAVariable(t *testing.T) {
	forms := []reservedForm{
		{"assign-chain", "let y = 0\ny = @ = 5", ""},
		{"assign-in-group", "const r = (@ = 7) + 1", ""},
		{"array-pattern", "let r = 0\n;[r, @] = [1, 8]", ""},
		{"read-in-sum", "const r = @ + 1", ""},
	}
	for word := range statementWords {
		for _, form := range forms {
			word, form := word, form
			t.Run(word+"/"+form.name, func(t *testing.T) {
				assertReservedWordError(t, form, word)
			})
		}
	}
}

// ({ k: x } = o) compiled to nothing until 13/10/2026, then was an error; since 14/10/2026 it renames,
// keyword keys included, as in JavaScript. ({ ...rest } = o) is still an error that says how to write it.
func TestObjectAssignmentPatternRenames(t *testing.T) {
	rows := []struct{ source, want string }{
		{"let x = 0\n;({ function: x } = { function: 5 })\nconst result = x", "5"},
		{"let a = 0\nlet b = 0\n;({ new: a, class: b } = { new: 1, class: 2 })\nconst result = [a, b]", "[1,2]"},
		{"let r = 0\n;({ default: r } = { default: 11 })\nconst result = r", "11"},
		{"let a = 0\nlet x = 0\n;({ a: x } = { a: 5 })\nconst result = [a, x]", "[0,5]"},
	}
	for _, row := range rows {
		if got := resultJSON(t, row.source); got != row.want {
			t.Fatalf("%q gives %s, want %s", row.source, got, row.want)
		}
	}
	_, err := CompileSource("let a = 0\n;({ ...a } = { a: 5 })")
	want := "<source>:2:8: Kitwork chưa có phần còn lại khi tách (...tên)"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("({ ...a } = …): error %v, want it to contain %s", err, want)
	}
	// Control: the shorthand still assigns.
	if got := resultJSON(t, "let a = 0\nlet b = 0\n;({ a, b } = { a: 5, b: 6 })\nconst result = [a, b]"); got != "[5,6]" {
		t.Fatalf("({ a, b } = …) gives %s, want [5,6]", got)
	}
}

// { function() { … } } is JavaScript's method shorthand, which Kitwork does not have; it used to compile
// to an object whose only key was "" holding the function. A function standing where a key goes is an
// error too.
func TestObjectMethodShorthandIsAnError(t *testing.T) {
	for _, word := range pinnedReservedWords {
		source := fmt.Sprintf("const o = { %s() { return 1 } }", word)
		_, err := CompileSource(source)
		if err == nil {
			t.Fatalf("%q compiled", source)
		}
		want := fmt.Sprintf("<source>:1:13: Kitwork chưa có phương thức viết tắt trong object ({ %s() { … } }); viết { %s: (…) => … }", word, word)
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%q\nerror: %v\nwant it to contain: %s", source, err, want)
		}
	}
	for _, source := range []string{
		"const o = { function named() { return 1 } }",
		"const o = { (x) => 1 }",
		"const o = { x => 1 }",
	} {
		_, err := CompileSource(source)
		if err == nil {
			t.Fatalf("%q compiled", source)
		}
		want := "<source>:1:13: Kitwork chưa có phương thức viết tắt trong object; hàm phải đứng sau một khoá: { tên: (…) => … }"
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%q\nerror: %v\nwant it to contain: %s", source, err, want)
		}
	}
}

// { word } is shorthand for { word: word }: it reads a variable that cannot exist.
func TestReservedWordShorthandPropertyIsAnError(t *testing.T) {
	for _, word := range pinnedReservedWords {
		for _, template := range []string{"const x = { @ }", "const x = { a: 1, @ }", "const x = {\n  @,\n  a: 1\n}"} {
			marker := strings.Index(template, "@")
			line := 1 + strings.Count(template[:marker], "\n")
			column := marker - strings.LastIndex(template[:marker], "\n")
			source := strings.ReplaceAll(template, "@", word)
			_, err := CompileSource(source)
			if err == nil {
				t.Fatalf("%q compiled; { %s } would read a variable named %s", source, word, word)
			}
			want := fmt.Sprintf("<source>:%d:%d: { %s } là viết tắt của { %s: %s }, tức đọc biến tên %s; mà %s là từ khoá của ngôn ngữ, không làm tên biến được",
				line, column, word, word, word, word, word)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%q\nerror: %v\nwant it to contain: %s", source, err, want)
			}
		}
	}
}

// const { a: b } = o renames (14/10/2026; an error until then), with a reserved word as the key too, as
// after a dot. The variable cannot be a reserved word.
func TestRenamingDestructureTakesReservedKeys(t *testing.T) {
	for _, word := range []string{"function", "default", "name"} {
		source := fmt.Sprintf("const o = { %s: 7 }\nconst { %s: f } = o\nconst result = f", word, word)
		if got := resultJSON(t, source); got != "7" {
			t.Fatalf("%q gives %s, want 7", source, got)
		}
	}
	_, err := CompileSource("const o = {}\nconst { a: function } = o")
	want := "<source>:2:12: function là từ khoá của ngôn ngữ, không làm tên biến được"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("const { a: function } = o: error %v, want it to contain %s", err, want)
	}
}
