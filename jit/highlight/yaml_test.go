package highlight

import "testing"

func init() {
	hostileFragments = append(hostileFragments,
		"a: |\n", "a: >-\n  b\n", "- |\n", "--- |\n", "? ", ": ", "[a,", "{a: [", "&", "*", "!<", "!<a", "%YAML",
		"a:", "'a': b", `"a":`, "key: 'unclosed", "\t- x", "|2+",
	)
}

// Keys are properties; values are typed the way the YAML 1.2 core schema
// types them; block scalars are strings by indentation.
func TestYAML(t *testing.T) {
	checkExact(t, "yaml", []tokenCase{
		{"", nil},
		{"key: value", tokensOf(Property, "key", Punctuation, ":", Plain, " ", String, "value")},
		{"a: 1\nb: -2.5e3\nc: 0x1F\nd: .inf\ne: true\nf: ~\ng: NULL\nh: yes\ni: 2026-10-12", tokensOf(
			Property, "a", Punctuation, ":", Plain, " ", Number, "1", Plain, "\n",
			Property, "b", Punctuation, ":", Plain, " ", Number, "-2.5e3", Plain, "\n",
			Property, "c", Punctuation, ":", Plain, " ", Number, "0x1F", Plain, "\n",
			Property, "d", Punctuation, ":", Plain, " ", Number, ".inf", Plain, "\n",
			Property, "e", Punctuation, ":", Plain, " ", Constant, "true", Plain, "\n",
			Property, "f", Punctuation, ":", Plain, " ", Constant, "~", Plain, "\n",
			Property, "g", Punctuation, ":", Plain, " ", Constant, "NULL", Plain, "\n",
			Property, "h", Punctuation, ":", Plain, " ", String, "yes", Plain, "\n",
			Property, "i", Punctuation, ":", Plain, " ", String, "2026-10-12")},
		// A ':' with no blank after it does not end a key: times, URLs.
		{"time: 09:30\nurl: http://x.test/a", tokensOf(
			Property, "time", Punctuation, ":", Plain, " ", String, "09:30", Plain, "\n",
			Property, "url", Punctuation, ":", Plain, " ", String, "http://x.test/a")},
		{"a b : c", tokensOf(Property, "a b", Plain, " ", Punctuation, ":", Plain, " ", String, "c")},
		{`a: "x \"y\" z"` + "\nb: 'it''s'", tokensOf(
			Property, "a", Punctuation, ":", Plain, " ", String, `"x \"y\" z"`, Plain, "\n",
			Property, "b", Punctuation, ":", Plain, " ", String, "'it''s'")},
		{`"on": push`, tokensOf(Property, `"on"`, Punctuation, ":", Plain, " ", String, "push")},
		{`a: "open`, tokensOf(Property, "a", Punctuation, ":", Plain, " ", String, `"open`)},
		// # is a comment only at a line's start or after a blank.
		{"# top\nkey: value # trailing\nurl: a#b", tokensOf(
			Comment, "# top", Plain, "\n", Property, "key", Punctuation, ":", Plain, " ", String, "value", Plain, " ",
			Comment, "# trailing", Plain, "\n", Property, "url", Punctuation, ":", Plain, " ", String, "a#b")},
		{"base: &base\n  a: 1\nchild:\n  <<: *base", tokensOf(
			Property, "base", Punctuation, ":", Plain, " ", Variable, "&base", Plain, "\n  ", Property, "a",
			Punctuation, ":", Plain, " ", Number, "1", Plain, "\n", Property, "child", Punctuation, ":", Plain, "\n  ",
			Property, "<<", Punctuation, ":", Plain, " ", Variable, "*base")},
		{"a: !!str text\nb: !<tag:x,1:y> v", tokensOf(
			Property, "a", Punctuation, ":", Plain, " ", Type, "!!str", Plain, " ", String, "text", Plain, "\n",
			Property, "b", Punctuation, ":", Plain, " ", Type, "!<tag:x,1:y>", Plain, " ", String, "v")},
		{"- a\n- - b\n  - 2\n- -1", tokensOf(
			Punctuation, "-", Plain, " ", String, "a", Plain, "\n", Punctuation, "-", Plain, " ", Punctuation, "-",
			Plain, " ", String, "b", Plain, "\n  ", Punctuation, "-", Plain, " ", Number, "2", Plain, "\n",
			Punctuation, "-", Plain, " ", Number, "-1")},
		{"? a\n: b", tokensOf(Punctuation, "?", Plain, " ", String, "a", Plain, "\n", Punctuation, ":", Plain, " ", String, "b")},
		// Flow collections, over lines too, with JSON-style keys.
		{"k: [a, 'b', {c: d}, 1]", tokensOf(
			Property, "k", Punctuation, ":", Plain, " ", Punctuation, "[", String, "a", Punctuation, ",", Plain, " ",
			String, "'b'", Punctuation, ",", Plain, " ", Punctuation, "{", Property, "c", Punctuation, ":", Plain, " ",
			String, "d", Punctuation, "},", Plain, " ", Number, "1", Punctuation, "]")},
		{"k: [a,\n  b]", tokensOf(
			Property, "k", Punctuation, ":", Plain, " ", Punctuation, "[", String, "a", Punctuation, ",", Plain, "\n  ",
			String, "b", Punctuation, "]")},
		{`{"a":1}`, tokensOf(Punctuation, "{", Property, `"a"`, Punctuation, ":", Number, "1", Punctuation, "}")},
		// Block scalars: the lines indented past their key are one string.
		{"run: |\n  line one\n\n  line two\nnext: 1", tokensOf(
			Property, "run", Punctuation, ":", Plain, " ", Punctuation, "|", Plain, "\n  ",
			String, "line one\n\n  line two", Plain, "\n", Property, "next", Punctuation, ":", Plain, " ", Number, "1")},
		{"a: >- # note\n  folded\n  text\nb: x", tokensOf(
			Property, "a", Punctuation, ":", Plain, " ", Punctuation, ">-", Plain, " ", Comment, "# note", Plain, "\n  ",
			String, "folded\n  text", Plain, "\n", Property, "b", Punctuation, ":", Plain, " ", String, "x")},
		{"- run: |\n    echo: not a key\n- b", tokensOf(
			Punctuation, "-", Plain, " ", Property, "run", Punctuation, ":", Plain, " ", Punctuation, "|",
			Plain, "\n    ", String, "echo: not a key", Plain, "\n", Punctuation, "-", Plain, " ", String, "b")},
		{"a: |\nb: 1", tokensOf(
			Property, "a", Punctuation, ":", Plain, " ", Punctuation, "|", Plain, "\n", Property, "b", Punctuation, ":",
			Plain, " ", Number, "1")},
		{"%YAML 1.2\n---\na: 1\n...\n--- |\n  only\n", tokensOf(
			Keyword, "%YAML", Plain, " ", Number, "1.2", Plain, "\n", Punctuation, "---", Plain, "\n",
			Property, "a", Punctuation, ":", Plain, " ", Number, "1", Plain, "\n", Punctuation, "...", Plain, "\n",
			Punctuation, "---", Plain, " ", Punctuation, "|", Plain, "\n  ", String, "only", Plain, "\n")},
		{"a: 1\r\nb: |\r\n  x\r\nc: ~\r\n", tokensOf(
			Property, "a", Punctuation, ":", Plain, " ", Number, "1", Plain, "\r\n", Property, "b", Punctuation, ":",
			Plain, " ", Punctuation, "|", Plain, "\r\n  ", String, "x", Plain, "\r\n", Property, "c", Punctuation, ":",
			Plain, " ", Constant, "~", Plain, "\r\n")},
	})
}

