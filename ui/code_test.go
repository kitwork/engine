package ui

import (
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/highlight"
	"github.com/kitwork/engine/ui/stylesheet"
)

// codePage compiles one content.ui with a palette (nil: Kit Design's light palette alone) and returns
// the page, failing on a compile error.
func codePage(t *testing.T, palette *stylesheet.Palette, source string) *Page {
	t.Helper()
	s := memorySite("", map[string]string{"/site/content.ui": source})
	s.Reload = true
	s.Palette = palette
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return page
}

// codeRender renders a page with data and returns the body and what its render told the author.
func codeRender(page *Page, scope map[string]any) (string, []string) {
	var told []string
	body := page.Render(Request{Path: "/", Scope: scope, Warn: func(message string) { told = append(told, message) }})
	return body, told
}

var codeTags = regexp.MustCompile(`<[^>]*>`)

// codeText is the text a reader sees in the first <code class="language-…"> of a body: its tags left
// out, its characters unescaped.
func codeText(t *testing.T, body, language string) string {
	t.Helper()
	open := `<code class="language-` + language + `">`
	start := strings.Index(body, open)
	if start < 0 {
		t.Fatalf("no %s in\n%s", open, body)
	}
	inner := body[start+len(open):]
	end := strings.Index(inner, "</code>")
	if end < 0 {
		t.Fatalf("the code is not closed:\n%s", body)
	}
	return html.UnescapeString(codeTags.ReplaceAllString(inner[:end], ""))
}

// Every kind a tokenizer writes gets its class and its rule: code from {{ }} can show any of them, so its
// page holds them all, each as stylesheet.Table.Code writes it. The prompt is the terminal's: no
// tokenizer writes it, so code holds no rule for it. CONTROL: a page whose code names no language has
// no code rule at all.
func TestCodeEveryKindGetsItsClassAndRule(t *testing.T) {
	page := codePage(t, nil, `<content><code language="{{ language }}">{{ source }}</code></content>`)
	css := string(page.Stylesheet().CSS)
	table := stylesheet.NewTable(nil)
	for _, kind := range highlight.Kinds() {
		name := kind.String()
		class := ".code-" + name + "{"
		switch kind {
		case highlight.Plain, highlight.Prompt:
			if strings.Contains(css, class) {
				t.Errorf("%s has no rule on a code element:\n%s", name, css)
			}
			continue
		}
		own := &stylesheet.Rules{}
		if _, err := table.Code(own, name); err != nil {
			t.Fatal(err)
		}
		// its rule, and the value in each mode of every engine colour it names (13/10/2026: a custom
		// property of the page, --kitwork-code-X, set on :root; never an author's --kitwork-strong-N-X)
		rule := ""
		for _, line := range strings.Split(string(own.CSS()), "\n") {
			if strings.HasPrefix(line, class) {
				rule = line
			}
		}
		if rule == "" || !strings.Contains(css, rule) {
			t.Errorf("kind %s: want its rule %q in the page's stylesheet:\n%s", name, rule, css)
		}
		if kind != highlight.Comment && kind != highlight.Operator && kind != highlight.Punctuation && !strings.Contains(rule, "var(--kitwork-code-") ||
			strings.Contains(rule, "--kitwork-strong-") {
			t.Errorf("kind %s: want its engine colour's property, got %q", name, rule)
		}
		for _, m := range regexp.MustCompile(`var\((--kitwork-(?:strong-[0-9]+|badge|code)-[a-z-]+|--kitwork-error)\)`).FindAllStringSubmatch(rule, -1) {
			if !regexp.MustCompile(`:root\{[^}]*` + m[1] + `:#[0-9a-f]{6}`).MatchString(css) {
				t.Errorf("kind %s: its rule names %s, which the page's stylesheet must set on :root:\n%s", name, m[1], css)
			}
		}
	}
	// a token of each kind is written in its class, and only those classes are written: the samples of
	// the languages hold every kind between them (regular-expression in javascript, inserted in diff…)
	written := map[string]bool{}
	for _, sample := range codeSamples(t) {
		body, _ := codeRender(page, map[string]any{"language": sample.language, "source": sample.source})
		for _, m := range regexp.MustCompile(`<span class="([^"]*)">`).FindAllStringSubmatch(body, -1) {
			written[m[1]] = true
		}
	}
	for _, kind := range writtenKinds() {
		if !written["code-"+kind.String()] {
			t.Errorf("no token of %s was written in its class code-%s", kind, kind)
		}
		delete(written, "code-"+kind.String())
	}
	if len(written) > 0 {
		t.Errorf("spans in a class that is no kind's: %v", written)
	}
	plain := codePage(t, nil, `<content><code>{{ source }}</code><code>go run .</code></content>`)
	if css := string(plain.Stylesheet().CSS); strings.Contains(css, ".code-") {
		t.Errorf("CONTROL: code without a language has no code rule:\n%s", css)
	}
}

