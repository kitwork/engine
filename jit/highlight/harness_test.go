package highlight

// The harness every language's tests share. A language file needs only:
//
//	func TestJSON(t *testing.T) {
//		checkExact(t, "json", []tokenCase{
//			{`{"a": 1}`, tokensOf(
//				Punctuation, "{",
//				Property, `"a"`,
//				Punctuation, ":",
//				Plain, " ",
//				Number, "1",
//				Punctuation, "}",
//			)},
//		})
//	}
//
// plus a sample in testdata/<language>/. The shared tests (contract_test.go,
// fuzz_test.go, benchmark_test.go) pick the language up from the registry and
// its samples from testdata on their own.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// tokenCase is one row of a language's table: a source and the tokens it gives.
type tokenCase struct {
	source string
	want   []Token
}

// tokensOf writes a token list compactly, as kind, text, kind, text, …
func tokensOf(parts ...any) []Token {
	if len(parts)%2 != 0 {
		panic(fmt.Sprintf("tokensOf takes kind, text pairs; got %d values", len(parts)))
	}
	tokens := make([]Token, 0, len(parts)/2)
	for index := 0; index < len(parts); index += 2 {
		kind, isKind := parts[index].(Kind)
		text, isText := parts[index+1].(string)
		if !isKind || !isText {
			panic(fmt.Sprintf("tokensOf pair %d must be a Kind then a string; got %T, %T", index/2, parts[index], parts[index+1]))
		}
		tokens = append(tokens, Token{Kind: kind, Text: text})
	}
	return tokens
}

// describe prints tokens in tokensOf form, one per line, so a failure shows the
// actual output in a shape that can go into a table once it has been checked.
func describe(tokens []Token) string {
	var builder strings.Builder
	builder.WriteString("tokensOf(\n")
	for _, token := range tokens {
		fmt.Fprintf(&builder, "\t%s, %q,\n", kindIdentifier(token.Kind), token.Text)
	}
	builder.WriteString(")")
	return builder.String()
}

