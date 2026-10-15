package ui

import "testing"

// A link goes to a page, a web address, an e-mail or a phone number; an image comes from the site, the
// web or data:image (KITWORK_UI §7, "Link ra ngoài"). Script URLs never reach the HTML.

func TestScriptLinksWrittenInTheSourceAreErrors(t *testing.T) {
	cases := []struct {
		name, content, at, says string
	}{
		{"javascript", `<content><link value="javascript:alert(1)">x</link></content>`,
			"content.ui:1:16", "link chỉ đi tới đường dẫn trong site"},
		{"hidden behind a tab", "<content><link value=\"java\tscript:alert(1)\">x</link></content>",
			"content.ui:1:16", "link chỉ đi tới"},
		{"data", `<content><link value="data:text/html,hi">x</link></content>`,
			"content.ui:1:16", "link chỉ đi tới"},
		{"image script", `<content><image value="javascript:alert(1)" alt=""></image></content>`,
			"content.ui:1:17", "image chỉ lấy ảnh"},
		{"target is the web's word", `<content><link value="/" target="_blank">x</link></content>`,
			"content.ui:1:26", "new-tab=\"true\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustFail(t, map[string]string{"/site/content.ui": c.content}, c.at, c.says)
		})
	}
}

func TestScriptLinksFromDataAreNotWritten(t *testing.T) {
	for _, url := range []string{"javascript:alert(document.cookie)", " JaVaScRiPt:alert(1)", "java\tscript:alert(1)", "data:text/html,<b>x</b>", "vbscript:msgbox(1)"} {
		html := render(t, `<content><link value="{{ url }}">Website</link><image value="{{ url }}" alt="Avatar"></image></content>`, map[string]any{"url": url})
		mustContain(t, html, `<a>Website</a>`, `<img alt="Avatar" loading`)
		mustNotContain(t, html, "script:", "data:text")
	}
	// control: the addresses a link and an image may hold are written
	for _, url := range []string{"https://kitwork.io/docs", "/san-pham/ca-phe-sua", "#quickstart", "mailto:hi@kitwork.io", "tel:+84901234567"} {
		html := render(t, `<content><link value="{{ url }}">Go</link></content>`, map[string]any{"url": url})
		mustContain(t, html, `href="`+url+`"`)
	}
	html := render(t, `<content><image value="{{ url }}" alt="Dot"></image></content>`, map[string]any{"url": "data:image/png;base64,iVBORw0KGgo="})
	mustContain(t, html, `src="data:image/png;base64,iVBORw0KGgo="`)
}

func TestExternalLinksAreMarked(t *testing.T) {
	files := map[string]string{"/site/content.ui": `<content><link value="https://github.com/kitwork/engine">Source</link>` +
		`<link value="https://kitwork.io/docs">Docs</link><link value="/about">About</link><link value="mailto:hi@kitwork.io">Mail</link></content>`}
	s := memorySite("", files)
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/", Origin: "https://kitwork.io"})
	mustContain(t, html,
		`<a href="https://github.com/kitwork/engine" rel="external noopener">Source</a>`, // another host
		`<a href="https://kitwork.io/docs">Docs</a>`,                                     // the page's own host
		`<a href="/about">About</a>`,
		`<a href="mailto:hi@kitwork.io">Mail</a>`)
}

func TestNewTab(t *testing.T) {
	files := map[string]string{"/site/content.ui": `<content><link value="/report.pdf" new-tab="true">Report</link>` +
		`<link value="https://github.com/kitwork/engine" new-tab="true">Source</link><link value="/about" new-tab="false">About</link></content>`}
	s := memorySite("", files)
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/", Origin: "https://kitwork.io", Language: "en"})
	mustContain(t, html,
		`<a href="/report.pdf" target="_blank" rel="noopener">Report<span`,
		`<a href="https://github.com/kitwork/engine" target="_blank" rel="external noopener">Source<span`,
		`(opens in a new tab)</span></a>`,
		`<a href="/about">About</a>`)
	vi := page.Render(Request{Path: "/", Origin: "https://kitwork.io", Language: "vi"})
	mustContain(t, vi, `(mở trong tab mới)</span></a>`)
}
