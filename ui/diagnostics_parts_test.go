package ui

import (
	"strings"
	"testing"
)

// The diagnostics of the calls (docs/KITWORK_UI_DIAGNOSTICS.md, decided 12/10/2026): what host.go,
// layout.go, component.go and resolve.go say. A warning lets the page render as written and says it
// once; a row that stays an error stays one.

// partsCompile compiles a site that must compile and returns the page.
func partsCompile(t *testing.T, files map[string]string, reload bool) *Page {
	t.Helper()
	s := memorySite("", files)
	s.Reload = reload
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return page
}

// partsWarns compiles a site that must compile (one that reloads every page: no reader or progressbar
// of the engine's own), checks it warns exactly once at the place given saying what it must, and
// renders "/" with no data.
func partsWarns(t *testing.T, files map[string]string, at, says string) string {
	t.Helper()
	page := partsCompile(t, files, true)
	found := 0
	for _, w := range page.Warnings() {
		if strings.HasPrefix(w.String(), at) && strings.Contains(w.String(), says) {
			found++
		}
	}
	if found != 1 {
		t.Errorf("want one warning at %q saying %q, got %d among %q", at, says, found, page.Warnings())
	}
	return page.Render(Request{Path: "/"})
}

// partsQuiet compiles a site that must compile with no warning at all, and renders "/" with no data.
func partsQuiet(t *testing.T, files map[string]string) string {
	t.Helper()
	page := partsCompile(t, files, true)
	if list := page.Warnings(); len(list) != 0 {
		t.Errorf("want no warning, got %q", list)
	}
	return page.Render(Request{Path: "/"})
}

// partsSite is a site with a component that has a UI (cart) and a layout (promo), and the content given.
func partsSite(content string) map[string]string {
	return map[string]string{
		"/site/content.ui": content,
		"/site/cart.ui":    `<box><text>giỏ</text></box>`,
		"/site/cart.work":  cartLogic,
		"/site/promo.ui":   `<card><text>khuyến mãi</text></card>`,
	}
}

// Prefix axes written out of their order at a call (host.go:124, :133; layout.go:114, :120) are the
// house's way of writing, as on an element: a warning, and the call reads as if written in order — the
// same classes, and the root that takes a show does not tell it a second time. What is wrong with a
// prefix otherwise stays an error at the call.
func TestPartsPrefixOrderAtACallIsAWarning(t *testing.T) {
	for _, c := range []struct{ written, ordered, class string }{
		{`<content><box component="cart" large+website:show="false" /></content>`, `<content><box component="cart" website+large:show="false" /></content>`, "large:show-false"},
		{`<content><promo large+website:show="false" /></content>`, `<content><promo website+large:show="false" /></content>`, "large:show-false"},
		{`<content><grid columns="2"><box component="cart" large+website:span="2" /></grid></content>`, `<content><grid columns="2"><box component="cart" website+large:span="2" /></grid></content>`, "large:span-2"},
		{`<content><grid columns="2"><promo large+website:place="end" /></grid></content>`, `<content><grid columns="2"><promo website+large:place="end" /></grid></content>`, "large:place-end"},
	} {
		at := where("content.ui", c.written, "large+website:")
		html := partsWarns(t, partsSite(c.written), at, "thứ tự tiền tố là đích → độ rộng → trạng thái (large+website): viết website+large:")
		if page := partsCompile(t, partsSite(c.written), true); len(page.Warnings()) != 1 {
			t.Errorf("%s: the root tells the order a second time: %q", c.written, page.Warnings())
		}
		mustContain(t, html, c.class)
		// control: written in order, the same page and nothing to tell
		if want := partsQuiet(t, partsSite(c.ordered)); html != want {
			t.Errorf("%s renders otherwise than in order:\n got  %s\n want %s", c.written, html, want)
		}
	}
	// a value at a call is read as on an element (classes.go, class): what it warns of there, it warns of
	// at the call — widths joined by a comma
	joined := `<content><grid columns="2"><promo small,large:span="2" /></grid></content>`
	mustContain(t, partsWarns(t, partsSite(joined), where("content.ui", joined, "small,large:"), "small,large:span"), "small:span-2")

	// the rest of a prefix at a call stays an error
	for _, c := range []struct{ content, needle, says string }{
		{`<content><box component="cart" tablet:show="false" /></content>`, "tablet:", "tiền tố không tồn tại: tablet:"},
		{`<content><box component="cart" hover+large:show="false" /></content>`, "hover+large:", "hover+large:show: ẩn hiện theo trạng thái chưa được hỗ trợ"},
		{`<content><promo large,hover:show="false" /></content>`, "large,hover:", "dấu , chỉ nối tiền tố cùng một trục"},
		{`<content><grid columns="2"><promo large+medium:span="2" /></grid></content>`, "large+medium:", "một trục chỉ viết một lần"},
		{`<content><grid columns="2"><box component="cart" hover+large:span="2" /></grid></content>`, "hover+large:", "span ở chỗ gọi nhận tiền tố độ rộng hay đích, không nhận tiền tố trạng thái"},
		{`<content><grid columns="2"><promo span="abc" /></grid></content>`, "span=", `span là số cột thẻ trải qua, số nguyên từ 1 đến 12, không có "abc"`},
	} {
		mustFail(t, partsSite(c.content), where("content.ui", c.content, c.needle), c.says)
	}
}

