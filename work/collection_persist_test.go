package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The collection FTS projection has twice shipped bugs invisible to unit tests, both hiding in the
// .persist() round-trip: a persisted index snapshot that dropped File.signature once left every live
// site with an EMPTY search index. huynhnhanquoc.com opens every collection with .persist("30d") and
// searches through it, so the swap from FTS5 to the hand-written BM25 projection must be proven on that
// exact path — search must return hits when the collection is persisted, AND survive a process restart
// that reloads the persisted snapshot from disk (a second Tenant over the same directory).
func TestCollectionSearchSurvivesPersist(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-collpersist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "acme", "localhost")
	postsDir := filepath.Join(dir, "_collection", "posts")
	if err := os.MkdirAll(postsDir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(postsDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("runtime.md", "---\ntitle: Bytecode Runtime\n---\nMột runtime chạy bytecode trên máy ảo của Nguyễn.")
	write("maybay.md", "---\ntitle: Máy bay\n---\nBài về máy bay và điều khiển từ xa.")

	// The site pattern: open WITH .persist(), then search through it.
	router := `import { router, collection } from "kitwork";` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  const posts = collection.open("posts").persist("30d");` + "\n" +
		`  return ctx.json({ hits: posts.search("nguyen") });` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	serve := func() string {
		tenant := NewTenant(tmp, "localhost")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		defer tenant.Close()
		rec := httptest.NewRecorder()
		tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		return rec.Body.String()
	}

	// First boot: builds the projection and writes the persisted index snapshot.
	first := serve()
	if !strings.Contains(first, `"slug":"runtime"`) {
		t.Fatalf("persisted search returned no hit for 'nguyen' → Nguyễn on first boot; body: %s", first)
	}

	// Second boot over the SAME directory: the index is reloaded from the persisted snapshot and the
	// FTS projection from .data/collection.db. If persistence dropped what the sync needs, the index
	// would come back empty — the historical failure. Search must still find the document.
	second := serve()
	if !strings.Contains(second, `"slug":"runtime"`) {
		t.Fatalf("persisted search went EMPTY after restart (reloaded snapshot); body: %s", second)
	}
}
