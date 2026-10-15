package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// memorySite is a site whose files live in a map, keyed by slash paths under /site.
func memorySite(target string, files map[string]string) *Site {
	return &Site{Root: "/site", Target: target, Read: func(path string) ([]byte, error) {
		if source, ok := files[filepath.ToSlash(path)]; ok {
			return []byte(source), nil
		}
		return nil, os.ErrNotExist
	}}
}

func renderAt(t *testing.T, s *Site, route, path string, scope map[string]any) string {
	t.Helper()
	page, err := s.Compile(filepath.Join(s.Root, filepath.FromSlash(route)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return page.Render(Request{Path: path, Scope: scope})
}

// renderWithCSS renders a route of a site and returns its body with the page's own stylesheet,
// page.<hash>.css: the rules of every class the page can carry.
func renderWithCSS(t *testing.T, s *Site, route, path string, scope map[string]any) (string, string) {
	t.Helper()
	page, err := s.Compile(filepath.Join(s.Root, filepath.FromSlash(route)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return page.Render(Request{Path: path, Scope: scope}), string(page.Stylesheet().CSS)
}

// renderCSS is render with the page's own stylesheet.
func renderCSS(t *testing.T, content string, scope map[string]any) (string, string) {
	t.Helper()
	s := memorySite("", map[string]string{"/site/content.ui": content})
	s.Reload = true
	return renderWithCSS(t, s, ".", "/", scope)
}

func render(t *testing.T, content string, scope map[string]any) string {
	t.Helper()
	s := memorySite("", map[string]string{"/site/content.ui": content})
	s.Reload = true // one rule at a time: the progressbar every other page gets has tests of its own
	return renderAt(t, s, ".", "/", scope)
}

func compileError(t *testing.T, files map[string]string) string {
	t.Helper()
	_, err := memorySite("", files).Compile("/site")
	if err == nil {
		t.Fatalf("expected a compile error")
	}
	return err.Error()
}

func mustContain(t *testing.T, html string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(html, w) {
			t.Errorf("missing %q in\n%s", w, html)
		}
	}
}

func mustNotContain(t *testing.T, html string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(html, u) {
			t.Errorf("unexpected %q in\n%s", u, html)
		}
	}
}

// One tag, text: the engine picks <p> for a block, <span> inside a line, <hN> for a heading.
func TestTextElementFollowsItsPlace(t *testing.T) {
	html := render(t, `<content>
  <text>Khối</text>
  <text heading="2">Tiêu đề</text>
  <link value="/x"><text>Trong dòng</text></link>
  <text>Tạm tính: <text weight="bold">29k</text></text>
</content>`, nil)
	mustContain(t, html, "<p>Khối</p>", `<h2>Tiêu đề</h2>`, `<a class="flex-row" href="/x"><span>Trong dòng</span></a>`,
		`<p>Tạm tính: <span class="weight-bold">29k</span></p>`)
	mustNotContain(t, html, "<p>Trong dòng", "<p class=\"weight-bold\"")
}

// A target prefix is decided when the page is built: other targets' branches are not in the output.
func TestTargetPrefixDecidedAtBuild(t *testing.T) {
	files := map[string]string{"/site/content.ui": `<content><box padding="2" mobile:padding="4" website:gap="1">x</box></content>`}
	web := renderAt(t, memorySite("website", files), ".", "/", nil)
	mustContain(t, web, `class="flex-column padding-2 gap-1"`)
	mustNotContain(t, web, "padding-4")
	// control: ios belongs to mobile, so the same source keeps the mobile branch and drops website's
	ios := renderAt(t, memorySite("ios", files), ".", "/", nil)
	mustContain(t, ios, `class="flex-column padding-2 padding-4"`)
	mustNotContain(t, ios, "gap-1")
}

func TestWidthAndStatePrefixes(t *testing.T) {
	html, css := renderCSS(t, `<content><box gap="2" large+hover:gap="4" small,large:padding="1" hover:background="brand-85" medium+hover:background="brand+15">x</box></content>`, nil)
	// the class is the attribute as written; widths joined by "," are min-width, so the narrowest says it.
	// The + of a prefix comes before the ":", the + of a stronger colour after the "=" (13/10/2026)
	mustContain(t, html, `class="flex-column gap-2 large+hover:gap-4 small:padding-1 hover:background-brand-85 medium+hover:background-brand+15"`)
	mustContain(t, css,
		"@media (min-width:640px){\n"+`.small\:padding-1{padding:.25rem}`,
		"@media (hover:hover){\n"+`.hover\:background-brand-85:where(:hover){background-color:color-mix(in oklab,var(--brand) 15%,var(--canvas))}`,
		"@media (min-width:1024px) and (hover:hover){\n"+`.large\+hover\:gap-4:where(:hover){gap:1rem}`,
		// a stronger colour is a custom property of the page, its value in each mode
		"@media (min-width:768px) and (hover:hover){\n"+`.medium\+hover\:background-brand\+15:where(:hover){background-color:var(--kitwork-strong-15-brand)}`,
		`--kitwork-strong-15-brand:#5537fc`)
}

func TestShowWithWidthsWritesDisplayClasses(t *testing.T) {
	html := render(t, `<content><box flex="row" show="false" medium:show="true">x</box><box show="open">y</box></content>`,
		map[string]any{"open": false})
	mustContain(t, html, `class="flex-row show-false medium:show-true"`, `class="flex-column" hidden>y`)
	mustNotContain(t, html, "display:none")
	// control: a true value writes no hiding at all
	html = render(t, `<content><box show="open">y</box></content>`, map[string]any{"open": true})
	mustNotContain(t, html, "display:none", "hidden")
}

// A false show is the bare hidden attribute wherever it is decided: a show of the page's data, a show
// at the host of a Kitwork component, and a show the browser keeps up to date inside the component. The
// kernel shows and hides with hidden too, so a show binding can always reveal what the server hid;
// an inline display:none would outlive the binding.
func TestFalseShowIsTheHiddenAttribute(t *testing.T) {
	src := `<content>
  <text show="sale">giảm giá</text>
  <box component="dropdown" show="signedIn"><text>tài khoản</text></box>
  <box component="dropdown"><card show="open"><text>menu</text></card></box>
</content>`
	html := render(t, src, map[string]any{"sale": false, "signedIn": false})
	mustContain(t, html,
		`<p hidden>giảm giá</p>`,
		`data-kitwork-component="dropdown" hidden><p>tài khoản</p>`,
		`data-kitwork-bind="show: open" hidden><p>menu</p>`)
	mustNotContain(t, html, "display:none")
	// control: true values hide nothing; the menu reads the dropdown's own open, which starts false
	html = render(t, src, map[string]any{"sale": true, "signedIn": true})
	mustContain(t, html, `<p>giảm giá</p>`, `data-kitwork-component="dropdown"><p>tài khoản</p>`,
		`data-kitwork-bind="show: open" hidden><p>menu</p>`)
}

func TestIfRemovesTheElement(t *testing.T) {
	src := `<content><text if="sale">Giảm giá</text><text>Luôn có</text></content>`
	mustNotContain(t, render(t, src, map[string]any{"sale": false}), "Giảm giá")
	mustContain(t, render(t, src, map[string]any{"sale": true}), "<p>Giảm giá</p>") // control
}

func TestAttributeAndPrefixErrors(t *testing.T) {
	cases := []struct{ source, want string }{
		{`<content><box large,hover:gap="2">x</box></content>`, "cùng một trục"},
		{`<content><box tablet:gap="2">x</box></content>`, "tiền tố không tồn tại: tablet"},
		{`<content><input bind:value="q"></content>`, "không có bind:"},
		{`<content><box if="a" hover:if="b">x</box></content>`, "if không nhận tiền tố"},
		{`<content><text color="ink" hover:heading="2">x</text></content>`, "không nhận tiền tố"},
		{`<content><box space="4">x</box></content>`, `gap="4"`},
	}
	for _, c := range cases {
		got := compileError(t, map[string]string{"/site/content.ui": c.source})
		if !strings.Contains(got, c.want) {
			t.Errorf("%s\n  error %q does not say %q", c.source, got, c.want)
		}
		if !strings.HasPrefix(got, "content.ui:1:") {
			t.Errorf("%s\n  error %q does not point at content.ui line 1", c.source, got)
		}
	}
	// control: the valid forms of the same attributes compile
	html := render(t, `<content><box large+hover:gap="2" space="between">x</box><input required="true"></content>`, nil)
	mustContain(t, html, "large+hover:gap-2", "space-between", " required>")
}

func TestParseErrorsPointAtTheSource(t *testing.T) {
	cases := []struct{ source, want string }{
		{"<content>\n  <box>\n</content>", "content.ui:3:1: thẻ đóng </content> không khớp thẻ mở <box> ở dòng 2"},
		{"<content>\n  <box gap=2>x</box>\n</content>", "content.ui:2:12: giá trị của gap phải đặt trong dấu nháy"},
		{"<content>{{ a </content>", "biểu thức {{ chưa được đóng bằng }}"},
		{"<content><box>", "thẻ <box> chưa được đóng"},
	}
	for _, c := range cases {
		got := compileError(t, map[string]string{"/site/content.ui": c.source})
		if !strings.Contains(got, c.want) {
			t.Errorf("%q\n  got  %s\n  want %s", c.source, got, c.want)
		}
	}
}

// A region tag is replaced by the root of its file; the call site only says where and whether.
func TestRegionReplacesItsCallSite(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page><header /><content /></page>`,
		"/site/header.ui":  `<header flex="row" gap="2"><text>Quán</text></header>`,
		"/site/content.ui": `<content><text>Trang chủ</text></content>`,
	}
	html := renderAt(t, memorySite("", files), ".", "/", nil)
	mustContain(t, html, `<header class="flex-row gap-2"><p>Quán</p></header>`, "<main", "<p>Trang chủ</p>")
	mustNotContain(t, html, "<header />", "<header><header")

	// the nearest page.ui replaces (never nests in) the outer one
	files["/site/shop/page.ui"] = `<page><content /></page>`
	files["/site/shop/content.ui"] = `<content><text>Cửa hàng</text></content>`
	shop := renderAt(t, memorySite("", files), "shop", "/shop", nil)
	mustContain(t, shop, "<p>Cửa hàng</p>")
	mustNotContain(t, shop, "Quán")

	// control: a region file with the wrong root, or layout on the call site, is an error
	bad := map[string]string{
		"/site/page.ui":    `<page><header /><content /></page>`,
		"/site/header.ui":  `<box><text>Quán</text></box>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	}
	if got := compileError(t, bad); !strings.Contains(got, "header.ui phải có gốc <header>") {
		t.Errorf("wrong root: %s", got)
	}
	bad["/site/header.ui"] = `<header><text>Quán</text></header>`
	bad["/site/page.ui"] = `<page><header gap="2" /><content /></page>`
	if got := compileError(t, bad); !strings.Contains(got, "chỉ nhận source, if, show") {
		t.Errorf("layout on call site: %s", got)
	}
}

func TestSiteUIGetsDataFromTheCallSite(t *testing.T) {
	files := map[string]string{
		"/site/content.ui":      `<content><grid columns="2"><product-card for="p of products" product="{{ p }}" old-price="{{ p.was }}" span="2" /></grid></content>`,
		"/site/product-card.ui": `<card><text>{{ product.name }} {{ oldPrice }}</text></card>`,
	}
	html := renderAt(t, memorySite("", files), ".", "/", map[string]any{"products": []any{
		map[string]any{"name": "A", "was": 10.0},
		map[string]any{"name": "B", "was": nil},
	}})
	mustContain(t, html, "<p>A 10</p>", "<p>B </p>", `<div class="card flex-column span-2">`)
	if strings.Count(html, `class="card `) != 2 {
		t.Errorf("want one card per product:\n%s", html)
	}
	// control: an unknown tag with no file is an error, not an empty element
	if got := compileError(t, map[string]string{"/site/content.ui": `<content><price-tag /></content>`}); !strings.Contains(got, "không biết thẻ <price-tag>") {
		t.Errorf("unknown tag: %s", got)
	}
}

func TestListShowsEmptyOnlyWithoutItems(t *testing.T) {
	src := `<content><list><item for="r of reviews"><text>{{ r }}</text></item><empty>Chưa có.</empty></list></content>`
	none := render(t, src, map[string]any{"reviews": []any{}})
	mustContain(t, none, `role="status">Chưa có.</li>`)
	some := render(t, src, map[string]any{"reviews": []any{"Ngon"}})
	mustContain(t, some, "<p>Ngon</p>")
	mustNotContain(t, some, "Chưa có.") // control
}

func TestLinkCurrent(t *testing.T) {
	cases := []struct {
		path, href string
		exact      bool
		want       string
	}{
		{"/", "/", false, "page"},
		{"/san-pham/a", "/", false, ""}, // "/" only matches itself
		{"/san-pham/a", "/san-pham", false, "true"},
		{"/san-pham/a", "/san-pham/", false, "true"},
		{"/san-pham/a", "/san-pham", true, ""},
		{"/san-pham-moi", "/san-pham", false, ""}, // a prefix of the text, not a parent folder
		{"/ve-chung-toi/", "/ve-chung-toi?tab=1", false, "page"},
		{"/a", "https://example.com/a", false, ""},
		{"/a", "#top", false, ""},
	}
	for _, c := range cases {
		if got := linkCurrent(c.path, c.href, c.exact); got != c.want {
			t.Errorf("linkCurrent(%q, %q, %v) = %q, want %q", c.path, c.href, c.exact, got, c.want)
		}
	}
}

func TestDataIsEscaped(t *testing.T) {
	evil := `<script>alert("x")</script>`
	html := render(t, `<content><text>{{ name }}</text><image value="/a.jpg" alt="{{ name }}"></image></content>`,
		map[string]any{"name": evil})
	mustNotContain(t, html, "<script>")
	mustContain(t, html, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;")
}

func TestBreadcrumbWritesStructuredData(t *testing.T) {
	s := memorySite("", map[string]string{"/site/a/content.ui": `<content><breadcrumb>
  <item><link value="/">Trang chủ</link><icon value="chevron-right" alt=""></icon></item>
  <item><text>{{ title }}</text></item>
</breadcrumb></content>`})
	page, err := s.Compile("/site/a")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/a", Origin: "https://quan.vn", Scope: map[string]any{"title": "Món </script>"}})
	mustContain(t, html, `<nav aria-label="Vị trí trang">`, `<span aria-current="page">Món &lt;/script&gt;</span>`,
		`"position":1,"name":"Trang chủ","item":"https://quan.vn/"`, `"position":2,"name":"Món \u003c/script\u003e","item":"https://quan.vn/a"`)
	if strings.Count(html, "</script>") != 1 {
		t.Errorf("the data must not close its script element early:\n%s", html)
	}
}

func TestAccordionsShareANameAndOpenFollowsData(t *testing.T) {
	html := render(t, `<content><accordions>
  <accordion for="f of faqs" open="{{ f.open }}">
    <box click="toggle()" flex="row"><text>{{ f.q }}</text></box>
    <text>{{ f.a }}</text>
  </accordion>
</accordions></content>`, map[string]any{"faqs": []any{
		map[string]any{"q": "Q1", "a": "A1", "open": true},
		map[string]any{"q": "Q2", "a": "A2", "open": false},
	}})
	mustContain(t, html, `<details name="accordions-1" open><summary`, `<details name="accordions-1"><summary`,
		"<span>Q1</span></summary><p>A1</p></details>")
}

func TestFieldLinksLabelAndHint(t *testing.T) {
	html := render(t, `<content><field><label>SĐT</label><input type="tel" value="{{ phone }}"><hint>Gọi lại.</hint></field></content>`,
		map[string]any{"phone": "0901"})
	mustContain(t, html, `<label for="kitwork-field-1">SĐT</label>`, `id="kitwork-field-1" aria-describedby="kitwork-field-1-hint"`, `value="0901"`, `id="kitwork-field-1-hint"`)
}

func TestHelpers(t *testing.T) {
	cases := []struct{ got, want string }{
		{money(29000.0, ""), "29.000đ"},
		{money(1234567.0, ""), "1.234.567đ"},
		{money(-5000.0, ""), "-5.000đ"},
		{money(12.5, "USD"), "$12.50"},
		{money(nil, ""), ""},
		{formatDate("2026-10-02T08:30:00Z"), "02/10/2026"},
		{initials("Nguyễn Văn An"), "NA"},
		{initials("an"), "A"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %q, want %q", i, c.got, c.want)
		}
	}
	// the author's own name wins over a built-in helper
	html := render(t, `<content><text>{{ money }}</text></content>`, map[string]any{"money": "của tôi"})
	mustContain(t, html, "<p>của tôi</p>")
}

// Drive swaps only <content />: links outside it (header, navbar) stay on the page across a soft
// navigation, so the engine marks them for the client to mark again. Links inside are re-rendered.
func TestKeptLinksAreMarkedForTheClient(t *testing.T) {
	html := renderAt(t, memorySite("", map[string]string{
		"/site/page.ui":    `<page><header /><content /></page>`,
		"/site/header.ui":  `<header><link value="/">Thực đơn</link><link value="/gioi-thieu" exact="true">Giới thiệu</link><link value="https://example.com">Ngoài</link></header>`,
		"/site/content.ui": `<content><link value="/san-pham">Sản phẩm</link></content>`,
	}), ".", "/", nil)
	mustContain(t, html,
		`href="/" aria-current="page" data-kitwork-current="">Thực đơn`,
		`href="/gioi-thieu" data-kitwork-current="exact">Giới thiệu`)
	// control: a link in the content, and a link to another site, carry no mark
	mustContain(t, html, `<a href="/san-pham">Sản phẩm</a>`, `<a href="https://example.com" rel="external noopener">Ngoài</a>`)
}

// reload="true" on one link loads the next page whole; Drive reads data-kit-reload="true".
func TestLinkReload(t *testing.T) {
	html := render(t, `<content><link value="/thanh-toan" reload="true">Thanh toán</link><link value="/menu" reload="false">Thực đơn</link><link value="/gio-hang">Giỏ</link></content>`, nil)
	mustContain(t, html, `<a data-kit-reload="true" href="/thanh-toan">Thanh toán</a>`)
	// control: reload="false" and no reload write nothing
	mustContain(t, html, `<a href="/menu">Thực đơn</a>`, `<a href="/gio-hang">Giỏ</a>`)
	for _, source := range []string{
		`<content><link value="/x" reload="yes">X</link></content>`,
	} {
		got := compileError(t, map[string]string{"/site/content.ui": source})
		if !strings.Contains(got, `reload`) || !strings.Contains(got, `"true"`) {
			t.Errorf("%s\n  error %q does not ask for \"true\" or \"false\"", source, got)
		}
	}
}

// The move between pages has a bar on every page of a site that does not reload: the one page.ui
// writes, else one at the top, above the header. A site that reloads every page has none.
func TestProgressbarIsOnEveryPageThatDoesNotReload(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page><header /><content /></page>`,
		"/site/header.ui":  `<header><link value="/">Quán</link></header>`,
		"/site/content.ui": `<content><text>Thực đơn</text></content>`,
	}
	html := renderAt(t, memorySite("", files), ".", "/", nil) // router.reload(false), the default
	mustContain(t, html, `data-kitwork-component="progressbar"`, `role="progressbar"`, `aria-label="Đang chuyển trang"`,
		`data-kitwork-bind="show: active; aria-valuenow: value" hidden`, `data-kitwork-style="top: top"`,
		`<div class="progressbar" data-kitwork-component="progressbar"`,
		`<div class="thickness-1 background-brand" data-kitwork-style="width: value + '%'"></div>`) // brand and 1px unless page.ui says otherwise
	// the grouped web form only: none of the hand-written data-kit-* spellings
	mustNotContain(t, html, "data-kit-")
	bar, header := strings.Index(html, `data-kitwork-component="progressbar"`), strings.Index(html, "<header")
	if bar < 0 || header < 0 || bar > header {
		t.Errorf("the bar must come before the header (bar at %d, header at %d)", bar, header)
	}
	if strings.Count(html, `data-kitwork-component="progressbar"`) != 1 {
		t.Errorf("one bar per page:\n%s", html)
	}
	// control: the same site with router.reload(true) has no bar
	reloading := memorySite("", files)
	reloading.Reload = true
	mustNotContain(t, renderAt(t, reloading, ".", "/", nil), "progressbar")

	// a page with no page.ui gets one too
	bare := memorySite("", map[string]string{"/site/content.ui": `<content><text>x</text></content>`})
	mustContain(t, renderAt(t, bare, ".", "/", nil), `data-kitwork-component="progressbar"`)
}

// Written in page.ui, the bar takes its colour and its name from there, and is not added twice.
func TestProgressbarWrittenInPage(t *testing.T) {
	s := memorySite("", map[string]string{
		"/site/page.ui":    `<page><box component="progressbar" color="ink-30" alt="Đang mở trang" /><header /><content /></page>`,
		"/site/header.ui":  `<header><text>Quán</text></header>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	})
	html := renderAt(t, s, ".", "/", nil)
	mustContain(t, html, `class="thickness-1 background-ink-30"`, `aria-label="Đang mở trang"`)
	mustNotContain(t, html, "background-brand", "Đang chuyển trang")
	if strings.Count(html, `data-kitwork-component="progressbar"`) != 1 {
		t.Errorf("a written bar replaces the default one:\n%s", html)
	}
}

