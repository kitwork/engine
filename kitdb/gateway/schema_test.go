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
// not written, "kinds of notes "", want at:text data:json done:bool id:integer price:decimal(10,2) title:text".
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
	if got := kindsOf(t, fixture.publicDB, "notes"); got != "at:text data:json done:bool id:integer price:decimal(10,2) title:text" {
		t.Fatalf("kinds of notes %q, want at:text data:json done:bool id:integer price:decimal(10,2) title:text", got)
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
	if got := kindsOf(t, fixture.publicDB, "notes"); got != "at:text data:json done:bool id:integer name:text price:decimal(10,2)" {
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
