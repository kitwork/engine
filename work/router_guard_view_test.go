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
// text through the template renderer. The handler after a guard that lets the request on still draws its page.
// (Until 27/10/2026 both held also with a template's index.kitwork.html left in the site, now a boot error, and a
// template site's guard that called ctx.view was not refused: ctx.view of a template site fails now.)
//
// CONTROL (go test -overlay, 25/10/2026): with refuseGuardAnswer refusing nothing, every refused row went red: the
// view went out and only runStage refused it once the guard was over, 500 "context.view(…) trong guard: guard chỉ
// …", without the words of the view rule ("guard không vẽ trang"), with or without the template's index (before
// runStage read what was sent, it was drawn through the template renderer: "Error reading index: …"). With runStage
// never setting guarding, the same; with it never clearing guarding, the handler's page after a guard that lets the
// request on answered 500 with the rule.
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
		// a guard a call makes that begins an answer, then answers again: the failure is the answer
		"acme/localhost/failafter/router.work": routerImport + "const make = () => (context) => {\n  context.status(401).json({ a: 1 });\n  return context.redirect(\"/login\");\n};\n" +
			"router.guard(make());\nrouter.get((context) => context.text(\"x\"));\n",
		// a guard that writes the page's text, then says no
		"acme/localhost/refused/router.work": routerImport + "router.guard((context) => {\n  context.title(\"Đăng nhập\");\n  return false;\n});\n" +
			"router.get((context) => context.text(\"x\"));\n",
		// a guard that lets the request on, then the handler's page
		"acme/localhost/page/router.work": routerImport + "import { open } from \"app/.shared/gate.work\";\nrouter.guard(open());\n" +
			"router.get((context) => context.view({ name: \"Quán\" })).guard(open());\n",
		"acme/localhost/page/content.ui": "<content><text>PAGE-{{ name }}</text></content>",
	}
	tenant, err := bootAppSite(t, t.TempDir(), site, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, target := range []string{"/made/x", "/titled/x", "/object", "/library"} {
		rec := serveAt(tenant, http.MethodGet, "http://localhost"+target)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), refusal) {
			t.Errorf("%s: %d %q, want 500 with %q", target, rec.Code, rec.Body.String(), refusal)
		}
	}
	if rec := serveAt(tenant, http.MethodGet, "http://localhost/failafter"); rec.Code != http.StatusInternalServerError ||
		strings.Contains(rec.Body.String(), `{"a":1}`) || !strings.Contains(rec.Body.String(), "`.redirect()` sau `.json()`") {
		t.Errorf("/failafter: %d %q, want 500 with the guard's failure, not its half-made answer", rec.Code, rec.Body.String())
	}
	if rec := serveAt(tenant, http.MethodGet, "http://localhost/refused"); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "Đăng nhập") {
		t.Errorf("/refused: %d %q, want 403 with no page", rec.Code, rec.Body.String())
	}
	if rec := serveAt(tenant, http.MethodGet, "http://localhost/page"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "PAGE-Quán") {
		t.Errorf("/page: %d %q, want 200 with the handler's page", rec.Code, rec.Body.String())
	}
}

