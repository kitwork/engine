package work

import (
	"os"
	"path/filepath"
	"testing"
)

// identitySite writes apps/<identity>/… and runs its domain, localhost: a path in files is relative to
// the IDENTITY, so "localhost/content.ui" is the domain's. (componentFixture in
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

// uiPage is the smallest Kitwork UI site of one page, for identitySite: the root router.work (its
// import line, then the lines given), the frame and the page. (writeTestSite and templatePage wrote a
// template site until the template pages went, 27/10/2026.)
func uiPage(router, content string) map[string]string {
	return map[string]string{
		"localhost/router.work": `import * as router from "kitwork/router";` + "\n" + router,
		"localhost/view.ui":     `<view><content /></view>`,
		"localhost/content.ui":  content,
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
