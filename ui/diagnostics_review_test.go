package ui

import (
	"strings"
	"testing"
)

// What the review of decision D found (12/10/2026, docs/KITWORK_UI_DIAGNOSTICS.md): a warning must
// let the page render exactly as written, a typo in a value never hides behind a warning, and a message
// says what the author wrote. Each test holds a CONTROL.

// The progressbar is a call of Kitwork's component whose host is drawn — it holds the line — so
// presentation written there would do something the engine does not attach yet (question 5): an error,
// never "trang hiện như không viết nó". The toast was one until 17/10/2026; its stack is now the author's
// <box>, which takes what any box takes, the stack's own words read first (toast.go). The value is read
// first. CONTROL: a word that does nothing on the reader stays a warning, and the toast draws what is
// written on its host.
func TestReviewToastAndProgressbarHostsAreDrawn(t *testing.T) {
	bar := map[string]string{"/site/page.ui": `<page><box component="progressbar" opacity="50" /><content /></page>`, "/site/content.ui": `<content><text>x</text></content>`}
	mustFail(t, bar, "page.ui:1:36", "progressbar chỉ nhận color (màu của vạch), thickness (độ dày), delay (chờ bao lâu mới hiện) và alt (tên đọc được), không nhận opacity")
	// the value is read first: a typo is the table's error, on the toast's host as on any box
	mustFail(t, content(`<content>`+toastStack(` padding="bigg"`)+`</content>`), "content.ui:1:33", `padding theo thang khoảng cách, không có "bigg"`)
	mustFail(t, content(`<content>`+toastStack(` click="x(("`)+`</content>`), "content.ui:1:33", "biểu thức không hợp lệ: x((")
	// place is the stack's own word: an edge or a corner of the screen, not a place beside a parent
	mustFail(t, content(`<content>`+toastStack(` place="top-end-x"`)+`</content>`), "content.ui:1:33", "place của toast là một mép hay góc màn hình")

	// CONTROL: the toast draws its host as written; the reader's clipped region draws nothing
	html := compileWarns(t, content(`<content>`+toastStack(` gap="8" padding="4" background="brand" medium:gap="2" place="top"`)+`</content>`), "content.ui:1:15", "toast không có as")
	mustContain(t, html, `<div class="flex-column gap-8 padding-4 background-brand medium:gap-2 toast toast-top" data-kitwork-component="toast" role="region"`)
	reader := map[string]string{"/site/page.ui": `<page><content /><reader padding="4" /></page>`, "/site/content.ui": `<content><text>x</text></content>`}
	compileWarns(t, reader, "page.ui:1:26", "padding không có tác dụng trên reader")
}

// show on a popover: "true" says what the popover does anyway (a warning, the page as written), but
// "false" or a condition means to keep it hidden while open, which the engine does not draw, so left
// out the popover would show what was written hidden: an error at the attribute. CONTROL: show="true"
// warns and renders the popover as one with no show.
func TestReviewPopoverShowThatWouldHide(t *testing.T) {
	popover := func(show string) string {
		return `<content><box component="dropdown"><button click="toggle()">m</button><popover ` + show + `><text>x</text></popover></box></content>`
	}
	for _, show := range []string{`show="false"`, `show="ready"`, `show="!open"`} {
		source := popover(show)
		mustFail(t, content(source), where("content.ui", source, "show="),
			"popover hiện khi component chứa nó mở; "+show+" trên popover chưa được hỗ trợ (bỏ nó thì popover vẫn hiện khi mở)")
	}
	mustFail(t, content(popover(`show="x(("`)), "content.ui:1:80", "biểu thức không hợp lệ: x((")

	// CONTROL
	written := compileWarns(t, content(popover(`show="true"`)), "content.ui:1:80", "popover hiện khi component chứa nó mở; không viết show")
	if plain := compileQuiet(t, content(popover(""))); written != plain {
		t.Errorf("show=\"true\" renders the popover as one with no show:\n got  %s\n want %s", written, plain)
	}
}

