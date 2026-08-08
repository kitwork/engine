package work

import (
	"testing"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
)

func TestMissingDefaultDatabaseDoesNotCreateImplicitSQLite(t *testing.T) {
	previous := database.Configs
	database.Configs = make(map[string]database.Config)
	t.Cleanup(func() {
		database.Configs = previous
	})

	handle := (&Database{tenant: &Tenant{}}).Connect()
	if handle.sqlDB != nil {
		t.Fatal("missing default connection opened a database; SQLite must be selected explicitly")
	}
	result := handle.Table("items").List()
	if result.K != value.Invalid {
		t.Fatalf("missing connection result kind = %v, want Invalid", result.K)
	}
	if got := result.String(); got != "database not connected" {
		t.Fatalf("missing connection error = %q, want %q", got, "database not connected")
	}
}

func TestTenantCannotSubmitDynamicDatabaseConnection(t *testing.T) {
	previous := database.Configs
	database.Configs = make(map[string]database.Config)
	t.Cleanup(func() {
		database.Configs = previous
	})

	dynamic := value.New(map[string]interface{}{
		"alias":    "escape",
		"type":     "postgres",
		"host":     "127.0.0.1",
		"user":     "tenant",
		"password": "secret",
	})
	handle := (&Database{tenant: &Tenant{}}).Connect(dynamic)
	if handle.sqlDB != nil {
		t.Fatal("tenant-supplied connection configuration was opened")
	}
	result := handle.Table("items").List()
	if result.K != value.Invalid || result.String() != "database not connected" {
		t.Fatalf("dynamic connection result = kind %v value %q", result.K, result.String())
	}
}
