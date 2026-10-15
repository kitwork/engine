package ui

import (
	"regexp"
	"strings"
	"testing"
)

// openTag is the start tag of the first element named tag in html ("" when there is none).
func openTag(html, tag string) string {
	return regexp.MustCompile(`<` + tag + `[ >][^>]*>`).FindString(html)
}

// The width scale is the width prefixes' scale: each word caps at the pixels where its prefix starts.
// container caps and centres, and adds no padding; max-width only caps. The class is the attribute as
// written; its rule is in the page's stylesheet.
func TestContainerCapsAndCentresOnTheWidthScale(t *testing.T) {
	scale := []struct{ word, pixels string }{
		{"small", "640px"}, {"medium", "768px"}, {"large", "1024px"}, {"wide", "1280px"}, {"huge", "1536px"},
	}
	for _, w := range scale {
		html, css := renderCSS(t, `<content><section container="`+w.word+`"><text>x</text></section></content>`, nil)
		if got, want := openTag(html, "section"), `<section class="flex-column container-`+w.word+`">`; got != want {
			t.Errorf("container=%q\n got  %s\n want %s", w.word, got, want)
		}
		mustContain(t, css, `.container-`+w.word+`{inline-size:100%;max-inline-size:`+w.pixels+`;margin-inline:auto}`)

		html, css = renderCSS(t, `<content><section max-width="`+w.word+`"><text>x</text></section></content>`, nil)
		if got, want := openTag(html, "section"), `<section class="flex-column max-width-`+w.word+`">`; got != want {
			t.Errorf("max-width=%q (a cap, not centred)\n got  %s\n want %s", w.word, got, want)
		}
		mustContain(t, css, `.max-width-`+w.word+`{max-inline-size:`+w.pixels+`}`)
		mustNotContain(t, css, "margin-inline")
	}

	// padding stays its own attribute: written, it is the only padding there is
	html, css := renderCSS(t, `<content><section container="large" padding="4"><text>x</text></section></content>`, nil)
	if got, want := openTag(html, "section"), `<section class="flex-column container-large padding-4">`; got != want {
		t.Errorf("container with padding\n got  %s\n want %s", got, want)
	}
	mustContain(t, css, `.container-large{inline-size:100%;max-inline-size:1024px;margin-inline:auto}`, `.padding-4{padding:1rem}`)

	// control: without the attributes nothing caps or centres
	html, css = renderCSS(t, `<content><section><text>x</text></section></content>`, nil)
	mustNotContain(t, html, "container-", "max-width-")
	mustNotContain(t, css, "max-inline-size", "margin-inline")
}

