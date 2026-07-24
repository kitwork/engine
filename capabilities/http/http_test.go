package http_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/capabilities"
	httpcap "github.com/kitwork/engine/capabilities/http"
)

type mockScope struct{}

func (m *mockScope) AppID() string                     { return "app_test" }
func (m *mockScope) Domain() string                    { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }
func (m *mockScope) DB(name string) *sql.DB            { return nil }

func TestHTTPCapability(t *testing.T) {
	scope := &mockScope{}
	val, ok := capabilities.DefaultRegistry.Get("http", scope)
	if !ok {
		t.Fatal("HTTP capability not registered")
	}

	adapter, ok := val.V.(*httpcap.HTTPAdapter)
	if !ok {
		t.Fatalf("Expected *httpcap.HTTPAdapter, got %T", val.V)
	}

	if adapter == nil {
		t.Fatal("HTTPAdapter is nil")
	}
}
