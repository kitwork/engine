package highlight

import (
	"regexp"
	"strings"
	"testing"
)

func TestRenderColorsAMarkedGoBlock(t *testing.T) {
	source := `<pre><code data-kitwork-highlight="go">package main</code></pre>`
	out := Render(source, "", nil)

	if !strings.Contains(out, `<span class="text-terminal-keyword font-bold">package</span>`) {
		t.Fatalf("keyword `package` was not colored with the keyword token class.\ngot: %s", out)
	}
	if !strings.Contains(out, "main") {
		t.Fatalf("identifier `main` was dropped from the output.\ngot: %s", out)
	}
	if strings.Contains(out, `<span class="text-terminal-keyword font-bold">main</span>`) {
		t.Fatalf("`main` is not a keyword but was colored as one.\ngot: %s", out)
	}
}

// A block the author did not mark must come back byte-identical. This is the
// whole doctrine: the engine fills a slot, it never decides on its own.
func TestRenderLeavesUnmarkedBlocksAlone(t *testing.T) {
	source := `<pre><code>package main</code></pre>`
	if out := Render(source, "", nil); out != source {
		t.Fatalf("an unmarked <code> block was rewritten.\nwant: %s\ngot:  %s", source, out)
	}
}

func TestRenderLeavesDocumentsWithoutMarkerUntouched(t *testing.T) {
	source := `<html><body><p>no code here</p></body></html>`
	if out := Render(source, "", nil); out != source {
		t.Fatalf("a document with no highlight marker was modified.\ngot: %s", out)
	}
}

// Running the pass over its own output must be a no-op. A filled block holds
// elements; a slot holds text. That difference is the only state needed.
func TestRenderIsIdempotent(t *testing.T) {
	source := `<pre><code data-kitwork-highlight="go">func main() { return }</code></pre>`
	once := Render(source, "", nil)
	twice := Render(once, "", nil)

	if once != twice {
		t.Fatalf("a second pass changed the output, so the pass is not idempotent.\nonce:  %s\ntwice: %s", once, twice)
	}
	if strings.Contains(twice, "&lt;span") {
		t.Fatalf("the second pass escaped its own spans, which is the double-processing bug.\ngot: %s", twice)
	}
}

