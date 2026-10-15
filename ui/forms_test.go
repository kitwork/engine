package ui

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/hydrate"
)

// Markup has three forms (KITWORK_UI §13): a Kitwork tag, a layout (a region, or a piece with only a
// .ui, called by its tag: layout_test.go), and component="name" on a tag for a piece with logic. These
// tests hold each rule of the third form to its message, at the file, line and column the author has
// to look at.

const dropdownUI = `<button click="toggle()">x</button>`

// A name with logic written as a tag is an error that says where its logic is and gives the
// component= form, on the tag its host is written on (the root of its .ui, else box), keeping the
// alias. A tag that is nothing at all says so.
func TestComponentWrittenAsATagIsAnError(t *testing.T) {
	cart := map[string]string{"/site/cart.ui": `<box><text>giỏ</text></box>`, "/site/cart.work": cartLogic}
	with := func(content string, more map[string]string) map[string]string {
		files := map[string]string{"/site/content.ui": content}
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
		{`<content><cart as="$cart" /></content>`, cart, `cart có logic (cart.work): viết <box component="cart" as="$cart" />`},
		{`<content><cart as="$cart" /></content>`, map[string]string{"/site/cart.ui": `<card><text>giỏ</text></card>`, "/.components/cart.work": cartLogic},
			`cart có logic (.components/cart.work): viết <card component="cart" as="$cart" />`},
		{`<content><dropdown as="$account"><button click="toggle()">x</button></dropdown></content>`, nil,
			`dropdown có logic (component của Kitwork): viết <box component="dropdown" as="$account">…</box>`},
		{`<content><appearance><button click="toggle()">x</button></appearance></content>`, nil,
			`appearance có logic (component của Kitwork): viết <box component="appearance">…</box>`},
		{`<content><toast as="$toast" /></content>`, nil, `toast có logic (component của Kitwork): viết <box component="toast" as="$toast" />`},
		{`<content><menu><text>x</text></menu></content>`, map[string]string{"/site/components.work": `import { component } from "kitwork"; component.define("menu", () => { var open = false; });`},
			`menu có logic (components.work): viết <box component="menu">…</box>`},
		{`<content><tabs><text>x</text></tabs></content>`, nil, `tabs là component của Kitwork mà engine chưa dựng được`},
		{`<content><dialog as="$confirm"><text>x</text></dialog></content>`, nil, `<dialog> là phần tử của Kitwork mà engine chưa dựng được`},
		// control: a tag that is nothing at all
		{`<content><price-tag /></content>`, nil, `không biết thẻ <price-tag>: không có trong từ vựng của Kitwork, không có layout price-tag.ui (từ thư mục của content.ui lên gốc site, rồi .components/), và không có component tên này`},
	} {
		tag := c.content[len("<content>"):]
		mustFail(t, with(c.content, c.more), where("content.ui", c.content, tag[:strings.IndexAny(tag, " >")]), c.says)
	}
	// the progressbar of page.ui too
	page := `<page><progressbar color="brand" /><content /></page>`
	mustFail(t, map[string]string{"/site/page.ui": page, "/site/content.ui": `<content><text>x</text></content>`},
		where("page.ui", page, "<progressbar"), `progressbar có logic (component của Kitwork): viết <box component="progressbar" … />`)
}

// A component with a .ui: the file's one root is the host, and the call writes that same tag, empty.
// The two are one element — the root's look and behaviour, the call's placement — never a wrapper.
func TestComponentRootTagRule(t *testing.T) {
	ui := `<box click-outside="close()" padding="2"><text>giỏ</text><card place="below-end"><text>x</text></card></box>`
	source := `<content><card component="cart" as="$cart" /></content>`
	mustFail(t, map[string]string{"/site/content.ui": source, "/site/cart.ui": ui, "/site/cart.work": cartLogic},
		where("content.ui", source, "component="), `cart.ui dựng một <box>: viết <box component="cart" as="$cart" />`)

	// control: the right tag compiles into one element, the file's root with what it derives from its
	// children (relative, for the panel placed below it)
	html := mustCompile(t, map[string]string{
		"/site/content.ui": `<content><grid columns="2"><box component="cart" as="$cart" span="2" /></grid></content>`,
		"/site/cart.ui":    ui, "/site/cart.work": cartLogic,
	})
	mustContain(t, html, `<div class="grid columns-2"><div class="flex-column place-anchor padding-2 span-2"><p>giỏ</p>`)
	if strings.Count(html, "<div") != 3 {
		t.Errorf("one element for the call and the root, got:\n%s", html)
	}
}

