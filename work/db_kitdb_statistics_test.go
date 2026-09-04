package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBStatisticsCodecRoundTripsAndRejectsCorruption(t *testing.T) {
	want := kitDBStatistics{
		StructID: "00112233445566778899aabbccddeeff", SchemaHash: "schema-hash",
		AnalyzedTransaction: 42, Rows: 100,
		Indexes: []kitDBIndexStatistics{{
			Signature: "ffeeddccbbaa99887766554433221100", Name: "category_price",
			Fields: []string{"category", "price"}, Entries: 90,
			DistinctPrefixes: []uint64{10, 80},
		}},
	}
	encoded, err := encodeKitDBStatistics(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeKitDBStatistics(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statistics round trip = %#v, want %#v", got, want)
	}
	corrupt := append([]byte(nil), encoded...)
	corrupt[len(corrupt)-5] ^= 0xff
	if _, err := decodeKitDBStatistics(corrupt); err == nil {
		t.Fatal("corrupt statistics passed checksum validation")
	}
}

func TestKitDBStatisticsSketchHasBoundedUsefulError(t *testing.T) {
	sketch := kitDBStatisticsSketch{}
	for number := uint64(0); number < 10_000; number++ {
		hash := uint64(14695981039346656037)
		for _, next := range []byte(fmt.Sprintf("value-%d", number)) {
			hash ^= uint64(next)
			hash *= 1099511628211
		}
		sketch.add(hash)
	}
	estimate := sketch.estimate(10_000)
	if estimate < 9_000 || estimate > 10_000 {
		t.Fatalf("distinct estimate = %d, want within 10%% and capped to entries", estimate)
	}
}

func TestKitDBAnalyzeMakesPlannerEstimatesUsefulAndInvalidatesOnWrite(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text, int } = database;
const products = struct({
  id: text().key(),
  category: text().notNull().index(),
  sku: text().notNull().index(),
  price: int().notNull()
});
const db = kitdb("statistics.kitdb", { products });
router.get((ctx) => {
  const table = db.products;
  const rows = [];
  for (let i = 0; i < 100; i++) {
    rows.push({ id: "product-" + i, category: i % 2 === 0 ? "even" : "odd", sku: "SKU-" + i, price: i });
  }
  table.createMany(rows);
  const before = table.where("category", "=", "even").where("sku", "=", "SKU-42").explain();
  const analyzed = table.analyze();
  const after = table.where("category", "=", "even").where("sku", "=", "SKU-42").explain();
  table.create({ id: "product-100", category: "even", sku: "SKU-100", price: 100 });
  const stale = table.where("category", "=", "even").where("sku", "=", "SKU-42").explain();
  return ctx.json({ before: before, analyzed: analyzed, after: after, stale: stale });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
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
		t.Fatalf("statistics route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	type explain struct {
		Index                 *string  `json:"index"`
		Statistics            string   `json:"statistics"`
		StatisticsTransaction *string  `json:"statisticsTransaction"`
		EstimatedRows         *float64 `json:"estimatedRows"`
		EstimateKind          string   `json:"estimateKind"`
	}
	var response struct {
		Before   explain `json:"before"`
		After    explain `json:"after"`
		Stale    explain `json:"stale"`
		Analyzed struct {
			Rows    int    `json:"rows"`
			Indexes int    `json:"indexes"`
			Status  string `json:"status"`
		} `json:"analyzed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Before.Index == nil || *response.Before.Index != "idx_products_category" ||
		response.Before.Statistics != "missing" || response.Before.EstimatedRows != nil {
		t.Fatalf("pre-ANALYZE plan = %#v", response.Before)
	}
	if response.Analyzed.Rows != 100 || response.Analyzed.Indexes != 2 || response.Analyzed.Status != "current" {
		t.Fatalf("ANALYZE result = %#v", response.Analyzed)
	}
	if response.After.Index == nil || *response.After.Index != "idx_products_sku" ||
		response.After.Statistics != "current" || response.After.StatisticsTransaction == nil ||
		response.After.EstimatedRows == nil || *response.After.EstimatedRows < 1 || *response.After.EstimatedRows > 2 ||
		response.After.EstimateKind != "approximate" {
		t.Fatalf("post-ANALYZE plan = %#v", response.After)
	}
	if response.Stale.Statistics != "stale" || response.Stale.EstimatedRows != nil ||
		response.Stale.EstimateKind != "unavailable" {
		t.Fatalf("stale statistics plan = %#v", response.Stale)
	}
}

func TestKitDBSQLAnalyzeAndStatisticsPragmaUseTheSamePlannerMetadata(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text, int } = database;
const products = struct({
  id: text().key(),
  category: text().notNull().index(),
  price: int().notNull()
});
const suppliers = struct({
  id: text().key(),
  region: text().notNull().index()
});
const db = kitdb("sql-statistics.kitdb", { products, suppliers }, { token: "statistics-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	_, config, found := resolveServe(tenant, "sql-statistics.kitdb")
	if !found || config.database == nil {
		t.Fatal("KitDB SQL statistics fixture is unavailable")
	}
	values := make([]string, 20)
	for index := range values {
		values[index] = fmt.Sprintf("('p-%d', 'category-%d', %d)", index, index%4, index)
	}
	if _, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`INSERT INTO products (id, category, price) VALUES `+strings.Join(values, ", "),
		kitSQLBindings{named: map[string]value.Value{}}, false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`INSERT INTO suppliers (id, region) VALUES ('supplier-1', 'north'), ('supplier-2', 'south')`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	); err != nil {
		t.Fatal(err)
	}
	analyzed, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `ANALYZE`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || analyzed.affected != 2 {
		t.Fatalf("SQL ANALYZE = %#v, %v", analyzed, err)
	}
	statistics, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `PRAGMA statistics(products)`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || len(statistics.rows) != 2 || statistics.rows[0][3].Int() != 20 ||
		statistics.rows[0][6].String() != "current" || statistics.rows[1][1].String() != "idx_products_category" {
		t.Fatalf("statistics pragma = %#v, %v", statistics, err)
	}
	supplierStatistics, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `PRAGMA statistics(suppliers)`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || len(supplierStatistics.rows) != 2 ||
		supplierStatistics.rows[0][3].Int() != 2 || supplierStatistics.rows[0][6].String() != "current" {
		t.Fatalf("supplier statistics pragma = %#v, %v", supplierStatistics, err)
	}
	explained, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`EXPLAIN QUERY PLAN SELECT * FROM products WHERE category = 'category-1' ORDER BY category DESC LIMIT 2`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || len(explained.rows) != 1 {
		t.Fatalf("statistics EXPLAIN = %#v, %v", explained, err)
	}
	detail := explained.rows[0][3].String()
	for _, expected := range []string{"KITDB INDEX", "STATS CURRENT@", "ESTIMATE", "EARLY STOP"} {
		if !strings.Contains(detail, expected) {
			t.Errorf("EXPLAIN detail %q does not contain %q", detail, expected)
		}
	}
	if _, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`INSERT INTO products (id, category, price) VALUES ('p-20', 'category-0', 20)`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	); err != nil {
		t.Fatal(err)
	}
	statistics, err = executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `PRAGMA statistics(products)`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || statistics.rows[0][6].String() != "stale" {
		t.Fatalf("mutated product statistics = %#v, %v", statistics, err)
	}
	supplierStatistics, err = executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `PRAGMA statistics(suppliers)`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || supplierStatistics.rows[0][6].String() != "current" {
		t.Fatalf("unrelated supplier statistics = %#v, %v", supplierStatistics, err)
	}
	if analyzed, err = executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `ANALYZE products`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	); err != nil || analyzed.affected != 1 {
		t.Fatalf("targeted SQL ANALYZE = %#v, %v", analyzed, err)
	}
	supplierStatistics, err = executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `PRAGMA statistics(suppliers)`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || supplierStatistics.rows[0][6].String() != "current" {
		t.Fatalf("supplier statistics after targeted ANALYZE = %#v, %v", supplierStatistics, err)
	}
	if _, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `ANALYZE`,
		kitSQLBindings{named: map[string]value.Value{}}, true,
	); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("readonly ANALYZE error = %v", err)
	}
}

func TestKitDBAnalyzeConflictCannotPublishOldStatisticsAsCurrent(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const products = struct({ id: text().key(), category: text().notNull().index() });
const db = kitdb("statistics-conflict.kitdb", { products }, { token: "statistics-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	_, config, found := resolveServe(tenant, "statistics-conflict.kitdb")
	if !found || config.database == nil {
		t.Fatal("KitDB statistics conflict fixture is unavailable")
	}
	execute := func(source string) kitDBRemoteResult {
		t.Helper()
		result, err := executeKitDBRemoteSQL(
			context.Background(), nil, config.database, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	execute(`INSERT INTO products (id, category) VALUES ('p-1', 'one')`)
	execute(`ANALYZE products`)

	table, err := kitDBRemoteTable(config.database, nil, "products")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureKitDBRemoteTableReady(table); err != nil {
		t.Fatal(err)
	}
	transaction, err := beginKitDBRecordTransaction(table.tenant, nil, table.dbName, false)
	if err != nil {
		t.Fatal(err)
	}
	analysisTable := *table
	analysisTable.transaction = transaction
	if err := analysisTable.ensureKitDBStruct(); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	statistics, err := collectKitDBStatistics(context.Background(), &analysisTable, transaction)
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}

	execute(`INSERT INTO products (id, category) VALUES ('p-2', 'two')`)
	encoded, err := encodeKitDBStatistics(statistics)
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	key, err := kitDBStatisticsKey(table.definition)
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Put(key, encoded); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); !errors.Is(err, errKitDBTransactionConflict) {
		t.Fatalf("stale ANALYZE commit error = %v", err)
	}

	visible := execute(`PRAGMA statistics(products)`)
	if len(visible.rows) != 2 || visible.rows[0][3].Int() != 1 ||
		visible.rows[0][6].String() != "stale" {
		t.Fatalf("statistics after conflicted ANALYZE = %#v", visible.rows)
	}
}
