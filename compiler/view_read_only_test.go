package compiler

import (
	"slices"
	"strings"
	"testing"
)

// viewOf is the function of the router.view a source writes, parsed.
func viewOf(t *testing.T, source string) (*Program, *FunctionLiteral) {
	t.Helper()
	prog, err := parseProgramSource(source, "router.work")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range prog.Statements {
		if line, ok := statement.(*ExpressionStatement); ok {
			if fn := routerViewFunction(line.Expression); fn != nil {
				return prog, fn
			}
		}
	}
	t.Fatal("no router.view in the source")
	return nil, nil
}

// A job is read only (29/10/2026) when its source changes nothing but the page's vars and its own names, and
// calls nothing but a method of a value its caller sent: the ones that touch data, call a function, or call a
// method of a var or of what the view read are not.
//
// CONTROL (by hand, 29/10/2026): with readOnlyCheck letting any call through, help came out read only (red).
func TestReadOnlyJobsAreThoseThatChangeOnlyTheVars(t *testing.T) {
	_, fn := viewOf(t, `
import * as router from "kitwork/router";
import { datastore } from "kitwork/storage";
const list = datastore("todo").segment("list");
router.view((context) => {
  var query = "";
  var page = 1;
  var filter = { done: false };
  const todos = list.get() ?? [];
  const search = (text) => { query = text.trim().toLowerCase(); page = 1; };
  const more = () => { page = page + 1; };
  const pick = (done) => { filter.done = done; };
  const refine = (words) => { const first = words[0]; if (first !== "") { query = first.slice(0, 20); } };
  const each = (items) => { for (const item of items) { query = query + item.trim(); } };
  const add = () => { list.set(todos.concat([{ title: query }])); query = ""; };
  const tidy = () => { query = query.trim(); };
  const help = () => { search("x"); };
  const keep = () => { todos[0].done = true; };
  const leave = () => { context.redirect("/"); };
  const sort = (rows) => { query = rows.map((row) => row.title).join(" "); };
  return context.bind({ query, todos, search, more, pick, refine, each, add, tidy, help, keep, leave, sort, clear: () => { query = ""; } });
});
`)
	got := readOnlyJobs(fn)
	want := []string{"search", "more", "pick", "refine", "each", "clear"}
	if !slices.Equal(got, want) {
		t.Fatalf("read only: got %v, want %v", got, want)
	}
}

// A name bound to a job that writes in another return, a const shadowed in a branch, and a return that is not
// a bind written out make the job not read only: what a call runs is not known from the name alone.
//
// CONTROL (by hand, 29/10/2026): with a name declared twice resolved all the same, and with the last bind of a
// name deciding it, search came out read only (red).
func TestReadOnlyJobsReadTheWholeFunction(t *testing.T) {
	cases := map[string]string{
		"another return binds a writer": `
  if (context.query("all")) {
    return context.bind({ query, search: () => { list.set([]); } });
  }
  return context.bind({ query, search });`,
		"a branch shadows the const": `
  if (context.query("all")) {
    const search = () => { list.set([]); };
    return context.bind({ query, search });
  }
  return context.bind({ query, search });`,
		"a return not written out": `
  const bound = { query, search };
  return context.bind(bound);`,
	}
	for name, tail := range cases {
		t.Run(name, func(t *testing.T) {
			_, fn := viewOf(t, `
import * as router from "kitwork/router";
import { datastore } from "kitwork/storage";
const list = datastore("todo").segment("list");
router.view((context) => {
  var query = "";
  const search = (text) => { query = text; };`+tail+`
});
`)
			if got := readOnlyJobs(fn); len(got) != 0 {
				t.Fatalf("no job should be read only, got %v", got)
			}
		})
	}
}

// The compiler hands router.view its read-only jobs as a second argument, { __readOnly: [ … ] }, the rules
// chained after it or not; a view with none keeps its one argument.
func TestRouterViewIsGivenItsReadOnlyJobs(t *testing.T) {
	prog, _ := viewOf(t, `
import * as router from "kitwork/router";
router.view((context) => {
  var query = "";
  const search = (text) => { query = text; };
  return context.bind({ query, search });
}).limit(10, "1m");
`)
	rewriteRouterViews(prog)
	text := prog.String()
	if !strings.Contains(text, `"__readOnly":["search"]`) && !strings.Contains(text, `__readOnly:[search]`) {
		t.Fatalf("router.view should be given its read-only jobs:\n%s", text)
	}

	prog, _ = viewOf(t, `
import * as router from "kitwork/router";
router.view((context) => context.bind({ add: () => { context.redirect("/"); } }));
`)
	rewriteRouterViews(prog)
	if text := prog.String(); strings.Contains(text, "__readOnly") {
		t.Fatalf("a view with no read-only job should keep its one argument:\n%s", text)
	}
}
