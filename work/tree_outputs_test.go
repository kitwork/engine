package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeSemanticOutputsAndTypedResponse(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-tree-outputs-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	router := `import { router } from "kitwork";
router.rss({
    path: "/feed.xml",
    title: "Test & Feed",
    description: "Structured output",
    link: "https://example.test",
    items: () => [{
        title: "A & B",
        description: "One < two",
        link: "/articles/a",
        published: "2026-07-12"
    }]
}).cache("1h");
router.rss({ path: "/broken.xml", title: "Broken", items: [] });
router.sitemap(() => [
    { loc: "/", lastmod: "2026-07-12" },
    { loc: "/concepts/runtime", lastmod: "2026-07-11" },
    { loc: "/concepts/runtime" }
]).cache("1h");
router.get((ctx) => ctx.type("text/csv; charset=utf-8").send("name,value\nkitwork,1"));
`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notfound.kitwork.html"), []byte("not found"), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://localhost"+path, nil)
		for name, data := range headers {
			req.Header.Set(name, data)
		}
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}
	get := func(path string) *httptest.ResponseRecorder {
		return request(http.MethodGet, path, nil)
	}

	rss := get("/feed.xml")
	if rss.Code != 200 || !strings.HasPrefix(rss.Header().Get("Content-Type"), "application/rss+xml") {
		t.Fatalf("rss response: status=%d type=%q body=%q", rss.Code, rss.Header().Get("Content-Type"), rss.Body.String())
	}
	for _, want := range []string{"<title>Test &amp; Feed</title>", "<title>A &amp; B</title>", "http://localhost/articles/a"} {
		if !strings.Contains(rss.Body.String(), want) {
			t.Fatalf("rss body missing %q: %s", want, rss.Body.String())
		}
	}
	etag := rss.Header().Get("ETag")
	lastModified := rss.Header().Get("Last-Modified")
	if etag == "" || lastModified == "" {
		t.Fatalf("rss validators missing: etag=%q last-modified=%q", etag, lastModified)
	}
	if hit := get("/feed.xml"); hit.Header().Get("X-Kitwork-Cache") != "hit" {
		t.Fatalf("second rss request should use method cache, headers=%v", hit.Header())
	}
	conditional := request(http.MethodGet, "/feed.xml", map[string]string{"If-None-Match": etag})
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("conditional rss: status=%d body=%q", conditional.Code, conditional.Body.String())
	}
	head := request(http.MethodHead, "/feed.xml", nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("ETag") != etag {
		t.Fatalf("rss HEAD: status=%d etag=%q body=%q", head.Code, head.Header().Get("ETag"), head.Body.String())
	}
	broken := get("/broken.xml")
	if broken.Code != http.StatusInternalServerError || !strings.Contains(broken.Body.String(), "requires title, description, and link") {
		t.Fatalf("invalid rss should fail clearly: status=%d body=%q", broken.Code, broken.Body.String())
	}

	sitemap := get("/sitemap.xml")
	if sitemap.Code != 200 || !strings.HasPrefix(sitemap.Header().Get("Content-Type"), "application/xml") {
		t.Fatalf("sitemap response: status=%d type=%q", sitemap.Code, sitemap.Header().Get("Content-Type"))
	}
	if count := strings.Count(sitemap.Body.String(), "<loc>http://localhost/concepts/runtime</loc>"); count != 1 {
		t.Fatalf("sitemap should deduplicate locations, count=%d body=%s", count, sitemap.Body.String())
	}

	csv := get("/")
	if csv.Code != 200 || csv.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("typed response: status=%d type=%q body=%q", csv.Code, csv.Header().Get("Content-Type"), csv.Body.String())
	}
}
