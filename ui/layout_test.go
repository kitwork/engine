package ui

import (
	"strings"
	"testing"
)

// A layout piece is markup with no logic, called by its tag (KITWORK_UI §13): no state, no instance,
// no scope boundary. These tests hold the layout path to what the spec says and every error an author
// can meet on it to its message, at the file, line and column to look at.

// productCard is the example's product card, cut to what these tests read.
const productCard = `<card flex="column" gap="3"><text heading="3">{{ product.name }}</text><text if="product.oldPrice">{{ product.oldPrice }}</text></card>`

func products() []any {
	return []any{
		map[string]any{"id": "ca-phe-sua", "name": "Cà phê sữa", "oldPrice": 35000.0},
		map[string]any{"id": "bac-xiu", "name": "Bạc xỉu", "oldPrice": nil},
	}
}

// A for at the call repeats the file's one root, which is the element itself: no wrapper, the call's
// placement on it, one row per item, read where the call stands.
func TestLayoutCallRepeatsItsRoot(t *testing.T) {
	files := map[string]string{
		"/site/content.ui":      `<content><grid columns="2"><product-card for="product of products; key: product.id" span="2" /></grid></content>`,
		"/site/product-card.ui": productCard,
	}
	html := renderAt(t, memorySite("", files), ".", "/", map[string]any{"products": products()})
	card := `<div class="card flex-column gap-3 span-2"><h3>`
	mustContain(t, html, `<div class="grid columns-2">`+card+`Cà phê sữa</h3><p>35000</p></div>`+card+`Bạc xỉu</h3></div></div>`)
	// control: no item, no card
	html = renderAt(t, memorySite("", files), ".", "/", map[string]any{"products": []any{}})
	mustContain(t, html, `<div class="grid columns-2"></div>`)
	mustNotContain(t, html, `class="card`)
}

// The call is the file written in place: the same markup pasted where the call stands renders the same
// HTML, inside a component the browser runs too — the layout reads that component's names and the
// page's data as the place does, its events are the component's, and it marks nothing for the kernel.
func TestLayoutReadsTheContextOfItsCall(t *testing.T) {
	button := `<button click="toggle()" expanded="{{ open }}"><text>{{ user.name }}</text></button>`
	scope := map[string]any{"user": map[string]any{"name": "Quốc"}, "open": true}
	called := renderAt(t, memorySite("", map[string]string{
		"/site/content.ui":        `<content><box component="dropdown" as="$account"><account-button /></box></content>`,
		"/site/account-button.ui": button,
	}), ".", "/", scope)
	inPlace := renderAt(t, memorySite("", map[string]string{
		"/site/content.ui": `<content><box component="dropdown" as="$account">` + button + `</box></content>`,
	}), ".", "/", scope)
	if called != inPlace {
		t.Errorf("a layout call renders what its file renders in place:\n call     %s\n in place %s", called, inPlace)
	}
	// the dropdown's names reach inside the layout (its event, its open, false where the page's is true),
	// the page's user too, and the layout itself marks nothing
	mustContain(t, called, `data-kitwork-bind="aria-expanded: open" data-kitwork-event="click: toggle()"`, `aria-expanded="false"`, "<span>Quốc</span>")
	if hosts := strings.Count(called, "data-kitwork-component="); hosts != 3 { // dropdown, progressbar, reader
		t.Errorf("only the dropdown, the progressbar and the reader are hosts, got %d:\n%s", hosts, called)
	}
	mustNotContain(t, called, "account-button", "data-kitwork-scope")

	// inside a component's .ui the layout reads the component's names: note is cart's, not the page's
	html := renderAt(t, memorySite("", map[string]string{
		"/site/content.ui":   `<content><box component="cart" /></content>`,
		"/site/cart.ui":      `<box><cart-note /></box>`,
		"/site/cart-note.ui": `<text>{{ note }}</text>`,
		"/site/cart.work":    cartLogic,
	}), ".", "/", map[string]any{"note": "của trang"})
	mustContain(t, html, "<p>chưa có</p>")
	mustNotContain(t, html, "của trang")
}

