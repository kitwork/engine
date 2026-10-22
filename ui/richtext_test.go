package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/utilities/richtext"
)

// <richtext value="{{ post.body }}" /> (decision of 20/10/2026, richtext.go): a post the site saved,
// cleaned on the server and written inside <div class="richtext">, the only way .ui writes HTML that
// came as data. These tests hold what it writes, each refusal at its place in its words, its text scope
// in the page's stylesheet only where it is used, and the contrast check over its words and links.

// richtextContent is the content.ui of a page that shows a post.
const richtextContent = `<content><richtext value="{{ post.body }}" /></content>`

// richtextPage compiles content.ui (reloading, so no progressbar is added) and fails on any warning.
func richtextPage(t *testing.T, files map[string]string) *Page {
	t.Helper()
	s := memorySite("", files)
	s.Reload = true
	page, warned := compileDiagnose(t, s)
	if len(warned) > 0 {
		t.Fatalf("want no warning, got:\n%s", strings.Join(warned, "\n"))
	}
	return page
}

// richtextCorpus is every source of the cleaner's corpora (utilities/richtext/testdata: filter-evasion
// payloads, mutation XSS, references without ";", controls and backslashes in addresses, the clipboard
// of Google Docs, Word, a browser, a spreadsheet and editors), with the characters a text file cannot
// hold written as the corpora write them ({NUL}, {U+2028}, {BYTE:FF}).
func richtextCorpus(t *testing.T) []string {
	t.Helper()
	names := map[string]string{"NUL": "\x00", "SOH": "\x01", "TAB": "\t", "LF": "\n", "CR": "\r", "FF": "\f", "VT": "\v",
		"ESC": "\x1b", "DEL": "\x7f", "NBSP": "\u00a0", "ZWSP": "\u200b", "BOM": "\ufeff"}
	escape := regexp.MustCompile(`\{([A-Z]+|U\+[0-9A-F]{4,6}|BYTE:[0-9A-F]{2})\}`)
	expand := func(s string) string {
		return escape.ReplaceAllStringFunc(s, func(m string) string {
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
	var sources []string
	for _, file := range []string{"xss.txt", "paste.txt"} {
		data, err := os.ReadFile(filepath.Join("..", "utilities", "richtext", "testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		inCase, inWant := false, false
		flush := func() {
			if inCase {
				sources = append(sources, expand(strings.TrimRight(strings.Join(lines, "\n"), "\n")))
			}
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			switch {
			case strings.HasPrefix(line, "== "):
				flush()
				lines, inCase, inWant = nil, true, false
			case !inCase:
			case line == "-- want" && !inWant:
				inWant = true
			case !inWant:
				lines = append(lines, line)
			}
		}
		flush()
	}
	if len(sources) < 100 {
		t.Fatalf("only %d sources read from the cleaner's corpora", len(sources))
	}
	return sources
}

// The value is written inside <div class="richtext"> exactly as the cleaner writes it for the page's
// origin (richtext.Display), for every source of the cleaner's corpora and for values that are no HTML:
// none, a number, plain text with its blank lines. A link to the page's own host carries nothing more, a
// link to another one rel="external noopener"; a page that does not know its origin takes every web link
// for another host's. Each request reads its own data.
// CONTROL: with richtextOp writing the value as it came (the cleaning left out) every value but the
// empty one failed (132 of 133), with it writing richtext.Clean(…).HTML (no origin) the link rows
// failed, and with the cache keeping a value by its text alone the row of the same value at another
// origin failed (run 20/10/2026 on overlaid copies of richtext.go).
func TestRichtextWritesTheValueCleaned(t *testing.T) {
	page := richtextPage(t, map[string]string{"/site/content.ui": richtextContent})
	const origin = "https://kitwork.io"
	render := func(value any, origin string) string {
		return page.Render(Request{Path: "/", Origin: origin, Scope: map[string]any{"post": map[string]any{"body": value}}})
	}
	body := func(inside string) string {
		return `<body class="flex-column"><main class="flex-column"><div class="richtext">` + inside + `</div></main></body>`
	}
	sources := append(richtextCorpus(t), "", "Một dòng\nhai dòng\n\nđoạn mới", "Tom &amp; Jerry <3")
	for _, source := range sources {
		if got, want := render(source, origin), body(richtext.Display(stringify(source), origin)); got != want {
			t.Errorf("value %q:\n got  %q\n want %q", source, got, want)
		}
	}
	// what the decision draws, as the page writes it
	for _, c := range []struct {
		value  any
		origin string
		want   string
	}{
		{`<h1>Thực đơn</h1><p>Cà phê <b>sữa</b> <i>đá</i>, <a href="https://kitwork.io/menu">xem</a> hay <a href="https://maps.example/q">bản đồ</a>.</p>` +
			`<script>alert(1)</script><ul><li>Bạc xỉu<li>Trà đào</ul><blockquote>Ngon.</blockquote><img src=x onerror=alert(1)>`, origin,
			"<h2>Thực đơn</h2>\n<p>Cà phê <strong>sữa</strong> <em>đá</em>, <a href=\"https://kitwork.io/menu\">xem</a> hay " +
				"<a href=\"https://maps.example/q\" rel=\"external noopener\">bản đồ</a>.</p>\n<ul>\n<li>Bạc xỉu</li>\n<li>Trà đào</li>\n</ul>\n<blockquote>\n<p>Ngon.</p>\n</blockquote>"},
		{`<a href="https://kitwork.io/menu">xem</a> <a href="/menu">menu</a> <a href="javascript:alert(1)">bấm</a>`, "",
			"<p><a href=\"https://kitwork.io/menu\" rel=\"external noopener\">xem</a> <a href=\"/menu\">menu</a> bấm</p>"},
		{"Một dòng\nhai dòng\n\nđoạn mới", origin, "<p>Một dòng<br>hai dòng</p>\n<p>đoạn mới</p>"},
		{nil, origin, ""},
		{12.5, origin, "<p>12.5</p>"},
		// the grammar of 20/10/2026: a small heading aligned, a code block, a rule, the new marks
		{`<h4 style="text-align:center">Nhỏ</h4><pre>a  b` + "\n" + `  c</pre><hr><p><u>u</u> <del>s</del> <a href="https://maps.example/q"><kbd>x</kbd></a></p>`, origin,
			"<h4 class=\"align-center\">Nhỏ</h4>\n<pre><code>a  b\n  c</code></pre>\n<hr>\n" +
				"<p><u>u</u> <s>s</s> <a href=\"https://maps.example/q\" rel=\"external noopener\"><code>x</code></a></p>"},
	} {
		if got := render(c.value, c.origin); got != body(c.want) {
			t.Errorf("value %v at %q:\n got  %q\n want %q", c.value, c.origin, got, body(c.want))
		}
	}
	if first, second := render("<p>một</p>", origin), render("<p>hai</p>", origin); first != body("<p>một</p>") || second != body("<p>hai</p>") {
		t.Errorf("each request writes its own value:\n%s\n%s", first, second)
	}
	// a value cleaned once is kept by value and origin: the same post on another site is that site's
	for _, c := range []struct{ origin, want string }{
		{origin, `<p><a href="https://kitwork.io/menu">xem</a></p>`},
		{"https://maps.example", `<p><a href="https://kitwork.io/menu" rel="external noopener">xem</a></p>`},
		{origin, `<p><a href="https://kitwork.io/menu">xem</a></p>`},
	} {
		if got := render(`<a href="https://kitwork.io/menu">xem</a>`, c.origin); got != body(c.want) {
			t.Errorf("the same value at %s:\n got  %s\n want %s", c.origin, got, body(c.want))
		}
	}
	// a value larger than the cache keeps (an eighth of its bound) is cleaned each time, the same
	large := strings.Repeat("<p>Một đoạn văn của bài.</p>", 40000)
	if first, second := render(large, origin), render(large, origin); first != body(richtext.Display(large, origin)) || second != first {
		t.Errorf("a value of %d bytes written %d and %d bytes", len(large), len(first), len(second))
	}
	// written on a richtext, presentation and control are the element's own: a class, an id, an if
	page = richtextPage(t, map[string]string{"/site/content.ui": `<content><richtext id="bai" if="post.body" value="{{ post.body }}" padding="4" max-width="640px" /></content>`})
	if got := render("<p>x</p>", origin); got != `<body class="flex-column"><main class="flex-column"><div class="richtext padding-4 max-width-640px" id="bai"><p>x</p></div></main></body>` {
		t.Errorf("a richtext's own words:\n got  %s", got)
	}
	if got := render("", origin); got != `<body class="flex-column"><main class="flex-column"></main></body>` {
		t.Errorf("if reads the page's data:\n got  %s", got)
	}
}

// Each thing a richtext does not take is an error at its place, in its words: no value (or an empty
// one), something written inside, a value read from what lives in the browser (an alias, the names of a
// component the kernel runs, a toast's notice), a value written in the markup (words, words around a
// {{ }}, a {{ }} that only spells words out), a richtext inside a line, a richtext as the host of a
// component (which holds what is written inside it). The ways other systems write HTML are refused with
// the hint that names richtext: the tags <html>, <raw>, <prose>, <markdown>, the attributes html=,
// v-html, x-html, innerHTML on any tag, raw() in an expression and {{{ }}}.
// CONTROL: the right spelling of each compiles (TestRichtextWritesTheValueCleaned), and a layout piece,
// which has no host, may be a richtext. Each check was disabled in turn and exactly the rows it guards
// failed: richtextPlace returning nil (the 2 inside, the 5 inline), the reads of the browser's state not
// looked for (alias, live, notice), a {{ }} of literals read as data (its 2 rows), refusedTags not
// looked up in element (the 4 tags), htmlAttributes not read by webWord (the 4 attributes on box, text,
// card; richtext's own html= is its value's check), raw left out of refusedCalls (the 2 raw()), the {{{
// check of parseInterpolation removed ({{{ }}}), richtext not in forbiddenHost (host) (run 20/10/2026
// on overlaid copies of richtext.go, compile.go, expr.go and host.go).
func TestRichtextRefusalsAtTheirPlace(t *testing.T) {
	const (
		needsValue = `richtext cần value: <richtext value="{{ post.body }}" />`
		inside     = `richtext không có nội dung bên trong: bài đã lưu đến từ value`
		written    = `richtext hiện bài đã lưu, value là {{ }}; chữ viết sẵn thì viết bằng thẻ .ui (<text>, <list>, <quote>)`
		inline     = `richtext là một khối: không đặt trong <text>, <link>, <button>, <label> (nó đang nằm trong `
		fromData   = `.ui không ghi HTML từ dữ liệu; bài đã lưu hiện bằng <richtext value="{{ post.body }}" />, làm sạch ở server`
	)
	browser := func(name string) string {
		return `richtext dựng ở server: value đọc dữ liệu của trang, không đọc ` + name + ` (trạng thái ở trình duyệt)`
	}
	// refused checks every row and goes on, so a check disabled shows each row it guards
	refused := func(files map[string]string, want string) {
		t.Helper()
		if _, err := memorySite("", files).Compile("/site"); err == nil || err.Error() != want {
			t.Errorf("want the error\n  %s\ngot\n  %v", want, err)
		}
	}
	for _, c := range []struct{ content, needle, says string }{
		{`<content><richtext /></content>`, "<richtext", needsValue},
		{`<content><richtext value="  " /></content>`, "<richtext", needsValue},
		{`<content><richtext value="{{ post.body }}"><text>Xin chào</text></richtext></content>`, "<richtext", inside},
		{`<content><richtext value="{{ post.body }}">Xin chào</richtext></content>`, "<richtext", inside},
		{`<content><dropdown as="$menu"><button click="toggle()">Menu</button></dropdown><richtext value="{{ $menu.open }}" /></content>`, "value=", browser("$menu")},
		{`<content><dropdown><button click="toggle()">Menu</button><richtext value="{{ open ? post.full : post.lead }}" /></dropdown></content>`, "value=", browser("open")},
		{`<content><richtext value="<p>Xin chào</p>" /></content>`, "value=", written},
		{`<content><richtext value="Bài: {{ post.body }}" /></content>`, "value=", written},
		{`<content><richtext value="{{ '<b>Xin chào</b>' }}" /></content>`, "value=", written},
		{`<content><richtext value="{{ ready ? '<b>Mở</b>' : '' }}" /></content>`, "value=", written},
		{`<content><text>Xem <richtext value="{{ post.body }}" /></text></content>`, "<richtext", inline + `<text> ở content.ui:1:10)`},
		{`<content><text heading="2"><richtext value="{{ post.body }}" /></text></content>`, "<richtext", inline + `<text> ở content.ui:1:10)`},
		{`<content><link value="/bai"><richtext value="{{ post.body }}" /></link></content>`, "<richtext", inline + `<link> ở content.ui:1:10)`},
		{`<content><button><richtext value="{{ post.body }}" /></button></content>`, "<richtext", inline + `<button> ở content.ui:1:10)`},
		{`<content><field><label><richtext value="{{ post.body }}" /></label><input /></field></content>`, "<richtext", inline + `<label> ở content.ui:1:17)`},
		{`<content><html>{{ post.body }}</html></content>`, "<html", `không có thẻ <html> (trang đã là <view>): HTML đã soạn, một bài viết hay một mô tả, hiện bằng <richtext value="{{ post.body }}" />, làm sạch ở server`},
		{`<content><raw>{{ post.body }}</raw></content>`, "<raw", `không có thẻ <raw>: .ui không ghi HTML chưa làm sạch; bài đã lưu hiện bằng <richtext value="{{ post.body }}" />`},
		{`<content><prose>{{ post.body }}</prose></content>`, "<prose", `không có thẻ <prose>: khối chữ của một bài đã lưu là <richtext value="{{ post.body }}" />, khoảng cách, danh sách, link và trích dẫn là của engine`},
		{`<content><markdown value="{{ post.body }}" /></content>`, "<markdown", `không có thẻ <markdown>: bài đã lưu hiện bằng <richtext value="{{ post.body }}" /> (richtext đọc HTML hoặc chữ thường, không đọc markdown)`},
		{`<content><richtext html="{{ post.body }}" /></content>`, "html=", `html không có: ` + fromData},
		{`<content><box html="{{ post.body }}" /></content>`, "html=", `html không có: ` + fromData},
		{`<content><text v-html="post.body" /></content>`, "v-html=", `v-html không có: ` + fromData},
		{`<content><box x-html="post.body"><text>x</text></box></content>`, "x-html=", `x-html không có: ` + fromData},
		{`<content><card innerHTML="{{ post.body }}" /></content>`, "innerHTML=", `innerHTML không có: ` + fromData},
		{`<content><text>{{ raw(post.body) }}</text></content>`, "{{ raw", `raw() không có: ` + fromData},
		{`<content><box show="raw(post.body) != ''"><text>x</text></box></content>`, "show=", `raw() không có: ` + fromData},
		{`<content><text>{{{ post.body }}}</text></content>`, "{{{", `{{{ }}} không có: ` + fromData},
	} {
		refused(map[string]string{"/site/content.ui": c.content}, where("content.ui", c.content, c.needle)+c.says)
	}
	// a toast's notice is the kernel's: a richtext of one would read what only the browser has
	overlay := `<toast as="$toast"><card for="notice of notices; key: notice.id"><richtext value="{{ notice.message }}" /><button click="dismiss(notice.id)"><icon value="x" alt="Đóng" /></button></card></toast>`
	refused(toastSite(overlay, `<content><text>x</text></content>`), where("overlay.ui", `<overlay>`+overlay, "value=")+browser("notice"))
	// a component's host holds what is written inside it, and a richtext holds nothing written
	refused(map[string]string{"/site/content.ui": `<content><site-post /></content>`, "/site/site-post.ui": `<richtext value="{{ body }}" />`,
		"/site/site-post.work": `import { component } from "kitwork"; component.define("site-post", () => { var body = ""; });`},
		"site-post.ui:1:1: gốc của site-post.ui là <richtext>, mà richtext không có nội dung con, và phần tử của nó do Kitwork quyết")
	// CONTROL: a layout's root is no host, so a layout piece may be a richtext (the post it reads is the call's)
	html := mustCompile(t, map[string]string{"/site/content.ui": `<content><site-post body="{{ '<p>x</p>' }}" /></content>`,
		"/site/site-post.ui": `<richtext value="{{ body }}" />`})
	mustContain(t, html, `<div class="richtext"><p>x</p></div>`)
}

// A richtext is refused inside a line however deep, not only as the line's own child: a box, a card, a
// section, a list's item, a grid or a layout piece between it and the <text>, <link>, <button>, <label>,
// heading or <error> around changes nothing a browser does (the <p> still ends before the richtext's
// <div>, the post's links still split the <a> into pieces). The message names the outermost line
// around, at its place. Inside frames that are no line, however deep, it compiles.
//
// CONTROL: with richtextPlace reading only the element right around it (cx.inline, as it was), all nine
// rows with a frame between the line and the richtext compiled, and this test failed.
func TestRichtextRefusedInsideALineHoweverDeep(t *testing.T) {
	const body = `<richtext value="{{ post.body }}" />`
	const piece = `<box><richtext value="{{ body }}" /></box>`
	inside := func(source, needle, tag string) string {
		return "richtext là một khối: không đặt trong <text>, <link>, <button>, <label> (nó đang nằm trong " + tag + " ở " +
			strings.TrimSuffix(where("content.ui", source, needle), ": ") + ")"
	}
	// mustFailExactly checks every row and goes on, so a check disabled shows each row it guards
	mustFailExactly := func(t *testing.T, files map[string]string, want string) {
		t.Helper()
		if _, err := memorySite("", files).Compile("/site"); err == nil || err.Error() != want {
			t.Errorf("want the error\n  %s\ngot\n  %v", want, err)
		}
	}
	for _, c := range []struct{ content, line, tag string }{
		{`<content><link value="/bai"><card><text>Tiêu đề</text>` + body + `</card></link></content>`, "<link", "<link>"},
		{`<content><text>Xem <box>` + body + `</box></text></content>`, "<text", "<text>"},
		{`<content><text heading="2"><box>` + body + `</box></text></content>`, "<text", "<text>"},
		{`<content><button><box>` + body + `</box></button></content>`, "<button", "<button>"},
		{`<content><link value="/bai"><section><list><item>` + body + `</item></list></section></link></content>`, "<link", "<link>"},
		{`<content><field><label><box>` + body + `</box></label><input name="ten" /></field></content>`, "<label", "<label>"},
		{`<content><field><label>Tên</label><input name="ten" /><error><grid columns="2">` + body + `</grid></error></field></content>`, "<error", "<error>"},
		// two lines around: the outermost is named
		{`<content><link value="/bai"><text>Đọc <box>` + body + `</box></text></link></content>`, "<link", "<link>"},
	} {
		mustFailExactly(t, map[string]string{"/site/content.ui": c.content},
			where("content.ui", c.content, "<richtext")+inside(c.content, c.line, c.tag))
	}
	// a layout piece whose root is a box holding the richtext, placed inside a line
	content := `<content><text>Xem <site-post body="{{ post.body }}" /></text></content>`
	mustFailExactly(t, map[string]string{"/site/content.ui": content, "/site/site-post.ui": piece},
		where("site-post.ui", piece, "<richtext")+inside(content, "<text", "<text>"))
	// control: inside frames that are no line, however deep, and beside a line, it compiles
	for _, content := range []string{
		`<content><section><card><box>` + body + `</box></card></section></content>`,
		`<content><list><item><card>` + body + `</card></item></list></content>`,
		`<content><link value="/bai"><text>Đọc</text></link><card>` + body + `</card></content>`,
		`<content><box><site-post body="{{ post.body }}" /></box></content>`,
	} {
		html := mustCompile(t, map[string]string{"/site/content.ui": content, "/site/site-post.ui": piece})
		mustContain(t, html, `<div class="richtext"></div>`)
	}
}

// The text scope is in the page's stylesheet only on a page that holds a richtext — in view.ui, a region
// or the content, however many — once, inside the elements layer, with the link's engine colour and the
// weights of its headings and strong text, and the code face its code and code blocks take; a page
// without one has none of it, and a page shown with its stylesheets inline carries it there.
// CONTROL: with richtextScope recording nothing (its Table.Richtext call left out) every row of a page
// with a richtext failed and the page without one still passed (run 20/10/2026 on an overlaid copy of
// richtext.go). With richtextScope not marking the code face used, the row of the code face failed.
func TestRichtextScopeOnlyOnItsPage(t *testing.T) {
	scope := "@layer elements{\n.richtext{min-inline-size:0}\n"
	link := ".richtext :where(a){color:var(--kitwork-link);text-decoration:underline;text-underline-offset:0.15em}\n}\n"

	page := richtextPage(t, map[string]string{"/site/content.ui": richtextContent})
	css := string(page.Stylesheet().CSS)
	if !strings.HasPrefix(css, "@layer reboot,elements,page,show;\n:root{--kitwork-link:") || !strings.Contains(css, scope) || !strings.HasSuffix(css, link) {
		t.Errorf("a page with a richtext and nothing else:\n%s", css)
	}
	if got := page.Weights(); itoaList(got) != "[400 600 700]" {
		t.Errorf("weights %v, want running text's 400, a heading's 600 and strong text's 700", got)
	}
	if !page.UsesCode() {
		t.Errorf("a page with a richtext does not ship the code face its code and code blocks take")
	}
	document := page.Document(Request{Path: "/"})
	if !strings.Contains(document, ".richtext{min-inline-size:0}") || !strings.Contains(document, "--kitwork-link:") {
		t.Errorf("a page shown on its own carries the scope inline:\n%.2000s", document)
	}

	two := richtextPage(t, map[string]string{
		"/site/view.ui":    `<view><header /><content /></view>`,
		"/site/header.ui":  `<header><richtext value="{{ site.notice }}" /></header>`,
		"/site/content.ui": `<content><richtext value="{{ post.lead }}" /><richtext value="{{ post.body }}" padding="4" /></content>`,
	})
	css = string(two.Stylesheet().CSS)
	if strings.Count(css, ".richtext{") != 1 || strings.Count(css, "@layer elements{") != 1 || !strings.Contains(css, link+"@layer page{\n.padding-4{padding:1rem}\n}\n") {
		t.Errorf("three richtexts in view.ui's region and the content: one scope, before the page layer:\n%s", css)
	}

	without := richtextPage(t, map[string]string{"/site/content.ui": `<content><text padding="4">{{ post.body }}</text></content>`})
	css = string(without.Stylesheet().CSS)
	if strings.Contains(css, "richtext") || strings.Contains(css, "@layer elements") || strings.Contains(css, "kitwork-link") {
		t.Errorf("a page without a richtext carries its scope:\n%s", css)
	}
	if got := without.Weights(); itoaList(got) != "[400]" {
		t.Errorf("a page of running text ships %v, want 400 alone", got)
	}
	if without.UsesCode() {
		t.Errorf("a page of running text ships the code face")
	}
}

// itoaList writes a list of numbers as a test reads them: [400 600 700].
func itoaList(list []int) string {
	parts := make([]string, len(list))
	for i, n := range list {
		parts[i] = itoa(n)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// A richtext's words are measured where it stands like any text, whatever its value will hold, its
// links in the engine's link colour, told at the richtext with what changes that colour (router.colors,
// or another ground) since no attribute does, and, once those read, code on its fills: code in a line,
// the words around it on ink/8 over the ground around, and a code block, the words around it on the
// panel, in each mode, told at the richtext with what changes them. A pair short at the words is told
// once (the band, brand and faint rows: their code is not measured besides). A pair short of contrast is a warning by
// default and the compile's error under router.contrast "strict". A richtext on the page's canvas, or a
// card, warns of nothing, in light and in dark.
// CONTROL: with the link's measure left out of richtextScope the link rows failed, and with the words'
// measure left out the colour row failed (run 20/10/2026 on overlaid copies of richtext.go).
// CONTROL: with the measures of code left out of richtextScope, the three rows of code failed (nothing
// was told); with them taken whatever the words and links read (the check of what was told before
// removed), the three rows short at the words or the links (band, brand, faint) told their code
// besides and failed.
func TestRichtextIsMeasured(t *testing.T) {
	richtextPage(t, map[string]string{"/site/content.ui": `<content><card padding="4"><richtext value="{{ post.body }}" /></card>` + richtextContent[len("<content>"):]})
	kit := darkPalette(t)
	for _, content := range []string{richtextContent, `<content><card><richtext value="{{ post.body }}" /></card></content>`} {
		if got := contrastCompile(t, kit, map[string]string{"/site/content.ui": content}); got != "" {
			t.Errorf("%s with Kit Design's dark look warns:\n%s", content, got)
		}
	}
	shade := func(name, dark string) *stylesheet.Palette { // Kit Design with one token's dark value changed
		light, _ := kit.Paint(name, stylesheet.LightMode)
		return darkPalette(t, map[string]stylesheet.Color{name: {Light: light.Hex(), Dark: dark}})
	}
	const codeAdvice = `Mã trong dòng của <richtext> nằm trên nền ink/8 engine tô dưới nó: đổi màu chữ (color trên <richtext>) hoặc đặt <richtext> trên nền khác (background).`
	const blockAdvice = `Khối mã trong <richtext> nằm trên nền panel engine tô: đổi màu chữ (color trên <richtext>) hoặc khai lại panel trong router.colors bằng một cặp { light: "#…", dark: "#…" } đủ tương phản.`
	const need = "; chữ thường cần 4.5:1 (chữ lớn 3:1). "

	band := `<content><box background="ink" color="paper"><richtext value="{{ post.body }}" /></box></content>`
	linkShort := where("content.ui", band, "<richtext") + `chữ link (brand+35) trong <richtext> trên nền ink chỉ 1.71:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). ` +
		`Chữ link engine tô theo router.colors: khai lại brand trong router.colors bằng một cặp { light: "#…", dark: "#…" } đủ tương phản trên nền ink, hoặc đặt <richtext> trên nền khác (background).`
	brand := `<content><box background="brand" color="brand.on"><richtext value="{{ post.body }}" /></box></content>`
	linkOnBrand := where("content.ui", brand, "<richtext") + `chữ link (brand+35) trong <richtext> trên nền brand chỉ 1.94:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). ` +
		`Chữ link engine tô theo router.colors từ brand, mà nền cũng là brand: đặt <richtext> trên nền khác (background).`
	faint := `<content><box background="paper"><richtext value="{{ post.body }}" color="ink-60" /></box></content>`
	textShort := where("content.ui", faint, "color=") + `color="ink-60" trên nền paper chỉ 2.73:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). Dùng color="ink-40" (4.87:1 sáng).`
	// ink-40 reads 4.87:1 on paper: code on ink/8 over paper and a code block on the panel do not
	grey := `<content><box background="paper"><richtext value="{{ post.body }}" color="ink-40" /></box></content>`
	greyCode := where("content.ui", grey, "<richtext") + `color="ink-40" trên nền ink/8 phủ trên paper chỉ 4.18:1 ở chế độ sáng` + need + codeAdvice + "\n" +
		where("content.ui", grey, "<richtext") + `color="ink-40" trên nền panel chỉ 4.37:1 ở chế độ sáng` + need + blockAdvice
	// ink #828282 in dark reads on the canvas; code on ink/8 over it and a code block on the panel do not
	darkCode := where("content.ui", richtextContent, "<richtext") + `chữ ink mặc định của trang trên nền ink/8 phủ trên canvas chỉ 4.27:1 ở chế độ tối` + need + codeAdvice + "\n" +
		where("content.ui", richtextContent, "<richtext") + `chữ ink mặc định của trang trên nền panel chỉ 4.48:1 ở chế độ tối` + need + blockAdvice
	// a panel of #5a6a7a in dark: only the code block falls short
	darkPanel := where("content.ui", richtextContent, "<richtext") + `chữ ink mặc định của trang trên nền panel chỉ 3.50:1 ở chế độ tối` + need + blockAdvice
	for _, c := range []struct {
		content string
		palette *stylesheet.Palette
		want    string
	}{
		{band, nil, linkShort}, {brand, nil, linkOnBrand}, {faint, nil, textShort}, {grey, nil, greyCode},
		{richtextContent, shade("ink", "#828282"), darkCode}, {richtextContent, shade("panel", "#5a6a7a"), darkPanel},
	} {
		s := memorySite("", map[string]string{"/site/content.ui": c.content})
		s.Reload = true
		s.Palette = c.palette
		_, warned := compileDiagnose(t, s)
		if strings.Join(warned, "\n") != c.want {
			t.Errorf("%s warns:\n got  %q\n want %q", c.content, strings.Join(warned, "\n"), c.want)
		}
		s.Contrast = ContrastStrict
		if _, err := s.Compile("/site"); err == nil || err.Error() != c.want {
			t.Errorf("%s under strict:\n got  %v\n want %q", c.content, err, c.want)
		}
	}
}
