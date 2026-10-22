package ui

import (
	"strings"
	"testing"
)

// Every component is written as its tag (KITWORK_UI §13, 18/10/2026): one of Kitwork's by a name
// without a dash (<tabs>, <appearance>), one of the site's by a name with one (<shop-cart>). These tests
// hold the edges of that rule; the forms themselves are exercised by every component's own tests.

// shopCartLogic is a site component with one state and one method.
const shopCartLogic = `import { component } from "kitwork";

component.define("shop-cart", () => {
  var open = false;
  const toggle = () => { open = !open; };
});
`

// component= is no longer a word of .ui: written anywhere, it is an error at the attribute that writes
// the tag instead, with the author's alias and whether the call is empty or holds a UI. A name without a
// dash that Kitwork has is that tag (an element too: reader); a site's name without one is told to take
// one; a path becomes a source. The tag a component builds on is Kitwork's, so a host the author chose
// (card) is told to go inside, and a region, which holds a component and never is one, is told to hold it.
// A value no tag could take ({{ }}, two names, capitals, nothing) writes no tag.
// CONTROL: the check in element() taken out, the region's note, the dash hint, the source, the alias,
// the note for a host the author chose, or the check of the value each turned this red (run 18/10/2026).
func TestComponentAttributeIsRemoved(t *testing.T) {
	const removed = "component= đã bỏ: component là một thẻ, "
	for _, c := range []struct{ name, source, says string }{
		{"one of Kitwork's", `<box component="tabs" as="$files"><button click="select('a')">A</button></box>`,
			`viết <tabs as="$files">…</tabs>`},
		{"on a host the author chose", `<card component="dropdown"><button click="toggle()">Mở</button><popover><text>a</text></popover></card>`,
			`viết <dropdown>…</dropdown> (<dropdown> dựng một <box>; cần <card> thì đặt nó bên trong)`},
		{"on the tag it builds", `<button component="appearance" click="toggle()">Đổi</button>`,
			`viết <appearance>…</appearance>`},
		{"a site name without a dash", `<box component="cart" as="$cart" />`,
			`viết <shop-cart as="$cart" />; tên component của site có gạch ngang, tên không gạch ngang là của Kitwork: đổi tên cart thành shop-cart`},
		{"a path", `<box component="./mini-cart" />`, `viết <mini-cart source="./mini-cart" />`},
		{"an element of Kitwork", `<box component="reader" />`, `viết <reader />`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + c.source + `</content>`
			mustFailExactly(t, content(source), where("content.ui", source, `component=`)+removed+c.says)
		})
	}
	t.Run("on the root of a region's file", func(t *testing.T) {
		header := `<header component="appearance"><text>x</text></header>`
		mustFailExactly(t, map[string]string{
			"/site/page.ui":    `<page><header /><content /></page>`,
			"/site/header.ui":  header,
			"/site/content.ui": `<content><text>x</text></content>`,
		}, where("header.ui", header, `component=`)+removed+"viết <appearance>…</appearance> bên trong <header>")
	})
	// a value no tag could take writes no tag: it says what a component's name is instead
	for _, value := range []string{`{{ kind }}`, `cart dropdown`, `productCard`, ``, `./`} {
		t.Run("the value "+value, func(t *testing.T) {
			source := `<content><box component="` + value + `"><text>a</text></box></content>`
			mustFailExactly(t, content(source), where("content.ui", source, `component=`)+removed+
				"tên của nó là tên thẻ, viết thường, cố định (<tabs>, <shop-cart>)")
		})
	}
}

// A Kitwork component's tag builds one element, its host, on the tag Kitwork chose: a button for
// appearance (what the visitor presses), a box for the others. The page carries what the kernel needs
// on it as before (data-kitwork-component, with the alias), and the tag is never an element of the web.
// CONTROL: element() sending the tag to layoutCall (no kitworkCall) or appearance built on a box each
// turned this red (run 18/10/2026).
func TestKitworkComponentTagsBuildTheirHost(t *testing.T) {
	html := mustCompile(t, content(`<content><appearance click="toggle()">Đổi</appearance><dropdown as="$menu"><button click="toggle()">Mở</button><popover><text>a</text></popover></dropdown></content>`))
	const button = `class="flex-row background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4"`
	mustContain(t, html, `<main class="flex-column">`+
		`<button `+button+` data-kitwork-component="appearance" type="button" data-kitwork-event="click: toggle()">Đổi</button>`+
		`<div class="flex-column place-anchor" data-kitwork-component="dropdown: $menu">`+
		`<button `+button+` type="button" data-kitwork-event="click: toggle()">Mở</button>`+
		`<div class="popover flex-column" hidden data-kitwork-bind="show: open"><p>a</p></div></div></main>`)
	mustNotContain(t, html, "<appearance", "<dropdown")
}

