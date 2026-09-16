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

func TestReferentialDefaultPostgresWire(t *testing.T) {
	e := triggerTestOpen(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	done := make(chan error, 1)
	go func() {
		done <- e.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "defaults", User: "kitdb", Password: "test-only"}})
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
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://kitdb:test-only@%s/defaults?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	exec := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	state := func(err error, want string) {
		t.Helper()
		var sqlError interface{ SQLState() string }
		if !errors.As(err, &sqlError) || sqlError.SQLState() != want {
			t.Fatalf("SQLSTATE %s: %v", want, err)
		}
	}
	for _, q := range []string{
		`CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, code INTEGER DEFAULT 0 REFERENCES p(code) ON UPDATE SET DEFAULT ON DELETE SET DEFAULT)`,
		`CREATE INDEX c_code ON c(code)`,
		`CREATE TABLE audit(id SERIAL PRIMARY KEY, old_code INTEGER, new_code INTEGER)`,
		`CREATE TRIGGER changed AFTER UPDATE ON c FOR EACH ROW INSERT INTO audit(old_code,new_code) VALUES(OLD.code,NEW.code)`,
		`INSERT INTO p VALUES(0,0),(1,1)`, `INSERT INTO c VALUES(1,1)`,
	} {
		exec(q)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SAVEPOINT original`); err != nil {
		t.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `UPDATE p SET code=$1 WHERE id=$2 RETURNING code`)
	if err != nil {
		t.Fatal(err)
	}
	var code int
	if err := statement.QueryRowContext(ctx, 2, 1).Scan(&code); err != nil || code != 2 {
		t.Fatal(code, err)
	}
	statement.Close()
	if err := tx.QueryRowContext(ctx, `SELECT code FROM c`).Scan(&code); err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT reassigned`); err != nil {
		t.Fatal(err)
	}
	// An unchanged default is not an escape from foreign-key validation.
	_, err = tx.ExecContext(ctx, `DELETE FROM p WHERE id=$1`, 0)
	state(err, "23503")
	_, err = tx.ExecContext(ctx, `SELECT code FROM c`)
	state(err, "25P02")
	if _, err := tx.ExecContext(ctx, `ROLLBACK TO reassigned`); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`).Scan(&code); err != nil || code != 1 {
		t.Fatal(code, err)
	}
	if _, err := tx.ExecContext(ctx, `ROLLBACK TO original`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	actionRows(t, e, `SELECT code FROM c`, "[[1]]")
	triggerTestCount(t, e, "audit", 0)
	exec(`CREATE TABLE guard(id INTEGER PRIMARY KEY, code INTEGER REFERENCES p(code))`)
	exec(`CREATE TRIGGER invalid AFTER UPDATE ON c FOR EACH ROW INSERT INTO guard(id,code) VALUES(NEW.id,99)`)
	_, err = db.ExecContext(ctx, `DELETE FROM p WHERE id=$1`, 1)
	state(err, "23503")
	triggerTestCount(t, e, "p", 2)
	triggerTestCount(t, e, "audit", 0)
	triggerTestCount(t, e, "guard", 0)
	actionRows(t, e, `SELECT code FROM c`, "[[1]]")
	exec(`DROP TRIGGER invalid ON c`)
	_, err = db.ExecContext(ctx, `DELETE FROM p WHERE id=1 RETURNING 1/(id-1)`)
	if err == nil {
		t.Fatal("late RETURNING failure accepted")
	}
	triggerTestCount(t, e, "p", 2)
	triggerTestCount(t, e, "audit", 0)
	actionRows(t, e, `SELECT code FROM c`, "[[1]]")
	result, err := db.ExecContext(ctx, `DELETE FROM p WHERE id=$1`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatal(affected, err)
	}
	actionRows(t, e, `SELECT code FROM c`, "[[0]]")
	actionRows(t, e, `SELECT old_code,new_code FROM audit`, "[[1 0]]")
}
