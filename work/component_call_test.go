package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// cartComponent is a component of the site whose jobs go through the bridge (28/10/2026, KITWORK_COMPONENTS.md
// mục 8): an input the page passes (owner, required), one with a default the call overrides (shippingFee), vars,
// and jobs; checkout writes what it read of its inputs into a var, for the tests to see where they came from.
const cartComponent = "import * as component from \"kitwork/component\";\n" +
	"component.define(\"shop-cart\", (context) => {\n" +
	"  const { owner, shippingFee = 15000 } = context.attributes();\n" +
	"  var items = [];\n" +
	"  var done = false;\n" +
	"  var receipt = \"\";\n" +
	"  const add = (name) => { items = items.concat([{ id: items.length + 1, name: name }]); done = false; };\n" +
	"  const checkout = () => { receipt = owner + \":\" + shippingFee + \":\" + items.length; items = []; done = true; };\n" +
	"  return context.bind({ items, done, receipt, shippingFee, add, checkout });\n" +
	"});\n"

// cartSite is a site whose page /cart draws the cart; /plain draws no cart, /locked draws it behind a guard
// that refuses everyone.
func cartSite(t *testing.T) *Tenant {
	t.Helper()
	cart := `<content><shop-cart owner="{{ user }}" shipping-fee="{{ 20000 }}" flex="column">` +
		`<list><item for="line of items; key: line.id"><text>{{ line.name }}</text></item></list>` +
		`<button click="add('cà phê')">Thêm</button><button click="checkout()" disabled="{{ checkout.pending }}">Đặt</button>` +
		`<text show="done">Đã nhận</text><text>{{ receipt }}</text></shop-cart></content>`
	view := "import * as router from \"kitwork/router\";\nrouter.view((context) => {\n  const user = \"an\";\n  return context.bind({ user });\n});\n"
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":       `import * as router from "kitwork/router";`,
		"view.ui":           "<view><content /></view>",
		"components.work":   cartComponent,
		"cart/content.ui":   cart,
		"cart/router.work":  view,
		"plain/content.ui":  `<content><text>{{ user }}</text></content>`,
		"plain/router.work": view,
		"locked/content.ui": cart,
		"locked/router.work": "import * as router from \"kitwork/router\";\nrouter.guard((context) => false);\n" +
			"router.view((context) => {\n  const user = \"an\";\n  return context.bind({ user });\n});\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// The page draws the cart as a component the browser runs: its host names it, the place it is called from and the
// data of its bind as its function gave it for this request (owner, not in its bind, stays on the server), what is
// written inside it is the kernel's, the components the page holds close its content, and the page loads the
// kernel and call.js though its own view binds no job.
func TestComponentWithJobsIsDrawnForTheBridge(t *testing.T) {
	tn := cartSite(t)
	r := serveGet(tn, "/cart", nil)
	body := r.Body.String()
	if r.Code != http.StatusOK {
		t.Fatalf("/cart: %d\n%.1500s", r.Code, body)
	}
	for _, part := range []string{
		`data-kitwork-component="shop-cart"`, `data-kitwork-at="` + placesOf(t, tn, "/cart")[0] + `"`, `data-kitwork-scope="done: false; items: []; receipt: &#39;&#39;; shippingFee: 20000"`,
		`data-kitwork-event="click: checkout()"`, `data-kitwork-event="click: add(&#39;cà phê&#39;)"`,
		`<!--kitwork-for-start id=s1-->`, `<script type="application/json" data-kitwork-page>`,
		`"jobs":{"shop-cart":["add","checkout"]}`, `"vars":{"shop-cart":["done","items","receipt"]}`, `/_kitwork/kernel.`, `/_kitwork/call.`,
	} {
		if !strings.Contains(body, part) {
			t.Errorf("/cart lacks %q\n%.3000s", part, body)
		}
	}
	if strings.Contains(body, "owner") {
		t.Errorf("owner is an input the look does not read: it stays on the server\n%.3000s", body)
	}
}

// componentAnswer reads a call's JSON answer.
// placesOf are the places of the components the browser runs on the page path draws (data-kitwork-at), in the
// order the page draws them: fixed by the source (ui callPlace), read here as a tab reads them.
func placesOf(t *testing.T, tn *Tenant, path string) []string {
	t.Helper()
	body := serveGet(tn, path, nil).Body.String()
	var out []string
	for _, part := range strings.Split(body, `data-kitwork-at="`)[1:] {
		out = append(out, part[:strings.Index(part, `"`)])
	}
	if len(out) == 0 {
		t.Fatalf("no component the browser runs on %s: %.300s", path, body)
	}
	return out
}

func componentAnswer(t *testing.T, body string) map[string]any {
	t.Helper()
	var answer map[string]any
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("no JSON: %v %.300s", err, body)
	}
	return answer
}

