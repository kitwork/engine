package ui

import (
	"path/filepath"
	"strings"
	"testing"
)

// givenSite is a Kitwork UI site in memory (Site.RouterWork): what every page is given is known.
func givenSite(files map[string]string) *Site {
	s := memorySite("", files)
	s.Reload, s.RouterWork = true, true
	return s
}

// givenError compiles one page of a folder of s (pageContent, pageNotFound, pageError) and gives its error, ""
// when it compiles.
func givenError(s *Site, kind, route string) string {
	dir := filepath.Join(s.Root, filepath.FromSlash(route))
	var err error
	switch kind {
	case pageNotFound:
		_, err = s.CompileNotFound(dir)
	case pageError:
		_, err = s.CompileError(dir)
	default:
		_, err = s.Compile(dir)
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

// givenRoute is a router.work whose router.get hands the page products and total.
const givenRoute = "import * as router from \"kitwork/router\";\n" +
	"router.get((context) => context.title(\"Sản phẩm\").view({ products: [], total: 0 }));\n"

// A page reads exactly the names of the object its route's context.view({ … }) is given (KITWORK_UI §4,
// 24/10/2026): any other name read, that nothing around declares and no component of the page declares, is
// the compile error "router.get không trả về x" at the read, file:line:col of the .ui that reads it — the {{
// of a run of text, the attribute of a value — naming the router.work, the names it gives and the nearest
// one. So it is in content.ui, in an attribute, inside a component that does not declare the name (it
// reaches the page), in a component's own .ui, in the frame view.ui, in a region it calls and in a layout
// piece.
//
// CONTROLS (24/10/2026): with notGiven returning nil every row compiled ("want … got \"\""); with checkNames
// recording only what is read outside every component (as strays are), the rows inside <dropdown> and in
// shop-cart.ui compiled; with readAt giving the run of text's start, the first row said content.ui:1:16, not
// the {{ at 1:19. (24/10/2026, fix of the review) With readAt taking the first {{ whose text begins with the
// expression's, as it was written first, the second read of a run said 1:16, not 1:41, and the read after an
// escaped \{{ said 1:17, not 1:31.
func TestPageReadingANameItsRouteDoesNotGiveIsAnError(t *testing.T) {
	const (
		given    = "trang đọc %s mà object của context.view({ … }) trong router.work không có; tên có: products, total"
		headline = "router.get không trả về "
	)
	says := func(name, near string) string {
		message := headline + name + ": " + strings.Replace(given, "%s", name, 1)
		if near != "" {
			message += "; ý là " + near + "?"
		}
		return message
	}
	cartLogic := `import { component } from "kitwork"; component.define("shop-cart", () => { var open = false; });`
	frame := "<view>\n  <text>{{ siteName }}</text>\n  <content />\n</view>"
	header := `<header><link for="entry of menu" value="{{ entry.url }}">{{ entry.label }}</link></header>`
	for _, c := range []struct {
		name       string
		files      map[string]string
		at         string
		read, near string
	}{
		{"a run of text", map[string]string{"/site/content.ui": `<content><text>Có {{ product.length }} món</text></content>`},
			where("content.ui", `<content><text>Có {{ product.length }} món</text></content>`, "{{ product"), "product", "products"},
		{"the second read of a run of text, its first letters the first's", map[string]string{"/site/content.ui": `<content><text>{{ products.length }} và {{ product }}</text></content>`},
			where("content.ui", `<content><text>{{ products.length }} và {{ product }}</text></content>`, "{{ product }}"), "product", "products"},
		{"a read after an escaped {{", map[string]string{"/site/content.ui": `<content><text>\{{ cart }} và {{ cart }}</text></content>`},
			where("content.ui", `<content><text>\{{ cart }} và {{ cart }}</text></content>`, "{{ cart }}</text>"), "cart", ""},
		{"an attribute", map[string]string{"/site/content.ui": `<content><link value="{{ url }}">Xem</link></content>`},
			where("content.ui", `<content><link value="{{ url }}">Xem</link></content>`, "value="), "url", ""},
		{"inside a component that does not declare it", map[string]string{"/site/content.ui": `<content><dropdown><button click="toggle()">{{ cart }}</button></dropdown></content>`},
			where("content.ui", `<content><dropdown><button click="toggle()">{{ cart }}</button></dropdown></content>`, "{{ cart"), "cart", ""},
		{"a component's own .ui", map[string]string{"/site/content.ui": `<content><shop-cart /></content>`,
			"/site/shop-cart.ui": `<shop-cart><text show="open">{{ owner }}</text></shop-cart>`, "/site/shop-cart.work": cartLogic},
			where("shop-cart.ui", `<shop-cart><text show="open">{{ owner }}</text></shop-cart>`, "{{ owner"), "owner", ""},
		{"the frame", map[string]string{"/site/content.ui": `<content><text>{{ total }}</text></content>`, "/site/view.ui": frame},
			where("view.ui", frame, "{{ siteName"), "siteName", ""},
		{"a region of the frame", map[string]string{"/site/content.ui": `<content><text>{{ total }}</text></content>`,
			"/site/view.ui": `<view><header /><content /></view>`, "/site/header.ui": header},
			where("header.ui", header, "for="), "menu", ""},
		{"a layout piece", map[string]string{"/site/content.ui": `<content><product-card for="product of products" product="{{ product }}" /></content>`,
			"/site/product-card.ui": `<card><text>{{ product.name }}</text><text>{{ price }}</text></card>`},
			where("product-card.ui", `<card><text>{{ product.name }}</text><text>{{ price }}</text></card>`, "{{ price"), "price", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.files["/site/router.work"] = givenRoute
			want := c.at + says(c.read, c.near)
			if got := givenError(givenSite(c.files), pageContent, "/"); got != want {
				t.Errorf("\n got  %q\n want %q", got, want)
			}
		})
	}
}

// What a page is given and what it writes for itself are no such error: the names of the object, a function of
// it called, the row of a for and its index, a layout piece's input and the row of the call's own for (read
// once a row: its if, show, key and inputs), a component's state, an alias and the kernel's $ names, the format
// helpers, a lambda's parameters, the globals of the expressions, and undefined, NaN and Infinity. A name a
// component of the page declares, read inside another component (copied of the terminal, in a dropdown), is
// not told either: it is no name the route forgot.
//
// CONTROLS (24/10/2026): with callScope reading every word of a call where the call stands, the layout call's
// key failed "content.ui:…: router.get không trả về p"; with literalNames empty, "router.get không trả về
// undefined"; with formatHelpers not consulted, "router.get không trả về money". (Fix of the review) With
// declaredOnPage answering false, "content.ui:7:38: router.get không trả về copied".
func TestPageNamesItGivesItselfAreNotRefused(t *testing.T) {
	route := "import * as router from \"kitwork/router\";\n" +
		"router.get((context) => context.view({ products: [{ id: 1, name: \"Cà phê\", visible: true }], total: 29000, label: (x) => \"#\" + x }));\n"
	content := `<content>
  <item for="p, i of products; key: p.id"><text>{{ i }}. {{ p.name }} {{ label(p.id) }}</text></item>
  <product-card for="p of products; key: p.id" show="p.visible" product="{{ p }}" />
  <dropdown><button click="toggle()" expanded="{{ open }}">Mở</button></dropdown>
  <terminal as="$install"><code>ls</code></terminal>
  <button click="$install.copy()">{{ $install.copied ? 'Đã chép' : 'Chép' }}</button>
  <dropdown><button click="toggle()">{{ copied ? 'Đã chép' : 'Mở' }}</button></dropdown>
  <text>{{ money(total) }} · {{ date(total) }} · {{ initials('Huỳnh Nhân Quốc') }}</text>
  <text>{{ products.map((each) => each.name).join(', ') }} {{ Math.max(total, 1) }} {{ JSON.stringify(total) }}</text>
  <text show="total !== undefined && total === total && total < Infinity && !isNaN(total) && total !== NaN">ok</text>
</content>`
	s := givenSite(map[string]string{
		"/site/router.work":     route,
		"/site/content.ui":      content,
		"/site/product-card.ui": `<card><text>{{ product.name }}</text><text>{{ money(total) }}</text></card>`,
	})
	if got := givenError(s, pageContent, "/"); got != "" {
		t.Fatalf("a page reading only what it is given and what it writes: %s", got)
	}
}

// notfound.ui is given nothing (context.notFound(), an address no folder answers) and error.ui only message,
// the words of context.error(…) (work/router_ui.go respondUI): the frame and its regions drawn around them are
// given the same, whatever the route's router.get gives its own content.ui. A name read past that is the error
// at the read, saying what the page is given.
//
// In a site that is not a Kitwork UI site (Site.RouterWork false) neither is known, and nothing is told.
//
// CONTROLS (24/10/2026): with notfound.ui and error.ui given the route's names as before (viewNames of the
// folder), the two rows of notfound.ui reading site compiled and error.ui reading message failed ("nên message
// không đọc ra gì"); with error.ui given nothing, the row reading message failed so, and the near name lost its
// "ý là message?". (Fix of the review) With notfound.ui and error.ui known whatever Site.RouterWork says, the
// two rows of a site that is not a Kitwork UI site failed at the frame: "view.ui:2:9: trang không tìm thấy
// không được đưa tên nào…", "view.ui:2:9: trang lỗi chỉ được đưa message…".
func TestNotFoundAndErrorPagesAreGivenTheirOwn(t *testing.T) {
	route := "import * as router from \"kitwork/router\";\nrouter.get((context) => context.view({ site: \"Kitwork\" }));\n"
	const (
		notFound  = "trang không tìm thấy không được đưa tên nào, nên site không đọc ra gì: notfound.ui, khung view.ui và các vùng quanh nó vẽ cho context.notFound() hay cho địa chỉ không thư mục nào khớp, không kèm object nào; phần động của khung là component (KITWORK_UI.md mục 4)"
		errorPage = "trang lỗi chỉ được đưa message, câu của context.error(…), nên %s không đọc ra gì: error.ui, khung view.ui và các vùng quanh nó không có tên nào khác"
	)
	plain := `<view><content /></view>`
	framed := "<view>\n  <text>{{ site }}</text>\n  <content />\n</view>"
	for _, c := range []struct {
		name, kind, view, page, want string
		plain                        bool // a site that is not a Kitwork UI site
	}{
		{"notfound.ui reads nothing", pageNotFound, plain, `<content><text>Không có trang này</text></content>`, "", false},
		{"notfound.ui reads a name", pageNotFound, plain, `<content><text>{{ site }}</text></content>`,
			where("notfound.ui", `<content><text>{{ site }}</text></content>`, "{{ site") + notFound, false},
		{"the frame of notfound.ui reads a name", pageNotFound, framed, `<content><text>Không có</text></content>`,
			where("view.ui", framed, "{{ site") + notFound, false},
		{"error.ui reads message", pageError, plain, `<content><text>{{ message }}</text></content>`, "", false},
		{"error.ui reads a name near message", pageError, plain, `<content><text>{{ mesage }}</text></content>`,
			where("error.ui", `<content><text>{{ mesage }}</text></content>`, "{{ mesage") + strings.Replace(errorPage, "%s", "mesage", 1) + "; ý là message?", false},
		{"the frame of error.ui reads a name", pageError, framed, `<content><text>{{ message }}</text></content>`,
			where("view.ui", framed, "{{ site") + strings.Replace(errorPage, "%s", "site", 1), false},
		// a site that is not a Kitwork UI site (a test's page and its Scope): what these pages are given is not
		// known there, and nothing is told
		{"notfound.ui of a site that is not a Kitwork UI site", pageNotFound, framed, `<content><text>{{ title }}</text></content>`, "", true},
		{"error.ui of a site that is not a Kitwork UI site", pageError, framed, `<content><text>{{ mesage }}</text></content>`, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			file := "/site/notfound.ui"
			if c.kind == pageError {
				file = "/site/error.ui"
			}
			s := givenSite(map[string]string{
				"/site/router.work": route, "/site/view.ui": c.view, file: c.page,
				"/site/content.ui": `<content><text>{{ site }}</text></content>`,
			})
			s.RouterWork = !c.plain
			if got := givenError(s, c.kind, "/"); got != c.want {
				t.Errorf("\n got  %q\n want %q", got, c.want)
			}
			// the route's own page reads what its router.get gives, the frame too
			if got := givenError(s, pageContent, "/"); got != "" {
				t.Errorf("content.ui: %s", got)
			}
		})
	}
}