// The bar belongs to the frame, at its top edge: not inside a region, not below the header, once.
func TestProgressbarPlacementErrors(t *testing.T) {
	compile := func(files map[string]string, reload bool) string {
		t.Helper()
		s := memorySite("", files)
		s.Reload = reload
		_, err := s.Compile("/site")
		if err == nil {
			t.Fatalf("expected a compile error for %v", files)
		}
		return err.Error()
	}
	content := `<content><text>x</text></content>`
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"inside the header", map[string]string{
			"/site/page.ui": `<page><header /><content /></page>`, "/site/content.ui": content,
			"/site/header.ui": `<header><box component="progressbar" /><text>Quán</text></header>`,
		}, "progressbar đặt trong page.ui"},
		{"inside the content", map[string]string{
			"/site/content.ui": `<content><box component="progressbar" /><text>x</text></content>`,
		}, "progressbar đặt trong page.ui"},
		{"a word that is none of .ui", map[string]string{
			"/site/page.ui": `<page><box component="progressbar" lang="vi" /><content /></page>`, "/site/content.ui": content,
		}, "progressbar chỉ nhận color"},
		{"content inside", map[string]string{
			"/site/page.ui": `<page><box component="progressbar">Đang tải</box><content /></page>`, "/site/content.ui": content,
		}, "progressbar không có nội dung con"},
	}
	for _, c := range cases {
		if got := compile(c.files, false); !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want it to say %q", c.name, got, c.want)
		}
	}
	// below the header, twice, value= or a site that reloads: drawn as written and warned of
	// (diagnostics_compile_test.go)
	// control: the same page.ui compiles on a site that does not reload
	s := memorySite("", map[string]string{"/site/page.ui": `<page><box component="progressbar" /><content /></page>`, "/site/content.ui": content})
	if _, err := s.Compile("/site"); err != nil {
		t.Errorf("a bar first in page.ui must compile: %v", err)
	}
}