// The root of a component's .ui is the host: if and for belong to the call, the root cannot be a host
// of another component (one element, one instance), as is written at the call, and a region is the
// page's, not a component's.
func TestComponentRootCarriesNoCallWords(t *testing.T) {
	content := `<content><box component="cart" /></content>`
	for _, c := range []struct{ ui, needle, says string }{
		{`<box if="ready"><text>x</text></box>`, "if=", "gốc của cart.ui không mang if: có thẻ chủ hay không, lặp mấy lần là việc của chỗ gọi"},
		{`<box for="x of xs"><text>x</text></box>`, "for=", "gốc của cart.ui không mang for"},
		{`<box component="dropdown"><text>x</text></box>`, "component=", "gốc của cart.ui đã là thẻ chủ của cart: một phần tử một component"},
		{`<box as="$x"><text>x</text></box>`, "as=", "as đặt ở chỗ gọi"},
	} {
		mustFail(t, map[string]string{"/site/content.ui": content, "/site/cart.ui": c.ui, "/site/cart.work": cartLogic},
			where("cart.ui", c.ui, c.needle), c.says)
	}
	header := `<header><text>x</text></header>`
	mustFail(t, map[string]string{"/site/content.ui": `<content><header component="cart" /></content>`, "/site/cart.ui": header, "/site/cart.work": cartLogic},
		"content.ui:1:18: ", "<header /> rỗng là chỗ gọi vùng")
	banner := map[string]string{"/site/content.ui": `<content><box component="banner" /></content>`,
		"/site/banner.work": `import { component } from "kitwork"; component.define("banner", () => { var open = false; });`}
	for _, c := range []struct{ ui, says string }{
		{header, "gốc của banner.ui là vùng <header>"},
		{`<image value="/a.png" alt="" />`, "gốc của banner.ui là <image>, mà image không có nội dung con"},
		// the root is the host, so a tag of Kitwork, never a layout piece the call's words would miss
		{`<promo />`, "gốc của banner.ui là <promo>, không phải thẻ của Kitwork: thẻ chủ của component là một thẻ như box, card, list"},
	} {
		banner["/site/banner.ui"] = c.ui
		banner["/site/promo.ui"] = `<box><text>x</text></box>`
		mustFail(t, banner, "banner.ui:1:1: ", c.says)
	}
}

// Kitwork's components that bring their own UI (toast, progressbar) are called on the tag that UI is
// built on, with their own inputs only; a site does not restyle them with a .ui of its own yet.
func TestKitworkComponentsWithTheirOwnUI(t *testing.T) {
	for _, c := range []struct{ overlay, needle, says string }{
		{`<overlay><box component="toast" if="ready" /></overlay>`, "if=", "toast chỉ nhận as, không nhận if"},
		{`<overlay><box component="toast"><text>x</text></box></overlay>`, "<box", "toast không có nội dung con; thông báo gửi bằng $toast.show(…)"},
		{`<overlay><section component="toast" /></overlay>`, "component=", `toast của Kitwork dựng một <box>: viết <box component="toast" />`},
	} {
		mustFail(t, map[string]string{"/site/page.ui": `<page><content /><overlay /></page>`, "/site/content.ui": `<content><text>x</text></content>`, "/site/overlay.ui": c.overlay},
			where("overlay.ui", c.overlay, c.needle), c.says)
	}
	mustFail(t, map[string]string{"/site/page.ui": `<page><content /><overlay /></page>`, "/site/content.ui": `<content><text>x</text></content>`,
		"/site/overlay.ui": `<overlay><box component="toast" as="$toast" /></overlay>`, "/site/toast.ui": `<box><text>x</text></box>`},
		"toast.ui:1:1: ", "toast.ui đổi giao diện toast của Kitwork: engine chưa dựng toast từ file của site")
	// control: the toast as it is written
	html := mustCompile(t, map[string]string{"/site/page.ui": `<page><content /><overlay /></page>`, "/site/content.ui": `<content><text>x</text></content>`,
		"/site/overlay.ui": `<overlay><box component="toast" as="$toast" /></overlay>`})
	mustContain(t, html, `<div class="toast" data-kitwork-component="toast: $toast" role="status" aria-live="polite"></div>`)
}

