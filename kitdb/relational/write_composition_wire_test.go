package relational

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestWriteCompositionPostgresWire(t *testing.T) {
	e := triggerTestOpen(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- e.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "wire", User: "kitdb", Password: "test-only"}})
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
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://kitdb:test-only@%s/wire?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	exec := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, n INTEGER CHECK(n>=0))`)
	exec(`CREATE TABLE audit (id SERIAL PRIMARY KEY, item INTEGER, n INTEGER CHECK(n<90))`)
	for _, event := range []string{"INSERT", "UPDATE"} {
		exec(fmt.Sprintf(`CREATE TRIGGER audit_%s AFTER %s ON items FOR EACH ROW INSERT INTO audit(item,n) VALUES(NEW.id,NEW.n)`, event, event))
	}
	exec(`INSERT INTO items VALUES(1,10)`)
	statement, err := db.PrepareContext(ctx, `INSERT INTO items SELECT CAST($1 AS INTEGER),CAST($2 AS INTEGER) ON CONFLICT(id) DO UPDATE SET n=items.n+excluded.n RETURNING id,n`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	var id, n int
	for _, amount := range []int{1, 2} {
		if err := statement.QueryRowContext(ctx, 1, amount).Scan(&id, &n); err != nil {
			t.Fatal(err)
		}
	}
	if id != 1 || n != 13 {
		t.Fatal(id, n)
	}
	state := func(err error, want string) {
		t.Helper()
		var sqlErr interface{ SQLState() string }
		if !errors.As(err, &sqlErr) || sqlErr.SQLState() != want {
			t.Fatalf("want SQLSTATE %s: %v", want, err)
		}
	}
	_, err = db.ExecContext(ctx, `SAVEPOINT outside`)
	state(err, "25P01")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	txExec := func(q string) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	txExec(`INSERT INTO items VALUES(2,20)`)
	txExec(`SAVEPOINT restore_here`)
	txExec(`INSERT INTO items SELECT id+10,n FROM items`)
	_, err = tx.ExecContext(ctx, `INSERT INTO items VALUES(1,40),(2,99) ON CONFLICT(id) DO UPDATE SET n=excluded.n`)
	if err == nil {
		t.Fatal("late trigger error accepted")
	}
	_, err = tx.ExecContext(ctx, `SELECT * FROM items`)
	state(err, "25P02")
	_, err = tx.ExecContext(ctx, `RELEASE restore_here`)
	state(err, "25P02")
	// Explicit prepare exercises Describe while the transaction is failed.
	rollback, err := tx.PrepareContext(ctx, `ROLLBACK TO SAVEPOINT restore_here`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rollback.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	rollback.Close()
	if err := tx.QueryRowContext(ctx, `SELECT SUM(n) FROM items`).Scan(&n); err != nil || n != 33 {
		t.Fatal(n, err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`).Scan(&n); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	_, err = tx.ExecContext(ctx, `ROLLBACK TO missing`)
	state(err, "3B001")
	txExec(`ROLLBACK TO restore_here`)
	_, err = tx.ExecContext(ctx, `INSERT INTO items VALUES(1,1),(1,2) ON CONFLICT(id) DO UPDATE SET n=excluded.n`)
	state(err, "21000")
	txExec(`ROLLBACK TO restore_here`)
	txExec(`RELEASE restore_here`)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT SUM(n) FROM items`).Scan(&n); err != nil || n != 33 {
		t.Fatal(n, err)
	}
	ro, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SAVEPOINT safe`, `ROLLBACK TO safe`, `RELEASE safe`} {
		if _, err := ro.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_, err = ro.ExecContext(ctx, `INSERT INTO items SELECT 3,3 ON CONFLICT DO NOTHING`)
	state(err, "25006")
	if err := ro.Rollback(); err != nil {
		t.Fatal(err)
	}
}
