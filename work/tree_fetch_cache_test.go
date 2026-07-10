package work

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The router-style chain on outbound HTTP, end to end through a real tenant VM:
// http.cache("1m").persist("1m").get(url) — read-through RAM + per-tenant disk under
// <tenant>/.persist/fetch/, and the fetch() builtin's { cache } option scoped to the same tenant.
func TestTreeOutboundFetchCache(t *testing.T) {
	savedLocal := AllowLocal
	AllowLocal = true // the upstream test server is loopback; SSRF guard would block it otherwise
	defer func() { AllowLocal = savedLocal }()

	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		fmt.Fprintf(w, `{"hits":%d}`, upstreamHits)
	}))
	defer upstream.Close()

	tmp, err := os.MkdirTemp("", "kitwork-fetch-cache-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, http } from "kitwork";` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`    const res = http.cache("1m").persist("1m").get("` + upstream.URL + `");` + "\n" +
		`    return ctx.json({ body: res.text(), cached: res.cached });` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	get := func() string {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec.Body.String()
	}

	// First request: live. Second: served from the tenant's RAM tier — upstream sees ONE hit.
	first := get()
	second := get()
	if upstreamHits != 1 {
		t.Fatalf("upstream hits = %d, want 1 (read-through)", upstreamHits)
	}
	if !strings.Contains(first, `\"hits\":1`) && !strings.Contains(first, `"hits":1`) {
		t.Errorf("first body: %s", first)
	}
	if !strings.Contains(second, `"cached":true`) {
		t.Errorf("second response should be flagged cached: %s", second)
	}

	// The persisted copy lives INSIDE this tenant's folder — .persist/fetch/, web-unreachable.
	fetchDir := filepath.Join(dir, ".persist", "fetch")
	entries, err := os.ReadDir(fetchDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("persisted fetch missing under %s: %v", fetchDir, err)
	}

	// STALE-ON-ERROR across a RESTART: a fresh tenant (empty RAM) + a dead upstream must still
	// answer from the persisted copy.
	upstream.Close()
	fresh := NewTenant(tmp, "localhost")
	if err := fresh.Run(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	fresh.Serve(rec, req)
	if !strings.Contains(rec.Body.String(), `"cached":true`) {
		t.Errorf("restart + dead upstream should serve the persisted copy, got: %s", rec.Body.String())
	}
}
