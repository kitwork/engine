package ui

import (
	"strings"
	"testing"
)

// lazy (15/10/2026, KITWORK_UI §7 "Bỏ qua phần ở xa màn hình: lazy") is one class spelled as written on a
// frame of the page's flow, with a width prefix or from data like any presentation attribute; its rule
// is the stylesheet's (ui/stylesheet/lazy_test.go). A page that writes it on a section far down
// compiles with no warning. CONTROL: a page without lazy carries no lazy class and no
// content-visibility.
func TestLazyClassesInThePage(t *testing.T) {
	html, css := renderCSS(t, `<content><section lazy="true"><text>a</text></section>`+
		`<grid columns="2" lazy="2860px" medium:lazy="1420px" wide:lazy="false"><box><text>b</text></box></grid>`+
		`<box lazy="{{ far }}"><text>c</text></box></content>`, map[string]any{"far": "true"})
	mustContain(t, html,
		`<section class="flex-column lazy-true">`,
		`<div class="grid columns-2 lazy-2860px medium:lazy-1420px wide:lazy-false">`,
		`<div class="flex-column lazy-true">`)
	mustContain(t, css,
		`.lazy-true{content-visibility:auto;contain-intrinsic-inline-size:auto none;contain-intrinsic-block-size:auto 500px}`,
		`@media print{.lazy-true{content-visibility:visible}}`,
		`.lazy-2860px{content-visibility:auto;contain-intrinsic-inline-size:auto none;contain-intrinsic-block-size:auto 2860px}`,
		`.medium\:lazy-1420px{content-visibility:auto;contain-intrinsic-inline-size:auto none;contain-intrinsic-block-size:auto 1420px}`,
		`.wide\:lazy-false{content-visibility:visible;contain-intrinsic-size:none}`,
		`.lazy-false{content-visibility:visible;contain-intrinsic-size:none}`) // the other value data may give
	compileQuiet(t, content(`<content><section lazy="true"><text>a</text></section><grid lazy="2860px" medium:lazy="1420px"><box><text>b</text></box></grid></content>`))
	// control: not written, nothing skips
	html, css = renderCSS(t, `<content><section><text>a</text></section></content>`, nil)
	mustNotContain(t, html, "lazy")
	mustNotContain(t, css, "content-visibility")
}

