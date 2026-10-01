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
