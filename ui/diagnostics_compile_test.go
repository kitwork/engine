package ui

import (
	"strings"
	"testing"
)

// The compiler's own rows of docs/KITWORK_UI_DIAGNOSTICS.md, decided 12/10/2026 ("đồng ý hết"): what
// works and shows as written is a warning — the page compiles, renders what was written, and the
// warning stands at the place, said once — while a missing required thing, a word the compiler does
// not understand and output it cannot build stay errors. Each test names its rows (file:line of the
// table) and holds a CONTROL: the error that a warning must not swallow, or the clean page that warns
// of nothing.

// compileDiagnose compiles a site that must compile and returns the page with its warnings, one line
// each (file:line:col: message).
func compileDiagnose(t *testing.T, s *Site) (*Page, []string) {
	t.Helper()
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("a warning lets the page compile; got the error:\n%v", err)
	}
	lines := make([]string, 0, len(page.Warnings()))
	for _, w := range page.Warnings() {
		lines = append(lines, w.String())
	}
	return page, lines
}

// compileWarns asserts a site (reloading, so no progressbar is added) compiles with a warning that
// starts at `at` and says `says`, said once, and returns the page rendered at "/" with no data.
func compileWarns(t *testing.T, files map[string]string, at, says string) string {
	t.Helper()
	s := memorySite("", files)
	s.Reload = true
	return compileWarnsOn(t, s, at, says)
}

func compileWarnsOn(t *testing.T, s *Site, at, says string) string {
	t.Helper()
	page, warned := compileDiagnose(t, s)
	found := 0
	for _, w := range warned {
		if strings.HasPrefix(w, at) && strings.Contains(w, says) {
			found++
		}
	}
	if found != 1 {
		t.Errorf("want one warning at %q saying %q, got:\n%s", at, says, strings.Join(warned, "\n"))
	}
	return page.Render(Request{Path: "/"})
}

// compileQuiet asserts a site compiles with no warning at all and returns it rendered.
func compileQuiet(t *testing.T, files map[string]string) string {
	t.Helper()
	s := memorySite("", files)
	s.Reload = true
	page, warned := compileDiagnose(t, s)
	if len(warned) > 0 {
		t.Errorf("want no warning, got:\n%s", strings.Join(warned, "\n"))
	}
	return page.Render(Request{Path: "/"})
}

// Accessibility (rows ui/compile.go 364, 366, 374, 1363, 2127, 2144; question 4): the page works but
// some readers cannot use it, so it renders as written and warns. The empty badge is told what it is
// ("Lời báo không khớp mã": it was told it held only an icon). CONTROL: a name, an alt, one reader in
// page.ui warn of nothing.
func TestCompileAccessibilityIsAWarning(t *testing.T) {
	cases := []struct{ name, content, needle, says, renders string }{
		{"a button with only an icon", `<content><button><icon value="x"></icon></button></content>`, "<button>",
			"<button> chỉ có icon nên chưa có tên để trình đọc màn hình đọc: viết alt trên icon", `<i class="icon-x" aria-hidden="true"></i></button>`},
		{"an empty alt is no name", `<content><button><icon value="x" alt=""></icon></button></content>`, "<button>",
			"viết alt trên icon", `<i class="icon-x" aria-hidden="true"></i></button>`},
		{"a link with only an image", `<content><link value="/"><image value="/logo.svg" alt=""></image></link></content>`, "<link",
			"<link> chỉ có ảnh nên chưa có tên để trình đọc màn hình đọc: viết alt trên ảnh", `<img src="/logo.svg" alt=""`},
		{"an empty button", `<content><button click="go()"></button></content>`, "<button",
			"<button> cần tên đọc được: chữ bên trong, hoặc icon có alt", `type="button"></button>`},
		{"a badge with only an icon", `<content><badge color="success"><icon value="point-fill"></icon></badge></content>`, "<badge",
			"<badge> chỉ có icon cần alt nói nó là gì", `<i class="icon-point-fill" aria-hidden="true"></i></span>`},
		{"an empty badge", `<content><badge></badge></content>`, "<badge",
			"<badge> cần tên đọc được: chữ bên trong, hoặc alt nói nó là gì", `<span class="badge flex-row"></span>`},
		{"an image without alt", `<content><image value="/a.jpg" /></content>`, "<image",
			`image cần alt (alt="" nếu chỉ để trang trí)`, `<img src="/a.jpg" loading="lazy" decoding="async">`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			html := compileWarns(t, content(c.content), where("content.ui", c.content, c.needle), c.says)
			mustContain(t, html, c.renders)
		})
	}
	// the empty badge is no badge "with only an icon"
	_, warned := compileDiagnose(t, memorySite("", content(`<content><badge></badge></content>`)))
	for _, w := range warned {
		if strings.Contains(w, "chỉ có icon") {
			t.Errorf("an empty badge is told it holds an icon: %s", w)
		}
	}

	// two readers both render, and a reader in the content renders where it is written
	twice := map[string]string{"/site/page.ui": `<page><content /><reader /><reader as="$say" /></page>`, "/site/content.ui": `<content><text>x</text></content>`}
	html := compileWarns(t, twice, where("page.ui", twice["/site/page.ui"], `<reader as`), "page chỉ có một reader")
	if got := strings.Count(html, `data-kitwork-component="reader`); got != 2 {
		t.Errorf("two readers written, %d rendered:\n%s", got, html)
	}
	inside := `<content><text>x</text><reader as="$say" /></content>`
	html = compileWarns(t, content(inside), where("content.ui", inside, "<reader"), "reader đặt trong page.ui, là con của <page>")
	mustContain(t, html, `<p>x</p><div class="reader" data-kitwork-component="reader: $say"`)

	// CONTROL
	compileQuiet(t, content(`<content><button><icon value="x" alt="Đóng"></icon></button><link value="/">Trang chủ</link>`+
		`<badge alt="Đang online"><icon value="point-fill"></icon></badge><image value="/a.jpg" alt="" /></content>`))
	compileQuiet(t, map[string]string{"/site/page.ui": `<page><content /><reader as="$say" /></page>`, "/site/content.ui": `<content><text>x</text></content>`})
}

