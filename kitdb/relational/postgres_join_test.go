package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestStandalonePostgresWireCompositeJoinAggregate(t *testing.T) {
	engine := openJoinAggregateEngine(t, "primary")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.ServePostgres(ctx, listener, PostgresServerOptions{
			PostgresOptions: PostgresOptions{Database: "joins", User: "kitdb", Password: "join-test-only"},
			MaxConnections:  4,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("JOIN test server did not stop")
		}
	})
	client, err := sql.Open("postgres", fmt.Sprintf("postgres://kitdb:join-test-only@%s/joins?sslmode=disable", listener.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetMaxOpenConns(1)
	statement, err := client.PrepareContext(ctx, `SELECT p.merchant AS merchant, COUNT(*) AS total, SUM(p.price) AS amount`+
		joinAggregateFrom+`WHERE e.id >= $1 AND p.merchant IS NOT NULL GROUP BY p.merchant HAVING total >= $2 ORDER BY total DESC LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for _, test := range []struct {
		start, count int64
		amount       string
	}{{1, 4, "24"}, {2, 3, "13.75"}} {
		var merchant, amount string
		var count int64
		if err := statement.QueryRowContext(ctx, test.start, 1).Scan(&merchant, &count, &amount); err != nil {
			t.Fatal(err)
		}
		if merchant != "shopee" || count != test.count || amount != test.amount {
			t.Fatalf("prepared aggregate = %q %d %s", merchant, count, amount)
		}
	}
	if _, err := client.ExecContext(ctx, `SELECT SUM(p.name)`+joinAggregateFrom); err == nil {
		t.Fatal("SUM(text) accepted")
	}
	var count int64
	if err := client.QueryRowContext(ctx, `SELECT COUNT(*)`+joinAggregateFrom).Scan(&count); err != nil || count != 8 {
		t.Fatalf("connection after SQL error: %d, %v", count, err)
	}
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+joinAggregateFrom).Scan(&count); err != nil || count != 7 {
		t.Fatalf("transaction JOIN: %d, %v", count, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := client.QueryRowContext(ctx, `SELECT COUNT(*)`+joinAggregateFrom).Scan(&count); err != nil || count != 8 {
		t.Fatalf("rollback JOIN: %d, %v", count, err)
	}
}
