package relational

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

func TestNativeSQLUsesEngineWithoutNetworkAndPreservesTransactions(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "native.kitdb"), Options{
		QueryCache: QueryCacheOptions{Select: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	database, err := OpenNativeSQL(engine)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	ctx := context.Background()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT NOT NULL, price BIGINT NOT NULL)`,
		`INSERT INTO products VALUES (1, 'keyboard', 100)`,
	} {
		if _, err := database.ExecContext(ctx, source); err != nil {
			t.Fatal(err)
		}
	}

	statement, err := database.PrepareContext(ctx, `SELECT name, price FROM products WHERE id = $1`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for range 2 {
		var name string
		var price int64
		if err := statement.QueryRowContext(ctx, int64(1)).Scan(&name, &price); err != nil {
			t.Fatal(err)
		}
		if name != "keyboard" || price != 100 {
			t.Fatalf("native row = %q/%d", name, price)
		}
	}
	if stats := engine.QueryCacheStats(); stats.Hits != 1 {
		t.Fatalf("native cache stats = %+v", stats)
	}

	transaction, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE products SET price = 120 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	var price int64
	if err := database.QueryRowContext(ctx, `SELECT price FROM products WHERE id = 1`).Scan(&price); err != nil {
		t.Fatal(err)
	}
	if price != 100 {
		t.Fatalf("price after rollback = %d", price)
	}

	transaction, err = database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE products SET price = 140 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT price FROM products WHERE id = 1`).Scan(&price); err != nil {
		t.Fatal(err)
	}
	if price != 140 {
		t.Fatalf("price after commit = %d", price)
	}
}

func TestNativeNodeSQLUsesLazyLeasesAndEvictsColdDatabase(t *testing.T) {
	root := t.TempDir()
	createPostgresNodeFixture(t, filepath.Join(root, "alpha.kitdb"), "rows", "alpha")
	createPostgresNodeFixture(t, filepath.Join(root, "beta.kitdb"), "rows", "beta")
	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, Password: "unused-native-password", ReadOnly: true,
		ManagerLimits: kitdbnode.Limits{
			MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20,
			DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()

	query := func(name string) {
		t.Helper()
		database, err := node.OpenNativeDatabase(name)
		if err != nil {
			t.Fatal(err)
		}
		database.SetMaxIdleConns(0)
		defer database.Close()
		var value string
		if err := database.QueryRow(`SELECT value FROM rows WHERE id = 1`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != name {
			t.Fatalf("%s value = %q", name, value)
		}
		if stats := node.Stats(); stats.Sessions != 0 || stats.ActiveEngines != 0 {
			t.Fatalf("%s retained an active session: %+v", name, stats)
		}
	}

	query("alpha")
	alpha, err := node.OpenNativeDatabase("alpha")
	if err != nil {
		t.Fatal(err)
	}
	alpha.SetMaxIdleConns(0)
	if _, err := alpha.Exec(`UPDATE rows SET value = 'changed' WHERE id = 1`); err == nil {
		_ = alpha.Close()
		t.Fatal("read-only native node accepted a write")
	}
	if err := alpha.Close(); err != nil {
		t.Fatal(err)
	}
	query("beta")
	node.mu.Lock()
	_, alphaOpen := node.engines["alpha"]
	_, betaOpen := node.engines["beta"]
	node.mu.Unlock()
	if alphaOpen || !betaOpen {
		t.Fatalf("cold LRU state: alpha=%t beta=%t", alphaOpen, betaOpen)
	}
}
