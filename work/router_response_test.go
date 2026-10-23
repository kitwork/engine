package work

import (
	"net/http"
	"testing"

	"github.com/kitwork/engine/value"
)

// Every answer and every edit returns the response itself (19/10/2026), so `return response.json(data)`
// is the answer and the edits chain before it (20/10/2026: before; the Go methods also give the error
// of router.work's order).
func TestResponseAnswersReturnTheResponse(t *testing.T) {
	r := &Response{}
	data := value.New("x")
	two := func(got *Response, err error) *Response {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		return got
	}
	answers := map[string]*Response{
		"json":       two(r.JSON(data)),
		"text":       two(r.Text(data)),
		"html":       two(r.HTML(data)),
		"css":        r.CSS(data),
		"svg":        two(r.SVG(data)),
		"file":       two(r.File(value.Value{K: value.Proxy, V: &fileHandle{}})),
		"file bytes": two(r.File(value.New([]byte("x")))),
		"redirect":   two(r.Redirect(value.New("/x"))),
		"view":       two(r.View(data)),
		"render":     r.Render(data),
		"send":       r.Send(data),
		"string":     r.String("x"),
		"bytes":      r.Bytes([]byte("x")),
		"image":      two(r.Image(value.New("/og.png"))),
		"title":      two(r.Title(data)),
		"meta":       two(r.Meta(value.New(map[string]value.Value{"type": value.New("article")}))),
		"error":      two(r.Error(data)),
		"notFound":   two(r.NotFound()),
		"status":     two(r.Status(201)),
		"type":       two(r.Type("text/csv")),
		"header":     two(r.Header("X-A", "1")),
		"headers":    two(r.Headers(value.New(map[string]value.Value{"X-B": value.New("2")}))),
		"cookie":     two(r.Cookie("seen", value.New("1"))),
	}
	for name, got := range answers {
		if got != r {
			t.Errorf("response.%s returned %p, want the response %p", name, got, r)
		}
	}
}

// A route takes its context apart in the parameter, ({ request, response }) => …, answers with
// `return response.…`, and goes on after the answer; context.status(201).json(data) is the same
// response as response.status(201).json(data).
func TestDestructuredHandlerAnswersWithResponse(t *testing.T) {
	tmp := writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import * as router from "kitwork/router";`,
		"split/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ request, response }) => {\n" +
			"  const order = { id: request.query(\"id\") };\n" +
			"  return response.status(201).json(order).header(\"X-Order\", order.id).cookie(\"seen\", \"1\");\n" +
			"});\n",
		"whole/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get((context) => context.status(201).json({ id: context.query(\"id\") }));\n",
		"closure/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ request, response }) => {\n" +
			"  const answer = (word) => response.text(word + \" \" + request.query(\"id\"));\n" +
			"  return answer(\"order\");\n" +
			"});\n",
		"renamed/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response: res }) => res.text(\"renamed\"));\n",
		"prepared/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.text(\"inside\")).guard(({ response }) => response.header(\"X-Seen\", \"1\"));\n",
		"guarded/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.text(\"inside\")).guard(({ request, response }) => request.query(\"pass\") == \"1\" ? true : response.status(403).text(\"blocked\"));\n",
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	split := serveGet(tn, "/split?id=7", nil)
	whole := serveGet(tn, "/whole?id=7", nil)
	if split.Code != http.StatusCreated || split.Body.String() != whole.Body.String() || whole.Code != http.StatusCreated {
		t.Fatalf("split %d %q, whole %d %q: want the same 201 answer", split.Code, split.Body.String(), whole.Code, whole.Body.String())
	}
	if split.Body.String() != `{"id":"7"}` {
		t.Fatalf("split body %q", split.Body.String())
	}
	if split.Header().Get("X-Order") != "7" {
		t.Fatalf("header after json: %q", split.Header().Get("X-Order"))
	}
	if cookies := split.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != "seen" || cookies[0].Value != "1" {
		t.Fatalf("cookie after json: %#v", cookies)
	}
	if r := serveGet(tn, "/closure?id=9", nil); r.Code != http.StatusOK || r.Body.String() != "order 9" {
		t.Fatalf("closure: %d %q", r.Code, r.Body.String())
	}
	if r := serveGet(tn, "/renamed", nil); r.Body.String() != "renamed" {
		t.Fatalf("renamed: %d %q", r.Code, r.Body.String())
	}
	// a guard that only prepares the response returns it: that is not data to answer with
	if r := serveGet(tn, "/prepared", nil); r.Code != http.StatusOK || r.Body.String() != "inside" || r.Header().Get("X-Seen") != "1" {
		t.Fatalf("guard preparing: %d %q seen=%q", r.Code, r.Body.String(), r.Header().Get("X-Seen"))
	}
	if r := serveGet(tn, "/guarded", nil); r.Code != http.StatusForbidden || r.Body.String() != "blocked" {
		t.Fatalf("guard refusing: %d %q", r.Code, r.Body.String())
	}
	if r := serveGet(tn, "/guarded?pass=1", nil); r.Code != http.StatusOK || r.Body.String() != "inside" {
		t.Fatalf("guard passing: %d %q", r.Code, r.Body.String())
	}
}
