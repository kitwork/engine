package ui

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/richtext"
)

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

// A browser reads past a tab, a line break or a NUL in an address and reads a backslash as a slash, so
// "/\evil.example", "/<TAB>/evil.example", "/<CR>/evil.example" and "\\evil.example" all go to the host
// evil.example, and "https://evil.example\@kitwork.io" goes there too. The link helpers read an address
// through richtext.NormalizeAddress: such a link is not a path of the site (a kept link gets no
// data-kitwork-current) and it leaves the site (rel="external noopener").
//
// CONTROL: with internalLink and externalLink reading href as written (their NormalizeAddress line
// removed), "/\evil.example" was marked as the site's own path and this test failed.
func TestLinksABrowserReadsAsAnotherHost(t *testing.T) {
	s := memorySite("", map[string]string{
		"/site/view.ui":    `<view><header /><content /></view>`,
		"/site/header.ui":  `<header><link value="{{ url }}">Đi</link></header>`,
		"/site/content.ui": `<content><link value="{{ url }}">Đi</link></content>`,
	})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"/\\evil.example", "/\t/evil.example", "/\r/evil.example", "/\n/evil.example",
		"/\x00/evil.example", "\\\\evil.example", "\\/evil.example", "https://evil.example\\@kitwork.io/"} {
		if internalLink(url) {
			t.Errorf("internalLink(%q) is true: a browser goes to evil.example", url)
		}
		if !externalLink(url, "https://kitwork.io") {
			t.Errorf("externalLink(%q) is false: a browser goes to evil.example", url)
		}
		html := page.Render(Request{Path: "/", Origin: "https://kitwork.io", Scope: map[string]any{"url": url}})
		if strings.Count(html, `rel="external noopener">Đi</a>`) != 2 || strings.Contains(html, "data-kitwork-current") {
			t.Errorf("%q is written as the site's own link:\n%s", url, html)
		}
	}
	// control: the site's own paths and host stay its own, a backslash in a path is still a path
	for _, url := range []string{"/san-pham", "/a\\b", "https://kitwork.io\\@evil.example/"} {
		if externalLink(url, "https://kitwork.io") {
			t.Errorf("externalLink(%q) is true", url)
		}
		html := page.Render(Request{Path: "/", Origin: "https://kitwork.io", Scope: map[string]any{"url": url}})
		mustNotContain(t, html, "rel=")
	}
	if !internalLink("/a\\b") || !internalLink("/san-pham") {
		t.Errorf("a path of the site is not internal")
	}
}

// The host a link goes to is the one a browser reads after the scheme and its slashes, not after the
// first "//" anywhere in the address. On a page of https://kitwork.io, "http:evil.example/x//kitwork.io"
// goes to evil.example (a scheme other than the page's is followed by a host however few slashes come
// after it), and so does each address below with the "//kitwork.io" in its path, its query or its
// fragment, behind a user or a port, with more slashes or a backslash, in capitals. <link> marks every
// one rel="external noopener", as richtext.Display marks the same address in a post: one rule,
// richtext.External. The site's own addresses, with another host's name in their path, stay its own.
//
// CONTROL: with externalLink reading the host after the first "//" (hostOf as it was), 60 of the 105
// addresses (every one written "http:", "HTTP:", "http:/" or "http:\") were the site's own, written
// without rel, and of the site's own, "http:kitwork.io/x//evil.example" and the host written with its
// port were another host's; this test failed.
func TestExternalLinkReadsTheHostAfterTheScheme(t *testing.T) {
	s := memorySite("", map[string]string{"/site/content.ui": `<content><link value="{{ url }}">Đi</link></content>`})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	const origin = "https://kitwork.io"
	count := 0
	for _, scheme := range []string{"http:", "HTTP:", "http:/", "http:\\", "http://", "HTTPS://", "//"} {
		for _, host := range []string{"evil.example", "user@evil.example", "evil.example:8443"} {
			for _, tail := range []string{"/x//kitwork.io", "?//kitwork.io", "#//kitwork.io", "//kitwork.io", "/a//kitwork.io/b"} {
				url := scheme + host + tail
				count++
				if !externalLink(url, origin) || internalLink(url) {
					t.Errorf("externalLink(%q) = %v, internalLink %v: a browser goes to evil.example", url, externalLink(url, origin), internalLink(url))
					continue
				}
				html := page.Render(Request{Path: "/", Origin: origin, Scope: map[string]any{"url": url}})
				if !strings.Contains(html, `rel="external noopener">Đi</a>`) {
					t.Errorf("%q is written as the site's own link:\n%s", url, html)
				}
				if address, kept := richtext.Link(url); kept && !strings.Contains(richtext.Display(`<a href="`+address+`">x</a>`, origin), "rel=") {
					t.Errorf("richtext.Display writes %q without rel, the ui with it", url)
				}
			}
		}
	}
	if count != 105 {
		t.Fatalf("%d addresses, want 105", count)
	}
	// control: the site's own host, another host's name in the path
	for _, url := range []string{"https://kitwork.io/x//evil.example", "http:kitwork.io/x//evil.example", "https://user@KITWORK.io:443?//evil.example", "/x//evil.example"} {
		if externalLink(url, origin) {
			t.Errorf("externalLink(%q) is true: a browser stays on kitwork.io", url)
		}
		html := page.Render(Request{Path: "/", Origin: origin, Scope: map[string]any{"url": url}})
		mustNotContain(t, html, "rel=")
	}
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
