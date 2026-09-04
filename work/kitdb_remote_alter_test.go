package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBRemoteAlterColumnIsAtomicAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("alter.kitdb", {}, { token: "alter-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant, database := openKitDBAlterDatabase(t, root)
	executeKitDBAlterSQL(t, database, `CREATE TABLE products (
  id TEXT PRIMARY KEY,
  sku TEXT NOT NULL UNIQUE,
  quantity TEXT NOT NULL,
  rank TEXT UNIQUE,
  legacy TEXT,
  note TEXT,
  parent_sku TEXT REFERENCES products(sku) ON DELETE RESTRICT,
  guarded INTEGER CHECK (guarded >= 0)
)`)
	executeKitDBAlterSQL(t, database, `CREATE TABLE orders (
  id TEXT PRIMARY KEY,
  product_sku TEXT REFERENCES products(sku) ON DELETE RESTRICT
)`)
	executeKitDBAlterSQL(t, database, `INSERT INTO products
	  (id, sku, quantity, rank, legacy, note, parent_sku, guarded)
	  VALUES
	  ('p1', 'KIT-1', '10', '1', '7', 'remove me', NULL, 1),
	  ('p2', 'KIT-2', '20', '01', 'bad', 'remove me too', 'KIT-1', 2)`)

	executeKitDBAlterSQL(t, database, `ALTER TABLE products RENAME COLUMN sku TO code`)
	executeKitDBAlterSQL(t, database, `INSERT INTO orders (id, product_sku) VALUES ('o1', 'KIT-1')`)
	foreignKeys := executeKitDBAlterSQL(t, database, `PRAGMA foreign_key_list(orders)`)
	if len(foreignKeys.rows) != 1 || foreignKeys.rows[0][4].String() != "code" {
		t.Fatalf("renamed foreign-key catalog = %#v", foreignKeys.rows)
	}
	renamed := executeKitDBAlterSQL(t, database, `SELECT code FROM products WHERE id = 'p1'`)
	if len(renamed.rows) != 1 || renamed.rows[0][0].String() != "KIT-1" {
		t.Fatalf("renamed row = %#v", renamed.rows)
	}
	if _, err := runKitDBAlterSQL(database, `SELECT sku FROM products`); err == nil || !strings.Contains(err.Error(), `no such column: sku`) {
		t.Fatalf("old column lookup error = %v", err)
	}

	executeKitDBAlterSQL(t, database, `ALTER TABLE products ALTER COLUMN quantity TYPE INTEGER`)
	converted := executeKitDBAlterSQL(t, database, `SELECT quantity FROM products WHERE id = 'p2'`)
	if len(converted.rows) != 1 || converted.rows[0][0].K != value.Number || converted.rows[0][0].N != 20 {
		t.Fatalf("converted quantity = %#v", converted.rows)
	}

	if _, err := runKitDBAlterSQL(database, `ALTER TABLE products ALTER COLUMN rank TYPE INTEGER`); err == nil ||
		!strings.Contains(err.Error(), "duplicate unique values") {
		t.Fatalf("unique-collision type migration error = %v", err)
	}
	unchangedRank := executeKitDBAlterSQL(t, database, `SELECT rank FROM products WHERE id = 'p2'`)
	if len(unchangedRank.rows) != 1 || unchangedRank.rows[0][0].K != value.String || unchangedRank.rows[0][0].String() != "01" {
		t.Fatalf("rank changed after rejected migration: %#v", unchangedRank.rows)
	}

	if _, err := runKitDBAlterSQL(database, `ALTER TABLE products ALTER COLUMN legacy TYPE INTEGER`); err == nil ||
		!strings.Contains(err.Error(), `field "legacy" cannot cast text to int32`) {
		t.Fatalf("invalid-cast migration error = %v", err)
	}
	unchangedLegacy := executeKitDBAlterSQL(t, database, `SELECT legacy FROM products WHERE id = 'p2'`)
	if len(unchangedLegacy.rows) != 1 || unchangedLegacy.rows[0][0].String() != "bad" {
		t.Fatalf("legacy changed after rejected migration: %#v", unchangedLegacy.rows)
	}
	executeKitDBAlterSQL(t, database, `CREATE INDEX products_legacy_idx ON products (legacy)`)

	executeKitDBAlterSQL(t, database, `ALTER TABLE products DROP COLUMN note`)
	executeKitDBAlterSQL(t, database, `ALTER TABLE products DROP COLUMN IF EXISTS missing`)
	if _, err := runKitDBAlterSQL(database, `SELECT note FROM products`); err == nil || !strings.Contains(err.Error(), `no such column: note`) {
		t.Fatalf("dropped column lookup error = %v", err)
	}
	for source, expected := range map[string]string{
		`ALTER TABLE products DROP COLUMN id`:                      "primary key column",
		`ALTER TABLE products DROP COLUMN code`:                    "is referenced by",
		`ALTER TABLE products DROP COLUMN rank`:                    "index or unique constraint",
		`ALTER TABLE products DROP COLUMN legacy`:                  "index or unique constraint",
		`ALTER TABLE products DROP COLUMN guarded`:                 "used by check constraint",
		`ALTER TABLE products ALTER COLUMN code TYPE INTEGER`:      "is referenced by",
		`ALTER TABLE orders ALTER COLUMN product_sku TYPE INTEGER`: "participates in a foreign key",
	} {
		if _, err := runKitDBAlterSQL(database, source); err == nil || !strings.Contains(err.Error(), expected) {
			t.Errorf("%s error = %v, want %q", source, err, expected)
		}
	}

	tenant.Close()
	tenant, database = openKitDBAlterDatabase(t, root)
	defer tenant.Close()
	reopened := executeKitDBAlterSQL(t, database, `SELECT code, quantity FROM products WHERE id = 'p1'`)
	if len(reopened.rows) != 1 || reopened.rows[0][0].String() != "KIT-1" ||
		reopened.rows[0][1].K != value.Number || reopened.rows[0][1].N != 10 {
		t.Fatalf("reopened altered row = %#v", reopened.rows)
	}
	_, reopenedDefinitions := database.schemaSnapshot()
	orderDefinition := reopenedDefinitions["orders"]
	orderField, found := kitDBField(orderDefinition, "product_sku")
	if !found || orderField.Reference == nil || orderField.Reference.Field != "code" {
		t.Fatalf("reopened foreign-key catalog = %#v", orderDefinition)
	}
	productDefinition := reopenedDefinitions["products"]
	parentField, found := kitDBField(productDefinition, "parent_sku")
	if !found || parentField.Reference == nil || parentField.Reference.Field != "code" {
		t.Fatalf("reopened self-reference catalog = %#v", productDefinition)
	}
	executeKitDBAlterSQL(t, database, `INSERT INTO orders (id, product_sku) VALUES ('o2', 'KIT-2')`)
	if _, err := runKitDBAlterSQL(database, `SELECT note FROM products`); err == nil {
		t.Fatal("dropped column returned after restart")
	}
	executeKitDBAlterSQL(t, database, `ALTER TABLE products RENAME COLUMN code TO sku`)
	restoredName := executeKitDBAlterSQL(t, database, `SELECT sku FROM products WHERE id = 'p1'`)
	if len(restoredName.rows) != 1 || restoredName.rows[0][0].String() != "KIT-1" {
		t.Fatalf("restored column name row = %#v", restoredName.rows)
	}
	restoredForeignKeys := executeKitDBAlterSQL(t, database, `PRAGMA foreign_key_list(orders)`)
	if len(restoredForeignKeys.rows) != 1 || restoredForeignKeys.rows[0][4].String() != "sku" {
		t.Fatalf("restored foreign-key catalog = %#v", restoredForeignKeys.rows)
	}
}

func TestCastKitDBMigrationValue(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		input   value.Value
		kind    value.Kind
		text    string
		wantErr string
	}{
		{name: "text integer", from: "text", to: "integer", input: value.New("42"), kind: value.Number, text: "42"},
		{name: "integer text", from: "integer", to: "text", input: value.New(42), kind: value.String, text: "42"},
		{name: "text boolean", from: "text", to: "bool", input: value.New("true"), kind: value.Number, text: "1"},
		{name: "json text", from: "json", to: "text", input: value.New(`{"ok":true}`), kind: value.String, text: `{"ok":true}`},
		{name: "fraction integer", from: "float", to: "integer", input: value.New(1.5), wantErr: "not an integer"},
		{name: "invalid number", from: "text", to: "float", input: value.New("nope"), wantErr: "not numeric"},
		{name: "inexact integer", from: "text", to: "integer", input: value.New("9007199254740993"), wantErr: "exact integer range"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converted, err := castKitDBMigrationValue(test.from, test.to, test.input)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("cast error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if converted.K != test.kind || converted.Text() != test.text {
				t.Fatalf("cast = %#v (%q), want kind=%s text=%q", converted, converted.Text(), test.kind, test.text)
			}
		})
	}
}