// delay says how long a move may take before the bar shows. Only a value that differs from Drive's
// own 120ms is written; anything but a whole number of ms in 0..10000 is refused.
func TestProgressbarDelay(t *testing.T) {
	page := func(bar string) string {
		s := memorySite("", map[string]string{
			"/site/page.ui":    `<page>` + bar + `<content /></page>`,
			"/site/content.ui": `<content><text>x</text></content>`,
		})
		return renderAt(t, s, ".", "/", nil)
	}
	mustContain(t, page(`<box component="progressbar" delay="0" />`), `data-kitwork-delay="0"`)
	mustContain(t, page(`<box component="progressbar" delay="400" />`), `data-kitwork-delay="400"`)
	// control: the default, written or not, puts nothing on the page
	mustNotContain(t, page(`<box component="progressbar" delay="120" />`), "data-kitwork-delay")
	mustNotContain(t, page(`<box component="progressbar" />`), "data-kitwork-delay")
	mustNotContain(t, page(``), "data-kitwork-delay")

	for _, bad := range []string{`"-1"`, `"fast"`, `"1.5"`, `"{{ wait }}"`} { // "10001": a warning
		s := memorySite("", map[string]string{
			"/site/page.ui":    `<page><box component="progressbar" delay=` + bad + ` /><content /></page>`,
			"/site/content.ui": `<content><text>x</text></content>`,
		})
		if _, err := s.Compile("/site"); err == nil {
			t.Errorf("delay=%s must be refused", bad)
		}
	}
}