// What the call passes is a plain attribute, read where the call stands (a hyphenated name is the
// camelCase name the file reads). Anything else is an error: presentation and behaviour are the file's,
// it has no content slot, and an attribute the file does not read goes nowhere.
func TestLayoutInputs(t *testing.T) {
	files := func(content string) map[string]string {
		return map[string]string{"/site/content.ui": content, "/site/product-card.ui": `<card><text>{{ product.name }} {{ oldPrice ?? '' }}</text></card>`}
	}
	html := renderAt(t, memorySite("", files(`<content><product-card product="{{ featured }}" old-price="{{ featured.was }}" /></content>`)), ".", "/",
		map[string]any{"featured": map[string]any{"name": "Bạc xỉu", "was": 35000.0}, "product": map[string]any{"name": "của trang"}})
	mustContain(t, html, "<p>Bạc xỉu 35000</p>")
	mustNotContain(t, html, "của trang")

	for _, c := range []struct{ content, needle, says string }{
		{`<content><product-card price="{{ 1 }}" /></content>`, "price=", "product-card.ui không đọc price; chỗ gọi nhận if, show, for, span, place, source và dữ liệu file đọc"},
		{`<content><product-card padding="2" /></content>`, "padding=", "trình bày của product-card nằm trong product-card.ui"},
		{`<content><product-card hover:product="{{ x }}" /></content>`, "hover:product=", "trình bày của product-card nằm trong product-card.ui"},
		{`<content><product-card click="go()" /></content>`, "click=", "sự kiện click đặt trong product-card.ui: product-card là layout, hành vi nằm trong file của nó"},
		{`<content><product-card><text>x</text></product-card></content>`, "<product-card", "product-card là layout nên thẻ gọi để rỗng: nội dung con không phải slot; nội dung nằm trong product-card.ui"},
		{`<content><product-card component="cart" /></content>`, "component=", `<product-card> là layout, không là thẻ chủ của component: component="cart" đặt trên một thẻ của Kitwork`},
		{`<content><product-card large:if="x" /></content>`, "large:if=", "if không nhận tiền tố"},
		{`<content><product-card product /></content>`, "product ", "product cần một giá trị"},
		// a show with a prefix moves onto the root of another file: what is wrong with it is said here
		{`<content><product-card hover:show="false" /></content>`, "hover:show=", "hover:show: ẩn hiện theo trạng thái chưa được hỗ trợ"},
		{`<content><product-card medium:show="visible" /></content>`, "medium:show=", `bản đầu chưa cho tiền tố đi cùng biểu thức; medium:show chỉ nhận "true" hoặc "false"`},
		{`<content><product-card tablet:show="true" /></content>`, "tablet:show=", "tiền tố không tồn tại: tablet"},
		// an input's value is read where the call stands: an alias there must be set on the page
		{`<content><product-card product="{{ $basket.items }}" /></content>`, "product=", `$basket chưa có trên trang này (route /): không thẻ nào trong tầm đặt as="$basket"`},
	} {
		mustFail(t, files(c.content), where("content.ui", c.content, c.needle), c.says)
	}
}

// if and show at the call read where the call stands, before the inputs go in: whether the root is
// there, and whether it shows (the hidden attribute, like any show). The root's own if reads the
// inputs. A show written as true or false, with width prefixes, is classes on the root.
func TestLayoutIfAndShowAtTheCall(t *testing.T) {
	files := map[string]string{
		"/site/content.ui":   `<content><badge-new if="product.listed" product="{{ other }}" /><badge-new show="visible" product="{{ other }}" /><badge-new show="false" medium:show="true" product="{{ other }}" /></content>`,
		"/site/badge-new.ui": `<box flex="row" if="!product.hidden"><text>{{ product.name }}</text></box>`,
	}
	scope := func(listed, visible, hidden bool) map[string]any {
		return map[string]any{"product": map[string]any{"listed": listed}, "visible": visible,
			"other": map[string]any{"name": "Mới", "listed": !listed, "hidden": hidden}}
	}
	html := renderAt(t, memorySite("", files), ".", "/", scope(true, false, false))
	if got := strings.Count(html, "<p>Mới</p>"); got != 3 {
		t.Errorf("the page's product is listed: three badges, got %d:\n%s", got, html)
	}
	mustContain(t, html, `<div class="flex-row" hidden><p>Mới</p></div>`, `<div class="flex-row show-false medium:show-true"><p>Mới</p></div>`)
	// control: the page's product unlisted drops the first; visible shows the second
	html = renderAt(t, memorySite("", files), ".", "/", scope(false, true, false))
	if got := strings.Count(html, "<p>Mới</p>"); got != 2 {
		t.Errorf("the page's product is not listed: two badges, got %d:\n%s", got, html)
	}
	mustNotContain(t, html, `<div class="flex flex-row" hidden>`)
	// the root's own if reads the input
	html = renderAt(t, memorySite("", files), ".", "/", scope(true, true, true))
	mustNotContain(t, html, "Mới")
}

