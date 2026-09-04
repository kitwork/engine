package work

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

func TestKitDBOrderedScalarComponentPreservesNumberAndTextOrder(t *testing.T) {
	testCases := []struct {
		name   string
		values []value.Value
	}{
		{
			name: "number",
			values: []value.Value{
				value.New(-1e12), value.New(-100.5), value.New(-1), value.New(0),
				value.New(0.125), value.New(1), value.New(100.5), value.New(1e12),
			},
		},
		{
			name: "text",
			values: []value.Value{
				value.New(""), value.New("\x00"), value.New("\x00a"), value.New("a"),
				value.New("a\x00"), value.New("aa"), value.New("b"), value.New("á"),
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for left := range testCase.values {
				leftEncoded, err := kitDBOrderedScalarComponent(testCase.values[left])
				if err != nil {
					t.Fatal(err)
				}
				for right := range testCase.values {
					rightEncoded, err := kitDBOrderedScalarComponent(testCase.values[right])
					if err != nil {
						t.Fatal(err)
					}
					logical := sign(kitDBCompareValues(testCase.values[left], testCase.values[right]))
					physical := sign(bytes.Compare(leftEncoded, rightEncoded))
					if logical != physical {
						t.Fatalf(
							"logical order %q vs %q = %d, encoded order = %d (%x vs %x)",
							testCase.values[left].Text(), testCase.values[right].Text(), logical, physical,
							leftEncoded, rightEncoded,
						)
					}
				}
			}
		})
	}

	negativeZero := value.New(0.0)
	negativeZero.N = -negativeZero.N
	zeroEncoded, _ := kitDBOrderedScalarComponent(value.New(0.0))
	negativeZeroEncoded, _ := kitDBOrderedScalarComponent(negativeZero)
	if !bytes.Equal(zeroEncoded, negativeZeroEncoded) {
		t.Fatalf("positive and negative zero have different keys: %x vs %x", zeroEncoded, negativeZeroEncoded)
	}

	left := appendCompositeKitDBTestKey(t, value.New("a"), value.New("bc"))
	right := appendCompositeKitDBTestKey(t, value.New("ab"), value.New("c"))
	if bytes.Equal(left, right) {
		t.Fatalf("composite components collide: %x", left)
	}
}

func sign(number int) int {
	switch {
	case number < 0:
		return -1
	case number > 0:
		return 1
	default:
		return 0
	}
}

func appendCompositeKitDBTestKey(t *testing.T, items ...value.Value) []byte {
	t.Helper()
	var key []byte
	for _, item := range items {
		component, err := kitDBOrderedScalarComponent(item)
		if err != nil {
			t.Fatal(err)
		}
		key = append(key, component...)
	}
	return key
}

