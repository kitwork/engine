package js

import (
	"encoding/json"
	"fmt"
	"html"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/utilities/richtext"
)

// Markdown both ways (parts/editor-markdown.js, 21/10/2026), in a real browser: the editor's own reading
// (read: DOMParser, inert, onto grammar v2) and writing (serialize) around the part, taken from the page
// as the formats' part hands them to it. A value is the canonical HTML richtext.Clean writes; a
// Markdown text is read back as the editor reads it (canonical below).

// markdownHands is what each Markdown proof starts with: the editor switched to Markdown once (so its
// formats' part loads the Markdown part, preloaded here, with the editor's hands, which the wrapper
// keeps), then canonical(text) and write(value) on those hands.
const markdownHands = `
var hands = null, converter = null, make = window[Symbol.for("kitwork:editor-markdown")];
window[Symbol.for("kitwork:editor-markdown")] = function (editor) { hands = editor; return converter = make(editor); };
press($("f-markdown"));
assert(await until(function () { return !!converter; }), "the formats' part made the converter with the editor's hands");
function doc(markup) { return new DOMParser().parseFromString(markup, "text/html"); }
function canonical(text) { return hands.serialize(hands.read(doc(converter.html(text)).body)).html; }
function write(value) { return converter.markdown(hands.read(doc(value).body)); }
`

// editorMarkdownPart is the tag of the Markdown part, loaded with the page so a proof can wrap it.
func editorMarkdownPart() string {
	return `<script src="` + PartAsset("editor-markdown").Path + `"></script>`
}

// runMarkdownDump runs a Markdown proof (its steps after markup, on an editor holding <p>x</p>) and
// returns the DOM it dumped.
func runMarkdownDump(t *testing.T, label, markup, steps string) string {
	t.Helper()
	page := editorMarkdownPart() + `<form id="form" method="post">` + editorFormatWire("<p>x</p>", "rich", "") + `</form>` + markup
	return runBrowserDump(t, label, page+editorProof(editorV2Script+editorFormatScript+markdownHands+steps))
}

// dumped reads the JSON a proof wrote into <pre id="id">.
func dumped(t *testing.T, out, id string, into any) {
	t.Helper()
	open := `<pre id="` + id + `">`
	i := strings.Index(out, open)
	if i < 0 {
		t.Fatalf("the proof wrote no %s:\n%.2000s", id, out)
	}
	j := strings.Index(out[i:], "</pre>")
	if err := json.Unmarshal([]byte(html.UnescapeString(out[i+len(open):i+j])), into); err != nil {
		t.Fatalf("%s: %v", id, err)
	}
}

// richtextCorpus is every value of utilities/richtext's corpus (testdata: xss.txt, paste.txt, the fuzz
// seeds), read here as its corpus_test.go reads it (that package's test files are its own): each
// source cleaned, the empty ones left out.
func richtextCorpus(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "..", "utilities", "richtext", "testdata")
	names := map[string]string{"NUL": "\x00", "SOH": "\x01", "TAB": "\t", "LF": "\n", "CR": "\r", "FF": "\f", "VT": "\v",
		"ESC": "\x1b", "DEL": "\x7f", "NBSP": "\u00a0", "ZWSP": "\u200b", "BOM": "\ufeff"}
	expand := regexp.MustCompile(`\{([A-Z]+|U\+[0-9A-F]{4,6}|BYTE:[0-9A-F]{2})\}`)
	expanded := func(s string) string {
		return expand.ReplaceAllStringFunc(s, func(m string) string {
			name := m[1 : len(m)-1]
			switch {
			case strings.HasPrefix(name, "U+"):
				n, _ := strconv.ParseUint(name[2:], 16, 32)
				return string(rune(n))
			case strings.HasPrefix(name, "BYTE:"):
				n, _ := strconv.ParseUint(name[5:], 16, 8)
				return string([]byte{byte(n)})
			}
			if c, ok := names[name]; ok {
				return c
			}
			return m
		})
	}
	var values []string
	seen := map[string]bool{}
	add := func(source string) {
		if v := richtext.Clean(source).HTML; v != "" && !seen[v] {
			seen[v] = true
			values = append(values, v)
		}
	}
	for _, file := range []string{"xss.txt", "paste.txt"} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		inWant, cases := false, 0
		flush := func() {
			for len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
			if inWant {
				add(expanded(strings.Join(lines, "\n")))
			} else if cases > 0 {
				add(expanded(strings.Join(lines, "\n")))
			}
			lines = nil
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			switch {
			case strings.HasPrefix(line, "== "):
				flush()
				cases++
				inWant = false
			case cases == 0:
			case line == "-- want" && !inWant:
				flush() // the source, cleaned
				inWant = true
			default:
				lines = append(lines, line)
			}
		}
		flush()
		if cases == 0 {
			t.Fatalf("%s holds no case", file)
		}
	}
	seeds, _ := filepath.Glob(filepath.Join(dir, "fuzz", "FuzzClean", "*"))
	for _, seed := range seeds {
		data, _ := os.ReadFile(seed)
		for _, line := range strings.Split(string(data), "\n") {
			if s, err := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(line, "string("), ")")); err == nil && strings.HasPrefix(line, "string(") {
				add(s)
			}
		}
	}
	return values
}