// A call of a component with a UI takes the call's words and inputs only: inputs are names the
// component declares or its .ui reads, everything else is the file's (its look, its behaviour), and
// the content of the call is no slot.
func TestComponentWithUITakesCallWordsAndInputs(t *testing.T) {
	files := func(content string) map[string]string {
		return map[string]string{
			"/site/content.ui": content,
			"/site/offer.ui":   `<card><text>{{ product.name }}{{ label ?? '' }}</text></card>`,
			"/site/offer.work": `import { component } from "kitwork"; component.define("offer", () => { var open = false; });`,
		}
	}
	for _, c := range []struct{ content, needle, says string }{
		{`<content><card component="offer"><text>x</text></card></content>`, "<card", "offer có giao diện (offer.ui) nên thẻ gọi để rỗng: nội dung con không phải slot"},
		{`<content><card component="offer" padding="2" /></content>`, "padding=", "trình bày của offer nằm trong offer.ui; chỗ gọi nhận component, as, if, show, for, span, place và dữ liệu nó đọc"},
		{`<content><card component="offer" hover:shadow="medium" /></content>`, "hover:shadow=", "trình bày của offer nằm trong offer.ui"},
		{`<content><card component="offer" click="go()" /></content>`, "click=", "sự kiện click đặt trong offer.ui"},
		{`<content><card component="offer" price="{{ 1 }}" /></content>`, "price=", "offer không khai và offer.ui không đọc price"},
		{`<content><card component="offer" large:show="open" /></content>`, "large:show=", `bản đầu chưa cho tiền tố đi cùng biểu thức; large:show chỉ nhận "true" hoặc "false"`},
	} {
		mustFail(t, files(c.content), where("content.ui", c.content, c.needle), c.says)
	}
	// control: what the .ui reads goes in; span and place, with width prefixes, land on the root
	s := memorySite("", files(`<content><grid><card component="offer" product="{{ featured }}" label="{{ ' mới' }}" medium:span="2" place="top-end" /></grid></content>`))
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/", Scope: map[string]any{"featured": map[string]any{"name": "Bạc xỉu"}}})
	mustContain(t, html, "<p>Bạc xỉu mới</p>", `<div class="grid place-anchor"><div class="card flex-column medium:span-2 place-top-end">`)
}

// A component without a UI: the host is the author's tag, its attributes stay the tag's, and what is
// written inside is the component's UI. An attribute that is not the tag's is an input; one the tag
// and the component both claim is an error, never a guess.
func TestComponentWithoutUIHost(t *testing.T) {
	menu := `import { component } from "kitwork"; component.define("menu", () => { var value = "x"; var open = false; });`
	for _, c := range []struct {
		content, needle, says string
		files                 map[string]string
	}{
		{`<content><box component="dropdown" /></content>`, "<box", `dropdown không có giao diện: nội dung bên trong thẻ là giao diện của nó (<box component="dropdown">…</box>)`, nil},
		{`<content><button component="dropdown"><text>x</text></button></content>`, "component=", "dropdown đặt trên <box>, <card>, <item>, <section>, không trên <button>", nil},
		{`<content><box component="dropdown" tone="x"><text>x</text></box></content>`, "tone=", "tone không phải thuộc tính của <box>, và dropdown không khai tone", nil},
		{`<content><box component="dropdown" source=".components/dropdown"><text>x</text></box></content>`, "source=", "source chỉ dành cho vùng", nil},
		{`<content><link component="menu" value="/x"><text>x</text></link></content>`, "value=", "value là thuộc tính của <link> và cũng là tên menu khai: một chữ một nghĩa trên một thẻ",
			map[string]string{"/site/components.work": menu}},
	} {
		files := map[string]string{"/site/content.ui": c.content}
		for k, v := range c.files {
			files[k] = v
		}
		mustFail(t, files, where("content.ui", c.content, c.needle), c.says)
	}
	// control: on a box, value is no word of the tag, so it is menu's input; the tag's own words stay
	// the tag's
	html := mustCompile(t, map[string]string{
		"/site/content.ui":      `<content><box component="menu" value="{{ 'y' }}" alt="Thực đơn" padding="2"><text>{{ value }}</text></box></content>`,
		"/site/components.work": menu,
	})
	mustContain(t, html, `<div class="flex-column padding-2" aria-label="Thực đơn"><p>y</p></div>`)
}

// What is written on a host without a UI and inside it reads the component's names first. A name the
// component and an outer level both declare is a warning there — a component around it, or the
// route's view — since markup would read one and look like the other (the nearest wins). Inside the
// component's own .ui its names win without question.
func TestNameReadInsideTheHostIsNotShadowed(t *testing.T) {
	nested := `<content><box component="dropdown"><box component="dropdown"><button click="toggle()">x</button></box></box></content>`
	partsWarns(t, map[string]string{"/site/content.ui": nested}, where("content.ui", nested, `click=`),
		"toggle ở đây là của dropdown, mà dropdown bao ngoài cũng khai toggle: một tên một nghĩa")

	view := `import { router } from "kitwork";
router.view((ctx) => {
  var open = true;
  const user = { name: "Quốc" };
});`
	header := `<content><box component="dropdown"><text show="open">menu</text></box></content>`
	partsWarns(t, map[string]string{"/site/content.ui": header, "/site/router.work": view}, where("content.ui", header, `show=`),
		"open ở đây là của dropdown, mà view của route / (router.work) cũng khai open")

	// controls: a name only the page declares reads the page; inside cart.ui, cart's total is cart's even
	// though the view declares total too
	html := mustCompile(t, map[string]string{
		"/site/content.ui": `<content><box component="dropdown"><text>{{ user.name }}</text><button click="toggle()">x</button></box><box component="cart" /></content>`,
		"/site/router.work": `import { router } from "kitwork";
router.view((ctx) => {
  const user = { name: "Quốc" };
  function total() { return 1; }
});`,
		"/site/cart.ui":   `<box><text>{{ total() }}</text></box>`,
		"/site/cart.work": cartLogic,
	})
	mustContain(t, html, `data-kitwork-event="click: toggle()"`)
}

