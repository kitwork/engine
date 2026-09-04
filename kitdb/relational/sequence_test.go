package relational

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/pgwire"
)

type sequenceExecutor interface {
	Execute(context.Context, string, ...any) (Result, error)
}

func sequenceQuery(t *testing.T, executor sequenceExecutor, query string, want ...any) {
	t.Helper()
	result, err := executor.Execute(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if want != nil && !reflect.DeepEqual(result.Rows, [][]any{want}) {
		t.Fatalf("%s: %#v want %#v", query, result.Rows, want)
	}
}

func TestSQLSequenceSessionsAndTransactions(t *testing.T) {
	e := bigintTestEngine(t)
	ctx := context.Background()
	a, b := e.NewSession(), e.NewSession()
	sequenceQuery(t, e, `CREATE SEQUENCE order_ids START 9007199254740993`)
	sequenceQuery(t, e, `CREATE TABLE orders (id BIGINT PRIMARY KEY, title TEXT)`)
	if _, err := a.Execute(ctx, `SELECT currval('order_ids')`); err == nil {
		t.Fatal("currval leaked before nextval")
	}
	if _, err := a.Execute(ctx, `SELECT lastval()`); err == nil {
		t.Fatal("lastval leaked before nextval")
	}
	columns, err := e.Describe(ctx, `SELECT nextval('order_ids') AS id`)
	if err != nil || len(columns) != 1 || columns[0].Kind != "bigint" {
		t.Fatalf("describe: %v %v", columns, err)
	}
	sequenceQuery(t, a, `SELECT nextval('public.order_ids'), currval('order_ids'), lastval()`, int64(9007199254740993), int64(9007199254740993), int64(9007199254740993))
	if _, err := b.Execute(ctx, `SELECT currval('order_ids')`); err == nil {
		t.Fatal("session leaked currval")
	}
	sequenceQuery(t, b, `SELECT nextval('order_ids')`, int64(9007199254740994))
	sequenceQuery(t, a, `SELECT currval('order_ids')`, int64(9007199254740993))
	sequenceQuery(t, a, `SELECT setval('order_ids',100,false), currval('order_ids')`, int64(100), int64(9007199254740993))
	sequenceQuery(t, a, `SELECT nextval('order_ids'), lastval()`, int64(100), int64(100))
	sequenceQuery(t, a, `SELECT setval('order_ids',110,true), currval('order_ids'), lastval()`, int64(110), int64(110), int64(110))
	tx, err := a.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	sequenceQuery(t, tx, `INSERT INTO orders (id,title) VALUES (1,'committed')`)
	sequenceQuery(t, tx, `SELECT nextval('order_ids')`, int64(111))
	sequenceQuery(t, b, `SELECT nextval('order_ids')`, int64(112))
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatalf("sequence self-conflict: %v", err)
	}
	tx, err = a.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	sequenceQuery(t, tx, `INSERT INTO orders (id,title) VALUES (2,'rollback')`)
	sequenceQuery(t, tx, `SELECT nextval('order_ids')`, int64(113))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	sequenceQuery(t, a, `SELECT currval('order_ids')`, int64(113))
	sequenceQuery(t, b, `SELECT nextval('order_ids')`, int64(114))
	result, err := e.Execute(ctx, `SELECT id FROM orders ORDER BY id`)
	if err != nil || !reflect.DeepEqual(result.Rows, [][]any{{int64(1)}}) {
		t.Fatalf("rollback: %#v %v", result, err)
	}
	// Row writes still invalidate another transaction's snapshot.
	tx, err = a.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	sequenceQuery(t, tx, `INSERT INTO orders (id,title) VALUES (3,'stale')`)
	sequenceQuery(t, e, `INSERT INTO orders (id,title) VALUES (4,'new')`)
	if _, err := tx.Commit(ctx); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("lost conflict: %v", err)
	}
	readOnly, err := a.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Rollback()
	for _, source := range []string{`SELECT nextval('order_ids')`, `SELECT setval('order_ids',1)`} {
		if _, err := readOnly.Execute(ctx, source); err == nil {
			t.Fatalf("read-only accepted %s", source)
		}
	}
	sequenceQuery(t, readOnly, `SELECT currval('order_ids')`, int64(113))
}