// thickness is how thick the line is in px, as a line width is everywhere (border-width, lineWidth,
// trackThickness): thickness="1" is 1px, and 1px unless page.ui says otherwise.
func TestProgressbarThickness(t *testing.T) {
	page := func(bar string) string {
		s := memorySite("", map[string]string{
			"/site/page.ui":    `<page>` + bar + `<content /></page>`,
			"/site/content.ui": `<content><text>x</text></content>`,
		})
		html, css := renderWithCSS(t, s, ".", "/", nil)
		return html + "\n" + css
	}
	mustContain(t, page(`<box component="progressbar" thickness="4" />`), `class="thickness-4 background-brand"`, `.thickness-4{block-size:4px}`)
	mustContain(t, page(`<box component="progressbar" thickness="8" color="ink" />`), `class="thickness-8 background-ink"`, `.thickness-8{block-size:8px}`)
	// control: unwritten, the default
	mustContain(t, page(`<box component="progressbar" />`), `class="thickness-1 background-brand"`, `.thickness-1{block-size:1px}`)
	mustContain(t, page(`<box component="progressbar" thickness="1" />`), `.thickness-1{block-size:1px}`) // 1 is 1px, not the spacing scale's 4px
	mustContain(t, page(``), `class="thickness-1 background-brand"`)
	for _, bad := range []string{`"0"`, `"0.5"`, `"2px"`, `"thin"`} { // "9": drawn, a warning
		s := memorySite("", map[string]string{
			"/site/page.ui":    `<page><box component="progressbar" thickness=` + bad + ` /><content /></page>`,
			"/site/content.ui": `<content><text>x</text></content>`,
		})
		_, err := s.Compile("/site")
		if err == nil || !strings.Contains(err.Error(), "thickness của progressbar") {
			t.Errorf("thickness=%s must be refused naming the thicknesses, got %v", bad, err)
		}
	}
}

