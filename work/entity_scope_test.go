package work

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
	_ "modernc.org/sqlite"
)

// These tests are written as ATTACKS. A scoped API is only worth having if the ways round it are
// closed, so each case is a thing a site could try — read, overwrite, delete or impersonate another
// app — and asserts it fails. The happy path is one test; the rest are the boundary.
//
// SQLite stands in for the shared Postgres: the builder emits the same SQL for both, and what is
// under test is which rows the predicate admits, not the driver.

func withSharedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE posts (
		id INTEGER PRIMARY KEY, identity TEXT, title TEXT, secret TEXT)`); err != nil {
		t.Fatal(err)
	}
	// Two apps sharing one table — the situation the identity column exists to separate.
	if _, err := db.Exec(`INSERT INTO posts (identity, title, secret) VALUES
		('acme',   'acme post',   'acme-only'),
		('acme',   'acme second', 'acme-only'),
		('victim', 'victim post', 'VICTIM-SECRET')`); err != nil {
		t.Fatal(err)
	}

	prev := database.System
	database.System = db
	t.Cleanup(func() { database.System = prev; db.Close() })
	return db
}

func tenantFor(identity string) *Tenant {
	return &Tenant{AppScope: AppScope{
		config: &Config{},
		entity: &Entity{Identity: identity},
	}}
}

func tableAs(identity, table string) *Entities {
	return (&Database{tenant: tenantFor(identity)}).Entity().Table(table)
}

func TestEntityReadsOnlyItsOwnRows(t *testing.T) {
	withSharedDB(t)

	got := tableAs("acme", "posts").List().String()
	if strings.Contains(got, "VICTIM-SECRET") {
		t.Fatalf("another app's row was returned:\n%s", got)
	}
	// CONTROL: without this the test would also pass on an API that returns nothing at all.
	if !strings.Contains(got, "acme post") {
		t.Fatalf("the app's own rows are missing:\n%s", got)
	}
}

// Naming the other app's identity explicitly must narrow, never widen: the engine's predicate is
// already there and the author's condition is ANDed onto it.
func TestEntityCannotWidenByAskingForAnotherIdentity(t *testing.T) {
	withSharedDB(t)

	got := tableAs("acme", "posts").
		Where(value.New("identity"), value.New("victim")).
		List().String()

	if strings.Contains(got, "VICTIM-SECRET") {
		t.Fatalf("asking for another identity returned its rows:\n%s", got)
	}
}

// A write is the dangerous direction: a row that is merely hidden from reads but writable is not
// isolated at all.
func TestEntityCannotUpdateAnotherIdentitysRow(t *testing.T) {
	db := withSharedDB(t)

	tableAs("acme", "posts").
		Where(value.New("title"), value.New("victim post")).
		Update(value.New(map[string]interface{}{"secret": "OVERWRITTEN"}))

	var secret string
	if err := db.QueryRow(`SELECT secret FROM posts WHERE identity='victim'`).Scan(&secret); err != nil {
		t.Fatal(err)
	}
	if secret != "VICTIM-SECRET" {
		t.Fatalf("another app's row was modified: secret = %q", secret)
	}
}

func TestEntityCannotDeleteAnotherIdentitysRow(t *testing.T) {
	db := withSharedDB(t)

	tableAs("acme", "posts").
		Where(value.New("title"), value.New("victim post")).
		Delete()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM posts WHERE identity='victim'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("another app's row was deleted (%d left)", n)
	}
}

// Writing someone else's identity onto a new row would let an app plant data inside another app's
// slice — invisible to it, but returned to the victim as its own.
func TestEntityCreateForcesItsOwnIdentity(t *testing.T) {
	db := withSharedDB(t)

	tableAs("acme", "posts").Create(value.New(map[string]interface{}{
		"title":    "planted",
		"identity": "victim", // the attempt
	}))

	var owner string
	if err := db.QueryRow(`SELECT identity FROM posts WHERE title='planted'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "acme" {
		t.Fatalf("identity in the payload won: row landed under %q", owner)
	}
}

// The choice made deliberately: no identity means refuse, not "return everything". A silent
// full-table read would be a leak that looks like a working query.
func TestEntityRefusesWhenTheAppHasNoIdentity(t *testing.T) {
	withSharedDB(t)

	res := tableAs("", "posts").List()
	if res.K != value.Invalid {
		t.Fatalf("an app with no identity got a result instead of an error: %v", res.V)
	}
	if !strings.Contains(res.String(), "identity") {
		t.Errorf("the error should say what is missing, got: %s", res.String())
	}
}

func TestEntityRefusesWithoutASharedDatabase(t *testing.T) {
	prev := database.System
	database.System = nil
	t.Cleanup(func() { database.System = prev })

	if res := tableAs("acme", "posts").List(); res.K != value.Invalid {
		t.Fatal("expected an error when no shared database is connected")
	}
}

// The escape hatch that makes builder-level scoping worthless elsewhere: .Raw() and .Exec() run a
// string, so any condition attached above them is irrelevant. This asserts the scoped type does not
// expose one — the guarantee is structural, not a rule someone has to remember.
func TestEntityExposesNoRawEscape(t *testing.T) {
	var d any = &Entities{}

	if _, bad := d.(interface{ Raw() value.Value }); bad {
		t.Error("Entities exposes Raw(): a raw string bypasses the identity predicate")
	}
	if _, bad := d.(interface {
		Exec(string, ...value.Value) value.Value
	}); bad {
		t.Error("Entities exposes Exec(): arbitrary SQL bypasses the identity predicate")
	}
	if _, bad := d.(interface{ Table(string) *Entities }); bad {
		t.Error("Entities exposes Table(): re-targeting would drop the predicate bound at open()")
	}
}

// system() reserves the unscoped entry point without opening it. A method that quietly returned
// every app's rows while the permission system it was supposed to wait for did not exist yet is
// exactly how a temporary hole becomes a permanent one.
func TestSystemIsRefusedUntilPermissionsExist(t *testing.T) {
	withSharedDB(t)

	res := (&Database{tenant: tenantFor("acme")}).System().Table("posts").List()
	if res.K != value.Invalid {
		t.Fatalf("database.system() returned rows before permissions exist: %v", res.V)
	}
	if !strings.Contains(res.String(), "permission") {
		t.Errorf("the refusal should name what is missing, got: %s", res.String())
	}
	// CONTROL: the scoped door is open, so the refusal above is about system() specifically and
	// not about the whole API being broken.
	if got := (&Database{tenant: tenantFor("acme")}).Entity().Table("posts").List(); got.K == value.Invalid {
		t.Fatalf("database.entity() should still work: %v", got.V)
	}
}
