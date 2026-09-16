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

func TestReferentialActionPostgresWire(t *testing.T) {
	e := triggerTestOpen(t)
	e.maximumMutationRows = 3
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	done := make(chan error, 1)
	go func() {
		done <- e.ServePostgres(ctx, listener, PostgresServerOptions{PostgresOptions: PostgresOptions{Database: "actions", User: "kitdb", Password: "test-only"}})
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
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://kitdb:test-only@%s/actions?sslmode=disable", listener.Addr()))
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
	count := func(table string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&got); err != nil || got != want {
			t.Fatal(table, got, want, err)
		}
	}
	for _, q := range []string{
		`CREATE TABLE p(id INTEGER PRIMARY KEY, code INTEGER UNIQUE)`,
		`CREATE TABLE c(id INTEGER PRIMARY KEY, code INTEGER REFERENCES p(code) ON UPDATE CASCADE ON DELETE CASCADE)`,
		`CREATE INDEX c_code ON c(code)`,
		`CREATE TABLE audit(id SERIAL PRIMARY KEY, value INTEGER)`,
		`CREATE TRIGGER changed AFTER UPDATE ON c FOR EACH ROW INSERT INTO audit(value) VALUES(NEW.code)`,
		`INSERT INTO p VALUES(1,1)`, `INSERT INTO c VALUES(1,1),(2,1)`,
	} {
		exec(q)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SAVEPOINT before_change`); err != nil {
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
	if err := tx.QueryRowContext(ctx, `SELECT SUM(code) FROM c`).Scan(&code); err != nil || code != 4 {
		t.Fatal(code, err)
	}
	if _, err := tx.ExecContext(ctx, `ROLLBACK TO before_change`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	count("audit", 0)
	exec(`INSERT INTO c VALUES(3,1),(4,1)`)
	_, err = db.ExecContext(ctx, `DELETE FROM p`)
	state(err, "54000")
	count("p", 1)
	count("c", 4)
	exec(`DELETE FROM c WHERE id=4`)
	exec(`CREATE TABLE required(id INTEGER PRIMARY KEY, pid INTEGER NOT NULL REFERENCES p(id) ON DELETE SET NULL)`)
	exec(`INSERT INTO required VALUES(1,1)`)
	// Remove one child so the NULL constraint, not the action budget, fails.
	exec(`DELETE FROM c WHERE id=3`)
	_, err = db.ExecContext(ctx, `DELETE FROM p`)
	state(err, "23502")
	count("p", 1)
	count("c", 2)
	exec(`DELETE FROM required`)
	exec(`CREATE TABLE g(id INTEGER PRIMARY KEY, cid INTEGER REFERENCES c(id))`)
	exec(`CREATE TRIGGER bad_audit AFTER DELETE ON c FOR EACH ROW INSERT INTO g(id,cid) VALUES(OLD.id,OLD.id)`)
	_, err = db.ExecContext(ctx, `DELETE FROM p`)
	state(err, "23503")
	count("p", 1)
	count("c", 2)
	count("g", 0)
	exec(`DROP TRIGGER bad_audit ON c`)
	// An unsupported physical child-key rewrite is a feature error, not UNIQUE.
	exec(`CREATE TABLE keyed(code INTEGER PRIMARY KEY REFERENCES p(code) ON UPDATE CASCADE)`)
	exec(`INSERT INTO keyed VALUES(1)`)
	_, err = db.ExecContext(ctx, `UPDATE p SET code=2`)
	state(err, "0A000")
	count("audit", 0)
	exec(`DELETE FROM keyed`)
	_, err = db.ExecContext(ctx, `UPDATE p SET code=2 RETURNING 1/(id-1)`)
	if err == nil {
		t.Fatal("late RETURNING failure accepted")
	}
	count("audit", 0)
	if err := db.QueryRowContext(ctx, `SELECT SUM(code) FROM c`).Scan(&code); err != nil || code != 2 {
		t.Fatal(code, err)
	}
	result, err := db.ExecContext(ctx, `DELETE FROM p`)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatal(affected, err)
	}
	count("c", 0)
}