// The tokenizer must escape, never inject. This is the security boundary.
func TestRenderEscapesSourceSoCodeCannotInjectMarkup(t *testing.T) {
	source := `<pre><code data-kitwork-highlight="go">x := "&lt;script&gt;alert(1)&lt;/script&gt;"</code></pre>`
	out := Render(source, "", nil)

	if strings.Contains(out, "<script>") {
		t.Fatalf("a raw <script> tag reached the output — code injected markup.\ngot: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("the script text was not escaped in the output.\ngot: %s", out)
	}
}

// The escape test above only exercises the string branch. Angle brackets in
// operator position take the punctuation branch, and an identifier can carry
// them too — both must escape or the block can close its own <pre> and inject.
func TestCodeEscapesOutsideStringLiterals(t *testing.T) {
	out := Code("if a < b && c > d", "go", tokenClasses)
	if stray := strayAngleBracket(out); stray != "" {
		t.Fatalf("a raw angle bracket survived the punctuation branch at %q.\ngot: %s", stray, out)
	}
	if !strings.Contains(out, "&lt;") || !strings.Contains(out, "&gt;") {
		t.Fatalf("angle brackets were not escaped.\ngot: %s", out)
	}
}

func TestRenderEscapesRawMarkupSmuggledThroughEntities(t *testing.T) {
	// The slot content is entity-encoded, so it passes the "is this already
	// filled" check as text — then unescapes to a real tag. It must be
	// re-escaped on the way out.
	source := `<pre><code data-kitwork-highlight="go">&lt;/code&gt;&lt;img onerror=x&gt;</code></pre>`
	out := Render(source, "", nil)

	// Every '<' the pass emits must open one of its own spans. Any other '<'
	// is source text that leaked unescaped, whatever shape it happens to take
	// once spans are interleaved with it.
	if stray := strayAngleBracket(out); stray != "" {
		t.Fatalf("source text leaked an unescaped '<' at %q — the block can break out of its container.\ngot: %s", stray, out)
	}
	if strings.Contains(out, ">") && strings.Count(out, "&gt;") == 0 {
		t.Fatalf("no '>' was escaped, so the smuggled markup passed through.\ngot: %s", out)
	}
}

// strayAngleBracket returns the first '<' that does not begin a <span> or
// </span> tag, or "" when every one of them does.
func strayAngleBracket(html string) string {
	for index := 0; index < len(html); index++ {
		if html[index] != '<' {
			continue
		}
		rest := html[index:]
		if strings.HasPrefix(rest, "<span ") || strings.HasPrefix(rest, "</span>") ||
			strings.HasPrefix(rest, "<pre") || strings.HasPrefix(rest, "</pre>") ||
			strings.HasPrefix(rest, "<code") || strings.HasPrefix(rest, "</code>") {
			continue
		}
		end := index + 12
		if end > len(html) {
			end = len(html)
		}
		return html[index:end]
	}
	return ""
}

// An unknown language is not an error and must not be guessed at: escape and
// leave it uncolored, which is strictly better than coloring it wrong.
func TestRenderEscapesButDoesNotColorAnUnknownLanguage(t *testing.T) {
	source := `<pre><code data-kitwork-highlight="brainfuck">+[-&gt;+&lt;]</code></pre>`
	out := Render(source, "", nil)

	if strings.Contains(out, "<span") {
		t.Fatalf("an unknown language was colored instead of left plain.\ngot: %s", out)
	}
	if !strings.Contains(out, "&lt;") {
		t.Fatalf("content of an unknown-language block lost its escaping.\ngot: %s", out)
	}
}

func TestRenderAcceptsTheShortMarkerToo(t *testing.T) {
	source := `<pre><code data-kit-highlight="go">const x = 1</code></pre>`
	if out := Render(source, "", nil); !strings.Contains(out, `<span class="text-terminal-keyword font-bold">const</span>`) {
		t.Fatalf("the short marker data-kit-highlight was not recognized.\ngot: %s", out)
	}
}

func TestThemeKeyNormalizesSpacingAndCase(t *testing.T) {
	for _, name := range []string{"tokyo night", "Tokyo-Night", "TOKYONIGHT", "tokyo_night"} {
		if got := ThemeKey(name); got != "tokyonight" {
			t.Fatalf("ThemeKey(%q) = %q, want %q — router.highlight() must accept the readable spelling", name, got, "tokyonight")
		}
	}
}

// The class names are fixed; the THEME decides what they resolve to. That is
// what makes a theme change a variable swap rather than a re-render.
func TestEmittedClassesAreThemeIndependent(t *testing.T) {
	if PaletteFor("classic") != PaletteFor("tokyonight") {
		t.Fatal("the emitted class names varied by theme; changing a theme would need every page re-rendered")
	}
	if !strings.HasPrefix(tokenClasses.Keyword, "text-terminal-") {
		t.Fatalf("the tokenizer emits %q, want a terminal token class", tokenClasses.Keyword)
	}
}

// A typo in a theme name must be visible at config time, not silently served as
// the default.
func TestKnownThemeRejectsATypo(t *testing.T) {
	for _, name := range []string{"tokyonight", "tokyo-night", "Tokyo Night", "classic", "mono"} {
		if !KnownTheme(name) {
			t.Fatalf("KnownTheme(%q) = false, want true", name)
		}
	}
	if KnownTheme("tokyonite") {
		t.Fatal("KnownTheme accepted a misspelled theme name")
	}
}

// Indentation is what makes a code block readable; the tokenizer must not
// normalize it away.
func TestCodePreservesWhitespaceExactly(t *testing.T) {
	source := "func a() {\n\tif x {\n\t\treturn\n\t}\n}"
	out := Code(source, "go", tokenClasses)

	stripped := stripSpans(out)
	if stripped != source {
		t.Fatalf("whitespace or characters changed.\nwant: %q\ngot:  %q", source, stripped)
	}
}

func TestCodeColorsCommentsStringsAndNumbers(t *testing.T) {
	out := Code(`// note
x := "hi"
n := 42`, "go", tokenClasses)

	for _, want := range []struct{ class, text string }{
		{tokenClasses.Comment, "// note"},
		{tokenClasses.String, `&#34;hi&#34;`}, // quotes are escaped on the way out
		{tokenClasses.Number, "42"},
	} {
		if !strings.Contains(out, `<span class="`+want.class+`">`+want.text+`</span>`) {
			t.Fatalf("%q was not colored with %s.\ngot: %s", want.text, want.class, out)
		}
	}
}

// The subset is the product: while/try are deliberately absent from the Kitwork
// keyword table, so they read as plain identifiers rather than as valid syntax.
func TestKitworkLanguageDoesNotColorExcludedKeywords(t *testing.T) {
	out := Code("while (true) { }", "kitwork", tokenClasses)
	if strings.Contains(out, `<span class="`+tokenClasses.Keyword+`">while</span>`) {
		t.Fatal("`while` was colored as a keyword, but the Kitwork subset rejects it")
	}

	out = Code("while (true) { }", "javascript", tokenClasses)
	if !strings.Contains(out, `<span class="`+tokenClasses.Keyword+`">while</span>`) {
		t.Fatal("`while` was not colored in plain JavaScript, so the control case does not distinguish the two tables")
	}
}

func TestCodeUnterminatedStringDoesNotDropTheTail(t *testing.T) {
	out := Code(`x := "unterminated`, "go", tokenClasses)
	if !strings.Contains(stripSpans(out), "unterminated") {
		t.Fatalf("text after an unterminated quote was lost.\ngot: %s", out)
	}
}

// stripSpans removes the emitted markup so a test can compare the text that
// survived tokenization against the input.
func stripSpans(html string) string {
	replacer := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "&#34;", `"`, "&#39;", "'")
	var builder strings.Builder
	for index := 0; index < len(html); {
		if html[index] == '<' {
			end := strings.IndexByte(html[index:], '>')
			if end < 0 {
				break
			}
			index += end + 1
			continue
		}
		next := strings.IndexByte(html[index:], '<')
		if next < 0 {
			builder.WriteString(html[index:])
			break
		}
		builder.WriteString(html[index : index+next])
		index += next
	}
	return replacer.Replace(builder.String())
}

