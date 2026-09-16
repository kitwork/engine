package relational

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/managed"
)

func TestPostgresManagedRootRoutesAndRestarts(t *testing.T) {
	root := createManagedNodeRoot(t, "shop", "clicks")
	createPostgresNodeFixture(t, filepath.Join(root, "rogue.kitdb"), "rows", "rogue")
	createPostgresNodeFixture(t, filepath.Join(root, "unregistered", "data.kitdb"), "rows", "unregistered")
	for attempt := 0; attempt < 2; attempt++ {
		node, err := OpenPostgresNode(PostgresNodeOptions{Root: root, Password: "node-secret"})
		if err != nil {
			t.Fatal(err)
		}
		if node.catalog == nil || node.Stats().Manager.OpenDatabases != 0 {
			t.Fatal("managed node must open .catalog but no user database")
		}
		if second, err := managed.Open(root); err == nil {
			_ = second.Close()
			t.Fatal("offline catalog mutation possible while server owns root")
		}
		address, stop := startPostgresNodeTestServer(t, node)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		maintenance := openPostgresNodeTestClient(t, address, "kitdb", "node-secret")
		if names := queryPostgresNodeStrings(t, ctx, maintenance, `SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); !slices.Equal(names, []string{"clicks", "kitdb", "shop"}) {
			t.Fatalf("registered databases = %v", names)
		}
		_ = maintenance.Close()
		for _, name := range []string{"shop", "clicks"} {
			client := openPostgresNodeTestClient(t, address, name+".kitdb", "node-secret")
			if got := queryPostgresNodeValue(t, ctx, client, `SELECT value FROM rows WHERE id = 1`); got != name {
				t.Fatalf("%s leaked another database: %s", name, got)
			}
			if names := queryPostgresNodeStrings(t, ctx, client, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name`); !slices.Contains(names, "rows") {
				t.Fatalf("missing table catalog: %v", names)
			}
			// TablePlus loads functions and schemas alongside the table tree.
			// An unrelated namespace JOIN must never invent unnamed functions.
			if functions := queryPostgresNodeStrings(t, ctx, client, `SELECT p.proname AS function_name FROM pg_catalog.pg_proc p
				LEFT JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
				WHERE n.nspname<>'pg_catalog' AND n.nspname<>'information_schema'`); len(functions) != 0 {
				t.Fatalf("invented function metadata: %v", functions)
			}
			if schemas := queryPostgresNodeStrings(t, ctx, client, `SELECT nspname FROM pg_namespace WHERE nspname != 'pg_catalog' AND nspname != 'information_schema'`); !slices.Equal(schemas, []string{"public"}) {
				t.Fatalf("manager schema filter = %v", schemas)
			}
			tx, err := client.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO rows (id, value) VALUES (2, 'rollback')`); err != nil {
				t.Fatal(err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := client.QueryRowContext(ctx, `SELECT COUNT(*) FROM rows`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("rollback count = %d, %v", count, err)
			}
			_ = client.Close()
		}
		for _, name := range []string{managed.CatalogDirectory, managed.LegacySystemDirectory, "rogue", "unregistered"} {
			client := openPostgresNodeRawClient(address, name, "node-secret")
			if err := client.PingContext(ctx); postgresNodeSQLState(err) != "3D000" {
				t.Fatalf("unexpected exposure %s: %v", name, err)
			}
			_ = client.Close()
		}
		cancel()
		stop()
		if err := node.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// Direct-file access remains independent after releasing the node owner.
	engine, err := Open(filepath.Join(root, "shop", "data.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	_ = engine.Close()
}

func TestPostgresManagedRootRejectsMissingOrReplacedDatabase(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		root := createManagedNodeRoot(t, "shop")
		if err := os.Rename(filepath.Join(root, "shop"), filepath.Join(root, "saved")); err != nil {
			t.Fatal(err)
		}
		if replaced {
			createPostgresNodeFixture(t, filepath.Join(root, "shop", "data.kitdb"), "rows", "impostor")
		}
		node, err := OpenPostgresNode(PostgresNodeOptions{Root: root, Password: "node-secret"})
		if err != nil {
			t.Fatal(err)
		}
		address, stop := startPostgresNodeTestServer(t, node)
		client := openPostgresNodeRawClient(address, "shop", "node-secret")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = client.PingContext(ctx)
		if postgresNodeSQLState(err) != "55000" || (replaced && !strings.Contains(err.Error(), "identity")) {
			t.Fatalf("replacement=%t, error=%v", replaced, err)
		}
		cancel()
		_ = client.Close()
		stop()
		_ = node.Close()
		if !replaced {
			if _, err := os.Stat(filepath.Join(root, "shop")); !os.IsNotExist(err) {
				t.Fatalf("silently recreated missing database: %v", err)
			}
		}
	}
}

func TestPostgresManagedRootNeverFallsBackFromBrokenCatalog(t *testing.T) {
	root := t.TempDir()
	createPostgresNodeFixture(t, filepath.Join(root, "rogue.kitdb"), "rows", "rogue")
	if err := os.Mkdir(filepath.Join(root, managed.CatalogDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	if node, err := OpenPostgresNode(PostgresNodeOptions{Root: root, Password: "node-secret"}); err == nil {
		_ = node.Close()
		t.Fatal("broken .catalog fell back to flat auto-discovery")
	}
}

func createManagedNodeRoot(t *testing.T, names ...string) string {
	t.Helper()
	path := t.TempDir()
	if _, err := managed.Init(path); err != nil {
		t.Fatal(err)
	}
	root, err := managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range names {
		createPostgresNodeFixture(t, filepath.Join(path, name, "data.kitdb"), "rows", name)
		if _, err := root.Register(name, name); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
