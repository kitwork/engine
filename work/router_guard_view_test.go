package work

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
)

// A guard draws no page (KITWORK_ROUTER.md mục 4, 25/10/2026). The compiler refuses context.view in a guard it
// can follow (compiler/router_guard.go); a guard it cannot (one a call makes, one read off an object, one of a
// library that takes view apart from the context) is refused as it runs: the request fails 500 naming what to
// write, never the template renderer's "Error reading index" of before, the page text the guard wrote before its
// view (context.title(…).view) included. A guard that writes page text and then says no answers 403, not that
// text through the template renderer. Both hold when the site also holds a template's index.kitwork.html. The
// handler after a guard that lets the request on still draws its page, and a template site's guard is not refused
// (its view is deferred, and the handler's answer goes out, as before).
//
// CONTROL (go test -overlay, 25/10/2026): with refuseGuardView refusing nothing, every refused row went red: the
// guard's view was taken as its answer and drawn through the template renderer, 500 "Error reading index: …
// index.kitwork.html", and with the template's index 500 "không có trang …\<route>\page.kitwork.html", either way
// without the words of the rule. With runStage never setting guarding, the same; with it never clearing guarding,
// the handler's page after a guard that lets the request on answered 500 with the rule.
// After the review: with runStage keeping what a failing UI guard began, /titled/x answered 500 "[Error reading
// index: …]", and with the template's index 500 "không có trang …\titled\x\page.kitwork.html", the template
// renderer either way; with it keeping the page text of a guard that says no, /refused answered the same two 500s
// instead of 403. After the second review: with runStage clearing only the page text of a failing UI guard, not the
// answer it began, /failafter answered 401 application/json {"a":1} though the guard failed.
func TestGuardDrawsNoPageAsItRuns(t *testing.T) {
	refusal := compiler.ContextViewInGuard("context.view(…)")
	site := map[string]string{
		"acme/.shared/gate.work": "export const gate = (context) => {\n  const { view } = context;\n  return view({ login: true });\n};\n" +
			"export const open = () => () => null;\n",
		"acme/localhost/router.work": routerImport + "router.get((context) => context.text(\"root\"));\n",
		// a guard a call makes, in a branch's config.work and nothing else in its folder
		"acme/localhost/made/config.work":   routerImport + "const make = () => (context) => context.view({ login: true });\nrouter.guard(make());\n",
		"acme/localhost/made/x/router.work": routerImport + "router.get((context) => context.text(\"x\"));\n",
		// the same, with the page's text before its view
		"acme/localhost/titled/config.work":   routerImport + "const make = () => (context) => context.title(\"Đăng nhập\").view({ login: true });\nrouter.guard(make());\n",
		"acme/localhost/titled/x/router.work": routerImport + "router.get((context) => context.text(\"x\"));\n",
		// a route's guard read off an object, through the response
		"acme/localhost/object/router.work": routerImport + "const rules = { gate: (context) => context.response.view({ login: true }) };\n" +
			"router.get((context) => context.text(\"x\")).guard(rules.gate);\n",
		// a guard of a library that takes view apart from the context (the router.work rule is the router file's)
		"acme/localhost/library/router.work": routerImport + "import { gate } from \"app/.shared/gate.work\";\nrouter.guard(gate);\n" +
			"router.get((context) => context.text(\"x\"));\n",
		// a guard that begins an answer, then fails: the failure is the answer
		"acme/localhost/failafter/router.work": routerImport + "router.guard((context) => {\n  context.status(401).json({ a: 1 });\n  return context.text(\"y\");\n});\n" +
			"router.get((context) => context.text(\"x\"));\n",
		// a guard that writes the page's text, then says no
		"acme/localhost/refused/router.work": routerImport + "router.guard((context) => {\n  context.title(\"Đăng nhập\");\n  return false;\n});\n" +
			"router.get((context) => context.text(\"x\"));\n",
		// a guard that lets the request on, then the handler's page
		"acme/localhost/page/router.work": routerImport + "import { open } from \"app/.shared/gate.work\";\nrouter.guard(open());\n" +
			"router.get((context) => context.view({ name: \"Quán\" })).guard(open());\n",
		"acme/localhost/page/content.ui": "<content><text>PAGE-{{ name }}</text></content>",
	}
	for _, index := range []bool{false, true} {
		files := map[string]string{}
		for path, content := range site {
			files[path] = content
		}
		if index {
			// a template's index left in the site: the guard's page text never reaches it
			files["acme/localhost/index.kitwork.html"] = "<!doctype html><html><body>TEMPLATE-INDEX {{ @page }}</body></html>"
		}
		tenant, err := bootAppSite(t, t.TempDir(), files, "localhost")
		if err != nil {
			t.Fatalf("index %v: run: %v", index, err)
		}
		for _, target := range []string{"/made/x", "/titled/x", "/object", "/library"} {
			rec := serveAt(tenant, http.MethodGet, "http://localhost"+target)
			if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), refusal) {
				t.Errorf("index %v, %s: %d %q, want 500 with %q", index, target, rec.Code, rec.Body.String(), refusal)
			}
		}
		if rec := serveAt(tenant, http.MethodGet, "http://localhost/failafter"); rec.Code != http.StatusInternalServerError ||
			strings.Contains(rec.Body.String(), `{"a":1}`) || !strings.Contains(rec.Body.String(), "`.text()` sau `.json()`") {
			t.Errorf("index %v, /failafter: %d %q, want 500 with the guard's failure, not its half-made answer", index, rec.Code, rec.Body.String())
		}
		if rec := serveAt(tenant, http.MethodGet, "http://localhost/refused"); rec.Code != http.StatusForbidden ||
			strings.Contains(rec.Body.String(), "TEMPLATE-INDEX") || strings.Contains(rec.Body.String(), "Error reading index") {
			t.Errorf("index %v, /refused: %d %q, want 403 with no page", index, rec.Code, rec.Body.String())
		}
		if rec := serveAt(tenant, http.MethodGet, "http://localhost/page"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "PAGE-Quán") {
			t.Errorf("index %v, /page: %d %q, want 200 with the handler's page", index, rec.Code, rec.Body.String())
		}
	}

	// a template site's guard is not refused: its view is deferred, the handler answers, as before
	template, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"x\")).guard((ctx) => ctx.view({ login: \"yes\" }));\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("run template: %v", err)
	}
	if rec := serveAt(template, http.MethodGet, "http://localhost/"); rec.Code != http.StatusOK || rec.Body.String() != "x" {
		t.Errorf("template: %d %q, want 200 \"x\"", rec.Code, rec.Body.String())
	}
}
