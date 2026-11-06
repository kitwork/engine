package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/database/fulltext"
	"github.com/kitwork/engine/work"
)

// The index matches reads (KITWORK_DATABASE.md mục 2) is the app's, not a generation's: the first statement opens
// it, a hot reload of the site reads the same index and a row the new generation writes is found in it, and it
// closes when the engine closes, so its files can be moved. It replaces the test of the host's search manager that
// went through collection.open().search(), removed on 01/11/2026.
//
// CONTROL (01/11/2026): with the app's files never released when it closes, "the index is still open after the
// engine closed"; with every Open building the index anew, "the reload opened another index".
func TestMatchesIndexOutlivesAReloadAndClosesWithTheEngine(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "acme")
	site := filepath.Join(app, "localhost")
	schema := `import { table, id, string } from "kitwork/schema";
import { connection } from "kitwork/database";

const posts = table({ id: id(), title: string().searchable() });

export const blog = connection().tables({ posts });
`
	router := `import * as router from "kitwork/router";
import { blog } from "app/.shared/schema/blog.work";

router.post((context) => {
  const title = context.request.form("title");
  return context.json(blog.insert((posts) => ({ title })));
});

router.get((context) => {
  const q = context.request.query("q");
  return context.json(blog.list((posts) => posts.matches(q)));
});
`
	for path, source := range map[string]string{
		filepath.Join(app, ".shared", "schema", "blog.work"): schema,
		filepath.Join(site, "router.work"):                   router,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// kitwork migrate first: a statement never makes its table (KITWORK_DATABASE.md "Database là gốc", slice 4)
	plans, release, err := work.PlanAppMigrations(root, "acme", work.RootLayoutMultiTenant, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if err := plan.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	release()
	engine := New(root, 0, true, "")
	closed := false
	t.Cleanup(func() {
		if !closed {
			engine.Close()
		}
	})
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}
	insert := func(title string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "http://localhost/", strings.NewReader(url.Values{"title": {title}}.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("insert %s = %d %q", title, response.Code, response.Body.String())
		}
	}
	search := func(q string) string {
		t.Helper()
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost/?q="+url.QueryEscape(q), nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET ?q=%s = %d %q", q, response.Code, response.Body.String())
		}
		return response.Body.String()
	}

	file := filepath.Join(app, ".database", "application", "kitwork.db")
	insert("Cà phê sữa đá")
	if body := search("ca phe"); !strings.Contains(body, `"title":"Cà phê sữa đá"`) {
		t.Fatalf("q=ca phe = %s", body)
	}
	index := fulltext.Shared().Lookup(file, "posts")
	if index == nil || !index.Tracked() {
		t.Fatalf("the index of posts after the first matches = %v", index)
	}

	loaded := engine.cache["localhost"].current()
	if err := os.WriteFile(filepath.Join(site, "router.work"), []byte(router+"\n// reload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cached := engine.cache["localhost"]
	cached.mu.Lock()
	cached.lastChecked = time.Time{}
	cached.mu.Unlock()
	insert("Phở bò")
	if body := search("pho bo"); !strings.Contains(body, `"title":"Phở bò"`) || strings.Contains(body, "Cà phê") {
		t.Fatalf("q=pho bo after the reload = %s", body)
	}
	if reloaded := engine.cache["localhost"].current(); reloaded == loaded {
		t.Fatal("the edit of router.work did not build the site anew")
	}
	if again := fulltext.Shared().Lookup(file, "posts"); again != index {
		t.Fatalf("the reload opened another index: %p, was %p", again, index)
	}

	engine.Close()
	closed = true
	if open := fulltext.Shared().Lookup(file, "posts"); open != nil {
		t.Fatal("the index is still open after the engine closed")
	}
}
