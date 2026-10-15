package highlight

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The kinds are a closed set with fixed names: a renderer builds class names
// from them and a contrast check walks them, so a renamed or added kind must
// show up here first.
func TestKindsAreTheClosedSet(t *testing.T) {
	want := []string{
		"plain", "keyword", "string", "number", "comment", "function", "type",
		"constant", "property", "variable", "operator", "punctuation", "tag",
		"attribute", "regular-expression", "inserted", "deleted", "heading", "prompt",
	}
	var got []string
	for _, kind := range Kinds() {
		got = append(got, kind.String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds:\n got %q\nwant %q", got, want)
	}
	if name := Kind(200).String(); name != "Kind(200)" {
		t.Errorf("a value outside the set printed as %q, borrowing a name", name)
	}
	if kindIdentifier(RegularExpression) != "RegularExpression" || kindIdentifier(Plain) != "Plain" {
		t.Errorf("describe would print constants that do not exist: %s, %s", kindIdentifier(RegularExpression), kindIdentifier(Plain))
	}
}

// Full names only, exactly as written.
func TestLanguagesAreFullNames(t *testing.T) {
	want := []string{
		"bash", "css", "diff", "go", "html", "javascript", "json", "powershell",
		"python", "sql", "text", "typescript", "ui", "work", "yaml",
	}
	if got := Languages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Languages() = %q\nwant %q", got, want)
	}
	for _, name := range want {
		if !Has(name) {
			t.Errorf("Has(%q) = false", name)
		}
	}
	for _, name := range []string{"js", "golang", "sh", "JavaScript", " javascript", "kitwork", ""} {
		if Has(name) {
			t.Errorf("Has(%q) = true; only the full name, exactly, is a language", name)
		}
	}
}

func TestSuggestNamesWhatToWrite(t *testing.T) {
	for _, row := range []struct{ name, want string }{
		// The short names people type, each to its full name.
		{"js", "javascript"}, {"ts", "typescript"},
		{"sh", "bash"}, {"shell", "bash"}, {"zsh", "bash"},
		{"ps", "powershell"}, {"ps1", "powershell"}, {"pwsh", "powershell"},
		{"py", "python"}, {"yml", "yaml"}, {"golang", "go"}, {"htm", "html"},
		{"patch", "diff"}, {"plaintext", "text"}, {"txt", "text"},
		{"kitwork", "ui work"}, {"kitworkjs", "work"},
		// Case and stray spaces.
		{"JS", "javascript"}, {"JavaScript", "javascript"}, {" python ", "python"}, {"bash", "bash"},
		// Near misses: one edit up to five characters, two from six.
		{"javasript", "javascript"}, {"pyhton", "python"}, {"powershel", "powershell"},
		{"typscript", "typescript"}, {"jsonc", "json"}, {"jsno", "json"}, {"html5", "html"},
		{"yamll", "yaml"}, {"diffs", "diff"}, {"tex", "text"},
		{"golan", "go"}, {"shel", "bash"}, // a near miss of a long short name
		// Nothing close: other languages, and two letters, where one edit reaches anything.
		{"cobol", ""}, {"java", ""}, {"rust", ""}, {"php", ""}, {"cs", ""}, {"c", ""}, {"", ""},
		{strings.Repeat("javascript", 1000), ""},
	} {
		got := strings.Join(Suggest(row.name), " ")
		if got != row.want {
			t.Errorf("Suggest(%q) = %q, want %q", clip(row.name), got, row.want)
		}
	}
}

// The error text is what an author reads, so it is pinned word for word.
func TestUnknownLanguageIsPlainTextAndAnError(t *testing.T) {
	for _, row := range []struct{ name, message string }{
		{"js", `ngôn ngữ "js" chưa có; viết javascript`},
		{"kitwork", `ngôn ngữ "kitwork" chưa có; viết ui hoặc work`},
		{"cobol", `ngôn ngữ "cobol" chưa có. Có: bash, css, diff, go, html, javascript, json, powershell, python, sql, text, typescript, ui, work, yaml`},
	} {
		tokens, err := Tokenize(row.name, "x = 1")
		if err == nil || err.Error() != row.message {
			t.Errorf("Tokenize(%q) error = %v\nwant %s", row.name, err, row.message)
		}
		var unknown *UnknownLanguageError
		if !errors.As(err, &unknown) || unknown.Name != row.name {
			t.Errorf("Tokenize(%q) error is not an *UnknownLanguageError naming it: %#v", row.name, err)
		}
		if !reflect.DeepEqual(tokens, tokensOf(Plain, "x = 1")) {
			t.Errorf("Tokenize(%q) tokens = %s, want the source as one plain token", row.name, describe(tokens))
		}
	}
	if tokens, _ := Tokenize("js", ""); len(tokens) != 0 {
		t.Errorf("an empty source gave tokens: %s", describe(tokens))
	}
	if tokens, err := Tokenize("text", "x = 1"); err != nil || !reflect.DeepEqual(tokens, tokensOf(Plain, "x = 1")) {
		t.Errorf("Tokenize(text) = %s, %v", describe(tokens), err)
	}
}

