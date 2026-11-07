package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// historyFile is a new file of its own, opened with options, and an admin's session of the port on it.
func historyFile(t *testing.T, options kitdb.Options) (string, *kitdb.Database, *Session) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "application", "kitwork.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	database, err := kitdb.Open(context.Background(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return path, database, historySession(t, path, database)
}

func historySession(t *testing.T, path string, database *kitdb.Database) *Session {
	t.Helper()
	access := newAccess()
	access.admin = true
	session, err := Open(context.Background(), Target{Name: "shop.example/application", Schemas: map[string]string{"public": path}},
		access, fakeColumns{}, Options{Writers: writers{path: database}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// historyOn is the tables of kitwork_tables whose history is on, in order, joined by commas.
func historyOn(t *testing.T, database *kitdb.Database) any {
	t.Helper()
	return stored(t, database, `SELECT coalesce(group_concat(table_name, ','), '') FROM (SELECT table_name FROM kitwork_tables WHERE history = 1 ORDER BY table_name)`)
}

// TestHistoryIsDeclaredInSQL: a table's history is the database's to declare (KITWORK_KITDB.md câu 13.11 (a), (d)),
// through the port as PostgreSQL writes a table's storage parameters: CREATE TABLE … WITH (history = on), ALTER TABLE
// … SET (history = on | off), RESET (history). A table made with none has no history: its rows change with no trace. A
// table renamed keeps its history; one dropped takes it away, so a table made again of the same name has none. The
// structure file says it (WITH (history = on), ALTER TABLE … SET after a table written as SQLite writes it), and a
// file made from the structure has the history it says, and only that, whatever the writer that makes it.
//
// CONTROL (07/11/2026): with CREATE TABLE taking no notice of WITH, "orders, made WITH (history = on): an insert
// committed no revision"; with a rename not moving kitwork_tables, "after RENAME TO purchases the history is on for
// \"orders\""; with a drop not taking the setting away, "purchases, made again after DROP TABLE: an insert committed
// revision 24"; with the structure not writing WITH, "the structure file: … lacks CREATE TABLE events (…) WITH (history
// = on);"; with LoadStructure making kitwork_tables only for a structure that says some history, "the file made from a
// structure of no history, opened again: an insert into notes committed revision 3".
func TestHistoryIsDeclaredInSQL(t *testing.T) {
	ctx := context.Background()
	path, database, session := historyFile(t, kitdb.Options{})
	// recorded runs an insert and reports whether it committed a revision: the history records the table
	recorded := func(label, insert string) bool {
		t.Helper()
		before := database.Revision()
		if tag := run(t, session, insert); tag != "INSERT 0 1" {
			t.Fatalf("%s: %s: tag %q", label, insert, tag)
		}
		return database.Revision() != before
	}

	run(t, session, `CREATE TABLE notes (id bigint PRIMARY KEY, body text)`)
	if recorded("notes, made with no history", `INSERT INTO notes VALUES (1, 'một')`) {
		t.Fatalf("notes, made with no history: an insert committed revision %d", database.Revision())
	}
	if tag := run(t, session, `CREATE TABLE orders (id bigint PRIMARY KEY, total bigint) WITH (history = on)`); tag != "CREATE TABLE" {
		t.Fatalf("tag %q", tag)
	}
	if !recorded("orders, made WITH (history = on)", `INSERT INTO orders VALUES (1, 100)`) {
		t.Fatalf("orders, made WITH (history = on): an insert committed no revision")
	}

	if tag := run(t, session, `ALTER TABLE notes SET (history = on)`); tag != "ALTER TABLE" {
		t.Fatalf("tag %q", tag)
	}
	if !recorded("notes after SET (history = on)", `INSERT INTO notes VALUES (2, 'hai')`) {
		t.Fatalf("notes after SET (history = on): an insert committed no revision")
	}
	run(t, session, `ALTER TABLE notes SET (history = 'off')`)
	if recorded("notes after SET (history = 'off')", `INSERT INTO notes VALUES (3, 'ba')`) {
		t.Fatalf("notes after SET (history = 'off'): an insert committed revision %d", database.Revision())
	}
	run(t, session, `ALTER TABLE notes SET (history = true)`)
	run(t, session, `ALTER TABLE notes RESET (history)`)
	if recorded("notes after RESET (history)", `INSERT INTO notes VALUES (4, 'bốn')`) {
		t.Fatalf("notes after RESET (history): an insert committed revision %d", database.Revision())
	}
	if got := historyOn(t, database); got != "orders" {
		t.Fatalf("the history is on for %q, want orders", got)
	}

	run(t, session, `ALTER TABLE orders RENAME TO purchases`)
	if got := historyOn(t, database); got != "purchases" {
		t.Fatalf("after RENAME TO purchases the history is on for %q", got)
	}
	if !recorded("purchases, renamed from orders", `INSERT INTO purchases VALUES (2, 200)`) {
		t.Fatalf("purchases, renamed from orders: an insert committed no revision")
	}
	run(t, session, `DROP TABLE purchases`)
	run(t, session, `CREATE TABLE purchases (id bigint PRIMARY KEY, total bigint)`)
	if recorded("purchases, made again after DROP TABLE", `INSERT INTO purchases VALUES (1, 100)`) {
		t.Fatalf("purchases, made again after DROP TABLE: an insert committed revision %d", database.Revision())
	}

	// a table written as SQLite writes it (not STRICT), its history on: the structure sets it after the table
	if _, err := database.Migrate(ctx, "t", `CREATE TABLE loose (id INTEGER PRIMARY KEY, value)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetHistory(ctx, "t", "loose", true); err != nil {
		t.Fatal(err)
	}
	run(t, session, `CREATE TABLE events (id bigint PRIMARY KEY, name text) WITH (history = on)`)
	structure := structureOf(t, path)
	for _, want := range []string{"CREATE TABLE events (\n  id bigserial PRIMARY KEY,\n  name text\n) WITH (history = on);", "ALTER TABLE loose SET (history = on);"} {
		if !strings.Contains(structure, want) {
			t.Fatalf("the structure file:\n%s\nlacks %s", structure, want)
		}
	}
	for _, unwanted := range []string{"notes (\n  id bigserial PRIMARY KEY,\n  body text\n) WITH", "purchases (\n  id bigserial PRIMARY KEY,\n  total bigint\n) WITH"} {
		if strings.Contains(structure, unwanted) {
			t.Fatalf("the structure file:\n%s\nsays the history of a table that has none: %s", structure, unwanted)
		}
	}

	// the file made from the structure, by a writer that keeps every table's history (the engines until slice 5): it
	// has kitwork_tables, so opened again by one that keeps the tables' it declares, notes has no history
	madePath := filepath.Join(t.TempDir(), "kitwork.db")
	made, err := kitdb.Open(ctx, madePath, kitdb.Options{HistoryOfEveryTable: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := LoadStructure(ctx, made, "boot", structure); err != nil {
		t.Fatal(err)
	}
	if got := historyOn(t, made); got != "events,loose" {
		t.Fatalf("the file made from the structure has the history on for %q, want events,loose", got)
	}
	if err := made.Close(); err != nil {
		t.Fatal(err)
	}
	made, err = kitdb.Open(ctx, madePath, kitdb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = made.Close() })
	for _, write := range []struct {
		table, insert string
		want          bool
	}{
		{"notes", `INSERT INTO notes (id, body) VALUES (1, 'một')`, false},
		{"events", `INSERT INTO events (id, name) VALUES (1, 'mở')`, true},
		{"loose", `INSERT INTO loose (id, value) VALUES (1, 'x')`, true},
	} {
		before := made.Revision()
		if _, err := made.Write(ctx, "t", func(transaction *kitdb.Transaction) error {
			_, err := transaction.Exec(write.insert)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if got := made.Revision() != before; got != write.want {
			t.Fatalf("the file made from the structure, opened again: an insert into %s committed revision %v, want %v", write.table, got, write.want)
		}
	}

	// a structure that says no table's history: the file made from it has none either, opened again
	quietPath, _, quietSession := historyFile(t, kitdb.Options{})
	run(t, quietSession, `CREATE TABLE notes (id bigint PRIMARY KEY, body text)`)
	quietMade := filepath.Join(t.TempDir(), "kitwork.db")
	quiet, err := kitdb.Open(ctx, quietMade, kitdb.Options{HistoryOfEveryTable: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := LoadStructure(ctx, quiet, "boot", structureOf(t, quietPath)); err != nil {
		t.Fatal(err)
	}
	if err := quiet.Close(); err != nil {
		t.Fatal(err)
	}
	if quiet, err = kitdb.Open(ctx, quietMade, kitdb.Options{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quiet.Close() })
	before := quiet.Revision()
	if _, err := quiet.Write(ctx, "t", func(transaction *kitdb.Transaction) error {
		_, err := transaction.Exec(`INSERT INTO notes (id, body) VALUES (1, 'một')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if quiet.Revision() != before {
		t.Fatalf("the file made from a structure of no history, opened again: an insert into notes committed revision %d", quiet.Revision())
	}

	refused(t, session, `ALTER TABLE nope SET (history = on)`, "42P01", `relation "nope" does not exist`)
	if tag := run(t, session, `ALTER TABLE IF EXISTS nope SET (history = on)`); tag != "ALTER TABLE" {
		t.Fatalf("tag %q", tag)
	}
	refused(t, session, `CREATE TABLE other (id bigint) WITH (fillfactor = 70)`, "22023", `unrecognized parameter "fillfactor"`)
	refused(t, session, `ALTER TABLE notes SET (history = maybe)`, "22023", `invalid value for boolean option "history": maybe`)
	refused(t, session, `ALTER TABLE notes SET (history)`, "42601", "expected = after history")
	refused(t, session, `ALTER TABLE notes RESET (history = on)`, "42601", `unexpected "="`)
	refused(t, session, `ALTER TABLE kitwork_tables SET (history = on)`, "42501", `"kitwork_tables" is a table of KitDB's own`)
	if _, err := TranslateSchema(`CREATE TABLE other (id bigint) WITH (history = on)`); err == nil || !strings.Contains(err.Error(), "sets no table's history") {
		t.Fatalf("TranslateSchema of a table WITH (history = on): %v", err)
	}
}

// TestHistoryOfAFileOfBefore: a file of before (its history has commits, no kitwork_tables) keeps the history of every
// table it has. Its structure file says so for every table; setting one table's history through the port, by a writer
// that keeps every table's, leaves the others' on.
//
// CONTROL (07/11/2026): with SetHistory writing no other table when it makes kitwork_tables, "after ALTER TABLE a SET
// (history = off) the history is on for \"\", want b".
func TestHistoryOfAFileOfBefore(t *testing.T) {
	ctx := context.Background()
	path, database, session := historyFile(t, kitdb.Options{HistoryOfEveryTable: true})
	if _, err := database.Migrate(ctx, "t", `CREATE TABLE a (id INTEGER PRIMARY KEY) STRICT`, `CREATE TABLE b (id INTEGER PRIMARY KEY) STRICT`); err != nil {
		t.Fatal(err)
	}
	structure := structureOf(t, path)
	for _, want := range []string{"CREATE TABLE a (\n  id bigserial PRIMARY KEY\n) WITH (history = on);", "CREATE TABLE b (\n  id bigserial PRIMARY KEY\n) WITH (history = on);"} {
		if !strings.Contains(structure, want) {
			t.Fatalf("the structure file of a file of before:\n%s\nlacks %s", structure, want)
		}
	}
	session = historySession(t, path, database)
	run(t, session, `ALTER TABLE a SET (history = off)`)
	if got := historyOn(t, database); got != "b" {
		t.Fatalf("after ALTER TABLE a SET (history = off) the history is on for %q, want b", got)
	}
	structure = structureOf(t, path)
	if strings.Contains(structure, "CREATE TABLE a (\n  id bigserial PRIMARY KEY\n) WITH") || !strings.Contains(structure, "CREATE TABLE b (\n  id bigserial PRIMARY KEY\n) WITH (history = on);") {
		t.Fatalf("the structure file after ALTER TABLE a SET (history = off):\n%s", structure)
	}
}
