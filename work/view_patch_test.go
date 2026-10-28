package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// A job of a page that runs in the kernel answers the data it changed (docs/KITWORK_BRIDGE.md): a name by
// value, a list the page draws by key by row. The todo page of demo.kitwork.io, its list in storage.
func patchTodoSite(t *testing.T) *Tenant {
	t.Helper()
	tn := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"view.ui":     "<view><content /></view>",
		"todo/content.ui": `<content><form submit="add()"><input value="{{ title }}" /></form><list>` +
			`<item for="todo of todos; key: todo.id"><input type="checkbox" checked="{{ todo.done }}" change="toggle(todo.id, !todo.done)" />` +
			`<text>{{ todo.title }}</text><button click="remove(todo.id)">x</button></item></list>` +
			`<text>{{ left }} left</text><button click="leave()">out</button></content>`,
		"todo/router.work": `import * as router from "kitwork/router";
import { datastore } from "kitwork/storage";

const list = datastore("todo").segment("list");

router.view((context) => {
  var title = "";
  const todos = list.get() ?? [];
  const left = todos.filter((todo) => !todo.done).length;
  const add = () => {
    list.set(todos.concat([{ id: todos.length + 1, title: title, done: false }]));
    title = "";
  };
  const toggle = (id, done) => {
    list.set(todos.map((todo) => todo.id === id ? { id: todo.id, title: todo.title, done: done } : todo));
  };
  const remove = (id) => {
    list.set(todos.filter((todo) => todo.id !== id));
  };
  const leave = () => context.redirect("/login");
  return context.bind({ title, todos, left, add, toggle, remove, leave });
});
`,
	}), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

func callAnswer(t *testing.T, tn *Tenant, job, body string) (map[string]any, string) {
	t.Helper()
	r := postCall(tn, "/todo", job, body)
	if r.Code != http.StatusOK || !strings.HasPrefix(r.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%s: %d %s %.600s", job, r.Code, r.Header().Get("Content-Type"), r.Body.String())
	}
	var answer map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &answer); err != nil {
		t.Fatalf("%s answered no JSON: %v %.300s", job, err, r.Body.String())
	}
	if _, has := answer["html"]; has {
		t.Fatalf("%s answered the page: %.300s", job, r.Body.String())
	}
	return answer, r.Body.String()
}

func TestJobAnswersTheDataItChanged(t *testing.T) {
	tn := patchTodoSite(t)
	if r := serveGet(tn, "/todo", nil); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "data-kitwork-page") {
		t.Fatalf("/todo: %d %.1500s", r.Code, r.Body.String())
	}
	for i, title := range []string{"buy milk", "call mum", "send letter"} {
		callAnswer(t, tn, "add", `{"args":[],"vars":{"title":"`+title+`"},"seq":`+string(rune('1'+i))+`}`)
	}

	// tick one box: the row that changed and the count, nothing else, well under 200 bytes
	answer, body := callAnswer(t, tn, "toggle", `{"args":[2,true],"vars":{"title":""},"seq":4}`)
	if len(body) >= 200 {
		t.Errorf("a ticked box answered %d bytes, want under 200: %s", len(body), body)
	}
	if answer["seq"] != float64(4) {
		t.Errorf("seq %v, want 4", answer["seq"])
	}
	if patch, _ := answer["patch"].(map[string]any); len(patch) != 1 || patch["left"] != float64(2) {
		t.Errorf("patch %v, want only left 2: %s", answer["patch"], body)
	}
	todos, _ := answer["lists"].(map[string]any)["todos"].(map[string]any)
	if todos["key"] != "id" {
		t.Fatalf("the list todos should change by row, by id: %s", body)
	}
	set, _ := todos["set"].([]any)
	if row, _ := set[0].(map[string]any); len(set) != 1 || row["id"] != float64(2) || row["done"] != true {
		t.Errorf("set %v, want the one row 2, done: %s", todos["set"], body)
	}
	if _, has := todos["remove"]; has {
		t.Errorf("nothing was removed: %s", body)
	}

	// remove a row: its key, nothing else of the list
	answer, body = callAnswer(t, tn, "remove", `{"args":[1],"vars":{"title":""},"seq":5}`)
	todos, _ = answer["lists"].(map[string]any)["todos"].(map[string]any)
	if removed, _ := todos["remove"].([]any); len(removed) != 1 || removed[0] != float64(1) {
		t.Errorf("remove 1: %s", body)
	}
	if _, has := todos["set"]; has {
		t.Errorf("remove 1 set rows: %s", body)
	}
	if _, has := todos["order"]; has {
		t.Errorf("remove 1 kept the order of the rest: %s", body)
	}

	// a job that sends the page away answers where to, nothing run again
	answer, body = callAnswer(t, tn, "leave", `{"args":[],"vars":{},"seq":6}`)
	if answer["redirect"] != "/login" || answer["seq"] != float64(6) || answer["patch"] != nil {
		t.Errorf("leave: %s", body)
	}
}