// A name without a dash is Kitwork's, so a tag outside the vocabulary without one names nothing; a piece
// of the site in reach under that name, a .ui or a .work, is told to take a dash, by its file.
// CONTROL: withoutADash taken out (the name looked up as the site's) turned the first two red; the
// lookup of the files in it, the last two (run 18/10/2026).
func TestSiteNamesTakeADash(t *testing.T) {
	const call = `<content><cart /></content>`
	at := where("content.ui", call, `<cart`)
	logic := `import { component } from "kitwork";

component.define("cart", () => {
  var open = false;
});
`
	for _, c := range []struct {
		name  string
		files map[string]string
		says  string
	}{
		{"nothing of that name", content(call),
			"không biết thẻ <cart>: Kitwork không có thẻ này, và tên layout hay component của site có gạch ngang (<product-card>, <shop-cart>)"},
		{"a layout of that name", map[string]string{"/site/content.ui": call, "/site/cart.ui": `<box><text>a</text></box>`},
			"<cart> không có gạch ngang nên là thẻ của Kitwork, mà Kitwork không có thẻ này: tên layout và component của site có gạch ngang; đổi tên cart.ui (vd. shop-cart) rồi gọi <shop-cart>"},
		{"logic of that name", map[string]string{"/site/content.ui": call, "/site/cart.work": logic},
			"<cart> không có gạch ngang nên là thẻ của Kitwork, mà Kitwork không có thẻ này: tên layout và component của site có gạch ngang; đổi tên cart.work (vd. shop-cart) rồi gọi <shop-cart>"},
		{"logic of the identity", map[string]string{"/site/content.ui": call, "/.components/cart.work": logic},
			"<cart> không có gạch ngang nên là thẻ của Kitwork, mà Kitwork không có thẻ này: tên layout và component của site có gạch ngang; đổi tên ../.components/cart.work (vd. shop-cart) rồi gọi <shop-cart>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			mustFailExactly(t, c.files, at+c.says)
		})
	}
}

// A file of the site named like one of Kitwork's components is never read, since the tag is always
// Kitwork's: a .ui in reach, or logic that defines the name, is an error at that file.
// CONTROL: namedLikeATag not asked for a Kitwork component, or the logic of the site not looked for
// under Kitwork's name, each turned its case red: the page compiled (run 18/10/2026).
func TestSiteFileNamedLikeAKitworkComponent(t *testing.T) {
	t.Run("a .ui", func(t *testing.T) {
		mustFailExactly(t, map[string]string{
			"/site/content.ui": `<content><tabs><button click="select('a')">A</button></tabs></content>`,
			"/site/tabs.ui":    `<box><text>a</text></box>`,
		}, "tabs.ui:1:1: tabs.ui đặt tên trùng component <tabs> của Kitwork: <tabs> luôn là component của Kitwork nên file này không bao giờ được gọi; đặt cho layout tên khác (viết thường, nối bằng gạch ngang) rồi gọi bằng tên đó")
	})
	t.Run("logic", func(t *testing.T) {
		mustFailExactly(t, map[string]string{
			"/site/content.ui":      `<content><dropdown><button click="toggle()">A</button></dropdown></content>`,
			"/site/components.work": "import { component } from \"kitwork\";\n\ncomponent.define(\"dropdown\", () => {\n  var open = false;\n});\n",
		}, `components.work:1:1: component.define("dropdown", …) đặt tên trùng thẻ <dropdown> của Kitwork: <dropdown> luôn là component của Kitwork nên logic này không bao giờ được đọc; tên component của site có gạch ngang (shop-dropdown)`)
	})
}

