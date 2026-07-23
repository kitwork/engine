package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Collections end to end through a real tenant VM:
//   - collection.open("posts") resolves the blessed _collection/posts home (bare-name sugar)
//   - where/orderBy/limit chain runs on the frontmatter index (draft excluded, date-desc, paged)
//   - search() hits the FTS5 projection: Vietnamese WITHOUT diacritics finds accented documents
//   - one broken-frontmatter file is skipped, never taking down the listing
func TestCollectionQueryAndSearchE2E(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-coll-*")
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
	write("bytecode-runtime.md", "---\ntitle: Bytecode Runtime\nstatus: published\npublishedAt: \"2026-01-02\"\ntags: [go, vm]\n---\nMột embedded runtime chạy bytecode trên máy ảo của Nguyễn.")
	write("may-bay.md", "---\ntitle: Máy bay không người lái\nstatus: published\npublishedAt: \"2026-01-03\"\n---\nBài về máy bay và điều khiển từ xa.")
	write("draft-note.md", "---\ntitle: Nháp\nstatus: draft\npublishedAt: \"2026-01-04\"\n---\nChưa xuất bản.")
	write("old-post.md", "---\ntitle: Bài cũ\nstatus: published\npublishedAt: \"2026-01-01\"\n---\nNội dung cũ.")
	write("broken.md", "---\ntitle: [unclosed\n---\nfrontmatter hỏng — phải bị bỏ qua, không vỡ index.")

	router := `import { router, collection } from "kitwork";` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  const posts = collection.open("posts");` + "\n" + // bare name → _collection/posts
		`  const list = posts.where("status", "published").orderBy("publishedAt", "desc").limit(2).all();` + "\n" +
		`  const total = posts.where("status", "published").count();` + "\n" +
		`  const hits = posts.search("nguyen");` + "\n" + // no diacritics → must find "Nguyễn"
		`  const phrase = posts.search("may bay");` + "\n" +
		`  return ctx.json({ list: list, total: total, hits: hits, phrase: phrase });` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	body := rec.Body.String()

	// Chain: 3 published docs total, page of 2, newest first, draft + broken excluded.
	if !strings.Contains(body, `"total":3`) {
		t.Errorf("count: want total=3 (draft + broken excluded), body: %s", body)
	}
	// Scope order/limit checks to the list section — search hits elsewhere in the JSON also carry slugs.
	listPart := body[strings.Index(body, `"list":[`):]
	if end := strings.Index(listPart, `]`); end > 0 {
		listPart = listPart[:end]
	}
	iMayBay := strings.Index(listPart, `"slug":"may-bay"`)
	iRuntime := strings.Index(listPart, `"slug":"bytecode-runtime"`)
	if iMayBay == -1 || iRuntime == -1 || iMayBay > iRuntime {
		t.Errorf("order: want may-bay (2026-01-03) before bytecode-runtime (2026-01-02) in list: %s", listPart)
	}
	if strings.Contains(listPart, `"slug":"old-post"`) {
		t.Errorf("limit(2) leaked old-post into the page; list: %s", listPart)
	}
	if strings.Contains(body, `"slug":"draft-note"`) {
		t.Errorf("where(status=published) leaked the draft; body: %s", body)
	}

	// Search: diacritics-blind Vietnamese, snippet highlighted (JSON escapes <b> as <b>).
	if !strings.Contains(body, `"hits":[`) || !strings.Contains(body, "Bytecode Runtime") {
		t.Errorf("search 'nguyen' must find the doc containing 'Nguyễn'; body: %s", body)
	}
	if !strings.Contains(body, "<b>") && !strings.Contains(body, "\\u003cb\\u003e") {
		t.Errorf("search snippet must highlight the match with <b>; body: %s", body)
	}
	if !strings.Contains(body, "người lái") && !strings.Contains(body, "Máy bay") {
		t.Errorf("search 'may bay' must find 'Máy bay không người lái'; body: %s", body)
	}

	// The projection database landed in the tenant's .data (disposable, gitignored).
	if _, err := os.Stat(filepath.Join(dir, ".data", "collection.db")); err != nil {
		t.Errorf("FTS projection .data/collection.db missing: %v", err)
	}
}

// Search must survive a file EDIT (signature-gated resync): new content becomes findable, stale not.
func TestCollectionSearchResync(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-collsync-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "acme", "localhost")
	postsDir := filepath.Join(dir, "_collection", "notes")
	if err := os.MkdirAll(postsDir, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(postsDir, "one.md")
	os.WriteFile(file, []byte("---\ntitle: One\n---\nfirst version alpha"), 0644)

	router := `import { router, collection } from "kitwork";` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  const notes = collection.open("notes");` + "\n" +
		`  return ctx.json({ alpha: notes.search("alpha"), beta: notes.search("beta") });` + "\n" +
		`});`
	os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	serve := func() string {
		rec := httptest.NewRecorder()
		tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		return rec.Body.String()
	}

	body := serve()
	if !strings.Contains(body, `"slug":"one"`) || strings.Contains(body, `"beta":[{`) {
		t.Fatalf("v1: want alpha hit and no beta hit; body: %s", body)
	}

	// Edit the file — beta replaces alpha. The dir signature changes → resync on next search.
	os.WriteFile(file, []byte("---\ntitle: One\n---\nsecond version beta"), 0644)
	body = serve()
	if !strings.Contains(body, `"beta":[{`) {
		t.Errorf("after edit: 'beta' must be findable (resync failed); body: %s", body)
	}
	if strings.Contains(body, `"alpha":[{`) {
		t.Errorf("after edit: stale 'alpha' still matches (old row not replaced); body: %s", body)
	}
}
