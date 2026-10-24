package work

import (
	"net/http"
	"strings"
	"testing"
)

// Reading a member never answers the request (19/10/2026): response.notFound, context.notFound and
// response.helloWorld read without a call are functions, and the handler answers as it would; calling
// notFound() answers 404. In a template site and in router.work alike.
func TestReadingAMemberDoesNotAnswer(t *testing.T) {
	read := "  const seen = [typeof response.notFound, typeof context.notFound, typeof response.helloWorld];\n"
	// helloWorld is the template site's; router.work does not show it (TestUIResponseShowsOnlyTheSpecNames)
	for _, site := range []struct{ name, file, read string }{
		{"template", "router.kitwork.js", `["function","function","function"]`},
		{"router.work", "router.work", `["function","function","undefined"]`},
	} {
		t.Run(site.name, func(t *testing.T) {
			files := map[string]string{
				site.file: "import * as router from \"kitwork/router\";\n",
				"read/" + site.file: "import * as router from \"kitwork/router\";\n" +
					"router.get((context) => {\n  const response = context.response;\n" + read +
					"  return response.json(seen);\n});\n",
				"called/" + site.file: "import * as router from \"kitwork/router\";\n" +
					"router.get(({ response }) => response.notFound());\n",
			}
			if site.file == "router.kitwork.js" {
				files["filesystem.kitwork"] = ""
			}
			tn := NewTenant(writeSite(t, files), "localhost")
			if err := tn.Run(); err != nil {
				t.Fatalf("run: %v", err)
			}
			if r := serveGet(tn, "/read", nil); r.Code != http.StatusOK || r.Body.String() != site.read {
				t.Fatalf("/read: %d %q", r.Code, r.Body.String())
			}
			if r := serveGet(tn, "/called", nil); r.Code != http.StatusNotFound {
				t.Fatalf("/called: %d %q, want 404", r.Code, r.Body.String())
			}
		})
	}
}

// In router.work the response shows a program only the names the spec lists (KITWORK_ROUTER §6: the
// edits, the answers, the head names the engine has); every other name reads as a key the response does
// not have — cache among them, the route's since 20/10/2026. A template site keeps every name it has.
// view is not probed here: in router.work it is only ever called with an object written in place, and
// typeof response.view, a read of it, is a compile error since 24/10/2026 (compiler/router_rules.go); every
// page test calls it.
func TestUIResponseShowsOnlyTheSpecNames(t *testing.T) {
	listed := []string{"status", "type", "header", "headers", "cookie", "title", "description", "image", "meta",
		"json", "html", "text", "svg", "file", "redirect", "notFound", "error", "url", "language", "jsonld"}
	unlisted := []string{"cache", "persist", "bind", "send", "contentType", "isError", "isSend", "return", "errorString",
		"directory", "layout", "template", "string", "bytes", "setCookie", "deleteCookie", "render", "helloWorld",
		"data", "kind", "code", "css"}
	probe := func(names []string) string {
		parts := make([]string, len(names))
		for i, name := range names {
			parts[i] = "typeof response." + name
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	route := "router.get(({ response }) => response.json({ listed: " + probe(listed) + ", unlisted: " + probe(unlisted) + " }));\n"
	repeat := func(word string, n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = `"` + word + `"`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}

	ui := NewTenant(writeSite(t, map[string]string{
		"router.work":       "import * as router from \"kitwork/router\";\n",
		"names/router.work": "import * as router from \"kitwork/router\";\n" + route,
	}), "localhost")
	if err := ui.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := `{"listed":` + repeat("function", len(listed)) + `,"unlisted":` + repeat("undefined", len(unlisted)) + `}`
	if r := serveGet(ui, "/names", nil); r.Body.String() != want {
		t.Fatalf("router.work:\n got  %s\n want %s", r.Body.String(), want)
	}

	// the template site keeps its names: send, bind, string, setCookie … are still there
	template := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  "import * as router from \"kitwork/router\";\n",
		"names/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"router.get(({ response }) => response.json([typeof response.send, typeof response.bind, typeof response.string, typeof response.setCookie, typeof response.contentType, typeof response.data]));\n",
	}), "localhost")
	if err := template.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(template, "/names", nil); r.Body.String() != `["function","function","function","function","string","undefined"]` {
		t.Fatalf("template site: %s", r.Body.String())
	}
}
