package ui

import (
	"testing"
)

// pin sticks a frame to the top or bottom edge of the screen while the page scrolls under it
// (KITWORK_UI §7, "pin"): the class is the attribute as written, its rule sticky (never fixed: the
// element keeps its place, nothing is covered), above the content and below every floating layer.
// A width prefix moves it: pin="none" medium:pin="top".
func TestPinClassesAndRules(t *testing.T) {
	html, css := renderCSS(t, `<content><section pin="top" background="paper"><text>a</text></section>`+
		`<box pin="bottom" background="paper"><text>b</text></box>`+
		`<list pin="none" medium:pin="top" background="paper"><item>c</item></list>`+
		`<card pin="top" large:pin="none"><text>d</text></card></content>`, nil)
	mustContain(t, html,
		`<section class="flex-column pin-top background-paper">`,
		`<div class="flex-column pin-bottom background-paper">`,
		`<ul class="flex-column pin-none medium:pin-top background-paper" role="list">`,
		`<div class="card flex-column background-canvas pin-top large:pin-none">`)
	mustContain(t, css,
		`.pin-top{position:sticky;inset-block-start:0;z-index:40}`,
		`.pin-bottom{position:sticky;inset-block-end:0;z-index:40}`,
		`.pin-none{position:revert-layer;inset-block:revert-layer;z-index:revert-layer}`,
		"@media (min-width:768px){\n"+`.medium\:pin-top{position:sticky;inset-block-start:0;z-index:40}`,
		"@media (min-width:1024px){\n"+`.large\:pin-none{position:revert-layer;inset-block:revert-layer;z-index:revert-layer}`)
	// a jump to an #anchor keeps clear of what is pinned, whatever its prefix (reboot.css)
	mustContain(t, rebootCSS(),
		`:where(html:has(.pin-top,[class*=":pin-top"])){scroll-padding-block-start:6rem}`,
		`:where(html:has(.pin-bottom,[class*=":pin-bottom"])){scroll-padding-block-end:6rem}`)
	// control: unwritten, nothing is pinned
	html, css = renderCSS(t, `<content><section><text>a</text></section></content>`, nil)
	mustNotContain(t, html, "pin-")
	mustNotContain(t, css, "sticky")
}

// A pinned element that writes no background takes the page's (<page background>, else canvas): the
// content scrolling under it would show through. The region at the top of the page, written in its own
// file, takes it too. CONTROL: one that writes a background keeps its own, with any prefix; one that
// is not pinned (pin="none" alone) gets none.
func TestPinnedElementTakesThePageBackground(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":    `<page background="paper"><header /><content /></page>`,
		"/site/header.ui":  `<header pin="top" flex="row" padding="4"><text>Quán</text></header>`,
		"/site/content.ui": `<content><box pin="bottom"><text>giỏ</text></box><box pin="top" medium:background="panel"><text>x</text></box><box pin="none"><text>y</text></box></content>`,
	}
	html := renderAt(t, memorySite("", files), ".", "/", nil)
	mustContain(t, html,
		`<header class="background-paper pin-top flex-row padding-4">`,
		`<div class="flex-column background-paper pin-bottom">`,
		`<div class="flex-column pin-top medium:background-panel">`,
		`<div class="flex-column pin-none">`)
	// no page background: the canvas
	files["/site/page.ui"] = `<page><header /><content /></page>`
	html = renderAt(t, memorySite("", files), ".", "/", nil)
	mustContain(t, html, `<header class="background-canvas pin-top flex-row padding-4">`)
	// a page background from data is not known when the page compiles: the canvas too
	files["/site/page.ui"] = `<page background="{{ theme }}"><header /><content /></page>`
	html = renderAt(t, memorySite("", files), ".", "/", map[string]any{"theme": "panel"})
	mustContain(t, html, `<header class="background-canvas pin-top flex-row padding-4">`)
	// control: nothing pinned, no background added
	files["/site/header.ui"] = `<header flex="row" padding="4"><text>Quán</text></header>`
	html = renderAt(t, memorySite("", files), ".", "/", nil)
	mustContain(t, html, `<header class="flex-row padding-4">`)
}

// pin is a word of three: anything else is an error at the attribute, in the author's language,
// naming top, bottom and none, and it never goes with a state (off a frame, or with place, a warning).
func TestPinErrors(t *testing.T) {
	cases := []struct{ name, content, at, says string }{
		{"another word", `<content><box pin="sticky"><text>a</text></box></content>`, "content.ui:1:15", `pin="sticky": pin nhận top, bottom hoặc none`},
		{"an unknown word", `<content><box pin="left"><text>a</text></box></content>`, "content.ui:1:15", `pin nhận top, bottom hoặc none, không có "left"`},
		{"a prefixed word", `<content><box medium:pin="fixed"><text>a</text></box></content>`, "content.ui:1:15", "pin nhận top, bottom hoặc none"},
		{"a state", `<content><box hover:pin="top"><text>a</text></box></content>`, "content.ui:1:15", "pin không đổi theo trạng thái"},
		// on a text, a link or the page, and with place: drawn as written and warned of (diagnostics_compile_test.go)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mustFail(t, map[string]string{"/site/content.ui": c.content}, c.at, c.says)
		})
	}
}
