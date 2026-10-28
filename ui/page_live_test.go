package ui

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kitwork/engine/jit/hydrate"
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

	// the page has a program (page_program.go): marks, no expression on the elements
	for _, want := range []string{
		`<!--kit-for:start id=s1-->`, `data-k-for="0" data-kitwork-template`,
		`data-kitwork-item="s1" data-kitwork-key="45"`, `data-kitwork-item="s1" data-kitwork-key="46"`, `<!--kit-for:end-->`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the page should hold %q:\n%s", want, html)
		}
	}
	for _, gone := range []string{"data-kitwork-bind", "data-kitwork-event", "data-kitwork-class", "data-kitwork-for", "data-kitwork-job-", "data-kitwork-var"} {
		if strings.Contains(html, gone) {
			t.Errorf("a page with a program should carry no %s:\n%s", gone, html)
		}
	}
	// if follows its data like show: drawn hidden while false
	if !regexp.MustCompile(`<p[^>]*data-k="\d+"[^>]* hidden>Chưa có việc nào.`).MatchString(html) {
		t.Errorf("the if of an empty list should be drawn hidden while the list has rows:\n%s", html)
	}

	// the data the page repaints from, at the end of <content />, naming its program
	m := regexp.MustCompile(`<script type="application/json" data-kitwork-page>(.*?)</script>`).FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("the page should end its content with its data:\n%s", html)
	}
	var data struct {
		State   map[string]any `json:"state"`
		Jobs    []string       `json:"jobs"`
		Vars    []string       `json:"vars"`
		Program string         `json:"program"`
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

	// the program: registered under the id the page names, each mark with what it writes and reads
	script := string(page.Script())
	if data.Program == "" || !strings.Contains(script, `["`+data.Program+`"]=`) {
		t.Fatalf("the program should register under the page's id %q:\n%s", data.Program, script)
	}
	body := script[strings.Index(script, "]=")+2 : strings.Index(script, ";if(k.pageReady")]
	var program pageProgram
	if err := json.Unmarshal([]byte(body), &program); err != nil {
		t.Fatalf("the program is no JSON: %v\n%s", err, body)
	}
	for _, mark := range regexp.MustCompile(`data-k="(\d+)"`).FindAllStringSubmatch(html, -1) {
		if n, _ := strconv.Atoi(mark[1]); n >= len(program.Groups) {
			t.Errorf("mark %d has no group in the program", n)
		}
	}
	find := func(name, source string) (programGroup, programPair, bool) {
		ir, _ := hydrate.Compile(source)
		want, _ := json.Marshal(ir)
		for _, g := range program.Groups {
			for _, pairs := range [][]programPair{g.Binds, g.Classes, g.Events} {
				for _, p := range pairs {
					got, _ := json.Marshal(p.IR)
					if p.Name == name && string(got) == string(want) {
						return g, p, true
					}
				}
			}
		}
		return programGroup{}, programPair{}, false
	}
	for _, c := range []struct {
		name, source string
		list         int
		reads        string
	}{
		{"value", "title", -1, "title"},
		{"disabled", "add.pending", -1, "add"},
		{"submit.prevent", "add()", -1, "add"},
		{"show", "todos.length === 0", -1, "todos"},
		{"checked", "todo.done", 0, ""},
		{"disabled", "toggle.pending", 0, "toggle"},
		{"change", "toggle(todo.id, !todo.done)", 0, "toggle"},
		{"text", "todo.title", 0, ""},
		{"click", "remove(todo.id)", 0, "remove"},
	} {
		g, p, ok := find(c.name, c.source)
		if !ok {
			t.Errorf("the program should hold %s: %s", c.name, c.source)
			continue
		}
		if g.List != c.list || strings.Join(p.Reads, ",") != c.reads {
			t.Errorf("%s: %s is in list %d reading %v, want list %d reading %q", c.name, c.source, g.List, p.Reads, c.list, c.reads)
		}
	}
	if len(program.Lists) != 1 || program.Lists[0].Item != "todo" {
		t.Errorf("the program should hold the list todos: %+v", program.Lists)
	}
	// the template's row is no item of the list: the row's marks three times (template + 2 rows)
	if got := strings.Count(html, `type="checkbox" data-k=`); got != 3 {
		t.Errorf("expected the row's box three times (template + 2 rows), got %d", got)
	}
}

// A page that holds a component of Kitwork has no program yet: it is written for the kernel, as before.
func TestPageWithAComponentHasNoProgramYet(t *testing.T) {
	s := givenSite(map[string]string{"/site/todo/router.work": liveTodoRoute, "/site/todo/content.ui": `<content>` +
		`<dropdown><button click="toggle()">{{ left }}</button><popover><text>{{ title }}</text></popover></dropdown>` +
		`<form submit="add()"><input value="{{ title }}" /></form><button click="remove(1)">x</button></content>`})
	page, err := s.Compile(filepath.Join(s.Root, "todo"))
	if err != nil {
		t.Fatal(err)
	}
	if page.Script() != nil {
		t.Errorf("a page with a dropdown should have no program yet")
	}
	html := page.Render(Request{Path: "/todo", Scope: liveTodoScope()})
	if !strings.Contains(html, `data-kitwork-event="submit.prevent: add()"`) || strings.Contains(html, `data-k="`) {
		t.Errorf("a page with no program is written for the kernel:\n%s", html)
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
