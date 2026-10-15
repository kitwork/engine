package highlight

import "testing"

func init() {
	hostileFragments = append(hostileFragments,
		"$a$", "$1", "?1", ":a", "@a", "E'\\", "N'", "U&'", "/* /*", "''''", "::", "?|", "INTO t(",
	)
}

func TestSQL(t *testing.T) {
	checkExact(t, "sql", []tokenCase{
		// Keywords in any case.
		{"SELECT a FROM t;\nselect b from t;\nSeLeCt c", tokensOf(
			Keyword, "SELECT", Plain, " a ", Keyword, "FROM", Plain, " t", Punctuation, ";", Plain, "\n",
			Keyword, "select", Plain, " b ", Keyword, "from", Plain, " t", Punctuation, ";", Plain, "\n",
			Keyword, "SeLeCt", Plain, " c",
		)},

		// '' is a quote inside a string; strings span lines.
		{"'it''s' 'two\nlines' ''", tokensOf(String, "'it''s'", Plain, " ", String, "'two\nlines'", Plain, " ", String, "''")},
		{"'open\nSELECT", tokensOf(String, "'open\nSELECT")},

		// Prefixed strings; only E'…' takes backslash escapes, after '' too.
		{"E'a\\'b' N'x' X'FF' b'01' U&'d\\0061t' e'\\n' 'no\\'", tokensOf(
			String, "E'a\\'b'", Plain, " ", String, "N'x'", Plain, " ", String, "X'FF'", Plain, " ",
			String, "b'01'", Plain, " ", String, "U&'d\\0061t'", Plain, " ", String, "e'\\n'", Plain, " ",
			String, "'no\\'",
		)},
		{"E'it''s \\'x\\'' y", tokensOf(String, "E'it''s \\'x\\''", Plain, " y")},

		// Quoted names are names: plain, keywords inside them included.
		{"SELECT \"Weird \"\"Name\"\"\", `menu items`, `a``b` FROM \"select\"", tokensOf(
			Keyword, "SELECT", Plain, " \"Weird \"\"Name\"\"\"", Punctuation, ",", Plain, " `menu items`",
			Punctuation, ",", Plain, " `a``b` ", Keyword, "FROM", Plain, " \"select\"",
		)},

		// Block comments nest; unclosed, they run to the end.
		{"-- line\n/* a /* nested */ b */ x /* open", tokensOf(
			Comment, "-- line", Plain, "\n", Comment, "/* a /* nested */ b */", Plain, " x ", Comment, "/* open",
		)},

		{"1 1.5 .5 1e10 1.5e-3 0x1F", tokensOf(
			Number, "1", Plain, " ", Number, "1.5", Plain, " ", Number, ".5", Plain, " ", Number, "1e10",
			Plain, " ", Number, "1.5e-3", Plain, " ", Number, "0x1F",
		)},

		{"a::date || b <> c ->> 'k' #>> d @> e != f", tokensOf(
			Plain, "a", Operator, "::", Type, "date", Plain, " ", Operator, "||", Plain, " b ", Operator, "<>",
			Plain, " c ", Operator, "->>", Plain, " ", String, "'k'", Plain, " ", Operator, "#>>", Plain, " d ",
			Operator, "@>", Plain, " e ", Operator, "!=", Plain, " f",
		)},

		// Parameters are variables; jsonb ?| and ?& are operators.
		{"$1 ?1 ? :name @name $name ?| ?&", tokensOf(
			Variable, "$1", Plain, " ", Variable, "?1", Plain, " ", Variable, "?", Plain, " ",
			Variable, ":name", Plain, " ", Variable, "@name", Plain, " ", Variable, "$name", Plain, " ",
			Operator, "?|", Plain, " ", Operator, "?&",
		)},

		// Dollar quoting: everything up to the same tag is one string, an
		// unclosed one runs to the end.
		{"AS $body$ SELECT 'x'; -- y\n$body$; SELECT $$a$$, $x$ open", tokensOf(
			Keyword, "AS", Plain, " ", String, "$body$ SELECT 'x'; -- y\n$body$", Punctuation, ";", Plain, " ",
			Keyword, "SELECT", Plain, " ", String, "$$a$$", Punctuation, ",", Plain, " ", String, "$x$ open",
		)},

		// A name glued to '(' is a call; LEFT is a keyword unless called.
		{"SELECT count(*), lower(email) FROM t LEFT JOIN u ON left(a, 1) = b", tokensOf(
			Keyword, "SELECT", Plain, " ", Function, "count", Punctuation, "(", Operator, "*", Punctuation, "),",
			Plain, " ", Function, "lower", Punctuation, "(", Plain, "email", Punctuation, ")", Plain, " ",
			Keyword, "FROM", Plain, " t ", Keyword, "LEFT", Plain, " ", Keyword, "JOIN", Plain, " u ",
			Keyword, "ON", Plain, " ", Function, "left", Punctuation, "(", Plain, "a", Punctuation, ",", Plain, " ",
			Number, "1", Punctuation, ")", Plain, " ", Operator, "=", Plain, " b",
		)},

		// Other keywords stay keywords before '('; a blank before '(' is no call.
		{"WHERE id IN (1) AND EXISTS(SELECT 1) AND count (x)", tokensOf(
			Keyword, "WHERE", Plain, " id ", Keyword, "IN", Plain, " ", Punctuation, "(", Number, "1",
			Punctuation, ")", Plain, " ", Keyword, "AND", Plain, " ", Keyword, "EXISTS", Punctuation, "(",
			Keyword, "SELECT", Plain, " ", Number, "1", Punctuation, ")", Plain, " ", Keyword, "AND",
			Plain, " count ", Punctuation, "(", Plain, "x", Punctuation, ")",
		)},

		// A type with a length is a type; called with anything else, a function.
		{"a VARCHAR(320), b NUMERIC(12, 2), c INT(11), d date, date(created_at), json('[]')", tokensOf(
			Plain, "a ", Type, "VARCHAR", Punctuation, "(", Number, "320", Punctuation, "),", Plain, " b ",
			Type, "NUMERIC", Punctuation, "(", Number, "12", Punctuation, ",", Plain, " ", Number, "2",
			Punctuation, "),", Plain, " c ", Type, "INT", Punctuation, "(", Number, "11", Punctuation, "),",
			Plain, " d ", Type, "date", Punctuation, ",", Plain, " ", Function, "date", Punctuation, "(",
			Plain, "created_at", Punctuation, "),", Plain, " ", Function, "json", Punctuation, "(",
			String, "'[]'", Punctuation, ")",
		)},

		// After INTO, TABLE, REFERENCES a name glued to '(' is a table.
		{"INSERT INTO orders(id) VALUES(1); CREATE TABLE t(a INT); REFERENCES sites(id)", tokensOf(
			Keyword, "INSERT", Plain, " ", Keyword, "INTO", Plain, " orders", Punctuation, "(", Plain, "id",
			Punctuation, ")", Plain, " ", Keyword, "VALUES", Punctuation, "(", Number, "1", Punctuation, ");",
			Plain, " ", Keyword, "CREATE", Plain, " ", Keyword, "TABLE", Plain, " t", Punctuation, "(",
			Plain, "a ", Type, "INT", Punctuation, ");", Plain, " ", Keyword, "REFERENCES", Plain, " sites",
			Punctuation, "(", Plain, "id", Punctuation, ")",
		)},

		{"NULL null TRUE false UNKNOWN", tokensOf(
			Constant, "NULL", Plain, " ", Constant, "null", Plain, " ", Constant, "TRUE", Plain, " ",
			Constant, "false", Plain, " ", Constant, "UNKNOWN",
		)},

		// A word longer than any keyword is a name, and costs no lookup.
		{"SELECT a_column_name_longer_than_any_keyword_is FROM t", tokensOf(
			Keyword, "SELECT", Plain, " a_column_name_longer_than_any_keyword_is ", Keyword, "FROM", Plain, " t",
		)},

		{"x := '1'::int", tokensOf(Plain, "x ", Operator, ":=", Plain, " ", String, "'1'", Operator, "::", Type, "int")},

		{"a[1:2] \x00 $ \xff\r\n", tokensOf(
			Plain, "a", Punctuation, "[", Number, "1", Punctuation, ":", Number, "2", Punctuation, "]",
			Plain, " \x00 $ \xff\r\n",
		)},
		{"", nil},
	})
}

