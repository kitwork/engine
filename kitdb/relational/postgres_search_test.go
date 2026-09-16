package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	_ "github.com/lib/pq"
)

func TestStandalonePostgresWireSearchAndCursor(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "search-wire.kitdb"), Options{
		Kernel:               kitdbengine.OpenOptions{RetainHistory: true},
		SearchForegroundWait: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.ServePostgres(ctx, listener, PostgresServerOptions{
			PostgresOptions: PostgresOptions{
				Database: "search", User: "kitdb", Password: "search-secret",
			},
			MaxConnections: 8,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case serveErr := <-done:
			if serveErr != nil {
				t.Errorf("ServePostgres: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServePostgres did not stop")
		}
		if err := engine.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	database, err := sql.Open("postgres", fmt.Sprintf(
		"postgres://kitdb:search-secret@%s/search?sslmode=disable", listener.Addr(),
	))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE products (
		merchant TEXT NOT NULL,
		id INTEGER NOT NULL,
		name TEXT NOT NULL SEARCHABLE WEIGHT 5,
		description TEXT NOT NULL SEARCHABLE,
		PRIMARY KEY (merchant, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO products (merchant, id, name, description) VALUES
		('shopee', 1, 'Bàn phím Logitech MX', 'không dây'),
		('shopee', 2, 'Bàn phím Logitech K380', 'bluetooth'),
		('lazada', 1, 'Bàn phím Logitech G Pro', 'chơi game')`); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query(`SELECT merchant, id, name, _score, _cursor
		FROM products
		WHERE * SEARCH $1
		ORDER BY _score DESC
		LIMIT 2`, "ban phim logitech")
	if err != nil {
		t.Fatal(err)
	}
	var cursor string
	count := 0
	for rows.Next() {
		var merchant, name string
		var id int64
		var score float64
		if err := rows.Scan(&merchant, &id, &name, &score, &cursor); err != nil {
			t.Fatal(err)
		}
		if score <= 0 || cursor == "" {
			t.Fatalf("wire SEARCH row score=%v cursor=%q", score, cursor)
		}
		count++
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("wire SEARCH first page rows = %d", count)
	}
	var merchant string
	var id int64
	if err := database.QueryRow(`SELECT merchant, id
		FROM products
		WHERE * SEARCH $1
		ORDER BY _score DESC
		LIMIT 1 AFTER $2`, "ban phim logitech", cursor).Scan(&merchant, &id); err != nil {
		t.Fatal(err)
	}
	if merchant == "" || id == 0 {
		t.Fatalf("wire SEARCH second page = %q/%d", merchant, id)
	}
	var total int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM products WHERE * SEARCH 'ban phim logitech' LIMIT 1`).Scan(&total); err != nil || total != 3 {
		t.Fatalf("wire SEARCH count = %d, %v", total, err)
	}
	statement, err := database.Prepare(`SELECT COUNT(*) AS total FROM products WHERE * SEARCH $1 AND merchant = $2`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	if err := statement.QueryRow("ban phim logitech", "shopee").Scan(&total); err != nil || total != 2 {
		t.Fatalf("prepared wire SEARCH count = %d, %v", total, err)
	}
	groups, err := database.Query(`SELECT merchant, COUNT(*) AS total, SUM(id) AS id_sum, AVG(id) AS id_average, MIN(id), MAX(id)
		FROM products WHERE * SEARCH 'ban phim logitech' GROUP BY merchant ORDER BY merchant`)
	if err != nil {
		t.Fatal(err)
	}
	groupRows := 0
	for groups.Next() {
		var name, average string
		var count, sum, low, high int64
		if err := groups.Scan(&name, &count, &sum, &average, &low, &high); err != nil {
			groups.Close()
			t.Fatal(err)
		}
		if name == "shopee" && (count != 2 || sum != 3 || average != "1.5" || low != 1 || high != 2) {
			groups.Close()
			t.Fatalf("wire aggregates: %s %d %d %s %d %d", name, count, sum, average, low, high)
		}
		groupRows++
	}
	if err := groups.Err(); err != nil {
		groups.Close()
		t.Fatal(err)
	}
	groups.Close()
	if groupRows != 2 {
		t.Fatalf("wire aggregate groups=%d", groupRows)
	}
	groupStatement, err := database.Prepare(`SELECT merchant, COUNT(*) AS total FROM products WHERE * SEARCH $1 GROUP BY merchant HAVING total >= $2 ORDER BY merchant`)
	if err != nil {
		t.Fatal(err)
	}
	defer groupStatement.Close()
	if err := groupStatement.QueryRow("ban phim logitech", 2).Scan(&merchant, &total); err != nil || merchant != "shopee" || total != 2 {
		t.Fatalf("prepared wire groups: %q %d %v", merchant, total, err)
	}
}