// font names a typeface by its role in router.fonts (body, code): the class is the attribute as
// written, and its rule reads the role's family from fonts.<hash>.css with the system stack of that
// role behind it, so font="code" is never a class with no face, declared or not.
func TestFontIsARoleOfTheTheme(t *testing.T) {
	files := map[string]string{"/site/content.ui": `<content><text font="code">29.000đ</text><text font="body">chữ</text></content>`}
	s := memorySite("", files)
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/"})
	mustContain(t, html, `<p class="font-code">29.000đ</p>`, `<p class="font-body">chữ</p>`)
	css := string(page.Stylesheet().CSS)
	mustContain(t, css, ".font-code{font-family:var(--font-code,ui-monospace,monospace)}", ".font-body{font-family:var(--font-body,system-ui,sans-serif)}")

	for source, want := range map[string]string{
		`<content><text font="mono">x</text></content>`:    "font theo vai trò trong router.fonts: code (không viết mono)",
		`<content><text font="sans">x</text></content>`:    "font theo vai trò trong router.fonts: body (không viết sans)",
		`<content><text font="cursive">x</text></content>`: "font là body hoặc code: họ chữ router.fonts khai",
	} {
		if got := compileError(t, map[string]string{"/site/content.ui": source}); !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want %q", source, got, want)
		}
	}
}

