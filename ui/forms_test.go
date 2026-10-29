package ui

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/hydrate"
)

// Every component is written as its tag (KITWORK_UI §13, 18/10/2026): one of Kitwork's by a name
// without a dash (<dropdown>, <toast>), one of the site's by a name with one (<shop-cart>; a piece with
// only a .ui is a layout, called the same way: layout_test.go). These tests hold each rule of a
// component's call, its host and its files to its message, at the file, line and column the author has
// to look at; the edges of the tag rule itself are in tags_test.go.

const dropdownUI = `<button click="toggle()">x</button>`

// A tag that names nothing the engine builds is an error at the tag that says why: one of Kitwork's the
// engine does not build yet (a component, an element), a name with a dash that nothing in reach has, and
// a component of the site in components.work whose name has no dash, told to take one (one in a file of
// its own name: tags_test.go). Until 18/10/2026 a component written as its tag was an error that gave the
// component= form; that is how a component is written now, so Kitwork's own written as their tags
// compile, the progressbar of view.ui with them.
func TestTagThatNamesNothingIsAnError(t *testing.T) {
	for _, c := range []struct {
		content string
		more    map[string]string
		says    string
	}{
		{`<content><menu><text>x</text></menu></content>`, map[string]string{"/site/components.work": `import { component } from "kitwork"; component.define("menu", () => { var open = false; });`},
			`<menu> không có gạch ngang nên là thẻ của Kitwork, mà Kitwork không có thẻ này: tên layout và component của site có gạch ngang; trong components.work đổi component.define("menu", …) thành component.define("shop-menu", …) rồi gọi <shop-menu>`},
		{`<content><tooltip><text>x</text></tooltip></content>`, nil, `<tooltip> là component của Kitwork mà engine chưa dựng được`},
		{`<content><dialog as="$confirm"><text>x</text></dialog></content>`, nil, `<dialog> là phần tử của Kitwork mà engine chưa dựng được`},
		// a name with a dash that nothing in reach has
		{`<content><price-tag /></content>`, nil, `không biết thẻ <price-tag>: không có layout price-tag.ui (từ thư mục của content.ui lên gốc site, rồi .components/), và không có component tên này`},
	} {
		files := map[string]string{"/site/content.ui": c.content}
		for k, v := range c.more {
			files[k] = v
		}
		tag := c.content[len("<content>"):]
		mustFail(t, files, where("content.ui", c.content, tag[:strings.IndexAny(tag, " >")]), c.says)
	}
	// control: the progressbar of view.ui written as its tag is Kitwork's, built on the box of its own UI
	html := mustCompile(t, map[string]string{"/site/view.ui": `<view><progressbar color="brand" /><content /></view>`, "/site/content.ui": `<content><text>x</text></content>`})
	mustContain(t, html, `<div class="progressbar" data-kitwork-component="progressbar"`, `<div class="thickness-1 background-brand"`)
	mustNotContain(t, html, "<progressbar")
}

// A component's look used again is a layout piece whose root is the component's tag (28/10/2026), called by
// that tag, empty: the call and the root are one element — the root's look and behaviour, the call's
// placement — never a wrapper, and the tag itself never reaches the page.
func TestComponentRootTagRule(t *testing.T) {
	ui := `<shop-cart click-outside="close()" padding="2"><text>giỏ</text><card place="below-right"><text>x</text></card></shop-cart>`
	// one element, the file's root with what it derives from its children (relative, for the panel
	// placed below it) and the call's span
	html := mustCompile(t, map[string]string{
		"/site/content.ui":   `<content><grid columns="2"><shop-cart as="$cart" span="2" /></grid></content>`,
		"/site/shop-cart.ui": ui, "/site/shop-cart.work": cartLogic,
	})
	mustContain(t, html, `<div class="grid columns-2"><div class="flex-column place-anchor padding-2 span-2"><p>giỏ</p>`)
	mustNotContain(t, html, "<shop-cart")
	if strings.Count(html, "<div") != 3 {
		t.Errorf("one element for the call and the root, got:\n%s", html)
	}
}

