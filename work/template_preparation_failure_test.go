package work

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// templateSite writes files (paths relative to the site) and boots the site in production or while
// developing (AllowLocal), its log captured. err is the boot's.
func templateSite(t *testing.T, developing bool, files map[string]string) (tenant *Tenant, logged *bytes.Buffer, err error) {
	t.Helper()
	saved := AllowLocal
	AllowLocal = developing
	t.Cleanup(func() { AllowLocal = saved })
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, "acme", "localhost", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logged = &bytes.Buffer{}
	savedLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logged, nil)))
	t.Cleanup(func() { slog.SetDefault(savedLog) })
	tenant = NewTenant(root, "localhost")
	if err = tenant.Run(); err == nil {
		t.Cleanup(tenant.Close)
	}
	return tenant, logged, err
}

func askTemplateSite(tenant *Tenant, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	request.Header.Set("Accept", "text/html")
	request.Header.Set("X-Request-ID", "req-template")
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, request)
	return recorder
}

// A not-found page whose own text does not prepare (a {{ if }} that cannot be read) keeps the site from
// starting, as a page that does not prepare does (16/10/2026: the site started, and in production every
// 404 answered the parser's message as its whole page). CONTROL: the same page with the condition
// written right starts and answers its 404; a site with no shell (index.kitwork.html) starts and
// answers its 404 as it did.
func TestANotFoundPageThatDoesNotPrepareKeepsTheSiteFromStarting(t *testing.T) {
	site := func(condition string) map[string]string {
		return map[string]string{
			"index.kitwork.html":    "<!doctype html>{{ @page }}",
			"page.kitwork.html":     "{{ if ok }}home{{ end }}",
			"notfound.kitwork.html": "<main>{{ if " + condition + " }}x{{ end }}404</main>",
		}
	}
	_, _, err := templateSite(t, false, site("a = b"))
	if err == nil {
		t.Fatal("a site whose not-found page does not prepare started")
	}
	for _, want := range []string{"prepare not-found template", "notfound.kitwork.html:1:15: {{ if a = b }}: một dấu = là phép gán"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("boot said %q, want it to hold %q", err, want)
		}
	}

	// CONTROL
	fine, _, err := templateSite(t, false, site("a == b"))
	if err != nil {
		t.Fatalf("a site whose not-found page prepares did not start: %v", err)
	}
	if answer := askTemplateSite(fine, "/missing"); answer.Code != http.StatusNotFound || !strings.Contains(answer.Body.String(), "<main>x404</main>") {
		t.Fatalf("GET /missing = %d %q, want 404 with the not-found page", answer.Code, answer.Body.String())
	}
	noShell, _, err := templateSite(t, false, map[string]string{
		"router.kitwork.js": `import { router } from "kitwork"; router.get((ctx) => ctx.json({ ok: true }));`,
	})
	if err != nil {
		t.Fatalf("a site with no shell did not start: %v", err)
	}
	if answer := askTemplateSite(noShell, "/missing"); answer.Code != http.StatusNotFound {
		t.Fatalf("GET /missing on a site with no shell = %d, want 404", answer.Code)
	}
}

// A page a handler chooses (ctx.view("page")) prepares when it is asked for. One whose own text does not
// prepare fails the request as a page that does not compile: 500, the message whole in the log, and in
// the answer only while developing; in production the answer is the status and the request id
// (16/10/2026: the answer was 200 with the parser's message as the page, in production too). CONTROL: a
// page chosen the same way that prepares answers 200.
func TestAPageChosenAtRequestThatDoesNotPrepareFailsTheRequest(t *testing.T) {
	files := map[string]string{
		"index.kitwork.html":       "<!doctype html>{{ @page }}",
		"router.kitwork.js":        `import { router } from "kitwork"; router.get((ctx) => ctx.view(ctx.query("page") || "_other"));`,
		"_other/page.kitwork.html": "<p>{{ if a = b }}x{{ end }}</p>",
		"_fine/page.kitwork.html":  "<p>{{ if a == b }}same{{ end }}</p>",
	}
	const said = "_other/page.kitwork.html:1:12: {{ if a = b }}: một dấu = là phép gán"

	production, logged, err := templateSite(t, false, files)
	if err != nil {
		t.Fatalf("the site did not start: %v", err)
	}
	answer := askTemplateSite(production, "/")
	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("production GET / = %d %q, want 500", answer.Code, answer.Body.String())
	}
	if body := answer.Body.String(); strings.Contains(body, "{{ if") || !strings.Contains(body, "req-template") {
		t.Fatalf("production answered %q, want the status and the request id only", body)
	}
	if !strings.Contains(logged.String(), "một dấu = là phép gán") || !strings.Contains(logged.String(), "req-template") {
		t.Fatalf("the log did not keep the message with the request id:\n%s", logged.String())
	}
	if control := askTemplateSite(production, "/?page=_fine"); control.Code != http.StatusOK || !strings.Contains(control.Body.String(), "<p>same") {
		t.Fatalf("CONTROL production GET /?page=_fine = %d %q, want 200 with the page", control.Code, control.Body.String())
	}

	developing, _, err := templateSite(t, true, files)
	if err != nil {
		t.Fatalf("the site did not start while developing: %v", err)
	}
	answer = askTemplateSite(developing, "/")
	if answer.Code != http.StatusInternalServerError || !strings.Contains(answer.Body.String(), said) {
		t.Fatalf("developing GET / = %d %q, want 500 with %q", answer.Code, answer.Body.String(), said)
	}
}