// A job of the cart runs on the instance the page draws at that place: its inputs read where the call stands
// (owner from the page's user, shippingFee 20000 as the call writes it), its vars as the browser sent them; the
// answer is what changed of that instance, never the page, never the page's data it did not touch.
//
// CONTROL (go test -overlay, 28/10/2026): with runComponent handing the function the vars the browser sent as its
// inputs (attributes: value.New(call.scope)), this test went red: receipt "mallory:0:2", the inputs the browser
// forged.
func TestComponentJobRunsOnTheInstanceThePageDraws(t *testing.T) {
	tn := cartSite(t)
	// the browser sends owner and shippingFee among the vars: they are no vars, and inputs never come from it
	r := postCall(tn, "/cart", "shop-cart.checkout",
		`{"args":[],"vars":{},"seq":5,"at":"`+placesOf(t, tn, "/cart")[0]+`","scope":{"items":[{"id":1,"name":"a"},{"id":2,"name":"b"}],"done":false,"receipt":"","owner":"mallory","shippingFee":0}}`)
	if r.Code != http.StatusOK {
		t.Fatalf("checkout: %d %.600s", r.Code, r.Body.String())
	}
	answer := componentAnswer(t, r.Body.String())
	if answer["seq"] != float64(5) {
		t.Errorf("seq %v, want the call's 5", answer["seq"])
	}
	components, _ := answer["components"].(map[string]any)
	mine, _ := components[placesOf(t, tn, "/cart")[0]].(map[string]any)
	patch, _ := mine["patch"].(map[string]any)
	if patch["receipt"] != "an:20000:2" {
		t.Errorf("receipt %v, want an:20000:2 (inputs where the call stands, items as sent)", patch["receipt"])
	}
	if patch["done"] != true || len(patch["items"].([]any)) != 0 {
		t.Errorf("patch %v, want done true and items []", patch)
	}
	if _, has := patch["shippingFee"]; has {
		t.Errorf("shippingFee did not change: %v", patch)
	}
	if _, has := answer["patch"]; has {
		t.Errorf("the page's data did not change: %.300s", r.Body.String())
	}
	if strings.Contains(r.Body.String(), "<") {
		t.Errorf("the answer is data, never HTML: %.300s", r.Body.String())
	}
}

// What the page does not draw is no door: a place where it draws nothing, another component's name at the
// cart's place, a name the cart does not bind as a job, the cart's job on a page that does not draw it; and a
// call of a component with no place is the request's error.
//
// CONTROL (go test -overlay, 28/10/2026): with serveComponentCall running what components.work defines under the
// name whether the page draws it there or not, this test went red: /plain answered 200, the cart's job run through a
// page that does not draw it, c9 on /cart 200, and other-cart a 404 of components.work, not of the page.
func TestComponentJobOnlyWhereThePageDrawsIt(t *testing.T) {
	tn := cartSite(t)
	for _, c := range []struct {
		path, job, body, says string
		code                  int
	}{
		{"/cart", "shop-cart.checkout", `{"at":"c9"}`, "không vẽ shop-cart ở chỗ c9", http.StatusNotFound},
		{"/cart", "other-cart.checkout", `{"at":"c1"}`, "không vẽ other-cart ở chỗ c1", http.StatusNotFound},
		{"/cart", "shop-cart.save", `{"at":"` + placesOf(t, tn, "/cart")[0] + `"}`, "shop-cart không có việc save", http.StatusNotFound},
		{"/cart", "shop-cart.items", `{"at":"` + placesOf(t, tn, "/cart")[0] + `"}`, "shop-cart không có việc items", http.StatusNotFound},
		{"/plain", "shop-cart.checkout", `{"at":"c1"}`, "không vẽ shop-cart ở chỗ c1", http.StatusNotFound},
		{"/cart", "shop-cart.checkout", `{}`, "cần chỗ gọi", http.StatusBadRequest},
		{"/cart", "shop-cart.checkout", `{"at":"../c1"}`, "cần chỗ gọi", http.StatusBadRequest},
	} {
		r := postCall(tn, c.path, c.job, c.body)
		if r.Code != c.code || !strings.Contains(r.Body.String(), c.says) {
			t.Errorf("%s %s %s: %d %s, want %d saying %q", c.path, c.job, c.body, r.Code, r.Body.String(), c.code, c.says)
		}
	}
}