// lazy on what is always on the screen skips nothing, so the page draws it as written and the author is
// told at the attribute, with where the screen holds it: a frame pinned to an edge (the element itself,
// or a frame around it, in another file: the region's, through a layout piece and a component's own
// .ui), an overlay, a popover, and the tags that are always on it (page, content, overlay, popover). On
// a tag that is no frame (a text, an image) it is told where lazy goes. CONTROL: lazy="false" there,
// lazy beside the pinned header (its sibling, not inside), and a pinned header with no lazy warn of
// nothing.
//
// The widths count (15/10/2026, the review of lazy): it is told only where lazy skips at a width where
// the element is on the screen, citing the pin in effect there (pin="none" large:pin="top" is told of
// large:pin, not of pin="none"). lazy="true" large:lazy="false" beside large:pin="top" skips on a phone,
// where nothing pins it: no warning.
func TestLazyOnScreenIsAWarning(t *testing.T) {
	page := func(header, content string) map[string]string {
		return map[string]string{
			"/site/page.ui":          `<page><header /><content /></page>`,
			"/site/header.ui":        header,
			"/site/content.ui":       content,
			"/site/menu-band.ui":     `<box lazy="true"><text>menu</text></box>`,
			"/site/cart.ui":          `<box><box lazy="400px"><text>giỏ</text></box></box>`,
			"/.components/cart.work": cartLogic,
		}
	}
	plain := `<content><text>a</text></content>`
	cases := []struct {
		name  string
		files map[string]string
		at    string
		says  string
	}{
		{"inside the pinned header", page(`<header pin="top"><box lazy="true"><text>Quán</text></box></header>`, plain),
			"header.ui:1:24", `lazy="true": phần tử này luôn ở trên màn hình (nằm trong khung ghim vào mép màn hình, pin ở header.ui:1:9)`},
		{"pinned from a width", page(`<header pin="none" large:pin="top"><box medium:lazy="300px"><text>Quán</text></box></header>`, plain),
			"header.ui:1:41", `medium:lazy="300px": phần tử này luôn ở trên màn hình (nằm trong khung ghim vào mép màn hình, pin ở header.ui:1:20)`},
		{"pinned below a width, lazy at every width", page(`<header pin="top" large:pin="none"><box lazy="true"><text>Quán</text></box></header>`, plain),
			"header.ui:1:41", `lazy="true": phần tử này luôn ở trên màn hình (nằm trong khung ghim vào mép màn hình, pin ở header.ui:1:9)`},
		{"pinned itself from a width, lazy at every width", page(`<header><text>Quán</text></header>`, `<content><box lazy="400px" large:pin="top" background="paper"><text>giỏ</text></box></content>`),
			"content.ui:1:15", `lazy="400px": phần tử này luôn ở trên màn hình (nó được ghim vào mép màn hình, pin ở content.ui:1:28)`},
		{"pinned itself", page(`<header><text>Quán</text></header>`, `<content><box pin="bottom" lazy="true"><text>giỏ</text></box></content>`),
			"content.ui:1:28", `phần tử này luôn ở trên màn hình (nó được ghim vào mép màn hình, pin ở content.ui:1:15)`},
		{"a layout piece in the pinned header", page(`<header pin="top"><menu-band /></header>`, plain),
			"menu-band.ui:1:6", `(nằm trong khung ghim vào mép màn hình, pin ở header.ui:1:9)`},
		{"a component's own .ui in the pinned header", page(`<header pin="top"><box component="cart" /></header>`, plain),
			"cart.ui:1:11", `(nằm trong khung ghim vào mép màn hình, pin ở header.ui:1:9)`},
		{"inside an overlay", map[string]string{
			"/site/page.ui":    `<page><content /><overlay /></page>`,
			"/site/overlay.ui": `<overlay><box lazy="true"><text>thông báo</text></box></overlay>`,
			"/site/content.ui": plain},
			"overlay.ui:1:15", `(nằm trong <overlay> ở overlay.ui:1:1, lớp nổi trên màn hình)`},
		{"inside a popover", content(`<content><box component="dropdown"><button click="toggle()">m</button><popover><box lazy="true"><text>x</text></box></popover></box></content>`),
			"content.ui:1:85", `(nằm trong <popover> ở content.ui:1:71, nổi trên màn hình khi mở)`},
		{"on the content", content(`<content lazy="true"><text>a</text></content>`),
			"content.ui:1:10", `lazy trên <content>: vùng nội dung luôn có phần ở trên màn hình, trình duyệt không bao giờ bỏ qua nó; đặt lazy trên từng phần ở xa màn hình bên trong (section, grid, card…)`},
		{"on the page", map[string]string{"/site/page.ui": `<page lazy="true"><content /></page>`, "/site/content.ui": plain},
			"page.ui:1:7", `lazy trên <page>: cả trang luôn ở trên màn hình`},
		{"on a text", content(`<content><text lazy="true">a</text></content>`),
			"content.ui:1:16", `lazy bỏ qua một khung ở xa màn hình (section, box, card, grid, list, footer…); <text> không phải khung (trang vẫn vẽ lazy như đã viết)`},
		{"on an image", content(`<content><image value="/a.png" alt="" lazy="true" /></content>`),
			"content.ui:1:39", `ảnh đã tự tải khi tới gần màn hình (loading="lazy")`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			html := compileWarns(t, c.files, c.at, c.says)
			if !strings.Contains(html, "lazy-") {
				t.Errorf("drawn as written: the page lost its lazy class:\n%.600s", html)
			}
		})
	}
	// control: lazy="false" inside the pinned header, lazy on the content beside it, a pinned header with
	// no lazy, the same layout piece and component outside the header: no warning
	for _, files := range []map[string]string{
		page(`<header pin="top"><box lazy="false"><text>Quán</text></box></header>`, `<content><section lazy="true"><text>a</text></section></content>`),
		page(`<header pin="top"><text>Quán</text></header>`, `<content><menu-band /><box component="cart" /><box pin="top" background="paper"><text>b</text></box><box lazy="true"><text>c</text></box></content>`),
		// lazy and pin at widths that never meet (the review of 15/10/2026)
		page(`<header><text>Quán</text></header>`, `<content><box lazy="true" large:lazy="false" large:pin="top" background="paper"><text>giỏ</text></box></content>`),
		page(`<header pin="none" large:pin="top"><box lazy="true" large:lazy="false"><text>Quán</text></box></header>`, plain),
		page(`<header pin="top" large:pin="none"><box lazy="false" large:lazy="true"><text>Quán</text></box></header>`, plain),
	} {
		compileQuiet(t, files)
	}
	// a popover that writes lazy inside the pinned header is told once, of itself: it is on the screen
	// when open, whatever holds it
	s := memorySite("", page(`<header pin="top"><box component="dropdown"><button click="toggle()">m</button><popover lazy="true"><text>x</text></popover></box></header>`, plain))
	s.Reload = true
	_, warned := compileDiagnose(t, s)
	if len(warned) != 1 || !strings.HasPrefix(warned[0], "header.ui:1:89: lazy trên <popover>: khung nổi ở trên màn hình khi mở") {
		t.Errorf("want one warning, of the popover itself, got:\n%s", strings.Join(warned, "\n"))
	}
}

