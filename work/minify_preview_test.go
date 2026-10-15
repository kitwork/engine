package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MINIFY=true (work.Minify) minifies what production minifies while developing (14/10/2026): the head
// of a .ui page and the HTML of a template page come out as production writes them, and nothing else
// that developing means changes: a .ui page's warnings still reach the browser's console
// (warnings.<hash>.js), and a template expression that fails still says so in the page.
//
// CONTROL: the same sites while developing without MINIFY are written out.
func TestMinifyPreviewWhileDeveloping(t *testing.T) {
	for _, minify := range []bool{false, true} {
		saved := Minify
		Minify = minify
		t.Cleanup(func() { Minify = saved })

		// a .ui page with a warning (a pair of colours short of contrast)
		tenant := buildUISite(t, withRootRouter(t, sitePalette, map[string]string{"localhost/content.ui": lowContrastContent}))
		page := serveUI(t, tenant, "http://localhost/").Body.String()
		minified := strings.Contains(page, `<meta name=generator content=`)
		if minified != minify {
			t.Errorf("MINIFY %v: the .ui head is minified: %v\n%.900s", minify, minified, page)
		}
		if !strings.Contains(page, `data-kitwork-generated=warnings`) && !strings.Contains(page, `data-kitwork-generated="warnings"`) {
			t.Errorf("MINIFY %v: while developing the page's warnings still reach the console:\n%.900s", minify, page)
		}

		// a template page with a comment the minifier drops, and an expression that fails
		root := t.TempDir()
		for name, content := range map[string]string{
			"index.kitwork.html": "<main>\n  <!-- a comment -->\n  {{ @page }}\n</main>",
			"router.kitwork.js":  "import { router } from \"kitwork\";\nrouter.get((ctx) => ctx.bind({ item: { n: 3 } }));\n",
			"page.kitwork.html":  "<p>[{{ item.n() }}]</p>",
		} {
			full := filepath.Join(root, "acme", "localhost", filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		saved2 := AllowLocal
		AllowLocal = true
		template := NewTenant(root, "localhost")
		if err := template.Run(); err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		template.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		template.Close()
		AllowLocal = saved2
		body := recorder.Body.String()
		if dropped := !strings.Contains(body, "a comment"); dropped != minify {
			t.Errorf("MINIFY %v: the template page is minified (its comment dropped): %v\n%s", minify, dropped, body)
		}
		if !strings.Contains(body, "không phải hàm") {
			t.Errorf("MINIFY %v: while developing a failed expression still says so in the page:\n%s", minify, body)
		}
	}
}