// A site component is called by its tag, as a layout is: without a .ui it builds on a box and what is
// written inside is its UI, reading its names first (open is the cart's false, not missing); with a .ui
// the call is empty and the file's root is the element. source names the file, here under another tag.
// CONTROL: layoutCall compiling a name with logic as a layout turned the first case red (open read as
// the page's, nothing); the source dropped from the host, the last (run 18/10/2026).
func TestSiteComponentByItsTag(t *testing.T) {
	t.Run("without a UI", func(t *testing.T) {
		html := mustCompile(t, map[string]string{
			"/site/content.ui":     `<content><shop-cart as="$cart"><text>{{ open ? 'Đóng' : 'Mở' }}</text></shop-cart></content>`,
			"/site/shop-cart.work": shopCartLogic,
		})
		mustContain(t, html, `<main class="flex-column"><div class="flex-column"><p>Mở</p></div></main>`)
	})
	t.Run("with a UI", func(t *testing.T) {
		html := mustCompile(t, map[string]string{
			"/site/content.ui":     `<content><shop-cart as="$cart" /></content>`,
			"/site/shop-cart.work": shopCartLogic,
			"/site/shop-cart.ui":   `<card padding="2"><text>{{ open }}</text></card>`,
		})
		mustContain(t, html, `<main class="flex-column"><div class="card flex-column padding-2"><p>false</p></div></main>`)
	})
	t.Run("by its source", func(t *testing.T) {
		html := mustCompile(t, map[string]string{
			"/site/content.ui":     `<content><my-cart source="./shop-cart" /></content>`,
			"/site/shop-cart.work": shopCartLogic,
			"/site/shop-cart.ui":   `<card padding="2"><text>{{ open }}</text></card>`,
		})
		mustContain(t, html, `<main class="flex-column"><div class="card flex-column padding-2"><p>false</p></div></main>`)
	})
}

// The root of a layout's file takes the call's words (for, if, show, span, place), and the root of a
// component's .ui is that component's own element: neither is a component's tag (one element, one
// component), and each is told to wrap it in a box.
// CONTROL: the component-tag case of checkLayoutRoot or of checkComponentRoot taken out turned its case
// red (run 18/10/2026).
func TestComponentTagIsNoRoot(t *testing.T) {
	t.Run("of a layout", func(t *testing.T) {
		mustFailExactly(t, map[string]string{
			"/site/content.ui":   `<content><promo-box /></content>`,
			"/site/promo-box.ui": `<tabs><button click="select('a')">A</button></tabs>`,
		}, "promo-box.ui:1:1: gốc của promo-box.ui là <tabs>, một component: chỗ gọi <promo-box /> đặt for, if, show, span, place lên chính gốc này, nên gốc không là một component; bọc <tabs> trong một <box>")
	})
	t.Run("of a component", func(t *testing.T) {
		mustFailExactly(t, map[string]string{
			"/site/content.ui":     `<content><shop-cart /></content>`,
			"/site/shop-cart.work": shopCartLogic,
			"/site/shop-cart.ui":   `<dropdown><button click="toggle()">A</button></dropdown>`,
		}, "shop-cart.ui:1:1: gốc của shop-cart.ui là <dropdown>: gốc là phần tử của chính shop-cart, mà một phần tử một component; bọc <dropdown> trong một <box>")
	})
}

// orderRowLogic is a site component with one state, for the tests of a piece whose root is an item.
const orderRowLogic = "import { component } from \"kitwork\";\n\ncomponent.define(\"order-row\", () => {\n  var open = false;\n});\n"

