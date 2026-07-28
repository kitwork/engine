package site_test

import (
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/app"
)

func TestSiteResourcesSurviveGenerationReplacement(t *testing.T) {
	root := t.TempDir()
	appRuntime := app.NewRuntime("identity-a")
	siteRuntime, _ := appRuntime.Site(root, "example.com")
	if err := siteRuntime.ConfigureResources(root); err != nil {
		t.Fatal(err)
	}
	persistStore := siteRuntime.PersistStore()
	limiter := siteRuntime.Limiter()
	broker := siteRuntime.SSEBroker()
	if persistStore == nil || limiter == nil || broker == nil {
		t.Fatal("site resources were not initialized")
	}
	if got, want := persistStore.Dir, filepath.Join(root, ".persist"); got != want {
		t.Fatalf("persist dir = %q, want %q", got, want)
	}

	first, _ := siteRuntime.PrepareGeneration()
	if _, err := siteRuntime.ActivateGeneration(first); err != nil {
		t.Fatal(err)
	}
	second, _ := siteRuntime.PrepareGeneration()
	if _, err := siteRuntime.ActivateGeneration(second); err != nil {
		t.Fatal(err)
	}
	first.Retire()

	if siteRuntime.PersistStore() != persistStore {
		t.Fatal("generation replacement changed the persistent store")
	}
	if siteRuntime.Limiter() != limiter {
		t.Fatal("generation replacement changed the site limiter")
	}
	if siteRuntime.SSEBroker() != broker {
		t.Fatal("generation replacement changed the site SSE broker")
	}
	if first.ResponseCache() == second.ResponseCache() {
		t.Fatal("generations shared a RAM response cache")
	}
	if err := siteRuntime.ConfigureResources(filepath.Join(root, "other")); err == nil {
		t.Fatal("site runtime accepted a different resource root")
	}
	siteRuntime.Close()
}
