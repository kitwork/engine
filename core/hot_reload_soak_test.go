package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEngineHotReloadGenerationSoak(t *testing.T) {
	root := t.TempDir()
	routerFile := writeTreeTenant(t, root, "v0")
	engine := New(root, 0, true, "")
	t.Cleanup(engine.Close)

	first := httptest.NewRecorder()
	engine.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("initial response = %d: %s", first.Code, first.Body.String())
	}

	const reloads = 12
	const requestsPerReload = 6
	for revision := 1; revision <= reloads; revision++ {
		engine.mu.RLock()
		cached := engine.cache["localhost"]
		engine.mu.RUnlock()
		if cached == nil {
			t.Fatal("tenant disappeared from engine cache")
		}
		oldTenant := cached.current()
		oldGeneration := oldTenant.SiteGeneration()

		body := fmt.Sprintf("v%d", revision)
		writeRouterBody(t, routerFile, body)
		cached.mu.Lock()
		cached.lastChecked = time.Time{}
		cached.mu.Unlock()

		var requests sync.WaitGroup
		failures := make(chan string, requestsPerReload)
		for index := 0; index < requestsPerReload; index++ {
			requests.Add(1)
			go func() {
				defer requests.Done()
				response := httptest.NewRecorder()
				engine.ServeHTTP(
					response,
					httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
				)
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), body) {
					failures <- fmt.Sprintf(
						"status=%d body=%q want=%q",
						response.Code,
						response.Body.String(),
						body,
					)
				}
			}()
		}
		requests.Wait()
		close(failures)
		for failure := range failures {
			t.Errorf("reload %d: %s", revision, failure)
		}

		current := cached.current()
		if current == oldTenant || current.SiteGeneration() == oldGeneration {
			t.Fatalf("reload %d did not publish a new tenant generation", revision)
		}
		if !oldGeneration.Retired() ||
			oldGeneration.Active() != 0 ||
			oldGeneration.RouteGraph() != nil ||
			oldGeneration.RenderPlan() != nil {
			t.Fatalf("reload %d retained the old generation", revision)
		}
	}

	health := engine.Health()
	if health.Generations.Prepared != reloads+1 ||
		health.Generations.Activated != reloads+1 ||
		health.Generations.Drained != reloads {
		t.Fatalf("generation health = %+v", health.Generations)
	}
	if health.Requests.Started != 1+reloads*requestsPerReload ||
		health.Requests.Completed != health.Requests.Started ||
		health.Requests.Inflight != 0 {
		t.Fatalf("request health = %+v", health.Requests)
	}
	if health.ActiveGenerations != 1 || health.ActiveGenerationLeases != 0 {
		t.Fatalf("active generation health = %+v", health)
	}
}