// A language= value comes from a tenant's page, so a pasted paragraph (or a
// megabyte) as the name must cost about one read of it, and its error text must
// not repeat it whole.
func TestLongLanguageNamesCostOneRead(t *testing.T) {
	name := strings.Repeat("É", 1<<19) // one megabyte, which lower case would copy
	if allocated := allocatedBy(func() { Suggest(name) }); allocated > 64<<10 {
		t.Errorf("Suggest on a one-megabyte name allocated %d bytes, want under 64 KiB", allocated)
	}
	if allocated := allocatedBy(func() { editDistance(name, "javascript", 2) }); allocated > 1<<10 {
		t.Errorf("editDistance decided a one-megabyte name is far with %d bytes allocated, want a length comparison", allocated)
	}
	_, err := Tokenize(name, "x")
	if want := `ngôn ngữ "` + strings.Repeat("É", 64) + `…" chưa có. Có: `; err == nil || !strings.HasPrefix(err.Error(), want) || len(err.Error()) > 1024 {
		t.Errorf("error text for a one-megabyte name is %d bytes, starting %q; want it to start %q", len(err.Error()), clip(err.Error()), want)
	}
	if unknown := (*UnknownLanguageError)(nil); !errors.As(err, &unknown) || unknown.Name != name {
		t.Errorf("the error does not keep the name as written")
	}
}

// One code block may not take the host down: past MaximumSourceSize the source
// comes back as one plain token and an error, costing no token list at all.
func TestOversizedSourcesComeBackAsPlainText(t *testing.T) {
	source := strings.Repeat("a+", MaximumSourceSize/2+1)
	var tokens []Token
	var err error
	if allocated := allocatedBy(func() { tokens, err = Tokenize("javascript", source) }); allocated > 64<<10 {
		t.Errorf("refusing %d bytes allocated %d bytes, want under 64 KiB", len(source), allocated)
	}
	if !reflect.DeepEqual(tokens, tokensOf(Plain, source)) {
		t.Errorf("an oversized source gave %d tokens, want the source as one plain token", len(tokens))
	}
	var tooLarge *SourceTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Language != "javascript" || tooLarge.Size != len(source) || tooLarge.Limit != MaximumSourceSize {
		t.Fatalf("error = %#v, want a *SourceTooLargeError naming javascript, %d and %d", err, len(source), MaximumSourceSize)
	}
	if want := `khối mã javascript dài 1048578 byte, quá giới hạn 1048576 byte; khối mã hiện thành chữ thường`; err.Error() != want {
		t.Errorf("error text = %s\nwant %s", err, want)
	}
	if tokens, err := Tokenize("javascript", source[:MaximumSourceSize]); err != nil || len(tokens) != MaximumSourceSize {
		t.Errorf("a source of exactly MaximumSourceSize gave %d tokens and %v, want it coloured", len(tokens), err)
	}
}