// as on a layout (layout.go:84) names nothing: a warning, and the layout renders as written. Its value
// is still read: an alias the kernel keeps or one not written as an alias is an error.
func TestPartsAsOnALayoutIsAWarning(t *testing.T) {
	written := `<content><promo as="$promo" /></content>`
	html := partsWarns(t, partsSite(written), where("content.ui", written, "as="),
		"as đặt tên cho một instance; promo là layout (chỉ có giao diện, không có logic) nên không có instance để gọi: bỏ as")
	if want := partsQuiet(t, partsSite(`<content><promo /></content>`)); html != want {
		t.Errorf("as on a layout changed the page:\n got  %s\n want %s", html, want)
	}
	for _, c := range []struct{ content, says string }{
		{`<content><promo as="promo" /></content>`, `as là $ và một tên bắt đầu bằng chữ (as="$account"), không phải "promo"`},
		{`<content><promo as="$el" /></content>`, "$el là tên engine giữ chỗ; đặt alias khác"},
		{`<content><promo as="$promo" /><button click="$promo.open()">x</button></content>`, ""},
	} {
		if c.says == "" { // it names nothing: a read of it is the error of an alias no host sets
			mustFail(t, partsSite(c.content), where("content.ui", c.content, "click="), `$promo chưa có trên trang này (route /)`)
			continue
		}
		mustFail(t, partsSite(c.content), where("content.ui", c.content, "as="), c.says)
	}
}

// A reader as the root of a layout (layout.go:378) renders: a warning, for the reader is written once,
// straight in page.ui. A call that puts a word on the root the reader does not take stays an error:
// the reader would drop it.
func TestPartsReaderRootOfALayoutIsAWarning(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":      `<page><announcer /><content /></page>`,
		"/site/content.ui":   `<content><text>x</text></content>`,
		"/site/announcer.ui": `<reader />`,
	}
	html := partsWarns(t, files, "announcer.ui:1:1: ", "gốc của announcer.ui là <reader>: reader đặt thẳng trong page.ui, một lần")
	mustContain(t, html, `<div class="reader" data-kitwork-component="reader" data-kitwork-bind="text: text" aria-live="polite" aria-atomic="true"></div>`)

	// called inside the content, the reader itself says where it goes (compile.go): one warning, not two
	inside := map[string]string{"/site/content.ui": `<content><announcer /></content>`, "/site/announcer.ui": `<reader />`}
	page := partsCompile(t, inside, true)
	if list := page.Warnings(); len(list) != 1 || !strings.HasPrefix(list[0].String(), "announcer.ui:1:1: ") {
		t.Errorf("want one warning at announcer.ui:1:1, got %q", list)
	}
	mustContain(t, page.Render(Request{Path: "/"}), `data-kitwork-component="reader"`)

	for _, call := range []string{`<announcer if="ready" />`, `<announcer show="false" />`, `<announcer medium:show="false" />`, `<announcer for="x of xs" />`} {
		frame := `<page>` + call + `<content /></page>`
		attr := call[len("<announcer "):strings.Index(call, "=")]
		mustFail(t, map[string]string{"/site/page.ui": frame, "/site/content.ui": files["/site/content.ui"], "/site/announcer.ui": `<reader />`},
			"announcer.ui:1:1: ", "reader không nhận "+attr+" mà chỗ gọi "+strings.TrimSuffix(where("page.ui", frame, attr+"="), ": ")+" đặt lên gốc")
	}
}

// A name a component declares, read inside its host and declared by a level around too (host.go:581,
// :583), is a warning: the nearest wins on the server as in the kernel, so the page renders as
// written — here the dropdown's open (false), not the view's (true).
func TestPartsShadowedNameIsAWarning(t *testing.T) {
	view := `import { router } from "kitwork";
router.view((ctx) => {
  var open = true;
});`
	content := `<content><box component="dropdown"><text show="open">menu</text></box></content>`
	files := map[string]string{"/site/content.ui": content, "/site/router.work": view}
	partsWarns(t, files, where("content.ui", content, "show="),
		"open ở đây là của dropdown, mà view của route / (router.work) cũng khai open: một tên một nghĩa — gọi qua alias ($tên.open) hoặc đổi tên trong view")
	html := partsCompile(t, files, true).Render(Request{Path: "/", Scope: map[string]any{"open": true}})
	mustContain(t, html, `<p data-kitwork-bind="show: open" hidden>menu</p>`)

	// the same in text: the warning stands where the text does
	text := `<content><box component="dropdown"><text>{{ open }}</text></box></content>`
	page := partsCompile(t, map[string]string{"/site/content.ui": text, "/site/router.work": view}, true)
	if at := where("content.ui", text, "{{ open }}"); len(page.Warnings()) != 1 || !strings.HasPrefix(page.Warnings()[0].String(), at+"open ở đây là của dropdown") {
		t.Errorf("want the warning at the text, %s, got %q", at, page.Warnings())
	}
	mustContain(t, page.Render(Request{Path: "/", Scope: map[string]any{"open": true}}), `<p data-kitwork-bind="text: open">false</p>`)

	// a component around: the nearest dropdown's toggle runs
	nested := `<content><box component="dropdown"><box component="dropdown"><button click="toggle()">x</button></box></box></content>`
	html = partsWarns(t, map[string]string{"/site/content.ui": nested}, where("content.ui", nested, "click="),
		"toggle ở đây là của dropdown, mà dropdown bao ngoài cũng khai toggle: một tên một nghĩa — gọi qua alias ($tên.toggle) hoặc đổi tên")
	mustContain(t, html, `<button class="flex-row `+neutralClasses+`" type="button" data-kitwork-event="click: toggle()">x</button>`)
	// control: a name only the page declares tells nothing
	partsQuiet(t, map[string]string{"/site/content.ui": `<content><box component="dropdown"><text>{{ title }}</text></box></content>`, "/site/router.work": view})
}

