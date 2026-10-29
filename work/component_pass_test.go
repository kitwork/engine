package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// guestSite is a page whose cart stands inside a box that computes, on the server, who looks (a cookie), and
// passes it to the cart as its owner: an input the server reads where the call stands, out of a component around
// it that the browser does not run.
func guestSite(t *testing.T) *Tenant {
	t.Helper()
	logic := "import * as component from \"kitwork/component\";\n" +
		"component.define(\"guest-box\", (context) => {\n" +
		"  const who = context.cookie(\"visitor\") ?? \"khách\";\n" +
		"  return context.bind({ who });\n" +
		"});\n" +
		"component.define(\"guest-cart\", (context) => {\n" +
		"  const { owner } = context.attributes();\n" +
		"  var receipt = \"\";\n" +
		"  const checkout = () => { receipt = \"cho \" + owner; };\n" +
		"  return context.bind({ receipt, checkout });\n" +
		"});\n"
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":     `import * as router from "kitwork/router";`,
		"view.ui":         "<view><content /></view>",
		"components.work": logic,
		"guest/content.ui": `<content><guest-box><text>{{ who }}</text>` +
			`<guest-cart owner="{{ who }}"><text>{{ receipt }}</text><button click="checkout()">Đặt</button></guest-cart></guest-box></content>`,
		"guest/router.work": "import * as router from \"kitwork/router\";\nrouter.view((context) => context.bind({ guest: true }));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// The instance a job is called on is found by one pass over the page that runs its components as the page's draw
// does (29/10/2026): its inputs are what the person was shown. The cart's owner is the box's who, which the box
// computes from a cookie; a pass that ran no component read who as its source writes it (nothing), and the
// checkout of Lan's cart named no one.
//
// CONTROL (by hand, 29/10/2026): with viewInstances drawing the page without running its components, the receipt
// read "cho " and nothing else (red).
func TestAComponentJobReadsItsInputsAsThePageDrewThem(t *testing.T) {
	tn := guestSite(t)
	page := serveGet(tn, "/guest", map[string]string{"Cookie": "visitor=Lan"}).Body.String()
	if !strings.Contains(page, ">Lan</p>") {
		t.Fatalf("the box should compute who looks:\n%.2000s", page)
	}
	at := placesOf(t, tn, "/guest")[0]
	req := httptest.NewRequest(http.MethodPost, "http://localhost/guest?"+ViewCallParameter+"=guest-cart.checkout",
		strings.NewReader(callBody{Seq: 1, Args: []any{}, Page: vars(), At: at, Mine: vars("receipt", "")}.String()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "visitor=Lan")
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	if rec.Code != http.StatusOK || instanceOf(componentAnswer(t, rec.Body.String()), at)["receipt"] != "cho Lan" {
		t.Fatalf("the cart's owner should be who the page showed, Lan: %d %s", rec.Code, rec.Body.String())
	}
}

// A job of a component loads components.work once for the request (29/10/2026): the pass that finds the
// instance, the run before the job and the run after it share the file. Its top level counts its loads; two calls
// one after the other see the count grow by one.
//
// CONTROL (by hand, 29/10/2026): with runComponent and the renderer loading the file each time, the count grew
// by four a call (red).
func TestAComponentJobLoadsItsFileOnce(t *testing.T) {
	logic := "import * as component from \"kitwork/component\";\nimport { datastore } from \"kitwork/storage\";\n" +
		"const loads = datastore(\"files\").segment(\"loads\");\n" +
		"loads.set((loads.get() ?? 0) + 1);\n" +
		"component.define(\"load-counter\", (context) => {\n" +
		"  var seen = 0;\n" +
		"  const look = () => { seen = loads.get(); };\n" +
		"  return context.bind({ seen, look });\n" +
		"});\n"
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":       `import * as router from "kitwork/router";`,
		"view.ui":           "<view><content /></view>",
		"components.work":   logic,
		"count/content.ui":  `<content><load-counter><text>{{ seen }}</text><button click="look()">Xem</button></load-counter></content>`,
		"count/router.work": "import * as router from \"kitwork/router\";\nrouter.view((context) => context.bind({ count: true }));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	at := placesOf(t, tn, "/count")[0]
	look := func() float64 {
		r := postCall(tn, "/count", "load-counter.look", callBody{Seq: 1, Args: []any{}, Page: vars(), At: at, Mine: vars("seen", 0)}.String())
		seen, _ := instanceOf(componentAnswer(t, r.Body.String()), at)["seen"].(float64)
		return seen
	}
	first := look()
	if second := look(); first == 0 || second != first+1 {
		t.Fatalf("each call should load components.work once: the count went from %v to %v", first, second)
	}
}