// A folder whose router.work has no router.get draws its content.ui on GET with nothing (work/router_serve.go):
// a name read is the error saying so, and naming what another door's context.view({ … }) gives (a POST drawing
// the page again). A folder of a Kitwork UI site with no router.work at all gives its page nothing; one with a
// router.kitwork.js is a template's, whose page is told nothing, as is any folder of a site that is not a
// Kitwork UI site (Site.RouterWork false). A router.get that never calls context.view, and one whose object is
// empty, give nothing either.
//
// CONTROLS (24/10/2026): with given not knowing a folder without router.work, the row "no router.work" compiled;
// with the template's router.kitwork.js not looked for, the row "router.kitwork.js" failed "router.get không trả
// về title: thư mục /tin-tuc không có router.work…"; with routerLineWord answering "" for every line, the rows
// whose router.work has a router.get said "… không có router.get …".
func TestAFolderWithoutRouterGetGivesNothing(t *testing.T) {
	const header = "import * as router from \"kitwork/router\";\n"
	page := `<content><text>{{ title }}</text></content>`
	at := where("content.ui", page, "{{ title")
	for _, c := range []struct {
		name   string
		router map[string]string // file → source, in /site/tin-tuc/
		plain  bool              // a site that is not a Kitwork UI site
		want   string
	}{
		{"only router.post", map[string]string{"router.work": header + "router.post((context) => context.status(400).view({ errors: [] }));\n"}, false,
			"router.get không trả về title: tin-tuc/router.work không có router.get, nên trang mở bằng GET không được đưa tên nào, và context.view({ … }) của cửa khác trong file cũng không có title; tên có: errors"},
		{"no door", map[string]string{"router.work": header + "router.guard(() => true);\n"}, false,
			"router.get không trả về title: tin-tuc/router.work không có router.get, nên trang mở bằng GET không được đưa tên nào; đưa title cho trang bằng router.get((context) => context.view({ title }))"},
		{"no router.work", map[string]string{}, false,
			"router.get không trả về title: thư mục /tin-tuc không có router.work, nên không có router.get và trang không được đưa tên nào; đưa title cho trang bằng router.get((context) => context.view({ title })) trong router.work của thư mục"},
		{"a router.get that never calls view", map[string]string{"router.work": header + "router.get((context) => context.json({ title: \"x\" }));\n"}, false,
			"router.get không trả về title: router.get trong tin-tuc/router.work không gọi context.view({ … }), nên trang không được đưa tên nào; đưa title cho trang bằng context.view({ title })"},
		{"an empty object", map[string]string{"router.work": header + "router.get((context) => context.view({}));\n"}, false,
			"router.get không trả về title: trang đọc title mà object của context.view({ … }) trong tin-tuc/router.work không có tên nào; thêm title vào object đó"},
		{"the chain after router.get", map[string]string{"router.work": header + "router.get((context) => context.view({ titel: \"x\" })).cache(\"5m\");\n"}, false,
			"router.get không trả về title: trang đọc title mà object của context.view({ … }) trong tin-tuc/router.work không có; tên có: titel; ý là titel?"},
		{"router.kitwork.js", map[string]string{"router.kitwork.js": "import { router } from \"kitwork\";\nrouter.get(() => ({}));\n"}, false, ""},
		{"not a Kitwork UI site", map[string]string{}, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"/site/router.work": givenRoute, "/site/tin-tuc/content.ui": page}
			for name, source := range c.router {
				files["/site/tin-tuc/"+name] = source
			}
			s := givenSite(files)
			s.RouterWork = !c.plain
			want := c.want
			if want != "" {
				want = strings.Replace(at, "content.ui", "tin-tuc/content.ui", 1) + want
			}
			if got := givenError(s, pageContent, "/tin-tuc"); got != want {
				t.Errorf("\n got  %q\n want %q", got, want)
			}
		})
	}
}

