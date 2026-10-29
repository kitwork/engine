package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// viewBindSite is a router.work site whose page is a router.view answering context.bind({ … })
// (28/10/2026, KITWORK_ROUTER_VIEW.md).
func viewBindSite(t *testing.T) *Tenant {
	t.Helper()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"view.ui":     "<view><content /></view>",
		"todo/content.ui": `<content><text id="title">[{{ title }}]</text><list><item for="todo of todos; key: todo.id">` +
			`<text>{{ todo.title }}</text></item></list><text id="left">{{ left }} left</text></content>`,
		"todo/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.view((context) => {\n" +
			"  var title = \"\";\n" +
			"  const todos = [{ id: 1, title: \"buy milk\", done: false }, { id: 2, title: \"call mum\", done: true }];\n" +
			"  const left = todos.filter((todo) => !todo.done).length;\n" +
			"  const add = () => { title = \"\"; };\n" +
			"  return context.title(\"Todo\").bind({ title, todos, left, add });\n" +
			"});\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

// router.view draws the folder's .ui page with the names of the object its function binds, the page's text
// in its head.
func TestRouterViewBindDrawsThePage(t *testing.T) {
	tn := viewBindSite(t)
	r := serveGet(tn, "/todo", nil)
	body := r.Body.String()
	if r.Code != http.StatusOK {
		t.Fatalf("/todo: %d\n%.800s", r.Code, body)
	}
	for _, part := range []string{"<title>Todo</title>", "[]", "buy milk", "call mum", "1 left"} {
		if !strings.Contains(body, part) {
			t.Errorf("/todo lacks %q\n%.1200s", part, body)
		}
	}
}

// postCall calls a job of a router.view page through the bridge, as the browser does.
func postCall(tn *Tenant, path, job, body string) *httptest.ResponseRecorder {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	req := httptest.NewRequest(http.MethodPost, "http://localhost"+path+sep+ViewCallParameter+"="+job, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	return rec
}

// A job runs on what the view's function bound with the vars the page sent; the answer is what changed: the
// var the job set, nothing of the data it did not touch, never the page.
func TestRouterViewJobRunsThroughTheBridge(t *testing.T) {
	tn := viewBindSite(t)
	r := postCall(tn, "/todo", "add", `{"args":[],"vars":{"title":"buy eggs"},"seq":3}`)
	if r.Code != http.StatusOK {
		t.Fatalf("add: %d %.600s", r.Code, r.Body.String())
	}
	var answer map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &answer); err != nil {
		t.Fatalf("add answered no JSON: %v %.300s", err, r.Body.String())
	}
	if answer["seq"] != float64(3) {
		t.Errorf("add: seq %v, want the call's 3", answer["seq"])
	}
	if patch, _ := answer["patch"].(map[string]any); len(patch) != 1 || patch["title"] != "" {
		t.Errorf("add: patch %v, want only title \"\"", answer["patch"])
	}
	if _, has := answer["html"]; has {
		t.Errorf("add answered the page: %.300s", r.Body.String())
	}
	if _, has := answer["lists"]; has {
		t.Errorf("add changed no list: %.300s", r.Body.String())
	}
}

// A name the page did not bind as a job is 404; a job that fails answers its message with its code.
func TestRouterViewJobErrors(t *testing.T) {
	tn := viewBindSite(t)
	if r := postCall(tn, "/todo", "nothing", `{}`); r.Code != http.StatusNotFound || !strings.Contains(r.Body.String(), "không có việc nothing") {
		t.Errorf("nothing: %d %s", r.Code, r.Body.String())
	}
	if r := postCall(tn, "/todo", "todos", `{}`); r.Code != http.StatusNotFound {
		t.Errorf("todos (data, not a job): %d %s", r.Code, r.Body.String())
	}
	if r := postCall(tn, "/todo", "add", `not json`); r.Code != http.StatusBadRequest {
		t.Errorf("bad body: %d %s", r.Code, r.Body.String())
	}
}

// The page of a router.view that binds jobs runs in the browser on its program (docs/KITWORK_BRIDGE.md,
// ui/page_program.go): its elements carry marks, its list is drawn as the program adopts it, its data closes
// its content and names its program, and it loads the kernel, call.js and its program, page.<hash>.js.
func TestRouterViewPageWiresItsJobs(t *testing.T) {
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"view.ui":     "<view><content /></view>",
		"todo/content.ui": `<content><form submit="add()"><input value="{{ title }}" /><button type="submit">Thêm</button></form>` +
			`<list><item for="todo of todos; key: todo.id"><button click="remove(todo.id)">{{ todo.title }}</button></item></list></content>`,
		"todo/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.view((context) => {\n" +
			"  var title = \"\";\n" +
			"  const todos = [{ id: 1, title: \"buy milk\" }, { id: 7, title: \"call mum\" }];\n" +
			"  const add = () => { title = \"\"; };\n" +
			"  const remove = (id) => { };\n" +
			"  return context.bind({ title, todos, add, remove });\n" +
			"});\n",
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	r := serveGet(tn, "/todo", nil)
	body := r.Body.String()
	if r.Code != http.StatusOK {
		t.Fatalf("/todo: %d\n%.1500s", r.Code, body)
	}
	for _, part := range []string{
		`data-kitwork-mark="`, `data-kitwork-template="0"`, `<!--kitwork-for-start id=s1-->`, `data-kitwork-item="s1" data-kitwork-key="7"`,
		`<script type="application/json" data-kitwork-page>`, `"program":"`, `/_kitwork/kernel.`, `/_kitwork/call.`,
	} {
		if !strings.Contains(body, part) {
			t.Errorf("/todo lacks %q\n%.3000s", part, body)
		}
	}
	// the page's expressions are in its program; what still carries the kernel's attributes is the engine's
	// own progressbar and reader
	for _, expression := range []string{"add()", "remove(todo.id)", "todo.title", "value: title"} {
		if strings.Contains(body, expression) {
			t.Errorf("a page with a program carries no expression on its elements, found %q\n%.3000s", expression, body)
		}
	}
	src := regexp.MustCompile(`src="?(/_kitwork/page\.[0-9a-f]{16}\.js)`).FindStringSubmatch(body)
	if src == nil {
		t.Fatalf("/todo should load its program, page.<hash>.js\n%.3000s", body)
	}
	program := serveGet(tn, src[1], nil)
	if program.Code != http.StatusOK || !strings.Contains(program.Body.String(), "k.pages") ||
		!strings.Contains(program.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("%s: %d %s %.300s", src[1], program.Code, program.Header().Get("Cache-Control"), program.Body.String())
	}
}
