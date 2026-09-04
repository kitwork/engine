package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/value"
)

func TestKitDBRemoteDropTableIsAtomicAndSurvivesRestart(t *testing.T) {
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

	tenant, database := openKitDBDropDatabase(t, root)
	defer func() { tenant.Close() }()
	executeKitDBDropSQL(t, database, `CREATE TABLE "NhanVien" (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  department TEXT
)`)
	executeKitDBDropSQL(t, database, `CREATE INDEX nhanvien_department_idx ON "NhanVien" (department)`)
	executeKitDBDropSQL(t, database, `INSERT INTO "NhanVien" (id, name, department) VALUES
  ('employee_1', 'Kitwork One', 'engineering'),
  ('employee_2', 'Kitwork Two', 'product')`)
	executeKitDBDropSQL(t, database, `ANALYZE "NhanVien"`)
	executeKitDBDropSQL(t, database, `INSERT INTO "NhanVien" (id, name, department) VALUES
  ('employee_3', 'Kitwork Three', 'engineering')`)
	executeKitDBDropSQL(t, database, `CREATE TABLE salaries (
  id TEXT PRIMARY KEY,
  employee_id TEXT NOT NULL REFERENCES "NhanVien"(id) ON DELETE RESTRICT,
  amount INTEGER NOT NULL
)`)

	if _, err := runKitDBDropSQL(database, `DROP TABLE "public"."NhanVien"`); err == nil ||
		!strings.Contains(err.Error(), `table "salaries" depends on it`) {
		t.Fatalf("dependent DROP error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `DROP TABLE "public"."NhanVien" CASCADE`); err == nil ||
		!strings.Contains(err.Error(), "cascading dependent constraints is not supported") {
		t.Fatalf("dependent CASCADE DROP error = %v", err)
	}
	stale, err := kitDBRemoteTable(database, nil, "NhanVien")
	if err != nil {
		t.Fatal(err)
	}
	_, oldDefinition, err := kitDBRemoteDefinition(database, "NhanVien")
	if err != nil {
		t.Fatal(err)
	}

	executeKitDBDropSQL(t, database, `DROP TABLE salaries RESTRICT`)
	executeKitDBDropSQL(t, database, `DROP TABLE "public"."NhanVien" CASCADE`)
	executeKitDBDropSQL(t, database, `DROP TABLE IF EXISTS "public"."NhanVien"`)
	if _, err := runKitDBDropSQL(database, `SELECT * FROM "NhanVien"`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("dropped table SELECT error = %v", err)
	}
	if err := stale.ensureKitDBStruct(); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("stale table handle was not rejected: %v", err)
	}
	if _, err := runKitDBDropSQL(database, `DROP TABLE protected_records`); err == nil ||
		!strings.Contains(err.Error(), "source-declared") {
		t.Fatalf("source-declared DROP error = %v", err)
	}
	if count := countKitDBDroppedStructKeys(t, database, oldDefinition); count != 0 {
		t.Fatalf("dropped table retained %d row/index/metadata keys", count)
	}

	tenant.Close()
	tenant, database = openKitDBDropDatabase(t, root)
	if _, err := runKitDBDropSQL(database, `SELECT * FROM "NhanVien"`); err == nil ||
		!strings.Contains(err.Error(), "no such table") {
		t.Fatalf("reopened dropped table SELECT error = %v", err)
	}
	executeKitDBDropSQL(t, database, `CREATE TABLE "NhanVien" (id TEXT PRIMARY KEY, name TEXT NOT NULL)`)
	result := executeKitDBDropSQL(t, database, `SELECT COUNT(*) AS total FROM "NhanVien"`)
	if len(result.rows) != 1 || len(result.rows[0]) != 1 || result.rows[0][0].N != 0 {
		t.Fatalf("recreated table exposed old rows: %#v", result.rows)
	}
}

func TestKitDBRemoteDropIndexUsesResumableRetirementAndSurvivesRestart(t *testing.T) {
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

	tenant, database := openKitDBDropDatabase(t, root)
	executeKitDBDropSQL(t, database, `CREATE TABLE inventory (
  id TEXT PRIMARY KEY,
  sku TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  price INTEGER NOT NULL
)`)
	executeKitDBDropSQL(t, database, `INSERT INTO inventory (id, sku, status, price) VALUES
  ('item_1', 'KIT-1', 'active', 100),
  ('item_2', 'KIT-2', 'disabled', 200),
  ('item_3', 'KIT-3', 'active', 300)`)
	executeKitDBDropSQL(t, database, `CREATE INDEX inventory_status_price_idx ON inventory (status, price)`)
	waitForKitDBSecondaryIndexIdle(t, database)

	if _, err := runKitDBDropSQL(database, `DROP INDEX inventory_pkey`); err == nil ||
		!strings.Contains(err.Error(), `constraint "inventory_pkey"`) {
		t.Fatalf("primary index DROP error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `DROP INDEX unique_inventory_sku`); err == nil ||
		!strings.Contains(err.Error(), `constraint "unique_inventory_sku"`) {
		t.Fatalf("unique index DROP error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `DROP INDEX missing_inventory_index`); err == nil ||
		!strings.Contains(err.Error(), "no such index") {
		t.Fatalf("missing index DROP error = %v", err)
	}
	executeKitDBDropSQL(t, database, `DROP INDEX IF EXISTS missing_inventory_index`)
	if _, err := runKitDBDropSQL(database, `DROP INDEX idx_protected_records_title`); err == nil ||
		!strings.Contains(err.Error(), "source-declared") {
		t.Fatalf("source-declared index DROP error = %v", err)
	}

	executeKitDBDropSQL(t, database, `DROP INDEX "public"."inventory_status_price_idx" CASCADE`)
	indexes := executeKitDBDropSQL(t, database, `PRAGMA index_list(inventory)`)
	for _, row := range indexes.rows {
		if len(row) > 1 && row[1].Text() == "inventory_status_price_idx" {
			t.Fatalf("dropped index remained visible before cleanup: %#v", indexes.rows)
		}
	}
	waitForKitDBSecondaryIndexIdle(t, database)
	_, definition, err := kitDBRemoteDefinition(database, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := ddlDefinitionSecondaryIndex(definition, "inventory_status_price_idx"); found {
		t.Fatal("dropped index remained in the published schema")
	}
	if count := countKitDBStructNamespaceKeys(t, database, definition, kitDBIndexNamespace); count != 3 {
		t.Fatalf("index namespace retained %d keys; want only 3 hidden primary-order entries", count)
	}

	tenant.Close()
	tenant, database = openKitDBDropDatabase(t, root)
	defer tenant.Close()
	reopened := executeKitDBDropSQL(t, database, `PRAGMA index_list(inventory)`)
	for _, row := range reopened.rows {
		if len(row) > 1 && row[1].Text() == "inventory_status_price_idx" {
			t.Fatalf("dropped index reappeared after restart: %#v", reopened.rows)
		}
	}
	result := executeKitDBDropSQL(t, database, `SELECT sku FROM inventory WHERE status = 'active' ORDER BY price`)
	if len(result.rows) != 2 || result.rows[0][0].Text() != "KIT-1" || result.rows[1][0].Text() != "KIT-3" {
		t.Fatalf("rows after index retirement = %#v", result.rows)
	}
}

func TestKitDBRemoteDropConstraintIsAtomicAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const protected_records = struct({ id: text().key(), code: text().unique() });
const db = kitdb("drop.kitdb", { protected_records }, { token: "drop-secret", access: "readwrite" });
router.get(() => db.protected_records.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant, database := openKitDBDropDatabase(t, root)
	defer func() { tenant.Close() }()
	executeKitDBDropSQL(t, database, `CREATE TABLE accounts (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  tenant TEXT NOT NULL,
  code TEXT NOT NULL,
  CONSTRAINT accounts_tenant_code_key UNIQUE (tenant, code)
)`)
	executeKitDBDropSQL(t, database, `CREATE TABLE orders (
  id TEXT PRIMARY KEY,
  account_email TEXT NOT NULL REFERENCES accounts(email) ON DELETE RESTRICT,
  account_tenant TEXT NOT NULL,
  account_code TEXT NOT NULL,
  amount INTEGER NOT NULL,
  CONSTRAINT orders_account_fkey FOREIGN KEY (account_tenant, account_code)
    REFERENCES accounts(tenant, code) ON DELETE RESTRICT,
  CONSTRAINT orders_amount_nonnegative CHECK (amount >= 0)
)`)
	executeKitDBDropSQL(t, database, `INSERT INTO accounts (id, email, tenant, code)
VALUES ('account_1', 'owner@example.com', 'tenant_a', 'A')`)
	executeKitDBDropSQL(t, database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_1', 'owner@example.com', 'tenant_a', 'A', 100)`)

	if _, err := runKitDBDropSQL(database,
		`ALTER TABLE accounts DROP CONSTRAINT unique_accounts_email`); err == nil ||
		!strings.Contains(err.Error(), `foreign key constraint "orders_account_email_fkey"`) {
		t.Fatalf("single unique dependency error = %v", err)
	}
	if _, err := runKitDBDropSQL(database,
		`ALTER TABLE accounts DROP CONSTRAINT accounts_tenant_code_key CASCADE`); err == nil ||
		!strings.Contains(err.Error(), "cascading dependent constraints is not supported") {
		t.Fatalf("composite unique CASCADE dependency error = %v", err)
	}
	if _, err := runKitDBDropSQL(database,
		`ALTER TABLE accounts DROP CONSTRAINT accounts_pkey`); err == nil ||
		!strings.Contains(err.Error(), "ALTER PRIMARY KEY") {
		t.Fatalf("primary constraint DROP error = %v", err)
	}
	if _, err := runKitDBDropSQL(database,
		`ALTER TABLE protected_records DROP CONSTRAINT unique_protected_records_code`); err == nil ||
		!strings.Contains(err.Error(), "source-declared") {
		t.Fatalf("source-declared constraint DROP error = %v", err)
	}
	if _, err := runKitDBDropSQL(database,
		`ALTER TABLE orders DROP CONSTRAINT missing_constraint`); err == nil ||
		!strings.Contains(err.Error(), "no such constraint") {
		t.Fatalf("missing constraint DROP error = %v", err)
	}
	executeKitDBDropSQL(t, database,
		`ALTER TABLE orders DROP CONSTRAINT IF EXISTS missing_constraint RESTRICT`)

	if _, err := runKitDBDropSQL(database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_bad_check', 'owner@example.com', 'tenant_a', 'A', -1)`); err == nil ||
		!strings.Contains(err.Error(), "check constraint") {
		t.Fatalf("CHECK before DROP error = %v", err)
	}
	executeKitDBDropSQL(t, database,
		`ALTER TABLE "public"."orders" DROP CONSTRAINT orders_amount_nonnegative`)
	executeKitDBDropSQL(t, database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_negative', 'owner@example.com', 'tenant_a', 'A', -1)`)

	executeKitDBDropSQL(t, database,
		`ALTER TABLE orders DROP CONSTRAINT orders_account_fkey RESTRICT`)
	executeKitDBDropSQL(t, database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_missing_tuple', 'owner@example.com', 'missing', 'missing', 10)`)
	executeKitDBDropSQL(t, database,
		`ALTER TABLE orders DROP CONSTRAINT orders_account_email_fkey`)
	executeKitDBDropSQL(t, database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_missing_email', 'missing@example.com', 'missing', 'missing', 20)`)

	executeKitDBDropSQL(t, database,
		`ALTER TABLE accounts DROP CONSTRAINT unique_accounts_email`)
	executeKitDBDropSQL(t, database,
		`ALTER TABLE accounts DROP CONSTRAINT accounts_tenant_code_key`)
	executeKitDBDropSQL(t, database, `INSERT INTO accounts (id, email, tenant, code)
VALUES ('account_2', 'owner@example.com', 'tenant_a', 'A')`)

	_, accounts, err := kitDBRemoteDefinition(database, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unique_accounts_email", "accounts_tenant_code_key"} {
		if _, found, findErr := ddlDefinitionConstraint(accounts, name); findErr != nil || found {
			t.Fatalf("dropped account constraint %q = found %t, %v", name, found, findErr)
		}
	}
	if count := countKitDBStructNamespaceKeys(t, database, accounts, kitDBUniqueNamespace); count != 0 {
		t.Fatalf("dropped unique constraints retained %d physical keys", count)
	}
	_, orders, err := kitDBRemoteDefinition(database, "orders")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"orders_account_email_fkey", "orders_account_fkey", "orders_amount_nonnegative",
	} {
		if _, found, findErr := ddlDefinitionConstraint(orders, name); findErr != nil || found {
			t.Fatalf("dropped order constraint %q = found %t, %v", name, found, findErr)
		}
	}

	tenant.Close()
	tenant, database = openKitDBDropDatabase(t, root)
	executeKitDBDropSQL(t, database, `INSERT INTO accounts (id, email, tenant, code)
VALUES ('account_3', 'owner@example.com', 'tenant_a', 'A')`)
	accountCount := executeKitDBDropSQL(t, database, `SELECT COUNT(*) FROM accounts`)
	if len(accountCount.rows) != 1 || accountCount.rows[0][0].N != 3 {
		t.Fatalf("accounts after constraint DROP restart = %#v", accountCount.rows)
	}
	orderCount := executeKitDBDropSQL(t, database, `SELECT COUNT(*) FROM orders`)
	if len(orderCount.rows) != 1 || orderCount.rows[0][0].N != 4 {
		t.Fatalf("orders after constraint DROP restart = %#v", orderCount.rows)
	}
}

func TestKitDBRemoteAddConstraintIsAtomicAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const protected_records = struct({ id: text().key(), code: text() });
const db = kitdb("drop.kitdb", { protected_records }, { token: "drop-secret", access: "readwrite" });
router.get(() => db.protected_records.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant, database := openKitDBDropDatabase(t, root)
	defer func() { tenant.Close() }()
	executeKitDBDropSQL(t, database, `CREATE TABLE accounts (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  tenant TEXT NOT NULL,
  code TEXT NOT NULL
)`)
	executeKitDBDropSQL(t, database, `CREATE TABLE orders (
  id TEXT PRIMARY KEY,
  account_email TEXT NOT NULL,
  account_tenant TEXT NOT NULL,
  account_code TEXT NOT NULL,
  amount INTEGER NOT NULL
)`)
	executeKitDBDropSQL(t, database, `INSERT INTO accounts (id, email, tenant, code)
VALUES ('account_1', 'owner@example.com', 'tenant_a', 'A')`)
	executeKitDBDropSQL(t, database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_1', 'owner@example.com', 'tenant_a', 'A', 100)`)

	executeKitDBDropSQL(t, database,
		`ALTER TABLE accounts ADD CONSTRAINT accounts_email_key UNIQUE (email)`)
	executeKitDBDropSQL(t, database,
		`ALTER TABLE accounts ADD CONSTRAINT accounts_tenant_code_key UNIQUE (tenant, code)`)
	executeKitDBDropSQL(t, database,
		`ALTER TABLE orders ADD CONSTRAINT orders_email_fkey `+
			`FOREIGN KEY (account_email) REFERENCES accounts(email) ON DELETE RESTRICT`)
	executeKitDBDropSQL(t, database,
		`ALTER TABLE orders ADD CONSTRAINT orders_account_fkey `+
			`FOREIGN KEY (account_tenant, account_code) REFERENCES accounts(tenant, code) ON DELETE RESTRICT`)
	executeKitDBDropSQL(t, database,
		`ALTER TABLE orders ADD CONSTRAINT orders_amount_nonnegative CHECK (amount >= 0)`)

	if _, err := runKitDBDropSQL(database,
		`ALTER TABLE accounts ADD CONSTRAINT accounts_email_key UNIQUE (email)`); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate ADD CONSTRAINT error = %v", err)
	}
	if _, err := runKitDBDropSQL(database,
		`ALTER TABLE accounts ADD CONSTRAINT accounts_email_alias UNIQUE (email)`); err == nil ||
		!strings.Contains(err.Error(), "cover the same fields") {
		t.Fatalf("duplicate unique fields error = %v", err)
	}
	if _, err := runKitDBDropSQL(database,
		`ALTER TABLE protected_records ADD CONSTRAINT protected_code_key UNIQUE (code)`); err == nil ||
		!strings.Contains(err.Error(), "source-declared") {
		t.Fatalf("source-declared ADD CONSTRAINT error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `INSERT INTO accounts (id, email, tenant, code)
VALUES ('account_duplicate', 'owner@example.com', 'tenant_b', 'B')`); err == nil ||
		!strings.Contains(err.Error(), "accounts_email_key") {
		t.Fatalf("named single UNIQUE enforcement error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_missing_email', 'missing@example.com', 'tenant_a', 'A', 10)`); err == nil ||
		!strings.Contains(err.Error(), "orders_email_fkey") {
		t.Fatalf("named single FOREIGN KEY enforcement error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_missing_tuple', 'owner@example.com', 'missing', 'missing', 10)`); err == nil ||
		!strings.Contains(err.Error(), "orders_account_fkey") {
		t.Fatalf("composite FOREIGN KEY enforcement error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_negative', 'owner@example.com', 'tenant_a', 'A', -1)`); err == nil ||
		!strings.Contains(err.Error(), "orders_amount_nonnegative") {
		t.Fatalf("added CHECK enforcement error = %v", err)
	}
	if _, err := runKitDBDropSQL(database,
		`DELETE FROM accounts WHERE id = 'account_1'`); err == nil ||
		!strings.Contains(err.Error(), "still references it") {
		t.Fatalf("added foreign-key RESTRICT error = %v", err)
	}

	executeKitDBDropSQL(t, database, `CREATE TABLE dirty_constraints (
  id TEXT PRIMARY KEY,
  code TEXT NOT NULL,
  parent_email TEXT NOT NULL,
  amount INTEGER NOT NULL
)`)
	executeKitDBDropSQL(t, database, `INSERT INTO dirty_constraints (id, code, parent_email, amount) VALUES
  ('dirty_1', 'same', 'owner@example.com', -1),
  ('dirty_2', 'same', 'missing@example.com', 1)`)
	for name, sql := range map[string]string{
		"dirty_code_key": `ALTER TABLE dirty_constraints ADD CONSTRAINT dirty_code_key UNIQUE (code)`,
		"dirty_parent_fkey": `ALTER TABLE dirty_constraints ADD CONSTRAINT dirty_parent_fkey ` +
			`FOREIGN KEY (parent_email) REFERENCES accounts(email)`,
		"dirty_amount_check": `ALTER TABLE dirty_constraints ADD CONSTRAINT dirty_amount_check CHECK (amount >= 0)`,
	} {
		if _, err := runKitDBDropSQL(database, sql); err == nil {
			t.Fatalf("dirty ADD CONSTRAINT %q unexpectedly succeeded", name)
		}
		_, definition, err := kitDBRemoteDefinition(database, "dirty_constraints")
		if err != nil {
			t.Fatal(err)
		}
		if _, found, findErr := ddlDefinitionConstraint(definition, name); findErr != nil || found {
			t.Fatalf("failed constraint %q published: found %t, %v", name, found, findErr)
		}
	}

	_, accounts, err := kitDBRemoteDefinition(database, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"accounts_email_key", "accounts_tenant_code_key"} {
		if _, found, findErr := ddlDefinitionConstraint(accounts, name); findErr != nil || !found {
			t.Fatalf("added account constraint %q = found %t, %v", name, found, findErr)
		}
	}
	if count := countKitDBStructNamespaceKeys(t, database, accounts, kitDBUniqueNamespace); count < 2 {
		t.Fatalf("added unique constraints have only %d physical keys", count)
	}
	_, orders, err := kitDBRemoteDefinition(database, "orders")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"orders_email_fkey", "orders_account_fkey", "orders_amount_nonnegative"} {
		if _, found, findErr := ddlDefinitionConstraint(orders, name); findErr != nil || !found {
			t.Fatalf("added order constraint %q = found %t, %v", name, found, findErr)
		}
	}

	tenant.Close()
	tenant, database = openKitDBDropDatabase(t, root)
	if _, err := runKitDBDropSQL(database, `INSERT INTO accounts (id, email, tenant, code)
VALUES ('account_after_restart', 'owner@example.com', 'tenant_b', 'B')`); err == nil ||
		!strings.Contains(err.Error(), "accounts_email_key") {
		t.Fatalf("reopened named UNIQUE enforcement error = %v", err)
	}
	if _, err := runKitDBDropSQL(database, `INSERT INTO orders
  (id, account_email, account_tenant, account_code, amount)
VALUES ('order_after_restart', 'owner@example.com', 'tenant_a', 'A', -1)`); err == nil ||
		!strings.Contains(err.Error(), "orders_amount_nonnegative") {
		t.Fatalf("reopened CHECK enforcement error = %v", err)
	}
}

func openKitDBDropDatabase(t *testing.T, root string) (*Tenant, *dbProxy) {
	t.Helper()
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	_, config, found := resolveServe(tenant, "drop.kitdb")
	if !found || config.database == nil {
		tenant.Close()
		t.Fatal("KitDB DROP test database is unavailable")
	}
	return tenant, config.database
}

func executeKitDBDropSQL(t *testing.T, database *dbProxy, source string) kitDBRemoteResult {
	t.Helper()
	result, err := runKitDBDropSQL(database, source)
	if err != nil {
		t.Fatalf("execute %q: %v", source, err)
	}
	return result
}

func runKitDBDropSQL(database *dbProxy, source string) (kitDBRemoteResult, error) {
	return executeKitDBRemoteSQL(
		context.Background(), nil, database, source,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
}

func countKitDBDroppedStructKeys(t *testing.T, database *dbProxy, definition *StructDef) int {
	t.Helper()
	managed, err := kitDBForRequest(database.tenant, database.dbName, nil).database()
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	snapshot, err := managed.database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()

	total := 0
	for _, namespace := range []byte{
		kitDBMigrationNamespace,
		kitDBPhysicalNamespace,
		kitDBStatisticsNamespace,
		kitDBRowNamespace,
		kitDBShadowRowNamespace,
		kitDBUniqueNamespace,
		kitDBIndexNamespace,
	} {
		prefix, err := kitDBFixedKey(namespace, definition.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
		if err != nil {
			t.Fatal(err)
		}
		for cursor.Next() {
			total++
		}
		if err := cursor.Err(); err != nil {
			_ = cursor.Close()
			t.Fatal(err)
		}
		_ = cursor.Close()
	}
	return total
}

func waitForKitDBSecondaryIndexIdle(t *testing.T, database *dbProxy) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		managed, err := kitDBForRequest(database.tenant, database.dbName, nil).database()
		if err != nil {
			t.Fatal(err)
		}
		managed.writeMu.Lock()
		pending, pendingErr := hasKitDBPendingSecondaryIndex(managed.database)
		managed.writeMu.Unlock()
		managed.Release()
		if pendingErr != nil {
			t.Fatal(pendingErr)
		}
		if !pending {
			if err := database.refreshKitDBCatalogDefinitions(); err != nil {
				t.Fatal(err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("secondary-index maintenance did not become idle")
		}
		time.Sleep(time.Millisecond)
	}
}

func countKitDBStructNamespaceKeys(
	t *testing.T,
	database *dbProxy,
	definition *StructDef,
	namespace byte,
) int {
	t.Helper()
	managed, err := kitDBForRequest(database.tenant, database.dbName, nil).database()
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	snapshot, err := managed.database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	prefix, err := kitDBFixedKey(namespace, definition.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	total := 0
	for cursor.Next() {
		total++
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	return total
}
