package ui

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/hydrate"
)

// cartLogic is the cart of KITWORK_UI §17, cut to what these tests read: state written out, a value
// computed by a function, and the bare-`var` form the parser produces today.
const cartLogic = `import { component } from "kitwork";

component.define("cart", () => {
  var open = false;
  var items = [];
  var note = "chưa có";
  const shipping = { fee: 15000, free: false };
  const total = () => {
    var sum = 0;
    for (const item of items) {
      sum = sum + item.price;
    }
    return sum;
  };
});
`

const cartUI = `<box><text>{{ items.length }} món · {{ note }} · {{ shipping.fee }}</text><text show="open">mở</text><text>Tạm tính {{ money(total()) }}</text><text>{{ user }}</text></box>`

// where is "file:line:col: " of the first needle in a source, columns counted in runes like the parser.
func where(file, source, needle string) string {
	i := strings.Index(source, needle)
	if i < 0 {
		panic("no " + needle + " in " + source)
	}
	line, column := 1, 1
	for _, r := range source[:i] {
		if r == '\n' {
			line, column = line+1, 1
			continue
		}
		column++
	}
	return file + ":" + itoa(line) + ":" + itoa(column) + ": "
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// mustFail compiles a site and checks the error starts at the place given and says what it must.
func mustFail(t *testing.T, files map[string]string, at, says string) {
	t.Helper()
	got := compileError(t, files)
	if !strings.HasPrefix(got, at) || !strings.Contains(got, says) {
		t.Errorf("want an error at %q saying %q,\n got %q", at, says, got)
	}
}

// mustCompile compiles a site that must compile, rendering "/" with no data.
func mustCompile(t *testing.T, files map[string]string) string {
	t.Helper()
	s := memorySite("", files)
	s.Reload = true
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return page.Render(Request{Path: "/"})
}

// A component with logic is a scope boundary: inside it a bare name is the component's own. The page
// here declares total, items and open too; the cart must not print them.
func TestComponentNamesCoverThePage(t *testing.T) {
	files := map[string]string{
		"/site/content.ui":       `<content><box component="cart" as="$cart" /></content>`,
		"/site/cart.ui":          cartUI,
		"/.components/cart.work": cartLogic,
	}
	page := map[string]any{
		"user":  "Quốc", // a name the component does not declare reaches it from the page
		"items": []any{1.0, 2.0, 3.0},
		"open":  true,
		"note":  "của trang",
		"total": funcOf(29000.0),
	}
	html := renderAt(t, memorySite("", files), ".", "/", page)
	mustContain(t, html,
		"<p>0 món · chưa có · 15000</p>", // the component's starting state
		`<p hidden>mở</p>`,               // its own open (false), not the page's
		"<p>Tạm tính </p>",               // total runs once the component does
		"<p>Quốc</p>")
	mustNotContain(t, html, "29.000đ", "3 món", "của trang")
	// its logic does not run on the client yet (step 4): no host the kernel would look for in vain
	mustNotContain(t, html, `data-kitwork-component="cart`)
}

// if and show at a call are read where the call stands: whether the component is there, and whether
// it shows, is decided before its names cover the page's. So they may not read the component's own
// names: show="open" at a cart's call is an error, not a silent read of either open.
func TestComponentCallIfAndShowReadWhereTheCallStands(t *testing.T) {
	files := map[string]string{
		"/site/content.ui": `<content><box component="cart" show="visible" /><box component="cart" if="!visible" /></content>`,
		"/site/cart.ui":    `<box><text>giỏ</text></box>`,
		"/site/cart.work":  cartLogic,
	}
	html := renderAt(t, memorySite("", files), ".", "/", map[string]any{"visible": true})
	mustContain(t, html, `<div class="flex-column"><p>giỏ</p>`)
	mustNotContain(t, html, `hidden><p>giỏ`)
	if got := strings.Count(html, "giỏ"); got != 1 {
		t.Errorf("if=\"!visible\" with the page's visible=true must drop the second cart; got %d carts:\n%s", got, html)
	}
	// control: the page's visible false hides the first cart and keeps the second
	html = renderAt(t, memorySite("", files), ".", "/", map[string]any{"visible": false})
	mustContain(t, html, `<div class="flex-column" hidden><p>giỏ</p>`)
	if got := strings.Count(html, "giỏ"); got != 2 {
		t.Errorf("want both carts with visible=false; got %d:\n%s", got, html)
	}

	for _, c := range []struct{ source, needle, says string }{
		{`<content><box component="cart" show="open" /></content>`, `show=`, "show đọc bên ngoài thẻ chủ; open là của cart: đặt nó trong cart.ui"},
		{`<content><box component="cart" if="items.length" /></content>`, `if=`, "if đọc bên ngoài thẻ chủ: có thẻ chủ hay không, lặp mấy lần quyết trước khi cart có; items là của cart"},
		{`<content><box component="cart" for="x of items" /></content>`, `for=`, "for đọc bên ngoài thẻ chủ"},
		{`<content><box component="cart" for="x of xs; key: note" /></content>`, `for=`, "key đọc bên ngoài thẻ chủ"},
		{`<content><box component="cart" note="{{ open }}" /></content>`, `note=`, "giá trị của note đọc bên ngoài thẻ chủ; open là của cart"},
	} {
		mustFail(t, map[string]string{"/site/content.ui": c.source, "/site/cart.ui": `<box><text>{{ note }}</text></box>`, "/site/cart.work": cartLogic},
			where("content.ui", c.source, c.needle), c.says)
	}
}

// An input is read where the call stands and is what the component starts from, over its own
// starting value.
func TestComponentDataAtTheCallWins(t *testing.T) {
	files := map[string]string{
		"/site/content.ui": `<content><box component="cart" note="{{ greeting }}" /></content>`,
		"/site/cart.ui":    `<box><text>{{ note }}</text></box>`,
		"/site/cart.work":  cartLogic,
	}
	html := renderAt(t, memorySite("", files), ".", "/", map[string]any{"greeting": "chào"})
	mustContain(t, html, "<p>chào</p>")

	// the call's if reads where the call stands, before the inputs go in: product here is the page's —
	// at a layout piece's call as at a component's
	files = map[string]string{
		"/site/content.ui":      `<content><product-card product="{{ other }}" if="product.listed" /></content>`,
		"/site/product-card.ui": `<card><text>{{ product.name }}</text></card>`,
	}
	html = renderAt(t, memorySite("", files), ".", "/", map[string]any{
		"product": map[string]any{"listed": true},
		"other":   map[string]any{"name": "Bạc xỉu", "listed": false},
	})
	mustContain(t, html, "<p>Bạc xỉu</p>")
}

// A layout piece has no names of its own: it reads where it is called. It has no instance, so it is
// never marked for the kernel and as names nothing; a <name>.work without that define is an error.
func TestLayoutPiecesAndInstanceErrors(t *testing.T) {
	files := map[string]string{
		"/site/content.ui":      `<content><product-card /></content>`,
		"/site/product-card.ui": `<card><text>{{ total }}</text></card>`,
	}
	html := renderAt(t, memorySite("", files), ".", "/", map[string]any{"total": 5.0})
	mustContain(t, html, "<p>5</p>")
	// the only hosts on the page are the progressbar's and the reader's (a site that does not reload
	// has both): the layout piece got none
	mustNotContain(t, html, `data-kitwork-component="product-card`)
	if hosts := strings.Count(html, "data-kitwork-component="); hosts != 2 || !strings.Contains(html, `data-kitwork-component="progressbar"`) ||
		!strings.Contains(html, `data-kitwork-component="reader"`) {
		t.Errorf("a layout piece is not a host; want only the progressbar's and the reader's hosts, got %d:\n%s", hosts, html)
	}

	source := `<content><product-card as="$card" /></content>`
	partsWarns(t, map[string]string{"/site/content.ui": source, "/site/product-card.ui": `<card><text>x</text></card>`},
		where("content.ui", source, `as=`), "as đặt tên cho một instance; product-card là layout (chỉ có giao diện, không có logic) nên không có instance để gọi")

	got := compileError(t, map[string]string{
		"/site/content.ui": `<content><box component="cart" /></content>`,
		"/site/cart.ui":    `<box><text>x</text></box>`,
		"/site/cart.work":  `import { component } from "kitwork"; component.define("giohang", () => { var open = false; });`,
	})
	if !strings.HasPrefix(got, "cart.work:1:1: ") || !strings.Contains(got, `component.define("cart", …)`) {
		t.Errorf("a cart.work without define(\"cart\"): %s", got)
	}
}

// components.work at the site root may hold several components; one that does not define the name is
// skipped, and the identity's .components/ is looked at next.
func TestComponentLookupOrder(t *testing.T) {
	files := map[string]string{
		"/site/content.ui":       `<content><box component="cart" /></content>`,
		"/site/cart.ui":          `<box><text>{{ note }}</text></box>`,
		"/site/components.work":  `import { component } from "kitwork"; component.define("menu", () => { var note = "menu"; });`,
		"/.components/cart.work": cartLogic,
	}
	mustContain(t, renderAt(t, memorySite("", files), ".", "/", nil), "<p>chưa có</p>")

	files["/site/components.work"] = `import { component } from "kitwork"; component.define("cart", () => { var note = "gốc site"; });`
	mustContain(t, renderAt(t, memorySite("", files), ".", "/", nil), "<p>gốc site</p>")
}

func funcOf(v any) any {
	return hydrate.Func(func([]any) any { return v })
}

// Kitwork's components cover the page the same way: the page's open does not open the dropdown, and
// the server does not know the viewer's appearance — its dark is unknown, so the first paint shows the
// light state whatever the page says. show written on the host reads where the call stands, and may
// not read the component's own names.
func TestBuiltinComponentNamesCoverThePage(t *testing.T) {
	html := render(t, `<content>
  <box component="dropdown" as="$account" show="signedIn"><button click="toggle()" expanded="{{ open }}">Tài khoản</button></box>
  <box component="appearance"><button click="toggle()"><icon value="{{ dark ? 'sun' : 'moon' }}" alt="Đổi giao diện"></icon></button></box>
  <box component="dropdown" show="menu"><text>menu</text></box>
</content>`, map[string]any{"open": true, "dark": true, "signedIn": false, "menu": true})
	mustContain(t, html,
		`data-kitwork-component="dropdown: $account" hidden`,
		`data-kitwork-component="dropdown"><p>menu</p>`, // show="menu" is the page's menu (true)
		`aria-expanded="false"`,
		`<i class="icon-moon"`)
	mustNotContain(t, html, `aria-expanded="true"`, `<i class="icon-sun"`)

	source := `<content><box component="dropdown" show="open"><text>menu</text></box></content>`
	mustFail(t, map[string]string{"/site/content.ui": source}, where("content.ui", source, `show=`),
		"show đọc bên ngoài thẻ chủ; open là của dropdown: đặt nó lên phần con")
}

// The host of a component without a UI is the author's tag: <box component="dropdown"> is one
// element, the box itself carrying the marker first (the dropdown host the kernel has always read,
// byte for byte), and appearance can sit on the very button that toggles it.
func TestComponentOnTheAuthorsHost(t *testing.T) {
	html := render(t, `<content><box component="dropdown" as="$account" click-outside="close()" keydown:escape="close()">
  <button click="toggle()" expanded="{{ open }}">Quốc</button>
  <popover place="below-end"><text>menu</text></popover>
</box></content>`, nil)
	mustContain(t, html, `<div class="flex-column place-anchor" data-kitwork-component="dropdown: $account" data-kitwork-event="click-outside: close(); keydown.escape: close()">`)

	html = render(t, `<content><button component="appearance" click="toggle()" border="none"><icon value="{{ dark ? 'sun' : 'moon' }}" alt="Đổi"></icon></button></content>`, nil)
	mustContain(t, html, `<button class="flex-row border-none" data-kitwork-component="appearance" type="button" data-kitwork-event="click: toggle()"><i class="icon-moon"`)
	if strings.Count(html, "<div") != 0 {
		t.Errorf("the button is the host: no element around it, got:\n%s", html)
	}
	// control: the same component on a box gets the box
	mustContain(t, render(t, `<content><box component="appearance"><button click="toggle()">x</button></box></content>`, nil),
		`<div class="flex-column" data-kitwork-component="appearance"><button`)
}

// A popover that writes no place still hangs below the start edge of what opens it (reboot.css:
// absolute, top 100%), so its host is its anchor exactly as when it writes one; otherwise the popover
// is positioned against some element further out. Control: a host whose child is neither placed nor
// a popover is no anchor.
func TestUnplacedPopoverIsAnchoredToItsHost(t *testing.T) {
	html := render(t, `<content><box component="dropdown" as="$menu">
  <button click="toggle()" expanded="{{ open }}">Menu</button>
  <popover><text>mục</text></popover>
</box></content>`, nil)
	mustContain(t, html, `<div class="flex-column place-anchor" data-kitwork-component="dropdown: $menu">`, `<div class="popover flex-column" hidden data-kitwork-bind="show: open">`)
	if !strings.Contains(rebootCSS(), ":where(.place-anchor){position:relative}") {
		t.Errorf("place-anchor must make its element the popover's containing block in reboot.css")
	}

	control := render(t, `<content><box component="dropdown"><button click="toggle()">Menu</button><box><text>mục</text></box></box></content>`, nil)
	mustContain(t, control, `<div class="flex-column" data-kitwork-component="dropdown">`)
	mustNotContain(t, control, "place-anchor")
}

// appearance declares mode, dark, toggle, set and system. resolved is not one of its names any more:
// written inside it, it is the page's, printed by the server with nothing for the kernel to keep up to
// date. Control: dark, its own, is bound.
func TestAppearanceStateIsModeAndDark(t *testing.T) {
	state := builtinState("appearance")
	for _, name := range []string{"mode", "dark", "toggle", "set", "system"} {
		if _, ok := state[name]; !ok {
			t.Errorf("appearance must declare %s, has %v", name, state)
		}
	}
	if _, ok := state["resolved"]; ok || len(state) != 5 {
		t.Errorf("appearance declares exactly mode, dark, toggle, set and system, has %v", state)
	}
	if state["dark"] != nil || state["mode"] != "system" {
		t.Errorf("the server does not know the viewer's choice: want mode system and dark unknown, got %v", state)
	}

	html := render(t, `<content><box component="appearance"><text>{{ resolved }}</text><text>{{ dark }}</text></box></content>`,
		map[string]any{"resolved": "của trang", "dark": "cũng của trang"})
	mustContain(t, html, `<p>của trang</p>`, `data-kitwork-bind="text: dark"`)
	mustNotContain(t, html, `text: resolved`, "cũng của trang")
}

// Inside a component the kernel runs, what reads only its names runs in the browser: its events go
// into data-kitwork-event (the part after ':' becomes '.'), the values that follow its state into
// data-kitwork-bind, a class from a ternary into data-kitwork-class, and a popover shows while its
// dropdown is open. What reads the page alone stays as the server printed it; a mix of the two, or a
// page event inside a component, is an error until the page itself runs on the client.
func TestBuiltinComponentRunsInTheBrowser(t *testing.T) {
	html := render(t, `<content>
  <box component="dropdown" as="$account" click-outside="close()" keydown:escape="close()">
    <button click="toggle()" expanded="{{ open }}"><text>{{ user.name }}</text></button>
    <popover place="below-end"><list><item><link value="/don-hang">Đơn</link></item></list></popover>
  </box>
  <box component="appearance"><button click="toggle()"><icon value="{{ dark ? 'sun' : 'moon' }}" alt="Đổi"></icon><text>{{ mode }}</text></button></box>
</content>`, map[string]any{"user": map[string]any{"name": "Quốc"}})
	mustContain(t, html,
		`data-kitwork-component="dropdown: $account" data-kitwork-event="click-outside: close(); keydown.escape: close()"`,
		`data-kitwork-bind="aria-expanded: open" data-kitwork-event="click: toggle()"`,
		`<span>Quốc</span>`, // the page's data: printed by the server, no binding
		`data-kitwork-bind="show: open"`,
		`data-kitwork-class="icon-moon: (dark ? &#39;sun&#39; : &#39;moon&#39;) == &#39;moon&#39;; icon-sun: (dark ? &#39;sun&#39; : &#39;moon&#39;) == &#39;sun&#39;"`,
		`data-kitwork-bind="text: mode"`)
	mustNotContain(t, html, `text: user.name`, `class.icon`)

	for source, want := range map[string]string{
		`<content><box component="appearance"><button click="toggle(); log()">x</button></box></content>`: "nhiều câu lệnh",
		`<content><box component="appearance"><button click="go(page)">x</button></box></content>`:        "bên trong appearance sự kiện click chỉ gọi tên của component",
		`<content><box component="appearance"><text>{{ dark + user.name }}</text></box></content>`:        "đọc cả tên của component lẫn user",
		`<content><box component="appearance"><icon value="{{ mode }}" alt=""></icon></box></content>`:    "phải viết ra mọi giá trị",
		`<content><box component="appearance"><text>Đang {{ mode }} lúc này</text></box></content>`:       "",
	} {
		_, err := memorySite("", map[string]string{"/site/content.ui": source}).Compile("/site")
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", source, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", source, err, want)
		}
	}
}

