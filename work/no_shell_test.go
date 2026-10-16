package work

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// loggedPath is text as the log's text handler writes it inside a quoted value (a Windows path's
// backslashes and a quote mark escaped).
func loggedPath(text string) string {
	quoted := strconv.Quote(text)
	return quoted[1 : len(quoted)-1]
}

// A site with no shell (index.kitwork.html), one that serves no template, answers an unknown path in
// production with the engine's plain 404, as a Kitwork UI site with no not-found page does: the shell it
// looked for, an absolute path on the server, goes to the log, once for the route and the generation
// (16/10/2026: "[Error reading index: <path>]" was the whole 404 page, in production too). While
// developing the answer still names it. CONTROL: the site's route answers; a site with a shell and a
// not-found page answers its 404 with that page.
func TestASiteWithNoShellAnswersItsNotFoundWithNoPathOfTheServer(t *testing.T) {
	files := map[string]string{
		"router.kitwork.js": `import { router } from "kitwork"; router.get((ctx) => ctx.json({ ok: true }));`,
	}
	production, logged, err := templateSite(t, false, files)
	if err != nil {
		t.Fatalf("a site with no shell did not start: %v", err)
	}
	shell := filepath.Join(production.resolve(), "index.kitwork.html")
	for _, path := range []string{"/missing", "/wp-login.php", "/missing"} {
		answer := askTemplateSite(production, path)
		if answer.Code != http.StatusNotFound || answer.Body.String() != "Not Found" ||
			answer.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatalf("production GET %s = %d %q (%s), want the plain 404", path, answer.Code, answer.Body.String(), answer.Header().Get("Content-Type"))
		}
	}
	if records := strings.Count(logged.String(), "Kitwork not-found page has no shell"); records != 1 {
		t.Fatalf("the log has %d records of the missing shell, want 1:\n%s", records, logged.String())
	}
	if !strings.Contains(logged.String(), loggedPath(shell)) {
		t.Fatalf("the log does not name the shell looked for (%s):\n%s", shell, logged.String())
	}

	developing, _, err := templateSite(t, true, files)
	if err != nil {
		t.Fatalf("a site with no shell did not start while developing: %v", err)
	}
	shell = filepath.Join(developing.resolve(), "index.kitwork.html")
	if answer := askTemplateSite(developing, "/missing"); answer.Code != http.StatusNotFound || !strings.Contains(answer.Body.String(), shell) {
		t.Fatalf("developing GET /missing = %d %q, want 404 naming %s", answer.Code, answer.Body.String(), shell)
	}

	// CONTROL
	if answer := askTemplateSite(production, "/"); answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), `"ok":true`) {
		t.Fatalf("CONTROL production GET / = %d %q, want the route's JSON", answer.Code, answer.Body.String())
	}
	withShell, _, err := templateSite(t, false, map[string]string{
		"index.kitwork.html":    "<!doctype html><main>{{ @page }}</main>",
		"page.kitwork.html":     "<p>home</p>",
		"notfound.kitwork.html": "<p>không có trang này</p>",
	})
	if err != nil {
		t.Fatalf("CONTROL: a site with a shell did not start: %v", err)
	}
	if answer := askTemplateSite(withShell, "/missing"); answer.Code != http.StatusNotFound || !strings.Contains(answer.Body.String(), "<p>không có trang này") {
		t.Fatalf("CONTROL production GET /missing = %d %q, want 404 with the not-found page", answer.Code, answer.Body.String())
	}
}

