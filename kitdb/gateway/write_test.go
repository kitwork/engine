package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/lib/pq"
)

// writeFixture is one database whose files the engine's writer holds: kitwork.db (public) with orders (amount a
// decimal(12, 2), stored × 100), users (active a boolean), secret, and items whose foreign key deletes them with
// their user; billing.db (billing) with invoices.
type writeFixture struct {
	public, billing     string
	publicDB, billingDB *kitdbsqlite.Database
}

func newWriteFixture(t *testing.T) *writeFixture {
	t.Helper()
	ctx := context.Background()
	directory := filepath.Join(t.TempDir(), "application")
	fixture := &writeFixture{public: filepath.Join(directory, "kitwork.db"), billing: filepath.Join(directory, "billing.db")}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	open := func(path string, statements ...string) *kitdbsqlite.Database {
		database, err := kitdbsqlite.Open(ctx, path, kitdbsqlite.Options{HistoryOfEveryTable: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		var schema, rows []string
		for _, statement := range statements {
			if strings.HasPrefix(statement, "INSERT") {
				rows = append(rows, statement)
			} else {
				schema = append(schema, statement)
			}
		}
		if _, err := database.Migrate(ctx, "test", schema...); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Write(ctx, "test", func(transaction *kitdbsqlite.Transaction) error {
			for _, statement := range rows {
				if _, err := transaction.Exec(statement); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return database
	}
	fixture.publicDB = open(fixture.public,
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, active INTEGER NOT NULL) STRICT",
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER, amount INTEGER) STRICT",
		"CREATE TABLE secret (id INTEGER PRIMARY KEY, code TEXT) STRICT",
		"CREATE TABLE items (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users (id) ON DELETE CASCADE) STRICT",
		"INSERT INTO users VALUES (1, 'An', 1), (2, 'Binh', 0)",
		"INSERT INTO orders VALUES (1, 1, 45000), (2, 1, -500)",
		"INSERT INTO secret VALUES (1, 's1')",
		"INSERT INTO items VALUES (1, 1), (2, 1)",
	)
	fixture.billingDB = open(fixture.billing,
		"CREATE TABLE invoices (id INTEGER PRIMARY KEY, user_id INTEGER, total INTEGER) STRICT",
		"INSERT INTO invoices VALUES (10, 1, 1999)",
	)
	return fixture
}

// writers hands the session the fixture's writers, as the engine hands its own.
type writers map[string]*kitdbsqlite.Database

func (files writers) Open(path string) (Writer, error) {
	database := files[path]
	if database == nil {
		return nil, errors.New("no writer for " + path)
	}
	return heldWriter{database}, nil
}

type heldWriter struct{ *kitdbsqlite.Database }

func (heldWriter) Close() error { return nil } // the fixture closes the database

func (fixture *writeFixture) session(t *testing.T, access *fakeAccess, options Options) *Session {
	t.Helper()
	options.Writers = writers{fixture.public: fixture.publicDB, fixture.billing: fixture.billingDB}
	options.User = "alice"
	session, err := Open(context.Background(), Target{Name: "demo.kitwork.io/application",
		Schemas: map[string]string{"public": fixture.public, "billing": fixture.billing}}, access, fakeColumns{}, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// run executes text with text parameters and returns its tag, failing the test on an error.
func run(t *testing.T, session *Session, text string, parameters ...string) string {
	t.Helper()
	result, err := session.Execute(context.Background(), text, textParameters(parameters...))
	if err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return result.CommandTag
}

// refused executes text and wants the error of code saying want.
func refused(t *testing.T, session *Session, text, code, want string, parameters ...string) {
	t.Helper()
	_, err := session.Execute(context.Background(), text, textParameters(parameters...))
	var protocol *pgwire.Error
	if !errors.As(err, &protocol) || protocol.Code != code || !strings.Contains(protocol.Message, want) {
		t.Fatalf("%s: %v, want %s saying %q", text, err, code, want)
	}
}

// stored reads one value of the file as the engine wrote it, through the writer's own reads.
func stored(t *testing.T, database *kitdbsqlite.Database, query string) any {
	t.Helper()
	var value any
	err := database.Read(context.Background(), func(snapshot *kitdbsqlite.Snapshot) error {
		rows, err := snapshot.Query(query)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			return errors.New("no row")
		}
		return rows.Scan(&value)
	})
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}

// changesSince are the commits of the file's history after revision.
func changesSince(t *testing.T, database *kitdbsqlite.Database, revision uint64) []kitdbsqlite.Commit {
	t.Helper()
	var commits []kitdbsqlite.Commit
	if err := database.Changes(context.Background(), revision, func(commit kitdbsqlite.Commit) error {
		commits = append(commits, commit)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return commits
}

// TestWritesGoThroughTheWriterAndItsHistory: INSERT, UPDATE and DELETE through the port change the file through the
// engine's writer, each a commit of the history by the session's user, a decimal written as its value stored × 100;
// the session's reads then see them as values.
//
// CONTROL (05/11/2026): with the schema's name left in the statement, "UPDATE public.orders SET amount = $1 WHERE id =
// $2: no such table: public.orders"; with the decimal written as given, "INSERT INTO orders (id, user_id, amount) VALUES
// (10, 2, 12.5): constraint failed: cannot store REAL value in INTEGER column orders.amount".
func TestWritesGoThroughTheWriterAndItsHistory(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess("public.orders", "insert public.orders", "update public.orders", "delete public.orders")
	session := fixture.session(t, access, Options{})
	before := fixture.publicDB.Revision()
	if tag := run(t, session, `INSERT INTO orders (id, user_id, amount) VALUES (10, 2, 12.5)`); tag != "INSERT 0 1" {
		t.Fatalf("tag %q", tag)
	}
	if amount := stored(t, fixture.publicDB, `SELECT amount FROM orders WHERE id = 10`); amount != int64(1250) {
		t.Fatalf("amount stored %v, want 1250", amount)
	}
	if tag := run(t, session, `UPDATE public.orders SET amount = $1 WHERE id = $2`, "-7.255", "10"); tag != "UPDATE 1" {
		t.Fatalf("tag %q", tag)
	}
	if amount := stored(t, fixture.publicDB, `SELECT amount FROM orders WHERE id = 10`); amount != int64(-726) {
		t.Fatalf("amount stored %v, want -726 (-7.255 rounded half away from zero)", amount)
	}
	result, err := session.Execute(context.Background(), `SELECT amount FROM orders WHERE id = 10`, nil)
	if err != nil || len(result.Rows) != 1 || string(result.Rows[0][0].Data) != "-7.26" {
		t.Fatalf("read back: %v %v", result.Rows, err)
	}
	if tag := run(t, session, `DELETE FROM "public".orders WHERE id = 10`); tag != "DELETE 1" {
		t.Fatalf("tag %q", tag)
	}
	commits := changesSince(t, fixture.publicDB, before)
	if len(commits) != 3 {
		t.Fatalf("%d commits, want 3", len(commits))
	}
	for _, commit := range commits {
		if len(commit.Changes) != 1 || commit.Changes[0].By != "port:alice" || commit.Changes[0].Table != "orders" {
			t.Fatalf("commit %d: %+v", commit.Revision, commit.Changes)
		}
	}
}

// TestWritesNeedTheirPrivileges: each verb needs its privilege on the table, an UPDATE or DELETE with a WHERE
// SELECT on it too, an INSERT … SELECT SELECT on what it reads; a write reads and writes one schema; a foreign key's
// action on other tables needs an admin; what is neither a row's statement nor a table's or an index's is not run.
//
// CONTROL (05/11/2026): with the privileges not asked, "INSERT INTO orders (id, amount) VALUES (20, 1): <nil>, want
// 42501 saying "permission denied for table public.orders""; with the foreign keys' actions ignored, "DELETE FROM users
// WHERE id = 1: <nil>, want 42501 saying "items ON DELETE CASCADE"".
func TestWritesNeedTheirPrivileges(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess("public.users")
	session := fixture.session(t, access, Options{})
	refused(t, session, `INSERT INTO orders (id, amount) VALUES (20, 1)`, "42501", "permission denied for table public.orders")
	access.allowed["insert public.orders"] = true
	run(t, session, `INSERT INTO orders (id, amount) VALUES (20, 1)`)
	access.allowed["update public.orders"] = true
	refused(t, session, `UPDATE orders SET user_id = 2 WHERE id = 20`, "42501", "permission denied for table public.orders")
	access.allowed["public.orders"] = true
	run(t, session, `UPDATE orders SET user_id = 2 WHERE id = 20`)
	refused(t, session, `INSERT INTO orders (id, user_id) SELECT id + 100, length(code) FROM secret`, "42501", "permission denied for table public.secret")
	refused(t, session, `INSERT INTO orders (id, user_id) SELECT id + 100, user_id FROM billing.invoices`, "0A000", "reads tables of schema public only")
	refused(t, session, `INSERT INTO orders (id) SELECT oid FROM pg_class LIMIT 1`, "0A000", "the catalog")
	refused(t, session, `DELETE FROM orders WHERE id = 20`, "42501", "permission denied for table public.orders")
	access.allowed["delete public.orders"] = true
	run(t, session, `DELETE FROM orders WHERE id = 20`)
	refused(t, session, `DELETE FROM users WHERE id = 1`, "42501", "permission denied for table public.users")
	access.allowed["delete public.users"] = true
	refused(t, session, `DELETE FROM users WHERE id = 1`, "42501", "items ON DELETE CASCADE")
	access.admin = true
	if tag := run(t, session, `DELETE FROM users WHERE id = 1`); tag != "DELETE 1" {
		t.Fatalf("tag %q", tag)
	}
	if items := stored(t, fixture.publicDB, `SELECT count(*) FROM items`); items != int64(0) {
		t.Fatalf("%v items left, want their user's foreign key to delete them", items)
	}
	refused(t, session, `CREATE VIEW extra AS SELECT 1`, "0A000", "CREATE TABLE, ALTER TABLE")
	refused(t, session, `PRAGMA user_version = 3`, "0A000", "not supported through the port")
}

// TestWritesMakeValuesTheFiles: a decimal's value given as a number, a string or a parameter is stored × 10^scale,
// rounded to the scale, within its precision; a boolean written t/false is 1/0; a decimal column read in a WHERE or
// an expression, or filled from a SELECT, is refused rather than misread.
//
// CONTROL (05/11/2026): with a decimal in the WHERE not refused, "UPDATE orders SET user_id = 1 WHERE amount > 100:
// <nil>, want 0A000 saying "does not read it""; with the rows after the first of VALUES not converted, "INSERT INTO
// orders (amount, id) VALUES ('1.5', 30), (+3, 31), …: constraint failed: cannot store REAL value in INTEGER column
// orders.amount".
func TestWritesMakeValuesTheFiles(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess("public.orders", "insert public.orders", "update public.orders", "public.users", "insert public.users",
		"update public.users")
	session := fixture.session(t, access, Options{})
	run(t, session, `INSERT INTO orders (amount, id) VALUES ('1.5', 30), (+3, 31), (-0.004, 32), ($1, 33), (NULL, 34)`, "2e3")
	for id, want := range map[int]any{30: int64(150), 31: int64(300), 32: int64(0), 33: int64(200000), 34: nil} {
		if amount := stored(t, fixture.publicDB, `SELECT amount FROM orders WHERE id = `+strconv.Itoa(id)); amount != want {
			t.Fatalf("order %d stored %v, want %v", id, amount, want)
		}
	}
	run(t, session, `INSERT INTO orders VALUES (35, 1, 9.999)`) // no column list: every column in order
	if amount := stored(t, fixture.publicDB, `SELECT amount FROM orders WHERE id = 35`); amount != int64(1000) {
		t.Fatalf("order 35 stored %v, want 1000", amount)
	}
	run(t, session, `INSERT INTO orders (id, amount) VALUES (36, 1) ON CONFLICT (id) DO UPDATE SET amount = excluded.amount`)
	run(t, session, `INSERT INTO orders (id, amount) VALUES (36, 2.5) ON CONFLICT (id) DO UPDATE SET amount = excluded.amount`)
	if amount := stored(t, fixture.publicDB, `SELECT amount FROM orders WHERE id = 36`); amount != int64(250) {
		t.Fatalf("order 36 stored %v, want 250", amount)
	}
	refused(t, session, `INSERT INTO orders (id, amount) VALUES (40, 12345678901.5)`, "22003", "numeric field overflow")
	refused(t, session, `INSERT INTO orders (id, amount) VALUES (40, 'many')`, "22P02", "invalid input syntax for type numeric")
	refused(t, session, `UPDATE orders SET amount = amount * 2 WHERE id = 30`, "0A000", "not an expression")
	refused(t, session, `UPDATE orders SET user_id = 1 WHERE amount > 100`, "0A000", "does not read it")
	refused(t, session, `INSERT INTO orders (id, amount) SELECT 41, 5`, "0A000", "not from a SELECT")
	run(t, session, `UPDATE users SET active = 'false' WHERE id = 1`)
	run(t, session, `INSERT INTO users (id, name, active) VALUES (3, 'Chi', $1), (4, 'Dung', TRUE)`, "t")
	for id, want := range map[int]int64{1: 0, 3: 1, 4: 1} {
		if active := stored(t, fixture.publicDB, `SELECT active FROM users WHERE id = `+strconv.Itoa(id)); active != want {
			t.Fatalf("user %d active %v, want %d", id, active, want)
		}
	}
	refused(t, session, `UPDATE users SET active = 'maybe' WHERE id = 1`, "22P02", "invalid input syntax for type boolean")
	refused(t, session, `INSERT INTO orders (id, amount) VALUES (42, 1) RETURNING id`, "0A000", "RETURNING")
}

// TestABlockHoldsTheWriterUntilItEnds: a block's writes commit together at COMMIT, as one commit of the history, and
// go at ROLLBACK; a failed statement fails the block, whose COMMIT then rolls back; a block that waits longer than
// WriteIdle is rolled back and lets the writer go; the engine's own write waits while a block holds the writer.
//
// A long statement of a block is canceled with its context, the block rolled back and the writer free at once.
//
// CONTROL (05/11/2026): with the idle timer not set, "the engine's write waited 30.0019215s for a block that waits"
// (the block's whole limit); with each statement of a block committed by itself, "3 commits for the block, want 1 of 2
// changes"; with a block's statement not canceled with its context, the test runs until its own limit (the INSERT of
// half a billion rows goes on).
func TestABlockHoldsTheWriterUntilItEnds(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess("public.orders", "insert public.orders", "update public.orders", "delete public.orders")
	session := fixture.session(t, access, Options{WriteIdle: 300 * time.Millisecond})
	ctx := context.Background()

	before := fixture.publicDB.Revision()
	run(t, session, `BEGIN`)
	run(t, session, `INSERT INTO orders (id, amount) VALUES (50, 1)`)
	run(t, session, `INSERT INTO orders (id, amount) VALUES (55, 1)`)
	run(t, session, `UPDATE orders SET amount = 2 WHERE id = 50`)
	if tag := run(t, session, `COMMIT`); tag != "COMMIT" {
		t.Fatalf("tag %q", tag)
	}
	if commits := changesSince(t, fixture.publicDB, before); len(commits) != 1 || len(commits[0].Changes) != 2 {
		t.Fatalf("%d commits for the block, want 1 of 2 changes", len(commits))
	}
	if amount := stored(t, fixture.publicDB, `SELECT amount FROM orders WHERE id = 50`); amount != int64(200) {
		t.Fatalf("amount %v, want 200", amount)
	}

	run(t, session, `BEGIN`)
	run(t, session, `DELETE FROM orders WHERE id = 50`)
	if tag := run(t, session, `ROLLBACK`); tag != "ROLLBACK" {
		t.Fatalf("tag %q", tag)
	}
	if count := stored(t, fixture.publicDB, `SELECT count(*) FROM orders WHERE id = 50`); count != int64(1) {
		t.Fatal("a rolled back DELETE was kept")
	}

	run(t, session, `BEGIN`)
	run(t, session, `INSERT INTO orders (id, amount) VALUES (51, 1)`)
	refused(t, session, `INSERT INTO orders (id, amount) VALUES (50, 1)`, "23505", "UNIQUE")
	refused(t, session, `INSERT INTO orders (id, amount) VALUES (52, 1)`, "25P02", "aborted")
	if tag := run(t, session, `COMMIT`); tag != "ROLLBACK" {
		t.Fatalf("COMMIT of a failed block: tag %q", tag)
	}
	if count := stored(t, fixture.publicDB, `SELECT count(*) FROM orders WHERE id = 51`); count != int64(0) {
		t.Fatal("the write of a failed block was kept")
	}

	// the engine writes while a block holds the writer: it waits for the block, which ends by its idle limit
	run(t, session, `BEGIN`)
	run(t, session, `INSERT INTO orders (id, amount) VALUES (53, 1)`)
	start := time.Now()
	if _, err := fixture.publicDB.Write(ctx, "engine", func(transaction *kitdbsqlite.Transaction) error {
		_, err := transaction.Exec(`INSERT INTO orders (id, amount) VALUES (54, 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	waited := time.Since(start)
	if waited < 200*time.Millisecond || waited > 3*time.Second {
		t.Fatalf("the engine's write waited %s for a block that waits", waited)
	}
	refused(t, session, `UPDATE orders SET amount = 3 WHERE id = 53`, "25P03", "rolled back")
	if tag := run(t, session, `COMMIT`); tag != "ROLLBACK" {
		t.Fatalf("COMMIT after the idle limit: tag %q", tag)
	}
	if count := stored(t, fixture.publicDB, `SELECT count(*) FROM orders WHERE id IN (53, 54)`); count != int64(1) {
		t.Fatalf("%v of the block's and the engine's rows, want the engine's alone", count)
	}
	refused(t, session, `BEGIN IMMEDIATE`, "0A000", "first write")

	// a long statement of a block is canceled with its context: the block goes, and the writer is free at once
	run(t, session, `BEGIN`)
	statementContext, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	_, err := session.Execute(statementContext, `INSERT INTO orders (id) WITH RECURSIVE n(i) AS (SELECT 1000 UNION ALL SELECT i + 1 FROM n
		WHERE i < 500000000) SELECT i FROM n`, nil)
	cancel()
	if codeOf(err) != "57014" {
		t.Fatalf("a canceled statement of a block: %v, want 57014", err)
	}
	start = time.Now()
	if _, err := fixture.publicDB.Write(ctx, "engine", func(transaction *kitdbsqlite.Transaction) error {
		_, err := transaction.Exec(`INSERT INTO orders (id, amount) VALUES (56, 1)`)
		return err
	}); err != nil || time.Since(start) > time.Second {
		t.Fatalf("the engine's write after the cancel: %v after %s", err, time.Since(start))
	}
	if tag := run(t, session, `ROLLBACK`); tag != "ROLLBACK" {
		t.Fatalf("tag %q", tag)
	}
	if count := stored(t, fixture.publicDB, `SELECT count(*) FROM orders WHERE id >= 1000`); count != int64(0) {
		t.Fatalf("%v rows of the canceled statement kept", count)
	}
}

// writeAuthenticator opens a session of the write fixture for the cleartext password "secret".
type writeAuthenticator struct {
	fixture *writeFixture
	access  *fakeAccess
}

func (authenticator writeAuthenticator) Authenticate(ctx context.Context, startup pgwire.Startup, password string) (pgwire.Session, error) {
	if password != "secret" {
		return nil, pgwire.NewError("28P01", "password authentication failed for user "+startup.User())
	}
	fixture := authenticator.fixture
	return Open(ctx, Target{Name: "demo.kitwork.io/application", Schemas: map[string]string{"public": fixture.public, "billing": fixture.billing}},
		authenticator.access, fakeColumns{}, Options{User: startup.User(), Writers: writers{fixture.public: fixture.publicDB, fixture.billing: fixture.billingDB}})
}

// TestWritesOverTheWire: through pgwire.Server and lib/pq, as psql or a database tool writes: a statement with
// parameters by the extended protocol, an sql.Tx committed and one rolled back, and one simple query of several
// statements, in an implicit transaction that an error rolls back whole.
//
// CONTROL (05/11/2026): with the server not splitting a simple query, "BEGIN; UPDATE …; COMMIT: pq: cannot execute more
// than one statement at a time in a read-only transaction"; with the implicit transaction not rolled back after the
// error, the next statement "pq: current transaction is aborted, commands ignored until end of transaction block".
func TestWritesOverTheWire(t *testing.T) {
	fixture := newWriteFixture(t)
	access := newAccess("public.orders", "insert public.orders", "update public.orders", "delete public.orders")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	server := pgwire.Server{Authenticator: writeAuthenticator{fixture: fixture, access: access}}
	go func() { served <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	client, err := sql.Open("postgres", fmt.Sprintf(
		"host=127.0.0.1 port=%d user=alice password=secret dbname=demo.kitwork.io/application sslmode=disable", listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.SetMaxOpenConns(1)
	amountOf := func(id int) string {
		t.Helper()
		var amount sql.NullString
		if err := client.QueryRow(`SELECT amount FROM orders WHERE id = $1`, id).Scan(&amount); err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		return amount.String
	}

	result, err := client.Exec(`INSERT INTO orders (id, user_id, amount) VALUES ($1, $2, $3)`, 70, 1, "19.99")
	if err != nil {
		t.Fatal(err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 || amountOf(70) != "19.99" {
		t.Fatalf("insert: %d row, amount %q", affected, amountOf(70))
	}

	tx, err := client.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE orders SET amount = $1 WHERE id = $2`, "20.5", 70); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = client.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM orders WHERE id = 70`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if amountOf(70) != "20.50" {
		t.Fatalf("after a commit and a rollback, amount %q, want 20.50", amountOf(70))
	}

	batch := `BEGIN; UPDATE orders SET amount = 1.25 WHERE id = 70; INSERT INTO orders (id, amount) VALUES (71, 2); COMMIT`
	if _, err := client.Exec(batch); err != nil {
		t.Fatalf("BEGIN; UPDATE …; COMMIT: %v", err)
	}
	if amountOf(70) != "1.25" || amountOf(71) != "2.00" {
		t.Fatalf("after the batch: %q %q", amountOf(70), amountOf(71))
	}
	var failure *pq.Error
	if _, err := client.Exec(`INSERT INTO orders (id, amount) VALUES (72, 1); INSERT INTO orders (id, amount) VALUES (71, 1)`); !errors.As(err, &failure) || failure.Code != "23505" {
		t.Fatalf("a batch whose second statement fails: %v, want 23505", err)
	}
	if amountOf(72) != "" {
		t.Fatal("order 72 kept after its batch failed")
	}
}