// generatedValues are n values of the grammar made at random (seeded: the same every run): paragraphs
// and headings (some aligned), quotes (a part aligned), lists of both kinds side by side, code blocks
// with backticks and blank lines, rules; marks over parts of words, links (spaces, parentheses, & and
// refused addresses), line breaks, code with line breaks; words holding what Markdown reads (* _ ` [ ]
// # > - + = | ~ & < \ "1."), Vietnamese. Each is cleaned by richtext, so it is a value the server holds.
func generatedValues(n int, seed int64) []string {
	r := rand.New(rand.NewSource(seed))
	words := []string{"một", "hai", "Ba", "bốn", "năm", "snake_case", "a*b", "_x_", "*", "**", "_", "__", "`", "``", "[x]", "]", "[",
		"#", "##", "# tựa", ">", "> q", "-", "- ", "+", "---", "===", "=", "1.", "2)", "|", "a | b", "~", "~~", "&", "&amp;", "&copy;", "&#169;",
		"<", "<b>", "</p>", "<!--", "\\", "\\*", "!", "![", "(", ")", "\"", "'", "đường", "Ünïcödé", "ﬁ", "😀", "x", "y", "z", "1", "10.", "- -", "***"}
	hrefs := []string{"https://kitwork.io", "https://kitwork.io/a b", "/x(y)", "/a?b=1&c=2", "/a?x=&copy;", "mailto:a@b.vn", "tel:+84 1", "javascript:alert(1)",
		"/path_with_underscores", "https://e.x/<tag>", "/back\\slash", "https://e.x/a)b"}
	marks := []string{"strong", "em", "u", "s", "code"}
	phrase := func() string {
		var b strings.Builder
		for i, k := 0, 1+r.Intn(6); i < k; i++ {
			text := html.EscapeString(words[r.Intn(len(words))])
			if r.Intn(3) == 0 {
				text += " "
			}
			if r.Intn(4) == 0 {
				text = " " + text
			}
			for _, mark := range marks {
				if r.Intn(5) == 0 {
					text = "<" + mark + ">" + text + "</" + mark + ">"
				}
			}
			if r.Intn(7) == 0 {
				text = `<a href="` + html.EscapeString(hrefs[r.Intn(len(hrefs))]) + `">` + text + "</a>"
			}
			if r.Intn(8) == 0 && i > 0 {
				b.WriteString("<br>")
			}
			b.WriteString(text)
		}
		return b.String()
	}
	align := func() string {
		switch r.Intn(6) {
		case 0:
			return ` class="align-center"`
		case 1:
			return ` class="align-right"`
		}
		return ""
	}
	var values []string
	for len(values) < n {
		var b strings.Builder
		for i, k := 0, 1+r.Intn(5); i < k; i++ {
			switch r.Intn(9) {
			case 0, 1, 2:
				b.WriteString("<p" + align() + ">" + phrase() + "</p>")
			case 3:
				level := strconv.Itoa(2 + r.Intn(5))
				b.WriteString("<h" + level + align() + ">" + phrase() + "</h" + level + ">")
			case 4:
				b.WriteString("<blockquote><p>" + phrase() + "</p><p" + align() + ">" + phrase() + "</p></blockquote>")
			case 5:
				tag := []string{"ul", "ol"}[r.Intn(2)]
				for lists := 1 + r.Intn(2); lists > 0; lists-- {
					b.WriteString("<" + tag + ">")
					for items := 1 + r.Intn(3); items > 0; items-- {
						b.WriteString("<li>" + phrase() + "</li>")
					}
					b.WriteString("</" + tag + ">")
				}
			case 6:
				code := []string{"func main() {\n\tx := 1\n}", "a ``` b\n\n\nc", "````\n~~~", "<b>&amp;</b>", "  lề\n", "x\n\n"}[r.Intn(6)]
				b.WriteString("<pre><code>" + html.EscapeString(code) + "</code></pre>")
			case 7:
				b.WriteString("<hr>")
			case 8:
				b.WriteString("<p>" + phrase() + "<br>" + phrase() + "</p>")
			}
		}
		if v := richtext.Clean(b.String()).HTML; v != "" {
			values = append(values, v)
		}
	}
	return values
}

