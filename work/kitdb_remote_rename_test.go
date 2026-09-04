package work

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKitDBRemoteRenameTablePreservesIdentityDependenciesAndRestart(t *testing.T) {
	root := newKitDBRenameTestRoot(t)
	tenant, database := openKitDBDropDatabase(t, root)
	defer func() { tenant.Close() }()

	executeKitDBDropSQL(t, database, `CREATE TABLE accounts (
  id TEXT PRIMARY KEY,
  tenant TEXT NOT NULL,
  code TEXT NOT NULL,
  status TEXT NOT NULL,
  CONSTRAINT accounts_tenant_code_key UNIQUE (tenant, code)
)`)
	executeKitDBDropSQL(t, database, `CREATE INDEX accounts_status_idx ON accounts (status)`)
	executeKitDBDropSQL(t, database, `CREATE TABLE orders (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
  account_tenant TEXT NOT NULL,
  account_code TEXT NOT NULL,
  CONSTRAINT orders_account_key FOREIGN KEY (account_tenant, account_code)
    REFERENCES accounts(tenant, code) ON DELETE RESTRICT
)`)
	executeKitDBDropSQL(t, database, `INSERT INTO accounts (id, tenant, code, status)
VALUES ('account_1', 'tenant_a', 'A', 'active')`)
	executeKitDBDropSQL(t, database, `INSERT INTO orders
  (id, account_id, account_tenant, account_code)
VALUES ('order_1', 'account_1', 'tenant_a', 'A')`)
	waitForKitDBSecondaryIndexIdle(t, database)

	stale, err := kitDBRemoteTable(database, nil, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	_, before, err := kitDBRemoteDefinition(database, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	beforePrefix := kitDBRenameTestIndexPrefix(t, database, before, "accounts_status_idx")

	executeKitDBDropSQL(t, database, `ALTER TABLE "public".accounts RENAME TO customers`)
	if _, err := runKitDBDropSQL(database, `SELECT * FROM accounts`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("old table name remained visible: %v", err)
	}
	if err := stale.ensureKitDBStruct(); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("stale table handle was not rejected: %v", err)
	}
	result := executeKitDBDropSQL(t, database, `SELECT id, status FROM customers WHERE id = 'account_1'`)
	if len(result.rows) != 1 || result.rows[0][0].String() != "account_1" {
		t.Fatalf("renamed table rows = %#v", result.rows)
	}
	_, after, err := kitDBRemoteDefinition(database, "customers")
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != before.ID {
		t.Fatalf("table identity changed across rename: %s -> %s", before.ID, after.ID)
	}
	afterPrefix := kitDBRenameTestIndexPrefix(t, database, after, "accounts_status_idx")
	if !bytes.Equal(beforePrefix, afterPrefix) {
		t.Fatalf("table rename changed physical index prefix: %x -> %x", beforePrefix, afterPrefix)
	}

	executeKitDBDropSQL(t, database, `INSERT INTO orders
  (id, account_id, account_tenant, account_code)
VALUES ('order_2', 'account_1', 'tenant_a', 'A')`)
	if _, err := runKitDBDropSQL(database, `INSERT INTO orders
  (id, account_id, account_tenant, account_code)
VALUES ('order_bad', 'missing', 'tenant_a', 'A')`); err == nil ||
		!strings.Contains(err.Error(), "references missing customers.id") {
		t.Fatalf("renamed inline foreign key error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `INSERT INTO orders
  (id, account_id, account_tenant, account_code)
VALUES ('order_bad_tuple', 'account_1', 'tenant_a', 'missing')`); err == nil ||
		!strings.Contains(err.Error(), "orders_account_key") {
		t.Fatalf("renamed composite foreign key error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `DELETE FROM customers WHERE id = 'account_1'`); err == nil ||
		!strings.Contains(err.Error(), "orders") {
		t.Fatalf("renamed incoming reference did not restrict delete: %v", err)
	}
	if _, err := runKitDBDropSQL(database, `ALTER TABLE customers RENAME TO orders`); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate table rename error = %v", err)
	}

	executeKitDBDropSQL(t, database, `CREATE TABLE accounts (id TEXT PRIMARY KEY, note TEXT)`)
	_, recreated, err := kitDBRemoteDefinition(database, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ID == after.ID {
		t.Fatal("recreated old table name reused the renamed table identity")
	}
	executeKitDBDropSQL(t, database, `INSERT INTO accounts (id, note) VALUES ('account_1', 'new table')`)
	if _, err := runKitDBDropSQL(database, `ALTER TABLE protected_records RENAME TO protected_archive`); err == nil ||
		!strings.Contains(err.Error(), "source-declared") {
		t.Fatalf("source-declared table rename error = %v", err)
	}

	tenant.Close()
	tenant, database = openKitDBDropDatabase(t, root)
	reopened := executeKitDBDropSQL(t, database, `SELECT COUNT(*) FROM customers`)
	if len(reopened.rows) != 1 || reopened.rows[0][0].N != 1 {
		t.Fatalf("renamed table after restart = %#v", reopened.rows)
	}
	oldName := executeKitDBDropSQL(t, database, `SELECT note FROM accounts WHERE id = 'account_1'`)
	if len(oldName.rows) != 1 || oldName.rows[0][0].String() != "new table" {
		t.Fatalf("recreated old name after restart = %#v", oldName.rows)
	}
	if _, err := runKitDBDropSQL(database, `INSERT INTO orders
  (id, account_id, account_tenant, account_code)
VALUES ('order_after_restart', 'missing', 'tenant_a', 'A')`); err == nil ||
		!strings.Contains(err.Error(), "references missing customers.id") {
		t.Fatalf("renamed foreign key after restart error = %v", err)
	}
}

func TestKitDBRemoteRenameIndexPreservesPhysicalIdentityAndRestart(t *testing.T) {
	root := newKitDBRenameTestRoot(t)
	tenant, database := openKitDBDropDatabase(t, root)
	defer func() { tenant.Close() }()

	executeKitDBDropSQL(t, database, `CREATE TABLE inventory (
  id TEXT PRIMARY KEY,
  sku TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  price INTEGER NOT NULL
)`)
	executeKitDBDropSQL(t, database, `INSERT INTO inventory (id, sku, status, price) VALUES
  ('item_1', 'SKU-1', 'active', 10),
  ('item_2', 'SKU-2', 'disabled', 20)`)
	executeKitDBDropSQL(t, database, `CREATE INDEX inventory_status_price_idx ON inventory (status, price)`)
	waitForKitDBSecondaryIndexIdle(t, database)
	_, before, err := kitDBRemoteDefinition(database, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	beforePrefix := kitDBRenameTestIndexPrefix(t, database, before, "inventory_status_price_idx")

	executeKitDBDropSQL(t, database, `ALTER INDEX "public".inventory_status_price_idx RENAME TO inventory_state_price_idx`)
	_, after, err := kitDBRemoteDefinition(database, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	afterPrefix := kitDBRenameTestIndexPrefix(t, database, after, "inventory_state_price_idx")
	if !bytes.Equal(beforePrefix, afterPrefix) {
		t.Fatalf("index rename changed physical prefix: %x -> %x", beforePrefix, afterPrefix)
	}
	indexes := executeKitDBDropSQL(t, database, `PRAGMA index_list(inventory)`)
	if kitDBRenameResultHasString(indexes, "inventory_status_price_idx") ||
		!kitDBRenameResultHasString(indexes, "inventory_state_price_idx") {
		t.Fatalf("renamed index catalog = %#v", indexes.rows)
	}

	// Reusing the old logical name on the same columns must allocate a new
	// immutable identity rather than aliasing the renamed index's keyspace.
	executeKitDBDropSQL(t, database, `CREATE INDEX inventory_status_price_idx ON inventory (status, price)`)
	waitForKitDBSecondaryIndexIdle(t, database)
	_, withRecreated, err := kitDBRemoteDefinition(database, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	recreatedPrefix := kitDBRenameTestIndexPrefix(t, database, withRecreated, "inventory_status_price_idx")
	if bytes.Equal(afterPrefix, recreatedPrefix) {
		t.Fatal("recreated old index name reused the renamed index physical identity")
	}
	if _, err := runKitDBDropSQL(database, `ALTER INDEX inventory_state_price_idx RENAME TO inventory_status_price_idx`); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate index rename error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `ALTER INDEX inventory_pkey RENAME TO inventory_primary_idx`); err == nil ||
		!strings.Contains(err.Error(), "constraint") {
		t.Fatalf("constraint-owned index rename error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `ALTER INDEX idx_protected_records_title RENAME TO protected_title_idx`); err == nil ||
		!strings.Contains(err.Error(), "source-declared") {
		t.Fatalf("source-declared index rename error = %v", err)
	}
	executeKitDBDropSQL(t, database, `ALTER INDEX IF EXISTS missing_inventory_idx RENAME TO ignored_idx`)

	tenant.Close()
	tenant, database = openKitDBDropDatabase(t, root)
	reopened := executeKitDBDropSQL(t, database, `PRAGMA index_list(inventory)`)
	if !kitDBRenameResultHasString(reopened, "inventory_state_price_idx") ||
		!kitDBRenameResultHasString(reopened, "inventory_status_price_idx") {
		t.Fatalf("renamed/recreated indexes after restart = %#v", reopened.rows)
	}
	rows := executeKitDBDropSQL(t, database, `SELECT sku FROM inventory
WHERE status = 'active' ORDER BY price`)
	if len(rows.rows) != 1 || rows.rows[0][0].String() != "SKU-1" {
		t.Fatalf("renamed index query after restart = %#v", rows.rows)
	}
}

func newKitDBRenameTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const protected_records = struct({ id: text().key(), title: text().index() });
const db = kitdb("drop.kitdb", { protected_records }, { token: "drop-secret", access: "readwrite" });
router.get(() => db.protected_records.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func kitDBRenameTestIndexPrefix(
	t *testing.T,
	database *dbProxy,
	definition *StructDef,
	name string,
) []byte {
	t.Helper()
	index, found := ddlDefinitionSecondaryIndex(definition, name)
	if !found {
		t.Fatalf("index %q is unavailable on %q", name, definition.Name)
	}
	managed, err := kitDBForRequest(database.tenant, database.dbName, nil).database()
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	metadata, _, err := loadKitDBIndexGenerationMetadata(managed.database, definition)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := kitDBIndexBasePrefixForGeneration(
		definition, index, kitDBPhysicalIndexGeneration(metadata, index),
	)
	if err != nil {
		t.Fatal(err)
	}
	return prefix
}

func kitDBRenameResultHasString(result kitDBRemoteResult, expected string) bool {
	for _, row := range result.rows {
		for _, item := range row {
			if item.String() == expected {
				return true
			}
		}
	}
	return false
}
