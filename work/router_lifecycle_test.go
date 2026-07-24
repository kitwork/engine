package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Validates the reworked lifecycle: guard([...]) runs sequentially (and short-circuits), success()
// runs after a clean handler, error() catches, and middleware() is an alias that joins the guard
// chain.
func TestTreeLifecycleGuardSuccessError(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-treelife-*")
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

	// guard([g1, g2]) — sequential, each may short-circuit. Proves BOTH run and IN ORDER.
	write("order/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.get((ctx) => ctx.text("handler ok")).guard([`+"\n"+
			`  (ctx) => ctx.query("who") == "1" ? ctx.status(403).text("blocked by 1") : true,`+"\n"+
			`  (ctx) => ctx.query("who") == "2" ? ctx.status(403).text("blocked by 2") : true`+"\n"+
			`]);`)

	// success() runs after a clean handler and can produce the response.
	write("success/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.get((ctx) => {}).success((ctx) => ctx.text("success ran"));`)

	// error() catches a handler that raised via ctx.error().
	write("fail/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.get((ctx) => ctx.error("boom")).error((ctx) => ctx.status(500).text("caught"));`)

	// middleware() is a deprecated alias — it joins the guard chain.
	write("mw/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.get((ctx) => ctx.text("mw handler")).middleware((ctx) => ctx.query("stop") == "1" ? ctx.status(403).text("stopped by mw") : true);`)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	hit := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec.Code, rec.Body.String()
	}

	cases := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{"guard chain passes → handler", "/order", 200, "handler ok"},
		{"first guard blocks", "/order?who=1", 403, "blocked by 1"},
		{"second guard blocks (first passed)", "/order?who=2", 403, "blocked by 2"},
		{"success hook runs after clean handler", "/success", 200, "success ran"},
		{"error hook catches", "/fail", 500, "caught"},
		{"middleware alias joins guard chain", "/mw?stop=1", 403, "stopped by mw"},
		{"middleware alias passes", "/mw", 200, "mw handler"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := hit(c.path)
			if code != c.wantStatus {
				t.Fatalf("%s: status=%d want %d (body=%q)", c.path, code, c.wantStatus, body)
			}
			if body != c.wantBody {
				t.Fatalf("%s: body=%q want %q", c.path, body, c.wantBody)
			}
		})
	}
}