// A lazy frame cuts what is drawn outside it and places a fixed child by itself (content-visibility:auto
// brings paint and layout containment), so what floats out of it breaks: a popover (a dropdown's menu)
// and a part placed outside its parent (place="below-…", "above-…") are cut at its edge, and a toast's
// fixed stack stands where the frame is, off the screen. The author is told once, at the lazy attribute,
// with what it holds and where; the page draws lazy as written (15/10/2026, the review of lazy: none of
// these was told). CONTROL: the same popover, toast and placed part beside the lazy frame, not inside.
func TestLazyAroundWhatFloatsIsAWarning(t *testing.T) {
	dropdown := `<box component="dropdown"><button click="toggle()">m</button><popover><text>x</text></popover></box>`
	cases := []struct {
		name, source, needle, says string
	}{
		{"a popover", `<content><section lazy="true">` + dropdown + `</section></content>`, `lazy="true"`,
			`lazy="true": khung này giữ <popover> ở content.ui:1:`},
		{"a popover in a footer lazy below large", `<content><footer lazy="true" large:lazy="false">` + dropdown + `</footer></content>`, `lazy="true"`,
			`lazy="true": khung này giữ <popover> ở content.ui:1:`},
		{"a toast", `<content><section lazy="600px">` + toastStack(` as="$toast"`) + `</section></content>`, `lazy="600px"`,
			`lazy="600px": khung này giữ <box component="toast"> ở content.ui:1:`},
		{"a part placed below its parent", `<content><section lazy="true"><box><box place="below-start"><text>gợi ý</text></box></box></section></content>`, `lazy="true"`,
			`lazy="true": khung này giữ <box place="below-start"> (đặt ra ngoài cha) ở content.ui:1:`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			at := strings.TrimSuffix(where("content.ui", c.source, c.needle), " ")
			html := compileWarns(t, content(c.source), at, c.says)
			if !strings.Contains(html, "lazy-") {
				t.Errorf("drawn as written: the page lost its lazy class:\n%.600s", html)
			}
		})
	}
	// in a layout piece and in a component's own .ui inside the lazy frame (ui/host.go passes it in)
	for name, files := range map[string]map[string]string{
		"a layout piece": {"/site/content.ui": `<content><section lazy="true"><menu-band /></section></content>`,
			"/site/menu-band.ui": `<box>` + dropdown + `</box>`},
		"a component's own .ui": {"/site/content.ui": `<content><section lazy="true"><box component="cart" /></section></content>`,
			"/site/cart.ui": `<box>` + dropdown + `</box>`, "/.components/cart.work": cartLogic},
	} {
		t.Run(name, func(t *testing.T) {
			compileWarns(t, files, "content.ui:1:19", `lazy="true": khung này giữ <popover> ở `)
		})
	}
	// told once, of the first: two popovers in one lazy section
	s := memorySite("", content(`<content><section lazy="true">`+dropdown+dropdown+`</section></content>`))
	s.Reload = true
	if _, warned := compileDiagnose(t, s); len(warned) != 1 {
		t.Errorf("want one warning, got:\n%s", strings.Join(warned, "\n"))
	}
	// control: beside the lazy frame, not inside
	compileQuiet(t, content(`<content><section lazy="true"><text>a</text></section>`+dropdown+toastStack(` as="$toast"`)+`<box><box place="below-start"><text>gợi ý</text></box></box></content>`))
}