// A guard answers only with context.redirect(…) or context.status(…).json(…), or lets the request on (nothing,
// true) or refuses it (false, 403) (KITWORK_ROUTER.md mục 4, 25/10/2026). One the compiler cannot follow (made
// by a call here) that answers otherwise, html, text, json with no status, notFound, error, or returns a value, fails
// as it runs: 500 in the compiler's words, never the answer it began. A template site's guard answers as before.
//
// CONTROL (go test -overlay, 25/10/2026): with refuseGuardAnswer allowing every answer, /text, /html and /json were
// refused only by runStage once the guard was over, with no call (want the refusal at the call: "call text"),
// /notfound answered the handler's 500 "`.text()` sau `.notFound()`", /error 500 "cấm" (error.ui) (before runStage
// read what was sent: /text 403 "đóng", /html 200, /json 200 {"a":1}); with runStage sending a returned value as
// before, /string answered 200 "đóng" and /object 200 {"a":1}.
func TestGuardAnswersOnlySoAsItRuns(t *testing.T) {
	made := func(guard string) string {
		return routerImport + "const make = () => " + guard + ";\nrouter.guard(make());\nrouter.get((context) => context.text(\"x\"));\n"
	}
	refused := map[string]struct{ guard, want string }{
		"/text":     {`(context) => context.status(403).text("đóng")`, compiler.ContextAnswerInGuard("context.text(…)")},
		"/html":     {`(context) => context.html("<h1>Đăng nhập</h1>")`, compiler.ContextAnswerInGuard("context.html(…)")},
		"/json":     {`(context) => context.json({ a: 1 })`, compiler.ContextAnswerInGuard("context.json(…) không có status")},
		"/notfound": {`(context) => context.notFound()`, compiler.ContextAnswerInGuard("context.notFound(…)")},
		"/error":    {`(context) => context.error("cấm")`, compiler.ContextAnswerInGuard("context.error(…)")},
		"/string":   {`(context) => "đóng"`, compiler.GuardReturnsValue("một chuỗi")},
		"/object":   {`(context) => ({ a: 1 })`, compiler.GuardReturnsValue("một object")},
	}
	files := map[string]string{
		"acme/localhost/router.work":          routerImport + "router.get((context) => context.text(\"root\"));\n",
		"acme/localhost/status/router.work":   made(`(context) => context.status(401).json({ error: "login" })`),
		"acme/localhost/redirect/router.work": made(`(context) => context.redirect("/login")`),
		"acme/localhost/true/router.work":     made(`(context) => true`),
		"acme/localhost/nothing/router.work":  made(`(context) => null`),
	}
	for target, row := range refused {
		files["acme/localhost"+target+"/router.work"] = made(row.guard)
	}
	tenant, err := bootAppSite(t, t.TempDir(), files, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for target, row := range refused {
		rec := serveAt(tenant, http.MethodGet, "http://localhost"+target)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), row.want) {
			t.Errorf("%s: %d %q, want 500 with %q", target, rec.Code, rec.Body.String(), row.want)
		}
	}
	// an answer is refused at its call, by the answer function itself
	for target, call := range map[string]string{"/text": "call text", "/html": "call html", "/json": "call json",
		"/notfound": "call notFound", "/error": "call error"} {
		if rec := serveAt(tenant, http.MethodGet, "http://localhost"+target); !strings.Contains(rec.Body.String(), call) {
			t.Errorf("%s: %q, want the refusal at the call (%q)", target, rec.Body.String(), call)
		}
	}
	for _, c := range []struct {
		target, body string
		code         int
	}{
		{"/status", `{"error":"login"}`, http.StatusUnauthorized},
		{"/redirect", "", http.StatusFound},
		{"/true", "x", http.StatusOK},
		{"/nothing", "x", http.StatusOK},
	} {
		rec := serveAt(tenant, http.MethodGet, "http://localhost"+c.target)
		if rec.Code != c.code || (c.body != "" && strings.TrimSpace(rec.Body.String()) != c.body) {
			t.Errorf("%s: %d %q, want %d %q", c.target, rec.Code, rec.Body.String(), c.code, c.body)
		}
	}
	if rec := serveAt(tenant, http.MethodGet, "http://localhost/redirect"); rec.Header().Get("Location") != "/login" {
		t.Errorf("/redirect: Location %q, want /login", rec.Header().Get("Location"))
	}

	// a template site's guard answers as before
	template, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"x\")).guard((ctx) => ctx.status(403).text(\"không\"));\n",
	}, "localhost")
	if err != nil {
		t.Fatalf("run template: %v", err)
	}
	if rec := serveAt(template, http.MethodGet, "http://localhost/"); rec.Code != http.StatusForbidden || rec.Body.String() != "không" {
		t.Errorf("template: %d %q, want 403 \"không\"", rec.Code, rec.Body.String())
	}
}