// writtenValues are n values as a writer makes them (seeded): marks and links over whole words, the
// spaces and the punctuation between words outside them, headings, quotes, lists, code, rules. Their
// Markdown is Markdown all through: no mark written as a tag, no block as its HTML line.
func writtenValues(n int, seed int64) []string {
	r := rand.New(rand.NewSource(seed))
	words := []string{"Xin", "chào", "thế", "giới", "bài", "viết", "đường", "Kitwork", "editor", "snake_case", "v2.0", "100%", "a+b", "x", "AI", "2026"}
	marks := []string{"strong", "em", "u", "s", "code"}
	line := func() string {
		var b strings.Builder
		for i, k := 0, 2+r.Intn(8); i < k; i++ {
			if i > 0 {
				b.WriteString([]string{" ", " ", " ", ", ", ". ", " (", ") ", ": ", " – "}[r.Intn(9)])
			}
			text := words[r.Intn(len(words))]
			for _, mark := range marks {
				if r.Intn(6) == 0 {
					text = "<" + mark + ">" + text + "</" + mark + ">"
				}
			}
			if r.Intn(9) == 0 {
				text = `<a href="` + []string{"https://kitwork.io", "/bai/1", "mailto:ai@kitwork.vn"}[r.Intn(3)] + `">` + text + "</a>"
			}
			b.WriteString(text)
		}
		if r.Intn(6) == 0 {
			b.WriteString("<br>" + words[r.Intn(len(words))])
		}
		return b.String()
	}
	var values []string
	for len(values) < n {
		var b strings.Builder
		for i, k := 0, 1+r.Intn(5); i < k; i++ {
			switch r.Intn(7) {
			case 0, 1, 2:
				b.WriteString("<p>" + line() + "</p>")
			case 3:
				level := strconv.Itoa(2 + r.Intn(4))
				b.WriteString("<h" + level + ">" + strings.ReplaceAll(line(), "<br>", " ") + "</h" + level + ">")
			case 4:
				b.WriteString("<blockquote><p>" + line() + "</p></blockquote>")
			case 5:
				tag := []string{"ul", "ol"}[r.Intn(2)]
				b.WriteString("<" + tag + "><li>" + line() + "</li><li>" + line() + "</li></" + tag + ">")
			case 6:
				b.WriteString("<pre><code>if (a &lt; b) {\n  return `x`;\n}</code></pre><hr>")
			}
		}
		if v := richtext.Clean(b.String()).HTML; v != "" {
			values = append(values, v)
		}
	}
	return values
}

