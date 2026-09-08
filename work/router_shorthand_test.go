package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffold builds a minimal tenant and returns a GET helper.
func scaffold(t *testing.T, files map[string]string) func(string) *httptest.ResponseRecorder {
	t.Helper()
	tmp, err := os.MkdirTemp("", "kitwork-shorthand-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })

	dir := filepath.Join(tmp, "test", "localhost")
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}
}

const shorthandShell = `<!doctype html><body>{{ @page }}</body>`

// robots(true) is the short way to say "yes, the default" — it must generate
// exactly what the no-argument form generates.
func TestRobotsBooleanGeneratesTheDefaultDocument(t *testing.T) {
	get := scaffold(t, map[string]string{
		"filesystem.kitwork":  "",
		"index.kitwork.html":  shorthandShell,
		"page.kitwork.html":   `<main>home</main>`,
		"router.kitwork.js":   "import { router } from \"kitwork\";\nrouter.robots(true);",
	})

	rec := get("/robots.txt")
	if rec.Code != 200 {
		t.Fatalf("robots(true): code=%d body=%q, want 200", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "User-agent: *") || !strings.Contains(body, "Allow: /") {
		t.Fatalf("robots(true) did not generate the default document.\ngot: %q", body)
	}
}

// false must declare nothing at all. Serving an empty document would be worse
// than a 404: an empty robots.txt still overrides a crawler's defaults.
func TestRobotsFalseDeclaresNothing(t *testing.T) {
	get := scaffold(t, map[string]string{
		"filesystem.kitwork":   "",
		"index.kitwork.html":   shorthandShell,
		"page.kitwork.html":    `<main>home</main>`,
		"notfound.kitwork.html": `<main>404</main>`,
		"router.kitwork.js":    "import { router } from \"kitwork\";\nrouter.robots(false);",
	})

	if rec := get("/robots.txt"); rec.Code == 200 && strings.Contains(rec.Body.String(), "User-agent") {
		t.Fatalf("robots(false) still served a generated document: %q", rec.Body.String())
	}
}

// assets(true) mounts the conventional folder without naming it.
func TestAssetsBooleanMountsTheConventionalFolder(t *testing.T) {
	get := scaffold(t, map[string]string{
		"filesystem.kitwork": "",
		"index.kitwork.html": shorthandShell,
		"page.kitwork.html":  `<main>home</main>`,
		"assets/note.txt":    "hello from assets",
		"router.kitwork.js":  "import { router } from \"kitwork\";\nrouter.assets(true);",
	})

	rec := get("/assets/note.txt")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hello from assets") {
		t.Fatalf("assets(true) did not serve assets/note.txt: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// A private "_assets" folder is the same convention: publicAssetURL already
// drops the underscore, so it must serve at the clean /assets URL.
func TestAssetsBooleanMountsThePrivateFolderAtTheCleanURL(t *testing.T) {
	get := scaffold(t, map[string]string{
		"filesystem.kitwork": "",
		"index.kitwork.html": shorthandShell,
		"page.kitwork.html":  `<main>home</main>`,
		"_assets/note.txt":   "hello from private assets",
		"router.kitwork.js":  "import { router } from \"kitwork\";\nrouter.assets(true);",
	})

	rec := get("/assets/note.txt")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hello from private assets") {
		t.Fatalf("assets(true) did not serve _assets/note.txt at /assets/: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// The shorthand must not invent a mount for a folder that is not there, or a
// site gains a route that can only ever 404.
func TestAssetsBooleanIgnoresAMissingFolder(t *testing.T) {
	get := scaffold(t, map[string]string{
		"filesystem.kitwork":    "",
		"index.kitwork.html":    shorthandShell,
		"page.kitwork.html":     `<main>home</main>`,
		"notfound.kitwork.html": `<main>404</main>`,
		"router.kitwork.js":     "import { router } from \"kitwork\";\nrouter.assets(true);",
	})

	if rec := get("/assets/note.txt"); rec.Code == 200 {
		t.Fatalf("assets(true) served from a folder that does not exist: %q", rec.Body.String())
	}
}