func TestKitDBExplicitMigrationIntentDoesNotReclassifyIndexOnlyChange(t *testing.T) {
	stored, target := generationReplacementDefinitions()
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}
	steps := planKitDBMigration(stored, target, true)
	if _, _, ok := kitDBOnlyChangesSecondaryIndexes(steps, stored, target); !ok {
		t.Fatalf("index-only migration was reclassified: %#v", steps)
	}
}

func TestKitDBCatalogMetadataCanPublishWithOnlineIndexBuild(t *testing.T) {
	stored := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":    {kind: "text", primary: true, seq: 1},
		"title": {kind: "text", seq: 2},
	})
	target := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id":    {kind: "text", primary: true, seq: 1, indexes: []colIndexRef{{name: "products_id_order"}}},
		"title": {kind: "text", seq: 2, searchable: true, searchWt: 5},
	})
	if err := reconcileKitDBDefinition(stored, target); err != nil {
		t.Fatal(err)
	}
	steps := planKitDBMigration(stored, target, true)
	build, retire, ok := kitDBOnlyChangesSecondaryIndexes(steps, stored, target)
	if !ok || len(build) != 1 || len(retire) != 1 || !retire[0].implicitPrimary {
		t.Fatalf("index plus catalog metadata was reclassified: build=%#v retire=%#v steps=%#v", build, retire, steps)
	}
	if !target.Fields[1].Searchable || target.Fields[1].SearchWeight != 5 {
		t.Fatalf("target search metadata = %#v", target.Fields[1])
	}
}

func openKitDBAlterDatabase(t *testing.T, root string) (*Tenant, *dbProxy) {
	t.Helper()
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tenant.Close)
	_, config, found := resolveServe(tenant, "alter.kitdb")
	if !found || config.database == nil {
		tenant.Close()
		t.Fatal("KitDB ALTER test database is unavailable")
	}
	return tenant, config.database
}

func executeKitDBAlterSQL(t *testing.T, database *dbProxy, source string) kitDBRemoteResult {
	t.Helper()
	result, err := runKitDBAlterSQL(database, source)
	if err != nil {
		t.Fatalf("execute %q: %v", source, err)
	}
	return result
}

func runKitDBAlterSQL(database *dbProxy, source string) (kitDBRemoteResult, error) {
	return executeKitDBRemoteSQL(
		context.Background(), nil, database, source,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
}