// What changed, by value and by row, the row form only when it is shorter.
func TestViewPatch(t *testing.T) {
	row := func(id float64, done bool) map[string]any {
		return map[string]any{"id": id, "title": "a fairly long title for row", "done": done}
	}
	before := map[string]any{"left": float64(2), "title": "x", "todos": []any{row(1, false), row(2, false), row(3, true)}, "gone": true}
	after := map[string]any{"left": float64(1), "title": "x", "todos": []any{row(3, true), row(2, true), row(4, false)}}
	patch, lists := viewPatch(before, after, map[string]string{"todos": "id"})
	if len(patch) != 2 || patch["left"] != float64(1) || patch["gone"] != nil {
		t.Errorf("patch %v", patch)
	}
	if _, has := patch["gone"]; !has {
		t.Errorf("a name the page lost should be null: %v", patch)
	}
	todos, _ := lists["todos"].(map[string]any)
	b, _ := json.Marshal(todos)
	if string(b) != `{"key":"id","order":[3,2,4],"remove":[1],"set":[{"done":true,"id":2,"title":"a fairly long title for row"},{"done":false,"id":4,"title":"a fairly long title for row"}]}` {
		t.Errorf("todos by row: %s", b)
	}
	// rows with no key, or one key twice, change whole
	if _, lists := viewPatch(map[string]any{"l": []any{row(1, false), row(1, true)}}, map[string]any{"l": []any{row(1, true)}}, map[string]string{"l": "id"}); len(lists) != 0 {
		t.Errorf("a list with a key twice should change whole: %v", lists)
	}
	// a short list is sent whole when that is shorter
	if patch, lists := viewPatch(map[string]any{"l": []any{}}, map[string]any{"l": []any{map[string]any{"id": float64(1)}}}, map[string]string{"l": "id"}); len(lists) != 0 || patch["l"] == nil {
		t.Errorf("a list shorter whole should be sent whole: %v %v", patch, lists)
	}
}

