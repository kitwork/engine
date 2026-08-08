package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEngineHealthIncludesRequestsAndCurrentOwnership(t *testing.T) {
	root := t.TempDir()
	writeTreeTenant(t, root, "healthy")
	engine := New(root, 0, false, "")
	t.Cleanup(engine.Close)

	for _, target := range []string{"/", "/missing"} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "http://localhost"+target, nil),
		)
	}

	snapshot := engine.Health()
	if snapshot.Requests.Started != 2 || snapshot.Requests.Completed != 2 {
		t.Fatalf("request counters = %+v", snapshot.Requests)
	}
	if snapshot.Requests.Inflight != 0 || snapshot.Requests.MaxInflight == 0 {
		t.Fatalf("request concurrency = %+v", snapshot.Requests)
	}
	if snapshot.LoadedApps != 1 ||
		snapshot.LoadedSites != 1 ||
		snapshot.ActiveGenerations != 1 ||
		snapshot.ActiveGenerationLeases != 0 {
		t.Fatalf("ownership snapshot = %+v", snapshot)
	}
	if snapshot.Generations.Prepared != 1 || snapshot.Generations.Activated != 1 {
		t.Fatalf("generation counters = %+v", snapshot.Generations)
	}
	if snapshot.Latencies.Request.Count != 2 || snapshot.Latencies.Resolve.Count != 2 {
		t.Fatalf("request latency snapshot = %+v", snapshot.Latencies)
	}
}
