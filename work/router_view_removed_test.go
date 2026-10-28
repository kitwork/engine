package work

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
)

// router.view is a door again (28/10/2026, KITWORK_ROUTER_VIEW.md): written as a call at the head of a line of
// a router.work it boots, its function answering context.bind({ … }) (router_view_bind_test.go). Taken apart or
// read under another name it is still refused where it is written, naming the one spelling: another name for
// the router (const r = router) is refused since 24/10/2026 (compiler/router_rules.go).
//
// It was gone from 23/10 to 28/10/2026, when a page was only router.get((context) => context.view({ … })).
func TestRouterViewIsACallOfItsLine(t *testing.T) {
	const head = "import * as router from \"kitwork/router\";\n"
	const view = "{\n  const note = \"x\";\n  return context.bind({ note });\n}"
	if _, err := bootUISite(t, map[string]string{
		"localhost/router.work": head + "router.view((context) => " + view + ");\n",
		"localhost/content.ui":  "<content><text>{{ note }}</text></content>",
	}, true); err != nil {
		t.Errorf("router.view((context) => …): run = %v, want it to boot", err)
	}
	for _, c := range []struct {
		name, source, at, want string
	}{
		{"another name", head + "const r = router;\nr.view((context) => " + view + ");\n", "router.work:2:11: ", compiler.RouterAsValue("router")},
		{"taken apart", head + "const { view } = router;\nview((context) => " + view + ");\n", "router.work:2:", compiler.RouterViewRemoved},
	} {
		_, err := bootUISite(t, map[string]string{
			"localhost/router.work": c.source,
			"localhost/content.ui":  "<content><text>{{ note }}</text></content>",
		}, true)
		if err == nil || !strings.Contains(err.Error(), c.at) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: run = %v, want %q at %s", c.name, err, c.want, c.at)
		}
	}
	if !strings.Contains(compiler.RouterViewRemoved, "router.view((context) => context.bind({ … }))") {
		t.Errorf("the error does not name the spelling to write: %s", compiler.RouterViewRemoved)
	}
}

