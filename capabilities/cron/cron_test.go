package cron_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/capabilities"
	croncap "github.com/kitwork/engine/capabilities/cron"

)

type mockScope struct{}

func (m *mockScope) AppID() string                     { return "app_test" }
func (m *mockScope) Domain() string                    { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }
func (m *mockScope) DB(name string) *sql.DB            { return nil }

func TestCronCapability(t *testing.T) {
	scope := &mockScope{}
	val, ok := capabilities.DefaultRegistry.Get("cron", scope)
	if !ok {
		t.Fatal("Cron capability not registered")
	}

	adapter, ok := val.V.(*croncap.CronAdapter)
	if !ok {
		t.Fatalf("Expected *croncap.CronAdapter, got %T", val.V)
	}

	b := adapter.Daily()
	if b == nil {
		t.Fatal("Expected CronBuilder, got nil")
	}
}
