package work

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
)

// router.view is gone (KITWORK_ROUTER.md, 23/10/2026): a page is router.get((context) => context.view({ … })).
// Written in a router.work it stops the boot where the compiler sees it; written where the compiler
// cannot follow the router (another name for it), and in a template site's router.kitwork.js, it stops the
// boot at the call, file:line:column, in the same words. Each names the spelling to write.
//
// CONTROL (23/10/2026, go test -overlay, the tree untouched): with FolderRouter.View declaring a GET again
// and the compiler's routerMember and routerDeclaration refusing nothing, the three sites booted:
// "router.work: run = <nil>, want … router.view đã bỏ"; with only the compiler's refusal off, the first
// failed at the call instead, "router.work:2:7: call view: router.view đã bỏ …", not at the word (2:8), so
// the row of the compiler's place went red alone.
func TestRouterViewIsGone(t *testing.T) {
	const view = "{\n  const note = \"x\";\n}"
	for _, c := range []struct {
		name, file, source, at string
	}{
		{"router.work", "router.work", "import * as router from \"kitwork/router\";\nrouter.view((context) => " + view + ");\n", "router.work:2:8: "},
		{"another name", "router.work", "import * as router from \"kitwork/router\";\nconst r = router;\nr.view((context) => " + view + ");\n", "router.work:3:"},
		{"template", "router.kitwork.js", "import { router } from \"kitwork\";\nrouter.view((ctx) => " + view + ");\n", "router.kitwork.js:2:"},
	} {
		_, err := bootUISite(t, map[string]string{
			"localhost/" + c.file:  c.source,
			"localhost/content.ui": "<content><text>{{ note }}</text></content>",
		}, true)
		if err == nil || !strings.Contains(err.Error(), c.at) || !strings.Contains(err.Error(), compiler.RouterViewRemoved) {
			t.Errorf("%s: run = %v, want %q at %s", c.name, err, compiler.RouterViewRemoved, c.at)
		}
	}
	if !strings.Contains(compiler.RouterViewRemoved, "router.get((context) => context.title(\"…\").view({ … }))") {
		t.Errorf("the error does not name the spelling to write: %s", compiler.RouterViewRemoved)
	}
}

// A page reads exactly the names of the object its handler hands context.view({ … }) (KITWORK_ROUTER.md,
// 23/10/2026): content.ui, the frame view.ui and the regions it calls (header.ui, footer.ui) all read the
// same names, a function of the object included; a const of the file, an import of it, and a variable of
// the handler that the object does not hand read as a name never given does. A path no folder answers
// draws notfound.ui with no names at all.
//
// CONTROL (23/10/2026, go test -overlay): with serveUIAnswer handing respondUI nothing (scope nil), the
// page, the frame and both regions lost the object's names: "/ lacks \"[head Kitwork]\"", "[foot Kitwork!]",
// the links; with viewData adding the names of the handler's scopes as router.view did, the file's const
// and its import reached the page: "/ page = \"[Kitwork] [file-const] [0] [function () { [native code] }]
// [0]\"", "/ shows \"file-const\"".
func TestUIPageReadsOnlyTheObjectOfItsView(t *testing.T) {
	tenant := buildUISite(t, map[string]string{
		".shared/site.work": "export const tagline = () => \"made with Go\";\n",
		"localhost/router.work": "import * as router from \"kitwork/router\";\nimport { tagline } from \"app/.shared/site.work\";\n" +
			"const secret = \"file-const\";\n" +
			"router.get((context) => {\n  const hidden = \"handler-local\";\n  const site = \"Kitwork\";\n" +
			"  return context.title(\"Trang\").view({ site, links: [{ url: \"/a\", label: \"A\" }], shout: (word) => word + \"!\" });\n});\n",
		"localhost/view.ui":     "<view><header /><content /><footer /></view>",
		"localhost/header.ui":   "<header><text id=\"head\">[head {{ site }}]</text><link for=\"entry of links\" value=\"{{ entry.url }}\">{{ entry.label }}</link></header>",
		"localhost/footer.ui":   "<footer><text id=\"foot\">[foot {{ shout(site) }}]</text></footer>",
		"localhost/content.ui":  "<content><text id=\"page\">[{{ site }}] [{{ secret }}] [{{ hidden }}] [{{ tagline }}] [{{ nothing }}]</text></content>",
		"localhost/notfound.ui": "<content><text id=\"missing\">[{{ site }}] [{{ nothing }}]</text></content>",
	})
	page := serveUI(t, tenant, "http://localhost/")
	body := page.Body.String()
	if page.Code != http.StatusOK {
		t.Fatalf("/ = %d\n%.1500s", page.Code, body)
	}
	for _, want := range []string{"<title>Trang</title>", "[head Kitwork]", `href="/a"`, ">A</a>", "[foot Kitwork!]"} {
		if !strings.Contains(body, want) {
			t.Errorf("/ lacks %q\n%.2500s", want, body)
		}
	}
	// secret, hidden and tagline read as nothing, a name never given, does
	if parts := bracketed(textOf(t, body, "page")); len(parts) != 5 || parts[0] != "Kitwork" ||
		parts[1] != parts[4] || parts[2] != parts[4] || parts[3] != parts[4] {
		t.Errorf("/ page = %q, want the object's site, then three names read as one never given", textOf(t, body, "page"))
	}
	for _, never := range []string{"file-const", "handler-local", "made with Go"} {
		if strings.Contains(body, never) {
			t.Errorf("/ shows %q, a name the object of its view does not hand:\n%.2500s", never, body)
		}
	}
	missing := serveUI(t, tenant, "http://localhost/khong-co")
	if parts := bracketed(textOf(t, missing.Body.String(), "missing")); missing.Code != http.StatusNotFound ||
		len(parts) != 2 || parts[0] != parts[1] {
		t.Errorf("/khong-co = %d %q, want notfound.ui with no names", missing.Code, textOf(t, missing.Body.String(), "missing"))
	}
}

