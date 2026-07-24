package shortbase_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/capabilities"
	sbcap "github.com/kitwork/engine/capabilities/shortbase"
	"github.com/kitwork/engine/value"
)

type mockScope struct{}

func (m *mockScope) AppID() string                      { return "app_test" }
func (m *mockScope) Domain() string                     { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }
func (m *mockScope) DB(name string) *sql.DB             { return nil }

func TestShortbaseCapability(t *testing.T) {
	scope := &mockScope{}
	val, ok := capabilities.DefaultRegistry.Get("shortbase", scope)
	if !ok {
		t.Fatal("Shortbase capability not registered")
	}

	adapter, ok := val.V.(*sbcap.ShortbaseAdapter)
	if !ok {
		t.Fatalf("Expected *sbcap.ShortbaseAdapter, got %T", val.V)
	}

	encoded := adapter.Encode(value.NewString("12345"))
	if encoded.K == value.Nil {
		t.Fatal("Shortbase encode failed")
	}

	decoded := adapter.Decode(encoded)
	if decoded.Text() != "12345" {
		t.Errorf("Expected '12345', got '%s'", decoded.Text())
	}
}
