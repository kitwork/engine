package work

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kitwork/engine/database/statement"
	"github.com/kitwork/engine/value"
)

// matchesPosts is a managed database with one table, posts: title and body .searchable(), published a boolean.
func matchesPosts(t *testing.T) *ManagedDatabase {
	t.Helper()
	shop := newManagedApp(t, map[string]string{"shop.example/router.work": `import * as router from "kitwork/router";`})
	tenant := shop.site("shop.example")
	posts := goStruct(t,
		goColumn{name: "id", builder: "id"},
		goColumn{name: "title", builder: "text", modifiers: []string{"searchable"}},
		goColumn{name: "body", builder: "text", modifiers: []string{"searchable"}},
		goColumn{name: "published", builder: "boolean"},
	)
	return goManaged(t, tenant, map[string]value.Value{"posts": posts})
}

func matchesInsert(t *testing.T, database *ManagedDatabase, title, body string, published bool) {
	t.Helper()
	hole := func(index int) *statement.Node { return statement.HoleNode(index) }
	insert := statement.Encode(&statement.Statement{Version: statement.Version, Action: statement.ActionInsert, Table: "posts",
		Values: []statement.Field{{Column: "title", Value: hole(0)}, {Column: "body", Value: hole(1)}, {Column: "published", Value: hole(2)}},
		Holes:  []statement.Hole{{Site: "router.work:1:1"}, {Site: "router.work:1:2"}, {Site: "router.work:1:3"}}})
	if created := database.OnInvoke(statement.Method, value.New(insert), value.New(title), value.New(body), value.New(published)); created.Failed() {
		t.Fatalf("insert %s: %v", title, created)
	}
}

// matchesNode is table.matches(hole), or table.column.matches(hole).
func matchesNode(column string, hole int) *statement.Node {
	return &statement.Node{Op: statement.OpMatches, Column: column, Args: []*statement.Node{statement.HoleNode(hole)}}
}

func matchesAnd(left, right *statement.Node) *statement.Node {
	return &statement.Node{Op: statement.OpAnd, Args: []*statement.Node{left, right}}
}

// matchesTitles is the titles of a list, in order.
func matchesTitles(t *testing.T, result value.Value) string {
	t.Helper()
	if result.Failed() || result.K != value.Array {
		t.Fatalf("list: %v", result)
	}
	names := make([]string, 0, len(result.Array()))
	for _, row := range result.Array() {
		names = append(names, row.Map()["title"].String())
	}
	return strings.Join(names, "|")
}

// matches on the app's SQLite database (KITWORK_DATABASE.md mục 2): rows come by score with score and snippet, the
// rest of the condition filters them, !matches only filters, one column narrows it, count and first answer from it,
// empty text matches nothing; a row the app just added is found at the next statement; with orderBy every matching
// row is ordered; a value that is not a string is refused where it is written.
//
// CONTROL: with run not reading matches, "list: … điều kiện" (SQL cannot run it); with the index not folded before a
// read (catchUp returning at once), "after an insert: banh xeo = """; with decorate leaving the rows in the order SQL
// gave them, "cà phê = "Trà sữa trân châu|Cà phê sữa đá|Cà phê muối"".
func TestManagedDatabaseMatches(t *testing.T) {
	database := matchesPosts(t)
	matchesInsert(t, database, "Trà sữa trân châu", "Uống kèm cà phê thì lạ", true)
	matchesInsert(t, database, "Cà phê sữa đá", "Pha phin buổi sáng, cà phê đậm", true)
	matchesInsert(t, database, "Cà phê muối", "Món mới ở Huế", false)
	matchesInsert(t, database, "Bánh mì", "Nóng giòn", true)

	run := func(s *statement.Statement, holes ...value.Value) value.Value {
		s.Version = statement.Version
		s.Table = "posts"
		for range holes {
			s.Holes = append(s.Holes, statement.Hole{Site: fmt.Sprintf("router.work:9:%d", len(s.Holes)+1)})
		}
		return database.OnInvoke(statement.Method, append([]value.Value{value.New(statement.Encode(s))}, holes...)...)
	}
	list := func(where *statement.Node, holes ...value.Value) string {
		return matchesTitles(t, run(&statement.Statement{Action: statement.ActionList, Where: where}, holes...))
	}
	published := &statement.Node{Op: statement.OpEqual, Args: []*statement.Node{{Op: statement.OpColumn, Column: "published"}, statement.Literal(true)}}
	notOnTitle := &statement.Node{Op: statement.OpNot, Args: []*statement.Node{matchesNode("title", 1)}}

	ranked := run(&statement.Statement{Action: statement.ActionList, Where: matchesNode("", 0)}, value.New("ca phe"))
	if got := matchesTitles(t, ranked); got != "Cà phê sữa đá|Cà phê muối|Trà sữa trân châu" {
		t.Fatalf("cà phê = %q, want the two titles first, by score", got)
	}
	best := ranked.Array()[0].Map()
	if best["score"].K != value.Number || best["score"].N <= 0 || !strings.Contains(strings.ToLower(best["snippet"].String()), "<b>cà</b> <b>phê</b>") {
		t.Fatalf("a row of matches: score %v, snippet %q", best["score"], best["snippet"].String())
	}
	for _, check := range []struct {
		name  string
		where *statement.Node
		holes []value.Value
		want  string
	}{
		{"and published", matchesAnd(matchesNode("", 0), published), []value.Value{value.New("ca phe")}, "Cà phê sữa đá|Trà sữa trân châu"},
		{"not on the title", matchesAnd(matchesNode("", 0), notOnTitle), []value.Value{value.New("ca phe"), value.New("muoi")}, "Cà phê sữa đá|Trà sữa trân châu"},
		{"on the body", matchesNode("body", 0), []value.Value{value.New("phin")}, "Cà phê sữa đá"},
		{"every word", matchesNode("", 0), []value.Value{value.New("ca phe hue")}, "Cà phê muối"},
		{"empty text", matchesNode("", 0), []value.Value{value.New("  ")}, ""},
	} {
		if got := list(check.where, check.holes...); got != check.want {
			t.Errorf("%s = %q, want %q", check.name, got, check.want)
		}
	}
	if counted := run(&statement.Statement{Action: statement.ActionCount, Where: matchesNode("", 0)}, value.New("ca phe")); counted.N != 3 {
		t.Errorf("count of cà phê = %v, want 3", counted)
	}
	if counted := run(&statement.Statement{Action: statement.ActionCount, Where: matchesAnd(matchesNode("", 0), published)}, value.New("ca phe")); counted.N != 2 {
		t.Errorf("count of cà phê published = %v, want 2", counted)
	}
	if first := run(&statement.Statement{Action: statement.ActionFirst, Where: matchesNode("", 0)}, value.New("banh mi")); first.K != value.Map || first.Map()["title"].String() != "Bánh mì" {
		t.Errorf("first of bánh mì = %v", first)
	}
	if none := run(&statement.Statement{Action: statement.ActionFirst, Where: matchesNode("", 0)}, value.New("pho")); !none.IsNil() {
		t.Errorf("first of phở = %v, want null", none)
	}

	// What the app writes is found at the next statement.
	matchesInsert(t, database, "Bánh xèo miền Tây", "Giòn rụm", true)
	if got := list(matchesNode("", 0), value.New("banh xeo")); got != "Bánh xèo miền Tây" {
		t.Fatalf("after an insert: banh xeo = %q", got)
	}

	// With orderBy every matching row is ordered by it; a page is a page of what matches.
	ordered := run(&statement.Statement{Action: statement.ActionList, Where: matchesNode("", 0), Order: []statement.Key{{Column: "title"}}}, value.New("ca phe"))
	if got := matchesTitles(t, ordered); got != "Cà phê muối|Cà phê sữa đá|Trà sữa trân châu" {
		t.Errorf("cà phê ordered by title = %q", got)
	}
	paged := run(&statement.Statement{Action: statement.ActionList, Where: matchesNode("", 0), Page: statement.Literal(2), Limit: statement.Literal(1)}, value.New("ca phe"))
	if got := matchesTitles(t, paged); got != "Cà phê muối" {
		t.Errorf("cà phê, page 2 of 1 = %q, want the second by score", got)
	}

	if code, message := failureOf(run(&statement.Statement{Action: statement.ActionList, Where: matchesNode("", 0)}, value.New(5))); code != value.InvalidArgumentCode ||
		message != "matches ở router.work:9:1 nhận một chuỗi" {
		t.Errorf("matches(5): %s %q", code, message)
	}
}

