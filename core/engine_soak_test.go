package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEngineHotReloadSoak(t *testing.T) {
	root := t.TempDir()
	routerFile := writeTreeTenant(t, root, "generation-0")
	engine := New(root, 100_000, true, "")
	t.Cleanup(engine.Close)

	serve := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
		)
		return recorder
	}

	first := serve()
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "generation-0") {
		t.Fatalf("initial generation: status=%d body=%q", first.Code, first.Body.String())
	}
	initialApp := engine.cache["localhost"].current().AppRuntime()

	iterations := 12
	if os.Getenv("KITWORK_SOAK") == "1" {
		iterations = 250
	}
	for iteration := 1; iteration <= iterations; iteration++ {
		entry := engine.cache["localhost"]
		oldTenant := entry.current()
		oldGeneration := oldTenant.SiteGeneration()

		want := fmt.Sprintf("generation-%d", iteration)
		writeRouterBody(t, routerFile, want)
		now := time.Now().Add(time.Duration(iteration) * time.Second)
		if err := os.Chtimes(routerFile, now, now); err != nil {
			t.Fatalf("touch generation %d: %v", iteration, err)
		}
		entry.mu.Lock()
		entry.lastChecked = time.Time{}
		entry.mu.Unlock()

		recorder := serve()
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf(
				"generation %d: status=%d body=%q",
				iteration,
				recorder.Code,
				recorder.Body.String(),
			)
		}

		current := engine.cache["localhost"].current()
		if current == oldTenant {
			t.Fatalf("generation %d reused the old tenant", iteration)
		}
		if current.AppRuntime() != initialApp {
			t.Fatalf("generation %d replaced the app runtime", iteration)
		}
		if current.SiteGeneration().Version() <= oldGeneration.Version() {
			t.Fatalf(
				"generation version did not advance: old=%d current=%d",
				oldGeneration.Version(),
				current.SiteGeneration().Version(),
			)
		}
		if !oldGeneration.Retired() {
			t.Fatalf("generation %d did not retire its predecessor", iteration)
		}
	}
}
