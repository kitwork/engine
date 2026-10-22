package js

import (
	"bytes"
	"compress/gzip"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/minifier"
	"github.com/kitwork/engine/utilities/richtext"
)

// The editor (components/editor/v2.0.0.js) reads and writes richtext's grammar in JavaScript: its
// FORMATS (what toggle() makes) and its ROLES (what an element read in is: a line of its kind, a
// container, a mark, a break, a paragraph's end, gone with what it holds, or unwrapped) are held here
// to utilities/richtext, element by element, so the two never drift apart.

// editorTables reads FORMATS and ROLES off the editor's source.
func editorTables(t *testing.T) (formats map[string]string, roles map[string]string) {
	t.Helper()
	source := ComponentSource("editor", "")
	block := func(name string) string {
		m := regexp.MustCompile(`(?s)var ` + name + ` = \{(.*?)\};`).FindStringSubmatch(source)
		if m == nil {
			t.Fatalf("no var %s = {…}; in the editor's source", name)
		}
		return m[1]
	}
	pair := regexp.MustCompile(`"?([\w-]+)"?:\s*"([^"]*)"`)
	formats = map[string]string{}
	for _, m := range pair.FindAllStringSubmatch(block("FORMATS"), -1) {
		formats[m[1]] = m[2]
	}
	roles = map[string]string{}
	for _, m := range pair.FindAllStringSubmatch(block("ROLES"), -1) {
		for _, element := range strings.Fields(m[2]) {
			if previous, twice := roles[element]; twice {
				t.Errorf("ROLES gives %s twice: %s and %s", element, previous, m[1])
			}
			roles[element] = m[1]
		}
	}
	if len(formats) == 0 || len(roles) == 0 {
		t.Fatalf("FORMATS %v, ROLES %v: the tables were not read", formats, roles)
	}
	return formats, roles
}

// richtextRole is what richtext.Clean makes of an element, read off its output for one probe: the
// element around "b" between "a" and "c" in a paragraph, with an href it may take.
func richtextRole(name string) string {
	out := richtext.Clean("<p>a<" + name + ` href="/x">b</` + name + ">c</p>").HTML
	switch out {
	case "<p>abc</p>":
		return "unwrap"
	case `<p>a<a href="/x">b</a>c</p>`:
		return "a"
	case "<p>a<br>b<br>c</p>":
		return "br"
	case "<p>a</p>\n<hr>\n<p>bc</p>":
		return "hr"
	case "<p>a</p>\n<p>b</p>\n<p>c</p>":
		return "p"
	case "<p>a</p>\n<p class=\"align-center\">b</p>\n<p>c</p>":
		return "center"
	case "<p>a</p>\n<pre><code>b</code></pre>\n<p>c</p>":
		return "pre"
	case "<p>a</p>\n<blockquote>\n<p>b</p>\n</blockquote>\n<p>c</p>":
		return "blockquote"
	case "<p>a</p>\n<ul>\n<li>b</li>\n</ul>\n<p>c</p>":
		return "ul"
	case "<p>a</p>\n<ol>\n<li>b</li>\n</ol>\n<p>c</p>":
		return "ol"
	}
	if m := regexp.MustCompile(`^<p>a<(strong|em|u|s|code)>b</([a-z]+)>c</p>$`).FindStringSubmatch(out); m != nil && m[1] == m[2] {
		return m[1]
	}
	if m := regexp.MustCompile(`^<p>a</p>\n<(h[2-6])>b</([a-z0-9]+)>\n<p>c</p>$`).FindStringSubmatch(out); m != nil && m[1] == m[2] {
		return m[1]
	}
	if !strings.Contains(out, "b") {
		return "drop"
	}
	return "unknown: " + out
}

// editorRole is what the editor's ROLES make of an element, in the same words: a stray li a list, an
// element ROLES does not name is unwrapped (center is a centred paragraph, pre a code block).
func editorRole(roles map[string]string, name string) string {
	switch role := roles[name]; role {
	case "":
		return "unwrap"
	case "li":
		return "ul"
	default:
		return role
	}
}

// htmlElements are the elements of HTML, the obsolete ones a paste still brings included.
var htmlElements = strings.Fields(`a abbr address area article aside audio b base basefont bdi bdo bgsound big blink blockquote body br
button canvas caption center cite code col colgroup data datalist dd del details dfn dialog dir div dl dt em embed fieldset
figcaption figure font footer form frame frameset h1 h2 h3 h4 h5 h6 head header hgroup hr html i iframe image img input ins
kbd keygen label legend li link listing main map mark marquee math menu meta meter nav nobr noembed noframes noscript object
ol optgroup option output p param picture plaintext pre progress q rp rt ruby s samp script search section select slot small
source span strike strong style sub summary sup svg table tbody td template textarea tfoot th thead time title tr track tt u ul
var video wbr xmp`)