// Two tabs of one page (28/10/2026): tab B was drawn, then tab A added rows. A call of tab B is computed on
// the rows the server has, which tab B never saw; a patch for those rows would leave tab B half new (the
// count of three rows over the one it shows). Its revisions say so: it gets the list whole, and every tab
// gets the new revision of what changed. A var has no revision.
func TestStalePageGetsWholeValues(t *testing.T) {
	tn := patchTodoSite(t)
	page := serveGet(tn, "/todo", nil).Body.String()
	m := regexp.MustCompile(`data-kitwork-page>(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no data block:\n%.2000s", page)
	}
	var block struct {
		Revs map[string]string `json:"revs"`
	}
	if err := json.Unmarshal([]byte(m[1]), &block); err != nil {
		t.Fatal(err)
	}
	if block.Revs["todos"] == "" || block.Revs["left"] == "" {
		t.Fatalf("the page should carry the revision of each name of its data: %v", block.Revs)
	}
	if _, has := block.Revs["title"]; has {
		t.Errorf("a var has no revision: %v", block.Revs)
	}
	copyRevs := func(r map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range r {
			out[k] = v
		}
		return out
	}
	tabA, tabB := copyRevs(block.Revs), copyRevs(block.Revs)
	call := func(revs map[string]string, job, args string) (map[string]any, string) {
		held, _ := json.Marshal(revs)
		answer, body := callAnswer(t, tn, job, `{"args":`+args+`,"vars":{"title":"row"},"seq":1,"revs":`+string(held)+`}`)
		if got, ok := answer["revs"].(map[string]any); ok {
			for k, v := range got {
				revs[k], _ = v.(string)
			}
		}
		return answer, body
	}
	for i := 0; i < 3; i++ {
		call(tabA, "add", "[]")
	}

	// tab A holds what the server holds: a tick is its row
	answer, body := call(tabA, "toggle", "[1,true]")
	if lists, _ := answer["lists"].(map[string]any); lists["todos"] == nil {
		t.Errorf("a tab that holds the server's rows gets its tick by row: %s", body)
	}
	if patch, _ := answer["patch"].(map[string]any); patch["todos"] != nil {
		t.Errorf("a tab that holds the server's rows does not get them whole: %s", body)
	}

	// tab B was drawn before the rows: its tick gets the list whole, and the count with it
	answer, body = call(tabB, "toggle", "[2,true]")
	patch, _ := answer["patch"].(map[string]any)
	rows, _ := patch["todos"].([]any)
	if len(rows) != 3 {
		t.Fatalf("a tab drawn before the rows should get them whole (3), got %s", body)
	}
	if lists, _ := answer["lists"].(map[string]any); lists["todos"] != nil {
		t.Errorf("a whole list is not also sent by row: %s", body)
	}
	if patch["left"] != float64(1) {
		t.Errorf("the count should come with the rows (1 left): %s", body)
	}
	if tabB["todos"] == block.Revs["todos"] {
		t.Errorf("tab B should hold the new revision of todos now: %v", tabB)
	}

	// tab A, one call behind tab B, gets the list whole too; a call that changes nothing sends nothing
	answer, body = call(tabA, "toggle", "[3,true]")
	if patch, _ := answer["patch"].(map[string]any); patch["todos"] == nil {
		t.Errorf("tab A, behind tab B's tick, should get the rows whole: %s", body)
	}
	answer, body = call(tabA, "toggle", "[3,true]")
	if answer["patch"] != nil || answer["lists"] != nil {
		t.Errorf("a call that changes nothing a tab does not hold sends nothing: %s", body)
	}

	// a row tab A removed, ticked in tab B (a ghost row there): the job finds nothing, and tab B, behind, gets
	// the rows whole, so the ghost leaves
	call(tabA, "remove", "[1]")
	answer, body = call(tabB, "toggle", "[1,false]")
	patch, _ = answer["patch"].(map[string]any)
	rows, _ = patch["todos"].([]any)
	if len(rows) != 2 {
		t.Fatalf("a tick on a row removed elsewhere should bring the rows whole (2), got %s", body)
	}
	for _, row := range rows {
		if row.(map[string]any)["id"] == float64(1) {
			t.Errorf("the removed row should not come back: %s", body)
		}
	}
}

// A tab seen again asks, with no job, for what changed since its revisions (?kitwork-refresh=1, 28/10/2026): the
// names whose revision is not its own come whole with their new revisions, a var never; a tab that holds the
// data as it is gets nothing.
func TestRefreshAnswersWhatChangedSinceTheRevisions(t *testing.T) {
	tn := patchTodoSite(t)
	page := serveGet(tn, "/todo", nil).Body.String()
	m := regexp.MustCompile(`data-kitwork-page>(.*?)</script>`).FindStringSubmatch(page)
	var block struct {
		Revs map[string]string `json:"revs"`
	}
	if m == nil || json.Unmarshal([]byte(m[1]), &block) != nil || block.Revs["todos"] == "" {
		t.Fatalf("no revisions in the page:\n%.2000s", page)
	}
	old, _ := json.Marshal(block.Revs)
	for i := 0; i < 2; i++ { // another tab adds two rows
		callAnswer(t, tn, "add", `{"args":[],"vars":{"title":"row"},"seq":1}`)
	}
	refresh := func(revs []byte) (map[string]any, string) {
		req := httptest.NewRequest(http.MethodPost, "http://localhost/todo?"+ViewRefreshParameter+"=1", strings.NewReader(`{"seq":3,"revs":`+string(revs)+`}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		var answer map[string]any
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil {
			t.Fatalf("refresh: %d %q", rec.Code, rec.Body.String())
		}
		return answer, rec.Body.String()
	}
	answer, body := refresh(old)
	patch, _ := answer["patch"].(map[string]any)
	rows, _ := patch["todos"].([]any)
	if len(rows) != 2 || patch["left"] != float64(2) {
		t.Fatalf("a tab drawn before two rows were added should get them whole, and the count: %s", body)
	}
	if _, has := patch["title"]; has {
		t.Errorf("a var is the tab's own, never sent back: %s", body)
	}
	revs, _ := answer["revs"].(map[string]any)
	if revs["todos"] == "" || revs["todos"] == block.Revs["todos"] {
		t.Errorf("the new revision of todos should come with it: %s", body)
	}
	held := map[string]string{}
	for k, v := range block.Revs {
		held[k] = v
	}
	for k, v := range revs {
		held[k], _ = v.(string)
	}
	now, _ := json.Marshal(held)
	if answer, body = refresh(now); answer["patch"] != nil || answer["revs"] != nil {
		t.Errorf("a tab that holds the data as it is should get nothing: %s", body)
	}
}
