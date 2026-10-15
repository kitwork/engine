package work

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// The colour gallery of the kitwork example (kitwork.localhost/colors) is served by the engine as
// written: 200 while developing, no warning, every class's rule in the stylesheets it links, and a
// swatch for every gradient of the standard set, every page backdrop, every gradient of the WebGradients
// collection built in, every named colour the engine has, and each accent role made stronger (+15) and
// lighter (-85, -95) beside its .on. The page is generated once from the engine's lists, so a name the
// engine renames, drops or adds makes it stale; before the navigation linked it, the crawl of
// TestExampleSitesCarryNoTailwindWord did not read it, and a stale swatch was a 500 every test missed
// (review of 12/10/2026). The gallery is read here whether a link reaches it or not.
func TestExampleColorsPage(t *testing.T) {
	if _, err := os.Stat(filepath.Join(examplesRoot, "kitwork", "kitwork.localhost", "colors", "content.ui")); err != nil {
		t.Skipf("no colour gallery beside the engine (%v)", err)
	}
	root := copyExamples(t)
	saved := AllowLocal
	AllowLocal = true // developing: a page with something to tell links its warnings
	t.Cleanup(func() { AllowLocal = saved })
	const host = "kitwork.localhost"
	tenant := NewTenant(root, host)
	if err := tenant.Run(); err != nil {
		t.Fatalf("%s: %v", host, err)
	}
	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/colors", nil))
	page := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("/colors = %d %q:\n%.600s", rec.Code, rec.Header().Get("Content-Type"), page)
	}
	if link := warningsScriptRe.FindStringSubmatch(page); link != nil {
		r := httptest.NewRecorder()
		tenant.Serve(r, httptest.NewRequest(http.MethodGet, "http://"+host+link[1], nil))
		t.Errorf("/colors links warnings:\n%.2000s", r.Body.String())
	}
	carried := map[string]bool{}
	for _, m := range classAttributeRe.FindAllStringSubmatch(page, -1) {
		for _, class := range strings.Fields(m[1]) {
			carried[class] = true
		}
	}
	for _, name := range stylesheet.StandardGradients() {
		if !carried["background-"+name] {
			t.Errorf("/colors has no swatch of the gradient %s (background-%s)", name, name)
		}
	}
	for _, name := range stylesheet.Backdrops() {
		if !carried["background-"+name] {
			t.Errorf("/colors has no swatch of the page backdrop %s (background-%s)", name, name)
		}
	}
	for _, name := range stylesheet.WebGradients() {
		if !carried["background-"+name] {
			t.Errorf("/colors has no swatch of the WebGradients gradient %s (background-%s)", name, name)
		}
	}
	for _, name := range stylesheet.NamedColours() {
		if !carried["background-"+name] {
			t.Errorf("/colors has no swatch of the named colour %s (background-%s)", name, name)
		}
	}
	// the roles: each accent with the steps a page writes most, in the operators of 13/10/2026 (the
	// solid fill, stronger +15, lighter -85 and -95, and the label .on that an accent still grows)
	for _, accent := range []string{"brand", "success", "warning", "danger", "info"} {
		for _, step := range []string{"", "+15", "-85", "-95", ".on"} {
			if !carried["background-"+accent+step] {
				t.Errorf("/colors thiếu ô màu %s%s của vai trò (class background-%s%s)", accent, step, accent, step)
			}
		}
	}
	css := ""
	for _, sheet := range regexp.MustCompile(`href="(/_kitwork/[a-z]+\.[0-9a-f]{16}\.css)"`).FindAllStringSubmatch(page, -1) {
		r := httptest.NewRecorder()
		tenant.Serve(r, httptest.NewRequest(http.MethodGet, "http://"+host+sheet[1], nil))
		css += r.Body.String()
	}
	for _, block := range inlineStylesheetRe.FindAllStringSubmatch(page, -1) {
		css += block[2] // router.bundle: the page's own CSS is written in the page
	}
	for class := range carried {
		if strings.HasPrefix(class, "language-") {
			continue // a code element's language: HTML's own name for it, no rule styles it
		}
		selector := "." + strings.NewReplacer(":", `\:`, "+", `\+`, ",", `\,`, ".", `\.`, "/", `\/`, "%", `\%`).Replace(class)
		if !strings.Contains(css, selector) {
			t.Errorf("/colors: class %s has no rule in the stylesheets the page links", class)
		}
	}
}

// copyExamples copies the examples beside the engine into a folder of the test, as a tenant boots them:
// a tenant writes what it keeps (.data, caches) into its folders, never into the repository's.
func copyExamples(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir(examplesRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(examplesRoot, path)
		if d.IsDir() {
			if d.Name() == ".data" || d.Name() == ".persist" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(root, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}