// Document writes the route's appearance on <html> only when it fixes one (light, dark): the pre-paint
// and kit.appearance fall back on it while the visitor has stored no choice. "system" (the default,
// follow the device), nothing, and any other word write nothing.
func TestDocumentWritesTheAppearanceOnlyForLightAndDark(t *testing.T) {
	s := memorySite("", map[string]string{"/site/content.ui": `<content><text>x</text></content>`})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	for mode, want := range map[string]string{
		"light":  `<html lang="vi" data-kitwork-appearance="light">`,
		"dark":   `<html lang="vi" data-kitwork-appearance="dark">`,
		"system": `<html lang="vi">`,
		"":       `<html lang="vi">`,
		"Dark":   `<html lang="vi">`,
		"blue":   `<html lang="vi">`,
	} {
		doc := page.Document(Request{Path: "/", Appearance: mode})
		if !strings.Contains(doc, "\n"+want+"\n<head>") {
			t.Errorf("Appearance %q: want %s, got %.80q", mode, want, doc)
		}
	}
	// beside a transition, both are written
	doc := page.Document(Request{Path: "/", Appearance: "dark", Transition: "fade"})
	mustContain(t, doc, `<html lang="vi" data-kitwork-appearance="dark" data-kitwork-transition="fade">`)
}