// The token list is the one large thing a tokenizer builds. On input that makes
// one token per byte it grows to fit the source in about two steps instead of a
// geometric series, so the bytes allocated stay a small multiple of the
// 24-byte tokens it returns. Growing by append cost 122 bytes per input byte.
func TestTokenListGrowsToFitTheSource(t *testing.T) {
	source := strings.Repeat("a+", MaximumSourceSize/2)
	var tokens []Token
	allocated := allocatedBy(func() { tokens = tokenizeWith(tokenizeJavaScript, source) })
	if len(tokens) != len(source) {
		t.Fatalf("%d tokens, want one per byte (%d)", len(tokens), len(source))
	}
	if perByte := float64(allocated) / float64(len(source)); perByte > 48 {
		t.Errorf("tokenizing %d bytes into one token per byte allocated %.1f bytes per input byte, want at most 48", len(source), perByte)
	}
	if cap(tokens) > len(tokens)+len(tokens)/16 {
		t.Errorf("%d tokens kept a capacity of %d: no input has more tokens than bytes, so the list need not grow past that", len(tokens), cap(tokens))
	}
	// Sparse tokens (a plain text) keep no large unused capacity.
	text := strings.Repeat("plain words ", 1<<14)
	if tokens := tokenizeWith(tokenizeText, text); cap(tokens) > 64 {
		t.Errorf("one plain token kept a capacity of %d tokens", cap(tokens))
	}
}

// allocatedBy is how many bytes run allocates on the heap.
func allocatedBy(run func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A tokenizer bug must not take a page down: the guard turns a panic into the
// source as plain text and an error naming the language.
func TestGuardTurnsAPanicIntoPlainText(t *testing.T) {
	broken := func(scan *scanner) {
		scan.emitLength(Keyword, 2)
		panic("index out of range")
	}
	tokens, err := guarded("bash", broken, "if x")
	if !reflect.DeepEqual(tokens, tokensOf(Plain, "if x")) {
		t.Errorf("tokens = %s, want the source as one plain token", describe(tokens))
	}
	if err == nil || !strings.Contains(err.Error(), `"bash"`) || !strings.Contains(err.Error(), "index out of range") {
		t.Errorf("error = %v, want one naming the language and the panic", err)
	}
}

// The scanner keeps the contract for the tokenizer: whitespace becomes plain,
// neighbours of one kind merge, an empty emit is nothing, the unread rest is plain.
func TestScannerMergesAndMakesWhitespacePlain(t *testing.T) {
	tokens := tokenizeWith(func(scan *scanner) {
		scan.emitLength(Keyword, 2)       // "if"
		scan.emitLength(Keyword, 1)       // " " is plain whatever it was called
		scan.emitLength(Plain, 1)         // "x" joins that plain
		scan.emitLength(String, 2)        // `"a`
		scan.emitLength(String, 2)        // `b"` joins that string
		scan.emit(Comment, scan.position) // nothing
	}, `if x"ab" tail`)
	want := tokensOf(Keyword, "if", Plain, " x", String, `"ab"`, Plain, " tail")
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %s\nwant %s", describe(tokens), describe(want))
	}
}

// An emit never goes backwards and never past the end.
func TestScannerEmitIsClamped(t *testing.T) {
	tokens := tokenizeWith(func(scan *scanner) {
		scan.emitLength(Number, 2)
		scan.emit(Keyword, 1)   // behind the position: nothing
		scan.emit(String, 1000) // past the end: to the end
	}, "12ab")
	if want := tokensOf(Number, "12", String, "ab"); !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %s\nwant %s", describe(tokens), describe(want))
	}
}

// One character is one UTF-8 sequence, or one byte that is not part of one.
func TestEmitCharacterTakesAWholeCharacter(t *testing.T) {
	tokens := tokenizeWith(func(scan *scanner) {
		scan.emitCharacter(Keyword)
		scan.emitCharacter(String)
	}, "é\xffa")
	if want := tokensOf(Keyword, "é", String, "\xff", Plain, "a"); !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %s\nwant %s", describe(tokens), describe(want))
	}
}

// A loop whose branch forgets to emit must still end.
func TestLoopForcesProgress(t *testing.T) {
	finished := make(chan []Token, 1)
	go func() {
		finished <- tokenizeWith(func(scan *scanner) {
			for loop := scan.loop(); loop.next(); {
				if scan.peek() == 'a' {
					scan.emitLength(Keyword, 1)
				}
			}
		}, "ab日a")
	}()
	select {
	case tokens := <-finished:
		if want := tokensOf(Keyword, "a", Plain, "b日", Keyword, "a"); !reflect.DeepEqual(tokens, want) {
			t.Errorf("got %s\nwant %s", describe(tokens), describe(want))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a loop with a branch that emits nothing spun forever")
	}
}

// embed hands a region to another tokenizer and takes back what it leaves as plain.
func TestEmbedTokenizesOnlyItsRegion(t *testing.T) {
	firstIsKeyword := func(region *scanner) { region.emitLength(Keyword, 1) }
	tokens := tokenizeWith(func(scan *scanner) {
		scan.emitLength(Punctuation, 2)
		scan.embed(firstIsKeyword, strings.Index(scan.source, "}}"))
		scan.emitLength(Punctuation, 2)
	}, "{{ab}}c")
	if want := tokensOf(Punctuation, "{{", Keyword, "a", Plain, "b", Punctuation, "}}", Plain, "c"); !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %s\nwant %s", describe(tokens), describe(want))
	}
}