// For every value the grammar allows, Markdown read back is the value: canonical(markdown(value)) ==
// value, over every value of richtext's corpus (XSS payloads and clipboards, cleaned), 600 made at
// random to be hard (marks starting or ending with a space or punctuation, words Markdown reads) and 300
// as a writer makes them, whose Markdown has no tag nor HTML line. Every value is the editor's own
// first (its read and serialize give it back), and Clean's.
// CONTROL (run 21/10/2026, copies of editor-markdown.js through -overlay, controls.py in the scratchpad's
// editor-formats): with markdown() giving its first writing without reading it back (no fallback to tags
// or HTML), values did not read back (the first, value 111); with starts() escaping nothing at a line's
// start, the reading back still held (the blocks fell back to tags and HTML lines), and the values as a
// writer makes them were no longer Markdown all through (the first, value 772).
// Since 21/10/2026 a value may end in a rule: a final-rule value after each kind of block is read back
// too, and every value that ends in a rule (those made at random among them) ends its Markdown in a
// "---" line of its own.
// CONTROL (21/10/2026, copies through -overlay, controls.py in the scratchpad's final-rule): with the
// editor's read() (v2.0.0.js) dropping a rule at the end again (its kept.pop() put back), the 108 values
// ending in a rule failed (the first, value 70 of richtext's corpus: "the editor reads it as" the value
// without its rule); with markdown() (editor-markdown.js) writing a rule as its HTML when it falls back
// to block by block (as it did before: "---" alone reads as nothing, a rule first), values 147 and 510
// ended their Markdown in <hr> and failed.
func TestBrowserEditorMarkdownIdentity(t *testing.T) {
	corpus, written := richtextCorpus(t), writtenValues(300, 2110)
	finals := []string{"<p>a</p>\n<hr>", "<h3>a</h3>\n<hr>", "<blockquote>\n<p>a</p>\n</blockquote>\n<hr>", "<ul>\n<li>a</li>\n</ul>\n<hr>",
		"<ol>\n<li>a</li>\n</ol>\n<hr>", "<pre><code>a\n---</code></pre>\n<hr>", "<p>a<br>---</p>\n<hr>", `<p class="align-center">a</p>` + "\n<hr>"}
	values := append(append(append(append([]string{}, corpus...), generatedValues(600, 21102026)...), finals...), written...)
	payload, _ := json.Marshal(values)
	out := runMarkdownDump(t, "editor markdown identity", `<pre id="values" hidden>`+html.EscapeString(string(payload))+`</pre><pre id="identity"></pre>`, `
  var values = JSON.parse($("values").textContent), results = values.map(function (value) {
    var own = hands.serialize(hands.read(doc(value).body)).html, text = write(value);
    return { own: own, text: text, back: canonical(text) };
  });
  $("identity").textContent = JSON.stringify(results);
`)
	var results []struct{ Own, Text, Back string }
	dumped(t, out, "identity", &results)
	if len(results) != len(values) {
		t.Fatalf("%d results for %d values", len(results), len(values))
	}
	tagged, lines, ruled := 0, 0, 0
	for i, v := range values {
		got := results[i]
		if c := richtext.Clean(v).HTML; c != v {
			t.Fatalf("value %d is not Clean's: %q", i, v)
		}
		if got.Own != v {
			t.Errorf("value %d: the editor reads it as %q, want %q", i, got.Own, v)
			continue
		}
		if got.Back != v {
			t.Errorf("value %d does not read back:\n value    %q\n markdown %q\n back     %q", i, v, got.Text, got.Back)
		}
		if strings.HasSuffix(v, "\n<hr>") {
			ruled++
			if !strings.HasSuffix(got.Text, "\n\n---") {
				t.Errorf("value %d ends in a rule, its Markdown does not end in a --- line:\n value    %q\n markdown %q", i, v, got.Text)
			}
		}
		hasTags := regexp.MustCompile(`<(strong|em|s)>`).MatchString(got.Text)
		hasLine := regexp.MustCompile(`(?m)^(> )?<(p|h[2-6])[ >]`).MatchString(got.Text) && !strings.Contains(v, "align-")
		if hasTags {
			tagged++
		}
		if hasLine {
			lines++
		}
		if i >= len(values)-len(written) && (hasTags || hasLine) {
			t.Errorf("value %d, as a writer makes it, is not Markdown all through:\n value    %q\n markdown %q", i, v, got.Text)
		}
	}
	t.Logf("%d values (%d of richtext's corpus, 600 hard, %d ending in a rule, %d as written): %d with a tag for a mark, %d with a block as its HTML line though not aligned, %d end in a rule",
		len(values), len(corpus), len(finals), len(written), tagged, lines, ruled)
	if ruled < 2*len(finals) {
		t.Errorf("only %d values end in a rule: the values no longer exercise a final rule", ruled)
	}
}

