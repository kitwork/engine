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

// Per-folder hot reload: editing a SUBFOLDER's router (or a module it imports) recompiles just
// that folder; creating a folder re-enters the tree — no restart, no touching the root router.
func TestTreeFolderHotReload(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-tree-hot-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) string {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	touchFuture := func(p string, secs int) {
		ft := time.Now().Add(time.Duration(secs) * time.Second)
		if err := os.Chtimes(p, ft, ft); err != nil {
			t.Fatal(err)
		}
	}

	write("router.kitwork.js", `import { router } from "kitwork";`)
	write("_core/service.kitwork.js", `export const answer = () => ("service-v1");`)
	apiRouter := write("api/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`import { answer } from "../_core/service.kitwork.js";`+"\n"+
			`router.get().handle((ctx) => ctx.text("api-v1 " + answer()));`)

	tenant := NewTenant(tmp, "localhost")
	tenant.HotReload = true
	if err := tenant.Run(); err != nil {
		t.Fatalf("tenant failed to run: %v", err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}

	// Baseline.
	if rec := get("/api"); !strings.Contains(rec.Body.String(), "api-v1 service-v1") {
		t.Fatalf("baseline: %d %s", rec.Code, rec.Body.String())
	}

	// 1. Edit the SUBFOLDER router — only this folder recompiles.
	write("api/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`import { answer } from "../_core/service.kitwork.js";`+"\n"+
			`router.get().handle((ctx) => ctx.text("api-v2 " + answer()));`)
	touchFuture(apiRouter, 5)
	time.Sleep(1100 * time.Millisecond) // past the per-node 1s throttle
	if rec := get("/api"); !strings.Contains(rec.Body.String(), "api-v2 service-v1") {
		t.Fatalf("subfolder router edit not hot-reloaded: %d %s", rec.Code, rec.Body.String())
	}

	// 2. Edit the IMPORTED module — the importing folder recompiles (Bytecode.Files is watched).
	servicePath := write("_core/service.kitwork.js", `export const answer = () => ("service-v2");`)
	touchFuture(servicePath, 10)
	time.Sleep(1100 * time.Millisecond)
	if rec := get("/api"); !strings.Contains(rec.Body.String(), "api-v2 service-v2") {
		t.Fatalf("imported module edit not hot-reloaded: %d %s", rec.Code, rec.Body.String())
	}

	// 3. CREATE a new folder — the parent's dir modtime changes, children rescan, route appears.
	if rec := get("/fresh"); rec.Code != 404 {
		t.Fatalf("fresh route should not exist yet, got %d", rec.Code)
	}
	write("fresh/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.get().handle((ctx) => ctx.text("fresh-alive"));`)
	time.Sleep(1100 * time.Millisecond)
	if rec := get("/fresh"); !strings.Contains(rec.Body.String(), "fresh-alive") {
		t.Fatalf("new folder not discovered: %d %s", rec.Code, rec.Body.String())
	}
}

// Control: with HotReload off (production), a compiled folder never re-stats or recompiles.
func TestTreeFolderHotReloadDisabled(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-tree-hot-off-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) string {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	write("router.kitwork.js", `import { router } from "kitwork";`)
	apiRouter := write("api/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+`router.get().handle((ctx) => ctx.text("v1"));`)

	tenant := NewTenant(tmp, "localhost") // HotReload stays false
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	get := func() string {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/api", nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec.Body.String()
	}
	if body := get(); !strings.Contains(body, "v1") {
		t.Fatalf("baseline: %s", body)
	}
	write("api/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+`router.get().handle((ctx) => ctx.text("v2"));`)
	ft := time.Now().Add(5 * time.Second)
	_ = os.Chtimes(apiRouter, ft, ft)
	time.Sleep(1100 * time.Millisecond)
	if body := get(); !strings.Contains(body, "v1") {
		t.Errorf("production must keep the compiled folder, got %s", body)
	}
}