// The root of a component's look is the component's tag written with content (28/10/2026): the call puts its
// for on the instance, so the root carries none; component= there is the removed attribute's error; a file
// named after a component whose root is anything else is the form of 18/10/2026, told to write the root; the
// tag written empty as its own file's root calls itself. The root's own as and if are its call's, in the file.
func TestComponentRootCarriesNoCallWords(t *testing.T) {
	content := `<content><shop-cart /></content>`
	for _, c := range []struct{ ui, needle, says string }{
		{`<shop-cart for="x of xs"><text>x</text></shop-cart>`, "for=", "gốc của shop-cart.ui không mang for: lặp mấy lần là việc của chỗ gọi (<shop-cart for=\"…\" />)"},
		{`<shop-cart component="dropdown"><text>x</text></shop-cart>`, "component=", "component= đã bỏ: component là một thẻ, viết <dropdown>…</dropdown>"},
		{`<box><text>x</text></box>`, "<box", "shop-cart.ui là giao diện của component shop-cart (logic ở shop-cart.work): gốc của nó là thẻ của chính component, <shop-cart …>…</shop-cart>, bố cục của box viết trên đó, không phải <box>"},
		{`<header><text>x</text></header>`, "<header", "không phải <header>"},
		{`<dropdown><button click="toggle()">x</button></dropdown>`, "<dropdown", "không phải <dropdown>"},
		{`<shop-cart />`, "<shop-cart", "<shop-cart /> rỗng là gốc của chính shop-cart.ui: gọi vòng"},
	} {
		mustFail(t, map[string]string{"/site/content.ui": content, "/site/shop-cart.ui": c.ui, "/site/shop-cart.work": cartLogic},
			where("shop-cart.ui", c.ui, c.needle), c.says)
	}
	// control: as and if written on the root are the root's, the file's own call of the component
	mustContain(t, mustCompile(t, map[string]string{"/site/content.ui": content,
		"/site/shop-cart.ui": `<shop-cart as="$cart" if="true"><text>giỏ</text></shop-cart>`, "/site/shop-cart.work": cartLogic}), "<p>giỏ</p>")
}

// Kitwork's component that brings its own UI (progressbar) is called by its tag, empty, with its own
// inputs only; a site does not restyle it with a .ui of its own. The toast brought its own until
// 17/10/2026: now the author draws the stack and its notice (toast.go), so what was its UI's rule is the
// toast's own words, named by its tag. Since 18/10/2026 <toast> builds the stack on a box, so a stack
// written component= on another tag is the removed attribute's error that says to put that tag inside,
// and a toast.ui beside is no piece of the site but an error at that file: <toast> is always Kitwork's,
// so the file would never be read.
func TestKitworkComponentsWithTheirOwnUI(t *testing.T) {
	for _, c := range []struct{ overlay, needle, says string }{
		{`<overlay>` + toastStack(` as="$toast" if="ready"`) + `</overlay>`, "if=", "toast luôn có mặt và có một trên mỗi trang: bỏ if trên <toast>"},
		{`<overlay><toast><text>x</text></toast></overlay>`, "<toast", "toast cần một mẫu thông báo bên trong <toast>"},
		{`<overlay><section component="toast">` + toastNotice + `</section></overlay>`, "component=",
			`component= đã bỏ: component là một thẻ, viết <toast>…</toast> (<toast> dựng một <box>; cần <section> thì đặt nó bên trong)`},
	} {
		mustFail(t, map[string]string{"/site/view.ui": `<view><content /><overlay /></view>`, "/site/content.ui": `<content><text>x</text></content>`, "/site/overlay.ui": c.overlay},
			where("overlay.ui", c.overlay, c.needle), c.says)
	}
	mustFail(t, map[string]string{"/site/view.ui": `<view><content /><overlay /></view>`, "/site/content.ui": `<content><text>x</text></content>`,
		"/site/overlay.ui": `<overlay>` + toastStack(` as="$toast"`) + `</overlay>`, "/site/toast.ui": `<box><text>x</text></box>`},
		"toast.ui:1:1: ", "toast.ui đặt tên trùng component <toast> của Kitwork: <toast> luôn là component của Kitwork nên file này không bao giờ được gọi")
	// control: the toast as it is written
	html := mustCompile(t, map[string]string{"/site/view.ui": `<view><content /><overlay /></view>`, "/site/content.ui": `<content><text>x</text></content>`,
		"/site/overlay.ui": `<overlay>` + toastStack(` as="$toast"`) + `</overlay>`})
	mustContain(t, html, `<div class="flex-column toast toast-bottom-right" data-kitwork-component="toast: $toast" role="region" aria-label="Thông báo (F8)" tabindex="-1"`)
}

