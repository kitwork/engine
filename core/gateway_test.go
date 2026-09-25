package core

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kitwork/engine/gateway"
)

func TestGatewayOnlyEngineDispatchesWithoutTenantResolution(t *testing.T) {
	exactUpstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Upstream", "exact")
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer exactUpstream.Close()
	fallbackUpstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Upstream", "fallback")
		writer.WriteHeader(http.StatusCreated)
	}))
	defer fallbackUpstream.Close()

	dispatcher, err := gateway.NewManager([]gateway.Target{
		{Domain: "api.example.com", Target: exactUpstream.URL},
		{Target: fallbackUpstream.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.Close()

	engine := NewGatewayHost("")
	defer engine.Close()
	engine.SetGateway(dispatcher)
	if engine.searchManager != nil || engine.kitDBNodeManager != nil {
		t.Fatal("gateway-only host initialized web runtime managers")
	}

	exact := httptest.NewRecorder()
	engine.ServeHTTP(exact, httptest.NewRequest(http.MethodGet, "http://api.example.com/status", nil))
	if exact.Code != http.StatusAccepted || exact.Header().Get("X-Upstream") != "exact" {
		t.Fatalf("exact response = %d %q", exact.Code, exact.Header().Get("X-Upstream"))
	}

	fallback := httptest.NewRecorder()
	engine.ServeHTTP(fallback, httptest.NewRequest(http.MethodGet, "http://unknown.example/status", nil))
	if fallback.Code != http.StatusCreated || fallback.Header().Get("X-Upstream") != "fallback" {
		t.Fatalf("fallback response = %d %q", fallback.Code, fallback.Header().Get("X-Upstream"))
	}
}

func TestGatewayOnlyEngineReturnsNotFoundWithoutDefaultTarget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	dispatcher, err := gateway.NewManager([]gateway.Target{{Domain: "api.example.com", Target: upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer dispatcher.Close()

	engine := NewGatewayHost("")
	defer engine.Close()
	engine.SetGateway(dispatcher)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://unknown.example/", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