// The reading, construct by construct (the want written by hand, each Clean's own).
// CONTROL (21/10/2026, a copy of v2.0.0.js through -overlay, controls.py in the scratchpad's
// final-rule): with the editor's read() dropping a rule at the end again (its kept.pop() put back), the
// four rows of a rule that ends the post failed (read without it).
// CONTROL (run 21/10/2026, copies of editor-markdown.js through -overlay): with emphasis() matching an
// opener without the rule of three, "*a**b***" failed; with link() taking a title with no white space
// before it, [a](<x>"t") became a link and failed. The rows of the review of 21/10/2026 (copies through
// -overlay, fix/controls.py in the scratchpad's editor-formats): with inert() leaving every tag as HTML,
// "Ô dùng thẻ <textarea> của HTML…" read as <p>Ô dùng thẻ</p>; with a line of <textarea> opening a
// block of HTML though no end follows, the <textarea> line and "Đoạn hai." read as one paragraph; with a
// lazy line taken after any block, the list with a fence took "Xong." into its item; with a lazy ===
// left as written, the quote's lazy === was lost; with every name a reference, "&notit; …" read as
// ¬it; with parentheses nested without end, the destination 33 deep became a link.
func TestBrowserEditorMarkdownReading(t *testing.T) {
	cases := [][2]string{
		// headings: # and ## are h2 (h1 is the page's title); setext: === an h2, --- an h3
		{"# Một", "<h2>Một</h2>"}, {"## Hai", "<h2>Hai</h2>"}, {"### Ba ###", "<h3>Ba</h3>"}, {"###### Sáu", "<h6>Sáu</h6>"},
		{"####### bảy", "<p>####### bảy</p>"}, {"#không", "<p>#không</p>"}, {"## Tựa \\#", "<h2>Tựa #</h2>"},
		{"Tựa\n===", "<h2>Tựa</h2>"}, {"Nhỏ\n---", "<h3>Nhỏ</h3>"},
		// paragraphs and breaks
		{"a\nb", "<p>a b</p>"}, {"a\n\n\nb", "<p>a</p>\n<p>b</p>"}, {"a\\\nb", "<p>a<br>b</p>"}, {"a  \nb", "<p>a<br>b</p>"}, {"a\\", "<p>a\\</p>"},
		// quotes: one level of paragraphs, a deeper one flattened, lazy lines
		{"> a\n> b\n>\n> c", "<blockquote>\n<p>a b</p>\n<p>c</p>\n</blockquote>"}, {"> > sâu", "<blockquote>\n<p>sâu</p>\n</blockquote>"},
		{"> a\nlazy", "<blockquote>\n<p>a lazy</p>\n</blockquote>"}, {"> a\n\n> b", "<blockquote>\n<p>a</p>\n</blockquote>\n<blockquote>\n<p>b</p>\n</blockquote>"},
		// lists: - * + and 1. 1), one level, deeper ones flattened; another bullet another list
		{"- a\n- b", "<ul>\n<li>a</li>\n<li>b</li>\n</ul>"}, {"* a\n+ b", "<ul>\n<li>a</li>\n</ul>\n<ul>\n<li>b</li>\n</ul>"},
		{"1. a\n2. b", "<ol>\n<li>a</li>\n<li>b</li>\n</ol>"}, {"1) a\n2) b", "<ol>\n<li>a</li>\n<li>b</li>\n</ol>"},
		{"- a\n  - b\n    - c", "<ul>\n<li>a</li>\n<li>b</li>\n<li>c</li>\n</ul>"}, {"- a\n\n  b", "<ul>\n<li>a<br>b</li>\n</ul>"},
		{"a\n- b", "<p>a</p>\n<ul>\n<li>b</li>\n</ul>"}, {"a\n2. b", "<p>a 2. b</p>"}, {"- a\nlazy", "<ul>\n<li>a lazy</li>\n</ul>"},
		// code: fenced (``` or ~~~, the info string ignored, no last newline), indented
		{"```go\nfunc() {\n\treturn\n}\n```", "<pre><code>func() {\n\treturn\n}</code></pre>"}, {"~~~\n```\n~~~", "<pre><code>```</code></pre>"},
		{"```\na\n\n\nb\n```", "<pre><code>a\n\n\nb</code></pre>"}, {"    a\n\n    b", "<pre><code>a\n\nb</code></pre>"},
		// rules
		{"a\n\n---\n\nb", "<p>a</p>\n<hr>\n<p>b</p>"}, {"a\n\n***\n\nb", "<p>a</p>\n<hr>\n<p>b</p>"}, {"a\n\n_ _ _\n\nb", "<p>a</p>\n<hr>\n<p>b</p>"},
		// a rule that ends the post stays (21/10/2026); one first goes, two in a row are one
		{"a\n\n---", "<p>a</p>\n<hr>"}, {"a\n\n---\n", "<p>a</p>\n<hr>"}, {"- a\n\n***", "<ul>\n<li>a</li>\n</ul>\n<hr>"}, {"> a\n\n___\n\n---", "<blockquote>\n<p>a</p>\n</blockquote>\n<hr>"},
		{"---\n\na", "<p>a</p>"}, {"---", ""},
		// emphasis and strong by CommonMark's delimiter rules
		{"*a* _b_ **c** __d__", "<p><em>a</em> <em>b</em> <strong>c</strong> <strong>d</strong></p>"},
		{"***a***", "<p><strong><em>a</em></strong></p>"}, {"*a**b***", "<p><em>a</em><strong><em>b</em></strong></p>"},
		{"**a***b*", "<p><strong>a</strong><em>b</em></p>"}, {"snake_case_name a_b_", "<p>snake_case_name a_b_</p>"},
		{"2*3*4", "<p>2<em>3</em>4</p>"}, {"a * b * c", "<p>a * b * c</p>"}, {"**a.**b", "<p>**a.**b</p>"}, {"*(a)*", "<p><em>(a)</em></p>"},
		// strikethrough: two tildes
		{"~~gạch~~ ~một~ ~~~ba~~~", "<p><s>gạch</s> ~một~ ~~~ba~~~</p>"},
		// code spans
		{"`a` `` b`c `` ` d `", "<p><code>a</code> <code>b`c</code> <code>d</code></p>"}, {"`*không*`", "<p><code>*không*</code></p>"},
		{"`mở", "<p>`mở</p>"},
		// links: the title goes, javascript: and data: refused (the words stay), autolinks
		{`[trang](https://kitwork.io "tựa")`, `<p><a href="https://kitwork.io">trang</a></p>`}, {"[a](</x y>)", `<p><a href="/x y">a</a></p>`},
		{"[a](/b(c)d)", `<p><a href="/b(c)d">a</a></p>`}, {"[a](javascript:alert(1))", "<p>a</p>"}, {"[a](data:text/html,x)", "<p>a</p>"},
		{"[a](/b\"c\")", `<p><a href="/b&quot;c&quot;">a</a></p>`}, {"[a](<x>\"t\")", `<p>[a]("t")</p>`}, {"[**đậm**](/x)", `<p><a href="/x"><strong>đậm</strong></a></p>`},
		{"<https://kitwork.io/a>", `<p><a href="https://kitwork.io/a">https://kitwork.io/a</a></p>`}, {"<ai@kitwork.vn>", `<p><a href="mailto:ai@kitwork.vn">ai@kitwork.vn</a></p>`},
		{"[a [b](/x)](/y)", `<p>[a <a href="/x">b</a>](/y)</p>`}, {"[ref][1]\n\n[1]: /x", "<p>[ref][1]</p>\n<p>[1]: /x</p>"},
		// escapes and character references
		{"\\*a\\* \\_b\\_ \\[c\\] \\# \\\\", "<p>*a* _b_ [c] # \\</p>"}, {"&copy; &#169; &#xA9; &amp; & &nope;", "<p>© © © &amp; &amp; &amp;nope;</p>"},
		// raw HTML, read onto the grammar: u stays, an aligned block stays, the rest goes
		{"a <u>b</u> <span>c</span> <b>d</b>", "<p>a <u>b</u> c <strong>d</strong></p>"},
		{`<p class="align-center">Giữa</p>`, `<p class="align-center">Giữa</p>`}, {`<div align="right">Phải</div>`, `<p class="align-right">Phải</p>`},
		{"<script>alert(1)</script>\n\nsau", "<p>sau</p>"}, {"<!-- chú thích -->\nx", "<p>x</p>"},
		// images: their words; tables: their cells' words, each a paragraph
		{"![Ảnh **đẹp**](x.png) sau", "<p>Ảnh đẹp sau</p>"}, {"| a | b |\n|---|:-:|\n| 1 | 2 \\| 3 |", "<p>a</p>\n<p>b</p>\n<p>1</p>\n<p>2 | 3</p>"},
		// (the review of 21/10/2026) an element whose words HTML does not read as markup, with no end tag
		// in its block, is words (GFM's tag filter): read as HTML it took every word after it; closed in
		// its block it is HTML, left out with what it holds
		{"Ô dùng thẻ <textarea> của HTML.\n\nĐoạn hai.\n\n## Phần ba", "<p>Ô dùng thẻ &lt;textarea&gt; của HTML.</p>\n<p>Đoạn hai.</p>\n<h2>Phần ba</h2>"},
		{"Dùng <plaintext> nhé.\n\nĐoạn hai.", "<p>Dùng &lt;plaintext&gt; nhé.</p>\n<p>Đoạn hai.</p>"}, {"a<script>x()</script>b", "<p>ab</p>"},
		{"a <title>b</title> c <style>d</style> e", "<p>a c e</p>"}, {"<textarea>\n\nĐoạn hai.", "<p>&lt;textarea&gt;</p>\n<p>Đoạn hai.</p>"},
		{"<iframe>\nsau\n\nĐoạn hai.", "<p>&lt;iframe&gt; sau</p>\n<p>Đoạn hai.</p>"}, {"<xmp>\n\nĐoạn hai.", "<p>&lt;xmp&gt;</p>\n<p>Đoạn hai.</p>"},
		{"x `</textarea>` <textarea> y\n\nz", "<p>x <code>&lt;/textarea&gt;</code> &lt;textarea&gt; y</p>\n<p>z</p>"},
		// a lazy line goes on a paragraph only: after a fence, a code block or a heading the quote or the
		// list ends; a lazy === is words
		{"- Cài đặt:\n  ```\n  go get kitwork\n  ```\nXong.", "<ul>\n<li>Cài đặt:<code><br>go get kitwork</code></li>\n</ul>\n<p>Xong.</p>"},
		{"> ## Tựa\nĐoạn sau trích dẫn.", "<blockquote>\n<p>Tựa</p>\n</blockquote>\n<p>Đoạn sau trích dẫn.</p>"},
		{"> Trích\nlazy\n===", "<blockquote>\n<p>Trích lazy ===</p>\n</blockquote>"}, {"> ```\n> mã\nsau", "<blockquote>\n<p><code>mã</code></p>\n</blockquote>\n<p>sau</p>"},
		{"- a\n\n      code\nb", "<ul>\n<li>a<code><br>code</code></li>\n</ul>\n<p>b</p>"}, {"> > a\nb", "<blockquote>\n<p>a b</p>\n</blockquote>"},
		// a character reference HTML reads only in part is words (&notit; is not ¬it;)
		{"&notit; &copyx; &ampx; &ltfoo; &gtbar; &semi; &copy; &MadeUpEntity; &#169;", "<p>&amp;notit; &amp;copyx; &amp;ampx; &amp;ltfoo; &amp;gtbar; ; © &amp;MadeUpEntity; ©</p>"},
		// parentheses in a destination nest 32 deep at most, as cmark
		{"[a](/" + strings.Repeat("(", 32) + "x" + strings.Repeat(")", 32) + ")", `<p><a href="/` + strings.Repeat("(", 32) + "x" + strings.Repeat(")", 32) + `">a</a></p>`},
		{"[a](/" + strings.Repeat("(", 33) + "x" + strings.Repeat(")", 33) + ")", "<p>[a](/" + strings.Repeat("(", 33) + "x" + strings.Repeat(")", 33) + ")</p>"},
	}
	var inputs []string
	for _, c := range cases {
		if got := richtext.Clean(c[1]).HTML; got != c[1] {
			t.Fatalf("the want of %q is not Clean's: %q", c[0], got)
		}
		inputs = append(inputs, c[0])
	}
	payload, _ := json.Marshal(inputs)
	out := runMarkdownDump(t, "editor markdown reading", `<pre id="inputs" hidden>`+html.EscapeString(string(payload))+`</pre><pre id="read"></pre>`, `
  $("read").textContent = JSON.stringify(JSON.parse($("inputs").textContent).map(canonical));
`)
	var got []string
	dumped(t, out, "read", &got)
	for i, c := range cases {
		if got[i] != c[1] {
			t.Errorf("%q\n got  %q\n want %q", c[0], got[i], c[1])
		}
	}
}

