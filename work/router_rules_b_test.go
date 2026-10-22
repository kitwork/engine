package work

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// In router.work a folder's type it does not know, or a regex that does not compile, stops the site at
// boot (the tree is prepared then), not at the first request that reaches it (KITWORK_ROUTER §2,
// 20/10/2026). A template site keeps reading them as [string] and {name}.
func TestUIPathSegmentErrorsAtBoot(t *testing.T) {
	for folder, want := range map[string]string{
		"{id[numbr]}":  "thư mục shop/{id[numbr]}: kiểu \"numbr\" không có",
		"{code([A-Z)}": "thư mục shop/{code([A-Z)}: regex \"[A-Z\" không đọc được",
	} {
		ui := writeSite(t, map[string]string{
			"router.work":                     `import * as router from "kitwork/router";`,
			"shop/" + folder + "/router.work": "import * as router from \"kitwork/router\";\nrouter.get(({ response }) => response.text(\"x\"));\n",
		})
		if err := NewTenant(ui, "localhost").Run(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: run error = %v, want %q", folder, err, want)
		}
		template := writeSite(t, map[string]string{
			"filesystem.kitwork":                    "",
			"router.kitwork.js":                     `import { router } from "kitwork";`,
			"shop/" + folder + "/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"x\"));\n",
		})
		if err := NewTenant(template, "localhost").Run(); err != nil {
			t.Errorf("template %s: %v", folder, err)
		}
	}
}

// In router.work a regex is held to the whole segment, [number] gives a number, {...rest} takes the
// rest of the path. A template site keeps a regex that may match a part, text params, and a one-segment
// rest.
func TestUIPathMatching(t *testing.T) {
	routes := map[string]string{
		"code/{code([A-Z]{3})}": `context.text("code " + context.params("code"))`,
		"item/{id[number]}":     `context.json({ next: context.params("id") + 1, kind: typeof context.params("id") })`,
		"files/{...rest}":       `context.text("rest " + context.params("rest"))`,
	}
	ui := map[string]string{"router.work": `import * as router from "kitwork/router";`}
	template := map[string]string{"filesystem.kitwork": "", "router.kitwork.js": `import { router } from "kitwork";`}
	for folder, answer := range routes {
		ui[folder+"/router.work"] = "import * as router from \"kitwork/router\";\nrouter.get((context) => " + answer + ");\n"
		template[folder+"/router.kitwork.js"] = "import { router } from \"kitwork\";\nrouter.get((context) => " + answer + ");\n"
	}
	uiSite := NewTenant(writeSite(t, ui), "localhost")
	templateSite := NewTenant(writeSite(t, template), "localhost")
	for _, tn := range []*Tenant{uiSite, templateSite} {
		if err := tn.Run(); err != nil {
			t.Fatalf("run: %v", err)
		}
	}
	for _, c := range []struct {
		site       *Tenant
		label      string
		path, want string
		code       int
	}{
		{uiSite, "router.work regex whole", "/code/ABC", "code ABC", 200},
		{uiSite, "router.work regex anchored", "/code/ABCD", "", 404},
		{templateSite, "template regex part", "/code/ABCD", "code ABCD", 200},
		{uiSite, "router.work number", "/item/41", `{"kind":"number","next":42}`, 200},
		{templateSite, "template number is text", "/item/41", `{"kind":"string","next":"411"}`, 200},
		{uiSite, "router.work rest", "/files/a/b/c", "rest a/b/c", 200},
		{templateSite, "template rest one segment", "/files/a", "rest a", 200},
	} {
		r := serveGet(c.site, c.path, nil)
		if r.Code != c.code || (c.want != "" && r.Body.String() != c.want) {
			t.Errorf("%s %s: %d %.80q, want %d %q", c.label, c.path, r.Code, r.Body.String(), c.code, c.want)
		}
	}
}