// as names one instance: never a layout piece (layout_test.go), never once per row (on the element,
// around it, or at the call that brings the file in), page-wide and unique in page.ui, the regions and the
// content, the instance's own inside a component's .ui. An alias read where nothing sets it is an
// error where it is read.
func TestAliasRules(t *testing.T) {
	cart := map[string]string{"/site/cart.ui": `<box><text>giỏ</text></box>`, "/site/cart.work": cartLogic}
	files := func(content string, more map[string]string) map[string]string {
		out := map[string]string{"/site/content.ui": content}
		for k, v := range cart {
			out[k] = v
		}
		for k, v := range more {
			out[k] = v
		}
		return out
	}
	for _, c := range []struct{ content, needle, says string }{
		{`<content><box component="cart" as="$cart" for="x of xs" /></content>`, "as=", "as không đặt trên thẻ có for: mỗi hàng là một instance"},
		{`<content><list><item for="x of xs"><box component="cart" as="$cart" /></item></list></content>`, "as=", "as nằm trong for (content.ui:1:22): mỗi hàng là một instance"},
		{`<content><box component="cart" as="$cart" /><box component="cart" as="$cart" /></content>`, `as="$cart" /><box`, ""},
		{`<content><box component="cart" as="$event" /></content>`, "as=", "$event là tên engine giữ chỗ; đặt alias khác"},
		{`<content><box component="cart" as="$_x" /></content>`, "as=", "as là $ và một tên bắt đầu bằng chữ"},
		{`<content><box component="cart" as="cart" /></content>`, "as=", "as là $ và một tên bắt đầu bằng chữ"},
		{`<content><button click="$basket.add(1)">x</button></content>`, "click=", `$basket chưa có trên trang này (route /): không thẻ nào trong tầm đặt as="$basket"`},
	} {
		if c.says == "" {
			// the second as of the same alias
			second := strings.LastIndex(c.content, `as="$cart"`)
			got := compileError(t, files(c.content, nil))
			want := "content.ui:1:" + itoa(second+1) + ": alias $cart đã đặt ở content.ui:1:32; alias là duy nhất trên trang"
			if got != want {
				t.Errorf("duplicate alias:\n got  %s\n want %s", got, want)
			}
			continue
		}
		mustFail(t, files(c.content, nil), where("content.ui", c.content, c.needle), c.says)
	}

	// an as inside a layout piece brought in by a call with a for: reported at the call, naming the line
	// inside
	content := `<content><box><row for="x of xs" /></box></content>`
	row := `<box><box component="dropdown" as="$more"><text>x</text></box></box>`
	got := compileError(t, map[string]string{"/site/content.ui": content, "/site/row.ui": row})
	if want := where("content.ui", content, "for=") + strings.TrimSuffix(where("row.ui", row, "as="), ": ") + ` đặt as="$more", mà chỗ gọi này lặp bằng for`; !strings.HasPrefix(got, want) {
		t.Errorf("as inside a file the for brings in:\n got  %s\n want %s…", got, want)
	}

	// an alias inside an instance's .ui is its own: two carts each have one, and the page cannot read it
	inner := map[string]string{"/site/cart.ui": `<box><box component="dropdown" as="$lines"><text>x</text></box></box>`, "/site/cart.work": cartLogic}
	two := `<content><box component="cart" as="$one" /><box component="cart" as="$two" /></content>`
	mustCompile(t, map[string]string{"/site/content.ui": two, "/site/cart.ui": inner["/site/cart.ui"], "/site/cart.work": cartLogic})
	reads := `<content><box component="cart" as="$one" /><button click="$lines.toggle()">x</button></content>`
	mustFail(t, map[string]string{"/site/content.ui": reads, "/site/cart.ui": inner["/site/cart.ui"], "/site/cart.work": cartLogic},
		where("content.ui", reads, "click="), "$lines chưa có trên trang này")

	// control: an alias set in a region is read from the content
	mustCompile(t, map[string]string{
		"/site/page.ui":    `<page><header /><content /></page>`,
		"/site/header.ui":  `<header><box component="dropdown" as="$menu"><button click="toggle()">Menu</button></box></header>`,
		"/site/content.ui": `<content><button click="$menu.toggle()">Mở</button></content>`,
	})
	// …and one that reaches only a site component, whose logic is not in the browser yet (step 4), says
	// so: written for the kernel, it would call nothing there (decision T2, 12/10/2026)
	siteOnly := `<content><button click="$cart.add(1)">Thêm</button></content>`
	mustFail(t, map[string]string{
		"/site/page.ui":    `<page><header /><content /></page>`,
		"/site/header.ui":  `<header><box component="cart" as="$cart" /></header>`,
		"/site/content.ui": siteOnly,
		"/site/cart.ui":    `<box><text>giỏ</text></box>`, "/site/cart.work": cartLogic,
	}, where("content.ui", siteOnly, "<button"), "$cart là alias của cart, mà cart chưa chạy ở trình duyệt")
}

