package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Exercises the response caching + rate limiting wired into the tree: .cache (RAM), .persist (disk,
// surviving a fresh tenant = "restart"), and .limit (429).
func TestTreeCachePersistLimit(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-cache-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("filesystem.kitwork", "")
	write("router.kitwork.js", `import { router } from "kitwork";`)
	write("cached/router.kitwork.js", `import { router } from "kitwork";`+"\n"+`router.get((ctx) => ctx.text("cached-body")).cache("1h");`)
	write("saved/router.kitwork.js", `import { router } from "kitwork";`+"\n"+`router.get((ctx) => ctx.text("saved-body")).persist("1h");`)
	write("limited/router.kitwork.js", `import { router } from "kitwork";`+"\n"+`router.get((ctx) => ctx.text("ok")).limit({ rate: 2, per: "1m" });`)

	hit := func(tn *Tenant, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		return rec
	}

	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	// .cache — first is a miss, second is served from RAM (X-Kitwork-Cache: hit).
	if r := hit(tn, "/cached"); r.Code != 200 || r.Body.String() != "cached-body" || r.Header().Get("X-Kitwork-Cache") != "" {
		t.Fatalf("cache miss: code=%d body=%q hdr=%q", r.Code, r.Body.String(), r.Header().Get("X-Kitwork-Cache"))
	}
	if r := hit(tn, "/cached"); r.Code != 200 || r.Body.String() != "cached-body" || r.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf("cache HIT expected: code=%d body=%q hdr=%q", r.Code, r.Body.String(), r.Header().Get("X-Kitwork-Cache"))
	}

	// .persist — first writes a file under .persist/, which SURVIVES a fresh tenant (restart).
	if r := hit(tn, "/saved"); r.Code != 200 || r.Body.String() != "saved-body" {
		t.Fatalf("persist first: code=%d body=%q", r.Code, r.Body.String())
	}
	persistDir := filepath.Join(dir, ".persist")
	if entries, _ := os.ReadDir(persistDir); len(entries) == 0 {
		t.Fatalf(".persist/ should contain the saved response, got empty")
	}
	fresh := NewTenant(tmp, "localhost") // fresh RAM cache — only disk survives
	if err := fresh.Run(); err != nil {
		t.Fatal(err)
	}
	if r := hit(fresh, "/saved"); r.Code != 200 || r.Body.String() != "saved-body" || r.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf("persist should serve from disk on a fresh tenant: code=%d body=%q hdr=%q", r.Code, r.Body.String(), r.Header().Get("X-Kitwork-Cache"))
	}

	// .limit — rate 2 / window: 200, 200, then 429.
	if r := hit(tn, "/limited"); r.Code != 200 {
		t.Fatalf("limit #1 = %d, want 200", r.Code)
	}
	if r := hit(tn, "/limited"); r.Code != 200 {
		t.Fatalf("limit #2 = %d, want 200", r.Code)
	}
	if r := hit(tn, "/limited"); r.Code != 429 {
		t.Fatalf("limit #3 = %d, want 429", r.Code)
	}
}