// Static code ships the rules of the kinds it holds and no other (the same rule as every class: a
// page's stylesheet holds what its HTML can carry). language="text" holds none. CONTROL: the same code
// from {{ }} ships every kind.
func TestCodeShipsOnlyTheKindsItUses(t *testing.T) {
	page := codePage(t, nil, `<content><code language="json">{"name": "kitwork", "port": 8080, "debug": true}</code><code language="text">if x then y</code></content>`)
	css := string(page.Stylesheet().CSS)
	for _, want := range []string{".code-property{", ".code-string{", ".code-number{", ".code-constant{", ".code-punctuation{"} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %s in\n%s", want, css)
		}
	}
	for _, unwanted := range []string{".code-keyword{", ".code-comment{", ".code-function{", ".code-tag{", ".code-inserted{"} {
		if strings.Contains(css, unwanted) {
			t.Errorf("unused %s in\n%s", unwanted, css)
		}
	}
	body := page.Render(Request{Path: "/"})
	mustContain(t, body, `<pre><code class="language-text">if x then y</code></pre>`)
	dynamic := codePage(t, nil, `<content><code language="json">{{ source }}</code></content>`)
	if css := string(dynamic.Stylesheet().CSS); !strings.Contains(css, ".code-keyword{") || !strings.Contains(css, ".code-deleted{") {
		t.Errorf("CONTROL: code from {{ }} can show any kind:\n%s", css)
	}
}

// A language the engine does not have is a compile error at the attribute, saying what to write: the
// full name of a short one, the spelling of a near miss, every language when nothing is close. A name
// a {{ }} spells out is checked the same, when the page compiles. CONTROL: every language and text
// compile.
func TestCodeUnknownLanguageIsAnErrorWithItsSuggestion(t *testing.T) {
	for _, c := range []struct{ source, says string }{
		{`<content><code language="js">a</code></content>`, `ngôn ngữ "js" chưa có; viết javascript`},
		{`<content><code language="Python">a</code></content>`, `ngôn ngữ "Python" chưa có; viết python`},
		{`<content><code language="javasript">a</code></content>`, `ngôn ngữ "javasript" chưa có; viết javascript`},
		{`<content><code language="cobol">a</code></content>`, `ngôn ngữ "cobol" chưa có. Có: bash, css, diff, go, html, javascript`},
		{`<content><code language="">a</code></content>`, `ngôn ngữ "" chưa có. Có: bash`},
		{`<content><text>Chạy <code language="sh">go run .</code></text></content>`, `ngôn ngữ "sh" chưa có; viết bash`},
		{`<content><code language="{{ windows ? 'pwsh' : 'bash' }}">a</code></content>`, `ngôn ngữ "pwsh" chưa có; viết powershell`},
		{`<content><code language="{{ snippet.language || 'js' }}">a</code></content>`, `ngôn ngữ "js" chưa có; viết javascript`},
	} {
		mustFail(t, content(c.source), where("content.ui", c.source, "language="), c.says)
	}
	for _, language := range highlight.Languages() {
		mustCompile(t, content(`<content><code language="`+language+`">a</code></content>`))
	}
	mustCompile(t, content(`<content><code language="{{ windows ? 'powershell' : 'bash' }}">a</code></content>`))
}