// A place the page drops with a warning (on a fragment, on a reader) anchors nothing: the parent is
// drawn as it is without that place, not position:relative. What a fragment holds stands in its parent,
// so a child of it written with place anchors the parent. CONTROL: a box with place anchors its parent.
func TestReviewADroppedPlaceAnchorsNothing(t *testing.T) {
	for _, c := range []struct{ with, without, needle string }{
		{`<content><fragment if="true" place="top"><text>x</text></fragment></content>`, `<content><fragment if="true"><text>x</text></fragment></content>`, "place="},
		{`<content><text>x</text><reader as="$say" place="top" /></content>`, `<content><text>x</text><reader as="$say" /></content>`, "place="},
	} {
		html := compileWarns(t, content(c.with), where("content.ui", c.with, c.needle), "place không có tác dụng trên")
		mustNotContain(t, html, "place-anchor")
		s := memorySite("", content(c.without))
		s.Reload = true
		page, _ := compileDiagnose(t, s)
		if plain := page.Render(Request{Path: "/"}); html != plain {
			t.Errorf("%s renders as written without its place:\n got  %s\n want %s", c.with, html, plain)
		}
	}
	// CONTROL
	mustContain(t, compileQuiet(t, content(`<content><box place="top"><text>x</text></box></content>`)), `<main class="flex-column place-anchor">`)
	mustContain(t, compileQuiet(t, content(`<content><fragment if="true"><box place="top"><text>x</text></box></fragment></content>`)), `<main class="flex-column place-anchor">`)
}

// A word of .ui on a tag that does not read it is a warning only when its value reads (question 1): the
// words with a fixed set of values or a number are read as the tag that reads them would, so a typo in
// the value is an error even there. CONTROL: the same words with a value that reads warn.
func TestReviewValueOfAWordOnTheWrongTag(t *testing.T) {
	for _, c := range []struct{ source, needle, says string }{
		{`<content><box heading="9"><text>a</text></box></content>`, "heading=", "heading là số tĩnh từ 1 đến 6"},
		{`<content><box heading="abc"><text>a</text></box></content>`, "heading=", "heading là số tĩnh từ 1 đến 6"},
		{`<content><text type="emial">a</text></content>`, "type=", `của button là button, submit hoặc reset; không có "emial"`},
		{`<content><box expanded="yes"><text>a</text></box></content>`, "expanded=", `expanded chỉ nhận "true", "false" hoặc {{ }}`},
		{`<content><box delay="fast"><text>a</text></box></content>`, "delay=", `delay là số mili giây từ 0 đến 10000 (mặc định 120; 0 là hiện ngay), không phải "fast"`},
		{`<content><box inputmode="nmeric"><text>a</text></box></content>`, "inputmode=", `inputmode là none, text, decimal, numeric, tel, search, email hoặc url, không có "nmeric"`},
		{`<content><box autocomplete="emial"><text>a</text></box></content>`, "autocomplete=", `không có "emial"`},
		// the text's own reading: digits only, heading="+3" made an <h+3>
		{`<content><text heading="+3">a</text></content>`, "heading=", "heading là số tĩnh từ 1 đến 6"},
	} {
		mustFail(t, content(c.source), where("content.ui", c.source, c.needle), c.says)
	}
	// CONTROL
	for _, c := range []struct{ source, needle, says string }{
		{`<content><box heading="2"><text>a</text></box></content>`, "heading=", "heading không có tác dụng trên <box>"},
		{`<content><text type="email">a</text></content>`, "type=", "type không có tác dụng trên <text>"},
		{`<content><box expanded="{{ open }}"><text>a</text></box></content>`, "expanded=", "expanded không có tác dụng trên <box>"},
		{`<content><box delay="300"><text>a</text></box></content>`, "delay=", "delay không có tác dụng trên <box>"},
	} {
		compileWarns(t, content(c.source), where("content.ui", c.source, c.needle), c.says)
	}
}