// The sample reads as a workflow: a few tokens from each part of it.
func TestYAMLSample(t *testing.T) {
	samples := fixturesFor(t, "yaml")
	if len(samples) == 0 {
		t.Fatal("testdata/yaml/ has no sample")
	}
	checkContains(t, "yaml", []tokenCase{{samples[0].source, tokensOf(
		Keyword, "%YAML", Number, "1.2", Punctuation, "---",
		Property, "paths-ignore", Punctuation, "[", String, "'docs/**'",
		Property, "DISABLED", Punctuation, ":", Plain, " ", Constant, "False",
		Property, "INFINITY", Punctuation, ":", Plain, " ", Number, "-.inf",
		Property, "OCTAL", Punctuation, ":", Plain, " ", Number, "0o17",
		Property, `"quoted key"`,
		Property, "defaults", Punctuation, ":", Plain, " ", Variable, "&defaults",
		Property, "<<", Punctuation, ":", Plain, " ", Variable, "*defaults",
		Comment, "# toàn bộ lịch sử",
		Punctuation, "|", Plain, "\n          ", String, "cd engine\n          go test ./... -count=1\n\n          echo \"xong: $GITHUB_SHA\"",
		Punctuation, ">-",
		Punctuation, "|+", Plain, "\n          ", String, "dòng một",
		Punctuation, "}]]",
		Type, "!kitwork/site",
		Type, "!<tag:yaml.org,2002:str>",
		Property, "multiline plain",
		Number, "+12",
		Punctuation, "...", Punctuation, "---",
		Comment, "# Tài liệu thứ hai: cấu hình site.",
		Property, "tls", Punctuation, ":", Plain, " ", String, "off",
		Punctuation, ">", Plain, "\n  ", String, "Một đoạn văn được gấp,\n  nối thành một dòng.\n\n  Đoạn thứ hai.",
		String, "a document that is only a block scalar",
	)}})
}