// A language from {{ }} is checked when the page renders: one the engine has colours the code and names
// it; any other value shows the code plain, with no language class, and tells the author while
// developing, once a page however often it renders; no value at all is plain code, nothing to tell.
// CONTROL: the same page with a known language colours it and tells nothing.
func TestCodeLanguageFromDataIsCheckedAtRender(t *testing.T) {
	source := `<content><code language="{{ snippet.language }}">{{ snippet.code }}</code></content>`
	page := codePage(t, nil, source)
	unknown := map[string]any{"snippet": map[string]any{"language": "js", "code": "const a = 1"}}
	body, told := codeRender(page, unknown)
	mustContain(t, body, `<pre><code>const a = 1</code></pre>`)
	mustNotContain(t, body, `code-keyword`, `language-js`)
	again, toldAgain := codeRender(page, unknown)
	if again != body {
		t.Errorf("the same data renders the same page:\n%s\n%s", body, again)
	}
	want := where("content.ui", source, "language=") + `language="{{ snippet.language }}": ngôn ngữ "js" chưa có; viết javascript; khối mã hiện thành chữ thường`
	if len(told) != 1 || told[0] != want || len(toldAgain) != 0 {
		t.Errorf("want one warning, once a page:\n want %s\n got  %q then %q", want, told, toldAgain)
	}
	empty, toldEmpty := codeRender(page, map[string]any{"snippet": map[string]any{"code": "const a = 1"}})
	mustContain(t, empty, `<pre><code>const a = 1</code></pre>`)
	if len(toldEmpty) != 0 {
		t.Errorf("no language is plain code, nothing to tell: %q", toldEmpty)
	}
	// CONTROL
	known, toldKnown := codeRender(page, map[string]any{"snippet": map[string]any{"language": "javascript", "code": "const a = 1"}})
	mustContain(t, known, `<pre><code class="language-javascript"><span class="code-keyword">const</span> a <span class="code-operator">=</span> <span class="code-number">1</span></code></pre>`)
	if len(toldKnown) != 0 {
		t.Errorf("CONTROL: a known language tells nothing, got %q", toldKnown)
	}
}

// Code in a line is <code class="language-…">, a block <pre><code class="language-…">; what the author
// writes on the code (size, weight, if) stays where it always went: on the <pre> of a block, on the
// <code> in a line. A static language and one from {{ }} make the same element. CONTROL: code with no
// language has no class and no span.
func TestCodeInLineAndAsABlock(t *testing.T) {
	body := render(t, `<content><text>Chạy <code language="bash" weight="bold">go run .</code> rồi mở trình duyệt.</text>`+
		`<code language="go" size="small">
      package main

      func main() {}
    </code><code>go run .</code></content>`, nil)
	mustContain(t, body,
		`<p>Chạy <code class="language-bash weight-bold"><span class="code-function">go</span> run .</code> rồi mở trình duyệt.</p>`,
		"<pre class=\"size-small\"><code class=\"language-go\"><span class=\"code-keyword\">package</span> main\n\n<span class=\"code-keyword\">func</span> <span class=\"code-function\">main</span><span class=\"code-punctuation\">()</span> <span class=\"code-punctuation\">{}</span></code></pre>",
		`<pre><code>go run .</code></pre>`)
	dynamic := render(t, `<content><text>Chạy <code language="{{ shell }}">{{ command }}</code></text><code language="{{ language }}" size="small">{{ source }}</code></content>`,
		map[string]any{"shell": "bash", "command": "go run .", "language": "go", "source": "package main"})
	mustContain(t, dynamic,
		`<p>Chạy <code class="language-bash"><span class="code-function">go</span> run .</code></p>`,
		`<pre class="size-small"><code class="language-go"><span class="code-keyword">package</span> main</code></pre>`)
}

// codeSample is source in one language.
type codeSample struct{ language, source string }