// A heading is sized by its level, semibold and tracked tight; size written on it picks a step of the
// heading scale and weight replaces the level's own. Capitals are spaced apart. A link or a button laid out in a row stays inline.
func TestHeadingsAndInlineRows(t *testing.T) {
	html := render(t, `<content>
  <text heading="1">Thực đơn</text>
  <text heading="2" size="mini">Giỏ hàng</text>
  <text heading="3" weight="bold">Cà phê sữa</text>
  <text size="mini" case="upper">Hôm nay</text>
  <link value="/" flex="row" gap="2"><icon value="coffee" alt=""></icon><text>Quán</text></link>
  <button flex="row" gap="2"><icon value="plus" alt=""></icon><text>Thêm</text></button>
</content>`, nil)
	mustContain(t, html,
		`<h1>Thực đơn</h1>`,
		`<h2 class="size-mini">Giỏ hàng</h2>`,
		`<h3 class="weight-bold">Cà phê sữa</h3>`,
		`<p class="size-mini case-upper">Hôm nay</p>`,
		`<a class="flex-row gap-2"`,
		`<button class="`+neutralClasses+` flex-row gap-2" type="button">`)
	// a link or a button laid out in a row stays inline (reboot.css), and capitals are spaced apart
	mustContain(t, rebootCSS(), `:where(a,button,label,span):where(.flex-row,.flex-column){display:inline-flex}`)
	_, css := renderCSS(t, `<content><text size="mini" case="upper">Hôm nay</text></content>`, nil)
	mustContain(t, css, `.case-upper{text-transform:uppercase;letter-spacing:.14em}`)
}

// A badge is a tinted pill: an accent's fill 85% lighter (X-85) under its text, the engine colour
// --kitwork-badge-X, stronger by the smallest step of 5 that reads 4.5:1 on that fill in each mode, from
// 15 in light (warning+55 on Kit Design's warning-85, brand+15 #5537fc on its brand-85) and from 0 in
// dark (decision of 13/10/2026); a text colour (ink, ink-30) has no family and sits on the panel fill. The fill is --kitwork-tint, which the colour class sets on a badge, so a
// background written on the badge wins. A colour from data takes the same rule: every token the
// palette declares is in the page's stylesheet.
func TestBadgeTints(t *testing.T) {
	html, css := renderCSS(t, `<content>
  <badge color="warning"><text>Đang pha</text></badge>
  <badge color="ink-30"><text>Tạm hết</text></badge>
  <badge color="{{ tone }}"><text>Chờ lấy</text></badge>
</content>`, map[string]any{"tone": "brand"})
	mustContain(t, html, `<span class="badge flex-row color-warning">`, `<span class="badge flex-row color-ink-30">`, `<span class="badge flex-row color-brand">`)
	mustContain(t, css,
		`.color-warning:where(.badge){--kitwork-tint:color-mix(in oklab,var(--warning) 15%,var(--canvas));color:var(--kitwork-badge-warning)}`,
		`.color-ink-30:where(.badge){--kitwork-tint:var(--panel)}`,
		`.color-brand:where(.badge){--kitwork-tint:color-mix(in oklab,var(--brand) 15%,var(--canvas));color:var(--kitwork-badge-brand)}`,
		`--kitwork-badge-warning:#8c5c00`, `--kitwork-badge-brand:#5537fc`)
	mustNotContain(t, css, `--kitwork-strong-`)
	// never stretched across a column, filled with the tint
	mustContain(t, rebootCSS(), `inline-size:fit-content`, `background-color:var(--kitwork-tint)`)
	mustNotContain(t, html, "/15")
}