// A word of .ui on a tag that does not read it does nothing there (question 1: rows ui/compile.go 566,
// 1525, 1769, 1820, 1914, 1963, 2050, 2153, 2218, 2286, 2338): the page renders without it and a
// warning says so. Its value is read first, so a value that does not read is an error, never a
// warning; a word that is no word of .ui is a typo, an error. CONTROL: each typo below.
func TestCompileWordOnTheWrongTagIsAWarning(t *testing.T) {
	cases := []struct{ name, content, needle, says, renders, never string }{
		{"value on a box", `<content><box value="x"><text>a</text></box></content>`, `value=`,
			"value không có tác dụng trên <box>; trang hiện như không viết nó", `<div class="flex-column"><p>a</p></div>`, `value=`},
		{"value on a text", `<content><text value="x">a</text></content>`, `value=`,
			"value không có tác dụng trên <text>", `<p>a</p>`, `value=`},
		{"alt on a code", `<content><code alt="x">go run .</code></content>`, `alt=`,
			"alt không có tác dụng trên <code>", `<pre><code>go run .</code></pre>`, `aria-label`},
		{"language on a text", `<content><text language="go">a</text></content>`, `language=`,
			"language không có tác dụng trên <text>", `<p>a</p>`, `language`},
		{"alt on a quote", `<content><quote alt="x"><text>a</text></quote></content>`, `alt=`,
			"alt không có tác dụng trên <quote>", `<blockquote class="flex-column"><p>a</p></blockquote>`, `aria-label`},
		{"name on an accordion", `<content><accordion name="g"><box click="toggle()"><text>T</text></box><text>b</text></accordion></content>`, `name=`,
			"name không có tác dụng trên <accordion>", `<details><summary`, `name="g"`},
		{"alt on an accordion's header", `<content><accordion><box click="toggle()" alt="x"><text>T</text></box><text>b</text></accordion></content>`, `alt=`,
			"alt không có tác dụng trên phần đầu của accordion", `<summary class="flex-column"><span>T</span></summary>`, `aria-label`},
		{"value on a breadcrumb", `<content><breadcrumb value="x"><item><link value="/">Home</link></item></breadcrumb></content>`, `value=`,
			"value không có tác dụng trên <breadcrumb>", `<nav aria-label="Vị trí trang">`, `value=`},
		{"gap and click on a fragment", `<content><fragment if="true" gap="4"><text>x</text></fragment></content>`, `gap=`,
			"gap không có tác dụng trên <fragment>; trang hiện như không viết nó (fragment không dựng phần tử nào", `<p>x</p>`, `gap-4`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			html := compileWarns(t, content(c.content), where("content.ui", c.content, c.needle), c.says)
			mustContain(t, html, c.renders)
			mustNotContain(t, html, c.never)
		})
	}
	// a reader and a progressbar are written in page.ui
	readerGap := map[string]string{"/site/page.ui": `<page><content /><reader gap="2" /></page>`, "/site/content.ui": `<content><text>x</text></content>`}
	html := compileWarns(t, readerGap, where("page.ui", readerGap["/site/page.ui"], "gap="), "gap không có tác dụng trên reader")
	mustContain(t, html, `<div class="reader" data-kitwork-component="reader"`)
	bar := memorySite("", map[string]string{"/site/page.ui": `<page><box component="progressbar" value="50" /><content /></page>`, "/site/content.ui": `<content><text>x</text></content>`})
	html = compileWarnsOn(t, bar, "page.ui:1:36", "value không có tác dụng trên progressbar")
	mustContain(t, html, `<div class="progressbar" data-kitwork-component="progressbar"`)
	mustNotContain(t, html, `value="50"`)

	// CONTROL: a typo in the name, or in the value of a known word, is an error
	for _, c := range []struct {
		files    map[string]string
		at, says string
	}{
		{content(`<content><box gpa="4"><text>a</text></box></content>`), "content.ui:1:15", "thuộc tính gpa không có trên <box>"},
		{content(`<content><text language="js">a</text></content>`), "content.ui:1:16", `ngôn ngữ "js" chưa có; viết javascript`},
		{content(`<content><code language="js">a</code></content>`), "content.ui:1:16", `ngôn ngữ "js" chưa có; viết javascript`},
		{content(`<content><code lang="go">a</code></content>`), "content.ui:1:16", "thuộc tính lang không có trên <code>"},
		{content(`<content><breadcrumb label="x"><item><link value="/">Home</link></item></breadcrumb></content>`), "content.ui:1:22", "thuộc tính label không có trên <breadcrumb>"},
		{content(`<content><fragment if="true" gap="bigg"><text>x</text></fragment></content>`), "content.ui:1:30", `gap theo thang khoảng cách, không có "bigg"`},
		{content(`<content><fragment if="true" gpa="4"><text>x</text></fragment></content>`), "content.ui:1:30", "thuộc tính gpa không có trên <fragment>"},
		{content(`<content><fragment if="true" click="x(("><text>x</text></fragment></content>`), "content.ui:1:30", "biểu thức không hợp lệ: x(("},
		{content(`<content>` + toastStack(` gap="bigg"`) + `</content>`), "content.ui:1:33", `không có "bigg"`},
		{content(`<content><box value="{{ x( }}"><text>a</text></box></content>`), "content.ui:1:15", "biểu thức không hợp lệ"},
		{map[string]string{"/site/page.ui": `<page><content /><reader gap="bigg" /></page>`, "/site/content.ui": `<content><text>x</text></content>`}, "page.ui:1:26", `không có "bigg"`},
		{map[string]string{"/site/page.ui": `<page><content /><reader lang="vi" /></page>`, "/site/content.ui": `<content><text>x</text></content>`}, "page.ui:1:26", "reader chỉ nhận as, không nhận lang"},
	} {
		mustFail(t, c.files, c.at, c.says)
	}
	// CONTROL: what would hide, repeat or land a link is no word to drop where nothing carries it
	mustFail(t, content(`<content><fragment if="true" show="false"><text>x</text></fragment></content>`), "content.ui:1:30", "fragment chỉ nhận for và if; show cần một phần tử thật")
	mustFail(t, content(`<content>`+toastStack(` if="ready"`)+`</content>`), "content.ui:1:33", "toast luôn có mặt và có một trên mỗi trang: bỏ if")
}

