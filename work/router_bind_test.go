package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Validates the exact pattern huynhnhanquoc.com/notes/{slug} uses: a dynamic folder whose router
// looks a record up in an object literal by the bound slug (bracket indexing on a string key),
// then binds it into the folder's content.ui via ctx.view({ item }) — or 404s for an unknown slug.
func TestTreeDynamicLookupAndViewBinding(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-treebind-*")
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

	write(UIRouterFileName, `import * as router from "kitwork/router";`)
	write("view.ui", `<view><content /></view>`)
	write("notfound.ui", `<content><text>404</text></content>`)

	write("notes/{slug}/content.ui", `<content><text heading="1">{{ item.title }}</text></content>`)
	write("notes/{slug}/"+UIRouterFileName,
		`import * as router from "kitwork/router";`+"\n"+
			`const notes = { 'alpha': { title: 'Alpha note' }, 'beta': { title: 'Beta note' } };`+"\n"+
			`router.get((ctx) => {`+"\n"+
			`  const note = notes[ctx.params('slug')];`+"\n"+
			`  return note ? ctx.view({ item: note }) : ctx.status(404).text('no such note');`+"\n"+
			`});`)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		rec := httptest.NewRecorder()
		tenant.Serve(rec, req)
		return rec
	}

	if rec := get("/notes/alpha"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Alpha note") {
		t.Fatalf("/notes/alpha: code=%d body=%q, want 200 containing 'Alpha note'", rec.Code, rec.Body.String())
	}
	if rec := get("/notes/beta"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Beta note") {
		t.Fatalf("/notes/beta: code=%d body=%q, want 200 containing 'Beta note'", rec.Code, rec.Body.String())
	}
	if rec := get("/notes/unknown"); rec.Code != 404 {
		t.Fatalf("/notes/unknown: code=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
}