// The author form must not survive into the delivered document: the browser
// kernel validates every data-kit-* name against its own directive list and
// throws on one it does not implement. Promotion to the engine namespace is
// what keeps a server-only directive out of that check.
func TestRenderPromotesTheAuthorMarkerToTheEngineNamespace(t *testing.T) {
	out := Render(`<pre><code data-kit-highlight="go">package main</code></pre>`, "", nil)

	if strings.Contains(out, "data-kit-highlight") {
		t.Fatalf("the author-form marker survived into the output; the browser kernel will reject it.\ngot: %s", out)
	}
	if !strings.Contains(out, `data-kitwork-highlight="go"`) {
		t.Fatalf("the marker was not promoted to the engine namespace, so the language is lost.\ngot: %s", out)
	}
}

func TestRenderPromotionKeepsOtherAttributes(t *testing.T) {
	out := Render(`<pre><code class="font-mono" data-kit-highlight="go" id="x">package main</code></pre>`, "", nil)

	for _, want := range []string{`class="font-mono"`, `id="x"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("promotion dropped %s from the open tag.\ngot: %s", want, out)
		}
	}
}

// CRLF is the default on the platform this engine is developed on, so a
// tokenizer that scans only for \n leaves the \r inside the preceding token.
// The browser then normalizes that stray \r into a line break of its own and
// the block renders an extra blank line.
func TestCodeHandlesCarriageReturnLineEndings(t *testing.T) {
	out := Code("// note\r\napp\r\n", "kitwork", tokenClasses)

	if strings.Contains(out, "note\r") {
		t.Fatalf("the comment token swallowed the carriage return; the block will render a blank line too many.\ngot: %q", out)
	}
	if stripSpans(out) != "// note\r\napp\r\n" {
		t.Fatalf("CRLF was not preserved verbatim.\nwant: %q\ngot:  %q", "// note\r\napp\r\n", stripSpans(out))
	}
}

// Before the roles were split, an option name and a bare identifier had NO class
// at all — they rendered as undifferentiated body text while a single
// "punctuation" colour covered everything else. Delimiters legitimately dominate
// a config listing (every editor theme paints them one colour), so the thing
// worth asserting is that the roles which SHOULD differ actually do.
func TestRolesThatShouldDifferAreDistinguished(t *testing.T) {
	source := `const ready = app.count >= 10 && !app.paused
app.database({ type: "sqlite", name: ".data/app.db" })`
	out := Code(source, "js", tokenClasses)

	counts := map[string]int{}
	for _, m := range regexp.MustCompile(`class="([^"]+)"`).FindAllStringSubmatch(out, -1) {
		counts[m[1]]++
	}
	for _, want := range []struct{ class, why string }{
		{tokenClasses.Property, "object keys"},
		{tokenClasses.Variable, "bare identifiers"},
		{tokenClasses.Operator, "operators, apart from structural punctuation"},
		{tokenClasses.Keyword, "keywords"},
		{tokenClasses.String, "strings"},
		{tokenClasses.Number, "numbers"},
		{tokenClasses.Method, "calls"},
	} {
		if counts[want.class] == 0 {
			t.Fatalf("%s were not given their own role (%s). %v", want.why, want.class, counts)
		}
	}

	// The control: an unclassified span would mean something fell through with
	// no role at all, which is the state this work replaced.
	if counts[""] != 0 {
			t.Fatalf("%d spans were emitted with no class. %v", counts[""], counts)
	}
}