// codeSamples are the realistic sources jit/highlight tests every language with, and sources made to
// break a renderer: markup in the code, characters HTML escapes, CRLF, a NUL, invalid UTF-8, an
// unterminated string and comment.
func codeSamples(t *testing.T) []codeSample {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "jit", "highlight", "testdata", "*", "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no samples of jit/highlight (%v)", err)
	}
	var out []codeSample
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, codeSample{language: filepath.Base(filepath.Dir(file)), source: string(data)})
	}
	for _, language := range highlight.Languages() {
		for _, source := range []string{
			"</code><script>alert(\"x\")</script>",
			"a < b && c > d ? 'x' : \"y\" // &amp; &lt;",
			"line one\r\nline two\r\n\ttab\x00nul",
			"bad \xff\xfe utf-8 \"unterminated",
			"/* never closed <!-- ",
			"",
		} {
			out = append(out, codeSample{language: language, source: source})
		}
	}
	return out
}

// Colouring adds spans, never changes the text: what a reader sees (and copies) of the code is the
// source byte for byte, in every language, from {{ }} and written in the page alike (a block shows its
// lines without the blank lines around them and the indent the source gives them all). CONTROL: a
// renderer that drops whitespace fails here.
func TestCodeTextIsTheSource(t *testing.T) {
	page := codePage(t, nil, `<content><code language="{{ language }}">{{ source }}</code></content>`)
	for _, sample := range codeSamples(t) {
		body, told := codeRender(page, map[string]any{"language": sample.language, "source": sample.source})
		if got := codeText(t, body, sample.language); got != sample.source {
			t.Errorf("%s: the text shown is not the source\n got  %q\n want %q", sample.language, clip(got), clip(sample.source))
		}
		if len(told) != 0 {
			t.Errorf("%s: %q", sample.language, told)
		}
		markup, _, err := highlightHTML(sample.language, sample.source)
		if err != nil {
			t.Errorf("%s: %v", sample.language, err)
		}
		if got := html.UnescapeString(codeTags.ReplaceAllString(markup, "")); got != sample.source {
			t.Errorf("%s: highlightHTML is not the source", sample.language)
		}
	}
	source := "<content>\n  <code language=\"go\">\n    // a block\n    func main() {\n    \tprintln(\"x\")\n    }\n  </code>\n</content>"
	body := render(t, source, nil)
	if got, want := codeText(t, body, "go"), "// a block\nfunc main() {\n\tprintln(\"x\")\n}"; got != want {
		t.Errorf("the block shows\n %q, want\n %q", got, want)
	}
	if got := codeText(t, render(t, `<content><text>a <code language="go"> x := 1 </code></text></content>`, nil), "go"); got != " x := 1 " {
		t.Errorf("code in a line keeps its spaces, got %q", got)
	}
}

