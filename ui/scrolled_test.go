package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// scrolled: is a state prefix like hover: (KITWORK_UI §8), written on any presentation attribute, the
// class the attribute as written: a header that floats up once the page scrolls under it. It goes
// with widths the way states do (medium+scrolled:). The element's transition lists exactly what its
// scrolled: classes change. A pinned header that writes its background only for the scrolled state
// takes no default background (KITWORK_UI §7, pin): transparent at the top, paper once scrolled.
func TestScrolledPrefix(t *testing.T) {
	files := map[string]string{
		"/site/page.ui": `<page><header /><content /></page>`,
		"/site/header.ui": `<header pin="top" scrolled:shadow="small" scrolled:background="paper" medium+scrolled:padding-y="2" flex="row" padding="4">` +
			`<link value="/" scrolled:color="ink" hover:color="brand+15">Kitwork</link></header>`,
		"/site/content.ui": `<content><text>a</text></content>`,
	}
	s := memorySite("", files)
	s.Reload = true
	html, css := renderWithCSS(t, s, ".", "/", nil)
	mustContain(t, html,
		`<header class="pin-top scrolled:shadow-small scrolled:background-paper medium+scrolled:padding-y-2 flex-row padding-4">`,
		`<a class="scrolled:color-ink hover:color-brand+15" href="/"`)
	mustContain(t, css,
		`.scrolled\:shadow-small:where(:root[data-kitwork-scrolled] *){box-shadow:0 1px 2px 0 rgb(0 0 0/5%)}`,
		`.scrolled\:background-paper:where(:root[data-kitwork-scrolled] *){background-color:var(--paper)}`,
		"@media (min-width:768px){\n"+`.medium\+scrolled\:padding-y-2:where(:root[data-kitwork-scrolled] *){padding-block:.5rem}`,
		`.scrolled\:color-ink:where(:root[data-kitwork-scrolled] *){color:var(--ink)}`,
		// the header animates its three, the link its colour; nothing else moves
		`.medium\+scrolled\:padding-y-2:where(.scrolled\:background-paper.scrolled\:shadow-small){transition:padding-block .15s,background-color .15s,box-shadow .15s}`,
		`.scrolled\:color-ink{transition:color .15s}`)
	mustNotContain(t, css, "transition:all", "padding .15s", "position .15s")
	// hover still wins over scrolled on the link
	if strings.Index(css, `.scrolled\:color-ink:where(`) > strings.Index(css, `.hover\:color-brand\+15:where(`) {
		t.Errorf("hover comes after scrolled in page.css:\n%s", css)
	}
	page := compiled(t, s)
	if !page.UsesScrolled() {
		t.Error("a page whose header carries scrolled: needs the scrolled script")
	}
	// control: the same header without scrolled: needs none, and keeps the default background of a pin
	files["/site/header.ui"] = `<header pin="top" flex="row" padding="4"><link value="/" hover:color="brand+15">Kitwork</link></header>`
	html, css = renderWithCSS(t, s, ".", "/", nil)
	mustContain(t, html, `<header class="background-canvas pin-top flex-row padding-4">`)
	mustNotContain(t, css, "data-kitwork-scrolled", "transition")
	if compiled(t, s).UsesScrolled() {
		t.Error("control: a page without scrolled: needs no scrolled script")
	}
}

// A page can carry a scrolled: class when any branch of it can: inside an if that is false for this
// request, inside a for, inside a layout piece called by its tag. Each makes the page ask for the script.
func TestScrolledInAnyBranchAsksForTheScript(t *testing.T) {
	for name, content := range map[string]string{
		"if":           `<content><box if="later" scrolled:shadow="small"><text>a</text></box></content>`,
		"for":          `<content><list><item for="x of items" scrolled:color="ink">{{ x }}</item></list></content>`,
		"layout piece": `<content><top-bar /></content>`,
	} {
		t.Run(name, func(t *testing.T) {
			s := memorySite("", map[string]string{
				"/site/content.ui": content,
				"/site/top-bar.ui": `<box scrolled:background="paper"><text>b</text></box>`,
			})
			s.Reload = true
			if !compiled(t, s).UsesScrolled() {
				t.Errorf("%s: the page can carry a scrolled: class", name)
			}
		})
	}
}

// Without a head built by the engine (a page shown on its own), the document carries what it needs
// inline: its stylesheets and, for a page with a scrolled: class, the script. With the engine's head
// it carries neither: the head links them. CONTROL: a page without scrolled: has no script.
func TestDocumentWithoutAHeadCarriesTheScrolledScript(t *testing.T) {
	script := string(stylesheet.ScrolledScript().JavaScript)
	s := memorySite("", map[string]string{"/site/content.ui": `<content><box scrolled:shadow="small"><text>a</text></box></content>`})
	s.Reload = true
	page := compiled(t, s)
	mustContain(t, page.Document(Request{Path: "/"}), "<script>\n"+script+"</script>\n</head>")
	if strings.Contains(page.Document(Request{Path: "/", Head: "<!-- built -->\n"}), "data-kitwork-scrolled") {
		t.Error("with the engine's head the document inlines no script")
	}
	s = memorySite("", map[string]string{"/site/content.ui": `<content><box shadow="small"><text>a</text></box></content>`})
	s.Reload = true
	if strings.Contains(compiled(t, s).Document(Request{Path: "/"}), "<script") {
		t.Error("control: a page without scrolled: has no script")
	}
}

// What is wrong with scrolled: is said at the attribute, in the author's language: a state does not
// move pin or show; widths come before states; one state axis per attribute; scroll: (the scroll
// event) where scrolled: was meant.
func TestScrolledErrors(t *testing.T) {
	cases := []struct{ name, content, at, says string }{
		{"pin", `<content><box scrolled:pin="top"><text>a</text></box></content>`, "content.ui:1:15", "pin không đổi theo trạng thái"},
		{"show", `<content><box scrolled:show="false"><text>a</text></box></content>`, "content.ui:1:15", "ẩn hiện theo trạng thái chưa được hỗ trợ"},
		{"two states", `<content><box scrolled+hover:shadow="small"><text>a</text></box></content>`, "content.ui:1:15", "một trục chỉ viết một lần"},
		{"a value from data", `<content><box scrolled:shadow="{{ x }}"><text>a</text></box></content>`, "content.ui:1:15", "chưa cho tiền tố đi cùng {{ }}"},
		{"the scroll event", `<content><box scroll:shadow="small"><text>a</text></box></content>`, "content.ui:1:15", `scroll: là sự kiện cuộn; đổi trình bày khi trang đã cuộn là tiền tố trạng thái scrolled: (scrolled:shadow="small")`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustFail(t, map[string]string{"/site/content.ui": c.content}, c.at, c.says)
		})
	}
	// control: the scroll event with its own words, and the scroll area, still compile
	for _, content := range []string{
		`<content><box scroll="y"><text>a</text></box></content>`,
		`<content><box component="dropdown" scroll="close()"><text>a</text></box></content>`,
	} {
		if _, err := memorySite("", map[string]string{"/site/content.ui": content}).Compile("/site"); err != nil {
			t.Errorf("control %s: %v", content, err)
		}
	}
}

func compiled(t *testing.T, s *Site) *Page {
	t.Helper()
	page, err := s.Compile(filepath.Join(s.Root, "."))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return page
}
