package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	req := httptest.NewRequest(http.MethodPost, "http://localhost"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ViewCallHeader, job)
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	return rec
}

// A job runs on what the view's function bound with the vars the page sent; the answer is the page drawn
// again and the vars the job changed.
func TestRouterViewJobRunsThroughTheBridge(t *testing.T) {
	tn := viewBindSite(t)
	r := postCall(tn, "/todo", "add", `{"args":[],"vars":{"title":"buy eggs"}}`)
	if r.Code != http.StatusOK {
		t.Fatalf("add: %d %.600s", r.Code, r.Body.String())
	}
	var answer struct {
		HTML string         `json:"html"`
		Vars map[string]any `json:"vars"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &answer); err != nil {
		t.Fatalf("add answered no JSON: %v %.300s", err, r.Body.String())
	}
	if title, ok := answer.Vars["title"]; !ok || title != "" {
		t.Errorf("add: vars %v, want title \"\"", answer.Vars)
	}
	if !strings.Contains(answer.HTML, "[]") || !strings.Contains(answer.HTML, "buy milk") {
		t.Errorf("add: page %.600s", answer.HTML)
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

// The page of a router.view writes each event that calls one of its jobs as the bridge reads it, the
// arguments read on the server where the element is drawn, marks the field of a var, and loads call.js.
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
		`data-kitwork-job-submit="add"`, `data-kitwork-args-submit="[]"`,
		`data-kitwork-job-click="remove"`, `data-kitwork-args-click="[1]"`, `data-kitwork-args-click="[7]"`,
		`data-kitwork-var="title"`, `/_kitwork/call.`,
	} {
		if !strings.Contains(body, part) {
			t.Errorf("/todo lacks %q\n%.3000s", part, body)
		}
	}
}
