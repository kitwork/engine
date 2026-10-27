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

// A prepared Tenant is one generation and never mutates in place. Production
// hot reload is owned by core.Engine, which replaces the whole generation.
func TestTreeGenerationDoesNotReloadInPlace(t *testing.T) {
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

	write("router.work", `import * as router from "kitwork/router";`)
	write(".shared/service.work", `export const answer = () => ("service-v1");`)
	apiRouter := write("api/router.work",
		`import * as router from "kitwork/router";`+"\n"+
			`import { answer } from "app/.shared/service.work";`+"\n"+
			`router.get((context) => context.text("api-v1 " + answer()));`)

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

	// Editing a subfolder router does not mutate this generation.
	write("api/router.work",
		`import * as router from "kitwork/router";`+"\n"+
			`import { answer } from "app/.shared/service.work";`+"\n"+
			`router.get((context) => context.text("api-v2 " + answer()));`)
	touchFuture(apiRouter, 5)
	time.Sleep(1100 * time.Millisecond) // past the per-node 1s throttle
	if rec := get("/api"); !strings.Contains(rec.Body.String(), "api-v1 service-v1") {
		t.Fatalf("subfolder edit mutated the active generation: %d %s", rec.Code, rec.Body.String())
	}

	// Editing an imported module also leaves this generation untouched.
	servicePath := write(".shared/service.work", `export const answer = () => ("service-v2");`)
	touchFuture(servicePath, 10)
	time.Sleep(1100 * time.Millisecond)
	if rec := get("/api"); !strings.Contains(rec.Body.String(), "api-v1 service-v1") {
		t.Fatalf("import edit mutated the active generation: %d %s", rec.Code, rec.Body.String())
	}

	// A new folder belongs to the next generation, not this route graph.
	if rec := get("/fresh"); rec.Code != 404 {
		t.Fatalf("fresh route should not exist yet, got %d", rec.Code)
	}
	write("fresh/router.work",
		`import * as router from "kitwork/router";`+"\n"+
			`router.get((context) => context.text("fresh-alive"));`)
	time.Sleep(1100 * time.Millisecond)
	if rec := get("/fresh"); rec.Code != 404 {
		t.Fatalf("new folder entered the active generation: %d %s", rec.Code, rec.Body.String())
	}
}

// A generation remains immutable when HotReload is left at its default too.
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

	write("router.work", `import * as router from "kitwork/router";`)
	apiRouter := write("api/router.work",
		`import * as router from "kitwork/router";`+"\n"+`router.get((context) => context.text("v1"));`)

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
	write("api/router.work",
		`import * as router from "kitwork/router";`+"\n"+`router.get((context) => context.text("v2"));`)
	ft := time.Now().Add(5 * time.Second)
	_ = os.Chtimes(apiRouter, ft, ft)
	time.Sleep(1100 * time.Millisecond)
	if body := get(); !strings.Contains(body, "v1") {
		t.Errorf("production must keep the compiled folder, got %s", body)
	}
}
