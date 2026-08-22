package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/search"
)

func TestEngineOwnsDatabaseSearchManager(t *testing.T) {
	root := t.TempDir()
	siteDirectory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(siteDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { sqlite, text } = database;
const db = sqlite("shop.db", {
  products: { id: text().key(), title: text().searchable() }
});
router.get((ctx) => {
  db.products.create({ id: "p1", title: "Kitwork search" });
  return ctx.json(db.products.search("kitwork").list());
});`
	reloadedRouter := `import { router, database } from "kitwork";
const { sqlite, text } = database;
const db = sqlite("shop.db", {
  products: { id: text().key(), title: text().searchable() }
});
router.get((ctx) => ctx.json(db.products.search("kitwork").list()));`
	if err := os.WriteFile(filepath.Join(siteDirectory, "router.kitwork.js"), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(root, 0, true, "")
	if err := engine.SetSearchManagerOptions(search.ManagerOptions{MaxOpenIndexes: 2}); err != nil {
		t.Fatal(err)
	}
	manager := engine.searchManager
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"p1"`) {
		t.Fatalf("search response = %d %q", response.Code, response.Body.String())
	}
	if manager == nil || engine.SearchStats().OpenIndexes != 1 {
		t.Fatalf("host search manager stats = %#v", engine.SearchStats())
	}
	if err := engine.SetSearchManagerOptions(search.ManagerOptions{}); err == nil {
		t.Fatal("search manager was reconfigured after a site loaded")
	}
	if _, err := os.Stat(filepath.Join(root, ".data", "search")); err != nil {
		t.Fatalf("host search root is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(siteDirectory, ".data", "search")); !os.IsNotExist(err) {
		t.Fatalf("site opened a second search manager: %v", err)
	}
	loaded := engine.cache["localhost"].current()
	if err := os.WriteFile(
		filepath.Join(siteDirectory, "router.kitwork.js"), []byte(reloadedRouter), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	cached := engine.cache["localhost"]
	cached.mu.Lock()
	cached.lastChecked = time.Time{}
	cached.mu.Unlock()
	reloadedResponse := httptest.NewRecorder()
	engine.ServeHTTP(reloadedResponse, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if reloadedResponse.Code != http.StatusOK || !strings.Contains(reloadedResponse.Body.String(), `"id":"p1"`) {
		t.Fatalf("reloaded search response = %d %q", reloadedResponse.Code, reloadedResponse.Body.String())
	}
	reloaded := engine.cache["localhost"].current()
	if reloaded == loaded {
		t.Fatal("hot reload did not replace the tenant facade")
	}
	if len(reloaded.SearchIndexKeys()) != 1 || engine.SearchStats().OpenIndexes != 1 ||
		engine.SearchStats().Commits != 1 {
		t.Fatalf("search ownership changed across hot reload: keys=%v stats=%#v",
			reloaded.SearchIndexKeys(), engine.SearchStats())
	}
	loaded = reloaded
	engine.closeTenantSearch(loaded)
	if stats := engine.SearchStats(); stats.OpenIndexes != 0 {
		t.Fatalf("search index remained open after proven site eviction: %#v", stats)
	}

	engine.Close()
	schema, err := search.NewSchema(search.Text("text", search.StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Search(
		context.Background(), "closed-check", schema,
		search.MatchQuery{Field: "text", Text: "kitwork"}, search.SearchOptions{},
	); !errors.Is(err, search.ErrClosed) {
		t.Fatalf("search after engine close error = %v", err)
	}
}