func clip(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// A token is text, never markup: code holding </code><script> shows those characters, whatever the
// language (in html it is a tag and an attribute to colour, still text), from {{ }} and written in the
// page. The only elements inside the code are the spans the engine writes, and the page holds no
// <script>, <img> or onerror of the code's.
func TestCodeEscapesItsText(t *testing.T) {
	page := codePage(t, nil, `<content><code language="{{ language }}">{{ source }}</code></content>`)
	attack := `</code></pre><script>alert(document.cookie)</script><img src=x onerror="alert(1)">`
	for _, language := range highlight.Languages() {
		body, _ := codeRender(page, map[string]any{"language": language, "source": attack})
		if strings.Contains(body, "<script") || strings.Contains(body, "<img") || strings.Contains(body, `onerror="`) {
			t.Errorf("%s: markup in the code reached the page:\n%s", language, body)
		}
		if got := codeText(t, body, language); got != attack {
			t.Errorf("%s: the code shows %q, not what it holds", language, got)
		}
		open := `<code class="language-` + language + `">`
		inner := body[strings.Index(body, open)+len(open):]
		inner = inner[:strings.Index(inner, "</code>")]
		for _, tag := range codeTags.FindAllString(inner, -1) {
			if tag != "</span>" && !regexp.MustCompile(`^<span class="code-[a-z-]+">$`).MatchString(tag) {
				t.Errorf("%s: an element the engine did not write: %s", language, tag)
			}
		}
	}
	static := render(t, `<content><code language="go">if a < b && c > d { s := "&lt;" }</code></content>`, nil)
	mustContain(t, static, `<span class="code-keyword">if</span> a <span class="code-operator">&lt;</span> b <span class="code-operator">&amp;&amp;</span> c <span class="code-operator">&gt;</span> d`,
		`<span class="code-string">&#34;&amp;lt;&#34;</span>`)
}

// The kinds of code are measured on what lies behind the code like any text (a warning by default, the
// compile error under "strict", word for word): every kind code from {{ }} can show, on the page's
// canvas, a card's paper and a panel, light and dark, on Kit Design's default palette, the kitwork
// example's and the quancaphe conformance fixture's, tells nothing. CONTROL: a site whose info is so
// light that its function colour (info+25) does not read hears it once, at the attribute that names
// the language, with the token to declare (info, never info+25: a step is no token); static code that
// holds no function is not told it; and a code block on a ground nothing reads on is told for each
// colour.
func TestCodeColoursAreMeasured(t *testing.T) {
	grounds := `<content><box><code language="{{ language }}">{{ source }}</code></box>` +
		`<card><code language="{{ language }}" size="small">{{ source }}</code></card>` +
		`<box background="panel" padding="4"><code language="{{ language }}">{{ source }}</code></box>` +
		`<section background="paper"><text>Chạy <code language="{{ language }}">{{ source }}</code></text></section></content>`
	for name, palette := range codeExamplePalettes(t) {
		if got := contrastCompile(t, palette, content(grounds)); got != "" {
			t.Errorf("%s: every kind reads on every ground, got:\n%s", name, got)
		}
	}
	// CONTROL: an info declared by the site so light that info+25 does not read
	light := darkPalette(t, map[string]stylesheet.Color{"info": {Light: "#a8c7fa", Dark: "#a8c7fa"}})
	source := `<content><box background="panel"><code language="{{ language }}">{{ source }}</code></box></content>`
	contrastFails(t, light, content(source), `language=`,
		`chữ function, property (info+25) trong khối mã trên nền panel chỉ 1.77:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: khai lại info trong router.colors bằng một cặp { light: "#…", dark: "#…" } đủ tương phản trên nền panel, hoặc đặt khối mã trên nền khác (background).`)
	contrastPasses(t, light, content(`<content><code language="json">[1, true, null]</code></content>`))
	got := contrastCompile(t, light, content(`<content><code language="go">func main() {}</code></content>`))
	if !strings.HasPrefix(got, `content.ui:1:16: chữ function (info+25) trong khối mã trên nền canvas`) || strings.Contains(got, "\n") {
		t.Errorf("CONTROL: static code holding a function is told it once, got:\n%s", got)
	}
	ink := contrastCompile(t, nil, content(`<content><box background="ink"><code language="diff">{{ source }}</code></box></content>`))
	for _, want := range []string{"chữ keyword, type (brand+35) trong khối mã trên nền ink", "chữ comment, operator, punctuation (ink-30) trong khối mã trên nền ink"} {
		if !strings.Contains(ink, want) {
			t.Errorf("CONTROL: on ink, want %q in:\n%s", want, ink)
		}
	}
	if strings.Contains(ink, "inserted") || strings.Contains(ink, "deleted") {
		t.Errorf("inserted and deleted read on their own fill, whatever the ground:\n%s", ink)
	}
	// in dark a code colour is its engine colour as written there (brand, never kitwork-code-brand), and
	// the token to declare is brand; light says brand+35 as before
	tinted := contrastCompile(t, darkPalette(t), content(`<content><box background="danger-70"><code language="go">func main() { var x = "a" }</code></box></content>`))
	for _, want := range []string{
		`content.ui:1:44: chữ keyword (brand) trong khối mã trên nền danger-70 chỉ 3.48:1 ở chế độ tối; chữ thường cần 4.5:1 (chữ lớn 3:1). Chữ này engine tô theo router.colors: khai lại brand trong router.colors bằng một cặp { light: "#…", dark: "#…" } đủ tương phản trên nền danger, hoặc đặt khối mã trên nền khác (background).`,
		`chữ string (success+35) trong khối mã trên nền danger-70 chỉ 3.83:1 ở chế độ sáng`,
	} {
		if !strings.Contains(tinted, want) {
			t.Errorf("in dark, want %q in:\n%s", want, tinted)
		}
	}
	if strings.Contains(tinted, "kitwork-") {
		t.Errorf("a message names an engine colour's property:\n%s", tinted)
	}
}

// codeExamplePalettes are Kit Design's default palette with its dark look, the kitwork example's
// router.colors and the quancaphe conformance fixture's theme (with its quan-ly branch), as
// stylesheet's code tests read them.
func codeExamplePalettes(t *testing.T) map[string]*stylesheet.Palette {
	t.Helper()
	kitwork := map[string]stylesheet.Color{ // docs/examples/kitwork/kitwork.localhost/router.work
		"brand": {Light: "#f5225f", Dark: "#ff6b6b"}, "canvas": {Light: "#ffeaec", Dark: "#16161e"},
		"paper": {Light: "#ffffff", Dark: "#1e2030"}, "panel": {Light: "#f7eff1", Dark: "#181a26"},
		"line": {Light: "#e5e3e5", Dark: "#2c3145"}, "ink": {Light: "#242442", Dark: "#c0caf5"},
		"success": {Light: "#0e9f6e"}, "warning": {Light: "#c98a1e"}, "danger": {Light: "#ef4444"}, "info": {Light: "#3b82f6"},
	}
	quancaphe := map[string]stylesheet.Color{ // the quancaphe conformance fixture (engine/conformance/testdata/ui/quancaphe/cases.json theme)
		"brand": {Light: "#c8742c"}, "canvas": {Light: "#f6f3ee", Dark: "#141110"},
		"panel": {Light: "#efe9e1", Dark: "#1a1613"}, "paper": {Light: "#ffffff", Dark: "#1f1a17"},
		"ink": {Light: "#2b211b", Dark: "#f3ece6"}, "line": {Light: "#e6ded5", Dark: "#3a302a"},
		"success": {Light: "#2f9e63"}, "warning": {Light: "#c98a1e"}, "danger": {Light: "#e5484d"},
	}
	return map[string]*stylesheet.Palette{
		"Kit Design":        darkPalette(t),
		"kitwork":           darkPalette(t, kitwork),
		"quancaphe":         darkPalette(t, quancaphe),
		"quancaphe/quan-ly": darkPalette(t, quancaphe, map[string]stylesheet.Color{"brand": {Light: "#2563eb"}}),
	}
}

// Code from {{ }} is coloured once for every page: the same language and text come back from the cache
// as the same HTML. The cache holds a bounded number of bytes: an entry past an eighth of the bound is
// never kept, and one that would pass the bound empties it first. CONTROL: what it gives is what
// highlightHTML writes.
func TestCodeCacheIsBounded(t *testing.T) {
	cache := &codeCache{limit: 4096}
	first, err := cache.html("go", "package main")
	if err != nil {
		t.Fatal(err)
	}
	want, _, _ := highlightHTML("go", "package main")
	if first != want {
		t.Errorf("CONTROL: the cache gives what highlightHTML writes:\n got  %s\n want %s", first, want)
	}
	if again, _ := cache.html("go", "package main"); again != first || len(cache.entries) != 1 {
		t.Errorf("the same code is one entry, got %d", len(cache.entries))
	}
	large := strings.Repeat("x := 1\n", 100)
	if _, err := cache.html("go", large); err != nil {
		t.Fatal(err)
	}
	_, kept := cache.entries[codeKey{language: "go", source: large}]
	_, still := cache.entries[codeKey{language: "go", source: "package main"}]
	if kept || !still || len(cache.entries) != 1 {
		t.Errorf("an entry past an eighth of the bound is not kept, and empties nothing: kept %v, the first still there %v", kept, still)
	}
	for i := 0; i < 40; i++ {
		if _, err := cache.html("go", strings.Repeat("y", i+1)+" := 1"); err != nil {
			t.Fatal(err)
		}
		if cache.size > cache.limit {
			t.Fatalf("the cache holds %d bytes, past its bound %d", cache.size, cache.limit)
		}
	}
	if _, err := cache.html("javascript", strings.Repeat("a", highlight.MaximumSourceSize+1)); err == nil {
		t.Error("a source past highlight.MaximumSourceSize is told")
	}
}

// Code past highlight.MaximumSourceSize shows plain, and the author hears of it while developing:
// written in the page when it compiles, from {{ }} when it renders, once a page.
func TestCodeTooLargeShowsPlain(t *testing.T) {
	big := strings.Repeat("a", highlight.MaximumSourceSize+1)
	page := codePage(t, nil, `<content><code language="javascript">{{ source }}</code></content>`)
	body, told := codeRender(page, map[string]any{"source": big})
	if !strings.Contains(body, `<pre><code class="language-javascript">`+big+`</code></pre>`) {
		t.Error("code past the bound shows plain")
	}
	_, again := codeRender(page, map[string]any{"source": big})
	if len(told) != 1 || !strings.Contains(told[0], "quá giới hạn 1048576 byte") || len(again) != 0 {
		t.Errorf("want one warning, once a page: %q then %q", told, again)
	}
	written := memorySite("", map[string]string{"/site/content.ui": `<content><code language="text">` + big + `</code></content>`})
	written.Reload = true
	compiled, err := written.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	if w := compiled.Warnings(); len(w) != 1 || !strings.Contains(w[0].String(), "content.ui:1:10: <code language=\"text\">: khối mã text dài") {
		t.Errorf("written in the page, told when it compiles: %v", w)
	}
}

// A token's colour is the engine's, so a pair of it short of contrast is told at its code, with the
// engine's advice, never folded into the line of the background that makes it short, the way the
// author's own texts on one background at two places or more are (one line at the background): each
// code's kinds are their own lines at its language=, by default as under "strict". The code's own text
// (the page's ink) is told once, at the background.
func TestCodeTokensAreToldAtTheirCode(t *testing.T) {
	source := "<content><box background=\"ink\"><code language=\"go\">func main() {}</code>\n<code language=\"go\">func main() {}</code></box></content>"
	warned, strict := groupedCompile(t, nil, content(source))
	own := `content.ui:1:15: chữ ink mặc định của trang trên nền ink chỉ 1.00:1`
	first, second := "content.ui:1:38: chữ ", "content.ui:2:7: chữ "
	var told, atFirst, atSecond int
	for _, line := range warned {
		switch {
		case strings.HasPrefix(line, own):
			told++
		case strings.HasPrefix(line, first) && strings.Contains(line, "trong khối mã trên nền ink"):
			atFirst++
		case strings.HasPrefix(line, second) && strings.Contains(line, "trong khối mã trên nền ink"):
			atSecond++
		default:
			t.Errorf("a line nobody expects (a group at the background?): %s", line)
		}
	}
	if told != 1 || atFirst != 3 || atSecond != 3 {
		t.Errorf("want the code's text once and its three colours (keyword, function, punctuation) at each language=, got:\n%s", strings.Join(warned, "\n"))
	}
	tokens := 0
	for _, line := range strict {
		if strings.Contains(line, "trong khối mã trên nền ink") {
			tokens++
			if !strings.Contains(line, "Chữ này engine tô theo router.colors") {
				t.Errorf("strict: a token's advice is the engine's: %s", line)
			}
			// a rung of ink on the ink it stands on: declaring ink anew would move both, so the advice
			// is the ground (review of 12/10/2026); any other rung may be declared anew
			if strings.Contains(line, "(ink-30)") != strings.Contains(line, "mà nền cũng là ink: đặt khối mã trên nền khác (background).") ||
				strings.Contains(line, "(ink-30)") && strings.Contains(line, "khai lại ink") {
				t.Errorf("strict: a rung of ink on ink is told to change the ground, never to declare ink anew: %s", line)
			}
		}
	}
	if tokens != 6 {
		t.Errorf("strict tells the same tokens, got %d of 6:\n%s", tokens, strings.Join(strict, "\n"))
	}
}
