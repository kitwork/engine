package work

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTestSite writes the smallest filesystem-routed site — a root router, a shell, one page and a
// notfound — and returns its tenant (not yet run) and the site directory.
func writeTestSite(t *testing.T, router, page string) (*Tenant, string) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	writeSiteFile(t, directory, "router.kitwork.js", `import { router } from "kitwork";`+"\n"+router)
	writeSiteFile(t, directory, "index.kitwork.html", `<!doctype html><html><head><title>Test</title></head><body {{ if section == "docs" }}data-ready="yes" {{ end }}>{{ @page }}</body></html>`)
	writeSiteFile(t, directory, "page.kitwork.html", page)
	writeSiteFile(t, directory, "notfound.kitwork.html", `<main>Not found</main>`)

	tenant := NewTenant(root, "localhost")
	t.Cleanup(tenant.Close)
	return tenant, directory
}

// identitySite writes apps/<identity>/… and runs its domain, localhost: a path in files is relative to
// the IDENTITY, so "localhost/page.kitwork.html" is the domain's. (componentFixture in
// sitecomponents_test.go until a site's own _components went, 26/10/2026.)
func identitySite(t *testing.T, files map[string]string) *Tenant {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "acme", "localhost"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		writeSiteFile(t, filepath.Join(root, "acme"), name, body)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tenant.Close() })
	return tenant
}

// templatePage is the smallest template site that renders one page through the render pipeline, for
// identitySite.
func templatePage(markup string) map[string]string {
	return map[string]string{
		"localhost/filesystem.kitwork": "",
		"localhost/index.kitwork.html": `<!doctype html><html><head></head><body>{{ @page }}</body></html>`,
		"localhost/page.kitwork.html":  markup,
		"localhost/router.kitwork.js":  `import { router } from "kitwork";`,
	}
}

func writeSiteFile(t *testing.T, directory, relative, source string) {
	t.Helper()
	filename := filepath.Join(directory, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}
