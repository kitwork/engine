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
		callAnswer(t, tn, "add", callBody{Seq: float64(1 + i), Args: []any{}, Page: vars("title", title)}.String())
	}

	// tick one box: under the page, the row that changed and the count, nothing else, well under 200 bytes; the
	// page holds tickets for the keys it reads, made lately, so none come again (view_changes.go)
	tickets := pageTickets(t, serveGet(tn, "/todo", nil).Body.String())
	answer, body := callAnswer(t, tn, "toggle", callBody{Seq: 4, Args: []any{2, true}, Page: vars("title", ""), Marks: ticketMarks(tickets)}.String())
	if len(body) >= 200 {
		t.Errorf("a ticked box answered %d bytes, want under 200: %s", len(body), body)
	}
	if answer["seq"] != float64(4) {
		t.Errorf("seq %v, want 4", answer["seq"])
	}
	page := instanceOf(answer, pagePlace)
	if len(page) != 2 || page["left"] != float64(2) {
		t.Errorf("the page should change left and the one row: %s", body)
	}
	if row, _ := page["todos#2"].(map[string]any); row["id"] != float64(2) || row["done"] != true {
		t.Errorf("the list todos should change by row, todos#2 now done: %s", body)
	}

	// remove a row: its key, null, nothing else of the list
	answer, body = callAnswer(t, tn, "remove", callBody{Seq: 5, Args: []any{1}, Page: vars("title", "")}.String())
	rows := rowsIn(instanceOf(answer, pagePlace), "todos")
	if gone, has := rows["1"]; len(rows) != 1 || !has || gone != nil {
		t.Errorf("remove 1 should bring todos#1 null, nothing else of the list: %s", body)
	}

	// a job that sends the page away answers where to, nothing run again
	answer, body = callAnswer(t, tn, "leave", callBody{Seq: 6, Args: []any{}, Page: vars()}.String())
	if answer["redirect"] != "/login" || answer["seq"] != float64(6) || answer[pagePlace] != nil {
		t.Errorf("leave: %s", body)
	}
}

// What changed in one instance, by value and by row, name#key, the rows only when they are shorter and the rows
// that stay kept their order.
func TestViewPatch(t *testing.T) {
	row := func(id float64, done bool) map[string]any {
		return map[string]any{"id": id, "title": "a fairly long title for row", "done": done}
	}
	keys := map[string]string{"todos": "id"}
	before := map[string]any{"left": float64(2), "title": "x", "todos": []any{row(1, false), row(2, false), row(3, true)}, "gone": true}
	after := map[string]any{"left": float64(1), "title": "x", "todos": []any{row(2, true), row(3, true), row(4, false)}}
	changes := viewPatch(before, after, keys)
	b, _ := json.Marshal(changes)
	if string(b) != `{"gone":null,"left":1,"todos#1":null,"todos#2":{"done":true,"id":2,"title":"a fairly long title for row"},"todos#4":{"done":false,"id":4,"title":"a fairly long title for row"}}` {
		t.Errorf("changes: %s", b)
	}
	// rows that stay in another order: the list whole
	if changes := viewPatch(before, map[string]any{"todos": []any{row(3, true), row(2, false), row(1, false)}}, keys); changes["todos"] == nil || len(rowsIn(changes, "todos")) != 0 {
		t.Errorf("a list sorted again should change whole: %v", changes)
	}
	// rows with no key, or one key twice, change whole
	if changes := viewPatch(map[string]any{"l": []any{row(1, false), row(1, true)}}, map[string]any{"l": []any{row(1, true)}}, map[string]string{"l": "id"}); changes["l"] == nil || len(rowsIn(changes, "l")) != 0 {
		t.Errorf("a list with a key twice should change whole: %v", changes)
	}
	// a short list is sent whole when that is shorter
	if changes := viewPatch(map[string]any{"l": []any{}}, map[string]any{"l": []any{map[string]any{"id": float64(1)}}}, map[string]string{"l": "id"}); changes["l"] == nil || len(rowsIn(changes, "l")) != 0 {
		t.Errorf("a list shorter whole should be sent whole: %v", changes)
	}
}