// title names what names the element it is written on (question 2): the text inside, alt where the tag
// reads it, the alt of the icon or image inside a link or a button — never alt on a link, which does
// nothing there. CONTROL: following each message compiles with nothing to tell.
func TestReviewTitleSaysWhatNamesTheElement(t *testing.T) {
	for _, c := range []struct{ source, says string }{
		{`<content><link value="/" title="x"><icon value="home" /></link></content>`, `tên đọc được là chữ bên trong phần tử, hoặc alt trên icon hay ảnh bên trong (<icon value="x" alt="Đóng" />)`},
		{`<content><button title="x">y</button></content>`, `tên đọc được là chữ bên trong phần tử, hoặc alt trên icon hay ảnh bên trong`},
		{`<content><box title="x"><text>y</text></box></content>`, `tên đọc được là chữ bên trong phần tử, hoặc alt (<box alt="…">)`},
	} {
		mustFail(t, content(c.source), where("content.ui", c.source, "title="), "title là chữ của web, .ui không có; "+c.says)
	}
	quote := `<content><quote title="x"><text>y</text></quote></content>`
	if got := compileError(t, content(quote)); got != "content.ui:1:17: title là chữ của web, .ui không có; tên đọc được là chữ bên trong phần tử" {
		t.Errorf("a quote reads no alt, so its title is told of the text alone, got %s", got)
	}
	// CONTROL
	compileQuiet(t, content(`<content><link value="/"><icon value="home" alt="Trang chủ" /></link><button>y</button>`+
		`<box alt="Hộp"><text>y</text></box><quote><text>y</text></quote></content>`))
}

// A true-or-false attribute written bare reads as "true" only on a tag of Kitwork that keeps it (rows
// ui/host.go:87 and ui/layout.go:76 stay errors): at the call of a layout or a component it is an input,
// data the call passes, and has none. CONTROL: on an input, and on the host of a component without a UI
// for a word the host tag reads, it is house style.
func TestReviewBareInputAtACallNeedsAValue(t *testing.T) {
	layout := func(call string) map[string]string {
		return map[string]string{"/site/content.ui": `<content>` + call + `</content>`, "/site/product-card.ui": `<card><text>{{ open }}</text><text if="selected">s</text></card>`}
	}
	cart := map[string]string{"/site/content.ui": `<content><box component="cart" open /></content>`, "/site/cart.ui": `<box><text>{{ open }}</text></box>`,
		"/site/cart.work": `import { component } from "kitwork"; component.define("cart", () => { var open = false; });`}
	for _, c := range []struct {
		files    map[string]string
		at, says string
	}{
		{content(`<content><box component="dropdown" open><text>x</text></box></content>`), "content.ui:1:36", "open cần một giá trị"},
		{layout(`<product-card open />`), "content.ui:1:24", "open cần một giá trị"},
		{layout(`<product-card selected />`), "content.ui:1:24", "selected cần một giá trị"},
		{cart, "content.ui:1:32", "open cần một giá trị"},
	} {
		mustFail(t, c.files, c.at, c.says)
	}
	// CONTROL
	html := compileWarns(t, content(`<content><button component="appearance" click="toggle()" disabled>x</button></content>`), "content.ui:1:58", `viết disabled="true"`)
	mustContain(t, html, `type="button" disabled data-kitwork-event="click: toggle()">x</button>`)
	html = compileWarns(t, content(`<content><text strikethrough>x</text></content>`), "content.ui:1:16", `viết strikethrough="true"`)
	mustContain(t, html, `<s>x</s>`)
	mustContain(t, compileQuiet(t, layout(`<product-card open="true" />`)), `<p>true</p>`)
}

