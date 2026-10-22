package work

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// viewSite is a template site whose shell prints the page's head.
func viewSite(t *testing.T, routes map[string]string) *Tenant {
	t.Helper()
	files := map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import * as router from "kitwork/router";`,
		"index.kitwork.html": `<!doctype html><head><title>{{ $.meta.title }}</title>` +
			`<meta name="description" content="{{ $.meta.description }}"><meta property="og:image" content="{{ $.meta.image }}">` +
			`<meta data-type="{{ $.meta.type }}"></head><body>{{ @page }}</body>`,
	}
	for path, source := range routes {
		files[path+"/page.kitwork.html"] = `<main>note {{ note }}</main>`
		files[path+"/router.kitwork.js"] = "import * as router from \"kitwork/router\";\n" + source + "\n"
	}
	tn := NewTenant(writeSite(t, files), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return tn
}

func wantPage(t *testing.T, label string, body string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(body, part) {
			t.Errorf("%s: body lacks %q\n%s", label, part, body)
		}
	}
}

// context.view(…) returns the request's response, as response.view(…) does (19/10/2026). A template site
// keeps its title after the view (KITWORK_ROUTER §6: it keeps every way it has); the cache is the
// route's (20/10/2026: .cache() on the answer is gone).
func TestContextViewReturnsTheResponse(t *testing.T) {
	tn := viewSite(t, map[string]string{
		"menu": `let runs = 0;
router.get((context) => {
  runs = runs + 1;
  return context.view({ note: "n" + runs }).title("Quán").description("Cà phê").image("/og.png").meta({ type: "article" }).header("X-View", "1");
}).cache("1h");`,
		"split": `router.get(({ response }) => response.view({ note: "split" }).title("Tách").header("X-View", "2"));`,
		"same":  `router.get((context) => (context.view() == context.response ? context.view({ note: "same" }) : context.text("another")));`,
	})

	first := serveGet(tn, "/menu", nil)
	if first.Code != http.StatusOK || first.Header().Get("X-View") != "1" {
		t.Fatalf("/menu: %d X-View=%q %q", first.Code, first.Header().Get("X-View"), first.Body.String())
	}
	wantPage(t, "/menu", first.Body.String(), "<title>Quán</title>", `content="Cà phê"`, `content="/og.png"`, `data-type="article"`, "<main>note n1</main>")
	if second := serveGet(tn, "/menu", nil); second.Body.String() != first.Body.String() || second.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf("/menu again: cache=%q %q", second.Header().Get("X-Kitwork-Cache"), second.Body.String())
	}

	// a title written on response.view(…) reaches the page, and the header stays with it
	split := serveGet(tn, "/split", nil)
	if split.Code != http.StatusOK || split.Header().Get("X-View") != "2" {
		t.Fatalf("/split: %d X-View=%q %q", split.Code, split.Header().Get("X-View"), split.Body.String())
	}
	wantPage(t, "/split", split.Body.String(), "<title>Tách</title>", "<main>note split</main>")

	wantPage(t, "/same", serveGet(tn, "/same", nil).Body.String(), "<main>note same</main>")
}

// The template pages written the old ways still render: a view not returned, a title set on the
// context, ctx.bind(…).title(…).meta(…).
func TestTemplateViewOldWaysStillRender(t *testing.T) {
	tn := viewSite(t, map[string]string{
		"statement": `router.get((ctx) => { ctx.view({ note: "statement" }).title("Câu lệnh"); });`,
		"context":   `router.get((ctx) => { ctx.title("Từ ctx"); return ctx.view({ note: "context" }); });`,
		"bind":      `router.get((ctx) => ctx.bind({ note: "bind" }).title("Bind").meta({ type: "article" }));`,
		"plain":     `router.get((ctx) => ctx.view({ note: "plain" }));`,
	})
	wantPage(t, "/statement", serveGet(tn, "/statement", nil).Body.String(), "<title>Câu lệnh</title>", "<main>note statement</main>")
	wantPage(t, "/context", serveGet(tn, "/context", nil).Body.String(), "<title>Từ ctx</title>", "<main>note context</main>")
	wantPage(t, "/bind", serveGet(tn, "/bind", nil).Body.String(), "<title>Bind</title>", `data-type="article"`, "<main>note bind</main>")
	wantPage(t, "/plain", serveGet(tn, "/plain", nil).Body.String(), "<main>note plain</main>")
}

// The media type is the edit .type() before the answer (20/10/2026: { type } of an answer is gone):
// context.type("image/png").file(bytes) for bytes the handler made; response.image(bytes) is gone.
func TestAnswerMediaTypeIsTheTypeEdit(t *testing.T) {
	tmp := writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import * as router from "kitwork/router";`,
		"qr/router.kitwork.js": "import * as router from \"kitwork/router\";\nimport { qrcode } from \"kitwork\";\n" +
			"router.get(({ response }) => response.header(\"X-Qr\", \"1\").type(\"image/png\").file(qrcode.data(\"kitwork\").png()));\n",
		"shortcut/router.kitwork.js": "import * as router from \"kitwork/router\";\nimport { qrcode } from \"kitwork\";\n" +
			"router.get((context) => context.type(\"image/png\").file(qrcode.data(\"kitwork\").png()));\n",
		"csv/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.status(201).type(\"text/csv; charset=utf-8\").text(\"a,b\"));\n",
		"send/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.type(\"application/xml\").send(\"<x/>\"));\n",
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, path := range []string{"/qr", "/shortcut"} {
		r := serveGet(tn, path, nil)
		if r.Code != http.StatusOK || r.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(r.Body.String(), "\x89PNG") {
			t.Fatalf("%s: %d type=%q body=%.8q", path, r.Code, r.Header().Get("Content-Type"), r.Body.String())
		}
	}
	if r := serveGet(tn, "/csv", nil); r.Code != http.StatusCreated || r.Header().Get("Content-Type") != "text/csv; charset=utf-8" || r.Body.String() != "a,b" {
		t.Fatalf("/csv: %d type=%q %q", r.Code, r.Header().Get("Content-Type"), r.Body.String())
	}
	if r := serveGet(tn, "/send", nil); r.Header().Get("Content-Type") != "application/xml" || r.Body.String() != "<x/>" {
		t.Fatalf("/send: %d type=%q %q", r.Code, r.Header().Get("Content-Type"), r.Body.String())
	}
	// .image() on a response is the head image, not an answer
	r := &Response{}
	if r.Image(value.New("/og.png")); r.IsSend() {
		t.Fatalf("response.image answered: kind=%q", r.Kind())
	}
}
