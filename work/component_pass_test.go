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

// A call has the budget the draw has (29/10/2026): one VM serves every run of components.work for the request, the
// pass that finds the instance, the run before the job, the job, the pass after it, the run after; each runs with
// the app's MaxEnergy of its own. The page draws four boxes that each count to 2000; with the least budget the
// page draws with, a job of one box answers.
//
// CONTROL (by hand, 29/10/2026): with runOnItsOwn leaving the energy as it was, the call ran out of energy (red).
func TestAComponentCallHasTheBudgetTheDrawHas(t *testing.T) {
	logic := "import * as component from \"kitwork/component\";\n" +
		"const sum = (n) => {\n  let total = 0;\n  for (let i = 0; i < n; i++) {\n    total = total + i;\n  }\n  return total;\n};\n" +
		"component.define(\"busy-box\", (context) => {\n" +
		"  const { label } = context.attributes();\n" +
		"  const total = sum(2000);\n" +
		"  var picked = \"\";\n" +
		"  const pick = () => { picked = label; };\n" +
		"  return context.bind({ total, picked, pick });\n" +
		"});\n"
	box := func(label string) string {
		return `<busy-box label="` + label + `"><text>{{ picked }}</text><button click="pick()">` + label + `</button></busy-box>`
	}
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":      `import * as router from "kitwork/router";`,
		"view.ui":          "<view><content /></view>",
		"components.work":  logic,
		"busy/content.ui":  "<content>" + box("a") + box("b") + box("c") + box("d") + "</content>",
		"busy/router.work": "import * as router from \"kitwork/router\";\nrouter.view((context) => context.bind({ busy: true }));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	places := placesOf(t, tn, "/busy")
	if len(places) != 4 {
		t.Fatalf("the page should draw four boxes: %v", places)
	}
	// the least budget the page draws with
	low, high := uint64(1000), uint64(50_000_000)
	for low+1 < high {
		tn.MaxEnergy = (low + high) / 2
		if serveGet(tn, "/busy", nil).Code == http.StatusOK {
			high = tn.MaxEnergy
		} else {
			low = tn.MaxEnergy
		}
	}
	tn.MaxEnergy = high + high/4
	r := postCall(tn, "/busy", "busy-box.pick", callBody{Seq: 1, Args: []any{}, Page: vars(), At: places[1], Mine: vars("picked", "")}.String())
	if r.Code != http.StatusOK || instanceOf(componentAnswer(t, r.Body.String()), places[1])["picked"] != "b" {
		t.Fatalf("with the budget the page draws with (%d), the job of box b should answer: %d %.600s", tn.MaxEnergy, r.Code, r.Body.String())
	}
}

// A component of the page that fails fails a call of the page as it fails its draw (29/10/2026): before the job, not
// a "this page does not draw it" (404); after it, not an answer that leaves out the instance's changes. As the draw,
// the answer carries the failure's text only while developing (30/10/2026): the box's message quotes the cookie it
// read and never bound, which in production reaches the browser by no name on its return line. The box fails for the
// visitor "boom"; the cart's breakIt breaks the box for every run after.
//
// CONTROL (by hand, 29/10/2026): with viewInstances giving no error, the first call answered 404 and the second 200
// with no receipt (red). CONTROL (by hand, 30/10/2026): with answerDrawFailure writing the failure's text whatever
// AllowLocal, the production answers held the cookie (red).
func TestAComponentThatFailsFailsTheCall(t *testing.T) {
	logic := "import * as component from \"kitwork/component\";\nimport { datastore } from \"kitwork/storage\";\n" +
		"const broken = datastore(\"boxes\").segment(\"broken\");\n" +
		"component.define(\"guest-box\", (context) => {\n" +
		"  const who = context.cookie(\"visitor\") ?? \"khách\";\n" +
		"  if (who === \"boom\") {\n    fail(\"hộp hỏng với \" + who);\n  }\n" +
		"  if (broken.get() === true) {\n    fail(\"hộp hỏng sau việc, khách \" + who);\n  }\n" +
		"  return context.bind({ who });\n" +
		"});\n" +
		"component.define(\"guest-cart\", (context) => {\n" +
		"  const { owner } = context.attributes();\n" +
		"  var receipt = \"\";\n" +
		"  const checkout = () => { receipt = \"cho \" + owner; };\n" +
		"  const breakIt = () => { broken.set(true); };\n" +
		"  return context.bind({ receipt, checkout, breakIt });\n" +
		"});\n"
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":     `import * as router from "kitwork/router";`,
		"view.ui":         "<view><content /></view>",
		"components.work": logic,
		"guest/content.ui": `<content><guest-box><text>{{ who }}</text>` +
			`<guest-cart owner="{{ who }}"><text>{{ receipt }}</text><button click="checkout()">Đặt</button><button click="breakIt()">Hỏng</button></guest-cart></guest-box></content>`,
		"guest/router.work": "import * as router from \"kitwork/router\";\nrouter.view((context) => context.bind({ guest: true }));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	at := placesOf(t, tn, "/guest")[0]
	call := func(job, visitor string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/guest?"+ViewCallParameter+"=guest-cart."+job,
			strings.NewReader(callBody{Seq: 1, Args: []any{}, Page: vars(), At: at, Mine: vars("receipt", "")}.String()))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "visitor="+visitor)
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		return rec
	}
	// in production: what failed and the request id, not the box's text
	if r := call("checkout", "boom"); r.Code != http.StatusInternalServerError || strings.Contains(r.Body.String(), "boom") ||
		!strings.Contains(r.Body.String(), "trang không vẽ được (Request ID: kw-") {
		t.Errorf("a box that fails before the job should fail the call, without its text: %d %s", r.Code, r.Body.String())
	}
	if r := call("breakIt", "Lan"); r.Code != http.StatusInternalServerError || strings.Contains(r.Body.String(), "Lan") ||
		!strings.Contains(r.Body.String(), "trang không vẽ lại được sau việc guest-cart.breakIt (Request ID: kw-") {
		t.Errorf("a box that fails after the job should fail the answer, without its text: %d %s", r.Code, r.Body.String())
	}
	// while developing: the box's text, to find the fault
	saved := AllowLocal
	AllowLocal = true
	t.Cleanup(func() { AllowLocal = saved })
	if r := call("checkout", "Lan"); r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "hộp hỏng sau việc, khách Lan") {
		t.Errorf("while developing, the answer should carry the box's text: %d %s", r.Code, r.Body.String())
	}
}