// A layout piece has no logic, so component= is not how it is called: the error says to call it by its
// tag (with source= when the value pinned a place). A site file named like a Kitwork component keeps
// its meaning: <dropdown /> is the site's layout, and Kitwork's dropdown is reached by its path.
func TestLayoutWrittenAsAComponent(t *testing.T) {
	card := map[string]string{"/site/product-card.ui": productCard}
	with := func(content string, more map[string]string) map[string]string {
		files := map[string]string{"/site/content.ui": content}
		for k, v := range card {
			files[k] = v
		}
		for k, v := range more {
			files[k] = v
		}
		return files
	}
	for _, c := range []struct {
		content string
		more    map[string]string
		says    string
	}{
		{`<content><card component="product-card" for="product of products" /></content>`, nil, "product-card là layout (chỉ có giao diện, không có logic): viết <product-card … />"},
		{`<content><card component="product-card" /></content>`, nil, "product-card là layout (chỉ có giao diện, không có logic): viết <product-card />"},
		{`<content><card component="./product-card" /></content>`, nil, `product-card là layout (chỉ có giao diện, không có logic): viết <product-card source="./product-card" />`},
		{`<content><box component="dropdown"><text>x</text></box></content>`, map[string]string{"/site/dropdown.ui": `<box><text>menu</text></box>`},
			`dropdown là layout (chỉ có giao diện, không có logic): viết <dropdown />; dropdown.ui ở đây là file của site, còn component dropdown của Kitwork viết component="kitwork/components/dropdown"`},
	} {
		mustFail(t, with(c.content, c.more), where("content.ui", c.content, "component="), c.says)
	}
	// a component Kitwork has not built yet is not offered by its path
	got := compileError(t, with(`<content><box component="tabs"><text>x</text></box></content>`, map[string]string{"/site/tabs.ui": `<box><text>x</text></box>`}))
	if !strings.Contains(got, "tabs là layout (chỉ có giao diện, không có logic): viết <tabs />") || strings.Contains(got, "kitwork/components/tabs") {
		t.Errorf("a site tabs.ui written component=: %s", got)
	}
	// a branch's file too: <dropdown /> there is the branch's layout
	mustContain(t, renderAt(t, memorySite("", map[string]string{
		"/site/quan-ly/content.ui":  `<content><dropdown /></content>`,
		"/site/quan-ly/dropdown.ui": `<box><text>menu quản lý</text></box>`,
	}), "quan-ly", "/quan-ly", nil), "<p>menu quản lý</p>")
	// controls: the tag calls the layout, and Kitwork's dropdown is still there by its path
	mustContain(t, mustCompile(t, with(`<content><dropdown /><box component="kitwork/components/dropdown"><button click="toggle()">x</button></box></content>`,
		map[string]string{"/site/dropdown.ui": `<box><text>menu</text></box>`})),
		`<div class="flex-column"><p>menu</p></div>`, `data-kitwork-component="dropdown"`)
}

