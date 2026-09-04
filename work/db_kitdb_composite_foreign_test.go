package work

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBCompositeForeignDSLReadsParentWriteInSameTransaction(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, text, ref } = database;
const accounts = struct({
  id: text().key(),
  tenant: text().notNull().unique("accounts_tenant_code", 1),
  code: text().notNull().unique("accounts_tenant_code", 2)
});
const orders = struct({
  id: text().key(),
  account_tenant: ref(accounts.tenant, "orders_account", 1).notNull(),
  account_code: ref(accounts.code, "orders_account", 2).notNull()
});
const db = kitdb("foreign-dsl.kitdb", { accounts, orders });
router.get((ctx) => {
  const action = ctx.query("action");
  if (action === "seed") {
    return ctx.json(db.transaction((tx) => {
      tx.accounts.create({ id: "a1", tenant: "north", code: "A" });
      tx.orders.create({ id: "o1", account_tenant: "north", account_code: "A" });
      tx.accounts.create({ id: "a2", tenant: "south", code: "B" });
      return { accounts: tx.accounts.count(), orders: tx.orders.count() };
    }));
  }
  if (action === "crossed") {
    return ctx.json(db.orders.create({ id: "bad", account_tenant: "north", account_code: "B" }));
  }
  return ctx.json({ accounts: db.accounts.count(), orders: db.orders.count() });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	start := func() *Tenant {
		t.Helper()
		tenant := NewTenant(root, "localhost")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		return tenant
	}
	request := func(tenant *Tenant, path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		tenant.Serve(response, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
		return response
	}

	tenant := start()
	seeded := request(tenant, "/?action=seed")
	if seeded.Code != http.StatusOK || !strings.Contains(seeded.Body.String(), `"accounts":2`) ||
		!strings.Contains(seeded.Body.String(), `"orders":1`) {
		t.Fatalf("same-transaction parent/child = %d %s", seeded.Code, seeded.Body.String())
	}
	crossed := request(tenant, "/?action=crossed")
	if crossed.Code != http.StatusInternalServerError || !strings.Contains(crossed.Body.String(), `constraint "orders_account"`) {
		t.Fatalf("crossed DSL tuple = %d %s", crossed.Code, crossed.Body.String())
	}
	tenant.Close()

	reopened := start()
	defer reopened.Close()
	status := request(reopened, "/")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"accounts":2`) ||
		!strings.Contains(status.Body.String(), `"orders":1`) {
		t.Fatalf("reopened DSL tuple = %d %s", status.Code, status.Body.String())
	}
}

func TestKitDBRemoteSQLCompositeForeignKeyIsAtomicAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("foreign-sql.kitdb", {}, { token: "foreign-secret", access: "readwrite" });
router.get(() => 1);`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	start := func() (*Tenant, *dbProxy) {
		t.Helper()
		tenant := NewTenant(root, "localhost")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		_, config, found := resolveServe(tenant, "foreign-sql.kitdb")
		if !found || config.database == nil {
			tenant.Close()
			t.Fatal("composite foreign-key SQL registration is unavailable")
		}
		return tenant, config.database
	}
	execute := func(database *dbProxy, source string) (kitDBRemoteResult, error) {
		t.Helper()
		return executeKitDBRemoteSQL(
			context.Background(), nil, database, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
	}

	tenant, database := start()
	if _, err := execute(database, `CREATE TABLE accounts (
  id TEXT PRIMARY KEY,
  tenant TEXT NOT NULL,
  code TEXT NOT NULL,
  CONSTRAINT accounts_tenant_code UNIQUE (tenant, code)
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `CREATE TABLE orders (
  id TEXT PRIMARY KEY,
  account_tenant TEXT,
  account_code TEXT,
  CONSTRAINT orders_account FOREIGN KEY (account_tenant, account_code)
    REFERENCES accounts (tenant, code) ON DELETE RESTRICT ON UPDATE NO ACTION
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `INSERT INTO accounts (id, tenant, code) VALUES
  ('a1', 'north', 'A'), ('a2', 'south', 'B')`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `INSERT INTO orders (id, account_tenant, account_code) VALUES
  ('o1', 'north', 'A'), ('nullable', 'north', NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `INSERT INTO orders (id, account_tenant, account_code)
VALUES ('crossed', 'north', 'B')`); err == nil || !strings.Contains(err.Error(), `constraint "orders_account"`) {
		t.Fatalf("crossed tuple error = %v", err)
	}
	if _, err := execute(database, `UPDATE accounts SET tenant = 'moved' WHERE id = 'a1'`); err == nil ||
		!strings.Contains(err.Error(), `constraint "orders_account"`) {
		t.Fatalf("referenced parent update error = %v", err)
	}
	if _, err := execute(database, `DELETE FROM accounts WHERE id = 'a1'`); err == nil ||
		!strings.Contains(err.Error(), `constraint "orders_account"`) {
		t.Fatalf("referenced parent delete error = %v", err)
	}

	foreignKeys, err := execute(database, `PRAGMA foreign_key_list(orders)`)
	if err != nil || len(foreignKeys.rows) != 2 {
		t.Fatalf("composite PRAGMA foreign_key_list = %#v, %v", foreignKeys, err)
	}
	if foreignKeys.rows[0][0].N != foreignKeys.rows[1][0].N || foreignKeys.rows[0][1].N != 0 ||
		foreignKeys.rows[1][1].N != 1 ||
		!reflect.DeepEqual(remoteResultColumnStrings(foreignKeys, 3), []string{"account_tenant", "account_code"}) ||
		!reflect.DeepEqual(remoteResultColumnStrings(foreignKeys, 4), []string{"tenant", "code"}) {
		t.Fatalf("composite foreign-key metadata = %#v", foreignKeys.rows)
	}
	catalog, err := execute(database, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'orders'`)
	if err != nil || len(catalog.rows) != 1 ||
		!strings.Contains(catalog.rows[0][0].String(), `CONSTRAINT "orders_account" FOREIGN KEY`) {
		t.Fatalf("composite foreign-key sqlite_master = %#v, %v", catalog, err)
	}

	if _, err := execute(database, `CREATE TABLE loose_accounts (id TEXT PRIMARY KEY, tenant TEXT, code TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `CREATE TABLE invalid_orders (
  id TEXT PRIMARY KEY,
  tenant TEXT,
  code TEXT,
  FOREIGN KEY (tenant, code) REFERENCES loose_accounts (tenant, code)
)`); err == nil || !strings.Contains(err.Error(), "not one ordered tuple-unique") {
		t.Fatalf("non-unique composite target error = %v", err)
	}
	if _, err := execute(database, `CREATE TABLE cascading_orders (
  id TEXT PRIMARY KEY,
  tenant TEXT,
  code TEXT,
  CONSTRAINT cascading_account FOREIGN KEY (tenant, code)
    REFERENCES accounts (tenant, code) ON DELETE CASCADE ON UPDATE CASCADE
)`); err != nil {
		t.Fatalf("create composite cascade = %v", err)
	}
	if _, err := execute(database, `INSERT INTO cascading_orders (id, tenant, code) VALUES ('cascade', 'south', 'B')`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `UPDATE accounts SET tenant = 'south-2', code = 'B-2' WHERE id = 'a2'`); err != nil {
		t.Fatalf("composite cascade update = %v", err)
	}
	cascadeUpdated, err := execute(database, `SELECT tenant, code FROM cascading_orders WHERE id = 'cascade'`)
	if err != nil || len(cascadeUpdated.rows) != 1 ||
		cascadeUpdated.rows[0][0].String() != "south-2" || cascadeUpdated.rows[0][1].String() != "B-2" {
		t.Fatalf("composite cascade update row = %#v, %v", cascadeUpdated, err)
	}
	if _, err := execute(database, `DELETE FROM accounts WHERE id = 'a2'`); err != nil {
		t.Fatalf("composite cascade delete = %v", err)
	}
	cascaded, err := execute(database, `SELECT COUNT(*) FROM cascading_orders`)
	if err != nil || len(cascaded.rows) != 1 || cascaded.rows[0][0].N != 0 {
		t.Fatalf("composite cascade rows = %#v, %v", cascaded, err)
	}
	if _, err := execute(database, `CREATE TABLE reversed_orders (
  id TEXT PRIMARY KEY,
  code TEXT,
  tenant TEXT,
  FOREIGN KEY (code, tenant) REFERENCES accounts (code, tenant)
)`); err == nil || !strings.Contains(err.Error(), "not one ordered tuple-unique") {
		t.Fatalf("reversed composite target error = %v", err)
	}
	if _, err := execute(database, `DELETE FROM orders WHERE id = 'o1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `DELETE FROM accounts WHERE id = 'a1'`); err != nil {
		t.Fatal(err)
	}
	tenant.Close()

	reopened, reopenedDatabase := start()
	defer reopened.Close()
	counts, err := execute(reopenedDatabase, `SELECT COUNT(*) AS total FROM orders`)
	if err != nil || len(counts.rows) != 1 || counts.rows[0][0].N != 1 {
		t.Fatalf("reopened child rows = %#v, %v", counts, err)
	}
	reopenedForeignKeys, err := execute(reopenedDatabase, `PRAGMA foreign_key_list(orders)`)
	if err != nil || !reflect.DeepEqual(
		remoteResultColumnStrings(reopenedForeignKeys, 3),
		[]string{"account_tenant", "account_code"},
	) {
		t.Fatalf("reopened composite foreign key = %#v, %v", reopenedForeignKeys, err)
	}
}

func TestKitDBCompositeForeignIdentitySurvivesFieldRenames(t *testing.T) {
	storedParentColumns := map[string]*ColumnSpec{
		"id":     {kind: "text", primary: true, seq: 1},
		"tenant": {kind: "text", seq: 2, uniques: []colUniqueRef{{name: "accounts_tenant_code", pos: 1}}},
		"code":   {kind: "text", seq: 3, uniques: []colUniqueRef{{name: "accounts_tenant_code", pos: 2}}},
	}
	storedChildColumns := map[string]*ColumnSpec{
		"id":             {kind: "text", primary: true, seq: 1},
		"account_tenant": {kind: "text", seq: 2, fk: &fkRef{target: storedParentColumns["tenant"], table: "accounts", column: "tenant", name: "orders_account", pos: 1}},
		"account_code":   {kind: "text", seq: 3, fk: &fkRef{target: storedParentColumns["code"], table: "accounts", column: "code", name: "orders_account", pos: 2}},
	}
	storedParent := bindStructDef("accounts", nil, storedParentColumns)
	storedChild := bindStructDef("orders", nil, storedChildColumns)

	currentParentColumns := map[string]*ColumnSpec{
		"id":    {kind: "text", primary: true, seq: 1},
		"scope": {kind: "text", from: "tenant", seq: 2, uniques: []colUniqueRef{{name: "accounts_tenant_code", pos: 1}}},
		"code":  {kind: "text", seq: 3, uniques: []colUniqueRef{{name: "accounts_tenant_code", pos: 2}}},
	}
	currentChildColumns := map[string]*ColumnSpec{
		"id":            {kind: "text", primary: true, seq: 1},
		"account_scope": {kind: "text", from: "account_tenant", seq: 2, fk: &fkRef{target: currentParentColumns["scope"], table: "accounts", column: "scope", name: "orders_account", pos: 1}},
		"account_code":  {kind: "text", seq: 3, fk: &fkRef{target: currentParentColumns["code"], table: "accounts", column: "code", name: "orders_account", pos: 2}},
	}
	currentParent := bindStructDef("accounts", nil, currentParentColumns)
	currentChild := bindStructDef("orders", nil, currentChildColumns)
	if err := reconcileKitDBDefinition(storedParent, currentParent); err != nil {
		t.Fatal(err)
	}
	if err := reconcileKitDBDefinition(storedChild, currentChild); err != nil {
		t.Fatal(err)
	}
	if currentChild.ForeignConstraints[0].ID != storedChild.ForeignConstraints[0].ID ||
		!reflect.DeepEqual(currentChild.ForeignConstraints[0].Fields, storedChild.ForeignConstraints[0].Fields) ||
		!reflect.DeepEqual(currentChild.ForeignConstraints[0].TargetFields, storedChild.ForeignConstraints[0].TargetFields) {
		t.Fatalf(
			"composite foreign identity changed across rename: stored=%#v current=%#v",
			storedChild.ForeignConstraints[0], currentChild.ForeignConstraints[0],
		)
	}
	definitions := map[string]*StructDef{"accounts": currentParent, "orders": currentChild}
	if err := validateStructForeignConstraints(currentChild, definitions); err != nil {
		t.Fatal(err)
	}
	encodedParent, err := json.Marshal(currentParent)
	if err != nil {
		t.Fatal(err)
	}
	encodedChild, err := json.Marshal(currentChild)
	if err != nil {
		t.Fatal(err)
	}
	hydratedParent, err := decodeKitDBCatalog(encodedParent, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	hydratedChild, err := decodeKitDBCatalog(encodedChild, "orders")
	if err != nil {
		t.Fatal(err)
	}
	hydrated := &dbProxy{
		tables: map[string]map[string]*ColumnSpec{
			"accounts": hydratedParent.columns,
			"orders":   hydratedChild.columns,
		},
		structs: map[string]*StructDef{"accounts": hydratedParent, "orders": hydratedChild},
	}
	hydrated.resolveForeignKeys()
	if err := validateStructForeignConstraints(hydratedChild, hydrated.structs); err != nil {
		t.Fatal(err)
	}
	if got := hydratedChild.columns["account_scope"].fk.targetFieldID; got != storedChild.ForeignConstraints[0].TargetFields[0] {
		t.Fatalf("hydrated target field identity = %q, want %q", got, storedChild.ForeignConstraints[0].TargetFields[0])
	}
}

func TestKitDBCompositeForeignMigrationValidatesExistingRowsBeforePublication(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	versionOne := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const accounts = struct({
  id: text().key(),
  tenant: text().notNull().unique("accounts_tenant_code", 1),
  code: text().notNull().unique("accounts_tenant_code", 2)
});
const orders = struct({ id: text().key(), account_tenant: text(), account_code: text() });
const db = kitdb("foreign-migration.kitdb", { accounts, orders });
router.get((ctx) => {
  const action = ctx.query("action");
  if (action === "seed") return ctx.json(db.transaction((tx) => {
    tx.accounts.create({ id: "a1", tenant: "north", code: "A" });
    tx.accounts.create({ id: "a2", tenant: "south", code: "B" });
    tx.orders.create({ id: "valid", account_tenant: "north", account_code: "A" });
    tx.orders.create({ id: "invalid", account_tenant: "north", account_code: "B" });
    return { count: tx.orders.count() };
  }));
  if (action === "clean") return ctx.json({ removed: db.orders.where("id", "=", "invalid").delete() });
  return ctx.json({ count: db.orders.count() });
});`
	versionTwo := `import { router, database } from "kitwork";
const { kitdb, struct, text, ref } = database;
const accounts = struct({
  id: text().key(),
  tenant: text().notNull().unique("accounts_tenant_code", 1),
  code: text().notNull().unique("accounts_tenant_code", 2)
});
const orders = struct({
  id: text().key(),
  account_tenant: ref(accounts.tenant, "orders_account", 1),
  account_code: ref(accounts.code, "orders_account", 2)
});
const db = kitdb("foreign-migration.kitdb", { accounts, orders }, { migrate: true });
router.get((ctx) => {
  if (ctx.query("action") === "crossed") {
    return ctx.json(db.orders.create({ id: "crossed", account_tenant: "north", account_code: "B" }));
  }
  return ctx.json({ count: db.orders.count() });
});`
	writeRouter := func(source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := func() *Tenant {
		t.Helper()
		tenant := NewTenant(root, "localhost")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		return tenant
	}
	request := func(tenant *Tenant, path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		tenant.Serve(response, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
		return response
	}

	writeRouter(versionOne)
	tenant := start()
	seeded := request(tenant, "/?action=seed")
	if seeded.Code != http.StatusOK || !strings.Contains(seeded.Body.String(), `"count":2`) {
		t.Fatalf("migration seed = %d %s", seeded.Code, seeded.Body.String())
	}
	tenant.Close()

	writeRouter(versionTwo)
	rejected := start()
	failed := request(rejected, "/")
	if failed.Code != http.StatusInternalServerError || !strings.Contains(failed.Body.String(), `constraint "orders_account"`) {
		t.Fatalf("dirty migration = %d %s", failed.Code, failed.Body.String())
	}
	rejected.Close()

	writeRouter(versionOne)
	cleaner := start()
	cleaned := request(cleaner, "/?action=clean")
	if cleaned.Code != http.StatusOK || !strings.Contains(cleaned.Body.String(), `"removed":1`) {
		t.Fatalf("migration cleanup = %d %s", cleaned.Code, cleaned.Body.String())
	}
	cleaner.Close()

	writeRouter(versionTwo)
	migrated := start()
	status := request(migrated, "/")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"count":1`) {
		t.Fatalf("clean migration = %d %s", status.Code, status.Body.String())
	}
	crossed := request(migrated, "/?action=crossed")
	if crossed.Code != http.StatusInternalServerError || !strings.Contains(crossed.Body.String(), `constraint "orders_account"`) {
		t.Fatalf("published migrated constraint = %d %s", crossed.Code, crossed.Body.String())
	}
	migrated.Close()

	reopened := start()
	defer reopened.Close()
	reopenedStatus := request(reopened, "/")
	if reopenedStatus.Code != http.StatusOK || !strings.Contains(reopenedStatus.Body.String(), `"count":1`) {
		t.Fatalf("reopened migrated constraint = %d %s", reopenedStatus.Code, reopenedStatus.Body.String())
	}
}

func TestCompositeForeignSchemaContractLowersToSQLite(t *testing.T) {
	parent := map[string]*ColumnSpec{
		"id":     {kind: "text", primary: true, seq: 1},
		"tenant": {kind: "text", seq: 2, uniques: []colUniqueRef{{name: "accounts_tenant_code", pos: 1}}},
		"code":   {kind: "text", seq: 3, uniques: []colUniqueRef{{name: "accounts_tenant_code", pos: 2}}},
	}
	child := map[string]*ColumnSpec{
		"id":             {kind: "text", primary: true, seq: 1},
		"account_tenant": {kind: "text", seq: 2, fk: &fkRef{target: parent["tenant"], name: "orders_account", pos: 1}},
		"account_code":   {kind: "text", seq: 3, fk: &fkRef{target: parent["code"], name: "orders_account", pos: 2}},
	}
	resolver := &dbProxy{tables: map[string]map[string]*ColumnSpec{"accounts": parent, "orders": child}}
	resolver.resolveForeignKeys()
	if err := validateSchema(child); err != nil {
		t.Fatal(err)
	}
	ddl := schemaDDL("orders", child)
	if !strings.Contains(ddl, `CONSTRAINT "orders_account" FOREIGN KEY ("account_tenant", "account_code")`) ||
		!strings.Contains(ddl, `REFERENCES "accounts" ("tenant", "code")`) {
		t.Fatalf("SQLite composite foreign-key DDL = %q", ddl)
	}

	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "foreign.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(database, "accounts", parent, false, false); err != nil {
		t.Fatal(err)
	}
	if err := migrate(database, "orders", child, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO accounts (id, tenant, code) VALUES ('a1', 'north', 'A'), ('a2', 'south', 'B')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO orders (id, account_tenant, account_code) VALUES ('o1', 'north', 'A')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO orders (id, account_tenant, account_code) VALUES ('bad', 'north', 'B')`); err == nil {
		t.Fatal("SQLite accepted a crossed composite foreign-key tuple")
	}
}