// classPairName is the engine's side of the grouped name rule; the reader (hydrate.Pairs) is the
// other. A name the engine writes must come back whole from the reader, and one the reader refuses
// must not be written. A name with ':' would be cut short by the reader, so it is never one.
func TestClassPairNameFollowsTheSplitter(t *testing.T) {
	for _, name := range []string{
		"icon-sun", "bg-brand", "brand.on", "color-ink-30", "color-brand+15", "p-0.5", "w_1", "a+b", "w-(--size)", "a(b)c(d)",
		"bg-x/50", "material-glass/60", "w-[1px]", "a(b(c))", "a)", "a(", "a b", "", "#x", "a'b",
	} {
		_, err := hydrate.Pairs(name + ": on")
		if got, want := classPairName(name), err == nil; got != want {
			t.Errorf("classPairName(%q) = %v, but the reader says %v (%v)", name, got, want, err)
		}
	}
	// the thin and thick glass (glass/60, glass/88 since 13/10/2026) and an alpha are names both take,
	// as material-glass.thin was
	for _, name := range []string{"material-glass/60", "material-glass/88", "background-paper/80", "material-glass.thin"} {
		if !classPairName(name) {
			t.Errorf("classPairName(%q) = false; a state cannot turn it on and off", name)
		}
	}
	for _, name := range []string{"hover:bg-brand", "md:flex"} {
		if classPairName(name) {
			t.Errorf("classPairName(%q) = true; the reader would cut it at its ':'", name)
		}
		// control: the reader does take it, as a shorter name — which is why the engine must not write it
		if pairs, err := hydrate.Pairs(name + ": on"); err != nil || len(pairs) != 1 || pairs[0].Name == name {
			t.Errorf("%q: want the reader to cut it short, got %v %v", name, pairs, err)
		}
	}
}

