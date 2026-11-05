package gateway

import (
	"context"
	"strings"
	"testing"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// kindsOf reads kitwork_columns of table in the file, "column:kind(precision,scale)" in column order.
func kindsOf(t *testing.T, database *kitdbsqlite.Database, table string) string {
	t.Helper()
	var kinds []string
	err := database.Read(context.Background(), func(snapshot *kitdbsqlite.Snapshot) error {
		rows, err := snapshot.Query(`SELECT column_name || ':' || kind || CASE WHEN kind = 'decimal' THEN '(' || precision || ',' || scale || ')'
			ELSE '' END FROM kitwork_columns WHERE table_name = ? ORDER BY column_name`, table)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var kind string
			if err := rows.Scan(&kind); err != nil {
				return err
			}
			kinds = append(kinds, kind)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(kinds, " ")
}

// TestSchemaStatementsThroughThePort: an admin makes a table as PostgreSQL writes one (serial, varchar, numeric,
// boolean, jsonb, defaults), and it is a STRICT table whose kinds are in kitwork_columns, so its decimal is written
// and read as a decimal in the same session; ALTER TABLE adds, renames and drops columns, CREATE and DROP INDEX make and
// take an index, DROP TABLE takes the table and its kinds; each one a commit of the history by the session's user. An
// account that is no admin is refused, and what SQLite cannot do is 0A000.
//
// CONTROL (05/11/2026): with the kinds not read again after the statement, "INSERT INTO notes (title, price, done)
// VALUES ('a', 12.5, 't'): constraint failed: cannot store REAL value in INTEGER column notes.price"; with the kinds
// not written, "kinds of notes "", want at:datetime data:json done:bool id:integer price:decimal(10,2) title:text".
func TestSchemaStatementsThroughThePort(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess()
	session := fixture.session(t, access, Options{})
	create := `CREATE TABLE IF NOT EXISTS public.notes (id serial PRIMARY KEY, title varchar(200) NOT NULL, ` +
		`price numeric(10, 2) DEFAULT 1.5, done boolean DEFAULT 'false', data jsonb, at timestamp with time zone)`
	refused(t, session, create, "42501", "only an admin")
	access.admin = true
	before := fixture.publicDB.Revision()
	if tag := run(t, session, create); tag != "CREATE TABLE" {
		t.Fatalf("tag %q", tag)
	}
	definition := stored(t, fixture.publicDB, `SELECT sql FROM sqlite_schema WHERE name = 'notes'`).(string)
	for _, want := range []string{`"id" INTEGER PRIMARY KEY`, `"title" TEXT NOT NULL`, `"price" INTEGER DEFAULT 150`, `"done" INTEGER DEFAULT 0`,
		`"data" TEXT`, `"at" TEXT`, `STRICT`} {
		if !strings.Contains(definition, want) {
			t.Fatalf("the table made: %s; want %s in it", definition, want)
		}
	}
	if got := kindsOf(t, fixture.publicDB, "notes"); got != "at:datetime data:json done:bool id:integer price:decimal(10,2) title:text" {
		t.Fatalf("kinds of notes %q, want at:datetime data:json done:bool id:integer price:decimal(10,2) title:text", got)
	}
	run(t, session, `INSERT INTO notes (title, price, done) VALUES ('a', 12.5, 't')`)
	run(t, session, `INSERT INTO notes (title) VALUES ('b')`)
	result, err := session.Execute(context.Background(), `SELECT price, done FROM notes ORDER BY id`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := cells(result); got != "12.50,t|1.50,f" {
		t.Fatalf("the note read back = %q, want 12.50,t|1.50,f", got)
	}

	run(t, session, `ALTER TABLE notes ADD COLUMN total numeric(12, 2), RENAME COLUMN title TO name`)
	if tag := run(t, session, `CREATE INDEX ON notes (name)`); tag != "CREATE INDEX" {
		t.Fatalf("tag %q", tag)
	}
	if index := stored(t, fixture.publicDB, `SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = 'notes_name_idx'`); index != int64(1) {
		t.Fatal("CREATE INDEX ON notes (name) made no notes_name_idx")
	}
	run(t, session, `DROP INDEX public.notes_name_idx`)
	run(t, session, `ALTER TABLE notes DROP COLUMN IF EXISTS total, DROP COLUMN IF EXISTS nothing`)
	if got := kindsOf(t, fixture.publicDB, "notes"); got != "at:datetime data:json done:bool id:integer name:text price:decimal(10,2)" {
		t.Fatalf("kinds after ALTER: %q", got)
	}
	run(t, session, `ALTER TABLE notes ADD COLUMN IF NOT EXISTS name text`) // there: nothing to do

	for _, statement := range []struct{ text, want string }{
		{`ALTER TABLE notes ALTER COLUMN name TYPE integer`, "ALTER COLUMN"},
		{`ALTER TABLE notes ADD COLUMN amount numeric`, "numeric(precision, scale)"},
		{`ALTER TABLE notes ADD COLUMN tags text[]`, "arrays"},
		{`ALTER TABLE notes ADD COLUMN shape point`, "type point is not supported"},
		{`CREATE VIEW v AS SELECT 1`, "CREATE TABLE, ALTER TABLE"},
		{`DROP TABLE notes CASCADE`, "CASCADE"},
		{`CREATE TABLE refs (id serial PRIMARY KEY, invoice integer REFERENCES billing.invoices (id))`, "stays in its schema's file"},
		{`CREATE TABLE odd (id integer PRIMARY KEY, n serial)`, "serial is the table's PRIMARY KEY only"},
		{`CREATE TABLE checked (id serial PRIMARY KEY, amount numeric(8, 2) CHECK (amount > 0))`, "CHECK"},
		{`CREATE INDEX hashed ON notes USING hash (name)`, "index method hash"},
	} {
		refused(t, session, statement.text, "0A000", statement.want)
	}
	run(t, session, `DROP TABLE notes`)
	if got := kindsOf(t, fixture.publicDB, "notes"); got != "" {
		t.Fatalf("kinds after DROP TABLE: %q", got)
	}
	statements := 0
	for _, commit := range changesSince(t, fixture.publicDB, before) {
		for _, change := range commit.Changes {
			if change.Operation == kitdbsqlite.Statement {
				statements++
				if change.By != "port:alice" {
					t.Fatalf("schema statement by %q", change.By)
				}
			}
		}
	}
	if statements < 6 {
		t.Fatalf("%d schema statements in the history, want the 6 commits' and more", statements)
	}
	// in a block: before a write it commits by itself; after one, it is refused
	run(t, session, `BEGIN`)
	run(t, session, `CREATE TABLE later (id serial PRIMARY KEY)`)
	access.allowed["insert public.orders"] = true
	run(t, session, `INSERT INTO orders (id, amount) VALUES (90, 1)`)
	refused(t, session, `CREATE TABLE refused (id serial PRIMARY KEY)`, "0A000", "commit first")
	run(t, session, `ROLLBACK`)
	if later := stored(t, fixture.publicDB, `SELECT count(*) FROM sqlite_schema WHERE name = 'later'`); later != int64(1) {
		t.Fatal("the table made in the block before its write went with the ROLLBACK")
	}
}

// TestSchemaStatementsTakeTheEnginesKinds: the port's schema statements say every kind of the engine's table() in
// PostgreSQL's words (KITWORK_DATABASE.md "Database là gốc"): kitid, KitDB's own domain, whose CREATE DOMAIN kitid AS
// text is taken and does nothing; date; timestamptz, the engine's timestamp(). timestamp without a time zone, time and
// uuid stay text. Any other domain is refused.
//
// CONTROL (05/11/2026): with typeOf mapping date to text as before, "kinds of events "at:datetime day:text id:kitid
// local:text ref:text starts:text"".
func TestSchemaStatementsTakeTheEnginesKinds(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess()
	access.admin = true
	session := fixture.session(t, access, Options{})
	if tag := run(t, session, `CREATE DOMAIN kitid AS text`); tag != "CREATE DOMAIN" {
		t.Fatalf("CREATE DOMAIN kitid AS text: tag %q", tag)
	}
	refused(t, session, `CREATE DOMAIN money AS numeric(12, 2)`, "0A000", "the one domain is KitDB's own")
	run(t, session, `CREATE TABLE events (id kitid PRIMARY KEY NOT NULL, day date NOT NULL, at timestamptz, `+
		`local timestamp without time zone, starts time, ref uuid)`)
	if got := kindsOf(t, fixture.publicDB, "events"); got != "at:datetime day:date id:kitid local:text ref:text starts:text" {
		t.Fatalf("kinds of events %q", got)
	}
	definition := stored(t, fixture.publicDB, `SELECT sql FROM sqlite_schema WHERE name = 'events'`).(string)
	if !strings.Contains(definition, `"id" TEXT PRIMARY KEY NOT NULL`) || !strings.Contains(definition, `"at" TEXT`) {
		t.Fatalf("the table made: %s", definition)
	}
	for _, statement := range []string{`CREATE TABLE t (id kitid PRIMARY KEY)`, `ALTER TABLE t ADD COLUMN at timestamptz`, kitidDomain + ";"} {
		if _, err := TranslateSchema(statement); err != nil {
			t.Fatalf("TranslateSchema(%q): %v", statement, err)
		}
	}
	if _, err := TranslateSchema(`DROP TABLE t`); err == nil {
		t.Fatal("TranslateSchema took DROP TABLE, which kitwork migrate never writes")
	}
}

// TestSchemaStatementsMakeTheSearchIndex: a table's full-text index is a statement of the database (KITWORK_DATABASE.md
// "Database là gốc"): CREATE INDEX <table>_search ON <table> USING search (columns) WITH (column = weight) sets the
// columns the port's MATCH and an app's matches read, as written (a column out of it leaves it); DROP INDEX
// <table>_search takes them all out. One per table, by that name; no b-tree index takes the name.
//
// CONTROL (05/11/2026): with searchIndex setting the columns without taking the others out first, "weights after the
// second CREATE "body:1 title:3"".
func TestSchemaStatementsMakeTheSearchIndex(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess()
	access.admin = true
	session := fixture.session(t, access, Options{})
	run(t, session, `CREATE TABLE posts (id kitid PRIMARY KEY NOT NULL, title text NOT NULL, body text)`)
	weights := func() string {
		t.Helper()
		var found []string
		err := fixture.publicDB.Read(context.Background(), func(snapshot *kitdbsqlite.Snapshot) error {
			rows, err := snapshot.Query(`SELECT column_name || ':' || search_weight FROM kitwork_columns WHERE table_name = 'posts' AND search_weight > 0 ORDER BY column_name`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var weight string
				if err := rows.Scan(&weight); err != nil {
					return err
				}
				found = append(found, weight)
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(found, " ")
	}
	if tag := run(t, session, `CREATE INDEX posts_search ON posts USING search (title, body) WITH (title = 2)`); tag != "CREATE INDEX" {
		t.Fatalf("tag %q", tag)
	}
	if got := weights(); got != "body:1 title:2" {
		t.Fatalf("weights %q", got)
	}
	run(t, session, `CREATE INDEX posts_search ON posts USING search (title) WITH (title = 3)`)
	if got := weights(); got != "title:3" {
		t.Fatalf("weights after the second CREATE %q", got)
	}
	refused(t, session, `CREATE INDEX finder ON posts USING search (title)`, "0A000", "one search index, named posts_search")
	refused(t, session, `CREATE INDEX posts_search ON posts (title)`, "0A000", "the name of table posts's search index")
	refused(t, session, `CREATE INDEX posts_search ON posts USING search (title) WITH (body = 2)`, "42601", "of a column the index reads")
	run(t, session, `DROP INDEX posts_search`)
	if got := weights(); got != "" {
		t.Fatalf("weights after DROP INDEX %q", got)
	}
	if statements, err := TranslateSchema(`CREATE INDEX posts_search ON posts USING search (title)`); err != nil || len(statements) != 0 {
		t.Fatalf("TranslateSchema of a search index: %q %v", statements, err)
	}
}

// TestSchemaStatementsRescaleANumber: ALTER COLUMN … TYPE changes the scale of a number column, its stored values
// rewritten in the commit that writes its kind (KITWORK_DATABASE.md "Database là gốc", mục 6): numeric(10, 2) to
// numeric(10, 4) multiplies, to bigint divides rounding half away from zero (12.50 is 13, -0.50 is -1), each read back
// as the type says. A value the new scale carries past the precision refuses it (22003), the values kept; a change of
// another type is a rebuild of the table SQLite needs (0A000).
//
// CONTROL (06/11/2026): with the rewrites not run, "after numeric(10, 4): 0.1250|-0.0050" (the kind changed alone).
func TestSchemaStatementsRescaleANumber(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess()
	access.admin = true
	session := fixture.session(t, access, Options{})
	run(t, session, `CREATE TABLE prices (id bigserial PRIMARY KEY, amount numeric(10, 2), small numeric(4, 2))`)
	run(t, session, `INSERT INTO prices (amount, small) VALUES (12.5, 99.99), (-0.5, 1)`)
	read := func(query string) string {
		t.Helper()
		result, err := session.Execute(context.Background(), query, nil)
		if err != nil {
			t.Fatal(err)
		}
		return cells(result)
	}
	if tag := run(t, session, `ALTER TABLE prices ALTER COLUMN amount TYPE numeric(10, 4)`); tag != "ALTER TABLE" {
		t.Fatalf("tag %q", tag)
	}
	if got := read(`SELECT amount FROM prices ORDER BY id`); got != "12.5000|-0.5000" {
		t.Fatalf("after numeric(10, 4): %s", got)
	}
	run(t, session, `ALTER TABLE prices ALTER COLUMN amount SET DATA TYPE bigint`)
	if got := read(`SELECT amount FROM prices ORDER BY id`); got != "13|-1" {
		t.Fatalf("after bigint: %s", got)
	}
	if got := kindsOf(t, fixture.publicDB, "prices"); !strings.Contains(got, "amount:integer") {
		t.Fatalf("kinds after bigint: %s", got)
	}
	refused(t, session, `ALTER TABLE prices ALTER COLUMN small TYPE numeric(4, 3)`, "22003", "numeric field overflow: 1 values of column small")
	if got := read(`SELECT small FROM prices ORDER BY id`); got != "99.99|1.00" {
		t.Fatalf("after the refusal: %s", got)
	}
	refused(t, session, `ALTER TABLE prices ALTER COLUMN amount TYPE text`, "0A000", "changes the scale of a number only")
	refused(t, session, `ALTER TABLE prices ALTER COLUMN amount SET NOT NULL`, "0A000", "NOT NULL or DEFAULT is not supported")
}
