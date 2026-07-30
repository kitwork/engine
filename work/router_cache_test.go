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
	write("failed/router.kitwork.js", `import { router } from "kitwork";`+"\n"+`router.get((ctx) => fail("cache failure")).cache("1h");`)
	write("fallback/router.kitwork.js", `import { router } from "kitwork";`+"\n"+`router.get((ctx) => ctx.error("boom")).error((ctx) => ctx.status(200).text("fallback")).cache("1h");`)
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
	// Runtime failures keep their request provenance and never become cache entries.
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)

	req := httptest.NewRequest(http.MethodGet, "http://localhost/failed", nil)
	req.Header.Set("X-Request-ID", "runtime-cache-test")
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "cache failure") {
		t.Fatalf("failed response: code=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Kitwork-Cache"); got != "" {
		t.Fatalf("failed response must not be cached, header=%q", got)
	}
	if got := rec.Header().Get("X-Request-ID"); got != "runtime-cache-test" {
		t.Fatalf("request id = %q, want runtime-cache-test", got)
	}
	if r := hit(tn, "/failed"); r.Header().Get("X-Kitwork-Cache") != "" {
		t.Fatalf("second failed response must execute again, cache header=%q", r.Header().Get("X-Kitwork-Cache"))
	}

	logged := logs.String()
	for _, field := range []string{
		`"request_id":"runtime-cache-test"`,
		`"site":"localhost"`,
		`"stage":"handler"`,
		`"program":`,
		`"code":"RUNTIME_ERROR"`,
		`"source":"router.kitwork.js"`,
	} {
		if !strings.Contains(logged, field) {
			t.Fatalf("runtime diagnostic missing %s:\n%s", field, logged)
		}
	}

	// A handled error may intentionally return 200, but it is still not cacheable.
	for i := 0; i < 2; i++ {
		r := hit(tn, "/fallback")
		if r.Code != http.StatusOK || r.Body.String() != "fallback" {
			t.Fatalf("fallback #%d: code=%d body=%q", i+1, r.Code, r.Body.String())
		}
		if got := r.Header().Get("X-Kitwork-Cache"); got != "" {
			t.Fatalf("fallback #%d must not be cached, header=%q", i+1, got)
		}
	}

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
