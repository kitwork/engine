package work

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
)

func TestConnectionBorrowsHostOwnedNativeDatabase(t *testing.T) {
	connection := &sql.DB{}
	unregister, err := database.RegisterOwned("native-shop", connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unregister)

	handle := (&Database{tenant: &Tenant{}}).Connection(value.New("native-shop"))
	if handle.sqlDB != connection {
		t.Fatalf("native connection = %p, want %p", handle.sqlDB, connection)
	}
}

func TestConnectionReturnsIndependentAliasHandles(t *testing.T) {
	mainDB := &sql.DB{}
	analyticsDB := &sql.DB{}
	unregisterMain, err := database.RegisterOwned("main", mainDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unregisterMain)
	unregisterAnalytics, err := database.RegisterOwned("analytics", analyticsDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unregisterAnalytics)

	root := &Database{tenant: &Tenant{}, config: &database.Config{}, alias: "default"}
	main := root.Connection(value.New("main"))
	analytics := root.Connection(value.New("analytics"))
	if main == root || analytics == root || main == analytics {
		t.Fatal("database.connection must return independent handles")
	}
	if main.sqlDB != mainDB || analytics.sqlDB != analyticsDB {
		t.Fatalf("connections = %p/%p, want %p/%p", main.sqlDB, analytics.sqlDB, mainDB, analyticsDB)
	}
	if root.sqlDB != nil || root.alias != "default" {
		t.Fatalf("root adapter was mutated: alias=%q connection=%p", root.alias, root.sqlDB)
	}
}

func TestConnectedReportsDeclaredAliasAvailability(t *testing.T) {
	connection := &sql.DB{}
	unregister, err := database.RegisterOwned("ready", connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unregister)

	db := &Database{tenant: &Tenant{}}
	if !db.Connected(value.New("ready")) {
		t.Fatal("registered alias was not connected")
	}
	if db.Connected(value.New("missing")) || db.Connected(value.New(42)) {
		t.Fatal("missing or invalid alias reported connected")
	}
}

func TestConnectionRejectsAliasOwnedByTwoDatabaseSources(t *testing.T) {
	previous := database.Configs
	database.Configs = map[string]database.Config{
		"shared": {Alias: "shared", Type: "sqlite", Name: "external.db"},
	}
	t.Cleanup(func() {
		database.Configs = previous
	})

	owned := &sql.DB{}
	unregister, err := database.RegisterOwned("shared", owned)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unregister)

	db := &Database{tenant: &Tenant{}}
	handle := db.Connection(value.New("shared"))
	if handle.sqlDB != nil || db.Connected(value.New("shared")) {
		t.Fatal("ambiguous alias resolved instead of failing closed")
	}
}

func TestMissingAliasHandleNeverFallsBackToDefault(t *testing.T) {
	connection := &sql.DB{}
	unregister, err := database.RegisterOwned("default", connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unregister)

	handle := (&Database{tenant: &Tenant{}}).Connection(value.New("missing"))
	if handle.alias != "missing" || handle.sqlDB != nil {
		t.Fatalf("missing handle = alias %q connection %p", handle.alias, handle.sqlDB)
	}
	if resolved := handle.db(); resolved != nil {
		t.Fatalf("missing alias fell back to default connection %p", resolved)
	}
}

func TestMissingDefaultDatabaseDoesNotCreateImplicitSQLite(t *testing.T) {
	previous := database.Configs
	database.Configs = make(map[string]database.Config)
	t.Cleanup(func() {
		database.Configs = previous
	})

	handle := (&Database{tenant: &Tenant{}}).Connection()
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

func TestConnectDoesNotResolveAliases(t *testing.T) {
	connection := &sql.DB{}
	unregister, err := database.RegisterOwned("external", connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unregister)

	handle := (&Database{tenant: &Tenant{}}).Connect(value.New("external"))
	if handle.sqlDB != nil {
		t.Fatal("database.connect resolved an alias; use database.connection instead")
	}
}
