package core

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/search"
)

func TestEngineCollectionSearchCanaryUsesHostManagerAcrossReload(t *testing.T) {
	root := t.TempDir()
	siteDirectory := filepath.Join(root, "test", "localhost")
	posts := filepath.Join(siteDirectory, "_collection", "posts")
	if err := os.MkdirAll(posts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(posts, "kitwork.md"),
		[]byte("---\ntitle: Kitwork Search\n---\nMột hệ thống của Nguyễn."),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	router := `import { router, collection } from "kitwork";
router.get((ctx) => ctx.json(collection.open("posts").search("nguyen")));`
	routerPath := filepath.Join(siteDirectory, "router.kitwork.js")
	if err := os.WriteFile(routerPath, []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := New(root, 0, true, "")
	defer engine.Close()
	if err := engine.SetSearchManagerOptions(search.ManagerOptions{
		MaxOpenIndexes: 2, DisableAutoCompact: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.SetCollectionSearchCanary(true); err != nil {
		t.Fatal(err)
	}
	serveCollectionCanaryRequest(t, engine)
	waitForEngineSearches(t, engine, 1)
	if stats := engine.SearchStats(); stats.OpenIndexes != 1 || stats.Commits != 1 || stats.Searches.Failed != 0 {
		t.Fatalf("first collection canary stats = %#v", stats)
	}
	if stats := engine.CollectionSearchCanaryStats(); !stats.Enabled || stats.Searches != 1 ||
		stats.Rebuilds != 1 || stats.Matches != 1 || stats.Mismatches != 0 || stats.Failures != 0 {
		t.Fatalf("first collection canary telemetry = %#v", stats)
	}
	loaded := engine.cache["localhost"].current()
	if len(loaded.SearchIndexKeys()) != 1 {
		t.Fatalf("collection index keys = %#v", loaded.SearchIndexKeys())
	}
	if _, err := os.Stat(filepath.Join(root, ".data", "search")); err != nil {
		t.Fatalf("host search root is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(siteDirectory, ".data", "search")); !os.IsNotExist(err) {
		t.Fatalf("collection opened a site-local search writer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(siteDirectory, ".data", "search-state", collectionCanaryVersionForTest)); err != nil {
		t.Fatalf("collection generation signatures are missing: %v", err)
	}

	if err := engine.SetCollectionSearchCanary(false); err == nil {
		t.Fatal("collection canary was reconfigured after a site loaded")
	}
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
	if stats := engine.SearchStats(); stats.OpenIndexes != 1 || stats.Commits != 1 || stats.Searches.Failed != 0 {
		t.Fatalf("collection canary rebuilt across hot reload: %#v", stats)
	}
	if stats := engine.CollectionSearchCanaryStats(); !stats.Enabled || stats.Searches != 2 ||
		stats.Rebuilds != 1 || stats.Matches != 2 || stats.Mismatches != 0 || stats.Failures != 0 {
		t.Fatalf("collection canary telemetry reset across hot reload: %#v", stats)
	}
	if len(reloaded.SearchIndexKeys()) != 1 {
		t.Fatalf("reloaded collection index keys = %#v", reloaded.SearchIndexKeys())
	}
}

const collectionCanaryVersionForTest = "collection-v2"

func serveCollectionCanaryRequest(t *testing.T, engine *Engine) {
	t.Helper()
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"slug":"kitwork"`) {
		t.Fatalf("collection search response = %d %q", response.Code, response.Body.String())
	}
}

func waitForEngineSearches(t *testing.T, engine *Engine, completed uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		managerStats := engine.SearchStats()
		canaryStats := engine.CollectionSearchCanaryStats()
		comparisons := canaryStats.Matches + canaryStats.Mismatches + canaryStats.Failures +
			canaryStats.Canceled + canaryStats.Stale
		if managerStats.Searches.Completed >= completed && comparisons >= completed &&
			canaryStats.PendingTasks == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf(
		"engine search did not drain: manager=%#v canary=%#v",
		engine.SearchStats(), engine.CollectionSearchCanaryStats(),
	)
}
