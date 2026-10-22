package richtext

import (
	"strings"
	"testing"
)

// keptLinks are addresses as written in an href and what Link keeps of them.
var keptLinks = []struct{ address, want string }{
	{"/", "/"},
	{"/about", "/about"},
	{"/san-pham/ca-phe?size=l#top", "/san-pham/ca-phe?size=l#top"},
	{"/a b", "/a b"},
	{"http://kitwork.io", "http://kitwork.io"},
	{"https://kitwork.io/docs", "https://kitwork.io/docs"},
	{"HTTPS://KITWORK.IO", "HTTPS://KITWORK.IO"},
	{"mailto:hi@kitwork.io", "mailto:hi@kitwork.io"},
	{"tel:+84901234567", "tel:+84901234567"},
	{"  /x", "/x"},
	{"\t/x\n", "/x"},
	{"\x01 \x02 /x", "/x"},
	{"/a&amp;b", "/a&b"},
	{"/a?lang=vi&region=vn&copy=2", "/a?lang=vi&region=vn&copy=2"},
	{"/a?x=&lt;&quot;", `/a?x=<"`},
	{"https:\\\\kitwork.io\\docs", "https://kitwork.io/docs"},
	{"h\nt\ttp://kitwork.io", "http://kitwork.io"},
}

// refusedLinks are addresses Link refuses.
var refusedLinks = []string{
	"//evil.example", "/\\evil.example", "\\\\evil.example", "\\/evil.example", "/\t/evil.example",
	"/\r/evil.example", "/\n/evil.example", "/\x00/evil.example", "/&#9;/evil.example", "/&#92;evil.example",
	"/&bsol;evil.example", "&#47;&#47;evil.example", "#top", "?q=1", "page.html", "./page", "../up",
	"evil.example", "kitwork.io/docs", "javascript:alert(1)", " javascript:alert(1)", "JaVaScRiPt:alert(1)",
	"java\tscript:alert(1)", "java\x00script:alert(1)", "\x01javascript:alert(1)", "jav&#x09;ascript:alert(1)",
	"&#106;avascript:alert(1)", "javascript&colon;alert(1)", "data:text/html,x", "data:image/png;base64,iVBO",
	"vbscript:msgbox(1)", "file:///etc/passwd", "ftp://kitwork.io", "", "   ",
}

// The link rule: an href is decoded once, C0 controls (NUL, TAB, CR, LF…) go wherever they are, spaces
// in front go, a backslash is a slash; then only http:, https:, mailto:, tel: or a path with exactly one
// "/" in front is kept. "/\evil.example", "/<TAB>/evil.example" and "\\evil.example" are "//evil.example",
// another host: refused. NormalizeAddress is the first half, shared with the ui package.
//
// CONTROL: with a backslash no longer read as a slash (NormalizeAddress's '\\' case removed),
// "/\evil.example" was kept as a path on the site and this test failed.
func TestLinkRule(t *testing.T) {
	for _, c := range keptLinks {
		if got, ok := Link(c.address); !ok || got != c.want {
			t.Errorf("Link(%q) = %q, %v; want %q, true", c.address, got, ok, c.want)
		}
	}
	for _, address := range refusedLinks {
		if got, ok := Link(address); ok || got != "" {
			t.Errorf("Link(%q) = %q, %v; want it refused", address, got, ok)
		}
	}
	for _, c := range []struct{ address, want string }{
		{"java\tscript:", "javascript:"},
		{"/\\evil.example", "//evil.example"},
		{"/\t/evil.example", "//evil.example"},
		{"\\\\evil.example", "//evil.example"},
		{"  /x  ", "/x  "},
		{" \x01 /x", "/x"},
		{"/a&amp;b", "/a&amp;b"}, // NormalizeAddress decodes nothing: the ui package hands it data, not HTML
		{"/ok", "/ok"},
	} {
		if got := NormalizeAddress(c.address); got != c.want {
			t.Errorf("NormalizeAddress(%q) = %q; want %q", c.address, got, c.want)
		}
	}
}

// Display writes rel="external noopener" on a link to a web page on another host than the origin's, in
// the pass that writes the grammar: the host compared is the one a browser reads (after the scheme's
// slashes, without the user in front or the port after it, a backslash ending it), in any case; a
// path, an e-mail and a phone number never leave. Without an origin every web link is external. Clean
// writes no rel.
//
// CONTROL: with External() always false, no link carried rel and this test failed.
func TestDisplayMarksExternalLinks(t *testing.T) {
	source := `<a href="https://github.com/kitwork">a</a> <a href="https://KITWORK.io:443/docs">b</a> ` +
		`<a href="https://user@kitwork.io/x">c</a> <a href="/about">d</a> <a href="mailto:hi@kitwork.io">e</a> ` +
		`<a href="https://evil.example\@kitwork.io/">f</a> <a href="https://kitwork.io\@evil.example/">g</a> ` +
		`<a href="http:evil.example">h</a> <a href="http:///kitwork.io">i</a>`
	rel := ` rel="external noopener"`
	want := `<p><a href="https://github.com/kitwork"` + rel + `>a</a> <a href="https://KITWORK.io:443/docs">b</a> ` +
		`<a href="https://user@kitwork.io/x">c</a> <a href="/about">d</a> <a href="mailto:hi@kitwork.io">e</a> ` +
		`<a href="https://evil.example/@kitwork.io/"` + rel + `>f</a> <a href="https://kitwork.io/@evil.example/">g</a> ` +
		`<a href="http:evil.example"` + rel + `>h</a> <a href="http:///kitwork.io">i</a></p>`
	for _, origin := range []string{"https://kitwork.io", "https://kitwork.io/", "kitwork.io", "http://Kitwork.IO:8080"} {
		if got := Display(source, origin); got != want {
			t.Errorf("Display(…, %q)\n got  %s\n want %s", origin, got, want)
		}
	}
	unknown := Display(source, "")
	if n := strings.Count(unknown, rel); n != 7 {
		t.Errorf("without an origin %d links are external, want the 7 web links:\n%s", n, unknown)
	}
	for _, kept := range []string{`<a href="/about">d</a>`, `<a href="mailto:hi@kitwork.io">e</a>`} {
		if !strings.Contains(unknown, kept) {
			t.Errorf("without an origin %s changed:\n%s", kept, unknown)
		}
	}
	if strings.Contains(Clean(source).HTML, "rel=") {
		t.Errorf("Clean wrote rel: %s", Clean(source).HTML)
	}
}
