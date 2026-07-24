package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Locks two behaviours: a {slug[string]} typed segment matches any non-numeric segment (not just
// digits), and the (request, response) => response.view({...}) handler style renders against the
// resolved folder in tree mode (the response.view bridge).
func TestTreeTypedSegmentsAndResponseView(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-typed-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("filesystem.kitwork", "")
	write("router.kitwork.js", `import { router } from "kitwork";`)
	write("index.kitwork.html", `<!doctype html><body>{{ @page }}</body>`)
	write("notfound.kitwork.html", `<main>404</main>`)

	// {slug[string]} — matches any segment; handler uses the response.view({...}) style.
	write("blog/{slug[string]}/page.kitwork.html", `<article>slug: {{ slug }}</article>`)
	write("blog/{slug[string]}/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.get((request, response) => response.view({ slug: request.params("slug") }));`)

	// {n[number]} — matches only digits.
	write("num/{n[number]}/router.kitwork.js",
		`import { router } from "kitwork";`+"\n"+
			`router.get((ctx) => ctx.json({ n: ctx.params("n") }));`)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	hit := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec.Code, rec.Body.String()
	}

	if code, body := hit("/blog/hello-world"); code != 200 || !strings.Contains(body, "slug: hello-world") {
		t.Fatalf("/blog/hello-world: code=%d body=%q — want 200 with 'slug: hello-world' ({slug[string]} + response.view)", code, body)
	}
	if code, body := hit("/num/42"); code != 200 || !strings.Contains(body, `"n":"42"`) {
		t.Fatalf("/num/42: code=%d body=%q — want 200 with n=42", code, body)
	}
	if code, _ := hit("/num/abc"); code != 404 {
		t.Fatalf("/num/abc: code=%d — want 404 (number type rejects non-digits)", code)
	}
}