// An icon the set does not have draws nothing (jit/icons skips it), so a static name is looked up when
// the page compiles ("Chữ gõ sai đang biến mất im lặng", <icon value="hom" />): an error naming the
// nearest icons. CONTROL: a name the set has, and a name from data, compile.
func TestReviewIconNameIsLookedUp(t *testing.T) {
	mustFail(t, content(`<content><icon value="hom" /></content>`), "content.ui:1:16", `không có icon "hom" trong bộ icon của Kitwork (Tabler); gần nhất: home`)
	mustFail(t, content(`<content><button><icon value="hom" alt="Nhà" /></button></content>`), "content.ui:1:24", `không có icon "hom"`)
	mustFail(t, content(`<content><icon value="qqqqzzzzxxxx" /></content>`), "content.ui:1:16", `không có icon "qqqqzzzzxxxx" trong bộ icon của Kitwork (Tabler)`)
	if got := compileError(t, content(`<content><icon value="qqqqzzzzxxxx" /></content>`)); strings.Contains(got, "gần nhất") {
		t.Errorf("a name nothing is near names no icon: %s", got)
	}
	// CONTROL
	mustContain(t, compileQuiet(t, content(`<content><icon value="home" /><icon value="home-fill" /><icon value="mesh" /></content>`)),
		`<i class="icon-home" aria-hidden="true"></i>`, `<i class="icon-home-fill"`, `<i class="icon-mesh"`)
	s := memorySite("", content(`<content><icon value="{{ name }}" /></content>`))
	s.Reload = true
	if _, err := s.Compile("/site"); err != nil {
		t.Errorf("an icon from data is named where it renders: %v", err)
	}
}

// At the call of a region, a component or a layout a word the call does not take stays an error
// (question 5), but a typo is told as a typo first (question 1): a name .ui does not have, a value the
// table refuses. And alt at a call is the root's attribute, not its presentation. CONTROL: a known word
// with a value that reads gets the call's own sentence.
func TestReviewCallsReadTheirWordsFirst(t *testing.T) {
	text := `<content><text>x</text></content>`
	region := func(call string) map[string]string {
		return map[string]string{"/site/page.ui": `<page>` + call + `<content /></page>`, "/site/header.ui": `<header><text>H</text></header>`, "/site/content.ui": text}
	}
	cart := func(call string) map[string]string {
		return map[string]string{"/site/content.ui": `<content>` + call + `</content>`, "/site/cart.ui": `<box><text>{{ count }}</text></box>`,
			"/site/cart.work": `import { component } from "kitwork"; component.define("cart", () => { var count = 0; });`}
	}
	promo := func(call string) map[string]string {
		return map[string]string{"/site/content.ui": `<content>` + call + `</content>`, "/site/promo.ui": `<card><text>x</text></card>`}
	}
	for _, c := range []struct {
		files    map[string]string
		at, says string
	}{
		{region(`<header gpa="4" />`), "page.ui:1:15", "thuộc tính gpa không có trong .ui; thẻ vùng <header /> ở chỗ gọi chỉ nhận source, if, show"},
		{region(`<header class="x" />`), "page.ui:1:15", "class là chữ của web"},
		{region(`<header background="papr" />`), "page.ui:1:15", `background="papr": "papr" không có trong bảng màu của trang`},
		{cart(`<box component="cart" background="papr" />`), "content.ui:1:32", `background="papr": "papr" không có trong bảng màu của trang`},
		{cart(`<box component="cart" click="x((" />`), "content.ui:1:32", "biểu thức không hợp lệ: x(("},
		{cart(`<box component="cart" alt="x" />`), "content.ui:1:32", "alt là thuộc tính của <box>, gốc của cart: viết nó trong cart.ui"},
		{promo(`<promo background="papr" />`), "content.ui:1:17", `background="papr": "papr" không có trong bảng màu của trang`},
		{promo(`<promo alt="x" />`), "content.ui:1:17", "alt là thuộc tính của <card>, gốc của promo: viết nó trong promo.ui"},
	} {
		mustFail(t, c.files, c.at, c.says)
	}
	// CONTROL
	for _, c := range []struct {
		files    map[string]string
		at, says string
	}{
		{region(`<header background="paper" />`), "page.ui:1:15", "thẻ vùng <header /> ở chỗ gọi chỉ nhận source, if, show; background đặt trên gốc của file header.ui"},
		{region(`<header id="top" />`), "page.ui:1:15", "id đặt trên gốc của file header.ui"},
		{cart(`<box component="cart" background="paper" />`), "content.ui:1:32", "trình bày của cart nằm trong cart.ui"},
		{cart(`<box component="cart" click="add()" />`), "content.ui:1:32", "sự kiện click đặt trong cart.ui"},
		{promo(`<promo background="paper" />`), "content.ui:1:17", "trình bày của promo nằm trong promo.ui"},
	} {
		mustFail(t, c.files, c.at, c.says)
	}
}