func TestKitDBSecondaryIndexCodecUpgradeIsAtomicAndIdempotent(t *testing.T) {
	columns := map[string]*ColumnSpec{
		"id":     {kind: "text", primary: true, seq: 1},
		"status": {kind: "text", seq: 2, indexes: []colIndexRef{{name: "status_price"}}},
		"price":  {kind: "integer", seq: 3, indexes: []colIndexRef{{name: "status_price"}}},
	}
	definition := bindStructDef("products", nil, columns)
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "legacy.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, definition, nil, ""); err != nil {
		t.Fatal(err)
	}

	index := collectIndexes(definition.Name, definition.columns)[0]
	legacyKeys := make([][]byte, 0, 3)
	rows := []map[string]value.Value{
		{"id": value.New("p1"), "status": value.New("a"), "price": value.New(2)},
		{"id": value.New("p2"), "status": value.New("active"), "price": value.New(10)},
		{"id": value.New("p3"), "status": value.New("á"), "price": value.New(-5)},
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		rowKey, err := kitDBRowKey(definition, row["id"])
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeKitDBValidatedRow(definition, row, nil)
		if err != nil {
			t.Fatal(err)
		}
		legacyKey, err := legacyKitDBSecondaryIndexKey(definition, index, row)
		if err != nil {
			t.Fatal(err)
		}
		legacyKeys = append(legacyKeys, legacyKey)
		if err := tx.Put(rowKey, encoded); err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(legacyKey, rowKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if ready, err := ensureKitDBSecondaryIndexCodec(database, definition); err != nil || ready {
		t.Fatalf("codec admission ready=%t err=%v", ready, err)
	}
	completeKitDBSecondaryIndexForTest(t, database)
	if _, err := ensureKitDBSecondaryIndexCodec(database, definition); err != nil {
		t.Fatal(err)
	}
	markerKey, _ := kitDBSecondaryIndexCodecMarkerKey(definition)
	marker, found, err := database.Get(markerKey)
	if err != nil || !found || !bytes.Equal(marker, kitDBSecondaryIndexCodecMarker) {
		t.Fatalf("codec marker = %x, found=%t, err=%v", marker, found, err)
	}
	for _, legacyKey := range legacyKeys {
		if _, found, err := database.Get(legacyKey); err != nil || found {
			t.Fatalf("legacy index key survived upgrade: %x, found=%t, err=%v", legacyKey, found, err)
		}
	}
	for _, row := range rows {
		rowKey, _ := kitDBRowKey(definition, row["id"])
		entries, err := kitDBSecondaryIndexEntries(definition, row, rowKey)
		if err != nil || len(entries) != 1 {
			t.Fatalf("v2 entries = %#v, err=%v", entries, err)
		}
		owner, found, err := database.Get(entries[0].key)
		if err != nil || !found || !bytes.Equal(owner, rowKey) {
			t.Fatalf("v2 index owner = %x, found=%t, err=%v", owner, found, err)
		}
	}
	transaction, err := database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensureKitDBSecondaryIndexCodec(database, definition); err != nil {
		t.Fatal(err)
	}
	if after, err := database.LastTransaction(); err != nil || after != transaction {
		t.Fatalf("idempotent codec check advanced transaction %d -> %d, err=%v", transaction, after, err)
	}
}

func TestKitDBSecondaryIndexCodecRejectsUnknownMarker(t *testing.T) {
	definition := bindStructDef("items", nil, map[string]*ColumnSpec{
		"id":   {kind: "text", primary: true, seq: 1},
		"code": {kind: "text", seq: 2, indexes: []colIndexRef{{}}},
	})
	database, err := kitdbengine.Open(filepath.Join(t.TempDir(), "unknown-codec.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := commitKitDBCatalog(database, definition, nil, ""); err != nil {
		t.Fatal(err)
	}
	markerKey, err := kitDBSecondaryIndexCodecMarkerKey(definition)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(markerKey, []byte("KITDB-SECONDARY-INDEX\x7f")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureKitDBSecondaryIndexCodec(database, definition); err == nil ||
		!strings.Contains(err.Error(), "unsupported secondary-index codec marker") {
		t.Fatalf("unknown codec marker error = %v", err)
	}
}

func legacyKitDBSecondaryIndexKey(
	definition *StructDef,
	index indexDef,
	row map[string]value.Value,
) ([]byte, error) {
	identity := stableSchemaID("index", definition.ID+":"+index.name+":"+strings.Join(index.columns, ","))
	key, err := kitDBFixedKey(kitDBIndexNamespace, definition.ID, identity)
	if err != nil {
		return nil, err
	}
	for _, column := range index.columns {
		component, err := kitDBScalarComponent(row[column])
		if err != nil {
			return nil, err
		}
		key = append(key, component...)
	}
	primary, _ := definition.primaryField()
	component, err := kitDBScalarComponent(row[primary.Name])
	if err != nil {
		return nil, err
	}
	return append(key, component...), nil
}

func TestKitDBPlannerV2MatchesSQLiteAndReadsTransactionalRanges(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { sqlite, kitdb, struct, text, int } = database;
const products = struct({
  id: text().key(),
  status: text().notNull().index("status_price"),
  price: int().notNull().index("status_price"),
  sku: text().notNull().index(),
  title: text().notNull()
});
const sql = sqlite("planner-v2.db", { products });
const native = kitdb("planner-v2.kitdb", { products });
router.get((ctx) => {
  const rows = [
    { id: "p1", status: "active", price: 2, sku: "a", title: "One" },
    { id: "p2", status: "active", price: 10, sku: "aa", title: "Two" },
    { id: "p3", status: "active", price: 7, sku: "ab", title: "Three" },
    { id: "p4", status: "disabled", price: 5, sku: "b", title: "Four" },
    { id: "p5", status: "active", price: 20, sku: "á", title: "Five" }
  ];
  for (const row of rows) {
    sql.products.create(row);
    native.products.create(row);
  }
  const read = (source) => ({
    range: source.products.where("status", "=", "active").where("price", ">=", 3).where("price", "<", 20).orderBy("price", "asc").limit(2).list(),
    exclusive: source.products.where("status", "=", "active").where("price", ">", 7).where("price", "<=", 20).orderBy("price", "asc").list(),
    contradictoryCount: source.products.where("status", "=", "active").where("price", ">=", 20).where("price", "<", 3).count(),
    textRange: source.products.where("sku", ">=", "a").where("sku", "<", "b").orderBy("sku", "asc").list(),
    ordered: source.products.orderBy("status", "asc").orderBy("price", "asc").list(),
    descending: source.products.where("status", "=", "active").where("price", ">=", 3).orderBy("price", "desc").limit(2).list(),
    orderedDescending: source.products.orderBy("status", "desc").orderBy("price", "desc").limit(3).list(),
    orderedPrimary: source.products.orderBy("id", "asc").limit(3).list()
  });
  const sqliteRows = read(sql);
  const kitdbRows = read(native);
  const transactionRows = native.transaction((tx) => {
    tx.products.create({ id: "p6", status: "active", price: 30, sku: "a0", title: "Six" });
    return {
      ascending: tx.products.where("status", "=", "active").where("price", ">=", 3).orderBy("price", "asc").limit(2).list(),
      descending: tx.products.where("status", "=", "active").where("price", ">=", 3).orderBy("price", "desc").limit(2).list()
    };
  });
  return ctx.json({
    sqlite: sqliteRows,
    kitdb: kitdbRows,
    transactionRows,
    plans: {
      prefix: native.products.where("status", "=", "active").explain(),
      range: native.products.where("status", "=", "active").where("price", ">=", 3).where("price", "<", 20).orderBy("price", "asc").limit(2).explain(),
      textRange: native.products.where("sku", ">=", "a").where("sku", "<", "b").orderBy("sku", "asc").explain(),
      orderOnly: native.products.orderBy("status", "asc").orderBy("price", "asc").limit(3).explain(),
      primaryOrder: native.products.orderBy("id", "asc").limit(3).explain(),
      descending: native.products.where("status", "=", "active").where("price", ">=", 3).orderBy("price", "desc").limit(2).explain()
    }
  });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		SQLite          map[string]any              `json:"sqlite"`
		KitDB           map[string]any              `json:"kitdb"`
		TransactionRows map[string][]map[string]any `json:"transactionRows"`
		Plans           map[string]plannerV2Explain `json:"plans"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(response.SQLite, response.KitDB) {
		t.Fatalf("SQLite/KitDB results differ:\nsqlite=%#v\nkitdb=%#v", response.SQLite, response.KitDB)
	}
	if ascending := response.TransactionRows["ascending"]; len(ascending) != 2 ||
		ascending[0]["id"] != "p3" || ascending[1]["id"] != "p2" {
		t.Fatalf("transactional range did not read its indexed write in order: %#v", response.TransactionRows)
	}
	if descending := response.TransactionRows["descending"]; len(descending) != 2 ||
		descending[0]["id"] != "p6" || descending[1]["id"] != "p5" {
		t.Fatalf("transactional reverse range did not merge its indexed write: %#v", response.TransactionRows)
	}
	assertPlannerV2Explain(t, response.Plans["prefix"], "index_prefix", "status_price", 1, "", "none", false, true, "forward")
	assertPlannerV2Explain(t, response.Plans["range"], "index_range", "status_price", 1, "price", "index", true, true, "forward")
	assertPlannerV2Explain(t, response.Plans["textRange"], "index_range", "idx_products_sku", 0, "sku", "index", true, true, "forward")
	assertPlannerV2Explain(t, response.Plans["orderOnly"], "index_order", "status_price", 0, "", "index", true, true, "forward")
	assertPlannerV2Explain(t, response.Plans["primaryOrder"], "index_order", "products_pkey", 0, "", "index", true, true, "forward")
	assertPlannerV2Explain(t, response.Plans["descending"], "index_range", "status_price", 1, "price", "index", true, true, "reverse")
}

func TestKitDBPlannerV2FallsBackWhenIndexNarrowingIsNotProvablySafe(t *testing.T) {
	definition := bindStructDef("events", nil, map[string]*ColumnSpec{
		"id":          {kind: "text", primary: true, seq: 1},
		"price":       {kind: "integer", seq: 2, indexes: []colIndexRef{{}}},
		"observed_at": {kind: "datetime", seq: 3, indexes: []colIndexRef{{}}},
	})
	table := &SchemaTable{table: definition.Name, columns: definition.columns, definition: definition}
	testCases := []struct {
		name       string
		conditions []query.Condition
		want       kitDBAccessKind
	}{
		{
			name:       "wrong bound type",
			conditions: []query.Condition{{Column: "price", Operator: ">=", Value: "ten", Logic: "AND"}},
			want:       kitDBAccessScan,
		},
		{
			name:       "mixed datetime representation",
			conditions: []query.Condition{{Column: "observed_at", Operator: ">=", Value: "2026-01-01", Logic: "AND"}},
			want:       kitDBAccessScan,
		},
		{
			name: "or predicate",
			conditions: []query.Condition{
				{Column: "price", Operator: ">=", Value: 10, Logic: "AND"},
				{Column: "id", Operator: "=", Value: "free", Logic: "OR"},
			},
			want: kitDBAccessScan,
		},
		{
			name:       "safe equality",
			conditions: []query.Condition{{Column: "price", Operator: "=", Value: 10, Logic: "AND"}},
			want:       kitDBAccessIndex,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			access, err := table.planKitDBAccess(query.ExecutionPlan{Conditions: testCase.conditions})
			if err != nil || access.kind != testCase.want {
				t.Fatalf("access = %#v, err=%v, want %s", access, err, testCase.want)
			}
		})
	}
}

type plannerV2Explain struct {
	Access         string  `json:"access"`
	Index          *string `json:"index"`
	EqualityPrefix int     `json:"equalityPrefix"`
	RangeField     *string `json:"rangeField"`
	Sort           string  `json:"sort"`
	OrderCovered   bool    `json:"orderCovered"`
	EarlyStop      bool    `json:"earlyStop"`
	Direction      string  `json:"direction"`
}

func assertPlannerV2Explain(
	t *testing.T,
	plan plannerV2Explain,
	access, index string,
	equalityPrefix int,
	rangeField, sortMode string,
	orderCovered, earlyStop bool,
	direction string,
) {
	t.Helper()
	actualRange := ""
	if plan.RangeField != nil {
		actualRange = *plan.RangeField
	}
	if plan.Access != access || plan.Index == nil || *plan.Index != index ||
		plan.EqualityPrefix != equalityPrefix || actualRange != rangeField || plan.Sort != sortMode ||
		plan.OrderCovered != orderCovered || plan.EarlyStop != earlyStop || plan.Direction != direction {
		t.Fatalf("planner v2 = %#v", plan)
	}
}
