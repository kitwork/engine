package capabilities_test

import (
	"testing"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/value"
)

type mockScope struct {
	appID  string
	domain string
}

func (m *mockScope) AppID() string                     { return m.appID }
func (m *mockScope) Domain() string                    { return m.domain }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }

func TestCapabilityRegistry(t *testing.T) {
	reg := capabilities.NewRegistry()
	reg.Register("ping", func(s capabilities.Scope) value.Value {
		return value.New("pong:" + s.AppID())
	})

	scope := &mockScope{appID: "app_123", domain: "example.com"}
	val, ok := reg.Get("ping", scope)
	if !ok {
		t.Fatal("Expected capability 'ping' to be registered")
	}
	if val.Text() != "pong:app_123" {
		t.Errorf("Expected 'pong:app_123', got '%s'", val.Text())
	}
}