// A mixed run like "})" is structural, not an operator.
func TestMixedPunctuationRunStaysStructural(t *testing.T) {
	out := Code(`f({ a: 1 })`, "js", tokenClasses)
	if strings.Contains(out, `<span class="`+tokenClasses.Operator+`">})`) {
		t.Fatalf("a run containing delimiters was called an operator.\n%s", out)
	}
}

// A shell line is a command, its flags and its arguments — not an expression.
func TestShellLineIsReadAsCommandFlagsAndArguments(t *testing.T) {
	out := Code("$ kitwork new --force mysite.com\n$ go run .", "bash", tokenClasses)

	for _, want := range []struct{ class, text string }{
		{tokenClasses.Prompt, "$"},
		{tokenClasses.Command, "kitwork"},
		{tokenClasses.Flag, "--force"},
		{tokenClasses.Argument, "new"},
	} {
		if !strings.Contains(out, `<span class="`+want.class+`">`+want.text+`</span>`) {
			t.Fatalf("%q was not coloured as %s.\n%s", want.text, want.class, out)
		}
	}
}

// The command role must reset at every newline, or only the first line of a
// listing gets one.
func TestEachShellLineGetsItsOwnCommand(t *testing.T) {
	out := Code("$ kitwork new\n$ go run .", "bash", tokenClasses)
	commands := strings.Count(out, `<span class="`+tokenClasses.Command+`">`)
	if commands != 2 {
		t.Fatalf("expected one command per line, got %d.\n%s", commands, out)
	}
}

// An override may be a colour, one of the SITE's own tokens, or a spelled-out
// class. Reading it as a colour only — which is what the first version did —
// turned "brand" into Color{0,0,0} and rendered it black without a word.
func TestOverrideAcceptsColourTokenOrClass(t *testing.T) {
	for _, c := range []struct{ value, want string }{
		{"#ff0000", "text-[#ff0000]"},
		{"brand", "text-brand"},
		{"ink-soft", "text-ink-soft"},
		{"text-pink-400", "text-pink-400"},
		{"text-pink-400 font-bold", "text-pink-400 font-bold"},
		{"font-bold", "font-bold"},
	} {
		if got := overrideClass(c.value); got != c.want {
			t.Fatalf("overrideClass(%q) = %q, want %q", c.value, got, c.want)
		}
	}
	if got := overrideClass("   "); got != "" {
		t.Fatalf("a blank override produced %q, want no class at all", got)
	}
}

// Naming a site token is the form worth having: the role then follows whatever
// that token does, including a theme swap, without the highlighter knowing.
func TestOverrideReachesTheEmittedSpan(t *testing.T) {
	out := Render(`<pre><code data-kitwork-highlight="go">package main</code></pre>`, "",
		map[string]string{"keyword": "brand"})

	if !strings.Contains(out, `<span class="text-brand">package</span>`) {
		t.Fatalf("the override did not reach the emitted span.\n%s", out)
	}
	if strings.Contains(out, "text-terminal-keyword") {
		t.Fatalf("the default keyword class survived the override.\n%s", out)
	}
}

