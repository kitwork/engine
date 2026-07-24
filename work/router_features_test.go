package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// One tenant exercising the root-router declarations added for the reference standard:
// stacked .ratelimit() windows, .favicon() served at /favicon.ico, the .assets() allowlist,
// and .language() → $.meta.language.
func TestTreeRootRouterDeclarations(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-tree-decl-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.ratelimit({ ip: 2, period: "120ms" }).ratelimit({ ip: 3, period: "1m" });`+"\n"+
			`router.favicon("./assets/favicon.ico").assets("./assets/*").language("en");`)
	write("index.kitwork.html", `<html lang="{{ $.meta.language }}"><body>{{ @page }}</body></html>`)
	write("page.kitwork.html", `<main>home</main>`)
	write("notfound.kitwork.html", `<main>nothing here</main>`)
	write("assets/favicon.ico", "FAVICON-BYTES")
	write("assets/style.css", "body{}")
	write("secret.txt", "leak me not")
	write(".persist/cached.gob", "gob-bytes")

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("tenant failed to run: %v", err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		req.RemoteAddr = "8.8.4.4:1234"
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}

	// .language(): the shell reads $.meta.language. (The production minifier may drop the quotes.)
	if rec := get("/"); rec.Code != 200 ||
		(!strings.Contains(rec.Body.String(), `lang="en"`) && !strings.Contains(rec.Body.String(), "lang=en")) {
		t.Fatalf("language meta: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Stacked .ratelimit(): burst 2/120ms + sustained 3/1m share the dimension but not a bucket.
	// The / above consumed (burst 1, sustained 1).
	if rec := get("/"); rec.Code != 200 { // burst 2, sustained 2
		t.Fatalf("second request should pass, got %d", rec.Code)
	}
	if rec := get("/"); rec.Code != 429 { // burst full → 429 (sustained untouched at 2)
		t.Fatalf("third request should hit the burst window, got %d", rec.Code)
	}
	time.Sleep(150 * time.Millisecond)    // burst window resets; sustained window persists
	if rec := get("/"); rec.Code != 200 { // burst 1, sustained 3 — the last sustained token
		t.Fatalf("burst window should have reset, got %d", rec.Code)
	}
	if rec := get("/"); rec.Code != 429 { // burst 2 ok, sustained 4 > 3 → 429
		t.Fatalf("sustained window should now reject, got %d", rec.Code)
	}

	// .favicon(): served at /favicon.ico from the declared file (static path, not rate-limited).
	if rec := get("/favicon.ico"); rec.Code != 200 || rec.Body.String() != "FAVICON-BYTES" {
		t.Fatalf("favicon: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// .assets() allowlist: declared prefix serves; anything else routes the tree (→ 404 view).
	if rec := get("/assets/style.css"); rec.Code != 200 {
		t.Fatalf("allowlisted asset should serve, got %d", rec.Code)
	}
	if rec := get("/secret.txt"); rec.Code != 404 {
		t.Fatalf("undeclared file must NOT be served, got %d", rec.Code)
	}

	// Dot segments are never served, allowlist or not (.persist/, .env, .git/).
	if rec := get("/.persist/cached.gob"); rec.Code == 200 {
		t.Fatal("dot-segment path must never serve")
	}
}
