package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newTestTenant builds a Tenant whose views/ folder is a fresh temp dir, bypassing path resolution.
func newTestTenant(t *testing.T) (*Tenant, string) {
	t.Helper()
	base := t.TempDir()
	viewsDir := filepath.Join(base, "views")
	if err := os.MkdirAll(viewsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tenant := &Tenant{
		config: &Config{root: base, base: base},
		entity: &Entity{Domain: "test.local"},
	}
	return tenant, viewsDir
}

func TestServeViewStaticTxt(t *testing.T) {
	tenant, viewsDir := newTestTenant(t)
	if err := os.WriteFile(filepath.Join(viewsDir, "robots.txt"), []byte("User-agent: *\nDisallow:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A source file that must NEVER be auto-served.
	if err := os.WriteFile(filepath.Join(viewsDir, "app.kitwork.js"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		served bool
	}{
		{"txt served", http.MethodGet, "/robots.txt", true},
		{"head served", http.MethodHead, "/robots.txt", true},
		{"missing txt", http.MethodGet, "/nope.txt", false},
		{"non-txt ignored", http.MethodGet, "/app.kitwork.js", false},
		{"post ignored", http.MethodPost, "/robots.txt", false},
		{"traversal blocked", http.MethodGet, "/../app.kitwork.js.txt", false},
		{"encoded traversal blocked", http.MethodGet, "/..%2f..%2fsecret.txt", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "http://test.local"+c.path, nil)
			rec := httptest.NewRecorder()
			got := tenant.serveViewStatic(rec, req)
			if got != c.served {
				t.Fatalf("serveViewStatic=%v, want %v (status=%d)", got, c.served, rec.Code)
			}
			if c.served {
				if ct := rec.Header().Get("Content-Type"); ct == "" {
					t.Errorf("expected a Content-Type for served file, got none")
				}
			}
		})
	}
}

// TestServeViewStaticTraversalEscape directly exercises a path that resolves outside views/.
func TestServeViewStaticTraversalEscape(t *testing.T) {
	tenant, _ := newTestTenant(t)
	// Plant a sensitive file one level ABOVE views/ and try to reach it.
	if err := os.WriteFile(filepath.Join(tenant.config.base, "outside.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://test.local/../outside.txt", nil)
	rec := httptest.NewRecorder()
	if tenant.serveViewStatic(rec, req) {
		t.Fatal("traversal to a file outside views/ was served — security hole")
	}
}
