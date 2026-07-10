package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTreeCollectionNativeCapability(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	write := func(rel, content string) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("router.kitwork.js", `import { router, collection } from "kitwork";
const concepts = collection.open("_data/concepts").cache("30m").persist("1h");
router.get((ctx) => {
    const items = concepts.index();
    const document = concepts.read("runtime");
    return ctx.json({
        count: items.length,
        title: document.meta.title,
        status: document.meta.status,
        html: document.html,
        toc: document.toc.length,
        folder: concepts.path
    });
});`)
	write("_data/concepts/runtime.md", "\xef\xbb\xbf---\ntitle: Runtime\nstatus: public\ncustom:\n  owner: hnq\n---\n# Runtime\n\n## First idea\n\nversion one\n")
	write("_data/concepts/draft.md", "---\ntitle: Draft\nstatus: draft\n---\n# Draft\n")

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	get := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
		}
		return body
	}

	first := get()
	if first["count"] != float64(2) || first["title"] != "Runtime" || first["status"] != "public" {
		t.Fatalf("unexpected collection response: %#v", first)
	}
	if first["toc"] != float64(1) || first["folder"] != "_data/concepts" {
		t.Fatalf("toc/path unavailable through VM: %#v", first)
	}
	if html, _ := first["html"].(string); !strings.Contains(html, "version one") || strings.Contains(html, "<h1") {
		t.Fatalf("unexpected rendered markdown: %q", html)
	}

	persisted := filepath.Join(dir, ".persist", "collection")
	if entries, err := os.ReadDir(persisted); err != nil || len(entries) == 0 {
		t.Fatalf("collection snapshots missing under %s: entries=%d err=%v", persisted, len(entries), err)
	}

	// A changed source signature must win over both RAM and disk snapshots immediately.
	time.Sleep(2 * time.Millisecond)
	write("_data/concepts/runtime.md", "---\ntitle: Runtime Two\nstatus: public\n---\n# Runtime Two\n\n## Second idea\n\nversion two with a different size\n")
	second := get()
	if second["title"] != "Runtime Two" || !strings.Contains(second["html"].(string), "version two") {
		t.Fatalf("source edit hidden by collection cache: %#v", second)
	}
}