// /a and /a/ are one path to the cache and to the rate limits, in both kinds of site.
func TestNormalizedPathKeys(t *testing.T) {
	for _, kind := range []string{"router.work", "router.kitwork.js"} {
		files := map[string]string{}
		if kind == "router.kitwork.js" {
			files["filesystem.kitwork"] = ""
		}
		files[kind] = "import * as router from \"kitwork/router\";\n"
		files["kept/"+kind] = "import * as router from \"kitwork/router\";\nlet runs = 0;\n" +
			"router.get(({ response }) => { runs = runs + 1; return response.text(\"runs \" + runs); }).cache(\"1h\");\n"
		files["limited/"+kind] = "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.text(\"ok\")).limit({ rate: 2, per: \"1m\" });\n"
		tn := NewTenant(writeSite(t, files), "localhost")
		if err := tn.Run(); err != nil {
			t.Fatalf("%s run: %v", kind, err)
		}
		serveGet(tn, "/kept", nil)
		if r := serveGet(tn, "/kept/", nil); r.Body.String() != "runs 1" || r.Header().Get("X-Kitwork-Cache") != "hit" {
			t.Errorf("%s /kept/: %q cache=%q, want the entry of /kept", kind, r.Body.String(), r.Header().Get("X-Kitwork-Cache"))
		}
		codes := []int{serveGet(tn, "/limited", nil).Code, serveGet(tn, "/limited/", nil).Code, serveGet(tn, "/limited//", nil).Code}
		if codes[2] != http.StatusTooManyRequests {
			t.Errorf("%s: /limited, /limited/, /limited// count as one: %v", kind, codes)
		}
	}
}

// In router.work a method the folder did not declare is 405 with Allow, OPTIONS is Allow, HEAD runs as
// GET and drops the body. A template site keeps its 404.
func TestUIMethodsAllowHeadOptions(t *testing.T) {
	ui := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"orders/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.header(\"X-Orders\", \"1\").json({ orders: [] }));\n" +
			"router.delete(({ response }) => response.json({ gone: true }));\n",
	}), "localhost")
	if err := ui.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	const allow = "DELETE, GET, HEAD, OPTIONS"
	if r := serveURL(ui, http.MethodPost, "http://localhost/orders"); r.Code != http.StatusMethodNotAllowed || r.Header().Get("Allow") != allow {
		t.Errorf("POST: %d Allow=%q", r.Code, r.Header().Get("Allow"))
	}
	if r := serveURL(ui, http.MethodOptions, "http://localhost/orders"); r.Code != http.StatusNoContent || r.Header().Get("Allow") != allow {
		t.Errorf("OPTIONS: %d Allow=%q", r.Code, r.Header().Get("Allow"))
	}
	head := serveURL(ui, http.MethodHead, "http://localhost/orders")
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("X-Orders") != "1" || !strings.HasPrefix(head.Header().Get("Content-Type"), "application/json") {
		t.Errorf("HEAD: %d body=%q headers=%v", head.Code, head.Body.String(), head.Header())
	}
	if r := serveURL(ui, http.MethodPost, "http://localhost/nothing-here"); r.Code != http.StatusNotFound {
		t.Errorf("POST to no folder: %d, want 404", r.Code)
	}

	template := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"orders/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => ctx.json({ orders: [] }));\n",
	}), "localhost")
	if err := template.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost/orders", nil)
	rec := httptest.NewRecorder()
	template.Serve(rec, req)
	if rec.Code != http.StatusNotFound || rec.Header().Get("Allow") != "" {
		t.Errorf("template POST: %d Allow=%q, want its 404", rec.Code, rec.Header().Get("Allow"))
	}
}

// An expiry the engine cannot read is a boot error in router.work and one line per file in a template
// site; it no longer turns the cache off without a word.
func TestUnreadableExpiry(t *testing.T) {
	err := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"a/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.text(\"x\")).cache(\"soon\");\n",
	}), "localhost").Run()
	if err == nil || !strings.Contains(err.Error(), `.cache("soon"): không đọc được hạn`) {
		t.Fatalf("router.work: run error = %v", err)
	}
	var tn *Tenant
	output := captureBootOutput(t, func() {
		tn = NewTenant(writeSite(t, map[string]string{
			"filesystem.kitwork": "",
			"router.kitwork.js":  `import { router } from "kitwork";`,
			"a/router.kitwork.js": "import { router } from \"kitwork\";\n" +
				"router.get((ctx) => ctx.text(\"x\")).cache(\"soon\").persist(\"later\");\n",
		}), "localhost")
		if err := tn.Run(); err != nil {
			t.Fatalf("template run: %v", err)
		}
	})
	if got := strings.Count(output, "không đọc được hạn"); got != 1 || !strings.Contains(output, `.cache("soon")`) {
		t.Fatalf("template warned %d times:\n%s", got, output)
	}
}