// A layout that gains logic is a component: every call written as its tag says how to write it, with
// the root tag of its .ui. Logic beside a branch's .ui is read by nothing, which is an error, not a
// layout that silently stays one.
func TestLayoutThatGainsLogic(t *testing.T) {
	logic := `import { component } from "kitwork"; component.define("product-card", () => { var open = false; });`
	content := `<content><grid><product-card for="product of products" /></grid></content>`
	mustFail(t, map[string]string{"/site/content.ui": content, "/site/product-card.ui": productCard, "/site/product-card.work": logic},
		where("content.ui", content, "<product-card"), `product-card có logic (product-card.work): viết <card component="product-card" … />`)

	branch := map[string]string{
		"/site/quan-ly/content.ui":       `<content><order-table /></content>`,
		"/site/quan-ly/order-table.ui":   `<list><item><text>đơn</text></item></list>`,
		"/site/quan-ly/order-table.work": `import { component } from "kitwork"; component.define("order-table", () => { var open = false; });`,
	}
	_, err := memorySite("", branch).Compile("/site/quan-ly")
	if err == nil || !strings.HasPrefix(err.Error(), "quan-ly/order-table.work:1:1: ") ||
		!strings.Contains(err.Error(), "order-table.work không được đọc: logic của component đặt ở gốc site (components.work hoặc order-table.work) hoặc .components/order-table.work của identity; ở đây quan-ly/order-table.ui là layout") {
		t.Errorf("a .work beside a branch's layout: %v", err)
	}
	// the same written component=: the branch's .work is read by nothing there either
	branch["/site/quan-ly/content.ui"] = `<content><list component="order-table" /></content>`
	if _, err := memorySite("", branch).Compile("/site/quan-ly"); err == nil || !strings.HasPrefix(err.Error(), "quan-ly/order-table.work:1:1: ") {
		t.Errorf("a .work beside a branch's .ui, written component=: %v", err)
	}
	// control: without it the branch's layout is called by its tag
	branch["/site/quan-ly/content.ui"] = `<content><order-table /></content>`
	delete(branch, "/site/quan-ly/order-table.work")
	mustContain(t, renderAt(t, memorySite("", branch), "quan-ly", "/quan-ly", nil), "<p>đơn</p>")
}

// A tag of Kitwork always means Kitwork's: a .ui file in reach named like a core tag or a Kitwork
// element would never be called, so it is an error at that file. Region files are the regions'.
func TestLayoutNamedLikeATag(t *testing.T) {
	for _, c := range []struct {
		files map[string]string
		at    string
		says  string
	}{
		{map[string]string{"/site/content.ui": `<content><card><text>x</text></card></content>`, "/site/card.ui": `<box><text>x</text></box>`},
			"card.ui:1:1: ", "card.ui đặt tên trùng thẻ lõi <card> của Kitwork: <card> luôn là thẻ lõi của Kitwork nên file này không bao giờ được gọi"},
		{map[string]string{"/site/shop/content.ui": `<content><list><item><text>x</text></item></list></content>`, "/site/list.ui": `<box><text>x</text></box>`},
			"list.ui:1:1: ", "list.ui đặt tên trùng thẻ lõi <list> của Kitwork"},
		{map[string]string{"/site/content.ui": `<content><icon value="x" alt="" /></content>`, "/.components/icon.ui": `<box><text>x</text></box>`},
			"../.components/icon.ui:1:1: ", "icon.ui đặt tên trùng phần tử <icon> của Kitwork: <icon> luôn là phần tử của Kitwork"},
		{map[string]string{"/site/content.ui": `<content><dialog /></content>`, "/site/dialog.ui": `<box><text>x</text></box>`},
			"dialog.ui:1:1: ", "dialog.ui đặt tên trùng phần tử <dialog> của Kitwork"},
	} {
		route := "/site"
		if _, ok := c.files["/site/shop/content.ui"]; ok {
			route = "/site/shop"
		}
		_, err := memorySite("", c.files).Compile(route)
		if err == nil || !strings.HasPrefix(err.Error(), c.at) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("want an error at %q saying %q,\n got %v", c.at, c.says, err)
		}
	}
	// control: the region files and a layout whose root is a card
	mustCompile(t, map[string]string{
		"/site/page.ui":         `<page><header /><content /></page>`,
		"/site/header.ui":       `<header><text>Quán</text></header>`,
		"/site/content.ui":      `<content><product-card product="{{ x }}" /></content>`,
		"/site/product-card.ui": productCard,
	})
}