func TestSQLSample(t *testing.T) {
	samples := fixturesFor(t, "sql")
	if len(samples) == 0 {
		t.Fatal("testdata/sql/ has no sample")
	}
	checkContains(t, "sql", []tokenCase{{samples[0].source, tokensOf(
		Comment, "-- Đơn hàng theo ngày của quán; chạy trên KitDB (PostgreSQL wire).",
		Type, "JSONB",
		Operator, "::",
		Keyword, "REFERENCES",
		Plain, " sites",
		Type, "DOUBLE",
		Type, "PRECISION",
		Function, "now",
		Plain, "\n  \"Weird Name\" ",
		Comment, "/* Doanh thu 7 ngày gần nhất, kể cả ngày không có đơn.\n   /* A nested comment, as PostgreSQL allows. */\n   Still inside the outer comment. */",
		Function, "generate_series",
		Keyword, "FILTER",
		Variable, "$1",
		Function, "rank",
		Keyword, "OVER",
		Variable, ":from",
		Operator, "#>>",
		Operator, "?|",
		String, "$body$\nDECLARE\n  result NUMERIC := 0;\nBEGIN\n  SELECT total INTO result FROM orders WHERE id = order_id;\n  RETURN COALESCE(result, 0); -- not a comment for SQL: this is all one string\nEND;\n$body$",
		String, "'it''s two drinks'",
		String, "E'hai dòng\\nmột ghi chú'",
		Variable, "@customer",
		Function, "left",
		String, "x'CAFE'",
		Plain, " `menu items` ",
		Keyword, "AUTOINCREMENT",
		Function, "datetime",
		Keyword, "select",
	)}})
}
