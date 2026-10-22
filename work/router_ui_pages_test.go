package work

import (
	"net/http"
	"strings"
	"testing"
)

// uiPagesSite is a Kitwork UI site with a not-found page at its root and one in shop/, and an error page
// at its root; its routes call notFound() and error(…) from their handlers.
func uiPagesSite(t *testing.T, extra map[string]string) *Tenant {
	t.Helper()
	files := map[string]string{
		"router.work":      `import * as router from "kitwork/router";`,
		"view.ui":          "<view><content /></view>",
		"notfound.ui":      "<content><text>KHONG-THAY-GOC</text></content>",
		"shop/notfound.ui": "<content><text>KHONG-THAY-SHOP</text></content>",
		"error.ui":         "<content><text>LOI: {{ message }}</text></content>",
		"shop/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get((context) => context.notFound());\n",
		"shop/item/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.notFound());\n",
		"other/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.notFound());\n",
		"boom/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.error(\"Hết hàng\"));\n",
		"fail/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get((context) => { context.error(\"Kho đóng\"); });\n",
		"runs/router.work": "import * as router from \"kitwork/router\";\n" +
			"let runs = 0;\n" +
			"router.get(({ response }) => { runs = runs + 1; return response.header(\"X-Runs\", \"\" + runs).notFound(); });\n",
		"kept/router.work": "import * as router from \"kitwork/router\";\n" +
			"let runs = 0;\n" +
			"router.get(({ response }) => { runs = runs + 1; return response.header(\"X-Runs\", \"\" + runs).notFound(); }).cache(\"1h\");\n",
		"failing/router.work": "import * as router from \"kitwork/router\";\n" +
			"let runs = 0;\n" +
			"router.get(({ response }) => { runs = runs + 1; return response.error(\"lần \" + runs); }).cache(\"1h\");\n",
	}
	for name, source := range extra {
		files[name] = source
	}
	tn := NewTenant(writeSite(t, files), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// notFound() in a router.work handler draws the nearest notfound.ui with 404 — the folder's own, else
// the nearest above — the way a path no folder matches draws it (20/10/2026).
func TestUINotFoundDrawsTheNearestPage(t *testing.T) {
	tn := uiPagesSite(t, nil)
	for _, c := range []struct{ path, want string }{
		{"/shop", "KHONG-THAY-SHOP"},      // context.notFound(), the folder's own
		{"/shop/item", "KHONG-THAY-SHOP"}, // response.notFound(), the parent's
		{"/other", "KHONG-THAY-GOC"},      // the root's
		{"/nothing-here", "KHONG-THAY-GOC"},
	} {
		for _, drive := range []bool{false, true} {
			header := map[string]string(nil)
			if drive {
				header = map[string]string{"X-KitJS-Drive": "1"}
			}
			r := serveGet(tn, c.path, header)
			if r.Code != http.StatusNotFound || !strings.Contains(r.Body.String(), c.want) || !strings.Contains(r.Body.String(), "<html") {
				t.Errorf("%s (drive %v): %d %.200q, want 404 with %s", c.path, drive, r.Code, r.Body.String(), c.want)
			}
		}
	}
}

// error(…) draws the nearest error.ui with 500, reading the handler's message; without error.ui it is
// the message as text, still 500 (it was 200).
func TestUIErrorDrawsTheErrorPage(t *testing.T) {
	tn := uiPagesSite(t, nil)
	for path, want := range map[string]string{"/boom": "LOI: Hết hàng", "/fail": "LOI: Kho đóng"} {
		for _, drive := range []bool{false, true} {
			header := map[string]string(nil)
			if drive {
				header = map[string]string{"X-KitJS-Drive": "1"}
			}
			r := serveGet(tn, path, header)
			if r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), want) || !strings.Contains(r.Body.String(), "<html") {
				t.Errorf("%s (drive %v): %d %.300q, want 500 with %q", path, drive, r.Code, r.Body.String(), want)
			}
		}
	}

	bare := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"boom/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.error(\"Hết hàng\"));\n",
	}), "localhost")
	if err := bare.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(bare, "/boom", nil); r.Code != http.StatusInternalServerError || r.Body.String() != "Hết hàng" {
		t.Errorf("no error.ui: %d %q, want 500 with the message", r.Code, r.Body.String())
	}
}

// The page answers as every answer does, by the route's cache rules (KITWORK_ROUTER §9; 20/10/2026: the
// cache is the route's): no .cache() on the route, the handler runs again; with it, the 404 is kept and
// the 500 never.
func TestUINotFoundFollowsTheCacheRules(t *testing.T) {
	tn := uiPagesSite(t, nil)
	first := serveGet(tn, "/runs", nil)
	second := serveGet(tn, "/runs", nil)
	if first.Code != http.StatusNotFound || first.Header().Get("X-Runs") != "1" || second.Header().Get("X-Runs") != "2" || second.Header().Get("X-Kitwork-Cache") != "" {
		t.Fatalf("not kept: %d runs=%q, then %d runs=%q cache=%q", first.Code, first.Header().Get("X-Runs"), second.Code, second.Header().Get("X-Runs"), second.Header().Get("X-Kitwork-Cache"))
	}
	kept := serveGet(tn, "/kept", nil)
	again := serveGet(tn, "/kept", nil)
	if kept.Code != http.StatusNotFound || again.Code != http.StatusNotFound || again.Header().Get("X-Kitwork-Cache") != "hit" || again.Header().Get("X-Runs") != "1" || !strings.Contains(again.Body.String(), "KHONG-THAY-GOC") {
		t.Fatalf("kept: %d, then %d cache=%q runs=%q %.120q", kept.Code, again.Code, again.Header().Get("X-Kitwork-Cache"), again.Header().Get("X-Runs"), again.Body.String())
	}
	serveGet(tn, "/failing", nil)
	if r := serveGet(tn, "/failing", nil); r.Code != http.StatusInternalServerError || r.Header().Get("X-Kitwork-Cache") != "" || !strings.Contains(r.Body.String(), "LOI: lần 2") {
		t.Fatalf("a 500 was kept: %d cache=%q %.200q", r.Code, r.Header().Get("X-Kitwork-Cache"), r.Body.String())
	}
}

// A template site keeps its own handling of notFound() and error(…).
func TestTemplateSiteKeepsItsNotFoundAndError(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"notfound.ui":        "<content><text>KHONG-DUNG</text></content>",
		"error.ui":           "<content><text>KHONG-DUNG</text></content>",
		"a/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => ctx.response.notFound());\n",
		"b/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => ctx.response.error(\"x\"));\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(tn, "/a", nil); r.Code != http.StatusNotFound || r.Body.String() != "404 Not Found" {
		t.Errorf("/a: %d %q", r.Code, r.Body.String())
	}
	if r := serveGet(tn, "/b", nil); r.Body.String() != "x" || strings.Contains(r.Body.String(), "KHONG-DUNG") {
		t.Errorf("/b: %d %q", r.Code, r.Body.String())
	}
}
