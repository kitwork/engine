package work

import (
	"context"
	"testing"
)

// migrateAppForTest is kitwork migrate --yes on the app identity under root: what a server's databases need before it
// serves, now that a statement never makes a table (KITWORK_DATABASE.md "Database là gốc", slice 4).
func migrateAppForTest(t testing.TB, root, identity string, layout RootLayout) []*DatabaseMigration {
	t.Helper()
	plans, release, err := PlanAppMigrations(root, identity, layout, nil)
	if err != nil {
		t.Fatalf("kitwork migrate: %v", err)
	}
	defer release()
	for _, plan := range plans {
		if !plan.Pending() {
			continue
		}
		if err := plan.Apply(context.Background()); err != nil {
			t.Fatalf("kitwork migrate %s: %v", plan.Database, err)
		}
	}
	return plans
}

// migrateAppAsItCan is migrateAppForTest for an app whose boot the test reads: a schema file that does not run is the
// boot's to say, not the migration's.
func migrateAppAsItCan(t testing.TB, root, identity string, layout RootLayout) {
	t.Helper()
	plans, release, err := PlanAppMigrations(root, identity, layout, nil)
	if err != nil {
		t.Logf("kitwork migrate: %v", err)
		return
	}
	defer release()
	for _, plan := range plans {
		if plan.Pending() {
			if err := plan.Apply(context.Background()); err != nil {
				t.Fatalf("kitwork migrate %s: %v", plan.Database, err)
			}
		}
	}
}

// migrateManagedForTest adds to the file of a database declared from Go what its tables declare and it lacks, as
// kitwork migrate does for a schema file.
func migrateManagedForTest(t testing.TB, managed *ManagedDatabase) {
	t.Helper()
	tables, _ := managed.proxy.schemaSnapshot()
	plan := &DatabaseMigration{Path: managed.proxy.sqlitePath, tables: tables, tenant: managed.proxy.tenant}
	if err := plan.Apply(context.Background()); err != nil {
		t.Fatalf("kitwork migrate %s: %v", managed.proxy.sqlitePath, err)
	}
}
