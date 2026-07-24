package capabilities_test

import (
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/value"
)

type mockScope struct {
	appID  string
	domain string
	root   string
}

func TestInstanceCacheHonorsCapabilityLifetime(t *testing.T) {
	reg := capabilities.NewRegistry()
	scope := &mockScope{appID: "app_123"}

	var appCalls atomic.Int32
	reg.RegisterWithLifetime("app", capabilities.LifetimeApp, func(capabilities.Scope) value.Value {
		appCalls.Add(1)
		return value.New(&struct{}{})
	})

	cache := capabilities.NewInstanceCache()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := cache.GetOrCompute("app", reg, scope); !ok {
				t.Error("app capability was not resolved")
			}
		}()
	}
	wg.Wait()
	if got := appCalls.Load(); got != 1 {
		t.Fatalf("app capability factory ran %d times, want 1", got)
	}

	var transientCalls atomic.Int32
	reg.RegisterWithLifetime("transient", capabilities.LifetimeTransient, func(capabilities.Scope) value.Value {
		transientCalls.Add(1)
		return value.New(&struct{}{})
	})
	cache.GetOrCompute("transient", reg, scope)
	cache.GetOrCompute("transient", reg, scope)
	if got := transientCalls.Load(); got != 2 {
		t.Fatalf("transient capability factory ran %d times, want 2", got)
	}
}

func (m *mockScope) AppID() string                      { return m.appID }
func (m *mockScope) Domain() string                     { return m.domain }
func (m *mockScope) ResolvePath(paths ...string) string { return m.root }
func (m *mockScope) DB(name string) *sql.DB             { return nil }

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
