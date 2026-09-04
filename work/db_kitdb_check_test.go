package work

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBRemoteSQLCheckConstraintsAreAtomicAndSurviveRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("checks.kitdb", {}, { token: "checks-secret", access: "readwrite" });
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
		_, config, found := resolveServe(tenant, "checks.kitdb")
		if !found || config.database == nil {
			tenant.Close()
			t.Fatal("CHECK SQL registration is unavailable")
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

	if _, err := parseKitSQL(
		`CREATE TABLE denied (id TEXT PRIMARY KEY, price INTEGER CHECK (price > ?))`,
		kitSQLBindings{positional: []value.Value{value.New(0)}, named: map[string]value.Value{}},
	); err == nil || !strings.Contains(err.Error(), "cannot use a bound parameter") {
		t.Fatalf("bound CHECK parse error = %v", err)
	}
	if _, err := parseKitSQL(
		`CREATE TABLE denied (id TEXT PRIMARY KEY, price INTEGER, CHECK (COUNT(*) > 0))`,
		kitSQLBindings{named: map[string]value.Value{}},
	); err == nil || !strings.Contains(err.Error(), "aggregate expressions") {
		t.Fatalf("aggregate CHECK parse error = %v", err)
	}

	tenant, database := start()
	if _, err := execute(database, `CREATE TABLE products (
  id TEXT PRIMARY KEY,
  price INTEGER NOT NULL,
  discount INTEGER,
  title TEXT CHECK (length(trim(title)) > 0),
  CONSTRAINT price_nonnegative CHECK (price >= 0),
  CONSTRAINT discount_valid CHECK (discount IS NULL OR (discount >= 0 AND discount <= price))
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `CREATE TABLE broken (
  id TEXT PRIMARY KEY,
  price INTEGER,
  CHECK (missing > price)
)`); err == nil || !strings.Contains(err.Error(), `no such column: missing`) {
		t.Fatalf("missing-field CHECK error = %v", err)
	}
	if _, err := execute(database, `INSERT INTO products (id, price, discount, title) VALUES
  ('p1', 10, 5, 'Alpha'), ('p2', 20, 10, 'Beta'), ('nullable', 0, NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `INSERT INTO products (id, price, discount, title) VALUES ('negative', -1, NULL, 'Bad')`); err == nil ||
		!strings.Contains(err.Error(), `check constraint "price_nonnegative" failed`) {
		t.Fatalf("negative price error = %v", err)
	}
	if _, err := execute(database, `INSERT INTO products (id, price, discount, title) VALUES ('blank', 1, 0, '   ')`); err == nil ||
		!strings.Contains(err.Error(), `check constraint "check_products_title_1" failed`) {
		t.Fatalf("blank title error = %v", err)
	}

	if _, err := execute(database, `UPDATE products SET discount = 15 WHERE id IN ('p1', 'p2')`); err == nil ||
		!strings.Contains(err.Error(), `check constraint "discount_valid" failed`) {
		t.Fatalf("multi-row CHECK error = %v", err)
	}
	unchanged, err := execute(database, `SELECT id, discount FROM products WHERE id IN ('p1', 'p2') ORDER BY id`)
	if err != nil || len(unchanged.rows) != 2 || unchanged.rows[0][1].N != 5 || unchanged.rows[1][1].N != 10 {
		t.Fatalf("multi-row CHECK rollback = %#v, %v", unchanged, err)
	}
	if _, err := execute(database, `UPDATE products SET discount = NULL WHERE id = 'p1'`); err != nil {
		t.Fatalf("NULL CHECK semantics: %v", err)
	}

	if _, err := execute(database, `ALTER TABLE products ADD COLUMN stock INTEGER NOT NULL DEFAULT 0 CHECK (stock >= 0)`); err != nil {
		t.Fatalf("ALTER ADD checked column: %v", err)
	}
	stock, err := execute(database, `SELECT COUNT(*) FROM products WHERE stock = 0`)
	if err != nil || len(stock.rows) != 1 || stock.rows[0][0].N != 3 {
		t.Fatalf("checked column backfill = %#v, %v", stock, err)
	}
	if _, err := execute(database, `UPDATE products SET stock = -1 WHERE id = 'p2'`); err == nil ||
		!strings.Contains(err.Error(), `check constraint "check_products_stock_1" failed`) {
		t.Fatalf("checked ALTER column error = %v", err)
	}
	catalog, err := execute(database, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'products'`)
	if err != nil || len(catalog.rows) != 1 ||
		!strings.Contains(catalog.rows[0][0].String(), `CONSTRAINT "price_nonnegative" CHECK`) ||
		!strings.Contains(catalog.rows[0][0].String(), `CONSTRAINT "check_products_stock_1" CHECK`) {
		t.Fatalf("CHECK catalog SQL = %#v, %v", catalog, err)
	}
	tenant.Close()

	reopened, reopenedDatabase := start()
	defer reopened.Close()
	if _, err := execute(reopenedDatabase, `UPDATE products SET price = -1, discount = NULL WHERE id = 'p2'`); err == nil ||
		!strings.Contains(err.Error(), `check constraint "price_nonnegative" failed`) {
		t.Fatalf("reopened CHECK error = %v", err)
	}
	status, err := execute(reopenedDatabase, `SELECT price, stock FROM products WHERE id = 'p2'`)
	if err != nil || len(status.rows) != 1 || status.rows[0][0].N != 20 || status.rows[0][1].N != 0 {
		t.Fatalf("reopened CHECK row = %#v, %v", status, err)
	}
}

func TestKitDBDSLCheckMigrationValidatesExistingRows(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	versionOne := `import { router, database } from "kitwork";
const { kitdb, struct, text, int } = database;
const products = struct({ id: text().key(), stock: int() });
const db = kitdb("check-migration.kitdb", { products });
router.get((ctx) => {
  const action = ctx.query("action");
  if (action === "seed") return ctx.json(db.products.create({ id: "p1", stock: -1 }));
  if (action === "clean") return ctx.json(db.products.where("id", "=", "p1").update({ stock: 1 }));
  return ctx.json({ count: db.products.count() });
});`
	versionTwo := `import { router, database } from "kitwork";
const { kitdb, struct, text, int } = database;
const products = struct({
  id: text().key(),
  stock: int().check("stock_nonnegative", ">=", 0)
});
const db = kitdb("check-migration.kitdb", { products }, { migrate: true });
router.get((ctx) => {
  if (ctx.query("action") === "invalid") return ctx.json(db.products.create({ id: "bad", stock: -1 }));
  return ctx.json({ count: db.products.count(), stock: db.products.find("p1").stock });
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
	seeded := start()
	if response := request(seeded, "/?action=seed"); response.Code != http.StatusOK {
		t.Fatalf("CHECK migration seed = %d %s", response.Code, response.Body.String())
	}
	seeded.Close()

	writeRouter(versionTwo)
	rejected := start()
	failed := request(rejected, "/")
	if failed.Code != http.StatusInternalServerError || !strings.Contains(failed.Body.String(), `check constraint "stock_nonnegative" failed`) {
		t.Fatalf("dirty CHECK migration = %d %s", failed.Code, failed.Body.String())
	}
	rejected.Close()

	writeRouter(versionOne)
	cleaner := start()
	if response := request(cleaner, "/?action=clean"); response.Code != http.StatusOK {
		t.Fatalf("CHECK migration cleanup = %d %s", response.Code, response.Body.String())
	}
	cleaner.Close()

	writeRouter(versionTwo)
	migrated := start()
	status := request(migrated, "/")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"stock":1`) {
		t.Fatalf("clean CHECK migration = %d %s", status.Code, status.Body.String())
	}
	invalid := request(migrated, "/?action=invalid")
	if invalid.Code != http.StatusInternalServerError || !strings.Contains(invalid.Body.String(), `check constraint "stock_nonnegative" failed`) {
		t.Fatalf("published DSL CHECK = %d %s", invalid.Code, invalid.Body.String())
	}
	migrated.Close()

	reopened := start()
	defer reopened.Close()
	status = request(reopened, "/")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"stock":1`) {
		t.Fatalf("reopened DSL CHECK = %d %s", status.Code, status.Body.String())
	}
}

func TestKitDBCheckIdentitySurvivesFieldRename(t *testing.T) {
	stored := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id": {kind: "text", primary: true, seq: 1},
		"price": {
			kind: "integer", seq: 2,
			checks: []colCheckRef{{name: "price_nonnegative", operator: ">=", value: value.New(0)}},
		},
	})
	current := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id": {kind: "text", primary: true, seq: 1},
		"amount": {
			kind: "integer", from: "price", seq: 2,
			checks: []colCheckRef{{name: "price_nonnegative", operator: ">=", value: value.New(0)}},
		},
	})
	storedCheck := stored.CheckConstraints[0]
	if err := reconcileKitDBDefinition(stored, current); err != nil {
		t.Fatal(err)
	}
	currentCheck := current.CheckConstraints[0]
	if currentCheck.ID != storedCheck.ID ||
		currentCheck.Expression.Arguments[0].Field != storedCheck.Expression.Arguments[0].Field {
		t.Fatalf("CHECK identity changed across rename: stored=%#v current=%#v", storedCheck, currentCheck)
	}
	rendered, err := renderStructCheckExpression(current, currentCheck.Expression)
	if err != nil || !strings.Contains(rendered, `"amount"`) || strings.Contains(rendered, `"price"`) {
		t.Fatalf("renamed CHECK rendering = %q, %v", rendered, err)
	}
	if err := validateKitDBRow(current, map[string]value.Value{
		"id": value.New("p1"), "amount": value.New(-1),
	}); err == nil || !strings.Contains(err.Error(), `check constraint "price_nonnegative" failed`) {
		t.Fatalf("renamed CHECK validation = %v", err)
	}
}

