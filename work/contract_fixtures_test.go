package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealAppHTTPContractFixture(t *testing.T) {
	AllowLocal = true
	defer func() { AllowLocal = false }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":"hello kitwork"}`))
	}))
	defer ts.Close()

	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import { router, http } from "kitwork";

router.get((ctx) => {
    const res = http.get("` + ts.URL + `").cache("5m").json();
    return ctx.json({ message: res.message });
});
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello kitwork") {
		t.Fatalf("expected 'hello kitwork' in response body, got: %s", rec.Body.String())
	}
}

func TestRealAppDatabaseContractFixture(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import { router, sqlite } from "kitwork";

router.get((ctx) => {
    sqlite.exec("CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT, price INTEGER)");
    sqlite.table("items").create({ name: "laptop", price: 1200 });
    const item = sqlite.table("items").where("name", "laptop").first();
    return ctx.json({ name: item.name });
});
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "laptop") {
		t.Fatalf("expected 'laptop' in response body, got: %s", rec.Body.String())
	}
}

func TestRealAppCollectionContractFixture(t *testing.T) {
	tmp := t.TempDir()
	appDir := filepath.Join(tmp, "acme", "localhost")

	postsDir := filepath.Join(appDir, "_collection", "posts")
	if err := os.MkdirAll(postsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(postsDir, "welcome.md"), []byte("---\ntitle: Welcome\nstatus: published\npublishedAt: \"2026-01-01\"\n---\nHello World"), 0644); err != nil {
		t.Fatal(err)
	}

	routerScript := `
import { router, collection } from "kitwork";

router.get((ctx) => {
    const posts = collection.open("posts").all();
    return ctx.json({ list: posts });
});
`
	if err := os.WriteFile(filepath.Join(appDir, "router.kitwork.js"), []byte(routerScript), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "welcome") {
		t.Fatalf("expected 'welcome' in response body, got: %s", rec.Body.String())
	}
}