// A job of a component runs under the guard of the page that draws it: a guard that refuses refuses the job.
func TestComponentJobRunsUnderThePagesGuard(t *testing.T) {
	tn := cartSite(t)
	r := postCall(tn, "/locked", "shop-cart.checkout", `{"at":"c1","scope":{"items":[]}}`)
	if r.Code != http.StatusForbidden {
		t.Fatalf("locked checkout: %d %.300s, want 403", r.Code, r.Body.String())
	}
	if strings.Contains(r.Body.String(), "components") {
		t.Errorf("a refused job changes nothing: %.300s", r.Body.String())
	}
}

// A page whose own view binds a var and a job, and that holds the cart: the page is compiled for the kernel, not
// to a program (a page holding a component is not, yet), and both the page's job and the cart's run through the
// bridge, each answering its own data.
func TestComponentJobOnAPageWithJobsOfItsOwn(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":     `import * as router from "kitwork/router";`,
		"view.ui":         "<view><content /></view>",
		"components.work": cartComponent,
		"shop/content.ui": `<content><input value="{{ note }}" /><button click="save()">Lưu</button><text>{{ saved }}</text>` +
			`<shop-cart owner="{{ user }}"><button click="checkout()">Đặt</button><text>{{ receipt }}</text></shop-cart></content>`,
		"shop/router.work": "import * as router from \"kitwork/router\";\nrouter.view((context) => {\n" +
			"  var note = \"\";\n  var saved = \"\";\n  const user = \"an\";\n" +
			"  const save = () => { saved = note; };\n" +
			"  return context.bind({ note, saved, user, save });\n});\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	body := serveGet(tn, "/shop", nil).Body.String()
	at := placesOf(t, tn, "/shop")[0]
	for _, part := range []string{`data-kitwork-component="shop-cart" data-kitwork-at="` + at + `"`, `"components":{"` + at + `":"shop-cart"}`, `data-kitwork-page>`, `data-kitwork-event="click: save()"`} {
		if !strings.Contains(body, part) {
			t.Errorf("/shop lacks %q\n%.3000s", part, body)
		}
	}
	if strings.Contains(body, `"program"`) {
		t.Errorf("a page holding a component has no program yet\n%.3000s", body)
	}
	page := componentAnswer(t, postCall(tn, "/shop", "save", `{"args":[],"vars":{"note":"giao trước 9 giờ","saved":""},"seq":1}`).Body.String())
	if patch, _ := page["patch"].(map[string]any); patch["saved"] != "giao trước 9 giờ" {
		t.Errorf("the page's job: %v", page)
	}
	cart := componentAnswer(t, postCall(tn, "/shop", "shop-cart.checkout", `{"args":[],"vars":{"note":"x","saved":""},"seq":2,"at":"`+at+`","scope":{"items":[{"id":1}],"done":false,"receipt":""}}`).Body.String())
	mine, _ := cart["components"].(map[string]any)[at].(map[string]any)
	if patch, _ := mine["patch"].(map[string]any); patch["receipt"] != "an:15000:1" {
		t.Errorf("the cart's job: %v", cart)
	}
	if _, has := cart["patch"]; has {
		t.Errorf("the cart's job changed nothing of the page: %v", cart)
	}
}