// inputmode and autocomplete are copied to the <input> as written, and a browser ignores a word it does
// not know: a typo never vanishes (the case of type="emial"). CONTROL: HTML's own values, autocomplete's
// tokens in order, and a value from data are written as they are.
func TestReviewInputWordsCopiedAsWritten(t *testing.T) {
	for _, c := range []struct{ source, needle, says string }{
		{`<content><input name="q" alt="Q" inputmode="nmeric"></content>`, "inputmode=", `inputmode là none, text, decimal, numeric, tel, search, email hoặc url, không có "nmeric"`},
		{`<content><input name="q" alt="Q" autocomplete="emial"></content>`, "autocomplete=", `autocomplete là on, off hoặc tên trường của HTML`},
		{`<content><input name="q" alt="Q" autocomplete="email shipping"></content>`, "autocomplete=", `không có "email shipping"`},
		{`<content><input name="q" alt="Q" autocomplete="home username"></content>`, "autocomplete=", `không có "home username"`},
	} {
		mustFail(t, content(c.source), where("content.ui", c.source, c.needle), c.says)
	}
	// CONTROL
	html := compileQuiet(t, content(`<content><input name="a" alt="A" inputmode="numeric" autocomplete="one-time-code">`+
		`<input name="b" alt="B" autocomplete="section-giao shipping mobile tel webauthn"><input name="c" alt="C" autocomplete="off">`+
		`<input name="d" alt="D" autocomplete="{{ field }}"><input name="e" alt="E" inputmode="Numeric" autocomplete="Email"></content>`)) // HTML reads them without case
	mustContain(t, html, `inputmode="numeric"`, `autocomplete="one-time-code"`, `autocomplete="section-giao shipping mobile tel webauthn"`, `autocomplete="off"`)
}

// A tag written with capitals that names nothing read lowercase is named as written, and when its
// capitals stand for dashes and a layout has that name, the message says how to call it (the React
// habit <ProductCard />). CONTROL: capitals that name a layout read lowercase render with a warning.
func TestReviewCapitalTagThatNamesNothing(t *testing.T) {
	card := map[string]string{"/site/content.ui": `<content><ProductCard /></content>`, "/site/product-card.ui": `<card><text>x</text></card>`}
	if got := compileError(t, card); got != "content.ui:1:10: không biết thẻ <ProductCard>: tên viết thường, các chữ nối bằng gạch ngang; layout product-card.ui gọi bằng <product-card />" {
		t.Errorf("<ProductCard> beside product-card.ui:\n got %s", got)
	}
	cart := map[string]string{"/site/content.ui": `<content><ShopCart /></content>`, "/site/shop-cart.ui": `<box><text>{{ count }}</text></box>`,
		"/site/shop-cart.work": `import { component } from "kitwork"; component.define("shop-cart", () => { var count = 0; });`}
	mustFail(t, cart, "content.ui:1:10", `không biết thẻ <ShopCart>: tên viết thường, các chữ nối bằng gạch ngang; shop-cart có logic (shop-cart.work): viết <box component="shop-cart" />`)
	mustFail(t, content(`<content><ProductCard /></content>`), "content.ui:1:10", "không biết thẻ <ProductCard> (đọc là <productcard>): không có trong từ vựng của Kitwork, không có layout productcard.ui")
	// CONTROL
	promo := map[string]string{"/site/content.ui": `<content><Promo /></content>`, "/site/promo.ui": `<card><text>x</text></card>`}
	mustContain(t, compileWarns(t, promo, "content.ui:1:10", "tên thẻ <Promo> viết thường: <promo>"), `<div class="card flex-column"><p>x</p></div>`)
}
