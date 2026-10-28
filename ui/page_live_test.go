package ui

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The todo page of demo.kitwork.io, as its author wrote it (28/10/2026): router.view binds a var, data and
// three jobs, so the page runs in the kernel (page_live.go).
const liveTodoRoute = `import * as router from "kitwork/router";
import { datastore } from "kitwork/storage";

const list = datastore("todo").segment("list");

router.view((context) => {
  var title = "";
  const todos = list.get() ?? [];
  const left = todos.filter((todo) => !todo.done).length;

  const add = () => {
    list.set(todos.concat([{ id: todos.length + 1, title: title.trim(), done: false }]));
    title = "";
  };
  const toggle = (id, done) => {
    list.set(todos.map((todo) => todo.id === id ? { id: todo.id, title: todo.title, done: done } : todo));
  };
  const remove = (id) => {
    list.set(todos.filter((todo) => todo.id !== id));
  };

  return context.title("Việc cần làm").bind({ title, todos, left, add, toggle, remove });
});
`

const liveTodoContent = `<content gap="6" padding="4">
  <card gap="4" padding="5">
    <form gap="3" submit="add()">
      <box flex="row" gap="2">
        <input value="{{ title }}" placeholder="Việc cần làm…" />
        <button type="submit" disabled="{{ add.pending }}">Thêm</button>
      </box>
    </form>
    <text show="add.error" color="danger+20">{{ add.error.message }}</text>
    <text if="todos.length === 0" color="ink-30">Chưa có việc nào.</text>
    <list gap="2">
      <item for="todo of todos; key: todo.id" flex="row" align="center" gap="3">
        <input type="checkbox" checked="{{ todo.done }}" change="toggle(todo.id, !todo.done)" disabled="{{ toggle.pending }}" />
        <text color="{{ todo.done ? 'ink-30' : 'ink' }}">{{ todo.title }}</text>
        <button click="remove(todo.id)" disabled="{{ remove.pending }}">Xoá</button>
      </item>
    </list>
    <text size="small" color="ink-30">Còn {{ left }} việc chưa xong.</text>
  </card>
</content>
`

func liveTodoPage(t *testing.T, route string) *Page {
	t.Helper()
	s := givenSite(map[string]string{"/site/todo/router.work": route, "/site/todo/content.ui": liveTodoContent})
	page, err := s.Compile(filepath.Join(s.Root, "todo"))
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func liveTodoScope() map[string]any {
	return map[string]any{
		"title": "",
		"todos": []any{
			map[string]any{"id": float64(45), "title": "Mua sữa", "done": false},
			map[string]any{"id": float64(46), "title": "Gửi thư", "done": true},
		},
		"left": float64(1),
	}
}

func TestPageWithJobsRunsInTheKernel(t *testing.T) {
	page := liveTodoPage(t, liveTodoRoute)
	if !page.Live() {
		t.Fatal("a router.view that binds jobs and a var should run in the kernel")
	}
	if keys := page.ListKeys(); keys["todos"] != "id" || len(keys) != 1 {
		t.Fatalf("the list todos is drawn by todo.id: keys = %v", keys)
	}
	html := page.Render(Request{Path: "/todo", Scope: liveTodoScope()})

	for _, want := range []string{
		// the list as the kernel adopts it: markers, the template hidden, each row with the list's id and its key
		`<!--kit-for:start id=s1-->`,
		`data-kitwork-for="todo of todos; key: todo.id" data-kitwork-template`,
		`data-kitwork-item="s1" data-kitwork-key="45"`,
		`data-kitwork-item="s1" data-kitwork-key="46"`,
		`<!--kit-for:end-->`,
		// what reads the bind follows it in the browser
		`checked: todo.done`, `disabled: toggle.pending`, `text: todo.title`, `disabled: add.pending`,
		`show: add.error`, `text: add.error.message`, `show: todos.length === 0`,
		`change: toggle(todo.id, !todo.done)`, `click: remove(todo.id)`,
		// a form that calls a job stays on the page
		`submit.prevent: add()`,
		// the two-way field of the var
		`value: title`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the page should hold %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "data-kitwork-job-") || strings.Contains(html, "data-kitwork-var") {
		t.Errorf("the marks of the v0 bridge should be gone:\n%s", html)
	}
	// if follows its data like show: drawn hidden while false
	if !regexp.MustCompile(`<[a-z]+[^>]*data-kitwork-bind="show: todos.length === 0"[^>]* hidden>Chưa có việc nào.`).MatchString(html) {
		t.Errorf("the if of an empty list should be drawn hidden while the list has rows:\n%s", html)
	}
	// the template's row is no item of the list: two rows, three copies of the row (template + 2)
	if got := strings.Count(html, `change: toggle(todo.id, !todo.done)`); got != 3 {
		t.Errorf("expected the row three times (template + 2 rows), got %d", got)
	}

	// the data the kernel repaints from, at the end of <content />
	m := regexp.MustCompile(`<script type="application/json" data-kitwork-page>(.*?)</script>`).FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("the page should end its content with its data:\n%s", html)
	}
	var data struct {
		State map[string]any `json:"state"`
		Jobs  []string       `json:"jobs"`
		Vars  []string       `json:"vars"`
	}
	if err := json.Unmarshal([]byte(m[1]), &data); err != nil {
		t.Fatal(err)
	}
	if strings.Join(data.Jobs, ",") != "add,remove,toggle" || strings.Join(data.Vars, ",") != "title" {
		t.Errorf("jobs %v, vars %v", data.Jobs, data.Vars)
	}
	if rows, _ := data.State["todos"].([]any); len(rows) != 2 || data.State["left"] != float64(1) || data.State["title"] != "" {
		t.Errorf("state = %v", data.State)
	}
	if _, has := data.State["add"]; has {
		t.Errorf("a job is no data: %v", data.State)
	}
	if !strings.Contains(html, `</script></main>`) && !strings.Contains(html, `</script></div>`) {
		t.Errorf("the data should close the content:\n%s", html)
	}
}

