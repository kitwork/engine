package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/search"
)

// The engine owns one bounded search manager for every site: a site's search opens its index there, never a
// manager of its own; the manager is not reconfigured once a site loaded; it outlives a hot reload, an index
// closes when its site is proven evicted, and it closes with the engine. A site reached it through
// db.<table>.search(…) until 22/10/2026, when that spelling was removed (the spec's search() is slice 2 of
// KITWORK_DATABASE.md); the collection search reaches the same manager, so the test searches a collection.
func TestEngineOwnsDatabaseSearchManager(t *testing.T) {
	root := t.TempDir()
	siteDirectory := filepath.Join(root, "test", "localhost")
	posts := filepath.Join(siteDirectory, "_collection", "posts")
	if err := os.MkdirAll(posts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(posts, "kitwork.md"), []byte("---\ntitle: Kitwork Search\n---\nMột hệ thống của Nguyễn."), 0o644); err != nil {
		t.Fatal(err)
	}
	router := `import { router, collection } from "kitwork";
router.get((ctx) => ctx.json(collection.open("posts").search("nguyen")));`
	routerPath := filepath.Join(siteDirectory, "router.kitwork.js")
	if err := os.WriteFile(routerPath, []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(root, 0, true, "")
	if err := engine.SetSearchManagerOptions(search.ManagerOptions{MaxOpenIndexes: 2, DisableAutoCompact: true}); err != nil {
		t.Fatal(err)
	}
	if err := engine.SetCollectionSearchCanary(true); err != nil {
		t.Fatal(err)
	}
	manager := engine.searchManager
	serveCollectionCanaryRequest(t, engine)
	waitForEngineSearches(t, engine, 1)
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
	if err := os.WriteFile(routerPath, []byte(router+"\n// reload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cached := engine.cache["localhost"]
	cached.mu.Lock()
	cached.lastChecked = time.Time{}
	cached.mu.Unlock()
	serveCollectionCanaryRequest(t, engine)
	waitForEngineSearches(t, engine, 2)
	reloaded := engine.cache["localhost"].current()
	if reloaded == loaded {
		t.Fatal("hot reload did not replace the tenant facade")
	}
	if engine.searchManager != manager || len(reloaded.SearchIndexKeys()) != 1 || engine.SearchStats().OpenIndexes != 1 ||
		engine.SearchStats().Commits != 1 {
		t.Fatalf("search ownership changed across hot reload: keys=%v stats=%#v",
			reloaded.SearchIndexKeys(), engine.SearchStats())
	}
	engine.closeTenantSearch(reloaded)
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