// ordersSite is a page that runs in the kernel and holds the cart, whose checkout writes the orders the page
// counts: a job of the cart changes the page's data.
func ordersSite(t *testing.T) *Tenant {
	t.Helper()
	logic := "import * as component from \"kitwork/component\";\nimport { datastore } from \"kitwork/storage\";\n" +
		"const orders = datastore(\"shop\").segment(\"orders\");\n" +
		"component.define(\"shop-cart\", (context) => {\n" +
		"  var items = [];\n" +
		"  const add = (name) => { items = items.concat([{ id: items.length + 1, name: name }]); };\n" +
		"  const checkout = () => { orders.set((orders.get() ?? []).concat([items.length])); items = []; };\n" +
		"  return context.bind({ items, add, checkout });\n" +
		"});\n"
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":     `import * as router from "kitwork/router";`,
		"view.ui":         "<view><content /></view>",
		"components.work": logic,
		"shop/content.ui": `<content><input value="{{ note }}" /><text>{{ count }} đơn</text>` +
			`<shop-cart><button click="add('trà')">Thêm</button><button click="checkout()">Đặt</button></shop-cart></content>`,
		"shop/router.work": "import * as router from \"kitwork/router\";\nimport { datastore } from \"kitwork/storage\";\n" +
			"const orders = datastore(\"shop\").segment(\"orders\");\n" +
			"router.view((context) => {\n  var note = \"\";\n  const count = (orders.get() ?? []).length;\n" +
			"  return context.bind({ note, count });\n});\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// A job of a component answers the page's revisions as a job of the page does (view_patch.go staleRevs): the
// tab that called holds the new revision of what the job changed of the page, a call that changes nothing of
// the page sends nothing of it to a tab up to date, and a tab drawn before another tab's checkout gets the
// count whole on its next call of the cart, though that call changed nothing of the page. A var has no
// revision. The answer carries the moment the page's data was read (stamp), after the page's own.
//
// CONTROL (go test -overlay, 28/10/2026): with serveComponentCall answering the page's data without revisions
// (the staleRevs block taken out), this test went red: the checkout answered no revision of count, and tab B's
// add answered no count, the tab left showing "0 đơn" over two orders.
func TestComponentJobAnswersThePagesRevisions(t *testing.T) {
	tn := ordersSite(t)
	page := serveGet(tn, "/shop", nil).Body.String()
	m := regexp.MustCompile(`data-kitwork-page>(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no data block:\n%.2000s", page)
	}
	var block struct {
		Bridge struct {
			Revs  map[string]map[string]string `json:"revs"`
			Stamp float64                      `json:"stamp"`
		} `json:"bridge"`
	}
	if err := json.Unmarshal([]byte(m[1]), &block); err != nil || block.Bridge.Revs["page"]["count"] == "" {
		t.Fatalf("the page should carry the revision of count: %v %s", err, m[1])
	}
	tabA, tabB := map[string]string{}, map[string]string{}
	for k, v := range block.Bridge.Revs["page"] {
		tabA[k], tabB[k] = v, v
	}
	call := func(revs map[string]string, job string) (map[string]any, string) {
		held, _ := json.Marshal(revs)
		r := postCall(tn, "/shop", job, `{"args":["trà"],"vars":{"note":""},"seq":1,"revs":`+string(held)+`,"at":"`+placesOf(t, tn, "/shop")[0]+`","scope":{"items":[{"id":1,"name":"trà"}]}}`)
		if r.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", job, r.Code, r.Body.String())
		}
		answer := componentAnswer(t, r.Body.String())
		if got, ok := answer["revs"].(map[string]any); ok {
			for k, v := range got {
				revs[k], _ = v.(string)
			}
		}
		return answer, r.Body.String()
	}

	answer, body := call(tabA, "shop-cart.checkout")
	if patch, _ := answer["patch"].(map[string]any); patch["count"] != float64(1) {
		t.Errorf("the checkout changed the page's count: %s", body)
	}
	revs, _ := answer["revs"].(map[string]any)
	if revs["count"] == nil || revs["count"] == block.Bridge.Revs["page"]["count"] {
		t.Errorf("the tab that called should get the new revision of count: %s", body)
	}
	if _, has := revs["note"]; has {
		t.Errorf("a var has no revision: %s", body)
	}
	if components, _ := answer["components"].(map[string]any); components[placesOf(t, tn, "/shop")[0]] == nil {
		t.Errorf("the cart's own data comes as before: %s", body)
	}
	if stamp, _ := answer["stamp"].(float64); block.Bridge.Stamp <= 0 || stamp <= block.Bridge.Stamp {
		t.Errorf("the answer should carry the moment the page's data was read, after the page's own (%.0f): %s", block.Bridge.Stamp, body)
	}
	call(tabA, "shop-cart.checkout") // two orders now

	// tab A holds the count as it is: an add changes nothing of the page, and nothing of it comes
	answer, body = call(tabA, "shop-cart.add")
	if answer["patch"] != nil || answer["revs"] != nil {
		t.Errorf("a tab up to date gets nothing of the page from a job that changed none of it: %s", body)
	}

	// tab B was drawn before both orders: its add brings the count whole, and its revision
	answer, body = call(tabB, "shop-cart.add")
	if patch, _ := answer["patch"].(map[string]any); patch["count"] != float64(2) {
		t.Errorf("a tab drawn before the orders should get the count whole (2): %s", body)
	}
	if tabB["count"] != tabA["count"] {
		t.Errorf("tab B should hold tab A's revision of count now: %v %v", tabA, tabB)
	}
}

// A page the server draws whole (no var, no job of its own) holds none of its data in the browser: a job of the
// cart on it sends nothing of the page's data, though the browser sends revisions.
//
// CONTROL (go test -overlay, 28/10/2026): with serveComponentCall answering the page's data on every page (the
// Live check taken out), this test went red: the answer carried user whole and its revision.
func TestComponentJobOnAPageDrawnWholeSendsNoPageData(t *testing.T) {
	tn := cartSite(t)
	r := postCall(tn, "/cart", "shop-cart.checkout", `{"args":[],"vars":{},"seq":1,"revs":{},"at":"`+placesOf(t, tn, "/cart")[0]+`","scope":{"items":[]}}`)
	answer := componentAnswer(t, r.Body.String())
	if answer["patch"] != nil || answer["revs"] != nil || answer["lists"] != nil || answer["stamp"] != nil {
		t.Errorf("a page drawn whole gets nothing of its data: %s", r.Body.String())
	}
	if components, _ := answer["components"].(map[string]any); components[placesOf(t, tn, "/cart")[0]] == nil {
		t.Errorf("the cart's own data comes as before: %s", r.Body.String())
	}
}

// shelfSite is a page drawing two carts with other inputs, each computing on the server, as the page is drawn,
// what the source cannot write out: the orders in storage, a label from its input, who looks from a cookie. /broken
// draws a component whose function fails.
func shelfSite(t *testing.T) *Tenant {
	t.Helper()
	logic := "import * as component from \"kitwork/component\";\nimport { datastore } from \"kitwork/storage\";\n" +
		"const orders = datastore(\"shelf\").segment(\"orders\");\n" +
		"component.define(\"shop-cart\", (context) => {\n" +
		"  const { owner } = context.attributes();\n" +
		"  var items = [];\n" +
		"  const placed = (orders.get() ?? []).length;\n" +
		"  const label = \"Giỏ của \" + owner;\n" +
		"  const visitor = context.cookie(\"visitor\") ?? \"khách\";\n" +
		"  const checkout = () => { orders.set((orders.get() ?? []).concat([items.length])); items = []; };\n" +
		"  return context.bind({ items, placed, label, visitor, checkout });\n" +
		"});\n" +
		"component.define(\"broken-box\", (context) => {\n" +
		"  const nothing = null;\n" +
		"  const value = nothing();\n" +
		"  return context.bind({ value });\n" +
		"});\n"
	cart := func(owner string) string {
		return `<shop-cart owner="` + owner + `"><text>{{ label }}</text><text>{{ placed }} đơn</text><text>{{ visitor }}</text>` +
			`<button click="checkout()">Đặt</button></shop-cart>`
	}
	view := "import * as router from \"kitwork/router\";\nrouter.view((context) => context.bind({ shelf: true }));\n"
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":        `import * as router from "kitwork/router";`,
		"view.ui":            "<view><content /></view>",
		"components.work":    logic,
		"shelf/content.ui":   `<content>` + cart("an") + cart("bình") + `</content>`,
		"shelf/router.work":  view,
		"broken/content.ui":  `<content><text>trước</text><broken-box><text>{{ value }}</text></broken-box></content>`,
		"broken/router.work": view,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// The page runs each cart's function as it draws it (KITWORK_COMPONENTS.md mục 9): the label from each cart's own
// input, the count of orders in storage, who looks from the request's cookie; each host carries its own data for
// the kernel. A checkout answers the new count of the cart that called, and the next page draws both carts with it.
//
// CONTROL (go test -overlay, 28/10/2026): with respondUI not handing the page the components' runs
// (request.Component), this test went red: the labels, the count and the visitor drawn empty, the hosts carrying
// none of that data, and the visitor of a cookie not drawn.
func TestComponentRunsAsThePageIsDrawn(t *testing.T) {
	tn := shelfSite(t)
	body := serveGet(tn, "/shelf", nil).Body.String()
	for _, part := range []string{
		`data-kitwork-at="` + placesOf(t, tn, "/shelf")[0] + `" data-kitwork-scope="items: []; label: &#39;Giỏ của an&#39;; placed: 0; visitor: &#39;khách&#39;"`,
		`data-kitwork-at="` + placesOf(t, tn, "/shelf")[1] + `" data-kitwork-scope="items: []; label: &#39;Giỏ của bình&#39;; placed: 0; visitor: &#39;khách&#39;"`,
		`>Giỏ của an</p>`, `>Giỏ của bình</p>`, `>0 đơn</p>`, `>khách</p>`,
	} {
		if !strings.Contains(body, part) {
			t.Errorf("/shelf lacks %q\n%.3000s", part, body)
		}
	}
	if seen := serveGet(tn, "/shelf", map[string]string{"Cookie": "visitor=Lan"}).Body.String(); !strings.Contains(seen, `>Lan</p>`) {
		t.Errorf("who looks is the request's: want Lan\n%.3000s", seen)
	}

	r := postCall(tn, "/shelf", "shop-cart.checkout", `{"args":[],"vars":{},"seq":1,"at":"`+placesOf(t, tn, "/shelf")[0]+`","scope":{"items":[{"id":1}]}}`)
	answer := componentAnswer(t, r.Body.String())
	mine, _ := answer["components"].(map[string]any)[placesOf(t, tn, "/shelf")[0]].(map[string]any)
	if patch, _ := mine["patch"].(map[string]any); patch["placed"] != float64(1) {
		t.Errorf("the checkout should answer the new count of its cart: %s", r.Body.String())
	}
	if body := serveGet(tn, "/shelf", nil).Body.String(); strings.Count(body, `>1 đơn</p>`) != 2 {
		t.Errorf("the next page draws both carts with the new count\n%.3000s", body)
	}
}

// A component whose function fails as the page is drawn fails the request, naming the component, as a failure of
// the view's function does: no page half drawn.
func TestComponentThatFailsAsThePageIsDrawnFailsTheRequest(t *testing.T) {
	saved := AllowLocal
	AllowLocal = true // developing: a failure answers with its text, which the test reads
	t.Cleanup(func() { AllowLocal = saved })
	tn := shelfSite(t)
	r := serveGet(tn, "/broken", nil)
	if r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "component broken-box") {
		t.Errorf("/broken: %d %.600s, want 500 naming broken-box", r.Code, r.Body.String())
	}
	if strings.Contains(r.Body.String(), "trước") {
		t.Errorf("no page half drawn: %.600s", r.Body.String())
	}
}

// A page that runs in the kernel draws the admin tools inside an if of its data, hidden for a visitor: their job
// is no visitor's to call (404, as for a component the page does not draw), and an admin's call runs.
//
// CONTROL (go test -overlay, 28/10/2026): with noteInstance counting an instance under a false if, the visitor's
// wipe answered 200 and ran.
func TestComponentJobUnderAFalseIfIsNotFound(t *testing.T) {
	logic := "import * as component from \"kitwork/component\";\n" +
		"component.define(\"admin-tools\", (context) => {\n  var done = false;\n  const wipe = () => { done = true; };\n" +
		"  return context.bind({ done, wipe });\n});\n"
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":     `import * as router from "kitwork/router";`,
		"view.ui":         "<view><content /></view>",
		"components.work": logic,
		"admin/content.ui": `<content><input value="{{ note }}" /><box if="isAdmin"><admin-tools><button click="wipe()">Xoá hết</button>` +
			`<text show="done">Đã xoá</text></admin-tools></box></content>`,
		"admin/router.work": "import * as router from \"kitwork/router\";\nrouter.view((context) => {\n  var note = \"\";\n" +
			"  const isAdmin = context.cookie(\"role\") == \"admin\";\n  return context.bind({ note, isAdmin });\n});\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	at := placesOf(t, tn, "/admin")[0]
	if body := serveGet(tn, "/admin", nil).Body.String(); !strings.Contains(body, `data-kitwork-at="`+at+`"`) {
		t.Fatalf("the page draws the tools hidden for the browser:\n%.2000s", body)
	}
	call := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/admin?"+ViewCallParameter+"=admin-tools.wipe",
			strings.NewReader(`{"args":[],"vars":{"note":""},"seq":1,"at":"`+at+`","scope":{"done":false}}`))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		return rec
	}
	if r := call(""); r.Code != http.StatusNotFound || !strings.Contains(r.Body.String(), "không vẽ admin-tools ở chỗ "+at) {
		t.Errorf("a visitor's wipe: %d %s, want 404", r.Code, r.Body.String())
	}
	r := call("role=admin")
	if r.Code != http.StatusOK {
		t.Fatalf("an admin's wipe: %d %s", r.Code, r.Body.String())
	}
	mine, _ := componentAnswer(t, r.Body.String())["components"].(map[string]any)[at].(map[string]any)
	if patch, _ := mine["patch"].(map[string]any); patch["done"] != true {
		t.Errorf("an admin's wipe runs: %s", r.Body.String())
	}
}