// A word of the web that .ui says in a word of its own stays an error, naming that word (question 2,
// rows ui/compile.go 1350, 1525 chữ lạ and chữ web): HTML's href, src, class, style, title, on…, and
// the CSS names the stylesheet knows (table.go misnamed, which used to be answered first by the
// general sentence). CONTROL: the .ui words compile.
func TestCompileWebWordsNameTheWordOfUI(t *testing.T) {
	for _, c := range []struct{ source, at, says string }{
		{`<content><box href="/">x</box></content>`, "content.ui:1:15", `href là chữ của web; đích của link là value: <link value="/">`},
		{`<content><link href="/">x</link></content>`, "content.ui:1:16", `href là chữ của web; đích của link là value`},
		{`<content><image src="/a.png" alt="" /></content>`, "content.ui:1:17", `src là chữ của web; nguồn ảnh là value: <image value="/a.png"`},
		{`<content><box class="x">y</box></content>`, "content.ui:1:15", "class là chữ của web; .ui không có class"},
		{`<content><text style="color:red">y</text></content>`, "content.ui:1:16", "style là chữ của web"},
		{`<content><button onclick="x()">y</button></content>`, "content.ui:1:18", `onclick là chữ của web; sự kiện viết bằng tên của nó, không có on: click="x()"`},
		{`<content><box onmouseenter="x()">y</box></content>`, "content.ui:1:15", `pointer-enter="x()"`},
		{`<content><link value="/" title="x">y</link></content>`, "content.ui:1:26", "title là chữ của web, .ui không có"},
		{`<content><box pl="4">x</box></content>`, "content.ui:1:15", `pl="4": pl là chữ viết tắt của Tailwind; .ui viết đủ chữ: padding-left`},
		{`<content><box margin="4">x</box></content>`, "content.ui:1:15", `margin="4": không có margin: khoảng cách giữa các phần là gap`},
		{`<content><text font-size="small">x</text></content>`, "content.ui:1:16", "cỡ chữ là size"},
	} {
		mustFail(t, content(c.source), c.at, c.says)
	}
	// CONTROL
	compileQuiet(t, content(`<content><link value="/">x</link><image value="/a.png" alt="" /><button click="x()">y</button>`+
		`<box padding-start="4" padding-right="2" gap="4"><text size="small">x</text></box></content>`))
}