// A long line of links never closed ("[a](" again and again: minified code pasted in Markdown, the
// review of 21/10/2026) reads in linear time: a destination is read 32 parentheses deep at most, as
// cmark reads it, so 160 000 characters read in well under a second, where each "](" read to the line's
// end had taken 13 s at 80 000 characters (and this reading, again 300 ms after each pause and on the
// form's formdata, froze the tab). The proof's browser is killed at 30 s; the whole run, the browser's
// start included, must take under 10 s.
// CONTROL (run 21/10/2026, a copy of editor-markdown.js through -overlay, fix/controls.py in the
// scratchpad's editor-formats): with link() nesting parentheses without end, the proof did not pass
// (the browser killed: 60 s for the run; with it, 0.8 s).
func TestBrowserEditorMarkdownReadsALongLineInTime(t *testing.T) {
	start := time.Now()
	out := runMarkdownDump(t, "editor markdown long line", `<pre id="long"></pre>`, `
  var text = new Array(40001).join("[a]("), read = canonical(text);
  $("long").textContent = JSON.stringify([text.length, read.length, read.slice(0, 12)]);
`)
	var got []any
	dumped(t, out, "long", &got)
	took := time.Since(start)
	if took > 10*time.Second || len(got) != 3 || got[0] != float64(160000) || got[2] != "<p>[a]([a]([" {
		t.Errorf("a line of 160 000 characters: %v in %v", got, took)
	}
	t.Logf("%v in %v", got, took)
}

