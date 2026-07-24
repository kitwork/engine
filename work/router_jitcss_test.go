package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// router.jitcss(config) must feed the render's JIT-CSS engine: a custom brand color from
// theme.extend.colors, and a custom dark: parent selector from darkMode.
func TestJitcssConfigApplied(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-jitcss-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("filesystem.kitwork", "")
	write("router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.jitcss({`+"\n"+
			`  darkMode: ['class', '[data-theme="dark"]'],`+"\n"+
			`  theme: { extend: { colors: { brand: { DEFAULT: '#e8173a' } } } },`+"\n"+
			`});`)
	// A page using a brand utility AND a dark: variant — both must reflect the config.
	write("index.kitwork.html", `<!doctype html><head></head><body class="jit-brand-marker bg-brand dark:text-brand">{{ @page }}</body>`)
	write("page.kitwork.html", `<main>hi</main>`)

	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tn.Serve(rec, req)
	body := rec.Body.String()

	// The root router.jitcss() runs during the first request's lazy compile.
	if tn.jitcssConfig == nil {
		t.Fatal("router.jitcss() did not install tenant.jitcssConfig")
	}

	// The custom brand color #e8173a (not the default #f82244) must drive .bg-brand.
	if !strings.Contains(body, "#e8173a") || strings.Contains(body, "#f82244") {
		t.Errorf("custom brand color not applied (expected #e8173a, not default #f82244):\n%.400q", body)
	}
	// The dark: variant must scope under the configured selector (minified: quotes stripped).
	if !strings.Contains(body, "[data-theme=dark]") || strings.Contains(body, ".dark .dark") {
		t.Errorf("custom dark selector [data-theme=dark] not applied:\n%.400q", body)
	}
}
