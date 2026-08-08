//go:build !race

package core

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEngineObservedRequestAllocationBudget(t *testing.T) {
	root := t.TempDir()
	writeTreeTenant(t, root, "ok")
	engine := New(root, 0, false, "")
	defer engine.Close()

	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	response := newEngineDiscardWriter()
	engine.ServeHTTP(response, request)

	allocations := testing.AllocsPerRun(100, func() {
		response.Reset()
		engine.ServeHTTP(response, request)
	})
	if response.status != http.StatusOK {
		t.Fatalf("response = %d", response.status)
	}
	if allocations > 48 {
		t.Fatalf("observed engine allocations/request = %.2f, budget is 48", allocations)
	}
}