// A page reads exactly the names of the object its handler hands context.view({ … }) (KITWORK_ROUTER.md,
// 23/10/2026): content.ui, the frame view.ui and the regions it calls (header.ui, footer.ui) all read the
// same names, a function of the object included. Any other name a page reads is a compile error at the read
// since 24/10/2026 (KITWORK_UI.md mục 4, ui/given.go), answered 500 while developing with the names the page
// is given and never a value: a const of the file, an import of it, a variable of the handler that the
// object does not hand, a name nothing has. A path no folder answers draws notfound.ui, which is given no
// name at all, so a frame that reads the route's names fails there; a branch whose own view.ui reads none
// draws it. A folder of the site with no router.work draws its content.ui with nothing, and a name it reads
// is the error naming the folder.
//
// CONTROL (23/10/2026, go test -overlay): with serveUIAnswer handing respondUI nothing (scope nil), the
// page, the frame and both regions lost the object's names: "/ lacks \"[head Kitwork]\"", "[foot Kitwork!]",
// the links; with viewData adding the names of the handler's scopes as router.view did, the file's const
// and its import reached the page. CONTROL (24/10/2026, go test -overlay): with the render plan not marking
// the site a Kitwork UI site's, /titled answered 200 and /khong-co 404, the frame's names read as nothing;
// with ui's notGiven telling nothing, /secret, /hidden, /tagline and /nothing answered 200 too.
func TestUIPageReadsOnlyTheObjectOfItsView(t *testing.T) {
	route := "import * as router from \"kitwork/router\";\nimport { tagline } from \"app/.shared/site.work\";\n" +
		"const secret = \"file-const\";\n" +
		"router.get((context) => {\n  const hidden = \"handler-local\";\n  const site = \"Kitwork\";\n" +
		"  return context.title(\"Trang\").view({ site, links: [{ url: \"/a\", label: \"A\" }], shout: (word) => word + \"!\" });\n});\n"
	header := "<header><text id=\"head\">[head {{ site }}]</text><link for=\"entry of links\" value=\"{{ entry.url }}\">{{ entry.label }}</link></header>"
	plainView := "<view><content /></view>"
	files := map[string]string{
		".shared/site.work":           "export const tagline = () => \"made with Go\";\n",
		"localhost/router.work":       route,
		"localhost/view.ui":           "<view><header /><content /><footer /></view>",
		"localhost/header.ui":         header,
		"localhost/footer.ui":         "<footer><text id=\"foot\">[foot {{ shout(site) }}]</text></footer>",
		"localhost/content.ui":        "<content><text id=\"page\">[{{ site }}] [{{ shout('Go') }}]</text></content>",
		"localhost/notfound.ui":       "<content><text id=\"missing\">Không có trang này</text></content>",
		"localhost/plain/view.ui":     plainView,
		"localhost/plain/content.ui":  "<content><text id=\"plain\">Trang tĩnh</text></content>",
		"localhost/titled/view.ui":    plainView,
		"localhost/titled/content.ui": "<content><text>{{ title }}</text></content>",
	}
	never := map[string]string{"secret": "file-const", "hidden": "handler-local", "tagline": "made with Go", "nothing": ""}
	for name := range never {
		files["localhost/"+name+"/router.work"] = route
		files["localhost/"+name+"/content.ui"] = "<content><text id=\"page\">[{{ site }}] [{{ " + name + " }}]</text></content>"
	}
	tenant := buildUISite(t, files)
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
	if text := textOf(t, body, "page"); text != "[Kitwork] [Go!]" {
		t.Errorf("/ page = %q, want the object's site and its function", text)
	}

	// a name the object does not hand: the error at the read, the names given, never the value
	for name, value := range never {
		content := files["localhost/"+name+"/content.ui"]
		want := fmt.Sprintf("%s/content.ui:1:%d: router.get không trả về %s: trang đọc %s mà object của context.view({ … }) trong %s/router.work không có; tên có: links, shout, site",
			name, strings.Index(content, "{{ "+name)+1, name, name, name)
		rec := serveUI(t, tenant, "http://localhost/"+name)
		if got := rec.Body.String(); rec.Code != http.StatusInternalServerError || !strings.Contains(got, want) || value != "" && strings.Contains(got, value) {
			t.Errorf("/%s = %d, want 500 saying %q:\n%.1500s", name, rec.Code, want, got)
		}
	}

	// notfound.ui is given nothing: the frame reading site fails where it reads it; a branch whose frame reads
	// nothing draws it
	missing := serveUI(t, tenant, "http://localhost/khong-co")
	want := fmt.Sprintf("header.ui:1:%d: trang không tìm thấy không được đưa tên nào, nên site không đọc ra gì", strings.Index(header, "{{ site")+1)
	if got := missing.Body.String(); missing.Code != http.StatusInternalServerError || !strings.Contains(got, want) {
		t.Errorf("/khong-co = %d, want 500 saying %q:\n%.1500s", missing.Code, want, got)
	}
	if missing := serveUI(t, tenant, "http://localhost/plain/khong-co"); missing.Code != http.StatusNotFound || textOf(t, missing.Body.String(), "missing") != "Không có trang này" {
		t.Errorf("/plain/khong-co = %d, want notfound.ui in the branch's frame:\n%.1500s", missing.Code, missing.Body.String())
	}

	// a folder with no router.work: its page is given nothing
	if plain := serveUI(t, tenant, "http://localhost/plain"); plain.Code != http.StatusOK || textOf(t, plain.Body.String(), "plain") != "Trang tĩnh" {
		t.Errorf("/plain = %d, want its page:\n%.1500s", plain.Code, plain.Body.String())
	}
	titled := serveUI(t, tenant, "http://localhost/titled")
	want = "titled/content.ui:1:16: router.get không trả về title: thư mục /titled không có router.work, nên không có router.get và trang không được đưa tên nào"
	if got := titled.Body.String(); titled.Code != http.StatusInternalServerError || !strings.Contains(got, want) {
		t.Errorf("/titled = %d, want 500 saying %q:\n%.1500s", titled.Code, want, got)
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
// parameter is the context whatever its name ((c), (content), (request)) or taken apart; any other
// parameter is null. A template site's router.kitwork.js keeps its way, by name: (request, response) are
// the request and the response. (The hooks of a failure, whose second parameter was what failed, are gone
// from router.work since 24/10/2026: TestRouterWorkFailureHooksAreGone.)
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
// response)) is the request's failure or null now. Each is told at the function's arrow, as every error of a
// handler's parameters is, and names the spelling to write. CONTROLS: the spellings it names boot, a
// handler's (context, failure) and (e) among them, and a template site's router.kitwork.js keeps reading them
// by name, its failure hooks' (err, ctx) too. (The rows of router.work's failure hooks, (err, ctx) => … and
// router.error((e) => …), left on 24/10/2026 with the hooks: TestRouterWorkFailureHooksAreGone.)
//
// CONTROL (23/10/2026, go test -overlay): with positionalParameterError refusing nothing, every refused site
// booted ("(request, response) => …: run = <nil>, want router.work:2:32: tham số `response` đứng sau tham số
// đầu …").
func TestRouterWorkParameterReadByNameIsABootError(t *testing.T) {
	const header = "import * as router from \"kitwork/router\";\n"
	const after = "đứng sau tham số đầu nên không phải "
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

// The failure hooks of router.work are gone (KITWORK_ROUTER.md mục 3, 24/10/2026): .error(), .success(),
// .finally() (and their old names .then(), .catch()) chained after a door, and router.error(…) of the branch,
// are compile errors at the word, naming what a failure does now: the nearest error.ui draws it, and logging
// or cleaning up is written in the handler. Whatever the hook's parameters were named. A template site's
// router.kitwork.js keeps its hooks (TestRouterWorkParameterReadByNameIsABootError's last row).
//
// CONTROL (24/10/2026, go test -overlay with RouteRuleRemoved and RouterRemovedWord knowing none of these words):
// .error, .success, .finally, .then and .catch became the nearest-word error (".error không nối sau
// router.get(…): sau cửa chỉ nối …"), and router.error the unknown-word one: "router.error không có; router
// có get, …", not the removal.
func TestRouterWorkFailureHooksAreGone(t *testing.T) {
	const header = "import * as router from \"kitwork/router\";\n"
	const route = "lỗi của route vẽ error.ui gần nhất; ghi log hay dọn dẹp viết trong handler"
	for _, c := range []struct{ name, source, want string }{
		{".error((err, ctx) => …)", header + "router.get((context) => {\n  fail(\"hỏng\");\n}).error((err, ctx) => ctx.status(503).json({ code: err.code }));\n",
			"router.work:4:4: .error(…) sau router.get(…) đã bỏ (24/10/2026): " + route},
		{".error((context, error) => …)", header + "router.get((context) => context.text(\"x\")).error((context, error) => context.status(503).json({ code: error.code }));\n",
			"router.work:2:44: .error(…) sau router.get(…) đã bỏ (24/10/2026): " + route},
		{".success", header + "router.post((context) => context.text(\"x\")).guard(() => true).success((context) => context.text(\"y\"));\n",
			"router.work:2:63: .success(…) sau router.post(…) đã bỏ (24/10/2026): " + route},
		{".finally", header + "router.get((context) => context.text(\"x\")).finally(() => null);\n",
			"router.work:2:44: .finally(…) sau router.get(…) đã bỏ (24/10/2026): " + route},
		{".then", header + "router.get((context) => context.text(\"x\")).then(() => null);\n",
			"router.work:2:44: .then(…) sau router.get(…) đã bỏ (24/10/2026): " + route},
		{".catch", header + "router.proxy(\"https://example.com/a.png\").catch(() => null);\n",
			"router.work:2:43: .catch(…) sau router.proxy(…) đã bỏ (24/10/2026): " + route},
		{"router.error((e) => …)", header + "router.error((e) => ({ seen: e.code }));\nrouter.get((context) => context.text(\"x\"));\n",
			"router.work:2:8: router.error đã bỏ (24/10/2026): " + route},
		{"router.error((context, err) => …)", header + "router.error((context, err) => context.status(503).json({ code: err.code }));\n",
			"router.work:2:8: router.error đã bỏ (24/10/2026): " + route},
	} {
		_, err := bootUISite(t, map[string]string{"localhost/router.work": c.source}, true)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: run = %v, want %s", c.name, err, c.want)
		}
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