// Every element of HTML is the same thing to the editor as to richtext.Clean (grammar v2, 20/10/2026:
// u, s, code, h4–h6, pre > code, hr and the centred <center> are the grammar's own), and toggle()'s
// formats are the grammar's own: a value made of each is kept by Clean as it is, and with p, li, a, br
// and rule()'s hr they are every element Clean writes.
// CONTROL: with ROLES giving "section" to drop and leaving out "h4", and FORMATS writing "b" for bold
// (a copy of v2.0.0.js through -overlay), this test named <h4>, <section> and bold's <b> (with the
// <strong> no format then made), and nothing else. With ROLES reading <del> as u and leaving out
// "center" and "listing", and FORMATS without "code-block" (a second copy, run 21/10/2026), it named
// <del>, <center>, <listing>, the wire's code-block and the <pre> no format then made, and nothing else.
func TestEditorTablesAreRichtextsGrammar(t *testing.T) {
	formats, roles := editorTables(t)
	names := map[string]bool{}
	for _, name := range htmlElements {
		names[name] = true
	}
	for name := range roles {
		names[name] = true
	}
	written := map[string]bool{}
	for name := range names {
		got, want := editorRole(roles, name), richtextRole(name)
		if got != want {
			t.Errorf("<%s>: the editor reads it as %s, richtext as %s", name, got, want)
		}
		for _, m := range regexp.MustCompile(`<([a-z0-9]+)`).FindAllStringSubmatch(richtext.Clean("<p>a<"+name+` href="/x">b</`+name+">c</p>").HTML, -1) {
			written[m[1]] = true
		}
	}

	wire := []string{"bold", "code", "code-block", "heading-2", "heading-3", "heading-4", "heading-5", "heading-6", "italic", "list",
		"list-ordered", "quote", "strikethrough", "underline"}
	var keys []string
	for key := range formats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if strings.Join(keys, " ") != strings.Join(wire, " ") {
		t.Errorf("FORMATS names %v, the wire's data-kitwork-format %v", keys, wire)
	}
	grammar := map[string]bool{"p": true, "li": true, "a": true, "br": true, "hr": true}
	for format, tag := range formats {
		grammar[tag] = true
		var value string
		switch tag {
		case "strong", "em", "u", "s", "code":
			value = "<p><" + tag + ">x</" + tag + "></p>"
		case "pre":
			value = "<pre><code>x</code></pre>"
		case "blockquote":
			value = "<blockquote>\n<p>x</p>\n</blockquote>"
		case "ul", "ol":
			value = "<" + tag + ">\n<li>x</li>\n</" + tag + ">"
		default:
			value = "<" + tag + ">x</" + tag + ">"
		}
		if clean := richtext.Clean(value).HTML; clean != value {
			t.Errorf("toggle(%s) makes <%s>, which richtext does not keep: %q is %q", format, tag, value, clean)
		}
	}
	for tag := range written {
		if !grammar[tag] {
			t.Errorf("richtext writes <%s>, which no format of the editor makes", tag)
		}
	}
	for tag := range grammar {
		if !written[tag] && tag != "a" && tag != "li" {
			t.Errorf("the editor makes <%s>, which richtext never writes", tag)
		}
	}
}

