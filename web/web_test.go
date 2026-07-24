package web_test

import (
	"net/http"
	"testing"

	"github.com/kitwork/engine/web"
)

func TestWebRouter(t *testing.T) {
	router := web.NewRouter()
	router.Handle("GET", "/api/ping", func(w http.ResponseWriter, r *http.Request) {})

	routes := router.Routes()
	if len(routes) != 1 {
		t.Fatalf("Expected 1 route, got %d", len(routes))
	}
	if routes[0].Path != "/api/ping" {
		t.Errorf("Expected '/api/ping', got '%s'", routes[0].Path)
	}
}