func TestKitDBColumnCheckUsesLogicalValues(t *testing.T) {
	definition := bindStructDef("settings", nil, map[string]*ColumnSpec{
		"id": {kind: "text", primary: true, seq: 1},
		"active": {
			kind: "bool", seq: 2,
			checks: []colCheckRef{{name: "active_required", operator: "=", value: value.New(true)}},
		},
		"amount": {
			kind: "decimal", seq: 3,
			checks: []colCheckRef{{name: "amount_minimum", operator: ">=", value: value.New(2)}},
		},
	})
	definitions := map[string]*StructDef{definition.Name: definition}
	if err := validateKitDBStruct(definition, definitions); err != nil {
		t.Fatal(err)
	}
	valid := map[string]value.Value{
		"id": value.New("one"), "active": coerceWrite("bool", value.New(true)),
		"amount": coerceWrite("decimal", value.New(10)),
	}
	if err := validateKitDBRow(definition, valid); err != nil {
		t.Fatalf("logical CHECK values = %v", err)
	}
	invalid := cloneKitDBRow(valid)
	invalid["active"] = coerceWrite("bool", value.New(false))
	if err := validateKitDBRow(definition, invalid); err == nil || !strings.Contains(err.Error(), `"active_required"`) {
		t.Fatalf("logical boolean CHECK = %v", err)
	}
	invalid = cloneKitDBRow(valid)
	invalid["amount"] = coerceWrite("decimal", value.New(1))
	if err := validateKitDBRow(definition, invalid); err == nil || !strings.Contains(err.Error(), `"amount_minimum"`) {
		t.Fatalf("logical decimal CHECK = %v", err)
	}
}
