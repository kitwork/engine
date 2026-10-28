package ui

import (
	"path/filepath"
	"strings"
	"testing"
)

// A component of the site whose jobs go through the bridge (28/10/2026, component_live.go), defined as
// KITWORK_COMPONENTS.md writes it: an input the call passes (owner, required), one with a default (shippingFee),
// vars and jobs in its bind, a helper and a computed const that stay out of it.
const liveCartLogic = `import * as component from "kitwork/component";

component.define("shop-cart", (context) => {
  const { owner, shippingFee = 15000 } = context.attributes();
  var items = [];
  var done = false;
  const label = "Giỏ hàng";
  const total = items.length * 2;
  const add = (name) => { items = items.concat([{ id: items.length + 1, name: name }]); };
  const checkout = () => { items = []; done = true; };
  return context.bind({ items, done, label, shippingFee, add, checkout });
});
`

const liveCartRoute = `import * as router from "kitwork/router";
router.view((context) => {
  const user = "an";
  return context.bind({ user });
});
`

const liveCartContent = `<content><shop-cart owner="{{ user }}" shipping-fee="{{ 20000 }}" gap="2">
  <text>{{ label }}</text>
  <list><item for="line of items; key: line.id"><text>{{ line.name }}</text></item></list>
  <button click="add('trà')">Thêm</button>
  <button click="checkout()" disabled="{{ checkout.pending || items.length == 0 }}">Đặt</button>
  <text show="checkout.error">{{ checkout.error.message }}</text>
  <text show="done">Đã nhận đơn.</text>
</shop-cart></content>`

func liveCartFiles(logic, route, content string) map[string]string {
	return map[string]string{
		"/site/components.work":  logic,
		"/site/cart/router.work": route,
		"/site/cart/content.ui":  content,
	}
}

func compileLiveCart(t *testing.T, files map[string]string) (*Page, error) {
	t.Helper()
	s := givenSite(files)
	return s.Compile(filepath.Join(s.Root, "cart"))
}

// The cart runs in the browser: its host names the component and its place, carries the one input its look
// reads (shippingFee, as the call passes it; owner stays on the server), what is written inside reads its names
// live, its list is drawn as the kernel adopts it, and the page ends its content with the component, its
// starting state as the source writes it, its jobs and its vars. A component whose function answers no bind
// (cartLogic) is drawn as before: no host the kernel looks for.
func TestComponentWithJobsRunsInTheBrowser(t *testing.T) {
	page, err := compileLiveCart(t, liveCartFiles(liveCartLogic, liveCartRoute, liveCartContent))
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/cart", Scope: map[string]any{"user": "an"}})
	mustContain(t, html,
		`data-kitwork-component="shop-cart" data-kitwork-at="c1" data-kitwork-scope="shippingFee: 20000"`,
		`<p data-kitwork-bind="text: label">Giỏ hàng</p>`,
		`<!--kitwork-for:start id=s1-->`, `data-kitwork-for="line of items; key: line.id"`,
		`data-kitwork-event="click: add(&#39;trà&#39;)"`, `data-kitwork-event="click: checkout()"`,
		`data-kitwork-bind="disabled: checkout.pending || items.length == 0"`, `data-kitwork-bind="show: done"`,
		`<script type="application/json" data-kitwork-components>{"shop-cart":{"jobs":["add","checkout"],"state":{"done":false,"items":[],"label":"Giỏ hàng","shippingFee":15000},"vars":["done","items"]}}</script></main>`)
	mustNotContain(t, html, "owner", "total")
	instances := page.Instances(Request{Path: "/cart", Scope: map[string]any{"user": "an"}})
	cart, ok := instances["c1"]
	if !ok || cart.Name != "shop-cart" || cart.Inputs["owner"] != "an" || cart.Inputs["shippingFee"] != float64(20000) || !strings.HasSuffix(cart.Logic, "components.work") {
		t.Errorf("the instance at c1, as the page draws it: %+v", instances)
	}
}

// A call the page does not draw is no instance: an if that is false at the call.
func TestInstancesAreThoseThePageDraws(t *testing.T) {
	content := strings.Replace(liveCartContent, `<shop-cart owner`, `<shop-cart if="user != 'an'" owner`, 1)
	page, err := compileLiveCart(t, liveCartFiles(liveCartLogic, liveCartRoute, content))
	if err != nil {
		t.Fatal(err)
	}
	if instances := page.Instances(Request{Path: "/cart", Scope: map[string]any{"user": "an"}}); len(instances) != 0 {
		t.Errorf("a cart the page does not draw is no instance: %+v", instances)
	}
}