// A parent's rule for its children reads the tag a piece of the site builds, the root of its file (the
// box a component without a .ui builds): a layout or a component whose root is an item is the list's
// item, in a list and in a breadcrumb; any other root is told by its file. A piece whose root is an
// item, wrapped in an item, would put an item inside an item: an error, not <li><li>.
// CONTROL: the list's and the breadcrumb's check reading the tag as written turned the first, second
// and fifth cases red (the error a component of 18/10 brought back); itemHoldsNoItem not asked, the
// fourth (run 18/10/2026).
func TestParentReadsTheRootOfAPiece(t *testing.T) {
	t.Run("a component whose root is an item, in a list", func(t *testing.T) {
		html := mustCompile(t, map[string]string{
			"/site/content.ui":     `<content><list><order-row for="o of [1, 2]" /></list></content>`,
			"/site/order-row.work": orderRowLogic,
			"/site/order-row.ui":   `<item><text>{{ open }}</text></item>`,
		})
		mustContain(t, html, `<ul class="flex-column" role="list"><li class="flex-column"><p>false</p></li><li class="flex-column"><p>false</p></li></ul>`)
	})
	t.Run("a layout whose root is an item, in a list", func(t *testing.T) {
		html := mustCompile(t, map[string]string{
			"/site/content.ui":    `<content><list><order-line /></list></content>`,
			"/site/order-line.ui": `<item><text>y</text></item>`,
		})
		mustContain(t, html, `<ul class="flex-column" role="list"><li class="flex-column"><p>y</p></li></ul>`)
	})
	t.Run("roots that are no item", func(t *testing.T) {
		source := `<content><list><product-card /></list></content>`
		mustFailExactly(t, map[string]string{"/site/content.ui": source, "/site/product-card.ui": `<card><text>y</text></card>`},
			where("content.ui", source, `<product-card`)+"con của list là item; product-card.ui có gốc <card>: bọc <product-card> trong <item>")
		source = `<content><list><order-row><text>a</text></order-row></list></content>`
		mustFailExactly(t, map[string]string{"/site/content.ui": source, "/site/order-row.work": orderRowLogic},
			where("content.ui", source, `<order-row`)+"con của list là item; order-row (order-row.work) dựng một <box>: bọc <order-row> trong <item>")
	})
	t.Run("an item around a piece whose root is an item", func(t *testing.T) {
		source := `<content><list><item><order-line /></item></list></content>`
		mustFailExactly(t, map[string]string{"/site/content.ui": source, "/site/order-line.ui": `<item><text>y</text></item>`},
			where("content.ui", source, `<order-line`)+"order-line.ui có gốc <item>: <order-line> đã là một mục, đặt nó thẳng trong <list>, không bọc trong <item>")
	})
	t.Run("an item of a breadcrumb around a piece whose root is an item", func(t *testing.T) {
		source := `<content><breadcrumb><item><order-line /></item></breadcrumb></content>`
		mustFailExactly(t, map[string]string{"/site/content.ui": source, "/site/order-line.ui": `<item><link value="/a">A</link></item>`},
			where("content.ui", source, `<order-line`)+"order-line.ui có gốc <item>: <order-line> đã là một mục, đặt nó thẳng trong <breadcrumb>, không bọc trong <item>")
	})
	t.Run("a piece whose root is an item, in a breadcrumb", func(t *testing.T) {
		html := mustCompile(t, map[string]string{
			"/site/content.ui":    `<content><breadcrumb><item><link value="/">Home</link></item><order-line /></breadcrumb></content>`,
			"/site/order-line.ui": `<item><link value="/a">A</link></item>`,
		})
		mustContain(t, html, `<ol class="breadcrumb flex-row" role="list"><li class="flex-row"><a href="/" aria-current="page">Home</a></li><li class="flex-row"><a href="/a">A</a></li></ol>`)
	})
}

