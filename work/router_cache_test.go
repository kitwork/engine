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

func TestTreeCacheQueryProjection(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-cache-projection-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) {
		file := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("filesystem.kitwork", "")
	write("router.kitwork.js", `import { router } from "kitwork";`)
	write("by-string/router.kitwork.js", `import { router } from "kitwork";
router.get((ctx) => ctx.text("page=" + (ctx.query("page") || "") + ";noise=" + (ctx.query("noise") || ""))).cache("1h", "page");`)
	write("by-array/router.kitwork.js", `import { router } from "kitwork";
router.get((ctx) => ctx.text("page=" + (ctx.query("page") || "") + ";category=" + (ctx.query("category") || "") + ";noise=" + (ctx.query("noise") || ""))).cache("1h", ["page", "category"]);`)
	write("by-object/router.kitwork.js", `import { router } from "kitwork";
router.get((ctx) => ctx.text("page=" + (ctx.query("page") || "") + ";list=" + (ctx.query("list") || "") + ";noise=" + (ctx.query("noise") || ""))).cache("1h", { page: true, list: ["new", "click", "vote"] });`)
	write("full-query/router.kitwork.js", `import { router } from "kitwork";
router.get((ctx) => ctx.text("page=" + (ctx.query("page") || "") + ";category=" + (ctx.query("category") || ""))).cache("1h");`)
	write("saved/router.kitwork.js", `import { router } from "kitwork";
router.get((ctx) => ctx.text("page=" + (ctx.query("page") || "") + ";noise=" + (ctx.query("noise") || ""))).cache("1h", { page: true }).persist("1h");`)
	write("drive/router.kitwork.js", `import { router } from "kitwork";
router.get((ctx) => {
    if (ctx.header("X-KitJS-Drive") == "1") return ctx.text("drive");
    return ctx.text("document");
}).cache("1h", []);`)
	write("dynamic/router.kitwork.js", `import { router } from "kitwork";
router.guard((ctx, response) => {
    const value = ctx.query("guard") || "none";
    response.header("X-Guard-Value", value);
    ctx.cookie("guard_seen", value);
    return true;
});
router.get((ctx) => ctx.text("dynamic-body")).cache("1h", []);`)

	hit := func(tn *Tenant, target string, drive bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+target, nil)
		if drive {
			req.Header.Set("X-KitJS-Drive", "1")
		}
		rec := httptest.NewRecorder()
		tn.Serve(rec, req)
		return rec
	}
	assertMiss := func(t *testing.T, response *httptest.ResponseRecorder, body string) {
		t.Helper()
		if response.Code != http.StatusOK || response.Body.String() != body || response.Header().Get("X-Kitwork-Cache") != "" {
			t.Fatalf("cache miss: code=%d body=%q header=%q", response.Code, response.Body.String(), response.Header().Get("X-Kitwork-Cache"))
		}
	}
	assertHit := func(t *testing.T, response *httptest.ResponseRecorder, body string) {
		t.Helper()
		if response.Code != http.StatusOK || response.Body.String() != body || response.Header().Get("X-Kitwork-Cache") != "hit" {
			t.Fatalf("cache hit: code=%d body=%q header=%q", response.Code, response.Body.String(), response.Header().Get("X-Kitwork-Cache"))
		}
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	assertMiss(t, hit(tenant, "/by-string?page=2&noise=first", false), "page=2;noise=first")
	assertHit(t, hit(tenant, "/by-string?noise=ignored&page=2", false), "page=2;noise=first")
	assertMiss(t, hit(tenant, "/by-string?page=3&noise=third", false), "page=3;noise=third")

	assertMiss(t, hit(tenant, "/by-array?page=2&category=tools&noise=first", false), "page=2;category=tools;noise=first")
	assertHit(t, hit(tenant, "/by-array?noise=ignored&category=tools&page=2", false), "page=2;category=tools;noise=first")

	assertMiss(t, hit(tenant, "/by-object?page=1&list=new&noise=first", false), "page=1;list=new;noise=first")
	assertHit(t, hit(tenant, "/by-object?noise=ignored&list=new&page=1", false), "page=1;list=new;noise=first")
	assertMiss(t, hit(tenant, "/by-object?page=1&list=invalid", false), "page=1;list=invalid;noise=")
	assertMiss(t, hit(tenant, "/by-object?page=1&list=invalid", false), "page=1;list=invalid;noise=")
	assertMiss(t, hit(tenant, "/by-object?page=1&page=2&list=new", false), "page=1;list=new;noise=")
	assertMiss(t, hit(tenant, "/by-object?page=1&page=2&list=new", false), "page=1;list=new;noise=")

	assertMiss(t, hit(tenant, "/full-query?page=4&category=design", false), "page=4;category=design")
	assertHit(t, hit(tenant, "/full-query?category=design&page=4", false), "page=4;category=design")

	assertMiss(t, hit(tenant, "/saved?page=7&noise=first", false), "page=7;noise=first")
	fresh := NewTenant(tmp, "localhost")
	if err := fresh.Run(); err != nil {
		t.Fatalf("fresh run: %v", err)
	}
	assertHit(t, hit(fresh, "/saved?noise=ignored&page=7", false), "page=7;noise=first")

	assertMiss(t, hit(tenant, "/drive", false), "document")
	assertMiss(t, hit(tenant, "/drive", true), "drive")
	assertHit(t, hit(tenant, "/drive", false), "document")
	assertHit(t, hit(tenant, "/drive", true), "drive")

	firstDynamic := hit(tenant, "/dynamic?guard=first", false)
	assertMiss(t, firstDynamic, "dynamic-body")
	if firstDynamic.Header().Get("X-Guard-Value") != "first" || firstDynamic.Result().Cookies()[0].Value != "first" {
		t.Fatalf("first dynamic guard state was not written: header=%q cookies=%#v", firstDynamic.Header().Get("X-Guard-Value"), firstDynamic.Result().Cookies())
	}
	secondDynamic := hit(tenant, "/dynamic?guard=second", false)
	assertHit(t, secondDynamic, "dynamic-body")
	if secondDynamic.Header().Get("X-Guard-Value") != "second" || secondDynamic.Result().Cookies()[0].Value != "second" {
		t.Fatalf("cached response dropped current guard state: header=%q cookies=%#v", secondDynamic.Header().Get("X-Guard-Value"), secondDynamic.Result().Cookies())
	}
}

func TestTreeCacheQueryProjectionRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		router string
		want   string
	}{
		{
			name:   "callback selector",
			router: `import { router } from "kitwork"; router.get((ctx) => ctx.text("ok")).cache("1h", (page) => page == "1");`,
			want:   "cache query projection must be a string, array, or object",
		},
		{
			name:   "different cache and persist selectors",
			router: `import { router } from "kitwork"; router.get((ctx) => ctx.text("ok")).cache("1h", "page").persist("1h", "category");`,
			want:   "cache and persist on the same route must use the same query projection",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmp, err := os.MkdirTemp("", "kitwork-cache-invalid-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmp)

			dir := filepath.Join(tmp, "test", "localhost")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "filesystem.kitwork"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(test.router), 0o644); err != nil {
				t.Fatal(err)
			}

			tenant := NewTenant(tmp, "localhost")
			err = tenant.Run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run error = %v, want substring %q", err, test.want)
			}
		})
	}
}