// pin, material, pin with place and a tint with an alpha under a material (rows ui/compile.go 769,
// 781, 849, 951; question 1): the value is read, the page draws what is written, and a warning says
// where the house puts it. CONTROL: a word pin or material does not take is still an error.
func TestCompilePlacementIsDrawnAsWritten(t *testing.T) {
	pinned := `<content><text pin="top">a</text></content>`
	html := compileWarns(t, content(pinned), "content.ui:1:16", "<text> không ghim được")
	mustContain(t, html, `<p class="pin-top">a</p>`)
	glass := `<content><text material="glass">a</text></content>`
	html = compileWarns(t, content(glass), "content.ui:1:16", "material là chất liệu của một khung (box, card, section, header, footer, popover…); <text> không mang material được")
	mustContain(t, html, `<p class="material-glass">a</p>`)
	for _, c := range []struct{ source, needle, says string }{
		{`<content><image value="/a.png" alt="" material="glass" /></content>`, "material=", "<image> không mang material được"},
		{`<content><icon value="coffee" alt="" material="glass" /></content>`, "material=", "<icon> không mang material được"},
		{`<content><button medium:material="glass/60">a</button></content>`, "medium:material=", "<button> không mang material được"},
		{`<content><link value="/" pin="top">a</link></content>`, "pin=", "<link> không ghim được"},
		// the cases TestMaterialErrors held before the decision (review 12/10/2026)
		{`<content><link value="/" material="glass">a</link></content>`, "material=", "<link> không mang material được"},
		{`<content><badge material="glass">a</badge></content>`, "material=", "<badge> không mang material được"},
		{`<content><text heading="1" scrolled:material="glass">a</text></content>`, "scrolled:material=", "<text> không mang material được"},
	} {
		compileWarns(t, content(c.source), where("content.ui", c.source, c.needle), c.says)
	}
	page := map[string]string{"/site/page.ui": `<page material="glass" pin="top"><content /></page>`, "/site/content.ui": `<content><text>a</text></content>`}
	html = compileWarns(t, page, "page.ui:1:7", "<page> là nền của cả trang, phía sau không có gì để nhìn qua")
	compileWarns(t, page, "page.ui:1:24", "<page> không ghim được")
	mustContain(t, html, `material-glass`, `pin-top`)

	both := `<content><box><box pin="top" place="top-end"><text>a</text></box></box></content>`
	html = compileWarns(t, content(both), "content.ui:1:20", "pin và place không đi cùng nhau")
	mustContain(t, html, `pin-top`, `place-top-end`)

	// a background with /N next to a material is the fallback's opacity, a design choice (glass first,
	// background second, 13/10/2026): no warning any more, the page drawn as written
	tint := `<content><box background="paper/80" material="glass"><text>a</text></box></content>`
	html = compileQuiet(t, content(tint))
	mustContain(t, html, `background-paper/80`, `material-glass`)
	tinted := `<content><box background="paper/80" scrolled:material="glass/60"><text>a</text></box></content>`
	compileQuiet(t, content(tinted))

	// CONTROL
	mustFail(t, content(`<content><text pin="topp">a</text></content>`), "content.ui:1:16", `pin nhận top, bottom hoặc none, không có "topp"`)
	mustFail(t, content(`<content><text material="glas">a</text></content>`), "content.ui:1:16", `material nhận none, glass hoặc glass/N (N từ 10 đến 95; glass là glass/75), không có "glas"`)
	compileQuiet(t, content(`<content><box pin="top" background="paper"><text>a</text></box><box background="paper" material="glass"><text>b</text></box></content>`))
}