// The names a function of the same file hands context.view({ … }) are the page's (the demo's page(context, …),
// apps/…/demo.kitwork.io/database/router.work): every .view of the file is read, a helper's, a POST's, a
// branch's of an if, so the page is given their union.
//
// CONTROL (24/10/2026): with the walk of the file not entering the value of a const (the helper page), the page
// failed "router.get không trả về today: … tên có: empty", the POST's object alone.
func TestNamesAHelperOfTheFileGivesArePagesNames(t *testing.T) {
	route := "import * as router from \"kitwork/router\";\n" +
		"const page = (context, added, problem) => context.title(\"Đơn\").view({ added: added, problem: problem, today: \"2026-10-24\" });\n" +
		"router.get((context) => page(context, null, null));\n" +
		"router.post((context) => {\n  if (context.request.form(\"x\") === \"\") {\n    return context.status(400).view({ \"empty\": true });\n  }\n  return page(context, 1, null);\n});\n"
	content := `<content><text>{{ today }} {{ added }} {{ problem }} {{ empty }}</text></content>`
	s := givenSite(map[string]string{"/site/router.work": route, "/site/content.ui": content})
	if got := givenError(s, pageContent, "/"); got != "" {
		t.Fatalf("the helper's names: %s", got)
	}
	stray := `<content><text>{{ todays }}</text></content>`
	s = givenSite(map[string]string{"/site/router.work": route, "/site/content.ui": stray})
	want := where("content.ui", stray, "{{ todays") + "router.get không trả về todays: trang đọc todays mà object của context.view({ … }) trong router.work không có; tên có: added, empty, problem, today; ý là today?"
	if got := givenError(s, pageContent, "/"); got != want {
		t.Errorf("\n got  %q\n want %q", got, want)
	}
}

