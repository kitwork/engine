package scheduler_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/services/scheduler"
)

type mockScope struct{}

func (m *mockScope) AppID() string                     { return "app_test" }
func (m *mockScope) Domain() string                    { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }
func (m *mockScope) DB(name string) *sql.DB            { return nil }

func TestSchedulerService(t *testing.T) {
	scope := &mockScope{}
	mgr := scheduler.NewManager(scope)

	job := mgr.Schedule("daily-report", "0 0 * * *")
	if job == nil || job.Name != "daily-report" {
		t.Fatalf("Failed to schedule job: %v", job)
	}

	list := mgr.List()
	if len(list) != 1 {
		t.Fatalf("Expected 1 job in list, got %d", len(list))
	}
}
