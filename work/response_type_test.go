package work

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// A template site (router.kitwork.js) keeps ctx.type() and .type() on the response, in the three shapes
// the sites use, with no warning: .type() stays (20/10/2026).
func TestTemplateSiteKeepsType(t *testing.T) {
	tmp := writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"feed.xml/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => {\n  const xml = \"<rss/>\";\n  return ctx\n    .type(\"application/rss+xml; charset=utf-8\")\n    .send(xml);\n});\n",
		"sitemap.xml/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => {\n  const xml = \"<urlset/>\";\n  return ctx\n    .type(\"application/xml; charset=utf-8\")\n    .send(xml);\n});\n",
		"qr/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => {\n  const svg = \"<svg/>\";\n  if (svg) {\n    ctx.type(\"image/svg+xml\");\n    return ctx.text(svg);\n  }\n  return ctx.json({ svg: null });\n});\n",
		"csv/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"const csv = \"text/csv\";\n" +
			"router.get((ctx) => ctx.response.type(csv).send(\"a,b\"));\n",
	})
	var tn *Tenant
	output := captureBootOutput(t, func() {
		tn = NewTenant(tmp, "localhost")
		if err := tn.Run(); err != nil {
			t.Fatalf("run: %v", err)
		}
		for path, want := range map[string]string{
			"/feed.xml":    "application/rss+xml; charset=utf-8",
			"/sitemap.xml": "application/xml; charset=utf-8",
			"/qr":          "image/svg+xml",
			"/csv":         "text/csv",
		} {
			r := serveGet(tn, path, nil)
			if r.Code != http.StatusOK || r.Header().Get("Content-Type") != want {
				t.Errorf("%s: %d type=%q %q, want %q", path, r.Code, r.Header().Get("Content-Type"), r.Body.String(), want)
			}
		}
	})
	if strings.Contains(output, "type()") {
		t.Errorf("a warning about .type() is printed:\n%s", output)
	}
}

// In router.work .type() is an edit before the answer: the answer's own media type applies only when
// none came before; .file() takes the one set, else application/octet-stream; against json or view it is
// an error, as after the answer.
func TestUISiteTypeIsAnEdit(t *testing.T) {
	routes := map[string]string{
		"csv":       `response.type("text/csv").text("a,b")`,
		"svg":       `response.type("image/svg+xml; charset=utf-8").text("<svg/>")`,
		"plain":     `response.text("a,b")`,
		"png":       `response.type("image/png").file(file.open("kitwork.png").bytes())`,
		"bytes":     `response.file(file.open("kitwork.png").bytes())`,
		"onjson":    `response.type("text/csv").json({ a: 1 })`,
		"after":     `response.text("a,b").type("text/csv")`,
		"context":   `context.type("text/csv").text("a,b")`,
		"article":   `context.meta({ type: "article" }).title("Bài").view({ note: 1 })`,
		"choiceold": `response.text("a,b", { type: "text/csv" })`,
	}
	files := map[string]string{"router.work": `import * as router from "kitwork/router";`, ".database/files/kitwork.png": "\x89PNG\r\n\x1a\n"}
	for name, answer := range routes {
		files[name+"/router.work"] = "import * as router from \"kitwork/router\";\nimport * as file from \"kitwork/file\";\n" +
			"router.get((context) => { const response = context.response; return " + answer + "; });\n"
	}
	tn := NewTenant(writeSite(t, files), "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for path, want := range map[string]string{
		"/csv":       "text/csv",
		"/svg":       "image/svg+xml; charset=utf-8",
		"/plain":     "text/plain; charset=utf-8",
		"/png":       "image/png",
		"/bytes":     "application/octet-stream",
		"/context":   "text/csv",
		"/choiceold": "text/plain; charset=utf-8", // there is no { type } of an answer
	} {
		if r := serveGet(tn, path, nil); r.Code != http.StatusOK || r.Header().Get("Content-Type") != want {
			t.Errorf("%s: %d type=%q %.60q, want %q", path, r.Code, r.Header().Get("Content-Type"), r.Body.String(), want)
		}
	}
	for _, path := range []string{"/onjson", "/after"} {
		if r := serveGet(tn, path, nil); r.Code != http.StatusInternalServerError {
			t.Errorf("%s: %d %q, want the request to fail", path, r.Code, r.Body.String())
		}
	}
}

// A program does not see the response's data, kind and code: reading one gives what a key the
// response does not have gives. The engine still reads them in Go.
func TestResponseHidesItsGettersFromAProgram(t *testing.T) {
	tmp := writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => {\n" +
			"  response.status(202).text(\"body\");\n" +
			"  const seen = [typeof response.data, typeof response.kind, typeof response.code, typeof response.Data, response.data == null, typeof response.nothing];\n" +
			"  return response.json(seen);\n" +
			"});\n",
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	r := serveGet(tn, "/", nil)
	missing := `"undefined"`
	var want strings.Builder
	want.WriteString("[")
	for i := 0; i < 4; i++ {
		want.WriteString(missing + ",")
	}
	want.WriteString("true," + missing + "]")
	if r.Body.String() != want.String() {
		t.Fatalf("/: %d %q, want %q", r.Code, r.Body.String(), want.String())
	}
	response, _ := (&Response{}).Text(value.New("x"))
	if response.Data().Text() != "x" || response.Kind() != "text" {
		t.Fatalf("the Go getters changed: %q %q", response.Data().Text(), response.Kind())
	}
}