// Two tabs of one page (28/10/2026): tab B was drawn, then tab A added rows. A call of tab B is computed on
// the rows the server has, which tab B never saw; a patch for those rows would leave tab B half new (the
// count of three rows over the one it shows). Its revisions say so: it gets what it missed, by row since its
// revision when that version is kept (view_rows.go, 28/10/2026), whole when it is not, and every tab gets the
// new revision of what changed. A var has no revision.
func TestStalePageGetsWhatItMissed(t *testing.T) {
	tn := patchTodoSite(t)
	page := serveGet(tn, "/todo", nil).Body.String()
	m := regexp.MustCompile(`data-kitwork-page>(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no data block:\n%.2000s", page)
	}
	var block struct {
		Bridge struct {
			Revs map[string]map[string]string `json:"revs"`
		} `json:"bridge"`
	}
	if err := json.Unmarshal([]byte(m[1]), &block); err != nil {
		t.Fatal(err)
	}
	drawn := block.Bridge.Revs[pagePlace]
	if drawn["todos"] == "" || drawn["left"] == "" {
		t.Fatalf("the page should carry the revision of each name of its data: %v", drawn)
	}
	if _, has := drawn["title"]; has {
		t.Errorf("a var has no revision: %v", drawn)
	}
	copyRevs := func(r map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range r {
			out[k] = v
		}
		return out
	}
	tabA, tabB := copyRevs(drawn), copyRevs(drawn)
	call := func(revs map[string]string, job, args string) (map[string]any, string) {
		var list []any
		json.Unmarshal([]byte(args), &list)
		answer, body := callAnswer(t, tn, job, callBody{Seq: 1, Args: list, Page: vars("title", "row"), Revs: map[string]map[string]string{pagePlace: revs}}.String())
		for k, v := range revsOf(answer, pagePlace) {
			revs[k], _ = v.(string)
		}
		return answer, body
	}
	for i := 0; i < 3; i++ {
		call(tabA, "add", "[]")
	}

	// tab A holds what the server holds: a tick is its row
	answer, body := call(tabA, "toggle", "[1,true]")
	if len(rowsIn(instanceOf(answer, pagePlace), "todos")) == 0 {
		t.Errorf("a tab that holds the server's rows gets its tick by row: %s", body)
	}
	if instanceOf(answer, pagePlace)["todos"] != nil {
		t.Errorf("a tab that holds the server's rows does not get them whole: %s", body)
	}

	// tab B was drawn before the rows: its tick gets the list whole, and the count with it
	answer, body = call(tabB, "toggle", "[2,true]")
	mine := instanceOf(answer, pagePlace)
	rows, _ := mine["todos"].([]any)
	if len(rows) != 3 {
		t.Fatalf("a tab drawn before the rows should get them whole (3), got %s", body)
	}
	if len(rowsIn(mine, "todos")) != 0 {
		t.Errorf("a whole list is not also sent by row: %s", body)
	}
	if mine["left"] != float64(1) {
		t.Errorf("the count should come with the rows (1 left): %s", body)
	}
	if tabB["todos"] == drawn["todos"] {
		t.Errorf("tab B should hold the new revision of todos now: %v", tabB)
	}

	// tab A, one call behind tab B, gets what it missed by row: its version is kept; a call that changes
	// nothing sends nothing
	answer, body = call(tabA, "toggle", "[3,true]")
	if len(rowsIn(instanceOf(answer, pagePlace), "todos")) == 0 {
		t.Errorf("tab A, behind tab B's tick, should get the rows it missed by row: %s", body)
	}
	if instanceOf(answer, pagePlace)["todos"] != nil {
		t.Errorf("tab A, holding a kept version, should not get the rows whole: %s", body)
	}
	answer, body = call(tabA, "toggle", "[3,true]")
	if answer[pagePlace] != nil {
		t.Errorf("a call that changes nothing a tab does not hold sends nothing: %s", body)
	}

	// a row tab A removed, ticked in tab B (a ghost row there): the job finds nothing, and tab B, behind, gets
	// the removal by row, so the ghost leaves
	call(tabA, "remove", "[1]")
	answer, body = call(tabB, "toggle", "[1,false]")
	if gone, has := rowsIn(instanceOf(answer, pagePlace), "todos")["1"]; !has || gone != nil {
		t.Fatalf("a tick on a row removed elsewhere should bring todos#1 null, so the ghost leaves: %s", body)
	}

	// a tab holding a revision no longer kept gets the list whole
	stale := copyRevs(tabB)
	stale["todos"] = "not-kept"
	answer, body = call(stale, "toggle", "[2,false]")
	rows, _ = instanceOf(answer, pagePlace)["todos"].([]any)
	if len(rows) != 2 {
		t.Fatalf("a tab holding a revision not kept should get the rows whole (2), got %s", body)
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
		Bridge struct {
			Revs map[string]map[string]string `json:"revs"`
		} `json:"bridge"`
	}
	if m == nil || json.Unmarshal([]byte(m[1]), &block) != nil || block.Bridge.Revs[pagePlace]["todos"] == "" {
		t.Fatalf("no revisions in the page:\n%.2000s", page)
	}
	drawn := block.Bridge.Revs[pagePlace]
	for i := 0; i < 2; i++ { // another tab adds two rows
		callAnswer(t, tn, "add", callBody{Seq: 1, Args: []any{}, Page: vars("title", "row")}.String())
	}
	refresh := func(revs map[string]string) (map[string]any, string) {
		body := callBody{Seq: 3, Revs: map[string]map[string]string{pagePlace: revs}}.String()
		req := httptest.NewRequest(http.MethodPost, "http://localhost/todo?"+ViewRefreshParameter+"=1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		var answer map[string]any
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil {
			t.Fatalf("refresh: %d %q", rec.Code, rec.Body.String())
		}
		return answer, rec.Body.String()
	}
	answer, body := refresh(drawn)
	mine := instanceOf(answer, pagePlace)
	rows, _ := mine["todos"].([]any)
	if len(rows) != 2 || mine["left"] != float64(2) {
		t.Fatalf("a tab drawn before two rows were added should get them whole, and the count: %s", body)
	}
	if _, has := mine["title"]; has {
		t.Errorf("a var is the tab's own, never sent back: %s", body)
	}
	revs := revsOf(answer, pagePlace)
	if revs["todos"] == "" || revs["todos"] == drawn["todos"] {
		t.Errorf("the new revision of todos should come with it: %s", body)
	}
	held := map[string]string{}
	for k, v := range drawn {
		held[k] = v
	}
	for k, v := range revs {
		held[k], _ = v.(string)
	}
	if answer, body = refresh(held); answer[pagePlace] != nil || revsOf(answer, pagePlace) != nil {
		t.Errorf("a tab that holds the data as it is should get nothing: %s", body)
	}
}
