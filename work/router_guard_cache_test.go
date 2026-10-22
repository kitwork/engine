package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeSite lays a filesystem site out under a temp root and returns the root (the tenant's identity
// folder is "test", the site "localhost").
func writeSite(t *testing.T, files map[string]string) string {
	t.Helper()
	tmp, err := os.MkdirTemp("", "kitwork-route-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	dir := filepath.Join(tmp, "test", "localhost")
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tmp
}

func serveGet(tn *Tenant, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	return rec
}

// A method's guard runs before its response cache is read: a request the guard let through fills the
// cache, and the next request the guard refuses is refused, not handed the cached bytes (19/10/2026:
// the cache was read first, so one allowed request opened the route to everyone until it expired).
func TestMethodGuardRunsBeforeResponseCache(t *testing.T) {
	const allowed = `(ctx) => ctx.header("X-Pass") == "yes"`
	tmp := writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import { router } from "kitwork";`,
		"ram/router.kitwork.js": `import { router } from "kitwork";` + "\n" +
			`router.get((ctx) => ctx.text("secret")).cache("1h").guard(` + allowed + `);`,
		"disk/router.kitwork.js": `import { router } from "kitwork";` + "\n" +
			`router.get((ctx) => ctx.text("secret")).persist("1h").guard(` + allowed + `);`,
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	pass := map[string]string{"X-Pass": "yes"}
	for _, route := range []string{"/ram", "/disk"} {
		if r := serveGet(tn, route, pass); r.Code != http.StatusOK || r.Body.String() != "secret" {
			t.Fatalf("%s allowed: code=%d body=%q", route, r.Code, r.Body.String())
		}
		// the guard let the same request through: the cache answers it
		if r := serveGet(tn, route, pass); r.Body.String() != "secret" || r.Header().Get("X-Kitwork-Cache") != "hit" {
			t.Fatalf("%s allowed again should be a cache hit: code=%d body=%q cache=%q", route, r.Code, r.Body.String(), r.Header().Get("X-Kitwork-Cache"))
		}
		for i := 0; i < 2; i++ {
			r := serveGet(tn, route, nil)
			if r.Body.String() == "secret" || r.Header().Get("X-Kitwork-Cache") == "hit" || r.Code == http.StatusOK {
				t.Fatalf("%s refused #%d got past the guard: code=%d body=%q cache=%q", route, i+1, r.Code, r.Body.String(), r.Header().Get("X-Kitwork-Cache"))
			}
		}
	}
}
