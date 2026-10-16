package work

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// A page a handler chooses (ctx.view) that is not there, in a site with no not-found page to stand in
// for it, fails the request as a page that does not compile: 500, the page looked for in the log with the
// request id, and in the answer only while developing (16/10/2026: 200 with the shell and nothing in it,
// and nothing was said). It holds for a page named at the request and for the folder's own page. CONTROL:
// a page that is there answers 200; with a not-found page, the page that is not there answers 200 with it,
// as it did.
func TestAPageChosenThatIsNotThereFailsTheRequest(t *testing.T) {
	files := map[string]string{
		"index.kitwork.html":       "<!doctype html><main>{{ @page }}</main>",
		"router.kitwork.js":        `import { router } from "kitwork"; router.get((ctx) => ctx.view(ctx.query("page") || ""));`,
		"_other/page.kitwork.html": "<p>other</p>",
	}
	production, logged, err := templateSite(t, false, files)
	if err != nil {
		t.Fatalf("the site did not start: %v", err)
	}
	for path, page := range map[string]string{"/?page=nope": filepath.Join("nope", "page.kitwork.html"), "/": "page.kitwork.html"} {
		looked := filepath.Join(production.resolve(), page)
		answer := askTemplateSite(production, path)
		body := answer.Body.String()
		if answer.Code != http.StatusInternalServerError || !strings.Contains(body, "req-template") ||
			strings.Contains(body, "page.kitwork.html") || strings.Contains(body, "<main>") {
			t.Fatalf("production GET %s = %d %q, want 500 with the status and the request id only", path, answer.Code, body)
		}
		if !strings.Contains(logged.String(), loggedPath(looked)) || !strings.Contains(logged.String(), "req-template") {
			t.Fatalf("the log did not keep the page looked for (%s) with the request id:\n%s", looked, logged.String())
		}
	}

	developing, _, err := templateSite(t, true, files)
	if err != nil {
		t.Fatalf("the site did not start while developing: %v", err)
	}
	looked := filepath.Join(developing.resolve(), "nope", "page.kitwork.html")
	if answer := askTemplateSite(developing, "/?page=nope"); answer.Code != http.StatusInternalServerError || !strings.Contains(answer.Body.String(), looked) {
		t.Fatalf("developing GET /?page=nope = %d %q, want 500 naming %s", answer.Code, answer.Body.String(), looked)
	}

	// CONTROL
	for name, site := range map[string]*Tenant{"production": production, "developing": developing} {
		if answer := askTemplateSite(site, "/?page=_other"); answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), "<p>other") {
			t.Fatalf("CONTROL %s GET /?page=_other = %d %q, want 200 with the page", name, answer.Code, answer.Body.String())
		}
	}
	files["notfound.kitwork.html"] = "<p>không có trang này</p>"
	withNotFound, _, err := templateSite(t, false, files)
	if err != nil {
		t.Fatalf("CONTROL: the site with a not-found page did not start: %v", err)
	}
	if answer := askTemplateSite(withNotFound, "/?page=nope"); answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), "<p>không có trang này") {
		t.Fatalf("CONTROL production GET /?page=nope = %d %q, want 200 with the not-found page", answer.Code, answer.Body.String())
	}
}

// A site with a shell and no not-found page answers an unknown path with its shell and nothing in it, at
// 404, as it did; in production the page carries no path of the server, and the log says which not-found
// page was looked for, once for the route and the generation (16/10/2026: nothing was said), and nothing
// when the site starts, for a route that never answers a page is no failure. While developing a comment
// in the page names it, as before. CONTROL: the site's page answers 200; with a not-found page the 404 is
// that page and the log says nothing of it.
func TestASiteWithNoNotFoundPageSaysSoInTheLog(t *testing.T) {
	files := map[string]string{
		"index.kitwork.html":    "<!doctype html><main>{{ @page }}</main>",
		"page.kitwork.html":     "<p>home</p>",
		"api/router.kitwork.js": `import { router } from "kitwork"; router.get((ctx) => ctx.json({ ok: true }));`,
	}
	production, logged, err := templateSite(t, false, files)
	if err != nil {
		t.Fatalf("the site did not start: %v", err)
	}
	notfound := filepath.Join(production.resolve(), "notfound.kitwork.html")
	if strings.Contains(logged.String(), loggedPath(notfound)) || strings.Contains(logged.String(), "page.kitwork.html") {
		t.Fatalf("the site's start logged a page that is not there:\n%s", logged.String())
	}
	for _, path := range []string{"/missing", "/wp-login.php", "/missing"} {
		answer := askTemplateSite(production, path)
		body := answer.Body.String()
		if answer.Code != http.StatusNotFound || !strings.Contains(body, "<main></main>") ||
			strings.Contains(body, "<!-- 404") || strings.Contains(body, "notfound.kitwork.html") {
			t.Fatalf("production GET %s = %d %q, want 404 with the shell and nothing in it", path, answer.Code, body)
		}
	}
	if records := strings.Count(logged.String(), "Kitwork not-found page is not there"); records != 1 {
		t.Fatalf("the log has %d records of the not-found page that is not there, want 1:\n%s", records, logged.String())
	}
	if !strings.Contains(logged.String(), loggedPath(notfound)) {
		t.Fatalf("the log does not name the not-found page looked for (%s):\n%s", notfound, logged.String())
	}

	developing, _, err := templateSite(t, true, files)
	if err != nil {
		t.Fatalf("the site did not start while developing: %v", err)
	}
	notfound = filepath.Join(developing.resolve(), "notfound.kitwork.html")
	if answer := askTemplateSite(developing, "/missing"); answer.Code != http.StatusNotFound || !strings.Contains(answer.Body.String(), "<!-- 404: "+notfound+" -->") {
		t.Fatalf("developing GET /missing = %d %q, want 404 with the comment naming %s", answer.Code, answer.Body.String(), notfound)
	}

	// CONTROL
	if answer := askTemplateSite(production, "/"); answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), "<p>home") {
		t.Fatalf("CONTROL production GET / = %d %q, want 200 with the page", answer.Code, answer.Body.String())
	}
	if answer := askTemplateSite(production, "/api"); answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), `"ok":true`) {
		t.Fatalf("CONTROL production GET /api = %d %q, want the route's JSON", answer.Code, answer.Body.String())
	}
	files["notfound.kitwork.html"] = "<p>không có trang này</p>"
	withNotFound, logged, err := templateSite(t, false, files)
	if err != nil {
		t.Fatalf("CONTROL: the site with a not-found page did not start: %v", err)
	}
	if answer := askTemplateSite(withNotFound, "/missing"); answer.Code != http.StatusNotFound || !strings.Contains(answer.Body.String(), "<p>không có trang này") {
		t.Fatalf("CONTROL production GET /missing = %d %q, want 404 with the not-found page", answer.Code, answer.Body.String())
	}
	if strings.Contains(logged.String(), "Kitwork not-found page is not there") {
		t.Fatalf("CONTROL: the log speaks of a not-found page that is there:\n%s", logged.String())
	}
}
