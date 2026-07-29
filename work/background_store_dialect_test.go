package work

import (
	"database/sql"
	"testing"

	"github.com/kitwork/engine/database"
)

// The background stores must choose their SQL by the system database's DIALECT, not by the mere
// presence of a handle.
//
// `database.System` is a *sql.DB, and a *sql.DB cannot be asked what SQL it speaks. Both the
// scheduler and the queue used to read `System != nil` as if it meant "Postgres" — true of every
// deployment so far, which is exactly why it survived. Configure the system database as SQLite (the
// manifest allows it: `type: env.DB_TYPE`) and both would reach for the Postgres store, whose `$1`
// placeholders and `FOR UPDATE SKIP LOCKED` SQLite cannot parse — so background work would fail at
// the first claim, in a deployment that boots and serves pages perfectly.
//
// The assertion is on the store's own label rather than on a query succeeding: a query can be made
// to pass by accident, but the label states WHICH dialect was selected, which is the decision under
// test.
func TestBackgroundStoresFollowSystemDialect(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	savedSystem, savedDriver := database.System, database.SystemDriver
	t.Cleanup(func() { database.System, database.SystemDriver = savedSystem, savedDriver })

	tenant := tenantFor("acme")

	// A SQLite system database must get the SQLite store.
	database.System = db
	database.SystemDriver = "sqlite"
	if got := tenant.openQueueStore().Label(); got != "sqlite" {
		t.Errorf("queue store for a SQLite system database is %q, want \"sqlite\"", got)
	}
	if got := tenant.openCronStore().label(); got != "sqlite" {
		t.Errorf("cron store for a SQLite system database is %q, want \"sqlite\"", got)
	}

	// CONTROL: the same handle declared as Postgres must get the Postgres store — otherwise the
	// test above would pass just as well if the code always chose SQLite.
	database.SystemDriver = "postgres"
	if got := tenant.openQueueStore().Label(); got != "postgres" {
		t.Errorf("queue store for a Postgres system database is %q, want \"postgres\"", got)
	}
	if got := tenant.openCronStore().label(); got != "postgres" {
		t.Errorf("cron store for a Postgres system database is %q, want \"postgres\"", got)
	}
}
