package database_test

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/capabilities"
	dbcap "github.com/kitwork/engine/capabilities/database"
	"github.com/kitwork/engine/value"
	_ "modernc.org/sqlite"
)

type mockScope struct {
	db *sql.DB
}

func (m *mockScope) AppID() string                      { return "app_test" }
func (m *mockScope) Domain() string                     { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string { return "/test" }
func (m *mockScope) DB(name string) *sql.DB             { return m.db }

func TestDatabaseCapability(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Open db failed: %v", err)
	}
	defer db.Close()

	scope := &mockScope{db: db}
	val, ok := capabilities.DefaultRegistry.Get("database", scope)
	if !ok {
		t.Fatal("Database capability not registered")
	}

	adapter, ok := val.V.(*dbcap.DatabaseAdapter)
	if !ok {
		t.Fatalf("Expected *dbcap.DatabaseAdapter, got %T", val.V)
	}

	tbl := adapter.Table(value.NewString("users"))
	if tbl.K == value.Invalid {
		t.Fatalf("Table creation failed: %v", tbl.V)
	}
}

func TestConnectionReturnsIndependentCapabilityHandle(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := dbcap.NewDatabaseAdapter(&mockScope{db: db})
	main := root.Connection("main")
	analytics := root.Connection("analytics")
	if main == root || analytics == root || main == analytics {
		t.Fatal("connection aliases share a mutable adapter")
	}
	if !root.Connected("main") {
		t.Fatal("declared connection was not reported available")
	}
	if connected := root.Connect(value.NewString("main")); connected.DB() != nil {
		t.Fatal("connect resolved an alias instead of reserving external connection semantics")
	}
}
