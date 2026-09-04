package work

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBCompositePrimaryKeyIsThePhysicalRowIdentity(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const anchor = struct({ id: text().key() });
const db = kitdb("composite-primary.kitdb", { anchor }, { token: "test", access: "readwrite" });
router.get(() => db.anchor.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	_, config, found := resolveServe(tenant, "composite-primary.kitdb")
	if !found || config.database == nil {
		t.Fatal("composite primary test database is unavailable")
	}
	database := config.database
	execute := func(source string) (kitDBRemoteResult, error) {
		return executeKitDBRemoteSQL(
			context.Background(), nil, database, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
	}
	executeWith := func(source string, positional ...value.Value) (kitDBRemoteResult, error) {
		return executeKitDBRemoteSQL(
			context.Background(), nil, database, source,
			kitSQLBindings{positional: positional, named: map[string]value.Value{}}, false,
		)
	}
	mustExecute := func(source string) kitDBRemoteResult {
		t.Helper()
		result, err := execute(source)
		if err != nil {
			t.Fatalf("execute %q: %v", source, err)
		}
		return result
	}

	mustExecute(`CREATE TABLE shops (
  merchant TEXT NOT NULL,
  id INTEGER NOT NULL,
  title TEXT,
  PRIMARY KEY (merchant, id)
)`)
	_, definitions := database.schemaSnapshot()
	definition := definitions["shops"]
	if definition == nil {
		t.Fatal("shops definition was not published")
	}
	primary := definition.primaryFields()
	if len(primary) != 2 || primary[0].Name != "merchant" || primary[0].PrimaryOrder != 1 ||
		primary[1].Name != "id" || primary[1].PrimaryOrder != 2 {
		t.Fatalf("composite primary fields = %#v", primary)
	}
	if primary[0].Unique || primary[1].Unique {
		t.Fatalf("composite key components must not be individually unique: %#v", primary)
	}

	rowOne := map[string]value.Value{"merchant": value.New("tiki"), "id": value.New(1)}
	rowTwo := map[string]value.Value{"merchant": value.New("tiki-1"), "id": value.New(2)}
	keyOne, err := kitDBRowKeyForRow(definition, rowOne)
	if err != nil {
		t.Fatal(err)
	}
	keyTwo, err := kitDBRowKeyForRow(definition, rowTwo)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(keyOne, keyTwo) {
		t.Fatal("distinct composite tuples encoded to the same physical row key")
	}

	mustExecute(`INSERT INTO shops (merchant, id, title) VALUES
  ('tiki', 1, 'Keyboard'),
  ('tiki', 2, 'Mouse'),
  ('shopee', 1, 'Screen')`)
	if _, err := execute(`INSERT INTO shops (merchant, id, title) VALUES ('tiki', 1, 'Duplicate')`); err == nil ||
		!strings.Contains(err.Error(), `merchant="tiki"`) {
		t.Fatalf("duplicate composite primary error = %v", err)
	}
	mustExecute(`CREATE TABLE orders (
  order_id TEXT PRIMARY KEY,
  merchant TEXT NOT NULL,
  product_id INTEGER NOT NULL,
  FOREIGN KEY (merchant, product_id) REFERENCES shops (merchant, id)
)`)
	mustExecute(`INSERT INTO orders (order_id, merchant, product_id) VALUES ('o1', 'tiki', 1)`)
	if _, err := execute(`INSERT INTO orders (order_id, merchant, product_id) VALUES ('o2', 'tiki', 999)`); err == nil ||
		!strings.Contains(err.Error(), "reference a missing shops key") {
		t.Fatalf("composite primary foreign key error = %v", err)
	}
	if _, err := execute(`CREATE TABLE invalid_reference (
  id TEXT PRIMARY KEY,
  merchant TEXT REFERENCES shops (merchant)
	)`); err == nil || !strings.Contains(err.Error(), "not independently primary or unique") {
		t.Fatalf("partial composite primary reference error = %v", err)
	}

	explain := mustExecute(`EXPLAIN QUERY PLAN SELECT * FROM shops WHERE merchant = 'tiki' AND id = 2`)
	if len(explain.rows) != 1 || len(explain.rows[0]) < 4 ||
		!strings.Contains(explain.rows[0][3].String(), "KITDB PRIMARY KEY shops.merchant,id") {
		t.Fatalf("composite primary explain = %#v", explain)
	}
	selected := mustExecute(`SELECT merchant, id, title FROM shops WHERE merchant = 'tiki' AND id = 2`)
	if len(selected.rows) != 1 || selected.rows[0][2].String() != "Mouse" {
		t.Fatalf("composite primary lookup = %#v", selected)
	}
	parameterPlan, err := executeWith(
		`EXPLAIN QUERY PLAN SELECT * FROM shops WHERE merchant = $1 AND id = $2`,
		value.New("tiki"), value.New("2"),
	)
	if err != nil || len(parameterPlan.rows) != 1 ||
		!strings.Contains(parameterPlan.rows[0][3].String(), "KITDB PRIMARY KEY shops.merchant,id") {
		t.Fatalf("composite primary parameter explain = %#v, %v", parameterPlan, err)
	}
	parameterized, err := executeWith(
		`SELECT merchant, id, title FROM shops WHERE merchant = $1 AND id = $2`,
		value.New("tiki"), value.New("2"),
	)
	if err != nil || len(parameterized.rows) != 1 || parameterized.rows[0][2].String() != "Mouse" {
		t.Fatalf("composite primary parameter lookup = %#v, %v", parameterized, err)
	}
	if _, err := executeWith(
		`SELECT * FROM shops WHERE merchant = $1 AND id = $2`,
		value.New("tiki"), value.New("not-an-integer"),
	); err == nil || !strings.Contains(err.Error(), "expects an integer") {
		t.Fatalf("invalid composite primary parameter error = %v", err)
	}
	prefixExplain := mustExecute(`EXPLAIN QUERY PLAN
SELECT * FROM shops WHERE merchant = 'tiki' ORDER BY id LIMIT 20`)
	if len(prefixExplain.rows) != 1 || len(prefixExplain.rows[0]) < 4 {
		t.Fatalf("composite primary prefix explain = %#v", prefixExplain)
	}
	prefixDetail := prefixExplain.rows[0][3].String()
	if !strings.Contains(prefixDetail, "KITDB INDEX PREFIX shops_pkey (merchant,id)") ||
		!strings.Contains(prefixDetail, "INDEX ORDER") {
		t.Fatalf("composite primary prefix explain detail = %q", prefixDetail)
	}
	prefixRows := mustExecute(`SELECT merchant, id FROM shops
WHERE merchant = 'tiki' ORDER BY id LIMIT 20`)
	if len(prefixRows.rows) != 2 || prefixRows.rows[0][1].N != 1 || prefixRows.rows[1][1].N != 2 {
		t.Fatalf("composite primary prefix rows = %#v", prefixRows)
	}

	upserted := mustExecute(`INSERT INTO shops (merchant, id, title) VALUES ('tiki', 2, 'Updated')
ON CONFLICT (id, merchant) DO UPDATE SET title = EXCLUDED.title RETURNING title`)
	if len(upserted.rows) != 1 || upserted.rows[0][0].String() != "Updated" {
		t.Fatalf("composite primary upsert = %#v", upserted)
	}

	info := mustExecute(`PRAGMA table_info(shops)`)
	if len(info.rows) != 3 || info.rows[0][5].N != 1 || info.rows[1][5].N != 2 || info.rows[2][5].N != 0 {
		t.Fatalf("composite primary table_info = %#v", info)
	}
	indexes := kitDBPostgresIndexes(definitions)
	foundPrimary := false
	for _, index := range indexes {
		if index.table.Name == "shops" && index.primary {
			foundPrimary = len(index.columns) == 2 && index.columns[0].Name == "merchant" && index.columns[1].Name == "id"
		}
	}
	if !foundPrimary {
		t.Fatalf("PostgreSQL catalog did not expose shops_pkey(merchant,id): %#v", indexes)
	}
}

func TestKitDBCompositeKeyDSLRequiresExplicitContiguousOrder(t *testing.T) {
	valid := bindStructDef("shopping", nil, map[string]*ColumnSpec{
		"merchant": {kind: "text", primary: true, primaryOrder: 1, seq: 1},
		"id":       {kind: "integer", primary: true, primaryOrder: 2, seq: 2},
	})
	if err := validateKitDBStruct(valid, map[string]*StructDef{"shopping": valid}); err != nil {
		t.Fatalf("valid composite key: %v", err)
	}

	missingOrder := bindStructDef("shopping", nil, map[string]*ColumnSpec{
		"merchant": {kind: "text", primary: true, seq: 1},
		"id":       {kind: "integer", primary: true, primaryOrder: 2, seq: 2},
	})
	if err := validateKitDBStruct(missingOrder, map[string]*StructDef{"shopping": missingOrder}); err == nil ||
		!strings.Contains(err.Error(), "explicit .key(position)") {
		t.Fatalf("missing composite key order error = %v", err)
	}

	repeatedOrder := bindStructDef("shopping", nil, map[string]*ColumnSpec{
		"merchant": {kind: "text", primary: true, primaryOrder: 1, seq: 1},
		"id":       {kind: "integer", primary: true, primaryOrder: 1, seq: 2},
	})
	if err := validateKitDBStruct(repeatedOrder, map[string]*StructDef{"shopping": repeatedOrder}); err == nil ||
		!strings.Contains(err.Error(), "repeat position 1") {
		t.Fatalf("repeated composite key order error = %v", err)
	}
}
