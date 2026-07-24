package insights_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/services/insights"
)

type mockScope struct{}

func (m *mockScope) AppID() string                     { return "app_test" }
func (m *mockScope) Domain() string                    { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }
func (m *mockScope) DB(name string) *sql.DB            { return nil }

func TestInsightsService(t *testing.T) {
	scope := &mockScope{}
	mgr := insights.NewManager(scope)

	mgr.Track("golang tutorial")
	mgr.Track("golang tutorial")

	top := mgr.TopQueries()
	if len(top) != 1 || top[0].Hits != 2 {
		t.Fatalf("Expected 1 query with 2 hits, got %v", top)
	}
}
