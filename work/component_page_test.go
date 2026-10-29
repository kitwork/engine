package work

import (
	"net/http"
	"strings"
	"testing"
)

// pagesSite is a site whose pages draw the cart (cartComponent) without router.view: /bare is a content.ui with
// no router.get, a page with no data; /got is one router.get answers with context.view({ … }), whose user the
// call passes in as the owner; /shut draws it behind a guard that refuses everyone.
func pagesSite(t *testing.T) *Tenant {
	t.Helper()
	bare := `<content><shop-cart owner="{{ 'khách' }}" flex="column"><button click="add('trà')">Thêm</button>` +
		`<button click="checkout()">Đặt</button><text>{{ receipt }}</text></shop-cart></content>`
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":      `import * as router from "kitwork/router";`,
		"view.ui":          "<view><content /></view>",
		"components.work":  cartComponent,
		"bare/content.ui":  bare,
		"got/content.ui":   `<content><shop-cart owner="{{ user }}" shipping-fee="{{ 30000 }}"><button click="checkout()">Đặt</button></shop-cart></content>`,
		"got/router.work":  "import * as router from \"kitwork/router\";\nrouter.get((context) => context.view({ user: \"bình\" }));\n",
		"shut/content.ui":  bare,
		"shut/router.work": "import * as router from \"kitwork/router\";\nrouter.guard((context) => false);\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// A job of a component runs on a page router.view does not draw (29/10/2026): a header's account menu stands on
// every page, most of which draw no router.view. The server draws the page again to find the instance at its
// place and reads its inputs there: none for a page with no data, router.get's for one it answers, never what the
// browser sends. The page holds no data in the browser, so the answer is the instance's alone; a guard of the
// page's folders refuses the job as it refuses the page, and a page job is no job of such a page.
//
// CONTROL (go test -overlay, 29/10/2026): the dispatch of router_serve.go left as it was (a call only on a page
// router.view draws) turned this test red: /bare answered 405, /got the page's HTML.
func TestComponentJobOnAPageWithoutRouterView(t *testing.T) {
	tn := pagesSite(t)
	at := placesOf(t, tn, "/bare")[0]
	r := postCall(tn, "/bare", "shop-cart.add", `{"args":["trà"],"vars":{},"seq":1,"at":"`+at+`","scope":{"items":[],"done":false,"receipt":""}}`)
	if r.Code != http.StatusOK {
		t.Fatalf("a job on a page with no data: %d %s", r.Code, r.Body.String())
	}
	answer := componentAnswer(t, r.Body.String())
	mine, _ := answer["components"].(map[string]any)[at].(map[string]any)
	patch, _ := mine["patch"].(map[string]any)
	if items, _ := patch["items"].([]any); len(items) != 1 || answer["patch"] != nil {
		t.Errorf("the instance's data, never the page's: %s", r.Body.String())
	}
	r = postCall(tn, "/bare", "shop-cart.checkout", `{"args":[],"vars":{},"seq":2,"at":"`+at+`","scope":{"items":[{"id":1,"name":"trà"}],"owner":"mallory"}}`)
	mine, _ = componentAnswer(t, r.Body.String())["components"].(map[string]any)[at].(map[string]any)
	if patch, _ := mine["patch"].(map[string]any); patch["receipt"] != "khách:15000:1" {
		t.Errorf("checkout on the inputs where the call stands: %d %s", r.Code, r.Body.String())
	}

	got := placesOf(t, tn, "/got")[0]
	r = postCall(tn, "/got", "shop-cart.checkout", `{"args":[],"vars":{},"seq":1,"at":"`+got+`","scope":{"items":[],"owner":"mallory"}}`)
	mine, _ = componentAnswer(t, r.Body.String())["components"].(map[string]any)[got].(map[string]any)
	if patch, _ := mine["patch"].(map[string]any); r.Code != http.StatusOK || patch["receipt"] != "bình:30000:0" {
		t.Errorf("router.get's data, read where the call stands: %d %s", r.Code, r.Body.String())
	}

	for _, c := range []struct {
		path, job, body, says string
		code                  int
	}{
		{"/shut", "shop-cart.checkout", `{"at":"` + at + `"}`, "", http.StatusForbidden},
		{"/bare", "add", `{"args":["trà"]}`, "không vẽ bằng router.view nên không có việc add", http.StatusNotFound},
		{"/bare", "shop-cart.checkout", `{"at":"c9"}`, "không vẽ shop-cart ở chỗ c9", http.StatusNotFound},
	} {
		r := postCall(tn, c.path, c.job, c.body)
		if r.Code != c.code || !strings.Contains(r.Body.String(), c.says) {
			t.Errorf("%s %s: %d %s, want %d saying %q", c.path, c.job, r.Code, r.Body.String(), c.code, c.says)
		}
	}
}