// Nesting stops at maximumEmbedDepth: the rest is plain, never a stack overflow.
func TestEmbedDepthIsBounded(t *testing.T) {
	var nest tokenizer
	nest = func(scan *scanner) {
		scan.emitLength(Punctuation, 1)
		scan.embed(nest, len(scan.source))
	}
	tokens := tokenizeWith(nest, strings.Repeat("(", 100))
	opened := maximumEmbedDepth + 1 // the outer scanner and one per level
	want := tokensOf(Punctuation, strings.Repeat("(", opened), Plain, strings.Repeat("(", 100-opened))
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("got %s\nwant %s", describe(tokens), describe(want))
	}
}

// previous answers "what was written last?" across whitespace, and never
// reaches back out of an embedded region.
func TestPreviousIsTheLastNonBlankToken(t *testing.T) {
	var seen []string
	record := func(scan *scanner) {
		kind, text := scan.previous()
		seen = append(seen, kind.String()+":"+text)
	}
	tokenizeWith(func(scan *scanner) {
		record(scan)                // nothing yet
		scan.emitLength(Keyword, 6) // "return"
		scan.emitLength(Plain, 2)   // "  "
		record(scan)                // the keyword, across the blank
		scan.emitLength(Plain, 2)   // "x " merges into the plain run
		record(scan)                // the plain run, up to its last non-blank byte
	}, "return  x ")
	tokenizeWith(func(scan *scanner) {
		scan.emitLength(Keyword, 3) // "let"
		scan.emitLength(Plain, 1)   // " "
		scan.embed(func(region *scanner) {
			record(region)              // the region's start: nothing, though "let" is behind it
			region.emitLength(Plain, 1) // "y", merged with the outer " "
			record(region)              // only the region's part of that token
		}, scan.position+1)
	}, "let y")
	want := []string{"plain:", "keyword:return", "plain:  x", "plain:", "plain:y"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("previous gave %q\nwant %q", seen, want)
	}
}

func TestScanNumber(t *testing.T) {
	for _, row := range []struct{ source, want string }{
		{"42", "42"}, {"42;", "42"}, {"3.14)", "3.14"}, {".5+1", ".5"},
		{"1e9 ", "1e9"}, {"2.5E-3,", "2.5E-3"}, {"6.02e+23", "6.02e+23"}, {"1e+", "1e"},
		{"0x1F]", "0x1F"}, {"0XdeadBEEF", "0XdeadBEEF"}, {"0x1p-3", "0x1p-3"}, {"0x1.8p3", "0x1.8p3"},
		{"0b1010_1010", "0b1010_1010"}, {"0o755", "0o755"}, {"1_000_000", "1_000_000"},
		{"10n", "10n"}, {"2i", "2i"}, {"1.5f", "1.5f"}, {"10px;", "10px"}, {"100L)", "100L"},
		{"1.toString", "1"}, {"1..2", "1"}, {"127.0.0.1", "127.0"}, {"9\xff", "9"},
		{"x1", ""}, {".x", ""}, {"-1", ""}, {"", ""},
	} {
		if got := row.source[:scanNumber(row.source, 0)]; got != row.want {
			t.Errorf("scanNumber(%q) read %q, want %q", row.source, got, row.want)
		}
	}
}