// A class that follows a component's state has its own attribute, read like style:
// data-kitwork-class="<the class>: <when it is on>", one pair per class, written after
// data-kitwork-bind and before data-kitwork-event. A class two values both make is one pair (two would
// undo each other), and a class the grouped name cannot carry (a variant) is refused; an opacity and a
// glass (glass/60, the thin glass since 13/10/2026) are names it carries.
func TestBuiltinComponentTogglesClassesByName(t *testing.T) {
	html := render(t, `<content><box component="dropdown">
  <button click="toggle()" expanded="{{ open }}" background="{{ open ? 'panel' : 'paper' }}">Tài khoản</button>
  <badge color="{{ open ? 'ink' : 'ink-30' }}"><text>mới</text></badge>
</box></content>`, nil)
	when := func(value string) string {
		return "(open ? &#39;panel&#39; : &#39;paper&#39;) == &#39;" + value + "&#39;"
	}
	mustContain(t, html,
		`<button class="flex-row background-paper" type="button" data-kitwork-bind="aria-expanded: open" data-kitwork-class="background-panel: `+when("panel")+`; background-paper: `+when("paper")+`" data-kitwork-event="click: toggle()"`)
	// a badge's colour is one class: its tint is the colour class's own (--kitwork-tint), so each
	// value is one pair
	tone := "(open ? &#39;ink&#39; : &#39;ink-30&#39;) == "
	mustContain(t, html,
		`<span class="badge flex-row color-ink-30" data-kitwork-class="color-ink: `+tone+`&#39;ink&#39;; color-ink-30: `+tone+`&#39;ink-30&#39;">`)
	mustNotContain(t, html, "class.")

	for source, want := range map[string]string{
		`<content><box component="appearance"><box hover:background="{{ dark ? 'brand' : 'ink' }}">x</box></box></content>`: "class hover:background-brand đổi theo trạng thái",
		// control: the same values without the variant are names the pair can carry, an opacity too
		`<content><box component="appearance"><box background="{{ dark ? 'brand' : 'ink' }}" color="white">x</box></box></content>`:  "",
		`<content><box component="appearance"><box background="{{ dark ? 'ink/50' : 'ink' }}" color="white">x</box></box></content>`: "",
		// and so are a colour lighter and a colour stronger (13/10/2026): - and + are letters of a name
		`<content><box component="appearance"><box background="{{ dark ? 'brand+15' : 'ink-30' }}" color="white">x</box></box></content>`: "",
	} {
		_, err := memorySite("", map[string]string{"/site/content.ui": source}).Compile("/site")
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", source, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", source, err, want)
		}
	}

	// the thin and the thick glass follow a state, as glass.thin and glass.thick did before 13/10/2026
	glass := render(t, `<content><box component="appearance"><box material="{{ dark ? 'glass/60' : 'none' }}">x</box>`+
		`<box material="{{ dark ? 'glass/88' : 'glass' }}">y</box></box></content>`, nil)
	thin := "(dark ? &#39;glass/60&#39; : &#39;none&#39;) == "
	thick := "(dark ? &#39;glass/88&#39; : &#39;glass&#39;) == "
	mustContain(t, glass,
		`data-kitwork-class="material-glass/60: `+thin+`&#39;glass/60&#39;; material-none: `+thin+`&#39;none&#39;"`,
		`data-kitwork-class="material-glass: `+thick+`&#39;glass&#39;; material-glass/88: `+thick+`&#39;glass/88&#39;"`)
}

// Outside every component an event does not reach the browser yet, but what is written is read: an
// expression that does not parse is an error at the attribute, never dropped unread. CONTROL: a
// well-formed event outside a component still compiles (with no data-kitwork-event).
func TestPageEventIsReadOutsideAComponent(t *testing.T) {
	mustFail(t, map[string]string{"/site/content.ui": `<content><button click="x((">e</button></content>`},
		"content.ui:1:18", "biểu thức không hợp lệ: x((")
	// CONTROL
	html := mustCompile(t, map[string]string{"/site/content.ui": `<content><button click="x()">b</button><button click:once="x()">c</button></content>`})
	mustContain(t, html, `>b</button>`, `>c</button>`)
	mustNotContain(t, html, "data-kitwork-event")
}