// Overriding one role must leave the rest of the theme alone.
func TestOverrideLeavesOtherRolesAlone(t *testing.T) {
	out := Render(`<pre><code data-kitwork-highlight="go">x := "s"</code></pre>`, "",
		map[string]string{"keyword": "brand"})
	if !strings.Contains(out, tokenClasses.String) {
		t.Fatalf("overriding keyword changed the string role.\n%s", out)
	}
}

// A pipeline is several commands. Reading only the first word of the whole
// listing as a command left every program after a pipe looking like an argument.
func TestShellSeparatorStartsANewCommand(t *testing.T) {
	for _, source := range []string{
		`curl -s x | grep y`,
		`cd /tmp && go build`,
		`echo a; echo b`,
	} {
		out := Code(source, "bash", tokenClasses)
		if got := strings.Count(out, `<span class="`+tokenClasses.Command+`">`); got != 2 {
			t.Fatalf("%q produced %d commands, want 2.\n%s", source, got, out)
		}
	}
}

// VAR=1 ./run.sh — the assignment prefix is not the program being run.
func TestShellAssignmentPrefixIsNotTheCommand(t *testing.T) {
	out := Code(`VAR=1 ./run.sh`, "bash", tokenClasses)

	if !strings.Contains(out, `<span class="`+tokenClasses.Variable+`">VAR</span>`) {
		t.Fatalf("the assignment name was not read as a variable.\n%s", out)
	}
	if !strings.Contains(out, `>./run.sh</span>`) {
		t.Fatalf("the program after the assignment was not the command.\n%s", out)
	}
}

// A path is one word. Splitting it produced four tokens for one program name.
func TestShellPathIsASingleToken(t *testing.T) {
	for _, c := range []struct{ source, path string }{
		{`./run.sh`, "./run.sh"},
		{`/usr/bin/env node`, "/usr/bin/env"},
		{`go build ./...`, "./..."},
	} {
		out := Code(c.source, "bash", tokenClasses)
		if !strings.Contains(out, ">"+c.path+"</span>") {
			t.Fatalf("%q did not keep %q as one token.\n%s", c.source, c.path, out)
		}
	}
}

// A shell word runs to whitespace. Broken at every dot and slash, one hostname
// or URL became five tokens in different colours.
func TestShellWordRunsToWhitespace(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{`kitwork new mysite.com`, "mysite.com"},
		{`curl http://localhost:8080/api`, "http://localhost:8080/api"},
		{`ls a.b.c`, "a.b.c"},
	} {
		if out := Code(c.source, "bash", tokenClasses); !strings.Contains(out, ">"+c.want+"</span>") {
			t.Fatalf("%q did not keep %q as one token.\n%s", c.source, c.want, out)
		}
	}
}

// "=" has to stay outside the word, or the assignment prefix and the flag value
// both lose their shape.
func TestShellWordStopsAtAnEquals(t *testing.T) {
	out := Code(`VAR=1 run --flag=value`, "bash", tokenClasses)
	for _, want := range []string{
		`<span class="` + tokenClasses.Variable + `">VAR</span>`,
		`<span class="` + tokenClasses.Flag + `">--flag</span>`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %s.\n%s", want, out)
		}
	}
}

// The command is the first word that is not part of an assignment prefix.
// Leaving the value to the next pass coloured it as the command.
func TestAssignmentValueIsNotTheCommand(t *testing.T) {
	out := Code(`ALLOW_LOCAL=true PORT=8080 go run .`, "bash", tokenClasses)

	if !strings.Contains(out, `<span class="`+tokenClasses.Command+`">go</span>`) {
		t.Fatalf("`go` was not read as the command.\n%s", out)
	}
	if strings.Contains(out, `<span class="`+tokenClasses.Command+`">true</span>`) {
		t.Fatalf("an assignment value was read as the command.\n%s", out)
	}
	if !strings.Contains(out, `<span class="`+tokenClasses.Argument+`">true</span>`) {
		t.Fatalf("the assignment value lost its role.\n%s", out)
	}
}