// matches written in an app's router.work: compiled into the statement, run on the app's database, the rows come
// back as JSON with score and snippet, and a post added through the app is found by the next request.
//
// CONTROL: with the compiler not reading matches, the route fails to compile ("không dịch được `posts.matches(q)`").
func TestMatchesFromAnAppsRoute(t *testing.T) {
	schema := `import { table, id, text, boolean } from "kitwork/schema";
import { connection } from "kitwork/database";

const posts = table({ id: id(), title: text().searchable(), body: text().searchable(), published: boolean() });

export const blog = connection().tables({ posts });
`
	route := `import * as router from "kitwork/router";
import { blog } from "app/.shared/schema/blog.work";

router.post((context) => {
  const title = context.request.form("title");
  const body = context.request.form("body");
  return context.json(blog.insert((posts) => ({ title, body, published: true })));
});

router.get((context) => {
  const q = context.request.query("q");
  return context.json(blog.list((posts) => posts.matches(q) && posts.published === true));
});
`
	tenant := newManagedApp(t, map[string]string{".shared/schema/blog.work": schema, "shop.example/router.work": route}).site("shop.example")
	for _, post := range [][2]string{{"Cà phê sữa đá", "Pha phin"}, {"Bánh mì", "Nóng giòn"}} {
		if status, body := managedPost(tenant, "/", url.Values{"title": {post[0]}, "body": {post[1]}}); status != http.StatusOK {
			t.Fatalf("insert %s = %d %s", post[0], status, body)
		}
	}
	search := func(q string) []map[string]any {
		t.Helper()
		status, body := managedGet(tenant, "/?q="+url.QueryEscape(q))
		var rows []map[string]any
		if status != http.StatusOK || json.Unmarshal([]byte(body), &rows) != nil {
			t.Fatalf("GET /?q=%s = %d %s", q, status, body)
		}
		return rows
	}
	rows := search("ca phe")
	if len(rows) != 1 || rows[0]["title"] != "Cà phê sữa đá" || rows[0]["snippet"] != "<b>Cà</b> <b>phê</b> sữa đá" {
		t.Fatalf("q=ca phe = %v", rows)
	}
	if score, ok := rows[0]["score"].(float64); !ok || score <= 0 {
		t.Fatalf("score = %v", rows[0]["score"])
	}
	if status, body := managedPost(tenant, "/", url.Values{"title": {"Phở bò"}, "body": {"Nước dùng trong"}}); status != http.StatusOK {
		t.Fatalf("insert phở = %d %s", status, body)
	}
	if rows := search("pho bo"); len(rows) != 1 || rows[0]["title"] != "Phở bò" {
		t.Fatalf("q=pho bo after the post was added = %v", rows)
	}
}