// kindIdentifier is the Go name of a kind's constant: "regular-expression" →
// RegularExpression.
func kindIdentifier(kind Kind) string {
	if kind >= kindCount {
		return kind.String()
	}
	parts := strings.Split(kindNames[kind], "-")
	for index, part := range parts {
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}

// contractViolation says how tokens break the contract for source, or returns
// "" when they keep it. Every shared test and the fuzz target judge by this.
func contractViolation(source string, tokens []Token) string {
	validSource := utf8.ValidString(source)
	var joined strings.Builder
	joined.Grow(len(source))
	for index, token := range tokens {
		switch {
		case token.Text == "":
			return fmt.Sprintf("token %d is empty", index)
		case token.Kind >= kindCount:
			return fmt.Sprintf("token %d (%q) has %v, which is outside the set", index, clip(token.Text), token.Kind)
		case token.Kind == Prompt:
			return fmt.Sprintf("token %d (%q) is a prompt; prompt is reserved for the terminal", index, clip(token.Text))
		case token.Kind != Plain && isAllWhitespace(token.Text):
			return fmt.Sprintf("token %d (%q) is only whitespace but is %s; whitespace is plain", index, clip(token.Text), token.Kind)
		case index > 0 && tokens[index-1].Kind == token.Kind:
			return fmt.Sprintf("tokens %d and %d are both %s; neighbours of one kind must be merged", index-1, index, token.Kind)
		case validSource && !utf8.ValidString(token.Text):
			return fmt.Sprintf("token %d (%q) cuts a character in half", index, clip(token.Text))
		}
		joined.WriteString(token.Text)
	}
	if text := joined.String(); text != source {
		at := 0
		for at < len(text) && at < len(source) && text[at] == source[at] {
			at++
		}
		return fmt.Sprintf("the tokens do not join back into the source: they give %d bytes for %d, first difference at byte %d", len(text), len(source), at)
	}
	return ""
}

// clip shortens a text for a failure message.
func clip(text string) string {
	if len(text) > 60 {
		return text[:60] + "…"
	}
	return text
}

// checkExact runs every row through the language and requires exactly its
// tokens. A row whose own tokens break the contract (do not join back into
// its source, two neighbours of one kind, …) is reported as a wrong row.
func checkExact(t testing.TB, language string, cases []tokenCase) {
	t.Helper()
	tokenize, known := registry[language]
	if !known {
		t.Errorf("no tokenizer is registered for %q", language)
		return
	}
	for index, row := range cases {
		if problem := contractViolation(row.source, row.want); problem != "" {
			t.Errorf("%s row %d: the table row itself breaks the contract: %s\nsource: %q", language, index, problem, row.source)
			continue
		}
		got := tokenizeWith(tokenize, row.source)
		if problem := contractViolation(row.source, got); problem != "" {
			t.Errorf("%s row %d: %s\nsource: %q\ngot: %s", language, index, problem, row.source, describe(got))
			continue
		}
		if at := firstDifference(got, row.want); at >= 0 {
			t.Errorf("%s row %d: wrong tokens from token %d on\nsource: %q\nwant: %s\ngot:  %s", language, index, at, row.source, describe(row.want), describe(got))
		}
	}
}

// checkContains requires each wanted token to appear among the language's
// tokens as a whole token, in the wanted order, others allowed in between. It
// suits long sources where only a few tokens matter.
func checkContains(t testing.TB, language string, cases []tokenCase) {
	t.Helper()
	tokenize, known := registry[language]
	if !known {
		t.Errorf("no tokenizer is registered for %q", language)
		return
	}
	for index, row := range cases {
		got := tokenizeWith(tokenize, row.source)
		if problem := contractViolation(row.source, got); problem != "" {
			t.Errorf("%s row %d: %s\nsource: %q\ngot: %s", language, index, problem, row.source, describe(got))
			continue
		}
		next := 0
		for _, wanted := range row.want {
			for next < len(got) && got[next] != wanted {
				next++
			}
			if next == len(got) {
				t.Errorf("%s row %d: no %s token %q (in this order)\nsource: %q\ngot: %s", language, index, wanted.Kind, wanted.Text, row.source, describe(got))
				break
			}
			next++
		}
	}
}

// firstDifference is the index of the first token where the lists differ, or -1.
func firstDifference(got, want []Token) int {
	for index := 0; index < len(got) || index < len(want); index++ {
		if index >= len(got) || index >= len(want) || got[index] != want[index] {
			return index
		}
	}
	return -1
}

// fixture is one sample under testdata/<language>/.
type fixture struct {
	language string
	path     string
	source   string
}

var (
	fixturesOnce   sync.Once
	fixturesLoaded []fixture
	fixturesFailed error
)

// loadFixtures reads every sample under testdata/<language>/, sorted by path.
// testdata/fuzz belongs to the fuzzer and is not a language.
func loadFixtures(t testing.TB) []fixture {
	t.Helper()
	fixturesOnce.Do(func() {
		directories, err := os.ReadDir("testdata")
		if err != nil {
			fixturesFailed = err
			return
		}
		for _, directory := range directories {
			if !directory.IsDir() || directory.Name() == "fuzz" {
				continue
			}
			files, err := os.ReadDir(filepath.Join("testdata", directory.Name()))
			if err != nil {
				fixturesFailed = err
				return
			}
			for _, file := range files {
				if file.IsDir() {
					continue
				}
				path := filepath.Join("testdata", directory.Name(), file.Name())
				content, err := os.ReadFile(path)
				if err != nil {
					fixturesFailed = err
					return
				}
				fixturesLoaded = append(fixturesLoaded, fixture{language: directory.Name(), path: path, source: string(content)})
			}
		}
	})
	if fixturesFailed != nil {
		t.Fatalf("reading testdata: %v", fixturesFailed)
	}
	return fixturesLoaded
}

// fixturesFor is the samples of one language.
func fixturesFor(t testing.TB, language string) []fixture {
	t.Helper()
	var found []fixture
	for _, item := range loadFixtures(t) {
		if item.language == language {
			found = append(found, item)
		}
	}
	return found
}

// hostileFragments are inputs that break careless scanners: openers with no
// closer, an escape as the last byte, bytes that are not text, nesting openers.
// The shared tests run each alone, repeated and in pairs, in every language,
// and repeat them to measure that a tokenizer stays linear and does not recurse.
// A language with openers of its own appends them in an init of its test file.
var hostileFragments = []string{
	" ", "\t", "\n", "\r", "\r\n", "\f", "\v", "\x00", "\xff", "\xc3", "\xe2\x82", "\xed\xa0\x80",
	"é", "Tiếng Việt", "日本", "\xc2\xa0", "\xef\xbb\xbf", "\xe2\x80\xa8", "😀",
	`"`, `'`, "`", `\`, `"\`, `'\`, "`\\", `\"`, `"""`, `'''`, `"""\`, `""`, `''`,
	"/*", "*/", "/**/", "//", "#", "--", "<!--", "-->", "<#", "#>", ";",
	"{{", "}}", "{{ x }}", "${", "${`", "`${", "$(", "$((", "@(", "@{", "$", "$$", "@", "%", "&", "*", "!", "?", "~",
	"<", ">", "</", "<a", "<a b=", `<a b="`, "<a b='", "<script>", "</script>", "<style>", "</style>",
	"<!DOCTYPE", "<?", "?>", "<![CDATA[", "]]>", "&amp;", "&#",
	"(", ")", "[", "]", "{", "}",
	"/", "/a/", "/[", "/\\", "a/b/c", "=/", "(/",
	"+", "-", "=", ":", ",", ".", "..", "...", "=>", "?.", "??",
	"0", "9", "0x", "0b", "0o", "1e", "1e+", "1.", ".1", "1_", "0x_", "1.2.3",
	"@@", "@@ -1 +1 @@", "+++", "---", "diff --git", "+", "-x",
	"<<EOF\n", "<<-EOF\n", "<<'EOF'\n", "EOF", `@"`, `"@`, "@'", "'@",
	`r"`, `f"`, `b'`, `u'`, `f"{`,
	"- ", "? ", ": ", "|", "|-", ">-", "&anchor", "*alias", "!!str", "---\n", "...\n",
	"a", "_", "if", "function", "SELECT", "true", "null",
}

// repeatTo repeats fragment until the result is at least size bytes.
func repeatTo(fragment string, size int) string {
	if fragment == "" {
		return ""
	}
	return strings.Repeat(fragment, (size+len(fragment)-1)/len(fragment))
}

// recorder stands in for *testing.T when a test checks that the harness
// itself fails a wrong table.
type recorder struct {
	testing.TB
	failures []string
}

func (record *recorder) Helper() {}

func (record *recorder) Errorf(format string, values ...any) {
	record.failures = append(record.failures, fmt.Sprintf(format, values...))
}