// What a component's call says, it says in the author's words: the tag written, also with a source
// (<my-row>, not the file's name), and a message that writes a fix writes that tag, not the box it
// builds (a title's alt, a gradient's fix, the colour a contrast fix moves, a lazy frame's warning).
// CONTROL: called() reading the file's name, tagOf not asked by webWord, by the gradient's two messages,
// by the contrast fix or by the lazy warning each turned its case red (run 18/10/2026).
func TestMessagesNameTheTagWritten(t *testing.T) {
	errs := []struct{ name, source, needle, says string }{
		{"a title's alt", `<dropdown title="Menu"><text>a</text></dropdown>`, `title=`,
			`title là chữ của web, .ui không có; tên đọc được là chữ bên trong phần tử, hoặc alt (<dropdown alt="…">)`},
		{"a gradient's fix", `<dropdown background="ink" color="prism"><text>a</text></dropdown>`, `color=`,
			`color="prism" là chữ gradient: gradient được cắt theo nét chữ (background-clip:text), nên background="ink" trên cùng phần tử cũng bị cắt theo nét chữ, không còn là nền. Để chữ gradient trên một <text> riêng bên trong: <dropdown background="ink"><text color="prism">…</text></dropdown>`},
		{"a gradient's fix on the button appearance builds", `<appearance click="toggle()" background="ink" color="prism"><text>a</text></appearance>`, `color=`,
			`color="prism" là chữ gradient: gradient được cắt theo nét chữ (background-clip:text), nên background="ink" trên cùng phần tử cũng bị cắt theo nét chữ, không còn là nền. Để chữ gradient trên một <text> riêng bên trong: <appearance background="ink"><text color="prism">…</text></appearance>`},
		{"what covers a gradient", `<box color="prism"><dropdown background="ink"><text>a</text></dropdown></box>`, `color=`,
			`color="prism" là chữ gradient: chữ bên trong thừa hưởng nét tô trong suốt của nó (-webkit-text-fill-color), và <dropdown> bên trong có nền background="ink" phủ lên gradient, nên chữ của <dropdown> không hiện. Để chữ gradient trên một <text> riêng không bọc phần tử có nền (<text color="prism">…</text>), hoặc cho <dropdown> màu chữ riêng (color="ink")`},
	}
	for _, c := range errs {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + c.source + `</content>`
			mustFailExactly(t, content(source), where("content.ui", source, c.needle)+c.says)
		})
	}
	t.Run("a call by its source", func(t *testing.T) {
		source := `<content><my-row source="./order-row" /></content>`
		mustFailExactly(t, map[string]string{"/site/content.ui": source, "/site/order-row.work": orderRowLogic},
			where("content.ui", source, `<my-row`)+"<my-row> không có giao diện: nội dung bên trong thẻ là giao diện của nó (<my-row>…</my-row>)")
	})
	t.Run("the colour a contrast fix moves", func(t *testing.T) {
		source := `<content><dropdown background="navy/50"><text color="white">Trà</text></dropdown></content>`
		s := memorySite("", content(source))
		s.Reload = true
		s.Palette = darkPalette(t)
		compileWarnsOn(t, s, where("content.ui", source, `color=`),
			`Dùng background="navy" trên <dropdown> (`+strings.TrimSuffix(where("content.ui", source, `<dropdown`), ": ")+`)`)
	})
	t.Run("a lazy frame's warning", func(t *testing.T) {
		source := `<content><section lazy="true"><box><dropdown place="below-left"><text>a</text></dropdown></box></section></content>`
		compileWarns(t, content(source), where("content.ui", source, `lazy=`), `khung này giữ <dropdown place="below-left"> (đặt ra ngoài cha)`)
	})
}

// The edges of a call of a site component: a bare true-or-false word of the box it builds reads as
// "true", told, as on one of Kitwork's; a source is the site's word, refused on a tag of Kitwork; a
// prefixed component= is the removed attribute too, never a word that vanishes; misplaced dashes are
// told the name they meant; logic in components.work is renamed at its define; a component= path whose
// name takes a dash renames the path's end with it.
// CONTROL: the bare word left empty in componentCall, the source let through on a Kitwork tag, the
// prefixed attribute skipped, dashesWrong not asked, the components.work wording, and the source path
// kept as written each turned its case red (run 18/10/2026).
func TestSiteCallEdges(t *testing.T) {
	t.Run("a bare true-or-false word", func(t *testing.T) {
		source := `<content><order-row italic><text>{{ open }}</text></order-row></content>`
		html := compileWarns(t, map[string]string{"/site/content.ui": source, "/site/order-row.work": orderRowLogic},
			where("content.ui", source, `italic`), `thuộc tính đúng/sai luôn có giá trị: viết italic="true" (viết trơn đọc là true)`)
		mustContain(t, html, `<div class="flex-column italic-true"><p>false</p></div>`)
	})
	t.Run("a source on a tag of Kitwork", func(t *testing.T) {
		source := `<content><dropdown source="./x"><text>a</text></dropdown></content>`
		mustFailExactly(t, content(source), where("content.ui", source, `source=`)+"source lấy một file của site; <dropdown> là của Kitwork, không có source")
	})
	for _, c := range []struct{ name, source, needle, says string }{
		{"a prefixed component= on a Kitwork tag", `<dropdown medium:component="tabs"><text>a</text></dropdown>`, `medium:component=`,
			"component= đã bỏ: component là một thẻ, viết <tabs>…</tabs>"},
		{"misplaced dashes", `<shop-cart- />`, `<shop-cart-`,
			"không biết thẻ <shop-cart->: tên layout và component của site viết thường, các từ nối bằng một gạch ngang (<shop-cart>)"},
		{"a path whose name takes a dash", `<box component="./cart" />`, `component=`,
			`component= đã bỏ: component là một thẻ, viết <shop-cart source="./shop-cart" />; tên component của site có gạch ngang, tên không gạch ngang là của Kitwork: đổi tên cart thành shop-cart`},
		{"a path of the identity", `<box component=".components/cart" />`, `component=`,
			`component= đã bỏ: component là một thẻ, viết <shop-cart source=".components/shop-cart" />; tên component của site có gạch ngang, tên không gạch ngang là của Kitwork: đổi tên cart thành shop-cart`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + c.source + `</content>`
			mustFailExactly(t, content(source), where("content.ui", source, c.needle)+c.says)
		})
	}
	t.Run("a prefixed component= on a layout", func(t *testing.T) {
		source := `<content><product-card medium:component="x" /></content>`
		mustFailExactly(t, map[string]string{"/site/content.ui": source, "/site/product-card.ui": `<card><text>y</text></card>`},
			where("content.ui", source, `medium:component=`)+"component= đã bỏ: component là một thẻ, viết <shop-x />; tên component của site có gạch ngang, tên không gạch ngang là của Kitwork: đổi tên x thành shop-x")
	})
	t.Run("misplaced dashes with a file", func(t *testing.T) {
		source := `<content><shop--cart /></content>`
		mustFailExactly(t, map[string]string{"/site/content.ui": source, "/site/shop--cart.ui": `<box><text>a</text></box>`},
			where("content.ui", source, `<shop--cart`)+"tên layout và component của site viết thường, các từ nối bằng một gạch ngang: đổi tên shop--cart.ui thành shop-cart.ui rồi gọi <shop-cart>")
	})
	t.Run("logic in components.work", func(t *testing.T) {
		source := `<content><cart /></content>`
		mustFailExactly(t, map[string]string{"/site/content.ui": source,
			"/site/components.work": "import { component } from \"kitwork\";\n\ncomponent.define(\"cart\", () => {\n  var open = false;\n});\n"},
			where("content.ui", source, `<cart`)+`<cart> không có gạch ngang nên là thẻ của Kitwork, mà Kitwork không có thẻ này: tên layout và component của site có gạch ngang; trong components.work đổi component.define("cart", …) thành component.define("shop-cart", …) rồi gọi <shop-cart>`)
	})
}