// Regions: an empty region call is the region's file and takes no component=; a region written with
// content may host a component without a UI, never one with a UI; a region has no .work.
func TestRegionRules(t *testing.T) {
	content := `<content><text>x</text></content>`
	page := `<page><header component="dropdown" /><content /></page>`
	mustFail(t, map[string]string{"/site/page.ui": page, "/site/header.ui": `<header><text>Quán</text></header>`, "/site/content.ui": content},
		where("page.ui", page, "component="), "<header /> rỗng là chỗ gọi vùng, lấy header.ui")

	mustFail(t, map[string]string{"/site/page.ui": `<page><header /><content /></page>`, "/site/header.ui": `<header><text>Quán</text></header>`,
		"/site/header.work": `var open = false;`, "/site/content.ui": content}, "header.work:1:1: ", "vùng không có file .work")

	inPlace := `<page><header component="cart"><text>x</text></header><content /></page>`
	mustFail(t, map[string]string{"/site/page.ui": inPlace, "/site/cart.ui": `<box><text>giỏ</text></box>`, "/site/cart.work": cartLogic, "/site/content.ui": content},
		where("page.ui", inPlace, "component="), "cart có giao diện (cart.ui) nên không đặt trên vùng <header>")

	// control: the root of header.ui hosts a component without a UI, read inside it
	html := mustCompile(t, map[string]string{
		"/site/page.ui":    `<page><header /><content /></page>`,
		"/site/header.ui":  `<header component="dropdown" as="$menu" keydown:escape="close()"><button click="toggle()" expanded="{{ open }}">Menu</button></header>`,
		"/site/content.ui": content,
	})
	mustContain(t, html, `<header class="flex-column" data-kitwork-component="dropdown: $menu" data-kitwork-event="keydown.escape: close()"><button`,
		`data-kitwork-bind="aria-expanded: open"`)
}

// component= sits on a tag that holds what the author writes: never on the page, the route's content,
// a part of another element, an element with no content, or a Kitwork element that is its own instance.
func TestForbiddenHosts(t *testing.T) {
	for _, c := range []struct{ content, says string }{
		{`<content><list><item><text>x</text></item><empty component="menu"><text>x</text></empty></list></content>`, "component không đặt trên <empty>: empty là một phần của thẻ chứa nó"},
		{`<content><fragment component="menu" if="x"><text>x</text></fragment></content>`, "component không đặt trên <fragment>: fragment không dựng phần tử nào"},
		{`<content><image component="menu" value="/a.png" alt="" /></content>`, "component không đặt trên <image>: image không có nội dung con"},
		{`<content><icon component="menu" value="x" alt="" /></content>`, "component không đặt trên <icon>"},
		{`<content><accordion component="menu"><box click="toggle()"><text>Q</text></box></accordion></content>`, "component không đặt trên <accordion>: accordion là phần tử của Kitwork, tự là instance"},
		{`<content><box component="dropdown"><popover component="menu"><text>x</text></popover></box></content>`, "component không đặt trên <popover>"},
		{`<content><code component="menu">x</code></content>`, "component không đặt trên <code>: code chỉ chứa chữ"},
		{`<content><accordion><box component="menu" click="toggle()"><text>Q</text></box><text>A</text></accordion></content>`, "phần đầu của accordion thành <summary> của nó"},
	} {
		mustFail(t, map[string]string{"/site/content.ui": c.content}, where("content.ui", c.content, `component="menu"`), c.says)
	}
	page := `<page component="menu"><content /></page>`
	mustFail(t, map[string]string{"/site/page.ui": page, "/site/content.ui": `<content><text>x</text></content>`}, "page.ui:1:7: ", "component không đặt trên <page>")
	routeContent := `<content component="menu"><text>x</text></content>`
	mustFail(t, map[string]string{"/site/content.ui": routeContent}, "content.ui:1:10: ", "component không đặt trên <content>: content là nội dung của route")
	mustFail(t, map[string]string{"/site/page.ui": `<page><content /><reader component="menu" /></page>`, "/site/content.ui": `<content><text>x</text></content>`},
		"page.ui:1:26: ", "component không đặt trên <reader>")
}

