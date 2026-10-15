package highlight

import "testing"

func init() {
	hostileFragments = append(hostileFragments,
		"'\\''", "`\\`", "0x1p-2i", "1.e", "&^=", "type (", "func (", "x.(type)",
	)
}

func TestGo(t *testing.T) {
	checkExact(t, "go", []tokenCase{
		{"package main", tokensOf(Keyword, "package", Plain, " main")},

		// A method: the receiver is not the name, the name is a call's shape.
		{"func (r *Reader) Read(p []byte) (int, error)", tokensOf(
			Keyword, "func", Plain, " ", Punctuation, "(", Plain, "r ", Operator, "*", Plain, "Reader",
			Punctuation, ")", Plain, " ", Function, "Read", Punctuation, "(", Plain, "p ", Punctuation, "[]",
			Type, "byte", Punctuation, ")", Plain, " ", Punctuation, "(", Type, "int", Punctuation, ",",
			Plain, " ", Type, "error", Punctuation, ")",
		)},

		// Builtin functions are functions when called; Go allows no other use, so
		// an uncalled `len` is a variable that shadows the builtin.
		{"n := len(s) + cap(s)\nlen := 3", tokensOf(
			Plain, "n ", Operator, ":=", Plain, " ", Function, "len", Punctuation, "(", Plain, "s",
			Punctuation, ")", Plain, " ", Operator, "+", Plain, " ", Function, "cap", Punctuation, "(",
			Plain, "s", Punctuation, ")", Plain, "\nlen ", Operator, ":=", Plain, " ", Number, "3",
		)},

		// Builtin types are types, called (a conversion) or not.
		{"b := []byte(s)", tokensOf(
			Plain, "b ", Operator, ":=", Plain, " ", Punctuation, "[]", Type, "byte", Punctuation, "(",
			Plain, "s", Punctuation, ")",
		)},

		{"x := nil == true || iota != false", tokensOf(
			Plain, "x ", Operator, ":=", Plain, " ", Constant, "nil", Plain, " ", Operator, "==", Plain, " ",
			Constant, "true", Plain, " ", Operator, "||", Plain, " ", Constant, "iota", Plain, " ",
			Operator, "!=", Plain, " ", Constant, "false",
		)},

		// Raw strings span lines and have no escapes; unclosed, they run to the end.
		{"s := `a\\n\nb\"` + `open\nnext", tokensOf(
			Plain, "s ", Operator, ":=", Plain, " ", String, "`a\\n\nb\"`", Plain, " ", Operator, "+",
			Plain, " ", String, "`open\nnext",
		)},

		// An interpreted string keeps an escaped quote; unclosed, it stops at the
		// line's end.
		{"\"a\\\"b\" \"open\nx", tokensOf(String, "\"a\\\"b\"", Plain, " ", String, "\"open", Plain, "\nx")},

		{"'\\'' 'é' '\\x41'", tokensOf(String, "'\\''", Plain, " ", String, "'é'", Plain, " ", String, "'\\x41'")},

		// Imaginary numbers, hexadecimal floats, separators, a trailing dot.
		{"0x1p-2 1_000 2.5e3i 1. 0b1010 0o17 .5 2i 017", tokensOf(
			Number, "0x1p-2", Plain, " ", Number, "1_000", Plain, " ", Number, "2.5e3i", Plain, " ",
			Number, "1.", Plain, " ", Number, "0b1010", Plain, " ", Number, "0o17", Plain, " ",
			Number, ".5", Plain, " ", Number, "2i", Plain, " ", Number, "017",
		)},

		{"// line\n/* block\n */x /* open", tokensOf(
			Comment, "// line", Plain, "\n", Comment, "/* block\n */", Plain, "x ", Comment, "/* open",
		)},

		// The longest operator wins.
		{"a &^= b <- c... d >>= e && !f", tokensOf(
			Plain, "a ", Operator, "&^=", Plain, " b ", Operator, "<-", Plain, " c", Operator, "...",
			Plain, " d ", Operator, ">>=", Plain, " e ", Operator, "&&", Plain, " ", Operator, "!", Plain, "f",
		)},

		// In a type group, the first name on a line declares a type; inside a
		// struct it is a field, and after the group it is a name again, even
		// first on a line inside brackets as deep as the group's were.
		{"type (\n\tA struct {\n\t\tB int\n\t}\n\tC = D\n)\nE := f(\n\tx)", tokensOf(
			Keyword, "type", Plain, " ", Punctuation, "(", Plain, "\n\t", Type, "A", Plain, " ",
			Keyword, "struct", Plain, " ", Punctuation, "{", Plain, "\n\t\tB ", Type, "int", Plain, "\n\t",
			Punctuation, "}", Plain, "\n\t", Type, "C", Plain, " ", Operator, "=", Plain, " D\n",
			Punctuation, ")", Plain, "\nE ", Operator, ":=", Plain, " ", Function, "f", Punctuation, "(",
			Plain, "\n\tx", Punctuation, ")",
		)},

		{"func Map[K comparable, V any](m map[K]V) []V", tokensOf(
			Keyword, "func", Plain, " ", Function, "Map", Punctuation, "[", Plain, "K ", Type, "comparable",
			Punctuation, ",", Plain, " V ", Type, "any", Punctuation, "](", Plain, "m ", Keyword, "map",
			Punctuation, "[", Plain, "K", Punctuation, "]", Plain, "V", Punctuation, ")", Plain, " ",
			Punctuation, "[]", Plain, "V",
		)},

		{"type List[T any] struct{}", tokensOf(
			Keyword, "type", Plain, " ", Type, "List", Punctuation, "[", Plain, "T ", Type, "any",
			Punctuation, "]", Plain, " ", Keyword, "struct", Punctuation, "{}",
		)},

		// `type` in a type switch declares nothing.
		{"switch v := x.(type) {\ncase int, string:\n}", tokensOf(
			Keyword, "switch", Plain, " v ", Operator, ":=", Plain, " x", Punctuation, ".(", Keyword, "type",
			Punctuation, ")", Plain, " ", Punctuation, "{", Plain, "\n", Keyword, "case", Plain, " ",
			Type, "int", Punctuation, ",", Plain, " ", Type, "string", Punctuation, ":", Plain, "\n",
			Punctuation, "}",
		)},

		{"go func() { ch <- struct{}{} }()", tokensOf(
			Keyword, "go", Plain, " ", Keyword, "func", Punctuation, "()", Plain, " ", Punctuation, "{",
			Plain, " ch ", Operator, "<-", Plain, " ", Keyword, "struct", Punctuation, "{}{}", Plain, " ",
			Punctuation, "}()",
		)},

		{"giá := 1\nouter:\n\tbreak outer", tokensOf(
			Plain, "giá ", Operator, ":=", Plain, " ", Number, "1", Plain, "\nouter", Punctuation, ":",
			Plain, "\n\t", Keyword, "break", Plain, " outer",
		)},

		{"Name string `json:\"name\"`", tokensOf(Plain, "Name ", Type, "string", Plain, " ", String, "`json:\"name\"`")},

		{"fmt.Println(strings.TrimSpace(s))", tokensOf(
			Plain, "fmt", Punctuation, ".", Function, "Println", Punctuation, "(", Plain, "strings",
			Punctuation, ".", Function, "TrimSpace", Punctuation, "(", Plain, "s", Punctuation, "))",
		)},

		// Bytes Go has no use for are plain, line endings included.
		{"x\r\n\x00@#$\xff?", tokensOf(Plain, "x\r\n\x00@#$\xff?")},
		{"", nil},
	})
}

func TestGoSample(t *testing.T) {
	samples := fixturesFor(t, "go")
	if len(samples) == 0 {
		t.Fatal("testdata/go/ has no sample")
	}
	checkContains(t, "go", []tokenCase{{samples[0].source, tokensOf(
		Keyword, "package",
		Number, "0x1p-10",
		Number, "2i",
		String, "'\\''",
		Constant, "iota",
		Type, "Token",
		String, "`json:\"kind\"`",
		Type, "Reader",
		Type, "Visit",
		Type, "Cache",
		Function, "Get",
		Keyword, "defer",
		Function, "Split",
		Function, "recover",
		Keyword, "continue",
		Function, "ScanLines",
		Operator, "<-",
		String, "`menu: one item per line\n\tprice after the name, # starts a comment \\n stays two characters`",
		Operator, "&^",
	)}})
}