func TestSQLSequenceLifecycleAndRejections(t *testing.T) {
	e := bigintTestEngine(t)
	s := e.NewSession()
	ctx := context.Background()
	sequenceQuery(t, s, `CREATE SEQUENCE s MINVALUE -9223372036854775808 MAXVALUE 9223372036854775807 START -9223372036854775808`)
	sequenceQuery(t, s, `SELECT nextval('s')`, int64(-9223372036854775808))
	sequenceQuery(t, s, `SELECT setval('s',-9223372036854775808,false), nextval('s')`, int64(-9223372036854775808), int64(-9223372036854775808))
	sequenceQuery(t, s, `ALTER SEQUENCE s RESTART WITH 5`)
	sequenceQuery(t, s, `SELECT nextval('s')`, int64(5))
	sequenceQuery(t, s, `CREATE SEQUENCE IF NOT EXISTS s START 100`)
	sequenceQuery(t, s, `SELECT nextval('s')`, int64(6))
	sequenceQuery(t, s, `SELECT nextval(NULL), setval('s',NULL), setval(NULL,2,false)`, nil, nil, nil)
	for _, source := range []string{
		`SELECT nextval('s') + 1`, `SELECT nextval('s'), 1`, `SELECT nextval('s',2)`, `SELECT nextval()`,
		`SELECT nextval('s'), setval('s','bad')`, `SELECT setval('s',2,'false')`,
		`SELECT nextval(lower('s'))`, `SELECT setval('s',9223372036854775808)`,
		`SELECT nextval('other.s')`, `CREATE TABLE s (id BIGINT PRIMARY KEY)`,
		`CREATE FUNCTION nextval(s TEXT) RETURNS BIGINT LANGUAGE SQL RETURN 1`,
		`CREATE FUNCTION impure() RETURNS BIGINT LANGUAGE SQL RETURN nextval('s')`,
	} {
		if _, err := s.Execute(ctx, source); err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
	sequenceQuery(t, s, `SELECT nextval('s')`, int64(7))
	sequenceQuery(t, s, `CREATE TABLE items (id BIGINT PRIMARY KEY, n TEXT)`)
	for _, source := range []string{`CREATE INDEX s ON items(n)`, `SELECT nextval('s') FROM items`} {
		if _, err := s.Execute(ctx, source); err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
	sequenceQuery(t, s, `CREATE INDEX items_n ON items(n)`)
	if _, err := s.Execute(ctx, `CREATE SEQUENCE items_n`); err == nil {
		t.Fatal("index/sequence name collision")
	}
	sequenceQuery(t, s, `DROP SEQUENCE s`)
	sequenceQuery(t, s, `CREATE SEQUENCE s`)
	if _, err := s.Execute(ctx, `SELECT currval('s')`); err == nil {
		t.Fatal("drop/recreate inherited currval")
	}
	if _, err := s.Execute(ctx, `SELECT lastval()`); err == nil {
		t.Fatal("drop/recreate inherited lastval")
	}
	sequenceQuery(t, s, `SELECT nextval('s')`, int64(1))
	sequenceQuery(t, s, `DROP SEQUENCE IF EXISTS missing`)
	sequenceQuery(t, s, `ALTER SEQUENCE IF EXISTS missing RESTART`)
}

func TestSQLSequenceCrashAndBackup(t *testing.T) {
	if path := os.Getenv("KITDB_SEQUENCE_CRASH_CHILD"); path != "" {
		e, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		sequenceQuery(t, e, `CREATE SEQUENCE s START 9007199254740993`)
		sequenceQuery(t, e, `SELECT nextval('s')`, int64(9007199254740993))
		os.Exit(32)
	}
	path := filepath.Join(t.TempDir(), "sequence.kitdb")
	child := exec.Command(os.Args[0], "-test.run=^TestSQLSequenceCrashAndBackup$")
	child.Env = append(os.Environ(), "KITDB_SEQUENCE_CRASH_CHILD="+path)
	output, err := child.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 32 {
		t.Fatalf("crash child: %v %s", err, output)
	}
	e, err := OpenWithOptions(path, Options{Kernel: kitdbengine.OpenOptions{RetainHistory: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	sequenceQuery(t, e, `SELECT nextval('s')`, int64(9007199254740994))
	anchor := filepath.Join(t.TempDir(), "backup.kitdb")
	if _, err := e.database.CreateBackupAnchor(context.Background(), anchor); err != nil {
		t.Fatal(err)
	}
	backup, err := Open(anchor)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	sequenceQuery(t, backup, `SELECT nextval('s')`, int64(9007199254740995))
	sequenceQuery(t, e, `SELECT nextval('s')`, int64(9007199254740995))
}

func TestSQLSequencePostgresWire(t *testing.T) {
	e := bigintTestEngine(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- e.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "sequences", User: "kitdb", Password: "test-only"}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://kitdb:test-only@%s/sequences?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, source := range []string{`CREATE SEQUENCE s START 9007199254740993`, `CREATE TABLE orders (id BIGINT PRIMARY KEY)`} {
		if _, err := a.ExecContext(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	statement, err := a.PrepareContext(ctx, `SELECT nextval($1) AS id`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	var value int64
	if err := statement.QueryRowContext(ctx, "s").Scan(&value); err != nil || value != 9007199254740993 {
		t.Fatalf("prepared: %d %v", value, err)
	}
	if err := b.QueryRowContext(ctx, `SELECT currval('s')`).Scan(&value); err == nil {
		t.Fatal("wire session leakage")
	}
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT nextval('s')`).Scan(&value); err != nil || value != 9007199254740994 {
		t.Fatalf("tx sequence: %d %v", value, err)
	}
	if _, err := tx.Exec(`INSERT INTO orders (id) VALUES ($1)`, value); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := a.QueryRowContext(ctx, `SELECT currval('s')`).Scan(&value); err != nil || value != 9007199254740994 {
		t.Fatalf("currval: %d %v", value, err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM information_schema.sequences WHERE sequence_schema='public' AND sequence_name='s'`,
		`SELECT COUNT(*) FROM pg_catalog.pg_class WHERE relkind='S' AND relname='s'`,
		`SELECT COUNT(*) FROM pg_catalog.pg_sequence`,
	} {
		if err := a.QueryRowContext(ctx, query).Scan(&value); err != nil || value != 1 {
			t.Fatalf("catalog %s: %d %v", query, value, err)
		}
	}
	if _, err := a.ExecContext(ctx, `DISCARD ALL`); err != nil {
		t.Fatal(err)
	}
	if err := a.QueryRowContext(ctx, `SELECT currval('s')`).Scan(&value); err == nil {
		t.Fatal("DISCARD ALL retained sequence state")
	}
	auth, err := e.PostgresAuthenticator(PostgresOptions{Database: "sequences", User: "kitdb", Password: "test-only", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	ro, err := auth.Authenticate(ctx, pgwire.Startup{Parameters: map[string]string{"user": "kitdb", "database": "sequences"}}, "test-only")
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.Execute(ctx, `SELECT nextval('s')`, nil); err == nil {
		t.Fatal("read-only authentication allowed sequence write")
	}
}