// A guard of a router.work or a config.work answers only so wherever it stands and however it answers: over a
// template's folder (router.kitwork.js) its answers are refused too, notFound and the template's response words
// (send…) among them, while its edits (cookie, title) go on as they do over a router.work; context.css is refused;
// a json stands only on a status the guard itself wrote, not one an earlier guard left; and the value a guard
// returns is refused at the guard's own place.
//
// CONTROL (go test -overlay, 25/10/2026): with Context.Cookie, Title and Notfound deciding by the leaf alone,
// /tpl/cookie and /tpl/title answered 500 "guard trả về một chuỗi" and "… một giá trị", and /tpl/notfound 200
// "tpl"; with runStage not reading what was sent, /tpl/send answered 200 "x"; with Response.CSS answering as
// before, /css was refused only by runStage, with no call; with an earlier guard's status counted, /status2
// answered 401 {"a":1}; with the returned value's error told from nowhere, /placed lacked "router.work:2:". With
// routerWork reading the leaf alone, /tpl/redirect/x answered 303 (a template's default) and /tpl/badcookie/x 200
// "tpl", its option unread.
func TestGuardAnswersOnlySoInEveryPlace(t *testing.T) {
	const template = "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"tpl\"));\n"
	const handler = "router.get((context) => context.text(\"x\"));\n"
	made := func(guard string) string {
		return routerImport + "const make = () => " + guard + ";\nrouter.guard(make());\n"
	}
	tenant, err := bootAppSite(t, t.TempDir(), map[string]string{
		"acme/localhost/router.work": routerImport + "router.get((context) => context.text(\"root\"));\n",
		// guards of a config.work over a template's folder
		"acme/localhost/tpl/text/config.work":             made(`(context) => context.status(403).text("đóng")`),
		"acme/localhost/tpl/text/x/router.kitwork.js":     template,
		"acme/localhost/tpl/view/config.work":             made(`(context) => context.view({ a: 1 })`),
		"acme/localhost/tpl/view/x/router.kitwork.js":     template,
		"acme/localhost/tpl/notfound/config.work":         made(`(context) => { context.notFound(); }`),
		"acme/localhost/tpl/notfound/x/router.kitwork.js": template,
		"acme/localhost/tpl/send/config.work":             made(`(context) => { context.response.send("x"); }`),
		"acme/localhost/tpl/send/x/router.kitwork.js":     template,
		"acme/localhost/tpl/cookie/config.work":           routerImport + "router.guard((context) => context.cookie(\"seen\", \"1\"));\n",
		"acme/localhost/tpl/cookie/x/router.kitwork.js":   template,
		"acme/localhost/tpl/title/config.work":            routerImport + "router.guard((context) => context.title(\"T\"));\n",
		"acme/localhost/tpl/title/x/router.kitwork.js":    template,
		// a redirect and a cookie the router.work way: 302 by default, the cookie's options checked
		"acme/localhost/tpl/redirect/config.work":          made(`(context) => context.redirect("/login")`),
		"acme/localhost/tpl/redirect/x/router.kitwork.js":  template,
		"acme/localhost/tpl/badcookie/config.work":         routerImport + "router.guard((context) => {\n  context.cookie(\"s\", \"1\", { sameSite: \"nonsense\" });\n});\n",
		"acme/localhost/tpl/badcookie/x/router.kitwork.js": template,
		// css, a status another guard wrote, the place of a returned value
		"acme/localhost/css/router.work":     made(`(context) => context.css("body{}")`) + handler,
		"acme/localhost/status2/config.work": routerImport + "router.guard((context) => {\n  context.status(401);\n});\n",
		"acme/localhost/status2/router.work": made(`(context) => context.json({ a: 1 })`) + handler,
		"acme/localhost/placed/router.work":  made(`(context) => "đóng"`) + handler,
	}, "localhost")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for target, want := range map[string]string{
		"/tpl/text/x":     compiler.ContextAnswerInGuard("context.text(…)"),
		"/tpl/view/x":     compiler.ContextViewInGuard("context.view(…)"),
		"/tpl/notfound/x": compiler.ContextAnswerInGuard("context.notFound(…)"),
		"/tpl/send/x":     compiler.ContextAnswerInGuard("context.response.send(…)"),
		"/css":            compiler.ContextAnswerInGuard("context.css(…)"),
		"/status2":        compiler.ContextAnswerInGuard("context.json(…) không có status"),
		"/placed":         "router.work:2:",
	} {
		rec := serveAt(tenant, http.MethodGet, "http://localhost"+target)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %q, want 500 with %q", target, rec.Code, rec.Body.String(), want)
		}
	}
	// an answer is refused where it is called, by the answer function, not only once the guard is over
	for target, call := range map[string]string{"/tpl/text/x": "call text", "/css": "call css"} {
		if rec := serveAt(tenant, http.MethodGet, "http://localhost"+target); !strings.Contains(rec.Body.String(), call) {
			t.Errorf("%s: %q, want the refusal at the call (%q)", target, rec.Body.String(), call)
		}
	}
	if rec := serveAt(tenant, http.MethodGet, "http://localhost/placed"); !strings.Contains(rec.Body.String(), compiler.GuardReturnsValue("một chuỗi")) {
		t.Errorf("/placed: %q, want %q", rec.Body.String(), compiler.GuardReturnsValue("một chuỗi"))
	}
	for _, target := range []string{"/tpl/cookie/x", "/tpl/title/x"} {
		if rec := serveAt(tenant, http.MethodGet, "http://localhost"+target); rec.Code != http.StatusOK || rec.Body.String() != "tpl" {
			t.Errorf("%s: %d %q, want 200 \"tpl\"", target, rec.Code, rec.Body.String())
		}
	}
	if rec := serveAt(tenant, http.MethodGet, "http://localhost/tpl/redirect/x"); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Errorf("/tpl/redirect/x: %d %q, want 302 to /login", rec.Code, rec.Header().Get("Location"))
	}
	if rec := serveAt(tenant, http.MethodGet, "http://localhost/tpl/badcookie/x"); rec.Code != http.StatusInternalServerError {
		t.Errorf("/tpl/badcookie/x: %d %q, want 500 refusing the cookie's option", rec.Code, rec.Body.String())
	}
	if rec := serveAt(tenant, http.MethodGet, "http://localhost/tpl/cookie/x"); !strings.Contains(rec.Header().Get("Set-Cookie"), "seen=1") {
		t.Errorf("/tpl/cookie/x: Set-Cookie %q, want seen=1", rec.Header().Get("Set-Cookie"))
	}
}
