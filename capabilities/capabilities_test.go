package capabilities_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/value"
)

type mockScope struct {
	appID  string
	domain string
	root   string
}

func (m *mockScope) AppID() string                     { return m.appID }
func (m *mockScope) Domain() string                    { return m.domain }
func (m *mockScope) ResolvePath(paths ...string) string { return m.root }
func (m *mockScope) DB(name string) *sql.DB            { return nil }

func TestCapabilityRegistry(t *testing.T) {
	reg := capabilities.NewRegistry()
	reg.Register("ping", func(s capabilities.Scope) value.Value {
		return value.New("pong:" + s.AppID())
	})

	scope := &mockScope{appID: "app_123", domain: "example.com", root: "/test"}
	val, ok := reg.Get("ping", scope)
	if !ok {
		t.Fatal("Expected capability 'ping' to be registered")
	}
	if val.Text() != "pong:app_123" {
		t.Errorf("Expected 'pong:app_123', got '%s'", val.Text())
	}
}
