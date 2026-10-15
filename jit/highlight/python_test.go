package highlight

import "testing"

func init() {
	hostileFragments = append(hostileFragments,
		"f'''{", `rb'''`, `f"{x!r:{y}}"`, `f"\N{`, `"\N{`, `rf"\{`, "@a.b.", "match (", "case _", "1..", "\\\n",
		`f"{"`, `f'{'`, `f"{'`, `f"{\"`, `f"{f"{`,
	)
}

func TestPython(t *testing.T) {
	checkExact(t, "python", []tokenCase{
		{"if x is not None and y in z: pass", tokensOf(
			Keyword, "if", Plain, " x ", Keyword, "is", Plain, " ", Keyword, "not", Plain, " ",
			Constant, "None", Plain, " ", Keyword, "and", Plain, " y ", Keyword, "in", Plain, " z",
			Punctuation, ":", Plain, " ", Keyword, "pass",
		)},

		// A name after def is a function, after class a type; `...` is a constant.
		{"class Line(Base):\n    def total(self) -> int: ...", tokensOf(
			Keyword, "class", Plain, " ", Type, "Line", Punctuation, "(", Plain, "Base", Punctuation, "):",
			Plain, "\n    ", Keyword, "def", Plain, " ", Function, "total", Punctuation, "(", Plain, "self",
			Punctuation, ")", Plain, " ", Operator, "->", Plain, " ", Type, "int", Punctuation, ":",
			Plain, " ", Constant, "...",
		)},

		// A decorator is `@` first on its line with its dotted name: a function.
		{"@property\n@functools.lru_cache(maxsize=None)\n    @staticmethod\ndef f(): pass", tokensOf(
			Function, "@property", Plain, "\n", Function, "@functools.lru_cache", Punctuation, "(",
			Plain, "maxsize", Operator, "=", Constant, "None", Punctuation, ")", Plain, "\n    ",
			Function, "@staticmethod", Plain, "\n", Keyword, "def", Plain, " ", Function, "f",
			Punctuation, "():", Plain, " ", Keyword, "pass",
		)},

		// Anywhere else, `@` multiplies matrices.
		{"c = a @ b\nc @= d", tokensOf(
			Plain, "c ", Operator, "=", Plain, " a ", Operator, "@", Plain, " b\nc ", Operator, "@=", Plain, " d",
		)},

		// Triple quotes span lines and hold single quotes; unclosed, they run to the end.
		{"\"\"\"doc\n\"quoted\" inside\"\"\" + '''a\nb''' + '''open\nstill", tokensOf(
			String, "\"\"\"doc\n\"quoted\" inside\"\"\"", Plain, " ", Operator, "+", Plain, " ",
			String, "'''a\nb'''", Plain, " ", Operator, "+", Plain, " ", String, "'''open\nstill",
		)},

		// One quote stops at the line's end unless a backslash continues it.
		{"'open\nx = \"a\\\nb\"", tokensOf(
			String, "'open", Plain, "\nx ", Operator, "=", Plain, " ", String, "\"a\\\nb\"",
		)},

		// Prefixes in any case; two letters need an r, and u takes no partner.
		{"rb'\\d' + Rb\"x\" + u'y' + BR'z' + ub'no' + ur'no' + bf'no' + f'' + Tr'q'", tokensOf(
			String, "rb'\\d'", Plain, " ", Operator, "+", Plain, " ", String, "Rb\"x\"", Plain, " ",
			Operator, "+", Plain, " ", String, "u'y'", Plain, " ", Operator, "+", Plain, " ",
			String, "BR'z'", Plain, " ", Operator, "+", Plain, " ub", String, "'no'", Plain, " ",
			Operator, "+", Plain, " ur", String, "'no'", Plain, " ", Operator, "+", Plain, " bf", String, "'no'", Plain, " ",
			Operator, "+", Plain, " ", String, "f''", Plain, " ", Operator, "+", Plain, " ", String, "Tr'q'",
		)},

		// f-string fields: braces are punctuation, the expression is Python, a
		// conversion is an operator, a format spec is text holding fields of its
		// own, and doubled braces are text.
		{"f\"{name!r:>{width}} {{x}}\"", tokensOf(
			String, "f\"", Punctuation, "{", Plain, "name", Operator, "!r", Punctuation, ":", String, ">",
			Punctuation, "{", Plain, "width", Punctuation, "}}", String, " {{x}}\"",
		)},

		// Other quotes nest; = documents itself; a call in a field is a call.
		{"f'{d[\"k\"]=} {len(xs):,}'", tokensOf(
			String, "f'", Punctuation, "{", Plain, "d", Punctuation, "[", String, "\"k\"", Punctuation, "]",
			Operator, "=", Punctuation, "}", Plain, " ", Punctuation, "{", Function, "len", Punctuation, "(",
			Plain, "xs", Punctuation, "):", String, ",", Punctuation, "}", String, "'",
		)},

		// \N{…} names a character, not a field; a backslash never hides a brace.
		{"f\"\\N{EM DASH}{x}\" + rf\"\\{y}\"", tokensOf(
			String, "f\"\\N{EM DASH}", Punctuation, "{", Plain, "x", Punctuation, "}", String, "\"",
			Plain, " ", Operator, "+", Plain, " ", String, "rf\"\\", Punctuation, "{", Plain, "y",
			Punctuation, "}", String, "\"",
		)},

		// An unclosed field in a one-line f-string stops at the line's end; a
		// triple-quoted one spans lines.
		{"f\"{x\ny = 1", tokensOf(
			String, "f\"", Punctuation, "{", Plain, "x\ny ", Operator, "=", Plain, " ", Number, "1",
		)},
		{"f\"{x\n\"tail\"", tokensOf(String, "f\"", Punctuation, "{", Plain, "x\n", String, "\"tail\"")},

		// A field does not start a line: `match` there is a name.
		{"f\"{match [0]}\"", tokensOf(
			String, "f\"", Punctuation, "{", Plain, "match ", Punctuation, "[", Number, "0", Punctuation, "]}",
			String, "\"",
		)},
		{"f'''a\n{b}\n'''", tokensOf(
			String, "f'''a\n", Punctuation, "{", Plain, "b", Punctuation, "}", String, "\n'''",
		)},

		// Inside a field the string's own quote opens a nested string when it
		// closes on the line (Python 3.12, PEP 701), so later fields stay fields.
		{"f\"{a[\"k\"]}\"", tokensOf(
			String, "f\"", Punctuation, "{", Plain, "a", Punctuation, "[", String, "\"k\"", Punctuation, "]}",
			String, "\"",
		)},
		{"name = f\"{user[\"name\"]} has {len(items)} items\"", tokensOf(
			Plain, "name ", Operator, "=", Plain, " ", String, "f\"", Punctuation, "{", Plain, "user",
			Punctuation, "[", String, "\"name\"", Punctuation, "]}", String, " has ", Punctuation, "{",
			Function, "len", Punctuation, "(", Plain, "items", Punctuation, ")}", String, " items\"",
		)},
		// A quote that does not close on its line ends the f-string there (the
		// rule before 3.12), so an unclosed field cannot colour the rest of the line.
		{"f\"{x\" + y\nz = \"a\"", tokensOf(
			String, "f\"", Punctuation, "{", Plain, "x", String, "\"", Plain, " ", Operator, "+",
			Plain, " y\nz ", Operator, "=", Plain, " ", String, "\"a\"",
		)},
		{"f\"{d[\"a\\\"b\"]}-{x}\"", tokensOf(
			String, "f\"", Punctuation, "{", Plain, "d", Punctuation, "[", String, "\"a\\\"b\"",
			Punctuation, "]}", String, "-", Punctuation, "{", Plain, "x", Punctuation, "}", String, "\"",
		)},

		// match and case open statements...
		{"match command:\n    case [x, _]:\n        pass\n    case _:\n        pass", tokensOf(
			Keyword, "match", Plain, " command", Punctuation, ":", Plain, "\n    ", Keyword, "case", Plain, " ",
			Punctuation, "[", Plain, "x", Punctuation, ",", Plain, " _", Punctuation, "]:", Plain, "\n        ",
			Keyword, "pass", Plain, "\n    ", Keyword, "case", Plain, " ", Keyword, "_", Punctuation, ":",
			Plain, "\n        ", Keyword, "pass",
		)},

		// ...and are names everywhere else.
		{"match = re.match(text)\ncase(1)\nprint(match, case)\ny = match if case else z", tokensOf(
			Plain, "match ", Operator, "=", Plain, " re", Punctuation, ".", Function, "match", Punctuation, "(",
			Plain, "text", Punctuation, ")", Plain, "\n", Function, "case", Punctuation, "(", Number, "1",
			Punctuation, ")", Plain, "\n", Function, "print", Punctuation, "(", Plain, "match",
			Punctuation, ",", Plain, " case", Punctuation, ")", Plain, "\ny ", Operator, "=", Plain, " match ",
			Keyword, "if", Plain, " case ", Keyword, "else", Plain, " z",
		)},

		// `type` opens a statement before a name, and is the builtin otherwise.
		{"type Point = tuple[float, float]\ntype(x)", tokensOf(
			Keyword, "type", Plain, " ", Type, "Point", Plain, " ", Operator, "=", Plain, " ", Type, "tuple",
			Punctuation, "[", Type, "float", Punctuation, ",", Plain, " ", Type, "float", Punctuation, "]",
			Plain, "\n", Type, "type", Punctuation, "(", Plain, "x", Punctuation, ")",
		)},

		// An annotation is a type whatever follows it: a default is not an
		// assignment to the builtin.
		{"def create(name: str, price: int = 0) -> dict:\n    ratio: float = 1.5", tokensOf(
			Keyword, "def", Plain, " ", Function, "create", Punctuation, "(", Plain, "name", Punctuation, ":",
			Plain, " ", Type, "str", Punctuation, ",", Plain, " price", Punctuation, ":", Plain, " ",
			Type, "int", Plain, " ", Operator, "=", Plain, " ", Number, "0", Punctuation, ")", Plain, " ",
			Operator, "->", Plain, " ", Type, "dict", Punctuation, ":", Plain, "\n    ratio",
			Punctuation, ":", Plain, " ", Type, "float", Plain, " ", Operator, "=", Plain, " ", Number, "1.5",
		)},

		// A builtin function is a function where called, as in Go: id, input
		// and format are field, parameter and loop names as often as calls.
		{"class User(Model):\n    id: int\n    format: str = \"json\"\ndef f(id, input):\n    for id in ids: print(id, key=len)", tokensOf(
			Keyword, "class", Plain, " ", Type, "User", Punctuation, "(", Plain, "Model", Punctuation, "):",
			Plain, "\n    id", Punctuation, ":", Plain, " ", Type, "int", Plain, "\n    format",
			Punctuation, ":", Plain, " ", Type, "str", Plain, " ", Operator, "=", Plain, " ", String, "\"json\"",
			Plain, "\n", Keyword, "def", Plain, " ", Function, "f", Punctuation, "(", Plain, "id",
			Punctuation, ",", Plain, " input", Punctuation, "):", Plain, "\n    ", Keyword, "for",
			Plain, " id ", Keyword, "in", Plain, " ids", Punctuation, ":", Plain, " ", Function, "print",
			Punctuation, "(", Plain, "id", Punctuation, ",", Plain, " key", Operator, "=", Plain, "len",
			Punctuation, ")",
		)},

		// A builtin's name is a name after a dot or before a single `=`.
		{"id = len(xs)\nprint(obj.id, key=str, id=1)", tokensOf(
			Plain, "id ", Operator, "=", Plain, " ", Function, "len", Punctuation, "(", Plain, "xs",
			Punctuation, ")", Plain, "\n", Function, "print", Punctuation, "(", Plain, "obj", Punctuation, ".",
			Plain, "id", Punctuation, ",", Plain, " key", Operator, "=", Type, "str", Punctuation, ",",
			Plain, " id", Operator, "=", Number, "1", Punctuation, ")",
		)},

		{"1. 1..real 1.e5 3j 0x_ff .5 1_000.5e-3 0o17 0b1", tokensOf(
			Number, "1.", Plain, " ", Number, "1.", Punctuation, ".", Plain, "real ", Number, "1.e5", Plain, " ",
			Number, "3j", Plain, " ", Number, "0x_ff", Plain, " ", Number, ".5", Plain, " ",
			Number, "1_000.5e-3", Plain, " ", Number, "0o17", Plain, " ", Number, "0b1",
		)},

		// A backslash continues the line, so `@` after it is not a decorator.
		{"x = 1 + \\\n    @foo  # note\n@bar", tokensOf(
			Plain, "x ", Operator, "=", Plain, " ", Number, "1", Plain, " ", Operator, "+", Plain, " \\\n    ",
			Operator, "@", Plain, "foo  ", Comment, "# note", Plain, "\n", Function, "@bar",
		)},

		{"async def f():\n    if (n := await g()): yield lambda: n", tokensOf(
			Keyword, "async", Plain, " ", Keyword, "def", Plain, " ", Function, "f", Punctuation, "():",
			Plain, "\n    ", Keyword, "if", Plain, " ", Punctuation, "(", Plain, "n ", Operator, ":=", Plain, " ",
			Keyword, "await", Plain, " ", Function, "g", Punctuation, "()):", Plain, " ", Keyword, "yield",
			Plain, " ", Keyword, "lambda", Punctuation, ":", Plain, " n",
		)},

		{"except (KeyError, ValueError) as error: raise RuntimeError(\"x\") from error", tokensOf(
			Keyword, "except", Plain, " ", Punctuation, "(", Type, "KeyError", Punctuation, ",", Plain, " ",
			Type, "ValueError", Punctuation, ")", Plain, " ", Keyword, "as", Plain, " error", Punctuation, ":",
			Plain, " ", Keyword, "raise", Plain, " ", Type, "RuntimeError", Punctuation, "(", String, "\"x\"",
			Punctuation, ")", Plain, " ", Keyword, "from", Plain, " error",
		)},

		{"x\r\n\x00$?`\xff", tokensOf(Plain, "x\r\n\x00$?`\xff")},
		{"", nil},
	})
}

func TestPythonSample(t *testing.T) {
	samples := fixturesFor(t, "python")
	if len(samples) == 0 {
		t.Fatal("testdata/python/ has no sample")
	}
	checkContains(t, "python", []tokenCase{{samples[0].source, tokensOf(
		Comment, "#!/usr/bin/env python3",
		String, "\"\"\"Báo cáo doanh thu: đọc CSV, in tổng theo món.\n\nMột docstring nhiều dòng với \"dấu nháy\" bên trong, và cả '''ba nháy đơn'''.\n\"\"\"",
		Number, "4j",
		Keyword, "type",
		Type, "Totals",
		Function, "@dataclass",
		Function, "@property",
		Operator, "!r",
		Function, "@functools.lru_cache",
		Keyword, "match",
		Keyword, "case",
		Keyword, "_",
		Operator, ":=",
		Keyword, "nonlocal",
		String, "b\"name,price\\n\"",
		String, "r\"C:\\data\\no\\escapes\"",
		String, "\"\\N{EM DASH}\"",
		Constant, "...",
		String, "\"__main__\"",
	)}})
}
