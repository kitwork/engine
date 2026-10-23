package work

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveURL(tn *Tenant, method, url string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, nil)
	req.Host = "localhost"
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	return rec
}

func setCookieHeader(r *httptest.ResponseRecorder) string {
	return strings.Join(r.Header().Values("Set-Cookie"), " | ")
}

// A cookie is Secure when the request came over https, takes expires, and maxAge as a span; in router.work
// a sameSite it does not know, or "none" without Secure, is an error (KITWORK_ROUTER §6, 20/10/2026). A
// template site keeps its quiet Lax.
func TestCookieRules(t *testing.T) {
	template := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"set/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => { ctx.cookie(\"a\", \"1\", { expires: \"2030-01-02\", maxAge: \"30d\" }); return ctx.text(\"ok\"); });\n",
		"weird/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => { ctx.cookie(\"a\", \"1\", { sameSite: \"sideways\" }); return ctx.text(\"ok\"); });\n",
	}), "localhost")
	if err := template.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	over := setCookieHeader(serveURL(template, http.MethodGet, "https://localhost/set"))
	if !strings.Contains(over, "Secure") || !strings.Contains(over, "Max-Age=2592000") || !strings.Contains(over, "Expires=Wed, 02 Jan 2030") {
		t.Errorf("https cookie: %s", over)
	}
	if plain := setCookieHeader(serveURL(template, http.MethodGet, "http://localhost/set")); strings.Contains(plain, "Secure") {
		t.Errorf("http cookie is Secure: %s", plain)
	}
	if weird := setCookieHeader(serveURL(template, http.MethodGet, "http://localhost/weird")); !strings.Contains(weird, "SameSite=Lax") {
		t.Errorf("template's unknown sameSite: %s", weird)
	}

	ui := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"none/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get((context) => context.cookie(\"a\", \"1\", { sameSite: \"none\" }).text(\"ok\"));\n",
		"secure/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get((context) => context.cookie(\"a\", \"1\", { sameSite: \"none\", secure: true }).text(\"ok\"));\n",
		"weird/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get((context) => context.cookie(\"a\", \"1\", { sameSite: \"sideways\" }).text(\"ok\"));\n",
	}), "localhost")
	if err := ui.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	logs := captureLogs(t)
	for path, want := range map[string]string{
		"/none":  "sameSite \\\"none\\\" cần secure: true",
		"/weird": "sameSite \\\"sideways\\\" không có",
	} {
		logs.Reset()
		if r := serveURL(ui, http.MethodGet, "http://localhost"+path); r.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), want) {
			t.Errorf("%s: %d, log lacks %q:\n%s", path, r.Code, want, logs.String())
		}
	}
	if r := serveURL(ui, http.MethodGet, "http://localhost/secure"); r.Code != http.StatusOK || !strings.Contains(setCookieHeader(r), "SameSite=None") {
		t.Errorf("/secure: %d %s", r.Code, setCookieHeader(r))
	}
	if r := serveURL(ui, http.MethodGet, "https://localhost/none"); r.Code != http.StatusOK || !strings.Contains(setCookieHeader(r), "Secure") {
		t.Errorf("/none over https is Secure by itself: %d %s", r.Code, setCookieHeader(r))
	}
}

// captureLogs sends the structured log to a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

// In router.work a status before .redirect() that is not 3xx is an error, not a quiet 303.
func TestUIRedirectStatus(t *testing.T) {
	ui := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"bad/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get((context) => context.status(200).redirect(\"/x\"));\n",
		"moved/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get((context) => context.status(308).redirect(\"/x\"));\n",
	}), "localhost")
	if err := ui.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	logs := captureLogs(t)
	if r := serveGet(ui, "/bad", nil); r.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "`.status(200)` trước `.redirect()`: chuyển hướng là 3xx") {
		t.Errorf("/bad: %d\n%s", r.Code, logs.String())
	}
	if r := serveGet(ui, "/moved", nil); r.Code != http.StatusPermanentRedirect {
		t.Errorf("/moved: %d", r.Code)
	}
	template := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"bad/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => ctx.redirect(\"/x\", 200));\n",
	}), "localhost")
	if err := template.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(template, "/bad", nil); r.Code != http.StatusSeeOther {
		t.Errorf("template /bad: %d, want its 303", r.Code)
	}
}

// A Kitwork UI site serves files only by its allowlist: assets/, the root files of the catalog,
// .well-known/. A template site serves every safe file under the app.
func TestUIStaticAllowlist(t *testing.T) {
	files := func(router string) map[string]string {
		return map[string]string{
			router:                      "",
			"assets/site.css":           "body{}",
			"robots.txt":                "User-agent: *",
			"notes.txt":                 "private notes",
			"docs/guide.txt":            "a guide",
			".well-known/security.txt":  "Contact: x",
			".well-known/../.env":       "",
			"assets/.hidden/secret.txt": "secret",
			"filesystem.kitwork":        "",
		}
	}
	ui := NewTenant(writeSite(t, files("router.work")), "localhost")
	if err := ui.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for path, served := range map[string]bool{
		"/assets/site.css":           true,
		"/robots.txt":                true,
		"/.well-known/security.txt":  true,
		"/notes.txt":                 false,
		"/docs/guide.txt":            false,
		"/assets/.hidden/secret.txt": false,
		"/filesystem.kitwork":        false,
	} {
		r := serveGet(ui, path, nil)
		if got := r.Code == http.StatusOK && !strings.Contains(r.Body.String(), "<html"); got != served {
			t.Errorf("router.work %s: %d %.60q, served=%v want %v", path, r.Code, r.Body.String(), got, served)
		}
	}
	template := NewTenant(writeSite(t, files("router.kitwork.js")), "localhost")
	if err := template.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(template, "/notes.txt", nil); r.Code != http.StatusOK || r.Body.String() != "private notes" {
		t.Errorf("template /notes.txt: %d %q", r.Code, r.Body.String())
	}
}

// A router.work handler does not see request.benchmark nor request.multipartForm; a template site still
// does. request.saveFile and context.saveFile are gone from both (KITWORK_FILE.md mục 17, 22/10/2026):
// reading either fails with the spelling of kitwork/file.
//
// CONTROL: with Request.SaveFile giving null, request.saveFile read no failure (its message null) in both
// sites and the test failed.
func TestUIHidesBenchmarkAndUploads(t *testing.T) {
	probe := `[typeof request.benchmark, typeof request.multipartForm, safe(() => request.saveFile).error.message, safe(() => context.saveFile("doc", "uploads/")).error.message]`
	gone := `request.saveFile(tên, đích) đã bỏ: lưu một upload bằng file.open(\"uploads/\").segment(id, \"avatar.png\").write(request.file(\"avatar\")), vào vùng dữ liệu của app (KITWORK_FILE.md mục 9, 17)`
	ui := NewTenant(writeSite(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";`,
		"a/router.work": "import * as router from \"kitwork/router\";\n" +
			"router.get((context) => { const request = context.request; return context.json(" + probe + "); });\n",
	}), "localhost")
	if err := ui.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(ui, "/a", nil); r.Body.String() != `["undefined","undefined","`+gone+`","`+gone+`"]` {
		t.Errorf("router.work: %s", r.Body.String())
	}
	template := NewTenant(writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"a/router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((context) => { const request = context.request; return context.json(" + probe + "); });\n",
	}), "localhost")
	if err := template.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(template, "/a", nil); strings.Contains(r.Body.String(), "undefined") || strings.Count(r.Body.String(), gone) != 2 {
		t.Errorf("template: %s", r.Body.String())
	}
}
