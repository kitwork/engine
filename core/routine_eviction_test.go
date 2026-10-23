package core

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

// A site idle past the timeout is evicted from memory, but not while a routine of go it started runs:
// routine.go lives as long as its app (KITWORK_ROUTINE.md mục 3), not as long as the site is visited.
// Once the routine has ended the site is evicted at the next pass.
//
// CONTROL: with evictIdle not asking the tenant for its running routines, the site was evicted while its
// routine was held ("evicted with 1 routine running"), and the routine was stopped.
func TestIdleSiteWithARunningRoutineIsNotEvicted(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "import * as router from \"kitwork/router\";\nimport * as routine from \"kitwork/routine\";\n" +
		"router.get((context) => { routine.go(() => testNotify(\"hold\")); return context.json(\"started\"); });\n"
	if err := os.WriteFile(filepath.Join(directory, work.UIRouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	hook := func(name string, _ ...value.Value) {
		if name != "hold" {
			return
		}
		close(held)
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}
	saved, savedBuiltin := work.TestNotifyHook, builtins.TestNotifyHook
	work.TestNotifyHook, builtins.TestNotifyHook = hook, hook
	t.Cleanup(func() { work.TestNotifyHook, builtins.TestNotifyHook = saved, savedBuiltin })

	engine := New(root, 0, false, "")
	t.Cleanup(engine.Close)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET / = %d %s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("the routine did not start")
	}

	engine.mu.RLock()
	cached := engine.cache["localhost"]
	engine.mu.RUnlock()
	tenant := cached.current()
	idle := time.Now().Add(engine.idleTimeout + time.Hour)
	engine.evictIdle(idle)
	engine.mu.RLock()
	_, kept := engine.cache["localhost"]
	engine.mu.RUnlock()
	if !kept {
		t.Fatalf("evicted with %d routine running", tenant.RunningRoutines())
	}
	if running := tenant.RunningRoutines(); running != 1 {
		t.Fatalf("%d routines running after the pass, want the one held", running)
	}

	close(release)
	for deadline := time.Now().Add(2 * time.Second); tenant.RunningRoutines() > 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	engine.evictIdle(idle)
	engine.mu.RLock()
	_, kept = engine.cache["localhost"]
	engine.mu.RUnlock()
	if kept {
		t.Fatal("the site was kept once its routine had ended")
	}
}