func TestScanQuoted(t *testing.T) {
	triple := quoted{closer: `"""`, escape: '\\', multiline: true}
	template := quoted{closer: "`", escape: '\\', multiline: true}
	for _, row := range []struct {
		body  string // the source after the opening quote
		rules quoted
		want  string
	}{
		{`abc" tail`, doubleQuoted, `abc"`},
		{`a\"b" tail`, doubleQuoted, `a\"b"`},
		{`a\\" tail`, doubleQuoted, `a\\"`},
		{`it\'s' x`, singleQuoted, `it\'s'`},
		{"abc\nnext\"", doubleQuoted, "abc"},   // unterminated: stops before the line break
		{"abc\r\nnext\"", doubleQuoted, "abc"}, // and before a CRLF
		{"a\\\nb\" x", doubleQuoted, "a\\\nb\""},
		{"a\\\r\nb\" x", doubleQuoted, "a\\\r\nb\""},
		{"abc", doubleQuoted, "abc"},   // no closer at all: to the end
		{`abc\`, doubleQuoted, `abc\`}, // an escape as the last byte
		{"it''s' x", quoted{closer: "'", doubled: true}, "it''s'"},
		{"a`\"b\" x", quoted{closer: `"`, escape: '`'}, "a`\"b\""},
		{"a\nb` x", template, "a\nb`"},
		{"x\"\" \"\"\" tail", triple, "x\"\" \"\"\""},
	} {
		if got := row.body[:scanQuoted(row.body, 0, row.rules)]; got != row.want {
			t.Errorf("scanQuoted(%q) read %q, want %q", row.body, got, row.want)
		}
	}
}

func TestScanIdentifier(t *testing.T) {
	for _, row := range []struct{ source, extra, want string }{
		{"name rest", "", "name"}, {"_x1 ", "", "_x1"}, {"1abc", "", ""},
		{"$el.x", "$", "$el"}, {"$el", "", ""}, {"font-size:", "-", "font-size"},
		{"tên_biến=1", "", "tên_biến"}, {"日本語 ", "", "日本語"}, {"café\xff", "", "café"},
		{"éx", "", "éx"}, {"x٣", "", "x٣"}, {"٣x", "", ""},
		{"\xffabc", "", ""}, {"", "", ""},
	} {
		if got := row.source[:scanIdentifier(row.source, 0, row.extra)]; got != row.want {
			t.Errorf("scanIdentifier(%q, %q) read %q, want %q", row.source, row.extra, got, row.want)
		}
	}
}

func TestScanHelpers(t *testing.T) {
	operators := []string{"=", "==", "==="}
	for _, row := range []struct {
		name      string
		got, want int
	}{
		{"a closed block comment", scanBlockComment("/* a */ b", 0, "/*", "*/"), 7},
		{"an unterminated block comment runs to the end", scanBlockComment("/* a", 0, "/*", "*/"), 4},
		{"the closer is looked for after the opener", scanBlockComment("/*/ x */", 0, "/*", "*/"), 8},
		{"no block comment opens here", scanBlockComment("x /*", 0, "/*", "*/"), 0},
		{"a markup comment", scanBlockComment("<!-- a --> b", 0, "<!--", "-->"), 10},
		{"a line ends before CRLF", scanToLineEnd("// a\r\nb", 0), 4},
		{"a line ends before LF", scanToLineEnd("# a\nb", 0), 3},
		{"a line ends before a lone CR", scanToLineEnd("-- a\rb", 0), 4},
		{"the last line ends at the end", scanToLineEnd("-- a", 0), 4},
		{"a CRLF is one line break", scanNewline("\r\nx", 0), 2},
		{"a LF is one line break", scanNewline("\nx", 0), 1},
		{"a CR is one line break", scanNewline("\rx", 0), 1},
		{"no line break", scanNewline("x", 0), 0},
		{"whitespace of every kind", scanWhitespace(" \t\r\n\f\vx", 0), 6},
		{"NUL is not whitespace", scanWhitespace(" \x00", 0), 1},
		{"until the closer, inclusive", scanUntil("abc]]>d", 0, "]]>"), 6},
		{"until the end without a closer", scanUntil("abc", 1, "]]>"), 3},
		{"the longest operator", matchLongest("===x", 0, operators), 3},
		{"no operator", matchLongest("x", 0, operators), 0},
		{"a start past the end is the end", scanNumber("12", 5), 2},
		{"a negative start is the start", scanWhitespace(" x", -3), 1},
		{"byteAt past the end is 0", int(byteAt("ab", 2)), 0},
	} {
		if row.got != row.want {
			t.Errorf("%s: got %d, want %d", row.name, row.got, row.want)
		}
	}
	keywords := newWordSet("if", "else")
	if !keywords.has("if") || keywords.has("If") || keywords.has("") {
		t.Error("a word set must match exactly what it holds")
	}
}