// bracketed is what each [ … ] of a text holds: "[a] [b]" is a, b.
func bracketed(text string) []string {
	parts := strings.Split(text, "] [")
	for i := range parts {
		parts[i] = strings.TrimSuffix(strings.TrimPrefix(parts[i], "["), "]")
	}
	return parts
}

// textOf is the text of the paragraph of a page with that id.
func textOf(t *testing.T, page, id string) string {
	t.Helper()
	open := `<p id="` + id + `">`
	start := strings.Index(page, open)
	if start < 0 {
		t.Fatalf("no <p id=%q>: %.1500s", id, page)
	}
	rest := page[start+len(open):]
	return rest[:strings.Index(rest, "</p>")]
}

// A handler in router.work takes its context by position (KITWORK_ROUTER.md mục 5, 23/10/2026): the first
// parameter is the context whatever its name ((c), (content), (request)) or taken apart; in a hook of a
// failure the second is what failed; any other parameter is null. A template site's router.kitwork.js
// keeps its way, by name: (request, response) are the request and the response.
//
// CONTROL (23/10/2026, go test -overlay): with arguments binding router.work by name as a template's,
// (c), (content) and (request) were no context and /c, /content, /request answered 404 "404 page not
// found"; the guard's (gate) was null and refused /guarded?key=open with 403 "request rejected"; the hook's
// (c, failure) got nulls and /failed answered the handler's 500 instead of its 503.
func TestRouterWorkHandlerTakesItsContextByPosition(t *testing.T) {
	const header = "import * as router from \"kitwork/router\";\n"
	tenant := buildUISite(t, map[string]string{
		"localhost/router.work":         header,
		"localhost/c/router.work":       header + "router.get((c) => c.json({ path: c.request.path }));\n",
		"localhost/content/router.work": header + "router.get((content) => content.status(201).text(\"content\"));\n",
		"localhost/request/router.work": header + "router.get((request) => request.json({ first: true }));\n",
		"localhost/parts/router.work":   header + "router.get(({ request, response }) => response.json({ method: request.method }));\n",
		"localhost/second/router.work":  header + "router.get((context, extra, more) => context.json({ extra, more }));\n",
		"localhost/guarded/router.work": header + "router.guard((gate) => gate.query(\"key\") === \"open\");\nrouter.get((context) => context.text(\"inside\"));\n",
		"localhost/failed/router.work": header + "router.get((context) => {\n  fail(\"hỏng\");\n})" +
			".error((c, failure) => c.status(503).json({ code: failure.code, message: failure.message }));\n",
		"localhost/template/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((request, response) => response.json({ path: request.path }));\n",
	})
	for path, want := range map[string]struct {
		code int
		body string
	}{
		"/c":                {http.StatusOK, `{"path":"/c"}`},
		"/content":          {http.StatusCreated, "content"},
		"/request":          {http.StatusOK, `{"first":true}`},
		"/parts":            {http.StatusOK, `{"method":"GET"}`},
		"/second":           {http.StatusOK, `{"extra":null,"more":null}`},
		"/guarded?key=open": {http.StatusOK, "inside"},
		"/failed":           {http.StatusServiceUnavailable, `{"code":"FAIL","message":"hỏng"}`},
		"/template":         {http.StatusOK, `{"path":"/template"}`},
	} {
		rec := serveUI(t, tenant, "http://localhost"+path)
		if rec.Code != want.code || strings.TrimSpace(rec.Body.String()) != want.body {
			t.Errorf("%s = %d %.300q, want %d %s", path, rec.Code, rec.Body.String(), want.code, want.body)
		}
	}
	if rec := serveUI(t, tenant, "http://localhost/guarded"); rec.Code != http.StatusForbidden {
		t.Errorf("/guarded without the key = %d, want 403 from the guard that read its context", rec.Code)
	}
}

