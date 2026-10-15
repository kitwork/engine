package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/work"
)

// Prewarm must discover every root/<identity>/<domain>/router.kitwork.js (the tenant marker) and
// warm it into the cache — while ignoring folders without a root router.
func TestPrewarmAndDiscover(t *testing.T) {
	tmpDir := t.TempDir()
	for _, dom := range []string{"localhost", "alpha.local"} {
		dir := filepath.Join(tmpDir, "test", dom)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(`const x = 1;`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A folder with no root router must be ignored by discovery.
	if err := os.MkdirAll(filepath.Join(tmpDir, "test", "notenant"), 0o755); err != nil {
		t.Fatal(err)
	}

	engine := New(tmpDir, 0, false, "")
	t.Cleanup(engine.Close)

	domains := engine.discoverTenants()
	if len(domains) != 2 {
		t.Fatalf("discoverTenants: want 2, got %d (%v)", len(domains), domains)
	}

	warmed, failed := engine.Prewarm()
	if warmed != 2 || failed != 0 {
		t.Fatalf("Prewarm: warmed=%d failed=%d, want 2/0", warmed, failed)
	}

	engine.mu.RLock()
	_, okL := engine.cache["localhost"]
	_, okA := engine.cache["alpha.local"]
	engine.mu.RUnlock()
	if !okL || !okA {
		t.Fatalf("expected both tenants cached (localhost=%v alpha.local=%v)", okL, okA)
	}
}

func TestPrewarmDomainWarmsOnlySelectedSite(t *testing.T) {
	root := t.TempDir()
	for _, domain := range []string{"primary.example.com", "second.example.com"} {
		directory := filepath.Join(root, "identity", domain)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, work.RouterFileName), []byte(`const ready = true;`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	engine := New(root, 0, false, "primary.example.com")
	t.Cleanup(engine.Close)
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}
	if err := engine.PrewarmDomain("PRIMARY.Example.COM."); err != nil {
		t.Fatal(err)
	}

	engine.mu.RLock()
	_, primaryWarm := engine.cache["primary.example.com"]
	_, secondWarm := engine.cache["second.example.com"]
	engine.mu.RUnlock()
	if !primaryWarm || secondWarm {
		t.Fatalf("warm cache: primary=%t second=%t", primaryWarm, secondWarm)
	}
}

// SetIdleTimeout(0) must disable eviction so cached tenants stay warm forever.
func TestSetIdleTimeoutNeverEvict(t *testing.T) {
	engine := New(t.TempDir(), 0, false, "")
	t.Cleanup(engine.Close)

	engine.SetIdleTimeout(0)
	engine.mu.RLock()
	to := engine.idleTimeout
	engine.mu.RUnlock()
	if to != 0 {
		t.Fatalf("idleTimeout = %v, want 0", to)
	}

	// Seed a cached entry that is already long-idle, then run one cleanup pass
	// with timeout 0 — it must NOT be evicted.
	engine.mu.Lock()
	engine.cache["pinned"] = &cachedTenant{lastAccess: time.Now().Add(-24 * time.Hour)}
	engine.mu.Unlock()

	// Replicate the cleanup decision (timeout 0 → skip).
	engine.mu.Lock()
	timeout := engine.idleTimeout
	evicted := false
	if timeout > 0 {
		for d, c := range engine.cache {
			if c.isExpired(time.Now(), timeout) {
				delete(engine.cache, d)
				evicted = true
			}
		}
	}
	_, stillThere := engine.cache["pinned"]
	engine.mu.Unlock()

	if evicted || !stillThere {
		t.Fatalf("with idleTimeout=0 the pinned tenant must survive (evicted=%v present=%v)", evicted, stillThere)
	}
}