// The value of component= is one static name, lowercase and hyphenated, or a path; a name that is a
// tag (the core vocabulary, a region, a Kitwork element) is written as that tag.
func TestComponentValueRules(t *testing.T) {
	for _, c := range []struct{ value, says string }{
		{`{{ kind }}`, "component là một tên cố định, không có {{ }}"},
		{`cart menu`, "component nhận một tên"},
		{`productCard`, `tên component viết thường, các từ nối bằng gạch ngang (product-card), không phải "productCard"`},
		{`card`, "card là thẻ lõi của Kitwork, không phải component: viết <card …>"},
		{`navbar`, "navbar là vùng, không phải component: <navbar /> lấy navbar.ui"},
		{`accordion`, "accordion là phần tử của Kitwork, không phải component: viết <accordion …>…</accordion>"},
		{`dialog`, "dialog là phần tử của Kitwork, không phải component"},
		{`tabs`, "tabs là component của Kitwork mà engine chưa dựng được"},
		{`giohang`, "không tìm thấy component giohang"},
	} {
		content := `<content><box component="` + c.value + `"><text>x</text></box></content>`
		mustFail(t, map[string]string{"/site/content.ui": content}, "content.ui:1:15: ", c.says)
	}
}

// A component is looked up from the folder of the file that writes it, never from the route: what
// header.ui writes means the same on every page. A page takes one source per name.
func TestLookupFromTheWritingFolder(t *testing.T) {
	logic := `import { component } from "kitwork"; component.define("mini-cart", () => { var open = false; });`
	files := map[string]string{
		"/site/page.ui":               `<page><header /><content /></page>`,
		"/site/header.ui":             `<header><box component="mini-cart" /></header>`,
		"/site/mini-cart.ui":          `<box><text>giỏ của site</text></box>`,
		"/site/shop/mini-cart.ui":     `<box><text>giỏ của shop</text></box>`,
		"/site/shop/content.ui":       `<content><text>Cửa hàng</text></content>`,
		"/site/content.ui":            `<content><text>x</text></content>`,
		"/.components/mini-cart.work": logic,
	}
	s := memorySite("", files)
	s.Reload = true
	html := renderAt(t, s, "shop", "/shop", nil)
	mustContain(t, html, "giỏ của site")
	mustNotContain(t, html, "giỏ của shop")

	// the shop's own content.ui reaches the shop's look — and a page with both takes two sources
	files["/site/shop/content.ui"] = `<content><box component="mini-cart" /></content>`
	_, err := memorySite("", files).Compile("/site/shop")
	if err == nil || !strings.HasPrefix(err.Error(), "shop/content.ui:1:15: ") ||
		!strings.Contains(err.Error(), "component mini-cart trên trang này đến từ hai nguồn: mini-cart.ui + ../.components/mini-cart.work (header.ui:1:14) và shop/mini-cart.ui + ../.components/mini-cart.work") {
		t.Errorf("two sources for one name on a page: %v", err)
	}
	// control: a page with only the branch's call takes the branch's look
	html = renderAt(t, memorySite("", map[string]string{
		"/site/shop/content.ui":       `<content><box component="mini-cart" /></content>`,
		"/site/shop/mini-cart.ui":     `<box><text>giỏ của shop</text></box>`,
		"/site/mini-cart.ui":          `<box><text>giỏ của site</text></box>`,
		"/.components/mini-cart.work": logic,
	}), "shop", "/shop", nil)
	mustContain(t, html, "giỏ của shop")
}

// Whether a component has a look is fixed where it is defined: a branch .ui may restyle a component
// that has one, never give one to a component without a UI.
func TestBranchUIForAComponentWithoutUI(t *testing.T) {
	files := map[string]string{
		"/site/quan-ly/content.ui":  `<content><box component="dropdown"><button click="toggle()">x</button></box></content>`,
		"/site/quan-ly/dropdown.ui": `<box><text>menu</text></box>`,
	}
	_, err := memorySite("", files).Compile("/site/quan-ly")
	if err == nil || !strings.HasPrefix(err.Error(), "quan-ly/dropdown.ui:1:1: ") ||
		!strings.Contains(err.Error(), "dropdown là component không có giao diện (của Kitwork)") ||
		!strings.Contains(err.Error(), "chỗ gọi quan-ly/content.ui:1:15 sẽ hỏng") {
		t.Errorf("a branch .ui for a component without a UI: %v", err)
	}
	// the same for a site's own component without a UI
	files = map[string]string{
		"/site/quan-ly/content.ui": `<content><box component="menu"><button click="toggle()">x</button></box></content>`,
		"/site/quan-ly/menu.ui":    `<box><text>menu</text></box>`,
		"/site/components.work":    `import { component } from "kitwork"; component.define("menu", () => { var open = false; });`,
	}
	_, err = memorySite("", files).Compile("/site/quan-ly")
	if err == nil || !strings.HasPrefix(err.Error(), "quan-ly/menu.ui:1:1: ") ||
		!strings.Contains(err.Error(), "menu là component không có giao diện (ở components.work)") {
		t.Errorf("a branch .ui for a site component without a UI: %v", err)
	}
	// control: a branch restyles a component that has a UI
	html := renderAt(t, memorySite("", map[string]string{
		"/site/quan-ly/content.ui": `<content><box component="cart" /></content>`,
		"/site/quan-ly/cart.ui":    `<box><text>giỏ quản lý {{ note }}</text></box>`,
		"/site/cart.ui":            `<box><text>giỏ</text></box>`,
		"/site/cart.work":          cartLogic,
	}), "quan-ly", "/quan-ly", nil)
	mustContain(t, html, "<p>giỏ quản lý chưa có</p>")
}