// A page whose router.view binds no job and no var stays as the server prints it: no kernel attributes for
// what it reads, no data written twice.
func TestPageWithNoJobStaysPrinted(t *testing.T) {
	route := `import * as router from "kitwork/router";
router.view((context) => context.bind({ title: "", todos: [], left: 0, add: 0, toggle: 0, remove: 0 }));
`
	page := liveTodoPage(t, route)
	if page.Live() {
		t.Fatal("a router.view with no job and no var should not run in the kernel")
	}
	html := page.Render(Request{Path: "/todo", Scope: liveTodoScope()})
	for _, gone := range []string{"data-kitwork-page", "kit-for:start", "data-kitwork-template", "checked: todo.done"} {
		if strings.Contains(html, gone) {
			t.Errorf("a printed page should not hold %q:\n%s", gone, html)
		}
	}
}

// A list and an if of the page's data inside a component the kernel runs (a dropdown) run in the kernel like
// the page's own (they were a compile error there until 28/10/2026, where a printed page draws them); a list
// of the component's own state is still not live.
func TestPageListInsideAComponentRunsInTheKernel(t *testing.T) {
	compile := func(content string) (*Page, error) {
		s := givenSite(map[string]string{"/site/todo/router.work": liveTodoRoute, "/site/todo/content.ui": content})
		return s.Compile(filepath.Join(s.Root, "todo"))
	}
	page, err := compile(`<content><dropdown><button click="toggle()">{{ left }} việc</button><popover>` +
		`<text if="todos.length === 0">Trống</text>` +
		`<list><item for="todo of todos; key: todo.id"><text>{{ todo.title }}</text><button click="remove(todo.id)">x</button></item></list>` +
		`</popover></dropdown><form submit="add()"><input value="{{ title }}" /></form></content>`)
	if err != nil {
		t.Fatalf("a list of the page's data inside a dropdown should compile on a page that runs in the kernel: %v", err)
	}
	if keys := page.ListKeys(); keys["todos"] != "id" {
		t.Errorf("the list inside the dropdown is drawn by todo.id: keys = %v", keys)
	}
	html := page.Render(Request{Path: "/todo", Scope: liveTodoScope()})
	for _, want := range []string{
		`data-kitwork-component="dropdown"`, `<!--kit-for:start id=s1-->`, `data-kitwork-template`,
		`data-kitwork-item="s1" data-kitwork-key="45"`, `data-kitwork-item="s1" data-kitwork-key="46"`,
		`show: todos.length === 0`, `click: remove(todo.id)`, `text: todo.title`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the page should hold %q:\n%s", want, html)
		}
	}
	if _, err := compile(`<content><dropdown><button click="toggle()">x</button><popover><list>` +
		`<item for="o of open; key: o"><text>{{ o }}</text></item></list></popover></dropdown>` +
		`<form submit="add()"><input value="{{ title }}" /></form><text>{{ left }} {{ todos.length }}</text><button click="remove(1)">x</button></content>`); err == nil ||
		!strings.Contains(err.Error(), "for theo trạng thái của component") {
		t.Errorf("a list of the dropdown's own state should still be an error, got %v", err)
	}
}