// A parameter of a router.work function that was read by name until 23/10/2026 and means something else
// read by position stops the boot where it is written (KITWORK_ROUTER.md mục 5): after the first, a name of
// the context, the request or the response ((request, response) => …, (req, res) => …, a guard's (context,
// response)) is the request's failure or null now; the first of a failure's hook named for the failure
// ((err, ctx) => …, router.error((e) => …)) is the context. Each is told at the function's arrow, as every
// error of a handler's parameters is, and names the spelling to write. CONTROLS: the spellings it names
// boot, a hook's (context, failure) and a handler's (e) among them, and a template site's router.kitwork.js
// keeps reading them by name.
//
// CONTROL (23/10/2026, go test -overlay): with positionalParameterError refusing nothing, every refused site
// booted ("(request, response) => …: run = <nil>, want router.work:2:32: tham số `response` đứng sau tham số
// đầu …").
func TestRouterWorkParameterReadByNameIsABootError(t *testing.T) {
	const header = "import * as router from \"kitwork/router\";\n"
	const after = "đứng sau tham số đầu nên không phải "
	const hook = "của móc lỗi là context: trong router.work tham số nhận theo vị trí (23/10/2026), lỗi của request là tham số thứ hai; viết (context, failure) => "
	for _, c := range []struct{ name, source, want string }{
		{"(request, response) => …", header + "router.get((request, response) => response.json({ a: 1 }));\n",
			"router.work:2:32: tham số `response` " + after + "response"},
		{"(req, res) => …", header + "router.get((req, res) => res.status(201).text(\"x\"));\n",
			"router.work:2:23: tham số `res` " + after + "response"},
		{"a guard's (context, response)", header + "router.guard((context, response) => {\n  response.header(\"X-A\", \"1\");\n});\n",
			"router.work:2:34: tham số `response` " + after + "response"},
		{"(context, Request)", header + "router.post((context, Request) => context.json({}));\n",
			"router.work:2:32: tham số `Request` " + after + "request"},
		{"(c, ctx)", header + "router.get((c, ctx) => c.json({}));\n",
			"router.work:2:21: tham số `ctx` " + after + "context"},
		{".error((err, ctx) => …)", header + "router.get((context) => {\n  fail(\"hỏng\");\n}).error((err, ctx) => ctx.status(503).json({ code: err.code }));\n",
			"router.work:4:21: tham số đầu `err` " + hook},
		{"router.error((e) => …)", header + "router.error((e) => ({ seen: e.code }));\nrouter.get((context) => context.text(\"x\"));\n",
			"router.work:2:18: tham số đầu `e` " + hook},
	} {
		_, err := bootUISite(t, map[string]string{"localhost/router.work": c.source}, true)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "(KITWORK_ROUTER.md mục 5)") {
			t.Errorf("%s: run = %v, want %s", c.name, err, c.want)
		}
	}
	for _, source := range []string{
		header + "router.get((context, failure) => context.json({ failure }));\n",
		header + "router.get((e) => e.text(\"e\"));\n",
		header + "router.get(({ request, response }) => response.json({ path: request.path }));\n",
		header + "router.get((context) => {\n  fail(\"hỏng\");\n}).error((context, error) => context.status(503).json({ code: error.code }));\n",
		header + "router.error((context, err) => context.status(503).json({ code: err.code }));\n",
	} {
		if _, err := bootUISite(t, map[string]string{"localhost/router.work": source}, true); err != nil {
			t.Errorf("%q: run = %v, want it to boot", source, err)
		}
	}
	if _, err := bootUISite(t, map[string]string{"localhost/router.kitwork.js": "import { router } from \"kitwork\";\n" +
		"router.get((request, response) => response.json({ path: request.path })).error((err, ctx) => ctx.status(503).json({ code: err.code }));\n"}, true); err != nil {
		t.Errorf("a template site's (request, response) and (err, ctx): run = %v, want it to boot", err)
	}
}