// A call of a component's look takes the call's words and inputs only: inputs are names its file reads (the
// file passes them on to the component on its root, 28/10/2026), everything else is the file's (its look, its
// behaviour).
func TestComponentWithUITakesCallWordsAndInputs(t *testing.T) {
	files := func(content string) map[string]string {
		return map[string]string{
			"/site/content.ui":      content,
			"/site/shop-offer.ui":   `<shop-offer><card><text>{{ product.name }}{{ label ?? '' }}</text></card></shop-offer>`,
			"/site/shop-offer.work": `import { component } from "kitwork"; component.define("shop-offer", () => { var open = false; });`,
		}
	}
	for _, c := range []struct{ content, needle, says string }{
		{`<content><shop-offer padding="2" /></content>`, "padding=", "trình bày của shop-offer nằm trong shop-offer.ui; chỗ gọi nhận as, if, show, for, span, place và dữ liệu nó đọc"},
		{`<content><shop-offer hover:shadow="medium" /></content>`, "hover:shadow=", "trình bày của shop-offer nằm trong shop-offer.ui"},
		{`<content><shop-offer click="go()" /></content>`, "click=", "sự kiện click đặt trong shop-offer.ui"},
		{`<content><shop-offer price="{{ 1 }}" /></content>`, "price=", "shop-offer.ui không đọc price (đầu vào của shop-offer viết trên gốc của file)"},
		{`<content><shop-offer large:show="open" /></content>`, "large:show=", `bản đầu chưa cho tiền tố đi cùng biểu thức; large:show chỉ nhận "true" hoặc "false"`},
	} {
		mustFail(t, files(c.content), where("content.ui", c.content, c.needle), c.says)
	}
	// control: what the .ui reads goes in; span and place, with width prefixes, land on the root
	s := memorySite("", files(`<content><grid><shop-offer product="{{ featured }}" label="{{ ' mới' }}" medium:span="2" place="top-right" /></grid></content>`))
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/", Scope: map[string]any{"featured": map[string]any{"name": "Bạc xỉu"}}})
	mustContain(t, html, "<p>Bạc xỉu mới</p>", `<div class="grid place-anchor"><div class="flex-column medium:span-2 place-top-right"><div class="card flex-column">`)
}

// A component without a UI builds on a box (a button for appearance): the attributes written on its tag
// are the box's, and what is written inside is the component's UI. An attribute that is not the box's
// is an input; one the box and the component both claim is an error, never a guess. The host is
// Kitwork's choice, so component= on a tag the author chose is the removed attribute's error that says
// to put that tag inside (until 18/10/2026: the tags a component may be written on).
func TestComponentWithoutUIHost(t *testing.T) {
	menu := `import { component } from "kitwork"; component.define("site-menu", () => { var value = "x"; var open = false; });`
	for _, c := range []struct {
		content, needle, says string
		files                 map[string]string
	}{
		{`<content><dropdown /></content>`, "<dropdown", `<dropdown> không có giao diện: nội dung bên trong thẻ là giao diện của nó (<dropdown>…</dropdown>)`, nil},
		{`<content><button component="dropdown"><text>x</text></button></content>`, "component=",
			"component= đã bỏ: component là một thẻ, viết <dropdown>…</dropdown> (<dropdown> dựng một <box>; cần <button> thì đặt nó bên trong)", nil},
		{`<content><dropdown tone="x"><text>x</text></dropdown></content>`, "tone=", "tone không phải thuộc tính của <box> (<dropdown> dựng một <box>), và dropdown không khai tone", nil},
		{`<content><dropdown source=".components/dropdown"><text>x</text></dropdown></content>`, "source=", "source lấy một file của site; <dropdown> là của Kitwork, không có source", nil},
		{`<content><site-menu alt="Thực đơn"><text>x</text></site-menu></content>`, "alt=",
			"alt là thuộc tính của <box> (<site-menu> dựng một <box>) và cũng là tên site-menu khai: một chữ một nghĩa trên một thẻ",
			map[string]string{"/site/components.work": `import { component } from "kitwork"; component.define("site-menu", () => { var alt = "x"; var open = false; });`}},
	} {
		files := map[string]string{"/site/content.ui": c.content}
		for k, v := range c.files {
			files[k] = v
		}
		mustFail(t, files, where("content.ui", c.content, c.needle), c.says)
	}
	// control: value is no word of the box, so it is site-menu's input; the box's own words stay the box's
	html := mustCompile(t, map[string]string{
		"/site/content.ui":      `<content><site-menu value="{{ 'y' }}" alt="Thực đơn" padding="2"><text>{{ value }}</text></site-menu></content>`,
		"/site/components.work": menu,
	})
	mustContain(t, html, `<div class="flex-column padding-2" aria-label="Thực đơn"><p>y</p></div>`)
}

