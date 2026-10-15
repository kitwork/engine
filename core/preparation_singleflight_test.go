package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/work"
)

func TestColdPreparationRunsConcurrentlyAcrossDomains(t *testing.T) {
	restoreAllowLocal := setPreparationTestAllowLocal(t)
	defer restoreAllowLocal()

	started := make(chan string, 2)
	release, unblock := preparationRelease(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started <- request.URL.Path
		<-release
		_, _ = writer.Write([]byte("ready"))
	}))
	defer upstream.Close()

	root := t.TempDir()
	writePreparationSite(t, root, "app-id", "one.example", upstream.URL+"/one")
	writePreparationSite(t, root, "app-id", "two.example", upstream.URL+"/two")
	engine := New(root, 0, false, "")
	t.Cleanup(engine.Close)
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}

	errors := make(chan error, 2)
	go func() { errors <- engine.PrewarmDomain("one.example") }()
	if path := waitPreparationPath(t, started); path != "/one" {
		t.Fatalf("first preparation reached %q, want /one", path)
	}
	go func() { errors <- engine.PrewarmDomain("two.example") }()
	if path := waitPreparationPath(t, started); path != "/two" {
		t.Fatalf("second preparation reached %q, want /two", path)
	}

	unblock()
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	health := engine.Health()
	if health.LoadedSites != 2 || health.Generations.Prepared != 2 || health.Generations.Activated != 2 {
		t.Fatalf("cold preparation health = %+v", health)
	}
}

func TestConcurrentColdPreparationSharesOneDomainFlight(t *testing.T) {
	restoreAllowLocal := setPreparationTestAllowLocal(t)
	defer restoreAllowLocal()

	started := make(chan string, 2)
	release, unblock := preparationRelease(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		started <- request.URL.Path
		<-release
		_, _ = writer.Write([]byte("ready"))
	}))
	defer upstream.Close()

	root := t.TempDir()
	writePreparationSite(t, root, "app-id", "one.example", upstream.URL+"/one")
	engine := New(root, 0, false, "")
	t.Cleanup(engine.Close)
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}

	const callers = 16
	errors := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			errors <- engine.PrewarmDomain("one.example")
		}()
	}
	if path := waitPreparationPath(t, started); path != "/one" {
		t.Fatalf("preparation reached %q, want /one", path)
	}
	select {
	case path := <-started:
		t.Fatalf("duplicate preparation reached %q before publication", path)
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream preparation calls = %d, want 1", got)
	}
	health := engine.Health()
	if health.LoadedSites != 1 || health.Generations.Prepared != 1 || health.Generations.Activated != 1 {
		t.Fatalf("shared preparation health = %+v", health)
	}
}

func TestCloseWaitsForColdPreparationAndPreventsPublication(t *testing.T) {
	restoreAllowLocal := setPreparationTestAllowLocal(t)
	defer restoreAllowLocal()

	started := make(chan string, 1)
	release, unblock := preparationRelease(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started <- request.URL.Path
		<-release
		_, _ = writer.Write([]byte("ready"))
	}))
	defer upstream.Close()

	root := t.TempDir()
	writePreparationSite(t, root, "app-id", "one.example", upstream.URL+"/one")
	engine := New(root, 0, false, "")
	if err := engine.SetRootLayout(work.RootLayoutMultiTenant); err != nil {
		t.Fatal(err)
	}

	prepared := make(chan error, 1)
	go func() { prepared <- engine.PrewarmDomain("one.example") }()
	if path := waitPreparationPath(t, started); path != "/one" {
		t.Fatalf("preparation reached %q, want /one", path)
	}
	closed := make(chan struct{})
	go func() {
		engine.Close()
		close(closed)
	}()
	waitEngineClosed(t, engine)
	select {
	case <-closed:
		t.Fatal("engine closed before accepted preparation released")
	case <-time.After(100 * time.Millisecond):
	}

	unblock()
	if err := <-prepared; err == nil || !strings.Contains(err.Error(), "engine is closed") {
		t.Fatalf("prewarm error = %v, want engine closed", err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("engine close did not finish after preparation drained")
	}
	health := engine.Health()
	if health.LoadedSites != 0 || health.ActiveGenerations != 0 {
		t.Fatalf("closed engine published preparation: %+v", health)
	}
}

func writePreparationSite(t testing.TB, root, identity, domain, upstream string) {
	t.Helper()
	directory := filepath.Join(root, identity, domain)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`
import { router, http } from "kitwork";
http.get(%q);
router.get((ctx) => ctx.text("ready"));
`, upstream)
	if err := os.WriteFile(filepath.Join(directory, work.RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setPreparationTestAllowLocal(t testing.TB) func() {
	t.Helper()
	previous := work.AllowLocal
	work.AllowLocal = true
	return func() { work.AllowLocal = previous }
}

func preparationRelease(t testing.TB) (<-chan struct{}, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() {
		once.Do(func() { close(release) })
	}
	t.Cleanup(unblock)
	return release, unblock
}

func waitPreparationPath(t testing.TB, started <-chan string) string {
	t.Helper()
	select {
	case path := <-started:
		return path
	case <-time.After(5 * time.Second):
		t.Fatal("cold preparation did not reach its blocking initializer")
		return ""
	}
}

func waitEngineClosed(t testing.TB, engine *Engine) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		engine.mu.RLock()
		closed := engine.closed
		engine.mu.RUnlock()
		if closed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("engine close did not mark the engine closed")
}