// A function takes the arguments of the router file that wrote it, not of the folder the request ends in
// (23/10/2026): a site may hold router.work in one folder and router.kitwork.js in another. A router.work's
// guard over a template folder takes its context by position; a template's guard and router.error over a
// router.work folder take theirs by name, the request, the response and what failed.
//
// CONTROL (23/10/2026, go test -overlay): with execTree choosing by the request's folder (uiRoute of the
// leaf) as before, /tpl?key=open answered 403 "request rejected" (the router.work guard's (c) was null); the
// template guard's (request, response) got the context and null, so /ui answered 200 "ui" with no X-Guard;
// and router.error's (err, ctx) got them too, so /ui/failed answered the handler's 500 "router.work:3:3:
// hỏng …" instead of the hook's 503.
func TestHandlerTakesTheArgumentsOfTheFileThatWroteIt(t *testing.T) {
	work := buildUISite(t, map[string]string{
		"localhost/router.work": "import * as router from \"kitwork/router\";\nrouter.guard((c) => c.query(\"key\") === \"open\");\n" +
			"router.get((context) => context.text(\"root\"));\n",
		"localhost/tpl/router.kitwork.js": "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.text(\"tpl\"));\n",
	})
	for path, want := range map[string]int{"/?key=open": http.StatusOK, "/tpl?key=open": http.StatusOK, "/tpl": http.StatusForbidden} {
		if rec := serveUI(t, work, "http://localhost"+path); rec.Code != want {
			t.Errorf("router.work over a template folder: %s = %d %.200q, want %d", path, rec.Code, rec.Body.String(), want)
		}
	}

	template := buildUISite(t, map[string]string{
		"localhost/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.guard((request, response) => {\n  response.header(\"X-Guard\", request.query(\"key\") || \"none\");\n});\n" +
			"router.error((err, ctx) => ctx.status(503).json({ code: err.code, message: err.message }));\n",
		"localhost/ui/router.work":        "import * as router from \"kitwork/router\";\nrouter.get((context) => context.text(\"ui\"));\n",
		"localhost/ui/failed/router.work": "import * as router from \"kitwork/router\";\nrouter.get((context) => {\n  fail(\"hỏng\");\n});\n",
	})
	rec := serveUI(t, template, "http://localhost/ui?key=k")
	if rec.Code != http.StatusOK || rec.Body.String() != "ui" || rec.Header().Get("X-Guard") != "k" {
		t.Errorf("a template guard over a router.work folder: /ui = %d %.200q X-Guard %q, want 200 ui k", rec.Code, rec.Body.String(), rec.Header().Get("X-Guard"))
	}
	rec = serveUI(t, template, "http://localhost/ui/failed")
	if body := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusServiceUnavailable || body != `{"code":"FAIL","message":"hỏng"}` {
		t.Errorf("a template router.error over a router.work folder: /ui/failed = %d %.300q, want 503 FAIL", rec.Code, body)
	}
}