// What the compiler cannot see in router.work, it does not tell (viewNames): a .view handed a name, a spread, a
// computed key or a second object; view read off anything without being called there (taken apart: { view },
// ["view"], a const of it); code of another file of the app, a name it imports, what such a call gave (a
// factory's function) or what a global made of either (Object.assign, Object.values), called with an argument,
// handed to a router line or a function of the file, kept in an object called so, or read where the reading
// does not follow it (returned by a function of the file, assigned), since that code may call .view itself. The page then compiles whatever it reads. CONTROL rows,
// known, are told: an import called with nothing, an import's value or what is read off it handed to
// context.view (by name, kept in a const, in a list, its length or a row of it) or used up where it is read (a
// template literal, a condition, !, typeof, a key read), what another file gave handed to Kitwork (richtext),
// and view called through context's modifiers.
//
// CONTROLS (24/10/2026, go test -overlay): with view read apart not looked for, the rows "{ view }",
// "[\"view\"]" and "a const of view" were told "router.get không trả về anything"; with an entry of any other key
// passed over (a spread's, a computed one), "a spread" and "a computed key" were told; with what another file
// gave not looked at when it is called, "a factory's function kept" was told; with handsOn answering false, the
// three other factory rows were told; with Kitwork's names not taking data, the richtext row was not told; with
// the engine's (context, response) not taking it, neither were the rows handing an import's data to
// context.view. (Fix of the review: an imported name was refused anywhere but called with nothing, which turned
// off the check of a route handing an imported list to its view; the three import rows above never rested on
// that rule.) With an imported name read where the reading does not follow it not looked at, "an import a
// function of the file returns" and "an import assigned" were told; with what a call is handed not followed,
// "an import handed to the view" and "what is read off an import…" were not told; with a value used up where it
// is read not followed (a condition, !, typeof, a key, a template literal), the second of them was not told;
// with a second object to .view read as the first alone, "two objects" was told "… tên có: other"; with what a
// global makes of code of another file not taken for that code, the two rows of a global giving back were told.
func TestWhatTheCompilerCannotSeeIsNotTold(t *testing.T) {
	const header = "import * as router from \"kitwork/router\";\n"
	page := `<content><text>{{ anything }}</text></content>`
	helper := map[string]string{"/site/page.work": "export const page = (context) => context.view({ anything: 1 });\nexport const list = () => [];\n" +
		"export const makePage = () => page;\nexport const sample = () => \"<p>x</p>\";\n" +
		"export const navigation = [{ url: \"/\", label: \"Trang chủ\" }];\nexport const format = (x) => \"#\" + x;\nexport const title = \"Kitwork\";\n"}
	for _, c := range []struct {
		name, router string
		told         bool
	}{
		{"a name", header + "router.get((context) => {\n  const data = { anything: 1 };\n  return context.view(data);\n});\n", false},
		{"a spread", header + "const base = { anything: 1 };\nrouter.get((context) => context.view({ ...base }));\n", false},
		{"a computed key", header + "const key = \"anything\";\nrouter.get((context) => context.view({ [key]: 1 }));\n", false},
		{"{ view }", header + "router.get((context) => {\n  const { view } = context;\n  return view({ anything: 1 });\n});\n", false},
		{"[\"view\"]", header + "router.get((context) => context[\"view\"]({ anything: 1 }));\n", false},
		{"a const of view", header + "router.get((context) => {\n  const show = context.view;\n  return show({ anything: 1 });\n});\n", false},
		{"an import called with the context", header + "import { page } from \"./page.work\";\nrouter.get((context) => page(context));\n", false},
		{"an import handed", header + "import { page } from \"./page.work\";\nrouter.get(page);\n", false},
		{"an import kept", header + "import { page } from \"./page.work\";\nconst handlers = { page };\nrouter.get((context) => handlers.page(context));\n", false},
		{"a factory's handler", header + "import { makePage } from \"./page.work\";\nrouter.get(makePage());\n", false},
		{"a factory's function kept", header + "import { makePage } from \"./page.work\";\nconst handle = makePage();\nrouter.get((context) => handle(context));\n", false},
		{"a factory's function in an object", header + "import { makePage } from \"./page.work\";\nconst handlers = { page: makePage() };\nrouter.get(handlers.page);\n", false},
		{"a factory's function handed to the file's", header + "import { makePage } from \"./page.work\";\nconst run = (answer, context) => answer(context);\nrouter.get((context) => run(makePage(), context));\n", false},
		{"an import a function of the file returns", header + "import { page } from \"./page.work\";\nconst pick = () => page;\nrouter.get((context) => pick()(context));\n", false},
		{"an import assigned", header + "import { page } from \"./page.work\";\nrouter.get((context) => {\n  let handle = null;\n  handle = page;\n  return handle(context);\n});\n", false},
		{"an import a global gives back", header + "import { page } from \"./page.work\";\nconst handlers = Object.assign({}, { page });\nrouter.get((context) => handlers.page(context));\n", false},
		{"a factory's function a global gives back", header + "import { makePage } from \"./page.work\";\nrouter.get(Object.values({ page: makePage() })[0]);\n", false},
		{"two objects", header + "router.get((context) => context.view({ other: 1 }, { anything: 1 }));\n", false},
		{"an import called with nothing", header + "import { list } from \"./page.work\";\nrouter.get((context) => context.view({ items: list() }));\n", true},
		{"an import handed to the view", header + "import { navigation, format } from \"./page.work\";\nrouter.get((context) => context.view({ navigation, format }));\n", true},
		{"what is read off an import, kept, or used up", header + "import { navigation, title } from \"./page.work\";\nconst kept = navigation;\n" +
			"router.get((context) => context.view({ kept, nav: navigation, links: [title, navigation], count: navigation.length, first: navigation[0], " +
			"label: `${title}!`, some: navigation ? 1 : 0, none: !title, kind: typeof title, at: kept[title] }));\n", true},
		{"what another file gave handed to Kitwork", header + "import { richtext } from \"kitwork\";\nimport { sample } from \"./page.work\";\nconst source = sample();\n" +
			"router.get(({ request, response }) => response.title(request.path).view({ source, cleaned: richtext(source) }));\n", true},
		{"view after modifiers", header + "router.get((context) => context.status(200).title(\"x\").view({ items: [] }));\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"/site/router.work": c.router, "/site/content.ui": page}
			for name, source := range helper {
				files[name] = source
			}
			got := givenError(givenSite(files), pageContent, "/")
			if told := strings.Contains(got, "router.get không trả về anything"); told != c.told || !told && got != "" {
				t.Errorf("told = %v, want %v: %q", told, c.told, got)
			}
		})
	}
}