// A path names one place for both halves: ./ and ../ from the file that writes it, .components/ for
// the identity, kitwork/components/ for Kitwork. It never leaves the site.
func TestComponentPaths(t *testing.T) {
	files := map[string]string{
		"/site/cart.ui":          `<box><text>{{ note }}</text></box>`,
		"/.components/cart.ui":   `<box><text>identity {{ note }}</text></box>`,
		"/.components/cart.work": cartLogic,
	}
	at := func(content string) map[string]string {
		out := map[string]string{"/site/content.ui": content}
		for k, v := range files {
			out[k] = v
		}
		return out
	}
	// by name: the site's look, the identity's logic
	mustContain(t, mustCompile(t, at(`<content><box component="cart" /></content>`)), "<p>chưa có</p>")
	// .components/ pins both to the identity
	mustContain(t, mustCompile(t, at(`<content><box component=".components/cart" /></content>`)), "<p>identity chưa có</p>")
	// ./ pins both to the site root: the site's look and no logic there, so a layout piece there,
	// called by its tag with source=, where note is the page's
	mustFail(t, at(`<content><box component="./cart" /></content>`), "content.ui:1:15: ",
		`cart là layout (chỉ có giao diện, không có logic): viết <cart source="./cart" />`)
	html := renderAt(t, memorySite("", at(`<content><cart source="./cart" /></content>`)), ".", "/", map[string]any{"note": "của trang"})
	mustContain(t, html, `<div class="flex-column"><p>của trang</p></div>`)
	mustNotContain(t, html, "chưa có")
	mustContain(t, mustCompile(t, at(`<content><box component="kitwork/components/dropdown"><button click="toggle()">x</button></box></content>`)),
		`data-kitwork-component="dropdown"`)
	for _, c := range []struct{ value, says string }{
		{`../../cart`, "đường dẫn ../../cart ra ngoài site"},
		{`shop/cart`, `đường dẫn component bắt đầu bằng ./ hoặc ../ (từ file đang viết), .components/ (của identity) hoặc kitwork/components/ (của Kitwork), không phải "shop/cart"`},
		{`kitwork/components/cart`, "Kitwork không có component cart"},
		{`./Cart`, "đường dẫn component kết thúc bằng tên component"},
		{`./nothing`, "không tìm thấy component nothing ở ./nothing"},
	} {
		content := `<content><box component="` + c.value + `" /></content>`
		mustFail(t, at(content), "content.ui:1:15: ", c.says)
	}
}

// The kernel keeps a for's row and a component's instance in one state slot, so a for or an if on a
// host that reads live state is refused with a message that says so. Over the page's data a host may
// repeat: the server builds each row, each one its own instance.
func TestLiveForAndIfOnAHost(t *testing.T) {
	for _, c := range []struct{ content, needle, says string }{
		{`<content><box component="appearance"><box component="dropdown" if="dark"><text>x</text></box></box></content>`, "if=",
			"if trên thẻ chủ của dropdown đọc trạng thái sống (dark): kernel chưa tách được hàng của for khỏi instance của component"},
		{`<content><box component="appearance"><box component="dropdown" for="x of dark"><text>x</text></box></box></content>`, "for=",
			"for trên thẻ chủ của dropdown đọc trạng thái sống (dark)"},
		{`<content><box component="appearance"><note if="dark" /></box></content>`, "if=",
			"bên trong appearance if theo trạng thái của component chưa chạy ở trình duyệt; dùng show"},
		{`<content><box component="appearance"><note for="x of dark" /></box></content>`, "for=",
			"bên trong appearance for theo trạng thái của component chưa chạy ở trình duyệt"},
	} {
		mustFail(t, map[string]string{"/site/content.ui": c.content, "/site/note.ui": `<card><text>x</text></card>`},
			where("content.ui", c.content, c.needle), c.says)
	}
	// control: over the page's data each row is its own host
	s := memorySite("", map[string]string{"/site/content.ui": `<content><list><item component="dropdown" for="x of xs; key: x"><button click="toggle()">{{ x }}</button></item></list></content>`})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/", Scope: map[string]any{"xs": []any{"a", "b"}}})
	if got := strings.Count(html, `data-kitwork-component="dropdown"`); got != 2 {
		t.Errorf("one host per row, got %d:\n%s", got, html)
	}
	// control: show is no if — a host's show that reads live state is kept up to date by the kernel
	html = mustCompile(t, map[string]string{"/site/content.ui": `<content><box component="appearance"><note show="dark" /></box></content>`,
		"/site/note.ui": `<card><text>x</text></card>`})
	mustContain(t, html, `<div class="card flex-column" data-kitwork-bind="show: dark" hidden><p>x</p>`)
}