// What the bridge cannot carry is a compile error, never a page drawn wrong: a var bound under another name (the
// bridge sends a var by its name), a call of it in a for (no fingerprint yet), a page that is no router.view (no
// bridge to call through), a required input the call does not pass, and a look in a .ui of its own. A name of the
// bind only the run knows is no error since the page runs the component as it is drawn (Request.Component).
//
// CONTROL (go test -overlay, 28/10/2026): with componentLogic not refusing what boundNames found, this test went
// red: the renamed var reached the page as a name of the page, an error of another kind.
func TestComponentWithJobsRefusesWhatItCannotDraw(t *testing.T) {
	computed := strings.Replace(liveCartLogic, "label, shippingFee", "label, total, shippingFee", 1)
	if _, err := compileLiveCart(t, liveCartFiles(computed, liveCartRoute, liveCartContent)); err != nil {
		t.Errorf("a name of the bind only the run knows compiles, the page runs the component: %v", err)
	}
	renamed := strings.Replace(liveCartLogic, "{ items, done,", "{ lines: items, done,", 1)
	cases := []struct {
		name, at, says string
		files          map[string]string
	}{
		{"renamed", "components.work:11:25: ", "var items vào bind dưới tên lines",
			liveCartFiles(renamed, liveCartRoute, liveCartContent)},
		{"in a for", "cart/content.ui:1:", "lặp theo for",
			liveCartFiles(liveCartLogic, `import * as router from "kitwork/router";
router.view((context) => context.bind({ user: "an", shops: [1, 2] }));
`, `<content><list><item for="shop of shops"><shop-cart owner="{{ user }}"><text>{{ label }}</text><button click="checkout()">Đặt</button></shop-cart></item></list></content>`)},
		{"no router.view", "cart/content.ui:1:", "không vẽ bằng router.view",
			liveCartFiles(liveCartLogic, `import * as router from "kitwork/router";
router.get((context) => context.view({ user: "an" }));
`, liveCartContent)},
		{"a required input", "cart/content.ui:1:", "thiếu đầu vào owner",
			liveCartFiles(liveCartLogic, liveCartRoute, strings.Replace(liveCartContent, `owner="{{ user }}" `, "", 1))},
	}
	for _, c := range cases {
		_, err := compileLiveCart(t, c.files)
		if err == nil || !strings.Contains(err.Error(), c.at) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: want an error at %q saying %q, got %v", c.name, c.at, c.says, err)
		}
	}
	withUI := liveCartFiles(liveCartLogic, liveCartRoute, `<content><shop-cart owner="{{ user }}" /></content>`)
	withUI["/site/shop-cart.ui"] = `<box><text>{{ label }}</text></box>`
	if _, err := compileLiveCart(t, withUI); err == nil || !strings.Contains(err.Error(), "chỉ nhận giao diện viết bên trong thẻ") {
		t.Errorf("a look in shop-cart.ui: %v", err)
	}
}

// A component whose bind holds neither a job nor a var is drawn on the server: drawn without Request.Component
// (here), a computed name of it is empty; the browser does not run it, so its host carries nothing for it.
func TestComponentWithoutJobsStaysTheServers(t *testing.T) {
	logic := `import * as component from "kitwork/component";
component.define("shop-badge", (context) => {
  const label = "Mới";
  const count = [1, 2].length;
  return context.bind({ label, count });
});
`
	page, err := compileLiveCart(t, map[string]string{
		"/site/components.work":  logic,
		"/site/cart/router.work": liveCartRoute,
		"/site/cart/content.ui":  `<content><shop-badge><text>{{ label }} {{ count }}</text></shop-badge></content>`,
	})
	if err != nil {
		t.Fatal(err)
	}
	html := page.Render(Request{Path: "/cart", Scope: map[string]any{"user": "an"}})
	mustContain(t, html, "<p>Mới </p>")
	mustNotContain(t, html, "data-kitwork-component=\"shop-badge", "data-kitwork-components")
}

// A call with nothing that changes per request (no input, no if, no show) is still an instance: its host is
// drawn each time, never folded into the page's fixed HTML, so the server finds it.
//
// CONTROL (go test -overlay, 28/10/2026): with fold folding such a host, this test went red: the content's root,
// fixed with the host in it, folded too and the page did not compile ("gốc của content.ui phải là một phần tử");
// on a page whose content holds something else that changes, a job of that cart answered 404 "trang này không vẽ
// shop-cart ở chỗ c1" (work TestComponentJobAnswersThePagesRevisions).
func TestInstanceOfACallWithNothingThatChanges(t *testing.T) {
	logic := `import * as component from "kitwork/component";
component.define("shop-cart", (context) => {
  var items = [];
  const checkout = () => { items = []; };
  return context.bind({ items, checkout });
});
`
	page, err := compileLiveCart(t, liveCartFiles(logic, liveCartRoute,
		`<content><shop-cart><button click="checkout()">Đặt</button></shop-cart></content>`))
	if err != nil {
		t.Fatal(err)
	}
	if cart, ok := page.Instances(Request{Path: "/cart", Scope: map[string]any{"user": "an"}})["c1"]; !ok || cart.Name != "shop-cart" {
		t.Errorf("a call with nothing that changes is still an instance: %+v", cart)
	}
}