// Logic of the site named like a core tag or a Kitwork element is never read, since the tag is always
// Kitwork's: an error at that file, as a .ui of that name is, asked once per name for the page. An as on
// an empty region call names nothing: a region is a layout, so as goes on a component inside it.
// CONTROL: logicNamedLikeATag not asked turned the first two cases red (the page compiled); the as case
// in region(), the last (it said to move as onto the root, where it does nothing) (run 18/10/2026).
func TestNamesNoComponentTakes(t *testing.T) {
	t.Run("logic named like a core tag", func(t *testing.T) {
		mustFailExactly(t, map[string]string{
			"/site/content.ui": `<content><card><text>a</text></card></content>`,
			"/site/card.work":  "import { component } from \"kitwork\";\n\ncomponent.define(\"card\", () => {\n  var open = false;\n});\n",
		}, `card.work:1:1: component.define("card", …) đặt tên trùng thẻ lõi <card> của Kitwork: <card> luôn là thẻ lõi của Kitwork nên logic này không bao giờ được đọc; tên component của site có gạch ngang (shop-card)`)
	})
	t.Run("logic named like an element", func(t *testing.T) {
		mustFailExactly(t, map[string]string{
			"/site/content.ui":      `<content><text>a</text></content>`,
			"/site/page.ui":         `<page><content /><reader as="$r" /></page>`,
			"/site/components.work": "import { component } from \"kitwork\";\n\ncomponent.define(\"reader\", () => {\n  var open = false;\n});\n",
		}, `components.work:1:1: component.define("reader", …) đặt tên trùng phần tử <reader> của Kitwork: <reader> luôn là phần tử của Kitwork nên logic này không bao giờ được đọc; tên component của site có gạch ngang (shop-reader)`)
	})
	t.Run("as on an empty region call", func(t *testing.T) {
		page := `<page><header as="$h" /><content /></page>`
		mustFailExactly(t, map[string]string{"/site/page.ui": page, "/site/header.ui": `<header><text>a</text></header>`, "/site/content.ui": `<content><text>x</text></content>`},
			where("page.ui", page, `as=`)+`as đặt tên cho một instance; vùng <header> là layout, không có instance để gọi: bỏ as, hoặc đặt nó trên một component bên trong header.ui (<dropdown as="$h">)`)
	})
}