// What is written on the tag of a component without a UI and inside it reads the component's names
// first. A name the component and an outer level both declare is a warning there — a component around
// it, or the route's view — since markup would read one and look like the other (the nearest wins).
// Inside the component's own .ui its names win without question.
func TestNameReadInsideTheHostIsNotShadowed(t *testing.T) {
	nested := `<content><dropdown><dropdown><button click="toggle()">x</button></dropdown></dropdown></content>`
	partsWarns(t, map[string]string{"/site/content.ui": nested}, where("content.ui", nested, `click=`),
		"toggle ở đây là của dropdown, mà dropdown bao ngoài cũng khai toggle: một tên một nghĩa")

	view := `import * as router from "kitwork/router";
router.get((context) => {
  const open = true;
  const user = { name: "Quốc" };
  return context.view({ open, user });
});`
	header := `<content><dropdown><text show="open">menu</text></dropdown></content>`
	partsWarns(t, map[string]string{"/site/content.ui": header, "/site/router.work": view}, where("content.ui", header, `show=`),
		"open ở đây là của dropdown, mà view của route / (router.work) cũng khai open")

	// controls: a name only the page declares reads the page; inside shop-cart.ui, the cart's total is
	// the cart's even though the view declares total too
	html := mustCompile(t, map[string]string{
		"/site/content.ui": `<content><dropdown><text>{{ user.name }}</text><button click="toggle()">x</button></dropdown><shop-cart /></content>`,
		"/site/router.work": `import * as router from "kitwork/router";
router.get((ctx) => ctx.view({ user: { name: "Quốc" }, total: () => 1 }));`,
		"/site/shop-cart.ui":   `<shop-cart><text>{{ total() }}</text></shop-cart>`,
		"/site/shop-cart.work": cartLogic,
	})
	mustContain(t, html, `data-kitwork-event="click: toggle()"`)
}

// as names one instance: never a layout piece (layout_test.go), never once per row (on the element,
// around it, or at the call that brings the file in), page-wide and unique in view.ui, the regions and the
// content, the instance's own inside a component's .ui. An alias read where nothing sets it is an
// error where it is read.
func TestAliasRules(t *testing.T) {
	cart := map[string]string{"/site/shop-cart.ui": `<shop-cart><text>giỏ</text></shop-cart>`, "/site/shop-cart.work": cartLogic}
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
		{`<content><shop-cart as="$cart" for="x of xs" /></content>`, "as=", "as không đặt trên thẻ có for: mỗi hàng là một instance"},
		{`<content><list><item for="x of xs"><shop-cart as="$cart" /></item></list></content>`, "as=", "as nằm trong for (content.ui:1:22): mỗi hàng là một instance"},
		{`<content><shop-cart as="$cart" /><shop-cart as="$cart" /></content>`, `as="$cart" /><shop-cart`, ""},
		{`<content><shop-cart as="$event" /></content>`, "as=", "$event là tên engine giữ chỗ; đặt alias khác"},
		{`<content><shop-cart as="$_x" /></content>`, "as=", "as là $ và một tên bắt đầu bằng chữ"},
		{`<content><shop-cart as="cart" /></content>`, "as=", "as là $ và một tên bắt đầu bằng chữ"},
		{`<content><button click="$basket.add(1)">x</button></content>`, "click=", `$basket chưa có trên trang này (route /): không thẻ nào trong tầm đặt as="$basket"`},
	} {
		if c.says == "" {
			// the second as of the same alias
			second := strings.LastIndex(c.content, `as="$cart"`)
			got := compileError(t, files(c.content, nil))
			want := "content.ui:1:" + itoa(second+1) + ": alias $cart đã đặt ở content.ui:1:21; alias là duy nhất trên trang"
			if got != want {
				t.Errorf("duplicate alias:\n got  %s\n want %s", got, want)
			}
			continue
		}
		mustFail(t, files(c.content, nil), where("content.ui", c.content, c.needle), c.says)
	}

	// an as inside a layout piece brought in by a call with a for: reported at the call, naming the line
	// inside
	content := `<content><box><site-row for="x of xs" /></box></content>`
	row := `<box><dropdown as="$more"><text>x</text></dropdown></box>`
	got := compileError(t, map[string]string{"/site/content.ui": content, "/site/site-row.ui": row})
	if want := where("content.ui", content, "for=") + strings.TrimSuffix(where("site-row.ui", row, "as="), ": ") + ` đặt as="$more", mà chỗ gọi này lặp bằng for`; !strings.HasPrefix(got, want) {
		t.Errorf("as inside a file the for brings in:\n got  %s\n want %s…", got, want)
	}

	// an alias inside an instance's .ui is its own: two carts each have one, and the page cannot read it
	inner := `<shop-cart><dropdown as="$lines"><text>x</text></dropdown></shop-cart>`
	two := `<content><shop-cart as="$one" /><shop-cart as="$two" /></content>`
	mustCompile(t, map[string]string{"/site/content.ui": two, "/site/shop-cart.ui": inner, "/site/shop-cart.work": cartLogic})
	reads := `<content><shop-cart as="$one" /><button click="$lines.toggle()">x</button></content>`
	mustFail(t, map[string]string{"/site/content.ui": reads, "/site/shop-cart.ui": inner, "/site/shop-cart.work": cartLogic},
		where("content.ui", reads, "click="), "$lines chưa có trên trang này")

	// control: an alias set in a region is read from the content
	mustCompile(t, map[string]string{
		"/site/view.ui":    `<view><header /><content /></view>`,
		"/site/header.ui":  `<header><dropdown as="$menu"><button click="toggle()">Menu</button></dropdown></header>`,
		"/site/content.ui": `<content><button click="$menu.toggle()">Mở</button></content>`,
	})
	// …and one that reaches only a site component, whose logic is not in the browser yet (step 4), says
	// so: written for the kernel, it would call nothing there (decision T2, 12/10/2026)
	siteOnly := `<content><button click="$cart.add(1)">Thêm</button></content>`
	mustFail(t, map[string]string{
		"/site/view.ui":      `<view><header /><content /></view>`,
		"/site/header.ui":    `<header><shop-cart as="$cart" /></header>`,
		"/site/content.ui":   siteOnly,
		"/site/shop-cart.ui": `<shop-cart><text>giỏ</text></shop-cart>`, "/site/shop-cart.work": cartLogic,
	}, where("content.ui", siteOnly, "<button"), "$cart là alias của shop-cart, mà shop-cart chưa chạy ở trình duyệt")
}