// The editor minifies (the minifier gives back what it cannot parse, unchanged, and the page would get
// the readable file) and stays a file a page can afford: at most 11 KiB gzip, minified. It was 8 KiB
// (8152 bytes) until the review of 20/10/2026, whose fixes took it to about 8.7 KiB: a drop taken from
// its start (the browser builds nothing), marks waiting at the caret in no element, the link popover
// closing however it is left, and every command waiting for a composition's end. Grammar v2 (21/10/2026:
// underline, strikethrough, code in a line and in a block, headings 4 to 6, alignment, rules, the
// buttons of a dropdown's popover, the paste mapping of style, a link holding one line as richtext's
// does) took it to 10954 bytes, and the paste fix after it to 10981. The fixes of the review of
// 21/10/2026 (a caret beside a rule, a selection into a code block, Backspace after one, the toolbar's
// dropdown closed by Escape and by the focus leaving it, Alt+F10 in a popover, a rule on an empty line,
// the alignments before any caret) took it to 11433, over 11 KiB, so the reading of a paste and a drop
// went to a file of its own, loaded at the first paste or drop (parts/editor-paste.js, PartAsset): the
// editor 10809 bytes (455 under 11 KiB), the part 1730 (318 under 2 KiB). The toolbars' tooltips and the
// link's popover under the words it links (21/10/2026) did not fit with it either (the popover's place
// alone was 471 bytes, the editor 11426 with both): they are a part too, loaded at the first focus in
// the editor or hover on a toolbar (parts/editor-place.js), the editor 10920 bytes (344 under 11 KiB)
// with the hooks that load and call it, the part 1581 (467 under 2 KiB). The fixes of their review
// (21/10/2026: the tooltip read from where the pointer is, every event kept while the part loads, the
// press or key before a focus, the popover by the words' first line, a width to the fraction of a pixel)
// took the editor to 10965 (299 under) and the part to 1942 (106 under). The formats (21/10/2026:
// rich, Markdown, HTML) are two parts more, the formats' (parts/editor-format.js: the views, the
// switch, the toolbar in a text surface; loaded at the first switch away from rich, 5 KiB at most) and
// Markdown's (parts/editor-markdown.js: both ways; loaded when Markdown is first chosen, 6.5 KiB at
// most); the editor keeps the hooks (format read and written, dropped, the part loaded at a switch and at
// the start, the hands handed to both parts, a select of the format pressed to open): 11107 bytes as
// built, 11122 once the two sides agreed (142 under 11 KiB), the formats' part 4237, Markdown's 5107, the
// paste part (dropped, and build, plain and leftOut handed on) 1983 (65 under 2 KiB). The fixes of their
// review (21/10/2026) took the editor to 11131 (133 under: a part that failed is asked again), the
// formats' part to 4992 (128 under 5 KiB: marks per line, a composition waited for, the reading at each
// input of a short text, Clean's style rule, quotes and lists joined, dropped at each reading, a
// dropdown off in HTML, the Markdown part asked again) and Markdown's to 5523 (GFM's tag filter, lazy
// lines after a paragraph only, references read whole, 32 parentheses deep). The rule that ends a post,
// kept (21/10/2026), took the editor to 11164 and Markdown's part to 5530; its review's fix (a paste or a
// drop that ends in a rule leaves the caret under it: under(), ruling's, handed to the paste part) to
// 11184 (80 under 11 KiB) and the paste part to 2005 (43 under 2 KiB; the same lines in the part alone
// were 2048).
// CONTROL: against a copy of v2.0.0.js with a stray ")" in tidy(), the minifier gave the source back
// unchanged and this test failed. The place part's row (run 21/10/2026, a copy through -overlay with a
// stray ")" in its place()) failed the same way, and so did the formats' and Markdown's (run 21/10/2026,
// copies through -overlay with a stray ")" after their repeat()).
func TestEditorMinifiesAndStaysSmall(t *testing.T) {
	if minifier.JS("var a  =  1;") == "var a  =  1;" {
		t.Skip("the stdminify build serves readable source")
	}
	for _, file := range []struct {
		name, source, registers string
		most                    int
	}{
		{"editor", ComponentSource("editor", ""), `component("editor",`, 11264},
		{"its paste part", partSource(t, "editor-paste"), `Symbol.for("kitwork:editor-paste")`, 2048},
		{"its place part", partSource(t, "editor-place"), `Symbol.for("kitwork:editor-place")`, 2048},
		{"its formats part", partSource(t, "editor-format"), `Symbol.for("kitwork:editor-format")`, 5120},
		{"its markdown part", partSource(t, "editor-markdown"), `Symbol.for("kitwork:editor-markdown")`, 6656},
	} {
		minified := minifier.JS(wrapComponent(file.source))
		if len(minified) >= len(file.source)/2 || !strings.Contains(minified, file.registers) {
			t.Fatalf("the minifier did not take %s (%d bytes in, %d out): it does not parse", file.name, len(file.source), len(minified))
		}
		var out bytes.Buffer
		writer, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
		_, _ = writer.Write([]byte(minified))
		_ = writer.Close()
		t.Logf("%s: %d bytes source, %d minified, %d gzip", file.name, len(file.source), len(minified), out.Len())
		if out.Len() > file.most {
			t.Errorf("%s is %d bytes gzip, over %d", file.name, out.Len(), file.most)
		}
	}
}

// partSource is the source of a part, parts/<name>.js.
func partSource(t *testing.T, name string) string {
	t.Helper()
	b, err := jsFS.ReadFile("parts/" + name + ".js")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