// as="$el" says the name is the engine's (host.go:485): aliasName refused it first, with the message
// of an alias not written as one.
func TestPartsReservedAliasIsSaid(t *testing.T) {
	for _, alias := range []string{"$el", "$event", "$this"} {
		content := `<content><box component="cart" as="` + alias + `" /></content>`
		mustFail(t, partsSite(content), where("content.ui", content, "as="), alias+" là tên engine giữ chỗ; đặt alias khác")
	}
	content := `<content><box component="cart" as="$1x" /></content>`
	mustFail(t, partsSite(content), where("content.ui", content, "as="), `as là $ và một tên bắt đầu bằng chữ (as="$account"), không phải "$1x"`)
}

// The rows of these files the decision keeps as errors, by the example of each row: presentation and
// events at a call (question 5), children the engine would drop (question 6) and files that never take
// effect (question 9).
func TestPartsRowsThatStayErrors(t *testing.T) {
	cart := func(content string, more ...string) map[string]string {
		files := partsSite(content)
		for i := 0; i+1 < len(more); i += 2 {
			files[more[i]] = more[i+1]
		}
		return files
	}
	for _, c := range []struct {
		row   string
		files map[string]string
		at    string
		says  string
	}{
		{"host.go:338", cart(`<content><box component="cart" click="add()" /></content>`), "content.ui:1:32: ", "sự kiện click đặt trong cart.ui: hành vi của cart nằm ở đó"},
		{"host.go:340", cart(`<content><box component="cart" background="paper" /></content>`), "content.ui:1:32: ", "trình bày của cart nằm trong cart.ui"},
		{"layout.go:127", cart(`<content><promo click="open()" /></content>`), "content.ui:1:17: ", "sự kiện click đặt trong promo.ui: promo là layout"},
		{"layout.go:136", cart(`<content><promo background="paper" /></content>`), "content.ui:1:17: ", "trình bày của promo nằm trong promo.ui"},
		{"host.go:63", cart(`<content><box component="cart"><text>x</text></box></content>`), "content.ui:1:10: ", "cart có giao diện (cart.ui) nên thẻ gọi để rỗng: nội dung con không phải slot"},
		{"layout.go:59", cart(`<content><promo><text>x</text></promo></content>`), "content.ui:1:10: ", "promo là layout nên thẻ gọi để rỗng: nội dung con không phải slot"},
		{"resolve.go:129", cart(`x <content />`), "content.ui:1:1: ", "chữ nằm ngoài thẻ gốc của file"},
		{"layout.go:313", cart(`<content><promo /></content>`, "/site/shop/content.ui", `<content><badge-new /></content>`, "/site/shop/badge-new.ui", `<card><text>x</text></card>`, "/site/shop/badge-new.work", `export default {}`),
			"shop/badge-new.work:1:1: ", "badge-new.work không được đọc"},
		{"layout.go:398", cart(`<content><box><text>x</text></box></content>`, "/site/box.ui", `<box />`), "box.ui:1:1: ", "box.ui đặt tên trùng thẻ lõi <box> của Kitwork"},
		{"component.go:325", cart(`<content><text>x</text></content>`, "/site/page.ui", `<page><box component="progressbar" /><content /></page>`, "/site/progressbar.ui", `<box><text>x</text></box>`),
			"progressbar.ui:1:1: ", "progressbar.ui đổi giao diện progressbar của Kitwork"},
		{"component.go:350", cart(`<content><promo /></content>`, "/site/shop/content.ui", `<content><text>x</text></content>`, "/site/shop/page.ui", `<page><box component="progressbar" /><content /></page>`,
			"/site/shop/progressbar.ui", `<box><text>x</text></box>`), "shop/progressbar.ui:1:1: ", "progressbar.ui đổi giao diện progressbar của Kitwork"},
	} {
		route := "/site"
		if _, ok := c.files["/site/shop/content.ui"]; ok {
			route = "/site/shop"
		}
		_, err := memorySite("", c.files).Compile(route)
		if err == nil || !strings.HasPrefix(err.Error(), c.at) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s stays an error: want one at %q saying %q, got %v", c.row, c.at, c.says, err)
		}
	}
}