// The root of a layout piece takes the call's for, if, show, span and place: it is an element of
// Kitwork that takes them, and it does not repeat itself.
func TestLayoutRoot(t *testing.T) {
	for _, c := range []struct{ ui, needle, says string }{
		{`<card for="x of xs"><text>x</text></card>`, "for=", `gốc của promo.ui không mang for: lặp mấy lần là việc của chỗ gọi (<promo for="…" />)`},
		{`<box component="dropdown"><button click="toggle()">x</button></box>`, "component=", `gốc của promo.ui mang component="dropdown": chỗ gọi <promo /> đặt for, if, show, span, place lên chính gốc này`},
		{`<page><content /></page>`, "<page", "gốc của promo.ui là <page>: page là khung của trang"},
		{`<navbar><link value="/">x</link></navbar>`, "<navbar", "gốc của promo.ui là vùng <navbar>: vùng gọi bằng thẻ của nó"},
		{`<fragment if="x"><text>x</text></fragment>`, "<fragment", "gốc của promo.ui là <fragment>, không dựng phần tử nào"},
		{`<promo-line />`, "<promo-line", "gốc của promo.ui là <promo-line>, không phải thẻ của Kitwork"},
	} {
		mustFail(t, map[string]string{"/site/content.ui": `<content><promo /></content>`, "/site/promo.ui": c.ui, "/site/promo-line.ui": `<box><text>x</text></box>`},
			where("promo.ui", c.ui, c.needle), c.says)
	}
	// control: a layout piece may hold a component and a layout piece inside its root
	mustContain(t, mustCompile(t, map[string]string{"/site/content.ui": `<content><promo /></content>`,
		"/site/promo.ui":      `<box><box component="dropdown"><button click="toggle()">x</button></box><promo-line /></box>`,
		"/site/promo-line.ui": `<text>dòng</text>`}),
		`data-kitwork-component="dropdown"`, "<p>dòng</p>")
}

// A layout piece is found from the folder of the file that writes the call, never from the route: what
// header.ui writes means the same on every page, a branch's own file is found from that branch, then the
// identity's .components/. Nearer wins and a page may hold two pieces of one name: they are built on
// the server and never named on the wire.
func TestLayoutLookupFromTheWritingFolder(t *testing.T) {
	files := map[string]string{
		"/site/page.ui":             `<page><header /><content /></page>`,
		"/site/header.ui":           `<header><mini-cart /></header>`,
		"/site/mini-cart.ui":        `<box><text>giỏ của site</text></box>`,
		"/site/shop/mini-cart.ui":   `<box><text>giỏ của shop</text></box>`,
		"/site/shop/content.ui":     `<content><mini-cart /><badge-new /></content>`,
		"/site/content.ui":          `<content><text>x</text></content>`,
		"/.components/badge-new.ui": `<badge><text>mới</text><badge-dot /></badge>`,
		"/.components/badge-dot.ui": `<text>•</text>`,
		"/site/badge-dot.ui":        `<text>chấm của site</text>`,
	}
	s := memorySite("", files)
	s.Reload = true
	html := renderAt(t, s, "shop", "/shop", nil)
	header, content := html[:strings.Index(html, "<main")], html[strings.Index(html, "<main"):]
	mustContain(t, header, "giỏ của site")
	mustContain(t, content, "giỏ của shop", "<span>mới</span><span>•</span>")
	mustNotContain(t, html, "chấm của site")
	// control: from the site root, the shop's file is out of reach
	files["/site/content.ui"] = `<content><mini-cart /></content>`
	mustNotContain(t, renderAt(t, memorySite("", files), ".", "/", nil), "giỏ của shop")
}

// source takes another file for the call: a name looked up like the tag's own, or a path. What it takes
// is a layout piece.
func TestLayoutSource(t *testing.T) {
	files := func(content string) map[string]string {
		return map[string]string{
			"/site/content.ui":            content,
			"/site/product-card.ui":       productCard,
			"/site/product-card-small.ui": `<box><text>{{ product.name }}</text></box>`,
			"/site/shop/compact.ui":       `<box><text>gọn {{ product.name }}</text></box>`,
			"/site/cart.ui":               `<box><text>giỏ</text></box>`,
			"/site/cart.work":             cartLogic,
		}
	}
	scope := map[string]any{"product": map[string]any{"name": "Bạc xỉu"}}
	html := renderAt(t, memorySite("", files(`<content><product-card source="product-card-small" /><product-card source="./shop/compact" /></content>`)), ".", "/", scope)
	mustContain(t, html, `<div class="flex-column"><p>Bạc xỉu</p></div>`, "<p>gọn Bạc xỉu</p>")
	mustNotContain(t, html, `class="card`)
	for _, c := range []struct{ content, says string }{
		{`<content><product-card source="cart" /></content>`, `source lấy một layout (chỉ có giao diện); cart có logic (cart.work) nên là component: viết component="cart" trên một thẻ`},
		{`<content><product-card source="nothing" /></content>`, `không tìm thấy layout nothing (source="nothing")`},
		{`<content><product-card source="{{ kind }}" /></content>`, "source là một tên cố định, không có {{ }}"},
		{`<content><product-card source="Small" /></content>`, `source là tên của một layout (viết thường, nối bằng gạch ngang) hoặc đường dẫn tới nó, không phải "Small"`},
	} {
		mustFail(t, files(c.content), where("content.ui", c.content, "source="), c.says)
	}
}

