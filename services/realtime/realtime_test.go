package realtime_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/services/realtime"
)

type mockScope struct{}

func (m *mockScope) AppID() string                      { return "app_test" }
func (m *mockScope) Domain() string                     { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }
func (m *mockScope) DB(name string) *sql.DB             { return nil }

func TestRealtimeService(t *testing.T) {
	scope := &mockScope{}
	broker := realtime.NewBroker(scope)

	ch := broker.Subscribe()
	broker.Publish("hello")

	msg := <-ch
	if msg != "hello" {
		t.Errorf("Expected 'hello', got '%s'", msg)
	}

	broker.Unsubscribe(ch)
}