// House style the compiler reads anyway (question 8 and rows ui/compile.go 325, 434, 506, 570, 584,
// 681, 711, 1506, 1702, 1981, ui/expr.go:25, ui/parse.go:238): the page renders as it means and a
// warning says how the house writes it. CONTROL: the house's own form warns of nothing, and an id from
// data stays an error.
func TestCompileHouseStyleIsAWarning(t *testing.T) {
	cases := []struct{ name, content, needle, says, renders string }{
		{"an id with capitals", `<content><section id="QuickStart"><text>a</text></section></content>`, `id=`,
			`id "QuickStart" có chữ hoa: id viết bằng chữ thường, số và dấu gạch nối (id="quickstart")`, `<section class="flex-column" id="QuickStart">`},
		{"an id on a code", `<content><code id="sample">go run .</code></content>`, `id=`,
			"id không đặt trên <code>; đặt trên phần tử bao quanh nó", `<pre id="sample"><code>go run .</code></pre>`},
		{"an id on a breadcrumb", `<content><breadcrumb id="trail"><item><link value="/">Home</link></item></breadcrumb></content>`, `id=`,
			"id không đặt trên <breadcrumb>", `<nav id="trail" aria-label="Vị trí trang">`},
		{"an id on an accordion's header", `<content><accordion><box click="toggle()" id="q"><text>T</text></box><text>b</text></accordion></content>`, `id=`,
			"id không đặt trên phần đầu của accordion", `<summary class="flex-column" id="q">`},
		{"a for in braces", `<content><box for="{{ x of xs }}"><text>{{ x }}</text></box></content>`, `for=`,
			`for đã là biểu thức, không bọc {{ }}: viết for="x of xs"`, ``},
		{"an if in braces", `<content><text if="{{ true }}">x</text></content>`, `if=`,
			`if đã là biểu thức, không bọc {{ }}: viết if="true"`, `<p>x</p>`},
		{"a show in braces", `<content><box show="{{ false }}"><text>x</text></box></content>`, `show=`,
			`show đã là biểu thức, không bọc {{ }}: viết show="false"`, `hidden`},
		{"an event in braces", `<content><button click:once="{{ x() }}">e</button></content>`, `click:once=`,
			`click:once đã là biểu thức, không bọc {{ }}: viết click:once="x()"`, `>e</button>`},
		{"a bare true-or-false", `<content><input required></content>`, `required`,
			`thuộc tính đúng/sai luôn có giá trị: viết required="true"`, ` required>`},
		{"prefixes out of order", `<content><box hover+large:gap="2">x</box></content>`, `hover+large:gap=`,
			"thứ tự tiền tố là đích → độ rộng → trạng thái (hover+large): viết large+hover:gap", `large+hover:gap-2`},
		{"a tag in capitals", "<content>\n  <Box><text>x</text></Box>\n</content>", `<Box>`,
			"tên thẻ <Box> viết thường: <box>", `<div class="flex-column"><p>x</p></div>`},
		{"a header without toggle()", `<content><accordion><box><text>T</text></box><text>b</text></accordion></content>`, `<box>`,
			`phần đầu của accordion (con đầu tiên) cần click="toggle()"`, `<details><summary class="flex-column"><span>T</span></summary><p>b</p></details>`},
		{"a fragment without for or if", `<content><fragment><text>x</text></fragment></content>`, `<fragment>`,
			"fragment không có for hay if thì thừa; bỏ thẻ này", `<main class="flex-column"><p>x</p></main>`},
		{"show on a popover", `<content><box component="dropdown"><button click="toggle()">m</button><popover show="true"><text>x</text></popover></box></content>`, `show=`,
			"popover hiện khi component chứa nó mở; không viết show", ` hidden data-kitwork-bind="show: open"`},
		{"wrap false on a paragraph", `<content><text wrap="false">a whole paragraph</text></content>`, `wrap=`,
			"wrap=\"false\" giữ một cụm chữ trong câu liền nhau: đặt trên <text> nằm trong một <text> khác (đoạn văn hay tiêu đề đã đứng riêng dòng nên ở đây không có gì để giữ liền)", `<p class="wrap-false">a whole paragraph</p>`},
		{"wrap false on a heading", `<content><text heading="1" wrap="false">Title</text></content>`, `wrap=`,
			"wrap=\"false\" giữ một cụm chữ", `<h1 class="wrap-false">Title</h1>`},
		{"a bare reload", `<content><link value="/x" reload>X</link></content>`, `reload`,
			`thuộc tính đúng/sai luôn có giá trị: viết reload="true"`, `<a data-kit-reload="true" href="/x">X</a>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			html := compileWarns(t, content(c.content), where("content.ui", c.content, c.needle), c.says)
			mustContain(t, html, c.renders)
		})
	}
	// the for in braces repeats its rows
	s := memorySite("", content(`<content><box for="{{ x of xs }}"><text>{{ x }}</text></box></content>`))
	s.Reload = true
	page, _ := compileDiagnose(t, s)
	mustContain(t, page.Render(Request{Path: "/", Scope: map[string]any{"xs": []any{"a", "b"}}}), `<p>a</p>`, `<p>b</p>`)

	// an empty region renders an empty element
	empty := map[string]string{"/site/page.ui": `<page><header /><content /></page>`, "/site/header.ui": `<header></header>`, "/site/content.ui": `<content><text>x</text></content>`}
	html := compileWarns(t, empty, "header.ui:1:1", "<header> trong header.ui không có nội dung")
	mustContain(t, html, `<header class="flex-column"></header>`)

	// CONTROL
	compileQuiet(t, content(`<content><section id="quickstart"><text>a</text></section><input required="true">`+
		`<box large+hover:gap="2" for="x of xs"><text if="true">x</text></box><accordion><box click="toggle()"><text>T</text></box><text>b</text></accordion></content>`))
	mustFail(t, content(`<content><box id="{{ x }}">x</box></content>`), "content.ui:1:15", "id là chữ cố định")
	mustFail(t, content(`<content><section id="Quick Start"><text>a</text></section></content>`), "content.ui:1:19", "chỉ gồm chữ thường, số và dấu gạch nối")
	// braces that are no one wrapped expression: the suggestion is what is written without them when that
	// reads as one expression, never a value cut in half (review 12/10/2026: if="a }} && b")
	for source, want := range map[string]string{
		`<content><text if="{{ a }} && b">x</text></content>`:                      `content.ui:1:16: if đã là biểu thức, không bọc {{ }}: viết if="a && b"`,
		`<content><box show="{{ a }} {{ b }}">x</box></content>`:                   `content.ui:1:15: show đã là biểu thức, không bọc {{ }}`,
		`<content><button click="{{ go() }} && {{ stop() }}">x</button></content>`: `content.ui:1:18: click đã là biểu thức, không bọc {{ }}: viết click="go() && stop()"`,
	} {
		if got := compileError(t, content(source)); got != want {
			t.Errorf("%s:\n got  %s\n want %s", source, got, want)
		}
	}
	mustFail(t, content(`<content><box medium+medium:gap="2">x</box></content>`), "content.ui:1:15", "một trục chỉ viết một lần")
}

// The progressbar (rows ui/compile.go 2089, 2092, 2097, 2273, 2280): two bars, one written below the
// header, one on a site that reloads, a thicker line or a longer wait all render as written and warn
// (question 3: a number the engine draws exactly). CONTROL: what is no number stays an error.
func TestCompileProgressbarIsDrawnAsWritten(t *testing.T) {
	content := `<content><text>x</text></content>`
	moves := func(pageUI string, files ...string) *Site {
		all := map[string]string{"/site/page.ui": pageUI, "/site/content.ui": content}
		for i := 0; i+1 < len(files); i += 2 {
			all[files[i]] = files[i+1]
		}
		return memorySite("", all)
	}
	twice := `<page><box component="progressbar" /><box component="progressbar" /><content /></page>`
	html := compileWarnsOn(t, moves(twice), where("page.ui", twice, `<box component="progressbar" /><content`), "page chỉ có một progressbar")
	if got := strings.Count(html, `data-kitwork-component="progressbar"`); got != 2 {
		t.Errorf("two bars written, %d rendered", got)
	}
	below := `<page><header /><box component="progressbar" /><content /></page>`
	html = compileWarnsOn(t, moves(below, "/site/header.ui", `<header><text>Quán</text></header>`), "page.ui:1:17", "progressbar đứng đầu page, trên <header />")
	mustContain(t, html, `</header><div class="progressbar"`)
	reloads := moves(`<page><box component="progressbar" /><content /></page>`)
	reloads.Reload = true
	html = compileWarnsOn(t, reloads, "page.ui:1:7", "router.reload(true) tải lại cả trang mỗi lần chuyển nên progressbar không bao giờ chạy")
	mustContain(t, html, `<div class="progressbar"`)

	thick := moves(`<page><box component="progressbar" thickness="9" /><content /></page>`)
	page, warned := compileDiagnose(t, thick)
	if len(warned) != 1 || !strings.HasPrefix(warned[0], "page.ui:1:36: thickness của progressbar là số px từ 1 đến 8 (mặc định 1)") {
		t.Errorf("thickness=\"9\": want one warning at the attribute, got %q", warned)
	}
	mustContain(t, page.Render(Request{Path: "/"}), `class="thickness-9 background-brand"`)
	mustContain(t, string(page.Stylesheet().CSS), `.thickness-9{block-size:9px}`)
	html = compileWarnsOn(t, moves(`<page><box component="progressbar" delay="20000" /><content /></page>`), "page.ui:1:36", "delay là số mili giây từ 0 đến 10000")
	mustContain(t, html, `data-kitwork-delay="20000"`)

	// CONTROL
	for _, bad := range []string{`thickness="0"`, `thickness="thin"`, `thickness="2px"`, `delay="-1"`, `delay="fast"`, `delay="1.5"`} {
		s := moves(`<page><box component="progressbar" ` + bad + ` /><content /></page>`)
		if _, err := s.Compile("/site"); err == nil || !strings.HasPrefix(err.Error(), "page.ui:1:36: ") {
			t.Errorf("%s must stay an error at the attribute, got %v", bad, err)
		}
	}
	quiet, warned := compileDiagnose(t, moves(`<page><box component="progressbar" thickness="8" delay="10000" /><content /></page>`))
	if len(warned) > 0 {
		t.Errorf("the house's own thickness and delay warn of nothing: %q", warned)
	}
	mustContain(t, quiet.Render(Request{Path: "/"}), `class="thickness-8 background-brand"`, `data-kitwork-delay="10000"`)
}

// "Chữ gõ sai đang biến mất im lặng": a typo the compiler used to drop now stops the compile at the
// place (ui/compile.go 689, 1308, 1333, 1349, 1376, 1427, 1497, the button's expanded; ui/runtime.go
// 244). CONTROL: the words each place takes compile.
func TestCompileTyposNeverVanish(t *testing.T) {
	for _, c := range []struct{ source, at, says string }{
		{`<content><link value="/" exact="yes">x</link></content>`, "content.ui:1:26", `exact chỉ nhận "true" hoặc "false"`},
		{`<content><button expanded="yes">x</button></content>`, "content.ui:1:18", `expanded chỉ nhận "true", "false" hoặc {{ }}`},
		{`<content><box scroll="z"><text>a</text></box></content>`, "content.ui:1:15", `scroll nhận x hoặc y, không có "z"`},
		{`<content><link value="">b</link></content>`, "content.ui:1:10", "link cần value (đích đến)"},
		{`<content><image value=" " alt="" /></content>`, "content.ui:1:10", "image cần value (nguồn ảnh)"},
		{`<content><time value="">x</time></content>`, "content.ui:1:10", "time cần value"},
		{`<content><icon value="" /></content>`, "content.ui:1:10", "icon cần value (tên icon)"},
		{`<content><input type="emial" alt="x"></content>`, "content.ui:1:17", `type của input là text, search, email`},
		{`<content><box component="appearance"><button click:debounse(300)="toggle()">x</button></box></content>`, "content.ui:1:46", "click:debounse(300): không có phần đi kèm debounse(300)"},
		{`<content><box component="appearance"><button click:debounce(0)="toggle()">x</button></box></content>`, "content.ui:1:46", "debounce(0): thời gian chờ là số mili giây từ 1 đến 60000"},
		{`<content><box component="appearance"><button click:escape="toggle()">x</button></box></content>`, "content.ui:1:46", "escape và enter lọc một phím, đặt trên keydown hoặc keyup"},
		{`<content><button keydown:escpae="x()">x</button></content>`, "content.ui:1:18", "keydown:escpae: không có phần đi kèm escpae"},
		// what the kernel or hydrate refuses on the wire (review 12/10/2026): a sign the kernel's digits do
		// not read, two thresholds, mod beside the key it already means
		{`<content><box component="appearance"><button click:debounce(+300)="toggle()">x</button></box></content>`, "content.ui:1:46", "debounce(+300): thời gian chờ là số mili giây từ 1 đến 60000"},
		{`<content><box intersect-enter:half.full="x()"><text>a</text></box></content>`, "content.ui:1:15", "intersect-enter:half.full: half và full là hai ngưỡng"},
		{`<content><box keydown:mod+ctrl+k="x()"><text>a</text></box></content>`, "content.ui:1:15", "keydown:mod+ctrl+k: mod+ctrl+k trộn mod với ctrl hay meta"},
		{`<content><box keydown:meta+mod+k="x()"><text>a</text></box></content>`, "content.ui:1:15", "meta+mod+k trộn mod với ctrl hay meta"},
	} {
		mustFail(t, content(c.source), c.at, c.says)
	}
	// CONTROL
	html := compileQuiet(t, content(`<content><link value="/" exact="true">x</link><button expanded="false">y</button><box scroll="y"><text>a</text></box>`+
		`<input type="email" alt="x"><box component="appearance"><button click:once.debounce(300)="toggle()">z</button></box>`+
		`<box keydown:mod+k.prevent="x()" keydown,keyup:escape="x()" intersect-enter:once.margin(-2.5rem)="x()"><text>b</text></box>`+
		`<box keydown:ctrl+shift+k="x()" intersect-leave:full="x()" click:throttle(0300)="x()"><text>c</text></box></content>`))
	mustContain(t, html, `data-kitwork-event="click.once.debounce(300): toggle()"`, `class="flex-column scroll-y"`, `type="email"`)
	// scroll from data is the area that scrolls, never a handler: each value it spells out is read
	s := memorySite("", content(`<content><box scroll="{{ wide ? 'x' : 'y' }}"><text>a</text></box></content>`))
	s.Reload = true
	page, _ := compileDiagnose(t, s)
	mustContain(t, page.Render(Request{Path: "/", Scope: map[string]any{"wide": true}}), `class="flex-column scroll-x"`)
	mustFail(t, content(`<content><box scroll="{{ wide ? 'x' : 'z' }}"><text>a</text></box></content>`), "content.ui:1:15", `scroll nhận x hoặc y, không có "z"`)
}

// "Lời báo không khớp mã" (ui/compile.go 374, 1240, 2291, ui/runtime.go 316): each message now says
// what the author wrote. CONTROL: the same places still stop the compile.
func TestCompileMessagesMatchWhatIsWritten(t *testing.T) {
	mustFail(t, content(`<content><list><option value="a">A</option></list></content>`), "content.ui:1:16",
		"con của list là item (danh sách rỗng thì thêm empty); bọc <option> trong <item>")
	if got := compileError(t, content(`<content><list><box>x</box></list></content>`)); strings.Contains(got, "hoặc option") {
		t.Errorf("a list takes no option, the message must not offer one: %s", got)
	}
	// purpel: purple is a named colour now, so the word the palette lacks is a typo of it
	bar := map[string]string{"/site/page.ui": `<page><box component="progressbar" color="purpel" /><content /></page>`, "/site/content.ui": `<content><text>x</text></content>`}
	got := compileError(t, bar)
	if !strings.HasPrefix(got, "page.ui:1:36: ") || !strings.Contains(got, `color="purpel"`) || strings.Contains(got, "background=") {
		t.Errorf("a colour the palette lacks is named as written, color=: %s", got)
	}
	mustFail(t, content(`<content><box component="appearance"><icon value="{{ mode }}" alt="Chế độ"></icon></box></content>`), "content.ui:1:38",
		`bên trong appearance value="{{ mode }}" đổi theo trạng thái`)
}

// Rows that stay errors (questions 5, 6, 7, 9 and ui/compile.go 320): what the engine cannot attach,
// content it would drop, a required value, a file it never reads. CONTROL for every warning above: a
// warning must let the page render as written, so these never become one.
func TestCompileRowsThatStayErrors(t *testing.T) {
	text := `<content><text>x</text></content>`
	for _, c := range []struct {
		files    map[string]string
		at, says string
	}{
		{map[string]string{"/site/page.ui": `<page><header component="menu" /><content /></page>`, "/site/header.ui": `<header><text>H</text></header>`, "/site/content.ui": text},
			"page.ui:1:15", "<header /> rỗng là chỗ gọi vùng"},
		{map[string]string{"/site/page.ui": `<page><header gap="4" /><content /></page>`, "/site/header.ui": `<header><text>H</text></header>`, "/site/content.ui": text},
			"page.ui:1:15", "thẻ vùng <header /> ở chỗ gọi chỉ nhận source, if, show"},
		{map[string]string{"/site/page.ui": `<page><header /><content /></page>`, "/site/header.ui": `<header><text>H</text></header>`, "/site/header.work": `export default {}`, "/site/content.ui": text},
			"header.work:1:1", "vùng không có file .work"},
		{map[string]string{"/site/page.ui": `<page><content /><reader>x</reader></page>`, "/site/content.ui": text}, "page.ui:1:18", "reader không có nội dung con"},
		{content(`<content><box component="toast">x</box></content>`), "content.ui:1:10", "toast cần một mẫu thông báo bên trong thẻ chủ"},
		{map[string]string{"/site/page.ui": `<page><box component="progressbar">x</box><content /></page>`, "/site/content.ui": text}, "page.ui:1:7", "progressbar không có nội dung con"},
		{content(`<content><select><option>A</option></select></content>`), "content.ui:1:18", "option cần value"},
		{content(`<content><time>hôm nay</time></content>`), "content.ui:1:10", "time cần value"},
		{content(`<content><box id="{{ x }}">x</box></content>`), "content.ui:1:15", "id là chữ cố định"},
	} {
		mustFail(t, c.files, c.at, c.says)
	}
}