// The page runs each component of the site whose logic answers a bind as it draws the call (Request.Component),
// with the inputs the call passes there: the cart draws the label its function computed for this request, and its
// host carries every name of its data for the kernel to start the instance from; a badge whose bind holds data
// only is run too, and drawn with its count, its host carrying nothing (the browser does not run it). Drawn without
// the hook, the cart takes the values its source writes out. A function of the bind (a job) is not data: a value
// the hook gives for it is not written.
//
// CONTROL (go test -overlay, 28/10/2026): with renderOne not asking Request.Component, this test went red: the
// label and the count drawn empty, the host carrying only the input shippingFee.
func TestComponentRunsAsThePageIsDrawn(t *testing.T) {
	logic := liveCartLogic + `
component.define("shop-badge", (context) => {
  const count = [1, 2].length;
  return context.bind({ count });
});
`
	computed := strings.Replace(logic, "label, shippingFee", "label, total, shippingFee", 1)
	content := strings.Replace(liveCartContent, "<text>{{ label }}</text>", "<text>{{ label }}</text><text>{{ total }} điểm</text>", 1)
	content = strings.Replace(content, "</shop-cart></content>", "</shop-cart><shop-badge><text>{{ count }} đơn</text></shop-badge></content>", 1)
	page, err := compileLiveCart(t, liveCartFiles(computed, liveCartRoute, content))
	if err != nil {
		t.Fatal(err)
	}
	var calls []Instance
	run := func(call Instance) map[string]any {
		calls = append(calls, call)
		switch call.Name {
		case "shop-cart":
			owner, _ := call.Inputs["owner"].(string)
			return map[string]any{"items": []any{}, "done": false, "label": "Giỏ của " + owner, "total": float64(4),
				"shippingFee": call.Inputs["shippingFee"], "checkout": "a job is no data"}
		case "shop-badge":
			return map[string]any{"count": float64(2)}
		}
		return nil
	}
	html := page.Render(Request{Path: "/cart", Scope: map[string]any{"user": "an"}, Component: run})
	mustContain(t, html,
		`data-kitwork-at="c1" data-kitwork-scope="done: false; items: []; label: &#39;Giỏ của an&#39;; shippingFee: 20000; total: 4"`,
		`>Giỏ của an</p>`, `>4 điểm</p>`, `<p>2 đơn</p>`)
	mustNotContain(t, html, `data-kitwork-component="shop-badge`, "a job is no data")
	if len(calls) != 2 || calls[0].Name != "shop-cart" || calls[0].Inputs["owner"] != "an" || calls[0].Inputs["shippingFee"] != float64(20000) ||
		!strings.HasSuffix(calls[0].Logic, "components.work") || calls[1].Name != "shop-badge" {
		t.Errorf("each call runs once, with its inputs where it stands: %+v", calls)
	}
	plain := page.Render(Request{Path: "/cart", Scope: map[string]any{"user": "an"}})
	mustContain(t, plain, `data-kitwork-scope="shippingFee: 20000"`, `>Giỏ hàng</p>`)
	mustNotContain(t, plain, "Giỏ của an", "2 đơn")
}

// On a page that runs in the kernel an if of the page's data is drawn hidden, for the browser to show it without a
// reload; a component it holds is drawn too, hidden. While the if is false for the request, that component is no
// instance a job may be called on: the server finds none at its place. True, it is.
//
// CONTROL (go test -overlay, 28/10/2026): with noteInstance counting an instance under a false if, this test went
// red: the admin tools were an instance at c1 for a visitor who is not an admin, and their job answered.
func TestInstanceUnderAFalseIfIsNone(t *testing.T) {
	logic := `import * as component from "kitwork/component";
component.define("admin-tools", (context) => {
  var done = false;
  const wipe = () => { done = true; };
  return context.bind({ done, wipe });
});
`
	route := "import * as router from \"kitwork/router\";\nrouter.view((context) => {\n  var note = \"\";\n  const isAdmin = false;\n" +
		"  return context.bind({ note, isAdmin });\n});\n"
	content := `<content><input value="{{ note }}" /><box if="isAdmin"><admin-tools><button click="wipe()">Xoá hết</button></admin-tools></box></content>`
	page, err := compileLiveCart(t, liveCartFiles(logic, route, content))
	if err != nil {
		t.Fatal(err)
	}
	visitor := map[string]any{"note": "", "isAdmin": false}
	if html := page.Render(Request{Path: "/cart", Scope: visitor}); !strings.Contains(html, `data-kitwork-at="c1"`) {
		t.Fatalf("the page still draws the tools, hidden, for the browser to show them without a reload:\n%s", html)
	}
	if instances := page.Instances(Request{Path: "/cart", Scope: visitor}); len(instances) != 0 {
		t.Errorf("under a false if the tools are no instance: %+v", instances)
	}
	admin := map[string]any{"note": "", "isAdmin": true}
	if tools, ok := page.Instances(Request{Path: "/cart", Scope: admin})["c1"]; !ok || tools.Name != "admin-tools" {
		t.Errorf("under a true if the tools are the instance at c1: %+v", tools)
	}
}