// Regions: a region is the page's and never a component. component= on an empty region call or on the
// root of a region's file is the removed attribute's error, which says to write the component's tag
// inside the region (until 18/10/2026 a region's root could host a component without a UI); a region has
// no .work. CONTROL: a component written as its tag inside the region's file is read there.
func TestRegionRules(t *testing.T) {
	content := `<content><text>x</text></content>`
	page := `<view><header component="dropdown" /><content /></view>`
	mustFail(t, map[string]string{"/site/view.ui": page, "/site/header.ui": `<header><text>Quán</text></header>`, "/site/content.ui": content},
		where("view.ui", page, "component="), "component= đã bỏ: component là một thẻ, viết <dropdown /> bên trong <header>")

	root := `<header component="dropdown" as="$menu" keydown:escape="close()"><button click="toggle()" expanded="{{ open }}">Menu</button></header>`
	mustFail(t, map[string]string{"/site/view.ui": `<view><header /><content /></view>`, "/site/header.ui": root, "/site/content.ui": content},
		where("header.ui", root, "component="), `component= đã bỏ: component là một thẻ, viết <dropdown as="$menu">…</dropdown> bên trong <header>`)

	mustFail(t, map[string]string{"/site/view.ui": `<view><header /><content /></view>`, "/site/header.ui": `<header><text>Quán</text></header>`,
		"/site/header.work": `var open = false;`, "/site/content.ui": content}, "header.work:1:1: ",
		"vùng không có file .work: điều header làm là một component bên trong header.ui, viết bằng thẻ của nó (<dropdown>, <shop-cart>)")

	// control: the root of header.ui holds a component without a UI, read inside it
	html := mustCompile(t, map[string]string{
		"/site/view.ui":    `<view><header /><content /></view>`,
		"/site/header.ui":  `<header><dropdown as="$menu" keydown:escape="close()"><button click="toggle()" expanded="{{ open }}">Menu</button></dropdown></header>`,
		"/site/content.ui": content,
	})
	mustContain(t, html, `<header class="flex-column"><div class="flex-column" data-kitwork-component="dropdown: $menu" data-kitwork-event="keydown.escape: close()"><button`,
		`data-kitwork-bind="aria-expanded: open"`)
}

// component= is gone (18/10/2026): on the page's root, the route's content or the head of an accordion it is
// the removed attribute's error like anywhere else, while a component's tag as the head of an accordion is no
// head (the head becomes its <summary>). The root of a component's look is the component's tag, a box
// (28/10/2026), so the tags the root of a component's .ui could not be until then are no longer a question.
func TestForbiddenHosts(t *testing.T) {
	page := `<view component="site-menu"><content /></view>`
	mustFail(t, map[string]string{"/site/view.ui": page, "/site/content.ui": `<content><text>x</text></content>`}, "view.ui:1:7: ",
		"component= đã bỏ: component là một thẻ, viết <site-menu>…</site-menu> bên trong <view>")
	routeContent := `<content component="site-menu"><text>x</text></content>`
	mustFail(t, map[string]string{"/site/content.ui": routeContent}, "content.ui:1:10: ",
		"component= đã bỏ: component là một thẻ, viết <site-menu>…</site-menu> bên trong <content>")
	head := `<content><accordion><box component="site-menu" click="toggle()"><text>Q</text></box><text>A</text></accordion></content>`
	mustFail(t, map[string]string{"/site/content.ui": head}, where("content.ui", head, "component="),
		"component= đã bỏ: component là một thẻ, viết <site-menu>…</site-menu>")
	tagHead := `<content><accordion><dropdown><text>Q</text></dropdown><text>A</text></accordion></content>`
	mustFail(t, map[string]string{"/site/content.ui": tagHead}, where("content.ui", tagHead, "<dropdown"),
		"phần đầu của accordion là box hoặc text, không phải <dropdown>")
}

