package work

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/value"
)

// A proxy snapshot taken before the write lock is the shape every remote SQL
// statement starts from: kitDBRemoteTable copies the schema, then
// ensureKitDBStruct reconciles the durable catalog under managed.writeMu. When
// another statement commits DDL in between, that copy is stale — and a
// catalog-owned struct must follow the catalog, never pull it back.

func startKitDBCatalogOwnedSnapshotTenant(t *testing.T, router string) *dbProxy {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tenant.Close)
	_, config, found := resolveServe(tenant, "owned.kitdb")
	if !found || config.database == nil {
		t.Fatal("owned.kitdb serve registration is unavailable")
	}
	return config.database
}

func runKitDBCatalogOwnedSQL(t *testing.T, database *dbProxy, source string) kitDBRemoteResult {
	t.Helper()
	result, err := executeKitDBRemoteSQL(
		context.Background(), nil, database, source,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil {
		t.Fatalf("%s: %v", source, err)
	}
	return result
}

func kitDBCatalogOwnedFields(t *testing.T, database *dbProxy, table string) map[string]bool {
	t.Helper()
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}
	info := runKitDBCatalogOwnedSQL(t, database, `PRAGMA table_info(`+table+`)`)
	fields := map[string]bool{}
	for _, row := range info.rows {
		if len(row) > 1 {
			fields[row[1].String()] = true
		}
	}
	return fields
}

func TestKitDBStaleSnapshotKeepsConcurrentlyAddedColumn(t *testing.T) {
	database := startKitDBCatalogOwnedSnapshotTenant(t, `import { router, database } from "kitwork";
const db = database.kitdb("owned.kitdb", {}, { token: "owned-secret", access: "readwrite" });
router.get(() => db.items.count());`)
	runKitDBCatalogOwnedSQL(t, database, `CREATE TABLE IF NOT EXISTS items (id KITID PRIMARY KEY, name TEXT NOT NULL)`)

	// Statement B copies the schema first, exactly as executeKitDBRemoteAlterAddColumn does
	// before it takes the write lock.
	stale, err := kitDBRemoteTable(database, nil, "items")
	if err != nil {
		t.Fatal(err)
	}
	// Statement A commits in between.
	runKitDBCatalogOwnedSQL(t, database, `ALTER TABLE items ADD COLUMN IF NOT EXISTS note_a TEXT DEFAULT ''`)
	if fields := kitDBCatalogOwnedFields(t, database, "items"); !fields["note_a"] {
		t.Fatalf("note_a was not added: %v", fields)
	}
	// Statement B resumes with its copy. The catalog is empty of rows, so a plan built from the
	// copy would apply without a migrate flag — and take note_a with it.
	if err := stale.ensureKitDBStruct(); err != nil {
		t.Fatal(err)
	}
	if fields := kitDBCatalogOwnedFields(t, database, "items"); !fields["note_a"] {
		t.Fatalf("a stale pre-lock snapshot pulled the catalog back and dropped note_a: %v", fields)
	}
	// And B's own change still lands on top of A's.
	runKitDBCatalogOwnedSQL(t, database, `ALTER TABLE items ADD COLUMN IF NOT EXISTS note_b TEXT DEFAULT ''`)
	if fields := kitDBCatalogOwnedFields(t, database, "items"); !fields["note_a"] || !fields["note_b"] {
		t.Fatalf("columns after both statements = %v", fields)
	}
}

func TestKitDBStaleSnapshotDoesNotResurrectDroppedSibling(t *testing.T) {
	database := startKitDBCatalogOwnedSnapshotTenant(t, `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const products = struct({ id: text().key(), title: text() });
const db = kitdb("owned.kitdb", { products }, { token: "owned-secret", access: "readwrite" });
router.get(() => db.products.count());`)
	runKitDBCatalogOwnedSQL(t, database, `CREATE TABLE IF NOT EXISTS items (id KITID PRIMARY KEY, name TEXT NOT NULL)`)

	// A declared table's handle carries every sibling in its snapshot, the catalog-owned
	// `items` included.
	declared, err := kitDBRemoteTable(database, nil, "products")
	if err != nil {
		t.Fatal(err)
	}
	if declared.definitions["items"] == nil {
		t.Fatal("snapshot did not carry the catalog-owned sibling")
	}
	runKitDBCatalogOwnedSQL(t, database, `DROP TABLE items`)

	// Reconciling the declared table must not bring the dropped sibling back.
	if err := declared.ensureKitDBStruct(); err != nil {
		t.Fatal(err)
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		t.Fatal(err)
	}
	if _, definitions := database.schemaSnapshot(); definitions["items"] != nil {
		t.Fatal("a stale snapshot recreated a dropped catalog-owned table")
	}
	// The declared table itself still reconciles as before.
	if fields := kitDBCatalogOwnedFields(t, database, "products"); !fields["id"] || !fields["title"] {
		t.Fatalf("declared table fields = %v", fields)
	}
}
