package work

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// uiGetSite is a router.work site whose routes answer with router.get and context.view({ … }), beside
// one written with router.view and one API route (20/10/2026).
func uiGetSite(t *testing.T) *Tenant {
	t.Helper()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work":     `import * as router from "kitwork/router";`,
		"page.ui":         "<page><content /></page>",
		"notfound.ui":     "<content><text>KHONG-THAY</text></content>",
		"error.ui":        "<content><text>LOI: {{ message }}</text></content>",
		"menu/content.ui": `<content><text id="menu">MENU {{ note }} {{ money(2) }} {{ runs }}</text><link id="next" value="/old">Cũ</link></content>`,
		"menu/router.work": "import * as router from \"kitwork/router\";\nlet runs = 0;\n" +
			"router.get((context) => { runs = runs + 1; return context.title(\"Thực đơn\").description(\"Cà phê\").view({ note: \"sữa\", money: (n) => n * 1000, runs }); }).cache(\"1h\");\n",
		"old/content.ui":   `<content><text id="old">CU {{ note }}</text></content>`,
		"old/router.work":  "import * as router from \"kitwork/router\";\nrouter.view((context) => { context.title(\"Cũ\"); const note = \"view\"; });\n",
		"api/router.work":  "import * as router from \"kitwork/router\";\nrouter.get((context) => context.json({ api: true }));\n",
		"bare/router.work": "import * as router from \"kitwork/router\";\nrouter.get((context) => context.view({ note: 1 }));\n",
		"gone/router.work": "import * as router from \"kitwork/router\";\nrouter.get((context) => context.title(\"Không tìm thấy\").notFound());\n",
		"boom/router.work": "import * as router from \"kitwork/router\";\nrouter.get((context) => context.title(\"Hỏng\").error(\"hết hàng\"));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// In router.work, router.get answering context.title(…).description(…).view({ … }) draws the folder's
// .ui page as router.view does: the page's text in its head, the object's names in the page (a function
// of it the page calls); router.view still draws its page; context.json in a folder is still an API.
func TestUIGetViewDrawsThePage(t *testing.T) {
	tn := uiGetSite(t)
	menu := serveGet(tn, "/menu", nil)
	body := menu.Body.String()
	for _, part := range []string{"<title>Thực đơn</title>", `content="Cà phê"`, "MENU sữa 2000 1", "<html"} {
		if !strings.Contains(body, part) {
			t.Errorf("/menu lacks %q: %d\n%.600s", part, menu.Code, body)
		}
	}
	if menu.Code != http.StatusOK || !strings.HasPrefix(menu.Header().Get("Content-Type"), "text/html") {
		t.Errorf("/menu: %d %q", menu.Code, menu.Header().Get("Content-Type"))
	}
	if r := serveGet(tn, "/old", nil); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "CU view") || !strings.Contains(r.Body.String(), "<title>Cũ</title>") {
		t.Errorf("router.view: %d %.300q", r.Code, r.Body.String())
	}
	if r := serveGet(tn, "/api", nil); r.Body.String() != `{"api":true}` {
		t.Errorf("/api: %d %q", r.Code, r.Body.String())
	}
}

// A folder with no page of its own answers context.view with an error that says so, not the template's
// index.kitwork.html.
func TestUIGetViewWithoutAPageSaysSo(t *testing.T) {
	tn := uiGetSite(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	r := serveGet(tn, "/bare", nil)
	if r.Code != http.StatusInternalServerError {
		t.Errorf("/bare: %d %q", r.Code, r.Body.String())
	}
	if logged := logs.String(); !strings.Contains(logged, "context.view(…) vẽ trang .ui của thư mục, mà /bare không có content.ui") || strings.Contains(logged, "index.kitwork.html") {
		t.Errorf("/bare logged:\n%s", logged)
	}
}

// The page's text stands before notFound() and error() too: they draw a page (notfound.ui, error.ui).
func TestUIPageTextBeforeNotFoundAndError(t *testing.T) {
	tn := uiGetSite(t)
	if r := serveGet(tn, "/gone", nil); r.Code != http.StatusNotFound || !strings.Contains(r.Body.String(), "<title>Không tìm thấy</title>") || !strings.Contains(r.Body.String(), "KHONG-THAY") {
		t.Errorf("/gone: %d %.300q", r.Code, r.Body.String())
	}
	if r := serveGet(tn, "/boom", nil); r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "<title>Hỏng</title>") || !strings.Contains(r.Body.String(), "LOI: hết hàng") {
		t.Errorf("/boom: %d %.300q", r.Code, r.Body.String())
	}
}

// The route's .cache() keeps the page for its load and for a move by Drive, each under its own key.
func TestUIGetViewIsKeptForLoadAndDrive(t *testing.T) {
	tn := uiGetSite(t)
	drive := map[string]string{"X-Kitwork-Hydrate": "1"}
	steps := []struct {
		label  string
		header map[string]string
		runs   string
		hit    string
	}{
		{"load", nil, "MENU sữa 2000 1", ""},
		{"load again", nil, "MENU sữa 2000 1", "hit"},
		{"drive", drive, "MENU sữa 2000 2", ""},
		{"drive again", drive, "MENU sữa 2000 2", "hit"},
		{"load after drive", nil, "MENU sữa 2000 1", "hit"},
	}
	for _, step := range steps {
		r := serveGet(tn, "/menu", step.header)
		if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), step.runs) || r.Header().Get("X-Kitwork-Cache") != step.hit {
			t.Errorf("%s: %d cache=%q, want %q and cache=%q", step.label, r.Code, r.Header().Get("X-Kitwork-Cache"), step.runs, step.hit)
		}
	}
}

// A real browser loads the page router.get drew, and Drive moves from it to a router.view page without a
// reload.
func TestBrowserUIGetView(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tn := uiGetSite(t)
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		forward.Header.Del("Accept-Encoding")
		answer := httptest.NewRecorder()
		serving.Lock()
		tn.Serve(answer, forward)
		serving.Unlock()
		for name, values := range answer.Header() {
			response.Header()[name] = values
		}
		response.WriteHeader(answer.Code)
		_, _ = io.Copy(response, answer.Body)
	}))
	defer server.Close()

	d := openDevtools(t, browser, server.URL+"/menu", 900, 700, "")
	// " of 0" until the page is the site's: loaded asks again while the browser is still on its blank one
	got := d.loaded(`(function () { var menu = document.getElementById("menu"); return menu ? document.title + " | " + menu.textContent + " of 1" : " of 0"; })()`)
	if !strings.HasPrefix(got, "Thực đơn | MENU sữa 2000 1") {
		t.Fatalf("the page the browser shows: %q\n%s", got, d.evaluate(`location.href + " " + document.documentElement.outerHTML.slice(0, 1500)`))
	}
	moved := d.evaluate(`new Promise(function (done) {
  window.__stayed = "yes";
  document.getElementById("next").click();
  var tries = 0;
  (function wait() {
    var old = document.getElementById("old");
    if (old || tries++ > 50) { done(document.title + " | " + (old ? old.textContent : "none") + " | " + (window.__stayed || "reloaded")); return; }
    setTimeout(wait, 100);
  })();
})`)
	if moved != "Cũ | CU view | yes" {
		t.Fatalf("the move by Drive: %q", moved)
	}
}