// The writing, value by value: the Markdown exactly, and read back as the value. A value that ends in a
// rule (21/10/2026) ends its Markdown in a --- line, which reads back as the rule.
// CONTROL (21/10/2026, a copy of v2.0.0.js through -overlay, controls.py in the scratchpad's
// final-rule): with the editor's read() dropping a rule at the end again (its kept.pop() put back), the
// two rows of a final rule failed on their writing ("a", no ---) and their reading back.
// CONTROL (run 21/10/2026, a copy of editor-markdown.js through -overlay): with plain() escaping no
// underscore, "_c_" read back as emphasis and the first case failed (its text and its reading back);
// with the second of two lists side by side written with the first's marker, the lists case failed.
func TestBrowserEditorMarkdownWriting(t *testing.T) {
	cases := [][2]string{
		{"<p>a * b _c_ snake_case [x] \\ `code` ~ ~~ &amp;copy; &lt;b&gt; 2 &lt; 3 #1 !</p>",
			"a \\* b \\_c\\_ snake_case \\[x\\] \\\\ \\`code\\` ~ \\~\\~ \\&copy; \\<b> 2 < 3 #1 !"},
		// (a line ending in a break's backslash is no rule nor underline: "---" stays as written there)
		{"<p>1. một<br># hai<br>- ba<br>+ bốn<br>&gt; năm<br>---<br>2) sáu<br>===</p>",
			"1\\. một\\\n\\# hai\\\n\\- ba\\\n\\+ bốn\\\n\\> năm\\\n---\\\n2\\) sáu\\\n\\==="},
		{"<h2>Tựa #</h2>", "## Tựa \\#"}, {"<h6>Sáu</h6>", "###### Sáu"},
		{"<p><strong>a</strong> <em>b</em> <u>c</u> <s>d</s> <code>e</code></p>", "**a** *b* <u>c</u> ~~d~~ `e`"},
		{"<p><strong><em>a</em></strong><em>b</em></p>", "***a**b*"},
		{"<p><strong>đậm </strong>sau</p>", "<strong>đậm </strong>sau"},
		{"<p><code>a`b</code> <code>`x</code> a<code> y </code>b</p>", "``a`b`` `` `x `` a`  y  `b"},
		{"<p>a<strong> b</strong> <em>c,</em>d (<strong>e</strong>)</p>", "a<strong> b</strong> <em>c,</em>d (**e**)"},
		{"<p><code>a<br>b</code></p>", "<code>a<br>b</code>"},
		{`<p><a href="https://kitwork.io/a b(c)">x</a> <a href="/x?a=1&amp;copy;=2">y</a> <a href="/z_">z</a></p>`,
			"[x](<https://kitwork.io/a b(c)>) [y](/x?a=1\\&copy;=2) [z](/z_)"},
		{`<p class="align-center">Giữa</p>`, `<p class="align-center">Giữa</p>`},
		{`<blockquote><p>a</p><p class="align-right">b</p></blockquote>`, "> a\n>\n> <p class=\"align-right\">b</p>"},
		{"<ul><li>a</li></ul><ul><li>b</li></ul><ol><li>c</li><li>d</li></ol><ol><li>e</li></ol>", "- a\n\n* b\n\n1. c\n2. d\n\n1) e"},
		{"<ul><li>a<br>b</li></ul><ol><li>1. c</li></ol>", "- a\\\n  b\n\n1. 1\\. c"},
		{"<pre><code>a ``` b\n\nc</code></pre>", "````\na ``` b\n\nc\n````"},
		{"<p>a</p><hr><p>b</p>", "a\n\n---\n\nb"},
		{"<p>a</p><hr>", "a\n\n---"}, {"<pre><code>x</code></pre><hr>", "```\nx\n```\n\n---"},
		{"<h3>a<br>b</h3>", "<h3>a<br>b</h3>"},
	}
	var inputs []string
	for _, c := range cases {
		if got := richtext.Clean(c[0]).HTML; got != strings.ReplaceAll(strings.ReplaceAll(c[0], "<li>", "\n<li>"), "</ol>", "</ol>") && got == "" {
			t.Fatalf("%q is not a value", c[0])
		}
		inputs = append(inputs, richtext.Clean(c[0]).HTML)
	}
	payload, _ := json.Marshal(inputs)
	out := runMarkdownDump(t, "editor markdown writing", `<pre id="inputs" hidden>`+html.EscapeString(string(payload))+`</pre><pre id="written"></pre>`, `
  $("written").textContent = JSON.stringify(JSON.parse($("inputs").textContent).map(function (value) { var text = write(value); return [text, canonical(text)]; }));
`)
	var got [][2]string
	dumped(t, out, "written", &got)
	for i, c := range cases {
		if got[i][0] != c[1] {
			t.Errorf("%q\n got  %q\n want %q", inputs[i], got[i][0], c[1])
		}
		if got[i][1] != inputs[i] {
			t.Errorf("%q reads back as %q", got[i][0], got[i][1])
		}
	}
}

var _ = fmt.Sprint