// On <page>, container is the width the regions line up on: each region inside gets it, <body> never
// does, so the page's background still spans the screen. A region that writes its own width keeps it.
func TestPageContainerCapsEachRegionNotTheBody(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page container="wide" background="canvas"><header /><content /><footer /><overlay /></page>`,
		"/site/header.ui":  `<header flex="row" padding="4"><text>Quán</text><navbar /></header>`,
		"/site/navbar.ui":  `<navbar flex="row" gap="6"><link value="/">Thực đơn</link></navbar>`,
		"/site/content.ui": `<content padding="6"><text>Thực đơn</text></content>`,
		"/site/footer.ui":  `<footer padding="6"><text>Địa chỉ</text></footer>`,
		"/site/overlay.ui": `<overlay><box component="toast" as="$toast" /></overlay>`,
	}
	const cap = "container-wide"
	html := renderAt(t, memorySite("", files), ".", "/", nil)
	if got, want := openTag(html, "header"), `<header class="flex-row padding-4 `+cap+`">`; got != want {
		t.Errorf("header\n got  %s\n want %s", got, want)
	}
	if got, want := openTag(html, "main"), `<main class="flex-column padding-6 `+cap+`">`; got != want {
		t.Errorf("content\n got  %s\n want %s", got, want)
	}
	if got, want := openTag(html, "footer"), `<footer class="flex-column padding-6 `+cap+`">`; got != want {
		t.Errorf("footer\n got  %s\n want %s", got, want)
	}
	body := openTag(html, "body")
	if !strings.Contains(body, "background-canvas") || strings.Contains(body, "container-") || strings.Contains(body, "max-width-") {
		t.Errorf("body keeps its background and gets no cap: %s", body)
	}
	// a region inside a region sits in a band that is already capped
	if got, want := openTag(html, "nav"), `<nav class="flex-row gap-6">`; got != want {
		t.Errorf("navbar inside the header\n got  %s\n want %s", got, want)
	}
	// what floats over the screen is not a band of the page: the bar, the reader, the overlay
	if strings.Count(html, cap) != 3 {
		t.Errorf("header, content and footer only, got %d caps:\n%s", strings.Count(html, cap), html)
	}
	mustContain(t, html, `data-kitwork-component="progressbar"`, `data-kitwork-component="reader"`) // they are on the page

	// a region written in place in page.ui is a region too; what is not a region keeps the whole width
	inPlace := memorySite("", map[string]string{
		"/site/page.ui":    `<page container="large"><header padding="4"><text>Quản lý</text></header><section background="panel"><text>Khuyến mãi</text></section><content /></page>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	})
	html = renderAt(t, inPlace, ".", "/", nil)
	if got, want := openTag(html, "header"), `<header class="flex-column padding-4 container-large">`; got != want {
		t.Errorf("header written in page.ui\n got  %s\n want %s", got, want)
	}
	if got := openTag(html, "main"); !strings.Contains(got, "container-large") {
		t.Errorf("content of a page with a header in place: %s", got)
	}
	if got, want := openTag(html, "section"), `<section class="flex-column background-panel">`; got != want {
		t.Errorf("a section straight in the page is not a region\n got  %s\n want %s", got, want)
	}

	// a region with its own container or max-width keeps its own, never both caps
	files["/site/content.ui"] = `<content container="medium"><text>Điều khoản</text></content>`
	files["/site/footer.ui"] = `<footer max-width="small"><text>Địa chỉ</text></footer>`
	html = renderAt(t, memorySite("", files), ".", "/", nil)
	if got, want := openTag(html, "main"), `<main class="flex-column container-medium">`; got != want {
		t.Errorf("content with its own container\n got  %s\n want %s", got, want)
	}
	if got, want := openTag(html, "footer"), `<footer class="flex-column max-width-small">`; got != want {
		t.Errorf("footer with its own max-width\n got  %s\n want %s", got, want)
	}

	// control: a page without container caps nothing
	files["/site/page.ui"] = `<page background="canvas"><header /><content /><footer /><overlay /></page>`
	files["/site/content.ui"] = `<content padding="6"><text>Thực đơn</text></content>`
	files["/site/footer.ui"] = `<footer padding="6"><text>Địa chỉ</text></footer>`
	mustNotContain(t, renderAt(t, memorySite("", files), ".", "/", nil), "container-", "max-width-")
}