// <body> names the page.ui it is built from: a link between pages of the same page.ui swaps only the
// content, a link to a page with another page.ui is a whole new page (Drive compares the two).
func TestBodyNamesItsLayout(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":                 `<page><content /></page>`,
		"/site/content.ui":              `<content><text>Thực đơn</text></content>`,
		"/site/quan-ly/page.ui":         `<page><content /></page>`,
		"/site/quan-ly/content.ui":      `<content><text>Quản lý</text></content>`,
		"/site/ve-chung-toi/content.ui": `<content><text>Về</text></content>`,
	}
	s := memorySite("", files)
	mustContain(t, renderAt(t, s, ".", "/", nil), `data-kitwork-layout="page.ui"`)
	mustContain(t, renderAt(t, s, "ve-chung-toi", "/ve-chung-toi", nil), `data-kitwork-layout="page.ui"`)
	mustContain(t, renderAt(t, s, "quan-ly", "/quan-ly", nil), `data-kitwork-layout="quan-ly/page.ui"`)
	// a site with no page.ui has one page for every route: nothing to tell apart
	mustNotContain(t, render(t, `<content><text>x</text></content>`, nil), "data-kitwork-layout")
}

// <reader /> is what a screen reader says aloud. A site that moves without a reload gets one on every
// page that writes none (Drive says the new title through it); a page may write its own with an alias,
// once, straight in <page>. A site that reloads every page gets none unless it writes one.
func TestReaderOnEveryPageThatMovesWithoutAReload(t *testing.T) {
	plain := map[string]string{"/site/content.ui": `<content><text>x</text></content>`}
	html := renderAt(t, memorySite("", plain), ".", "/", nil)
	// invisible but read (reboot.css's reader: 1px and clipped, never display:none); no style attribute
	mustContain(t, html, `<div class="reader" data-kitwork-component="reader" data-kitwork-bind="text: text" aria-live="polite" aria-atomic="true"></div>`)
	mustNotContain(t, html, ` style="`)
	// and never in a selection: what it says is spoken, so a copy of the text around it never takes it
	mustContain(t, rebootCSS(), `:where(.reader){position:absolute;inline-size:1px;block-size:1px;margin:-1px;overflow:hidden;clip-path:inset(50%);white-space:nowrap;-webkit-user-select:none;user-select:none}`)
	if strings.Count(html, `data-kitwork-component="reader`) != 1 {
		t.Errorf("one reader per page:\n%s", html)
	}
	reloads := memorySite("", plain)
	reloads.Reload = true
	if html := renderAt(t, reloads, ".", "/", nil); strings.Contains(html, "reader") {
		t.Errorf("a site that reloads every page needs no reader:\n%s", html)
	}

	written := map[string]string{
		"/site/page.ui":    `<page><content /><reader as="$reader" /></page>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	}
	html = renderAt(t, memorySite("", written), ".", "/", nil)
	mustContain(t, html, `data-kitwork-component="reader: $reader"`)
	if strings.Count(html, `data-kitwork-component="reader`) != 1 {
		t.Errorf("the reader page.ui writes is the only one:\n%s", html)
	}
	reloads = memorySite("", written)
	reloads.Reload = true
	mustContain(t, renderAt(t, reloads, ".", "/", nil), `data-kitwork-component="reader: $reader"`)

	for source, want := range map[string]string{
		`<page><content /><reader as="reader" /></page>`:          "as là $ và một tên",
		`<page><content /><reader lang="vi" /></page>`:            "reader chỉ nhận as",
		`<page><content /><reader><text>x</text></reader></page>`: "reader không có nội dung con",
	} {
		got := compileError(t, map[string]string{"/site/page.ui": source, "/site/content.ui": `<content><text>x</text></content>`})
		if !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want %q", source, got, want)
		}
	}
}
