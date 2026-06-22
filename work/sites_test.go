package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSiteApp(t *testing.T, root, domain string) string {
	t.Helper()
	dir := filepath.Join(root, SitesDirName, domain)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, AppFileName), []byte("export default {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDiscoverSites(t *testing.T) {
	root := t.TempDir()
	writeSiteApp(t, root, "domain1.vn")
	writeSiteApp(t, root, "domain2.io")
	// A folder without the app file must be ignored.
	if err := os.MkdirAll(filepath.Join(root, SitesDirName, "empty.ai"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := DiscoverSites(root)
	found := map[string]bool{}
	for _, d := range got {
		found[d] = true
	}
	if !found["domain1.vn"] || !found["domain2.io"] {
		t.Fatalf("expected domain1.vn and domain2.io, got %v", got)
	}
	if found["empty.ai"] {
		t.Errorf("folder without app file should not be a site: %v", got)
	}
}

func TestDiscoverSitesNoSitesDir(t *testing.T) {
	if got := DiscoverSites(t.TempDir()); got != nil {
		t.Errorf("expected nil for missing sites/ dir, got %v", got)
	}
	if got := DiscoverSites("."); got != nil {
		t.Errorf("standalone root should yield no sites, got %v", got)
	}
}

func TestResolvePrefersSites(t *testing.T) {
	root := t.TempDir()
	sitePath := writeSiteApp(t, root, "example.vn")

	// Also create a flat root/<domain> folder to prove sites/ wins.
	flat := filepath.Join(root, "example.vn")
	if err := os.MkdirAll(flat, 0o755); err != nil {
		t.Fatal(err)
	}

	tenant := &Tenant{
		config: &Config{root: root},
		entity: &Entity{Domain: "example.vn"}, // no Identity → single-tenant branch
	}
	if base := tenant.resolve(); base != sitePath {
		t.Fatalf("resolve() = %q, want sites path %q", base, sitePath)
	}
}

func TestResolveFallsBackToFlat(t *testing.T) {
	root := t.TempDir()
	flat := filepath.Join(root, "plain.vn")
	if err := os.MkdirAll(flat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flat, AppFileName), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant := &Tenant{
		config: &Config{root: root},
		entity: &Entity{Domain: "plain.vn"},
	}
	if base := tenant.resolve(); base != flat {
		t.Fatalf("resolve() = %q, want flat path %q", base, flat)
	}
}

// TestSitesEndToEnd compiles a real single-tenant site under <root>/sites/<domain>/ and serves
// HTTP requests through Tenant.Serve, proving the full path: sites/ resolution + a views/*.txt
// auto-served as a static file that WINS over a router.get("/*") catch-all, while a specific
// route and the catch-all itself still behave normally.
func TestSitesEndToEnd(t *testing.T) {
	root := t.TempDir()
	siteDir := filepath.Join(root, SitesDirName, "demo.local")
	viewsDir := filepath.Join(siteDir, "views")
	if err := os.MkdirAll(viewsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	app := `import { router } from 'kitwork';
router.get("/page").handle((request, response) => response.text("PAGE"));
router.get("/*").handle((request, response) => response.text("CATCHALL"));
`
	if err := os.WriteFile(filepath.Join(siteDir, AppFileName), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(viewsDir, "robots.txt"), []byte("ROBOTS-OK"), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(root, "demo.local")
	if err := tenant.Run(); err != nil {
		t.Fatalf("tenant.Run() failed (sites/ resolution broken?): %v", err)
	}

	serve := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "http://demo.local"+path, nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}

	// 1. A real views/robots.txt is auto-served even though "/*" would otherwise catch it.
	if code, body := serve("/robots.txt"); code != http.StatusOK || body != "ROBOTS-OK" {
		t.Errorf("/robots.txt: got code=%d body=%q, want 200 ROBOTS-OK (txt should win over catch-all)", code, body)
	}
	// 2. A .txt with no file falls through to the catch-all handler.
	if code, body := serve("/missing.txt"); code != http.StatusOK || body != "CATCHALL" {
		t.Errorf("/missing.txt: got code=%d body=%q, want 200 CATCHALL", code, body)
	}
	// 3. A specific explicit route is unaffected.
	if code, body := serve("/page"); code != http.StatusOK || body != "PAGE" {
		t.Errorf("/page: got code=%d body=%q, want 200 PAGE", code, body)
	}
	// 4. A normal non-txt path still hits the catch-all.
	if code, body := serve("/whatever"); code != http.StatusOK || body != "CATCHALL" {
		t.Errorf("/whatever: got code=%d body=%q, want 200 CATCHALL", code, body)
	}
}