// A page a handler chooses (ctx.view) in a site with no shell has nothing to stand in. It fails the
// request as a page that does not compile: 500, the shell looked for in the log with the request id, and
// in the answer only while developing (16/10/2026: 200 with "[Error reading index: <path>]" as the page,
// in production too). It holds for the folder's own page and a page named at the request. CONTROL: with
// a shell the same page answers 200.
func TestAPageChosenInASiteWithNoShellFailsTheRequest(t *testing.T) {
	files := map[string]string{
		"router.kitwork.js":        `import { router } from "kitwork"; router.get((ctx) => ctx.view(ctx.query("page") || ""));`,
		"_other/page.kitwork.html": "<p>other</p>",
	}
	production, logged, err := templateSite(t, false, files)
	if err != nil {
		t.Fatalf("the site did not start: %v", err)
	}
	shell := filepath.Join(production.resolve(), "index.kitwork.html")
	for _, path := range []string{"/", "/?page=_other"} {
		answer := askTemplateSite(production, path)
		body := answer.Body.String()
		if answer.Code != http.StatusInternalServerError || !strings.Contains(body, "req-template") ||
			strings.Contains(body, "index.kitwork.html") || strings.Contains(body, "Error reading index") {
			t.Fatalf("production GET %s = %d %q, want 500 with the status and the request id only", path, answer.Code, body)
		}
	}
	if !strings.Contains(logged.String(), loggedPath(shell)) || !strings.Contains(logged.String(), "req-template") {
		t.Fatalf("the log did not keep the shell looked for with the request id:\n%s", logged.String())
	}

	developing, _, err := templateSite(t, true, files)
	if err != nil {
		t.Fatalf("the site did not start while developing: %v", err)
	}
	shell = filepath.Join(developing.resolve(), "index.kitwork.html")
	if answer := askTemplateSite(developing, "/?page=_other"); answer.Code != http.StatusInternalServerError || !strings.Contains(answer.Body.String(), shell) {
		t.Fatalf("developing GET /?page=_other = %d %q, want 500 naming %s", answer.Code, answer.Body.String(), shell)
	}

	// CONTROL
	files["index.kitwork.html"] = "<!doctype html><main>{{ @page }}</main>"
	withShell, _, err := templateSite(t, false, files)
	if err != nil {
		t.Fatalf("CONTROL: the site with a shell did not start: %v", err)
	}
	if answer := askTemplateSite(withShell, "/?page=_other"); answer.Code != http.StatusOK || !strings.Contains(answer.Body.String(), "<p>other") {
		t.Fatalf("CONTROL production GET /?page=_other = %d %q, want 200 with the page", answer.Code, answer.Body.String())
	}
}

// A {{ layout "name" }} whose file is not there wrote "[Error: file does not exist]" in the page, in
// production too. In production the page writes nothing there and the log says which layout and why;
// while developing the error stands in the page as before. CONTROL: a layout that is there renders in
// both.
func TestALayoutThatIsNotThereWritesNoErrorInAPageInProduction(t *testing.T) {
	files := map[string]string{
		"index.kitwork.html": `<!doctype html><main>{{ layout "missing" }}{{ layout "here" }}{{ @page }}</main>`,
		"here.html":          "<nav>here</nav>",
		"page.kitwork.html":  "<p>home</p>",
	}
	production, logged, err := templateSite(t, false, files)
	if err != nil {
		t.Fatalf("the site did not start: %v", err)
	}
	answer := askTemplateSite(production, "/")
	if body := answer.Body.String(); answer.Code != http.StatusOK || strings.Contains(body, "[Error") || strings.Contains(body, "file does not exist") {
		t.Fatalf("production GET / = %d %q, want 200 with no error in the page", answer.Code, body)
	}
	if !strings.Contains(logged.String(), loggedPath(`layout "missing"`)) || !strings.Contains(logged.String(), "file does not exist") {
		t.Fatalf("the log does not say which layout and why:\n%s", logged.String())
	}
	developing, _, err := templateSite(t, true, files)
	if err != nil {
		t.Fatalf("the site did not start while developing: %v", err)
	}
	if body := askTemplateSite(developing, "/").Body.String(); !strings.Contains(body, "[Error: file does not exist]") {
		t.Fatalf("developing GET / = %q, want the error in the page", body)
	}

	// CONTROL
	for name, site := range map[string]*Tenant{"production": production, "developing": developing} {
		if body := askTemplateSite(site, "/").Body.String(); !strings.Contains(body, "<nav>here</nav>") || !strings.Contains(body, "<p>home") {
			t.Fatalf("CONTROL %s GET / = %q, want the layout that is there and the page", name, body)
		}
	}
}
