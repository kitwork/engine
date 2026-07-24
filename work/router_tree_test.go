package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A REAL filesystem-routed tenant, built on disk and served through the same path the engine
// uses (Run() → Serve()). It exercises the whole tree pipeline end to end:
//
//	filesystem.kitwork              → opts the tenant into folder routing
//	index.kitwork.html              → the shell (inherited, walked up)
//	page.kitwork.html               → the home page (local)
//	notfound.kitwork.html           → the 404 view (bubbles up)
//	users/page.kitwork.html         → a static child page
//	users/{user}/router.kitwork.js  → a dynamic folder with a handler that reads the param
//	users/{user}/router → .guard     → blocks one value to prove folder guards run
func TestTreeTenantServesFilesystemRoutes(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-tree-*")
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

	write("filesystem.kitwork", "")
	write("router.kitwork.js", `import { router } from "kitwork";`)
	write("index.kitwork.html", `<!doctype html><html><body><div id="shell">{{ @page }}</div></body></html>`)
	write("page.kitwork.html", `<main>home page</main>`)
	write("notfound.kitwork.html", `<main>nothing here</main>`)

	write("users/page.kitwork.html", `<main>all users</main>`)

	write("users/{user}/page.kitwork.html", `<main>a profile</main>`)
	write("users/{user}/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.guard((ctx) => ctx.params("user") == "banned" ? ctx.status(403).text("blocked") : true);`+"\n"+
			`router.get().handle((ctx) => ctx.json({ user: ctx.params("user") }));`)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("tree tenant failed to run: %v", err)
	}
	if tenant.tree == nil {
		t.Fatal("tenant.tree is nil — filesystem.kitwork marker did not activate tree mode")
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}

	cases := []struct {
		name       string
		path       string
		wantStatus int
		wantSub    string
	}{
		{"home page renders in shell", "/", 200, "shell"},
		{"home page body", "/", 200, "home page"},
		{"static child page", "/users", 200, "all users"},
		{"dynamic handler reads param", "/users/quoc", 200, `"user":"quoc"`},
		{"folder guard blocks a value", "/users/banned", 403, "blocked"},
		{"unknown path → notfound @404", "/nope", 404, "nothing here"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := get(c.path)
			if rec.Code != c.wantStatus {
				t.Fatalf("%s: status = %d, want %d (body=%q)", c.path, rec.Code, c.wantStatus, rec.Body.String())
			}
			if c.wantSub != "" && !strings.Contains(rec.Body.String(), c.wantSub) {
				t.Fatalf("%s: body %q does not contain %q", c.path, rec.Body.String(), c.wantSub)
			}
		})
	}
}