// Width prefixes work as for every presentation attribute: on an element and, through <page>, on
// each region. A target prefix is decided at build like any other.
func TestContainerWidthPrefixes(t *testing.T) {
	html := render(t, `<content><section container="large" huge:container="wide"><text>x</text></section></content>`, nil)
	if got, want := openTag(html, "section"), `<section class="flex-column container-large huge:container-wide">`; got != want {
		t.Errorf("section\n got  %s\n want %s", got, want)
	}
	html = render(t, `<content><list max-width="small" medium:max-width="large"><item>x</item></list></content>`, nil)
	if got, want := openTag(html, "ul"), `<ul class="flex-column max-width-small medium:max-width-large" role="list">`; got != want {
		t.Errorf("list\n got  %s\n want %s", got, want)
	}

	files := map[string]string{
		"/site/page.ui":    `<page container="large" huge:container="wide"><header><text>Quán</text></header><content /></page>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	}
	html = renderAt(t, memorySite("", files), ".", "/", nil)
	if got, want := openTag(html, "header"), `<header class="flex-column container-large huge:container-wide">`; got != want {
		t.Errorf("header of a page with a prefixed container\n got  %s\n want %s", got, want)
	}
	if body := openTag(html, "body"); strings.Contains(body, "container-") {
		t.Errorf("the prefixed form never reaches body either: %s", body)
	}

	// a target branch: kept for its target, dropped from another build
	target := map[string]string{"/site/content.ui": `<content><section mobile:container="medium"><text>x</text></section></content>`}
	mustContain(t, renderAt(t, memorySite("ios", target), ".", "/", nil), `class="flex-column container-medium"`)
	mustNotContain(t, renderAt(t, memorySite("website", target), ".", "/", nil), "container-")
}

// A word outside the width scale is a compile error at the attribute, naming the scale: a Tailwind
// width (2xl, 7xl), a text size (mini), anything else. The valid words compile (the control).
func TestWidthWordsOutsideTheScaleAreErrors(t *testing.T) {
	const scale = "small (640px), medium (768px), large (1024px), wide (1280px) hoặc huge (1536px)"
	cases := []struct{ source, at, want string }{
		{"<content>\n  <section max-width=\"2xl\">x</section>\n</content>", "content.ui:2:12: ", "max-width nhận một chữ của thang độ rộng (" + scale + ")"},
		{"<content>\n  <section max-width=\"2xl\">x</section>\n</content>", "content.ui:2:12: ", "2xl là chữ của Tailwind, không phải của .ui"},
		{"<content>\n  <section container=\"2xl\">x</section>\n</content>", "content.ui:2:12: ", "container theo thang độ rộng: " + scale + "; 2xl là chữ của Tailwind"},
		{"<content>\n  <box gap=\"2\" container=\"7xl\">x</box>\n</content>", "content.ui:2:16: ", "7xl là chữ của Tailwind"},
		{"<content>\n  <box max-width=\"screen-lg\">x</box>\n</content>", "content.ui:2:8: ", "screen-lg là chữ của Tailwind"},
		{"<content>\n  <box container=\"mini\">x</box>\n</content>", "content.ui:2:8: ", "container theo thang độ rộng: " + scale + "; mini là cỡ chữ, không phải độ rộng"},
		{"<content>\n  <box container=\"rong\">x</box>\n</content>", "content.ui:2:8: ", `container theo thang độ rộng: ` + scale + `; không có "rong"`},
		{"<content>\n  <box container=\"large\" huge:container=\"2xl\">x</box>\n</content>", "content.ui:2:26: ", "2xl là chữ của Tailwind"},
	}
	for _, c := range cases {
		got := compileError(t, map[string]string{"/site/content.ui": c.source})
		if !strings.HasPrefix(got, c.at) {
			t.Errorf("%q\n  error %q does not point at %s", c.source, got, c.at)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%q\n  error %q\n  does not say %q", c.source, got, c.want)
		}
	}

	// on <page>, the width is one fixed word: data cannot move it onto <body>
	got := compileError(t, map[string]string{
		"/site/page.ui":    "<page\n  container=\"{{ width }}\"><content /></page>",
		"/site/content.ui": `<content><text>x</text></content>`,
	})
	if want := "page.ui:2:3: container của <page> là một chữ cố định (" + scale + "), không đổi theo dữ liệu"; got != want {
		t.Errorf("page container from data\n got  %s\n want %s", got, want)
	}

	// control: every word of the scale compiles, with and without a prefix
	html := render(t, `<content><box container="small" medium:container="medium" large:max-width="large" wide:max-width="wide" huge:container="huge">x</box></content>`, nil)
	mustContain(t, html, `class="flex-column container-small medium:container-medium large:max-width-large wide:max-width-wide huge:container-huge"`)
}

// The page's stylesheet has a rule for every class container and max-width write, each under the
// width its prefix starts at: the classes are only as good as the CSS they get on the page.
func TestWidthClassesGetTheirCSS(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page container="large" huge:container="wide"><header><text>Quán</text></header><content /></page>`,
		"/site/content.ui": `<content><section max-width="small"><text>x</text></section></content>`,
	}
	_, css := renderWithCSS(t, memorySite("", files), ".", "/", nil)
	base, wide, _ := strings.Cut(css, "@media (min-width:1536px){")
	for _, rule := range []string{
		`.container-large{inline-size:100%;max-inline-size:1024px;margin-inline:auto}`,
		`.max-width-small{max-inline-size:640px}`,
	} {
		if !strings.Contains(base, rule) {
			t.Errorf("missing %s outside the media queries:\n%s", rule, css)
		}
	}
	if rule := `.huge\:container-wide{inline-size:100%;max-inline-size:1280px;margin-inline:auto}`; !strings.Contains(wide, rule) {
		t.Errorf("missing %s inside @media (min-width:1536px):\n%s", rule, css)
	}
	// control: a page that writes neither gets none of these rules
	_, plain := renderWithCSS(t, memorySite("", map[string]string{"/site/content.ui": `<content><text>x</text></content>`}), ".", "/", nil)
	if strings.Contains(plain, "max-inline-size") || strings.Contains(plain, "margin-inline") {
		t.Errorf("rules for classes the page does not use:\n%s", plain)
	}
}