// The value of a removed component= names the tag to write instead: a name Kitwork has is that tag — a
// core tag, a region, a Kitwork element, one the engine does not build yet — and Kitwork's path form
// (kitwork/components/x), gone with it, is the tag of its name, no source (a site's name, a path of the
// site: tags_test.go). Until 18/10/2026 these were the rules of the value of component=.
func TestRemovedComponentValueNamesTheTag(t *testing.T) {
	for _, c := range []struct{ value, says string }{
		{`card`, "viết <card>…</card>"},
		{`navbar`, "viết <navbar>…</navbar>"},
		{`accordion`, "viết <accordion>…</accordion>"},
		{`dialog`, "viết <dialog>…</dialog>"},
		{`tooltip`, "viết <tooltip>…</tooltip>"},
		{`kitwork/components/dropdown`, "viết <dropdown>…</dropdown>"},
	} {
		content := `<content><box component="` + c.value + `"><text>x</text></box></content>`
		mustFail(t, map[string]string{"/site/content.ui": content}, "content.ui:1:15: ", "component= đã bỏ: component là một thẻ, "+c.says)
	}
}

// A component's look is looked up from the folder of the file that writes it, never from the route: what
// header.ui writes means the same on every page. A page takes one look per name found by name, each call
// told at its tag.
func TestLookupFromTheWritingFolder(t *testing.T) {
	logic := `import { component } from "kitwork"; component.define("mini-cart", () => { var open = false; });`
	files := map[string]string{
		"/site/view.ui":               `<view><header /><content /></view>`,
		"/site/header.ui":             `<header><mini-cart /></header>`,
		"/site/mini-cart.ui":          `<mini-cart><text>giỏ của site</text></mini-cart>`,
		"/site/shop/mini-cart.ui":     `<mini-cart><text>giỏ của shop</text></mini-cart>`,
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
	files["/site/shop/content.ui"] = `<content><mini-cart /></content>`
	_, err := memorySite("", files).Compile("/site/shop")
	if err == nil || !strings.HasPrefix(err.Error(), "shop/content.ui:1:10: ") ||
		!strings.Contains(err.Error(), "component mini-cart trên trang này có hai giao diện tìm theo tên: mini-cart.ui (header.ui:1:9) và shop/mini-cart.ui") {
		t.Errorf("two sources for one name on a page: %v", err)
	}
	// control: a page with only the branch's call takes the branch's look
	html = renderAt(t, memorySite("", map[string]string{
		"/site/shop/content.ui":       `<content><mini-cart /></content>`,
		"/site/shop/mini-cart.ui":     `<mini-cart><text>giỏ của shop</text></mini-cart>`,
		"/site/mini-cart.ui":          `<mini-cart><text>giỏ của site</text></mini-cart>`,
		"/.components/mini-cart.work": logic,
	}), "shop", "/shop", nil)
	mustContain(t, html, "giỏ của shop")
}

// A branch may give a component of the site a look of its own: a piece in the branch whose root is the
// component's tag, nearer than the site's. One of Kitwork's is never restyled by a file of the site: since
// 18/10/2026 a branch file under its name is an error at that file, as one at the site root is (tags_test.go),
// since its tag is always Kitwork's.
func TestBranchUIForAComponentWithoutUI(t *testing.T) {
	files := map[string]string{
		"/site/quan-ly/content.ui":  `<content><dropdown><button click="toggle()">x</button></dropdown></content>`,
		"/site/quan-ly/dropdown.ui": `<box><text>menu</text></box>`,
	}
	_, err := memorySite("", files).Compile("/site/quan-ly")
	if err == nil || !strings.HasPrefix(err.Error(), "quan-ly/dropdown.ui:1:1: ") ||
		!strings.Contains(err.Error(), "dropdown.ui đặt tên trùng component <dropdown> của Kitwork: <dropdown> luôn là component của Kitwork nên file này không bao giờ được gọi") {
		t.Errorf("a branch .ui named like one of Kitwork's components: %v", err)
	}
	// control: a branch gives a component its own look
	html := renderAt(t, memorySite("", map[string]string{
		"/site/quan-ly/content.ui":   `<content><shop-cart /></content>`,
		"/site/quan-ly/shop-cart.ui": `<shop-cart><text>giỏ quản lý {{ note }}</text></shop-cart>`,
		"/site/shop-cart.ui":         `<shop-cart><text>giỏ</text></shop-cart>`,
		"/site/shop-cart.work":       cartLogic,
	}), "quan-ly", "/quan-ly", nil)
	mustContain(t, html, "<p>giỏ quản lý chưa có</p>")
}

// A path, written source="…" on the tag, names the file of a look: ./ and ../ from the file that writes it,
// .components/ for the identity; the logic of the component its root is found by name (28/10/2026). It never
// leaves the site and ends in a name of the site, with a dash. Kitwork's path form (kitwork/components/x) went
// with component= (18/10/2026): a component of Kitwork is its tag, and that form is no path.
func TestComponentPaths(t *testing.T) {
	files := map[string]string{
		"/site/shop-cart.ui":          `<shop-cart><text>{{ note }}</text></shop-cart>`,
		"/.components/shop-cart.ui":   `<shop-cart><text>identity {{ note }}</text></shop-cart>`,
		"/.components/shop-cart.work": cartLogic,
	}
	at := func(content string) map[string]string {
		out := map[string]string{"/site/content.ui": content}
		for k, v := range files {
			out[k] = v
		}
		return out
	}
	// by name: the site's look, the identity's logic
	mustContain(t, mustCompile(t, at(`<content><shop-cart /></content>`)), "<p>chưa có</p>")
	// .components/ takes the identity's look
	mustContain(t, mustCompile(t, at(`<content><shop-cart source=".components/shop-cart" /></content>`)), "<p>identity chưa có</p>")
	// ./ takes the look at the site root; its root is the component, whose note is its own, not the page's
	html := renderAt(t, memorySite("", at(`<content><shop-cart source="./shop-cart" /></content>`)), ".", "/", map[string]any{"note": "của trang"})
	mustContain(t, html, `<div class="flex-column"><p>chưa có</p></div>`)
	mustNotContain(t, html, "của trang")
	for _, c := range []struct{ value, says string }{
		{`../../shop-cart`, "đường dẫn ../../shop-cart ra ngoài site"},
		{`shop/shop-cart`, `đường dẫn bắt đầu bằng ./ hoặc ../ (từ file đang viết) hoặc .components/ (của identity), không phải "shop/shop-cart"`},
		{`kitwork/components/shop-cart`, `đường dẫn bắt đầu bằng ./ hoặc ../ (từ file đang viết) hoặc .components/ (của identity), không phải "kitwork/components/shop-cart"`},
		{`./Cart`, `đường dẫn component kết thúc bằng tên component (viết thường, nối bằng gạch ngang), không phải "Cart"`},
		{`./cart`, `đường dẫn kết thúc bằng tên của site, có gạch ngang (product-card), không phải "cart": tên không gạch ngang là của Kitwork`},
		{`./no-thing`, `không tìm thấy no-thing (source="./no-thing")`},
	} {
		content := `<content><shop-cart source="` + c.value + `" /></content>`
		mustFail(t, at(content), where("content.ui", content, "source="), c.says)
	}
}

// The kernel keeps a for's row and a component's instance in one state slot, so a for or an if on a
// component's tag that reads live state is refused with a message that says so, and inside a component
// the browser runs an if or a for over its state is told to use show. Over the page's data a component
// may repeat: the server builds each row, each one its own instance. The live component around here is
// a dropdown: appearance, around until 18/10/2026, is a button now, and holds no box.
func TestLiveForAndIfOnAHost(t *testing.T) {
	for _, c := range []struct{ content, needle, says string }{
		{`<content><dropdown><button click="toggle()">Menu</button><appearance if="open">Đổi</appearance></dropdown></content>`, "if=",
			"if trên <appearance> đọc trạng thái sống (open): kernel chưa tách được hàng của for khỏi instance của component"},
		{`<content><dropdown><button click="toggle()">Menu</button><appearance for="x of open">Đổi</appearance></dropdown></content>`, "for=",
			"for trên <appearance> đọc trạng thái sống (open)"},
		{`<content><dropdown><button click="toggle()">Menu</button><site-note if="open" /></dropdown></content>`, "if=",
			"bên trong dropdown if theo trạng thái của component chưa chạy ở trình duyệt; dùng show"},
		{`<content><dropdown><button click="toggle()">Menu</button><site-note for="x of open" /></dropdown></content>`, "for=",
			"bên trong dropdown for theo trạng thái của component chưa chạy ở trình duyệt"},
	} {
		mustFail(t, map[string]string{"/site/content.ui": c.content, "/site/site-note.ui": `<card><text>x</text></card>`},
			where("content.ui", c.content, c.needle), c.says)
	}
	// control: over the page's data each row is its own instance, the for on the component's tag
	s := memorySite("", map[string]string{"/site/content.ui": `<content><dropdown for="x of xs; key: x"><button click="toggle()">{{ x }}</button></dropdown></content>`})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/", Scope: map[string]any{"xs": []any{"a", "b"}}})
	if got := strings.Count(html, `data-kitwork-component="dropdown"`); got != 2 {
		t.Errorf("one host per row, got %d:\n%s", got, html)
	}
	// control: show is no if — a show that reads live state is kept up to date by the kernel
	html = mustCompile(t, map[string]string{"/site/content.ui": `<content><dropdown><button click="toggle()">Menu</button><site-note show="open" /></dropdown></content>`,
		"/site/site-note.ui": `<card><text>x</text></card>`})
	mustContain(t, html, `<div class="card flex-column" data-kitwork-bind="show: open" hidden><p>x</p>`)
}

// A popover shows while the component around it is open: one that declares open and close.
func TestPopoverNeedsAnOpeningComponent(t *testing.T) {
	content := `<content><box><popover><text>x</text></popover></box></content>`
	mustFail(t, map[string]string{"/site/content.ui": content}, where("content.ui", content, "<popover"),
		"popover hiện khi component chứa nó mở: đặt nó trong một component có open và close (<dropdown>…<popover>…</popover></dropdown>)")
	inAppearance := `<content><appearance click="toggle()"><popover><text>x</text></popover></appearance></content>`
	mustFail(t, map[string]string{"/site/content.ui": inAppearance}, where("content.ui", inAppearance, "<popover"), "popover hiện khi component chứa nó mở")
	// control
	mustContain(t, mustCompile(t, map[string]string{"/site/content.ui": `<content><dropdown><button click="toggle()">x</button><popover><text>x</text></popover></dropdown></content>`}),
		`hidden data-kitwork-bind="show: open"`)
}

// An instance the kernel runs starts from its inputs: the server renders with them, and the host
// carries them per request as data-kitwork-scope, the page's data written as literals. A component
// the kernel does not run (a site component) gets none: its host is not marked yet.
func TestInputsSeedTheInstance(t *testing.T) {
	s := memorySite("", map[string]string{"/site/content.ui": `<content><dropdown open="{{ start }}"><button click="toggle()" expanded="{{ open }}">x</button></dropdown></content>`})
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/", Scope: map[string]any{"start": true}})
	mustContain(t, html, `data-kitwork-component="dropdown" data-kitwork-scope="open: true"`, `aria-expanded="true"`)
	html = page.Render(Request{Path: "/", Scope: map[string]any{"start": false}})
	mustContain(t, html, `data-kitwork-scope="open: false"`, `aria-expanded="false"`)
	// a value that reads a live component around the host (the terminal's copied) is the expression,
	// which the kernel runs in the scope around the host
	mustContain(t, mustCompile(t, map[string]string{"/site/content.ui": `<content><terminal><code>ls</code><dropdown open="{{ copied }}"><button click="close()">x</button></dropdown></terminal></content>`}),
		`data-kitwork-component="dropdown" data-kitwork-scope="open: copied"`)

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
		got := literal(c.value)
		if got != c.want {
			t.Errorf("literal(%v) = %s, want %s", c.value, got, c.want)
		}
		ir, err := hydrate.Compile(got)
		if err != nil {
			t.Errorf("the kernel cannot read %s: %v", got, err)
			continue
		}
		// it reads back as the value the server read
		if back, err := hydrate.Eval(ir, map[string]any{}); err != nil || literal(back) != got {
			t.Errorf("%s reads back as %v (%v)", got, back, err)
		}
	}
	// control: the site's cart takes its input on the server and carries no scope
	html = mustCompile(t, map[string]string{"/site/content.ui": `<content><shop-cart text="{{ 'x' }}" /></content>`,
		"/site/shop-cart.ui": `<shop-cart note="{{ text }}"><text>{{ note }}</text></shop-cart>`, "/site/shop-cart.work": cartLogic})
	mustContain(t, html, "<p>x</p>")
	mustNotContain(t, html, "data-kitwork-scope")
}

// A site component inside one the kernel runs: its own events are its own (its logic reaches the
// browser later), not page events inside the Kitwork component, and its names win inside its look.
func TestSiteComponentInsideAKitworkComponent(t *testing.T) {
	html := mustCompile(t, map[string]string{
		"/site/content.ui":   `<content><dropdown><button click="toggle()">x</button><shop-cart /></dropdown></content>`,
		"/site/shop-cart.ui": `<shop-cart click-outside="close()"><button click="toggle()">{{ note }}</button></shop-cart>`,
		"/site/shop-cart.work": `import { component } from "kitwork";
component.define("shop-cart", () => {
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