// Inside a component the browser runs, a layout call's for and if are the server's alone (the kernel
// repeats and removes nothing by live state yet), and an input read from live state would be printed
// once with no instance to keep it: errors. Its show follows the live state.
func TestLayoutCallInsideALiveComponent(t *testing.T) {
	note := map[string]string{"/site/note.ui": `<card><text>{{ label ?? 'x' }}</text></card>`}
	with := func(content string) map[string]string {
		return map[string]string{"/site/content.ui": content, "/site/note.ui": note["/site/note.ui"]}
	}
	content := `<content><box component="appearance"><note label="{{ dark ? 'tối' : 'sáng' }}" /></box></content>`
	mustFail(t, with(content), where("content.ui", content, "label="),
		"giá trị của label đọc trạng thái sống của appearance: note là layout, không có instance ở trình duyệt giữ giá trị đó")
	// control: an input from the page's data is the server's, inside a live component too
	html := renderAt(t, memorySite("", with(`<content><box component="appearance"><note label="{{ greeting }}" /></box></content>`)), ".", "/", map[string]any{"greeting": "chào"})
	mustContain(t, html, "<p>chào</p>")
	mustNotContain(t, html, "text: label")
}

// What a call passes covers the names around it inside the file, as the server binds them: an input
// named like a live component's name is the call's value there, printed by the server, not the
// component's live one.
func TestLayoutInputsCoverTheNamesAround(t *testing.T) {
	files := func(content string) map[string]string {
		return map[string]string{"/site/content.ui": content, "/site/mode-label.ui": `<text>{{ mode }}</text>`}
	}
	html := mustCompile(t, files(`<content><box component="appearance"><mode-label mode="{{ 'tối' }}" /></box></content>`))
	mustContain(t, html, "<p>tối</p>")
	mustNotContain(t, html, "text: mode")
	// control: without the input, mode is appearance's, kept up to date by the kernel
	mustContain(t, mustCompile(t, files(`<content><box component="appearance"><mode-label /></box></content>`)), `data-kitwork-bind="text: mode"`)

	// an input is a level around a component inside the file: a name both declare is a warning there
	panel := map[string]string{
		"/site/content.ui":    `<content><menu-panel open="{{ start }}" /></content>`,
		"/site/menu-panel.ui": `<box><text>{{ open }}</text><box component="dropdown"><text show="open">x</text></box></box>`,
	}
	partsWarns(t, panel, where("menu-panel.ui", panel["/site/menu-panel.ui"], "show="),
		"open ở đây là của dropdown, mà đầu vào của menu-panel bao ngoài cũng khai open: một tên một nghĩa")
}

// An input of a layout piece is a name its file reads — or a layout piece it calls reads, unless that
// call passes it.
func TestLayoutReadsAreTransitive(t *testing.T) {
	files := func(content string) map[string]string {
		return map[string]string{
			"/site/content.ui":    content,
			"/site/order-card.ui": `<card><order-line /><order-line order="{{ previous }}" /></card>`,
			"/site/order-line.ui": `<text>{{ order.id }}</text>`,
			// passes order to the piece it calls, so order is no input of its own
			"/site/order-summary.ui": `<card><order-line order="{{ latest }}" /></card>`,
		}
	}
	html := renderAt(t, memorySite("", files(`<content><order-card order="{{ latest }}" previous="{{ first }}" /></content>`)), ".", "/",
		map[string]any{"latest": map[string]any{"id": "DH-1"}, "first": map[string]any{"id": "DH-0"}})
	mustContain(t, html, "<p>DH-1</p><p>DH-0</p>")
	content := `<content><order-card total="{{ 1 }}" /></content>`
	mustFail(t, files(content), where("content.ui", content, "total="), "order-card.ui không đọc total")
	content = `<content><order-summary latest="{{ latest }}" order="{{ latest }}" /></content>`
	mustFail(t, files(content), where("content.ui", content, "order="), "order-summary.ui không đọc order")
}