// A popover shows while the component around it is open: one that declares open and close.
func TestPopoverNeedsAnOpeningComponent(t *testing.T) {
	content := `<content><box><popover><text>x</text></popover></box></content>`
	mustFail(t, map[string]string{"/site/content.ui": content}, where("content.ui", content, "<popover"),
		"popover hiện khi component chứa nó mở: đặt nó trong thẻ chủ của một component có open và close")
	inAppearance := `<content><box component="appearance"><popover><text>x</text></popover></box></content>`
	mustFail(t, map[string]string{"/site/content.ui": inAppearance}, where("content.ui", inAppearance, "<popover"), "popover hiện khi component chứa nó mở")
	// control
	mustContain(t, mustCompile(t, map[string]string{"/site/content.ui": `<content><box component="dropdown"><button click="toggle()">x</button><popover><text>x</text></popover></box></content>`}),
		`hidden data-kitwork-bind="show: open"`)
}

// An instance the kernel runs starts from its inputs: the server renders with them, and the host
// carries them per request as data-kitwork-scope, the page's data written as literals. A component
// the kernel does not run (a site component) gets none: its host is not marked yet.
func TestInputsSeedTheInstance(t *testing.T) {
	s := memorySite("", map[string]string{"/site/content.ui": `<content><box component="dropdown" open="{{ start }}"><button click="toggle()" expanded="{{ open }}">x</button></box></content>`})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/", Scope: map[string]any{"start": true}})
	mustContain(t, html, `data-kitwork-component="dropdown" data-kitwork-scope="open: true"`, `aria-expanded="true"`)
	html = page.Render(Request{Path: "/", Scope: map[string]any{"start": false}})
	mustContain(t, html, `data-kitwork-scope="open: false"`, `aria-expanded="false"`)
	// a value that reads a live component around the host is the expression, which the kernel runs in
	// the scope around the host
	mustContain(t, mustCompile(t, map[string]string{"/site/content.ui": `<content><box component="appearance"><box component="dropdown" open="{{ dark }}"><button click="close()">x</button></box></box></content>`}),
		`data-kitwork-component="dropdown" data-kitwork-scope="open: dark"`)

	for _, c := range []struct {
		value any
		want  string
	}{
		{"it's", `'it\'s'`},
		{`a\b`, `'a\\b'`},
		{3.5, "3.5"},
		{nil, "null"},
		{[]any{1.0, "a"}, "[1, 'a']"},
		{map[string]any{"b": 1.0, "a": true}, "{'a': true, 'b': 1}"},
	} {
		got := literal(c.value, 0)
		if got != c.want {
			t.Errorf("literal(%v) = %s, want %s", c.value, got, c.want)
		}
		ir, err := hydrate.Compile(got)
		if err != nil {
			t.Errorf("the kernel cannot read %s: %v", got, err)
			continue
		}
		// it reads back as the value the server read
		if back, err := hydrate.Eval(ir, map[string]any{}); err != nil || literal(back, 0) != got {
			t.Errorf("%s reads back as %v (%v)", got, back, err)
		}
	}
	// control: the site's cart takes its input on the server and carries no scope
	html = mustCompile(t, map[string]string{"/site/content.ui": `<content><box component="cart" note="{{ 'x' }}" /></content>`,
		"/site/cart.ui": `<box><text>{{ note }}</text></box>`, "/site/cart.work": cartLogic})
	mustContain(t, html, "<p>x</p>")
	mustNotContain(t, html, "data-kitwork-scope")
}

// A site component inside one the kernel runs: its own events are its own (its logic reaches the
// browser later), not page events inside the Kitwork component, and its names win inside its .ui.
func TestSiteComponentInsideAKitworkComponent(t *testing.T) {
	html := mustCompile(t, map[string]string{
		"/site/content.ui": `<content><box component="dropdown"><button click="toggle()">x</button><box component="cart" /></box></content>`,
		"/site/cart.ui":    `<box click-outside="close()"><button click="toggle()">{{ note }}</button></box>`,
		"/site/cart.work": `import { component } from "kitwork";
component.define("cart", () => {
  var open = false;
  var note = "giỏ";
  const toggle = () => { open = !open; };
  const close = () => { open = false; };
});`,
	})
	mustContain(t, html, `data-kitwork-event="click: toggle()">x</button>`, `<div class="flex-column"><button class="`, ">giỏ</button>")
	if strings.Count(html, "data-kitwork-event") != 1 {
		t.Errorf("only the dropdown's button is wired; the cart's events wait for the cart:\n%s", html)
	}
}
