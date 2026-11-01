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
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

func TestKitDBRemoteSQLParserFailsClosed(t *testing.T) {
	bindings := kitSQLBindings{
		positional: []value.Value{value.New("KIT-1"), value.New(20)},
		named:      map[string]value.Value{"status": value.New("active")},
	}
	statement, err := parseKitSQL(
		`SELECT sku, price FROM products WHERE sku = ? AND price >= ? AND status = :status ORDER BY price DESC LIMIT 20`,
		bindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if statement.kind != "select" || statement.table != "products" || len(statement.conditions) != 3 || len(statement.orders) != 1 || statement.limit != 20 {
		t.Fatalf("unexpected statement: %#v", statement)
	}
	widePage, err := parseKitSQL(
		`SELECT * FROM products ORDER BY id LIMIT 300`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || widePage.limit != 300 {
		t.Fatalf("remote LIMIT 300 was not accepted: %#v, %v", widePage, err)
	}
	definition := bindStructDef("products", nil, map[string]*ColumnSpec{
		"id": {kind: "text", primary: true, seq: 1},
	})
	table := &SchemaTable{
		engine: "kitdb", table: definition.Name, columns: definition.columns, definition: definition,
	}
	if err := applyKitDBRemoteNarrowing(table, widePage); err != nil {
		t.Fatalf("apply remote LIMIT 300: %v", err)
	}
	pagePlan := table.builder().ExecutionPlan()
	if pagePlan.Limit != 300 || pagePlan.MaxLimit != kitDBRemoteSelectRowLimit {
		t.Fatalf("remote page bounds = limit %d max %d", pagePlan.Limit, pagePlan.MaxLimit)
	}
	if _, err := parseKitSQL(
		`SELECT * FROM products LIMIT 1001`,
		kitSQLBindings{named: map[string]value.Value{}},
	); err == nil || !strings.Contains(err.Error(), "remote result limit of 1000") {
		t.Fatalf("oversized remote LIMIT error = %v", err)
	}
	aggregate, err := parseKitSQL(
		`SELECT COUNT(price) AS priced, SUM(price) AS total, AVG(price) AS mean, MIN(sku), MAX(sku) FROM products WHERE status = :status`,
		kitSQLBindings{named: map[string]value.Value{"status": value.New("active")}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.kind != "select" || len(aggregate.projections) != 5 ||
		aggregate.projections[0].aggregate != "count" || aggregate.projections[0].field != "price" ||
		aggregate.projections[1].aggregate != "sum" || aggregate.projections[2].aggregate != "avg" ||
		aggregate.projections[3].aggregate != "min" || aggregate.projections[4].aggregate != "max" {
		t.Fatalf("unexpected aggregate statement: %#v", aggregate)
	}
	distinct, err := parseKitSQL(
		`SELECT DISTINCT status AS state, category FROM products ORDER BY state`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || !distinct.distinct || len(distinct.projections) != 2 ||
		distinct.projections[0].field != "status" || distinct.orders[0].column != "state" {
		t.Fatalf("unexpected DISTINCT statement: %#v, %v", distinct, err)
	}
	ordinary, err := parseKitSQL(
		`SELECT count, sum FROM metrics`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary.projections) != 2 || ordinary.projections[0].field != "count" ||
		ordinary.projections[0].aggregate != "" || ordinary.projections[1].field != "sum" ||
		ordinary.projections[1].aggregate != "" {
		t.Fatalf("function-like field names were not parsed as fields: %#v", ordinary)
	}
	explain, err := parseKitSQL(
		`EXPLAIN QUERY PLAN SELECT * FROM products WHERE sku = ?`,
		kitSQLBindings{positional: []value.Value{value.New("KIT-1")}, named: map[string]value.Value{}},
	)
	if err != nil || explain.kind != "explain" || explain.table != "products" {
		t.Fatalf("unexpected explain statement: %#v, %v", explain, err)
	}
	grouped, err := parseKitSQL(
		`SELECT * FROM products WHERE (status = 'active' OR price BETWEEN 10 AND 20) AND NOT title LIKE 'Draft%' LIMIT 5, 10`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if grouped.predicate == nil || grouped.predicate.Kind != query.PredicateAnd ||
		len(grouped.conditions) != 0 || grouped.offset != 5 || grouped.limit != 10 {
		t.Fatalf("unexpected grouped predicate: %#v", grouped)
	}
	joined, err := parseKitSQL(
		`SELECT p.id AS product_id, u.id AS user_id FROM products AS p
INNER JOIN users u ON p.owner_id = u.id WHERE p.status = 'active' ORDER BY u.id`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || joined.tableAlias != "p" || len(joined.joins) != 1 ||
		joined.joins[0].kind != "inner" || joined.joins[0].alias != "u" ||
		joined.projections[0].field != "p.id" || joined.orders[0].column != "u.id" {
		t.Fatalf("unexpected joined statement: %#v, %v", joined, err)
	}
	commented, err := parseKitSQL(
		"SELECT * FROM products /* manager probe */ WHERE sku = 'A' -- one statement\n",
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || commented.predicate == nil {
		t.Fatalf("SQL comments were not parsed safely: %#v, %v", commented, err)
	}
	multiInsert, err := parseKitSQL(
		`INSERT INTO products (sku, price) VALUES ('A', 10), ('B', 20), ('C', 30) RETURNING sku`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || len(multiInsert.insertRows) != 3 || len(multiInsert.insertCols) != 2 {
		t.Fatalf("unexpected multi-row INSERT: %#v, %v", multiInsert, err)
	}
	upsert, err := parseKitSQL(
		`INSERT INTO products (sku, price) VALUES ('A', 10)
ON CONFLICT (sku) DO UPDATE SET price = excluded.price, status = 'active' RETURNING sku`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || upsert.conflict == nil || upsert.conflict.action != "update" ||
		len(upsert.conflict.columns) != 1 || upsert.conflict.columns[0] != "sku" ||
		upsert.conflict.assignments["price"].excluded != "price" {
		t.Fatalf("unexpected UPSERT: %#v, %v", upsert, err)
	}
	compositeConflict, err := parseKitSQL(
		`INSERT INTO products (sku, title) VALUES ('A', 'Alpha') ON CONFLICT (title, sku) DO NOTHING`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || compositeConflict.conflict == nil ||
		!reflect.DeepEqual(compositeConflict.conflict.columns, []string{"title", "sku"}) {
		t.Fatalf("unexpected composite conflict target: %#v, %v", compositeConflict, err)
	}
	expressionUpdate, err := parseKitSQL(
		`UPDATE products SET price = price + 1, title = UPPER(title), note = NULL
WHERE price * 2 >= 40 RETURNING sku, price`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil || len(expressionUpdate.updateColumns) != 3 ||
		len(expressionUpdate.updateExpressions) != 2 || len(expressionUpdate.values) != 1 ||
		expressionUpdate.whereExpression == nil || len(expressionUpdate.returning) != 2 {
		t.Fatalf("unexpected expression UPDATE: %#v, %v", expressionUpdate, err)
	}

	for _, source := range []string{
		`SELECT * FROM products; DELETE FROM products`,
		"SELECT * FROM products -- comment\n; DELETE FROM products",
		`SELECT * FROM products /* harmless ; */; DELETE FROM products`,
		`UPDATE products SET price = 1`,
		`INSERT INTO products (sku) VALUES ('A') RETURNING SUM(price)`,
		`SELECT * FROM products WHERE sku IN ()`,
		`SELECT * FROM products WHERE (sku = 'A'`,
		`SELECT * FROM products WHERE price BETWEEN 10`,
		`SELECT * FROM products /* unterminated`,
		`SELECT * FROM products p JOIN users u ON p.id > u.id`,
		`SELECT * FROM products p JOIN users u ON p.id = u.id JOIN teams t ON u.id = t.id`,
		`SELECT * FROM products p RIGHT JOIN users u ON p.id = u.id`,
		`INSERT INTO products (sku) VALUES ('A') ON CONFLICT DO UPDATE SET sku = excluded.sku`,
		`INSERT INTO products (sku) VALUES ('A') ON CONFLICT (sku) DO UPDATE SET sku = excluded.sku + 'x'`,
	} {
		parsed, err := parseKitSQL(source, kitSQLBindings{named: map[string]value.Value{}})
		if err == nil && parsed.kind != "update" {
			t.Errorf("parseKitSQL(%q) unexpectedly succeeded: %#v", source, parsed)
		}
		if parsed.kind == "update" && len(parsed.conditions) != 0 {
			t.Errorf("unsafe update unexpectedly gained conditions: %#v", parsed)
		}
	}
}

func TestKitDBRemoteSQLConnectionProbe(t *testing.T) {
	statement, err := parseKitSQL(`SELECT 1 AS connected, ? AS label, true AS ready`, kitSQLBindings{
		positional: []value.Value{value.New("kitdb")},
		named:      map[string]value.Value{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if statement.kind != "select_scalar" || len(statement.scalars) != 3 {
		t.Fatalf("unexpected scalar statement: %#v", statement)
	}
	result := executeKitDBRemoteScalar(statement)
	if len(result.rows) != 1 || len(result.rows[0]) != 3 || result.columns[0].name != "connected" || result.rows[0][0].N != 1 {
		t.Fatalf("unexpected scalar result: %#v", result)
	}
	expressions, err := parseKitSQL(
		`SELECT 1 + 2 * 3 AS precedence,
COALESCE(NULL, 'kitdb') AS fallback,
CASE WHEN 2 > 1 THEN UPPER('go') ELSE 'bad' END AS branch,
LENGTH('Nguyễn') AS runes,
'kit' || 'db' AS joined,
10 / 0 AS divided_by_zero,
5 / 2 AS integer_division`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	expressionResult := executeKitDBRemoteScalar(expressions)
	row := expressionResult.rows[0]
	if row[0].N != 7 || row[1].String() != "kitdb" || row[2].String() != "GO" ||
		row[3].N != 6 || row[4].String() != "kitdb" || !row[5].IsNil() || row[6].N != 2 ||
		expressionResult.columns[6].kind != "integer" {
		t.Fatalf("unexpected scalar expression result: %#v", expressionResult)
	}
	floatDivision, err := parseKitSQL(`SELECT 5.0 / 2 AS quotient`, kitSQLBindings{
		named: map[string]value.Value{},
	})
	if err != nil {
		t.Fatal(err)
	}
	floatDivisionResult := executeKitDBRemoteScalar(floatDivision)
	if floatDivisionResult.rows[0][0].N != 2.5 || floatDivisionResult.columns[0].kind != "float" {
		t.Fatalf("float literal affinity = %#v", floatDivisionResult)
	}
	negative, err := parseKitSQL(`SELECT sku FROM products WHERE price = -1`, kitSQLBindings{
		named: map[string]value.Value{},
	})
	if err != nil || negative.whereExpression != nil || negative.predicate == nil ||
		len(negative.conditions) != 1 || negative.conditions[0].value.N != -1 {
		t.Fatalf("negative planner constant = %#v, %v", negative, err)
	}

	for _, source := range []string{
		`SELECT 1; DELETE FROM products`,
		`SELECT sqlite_version()`,
		`SELECT (SELECT 1)`,
		`SELECT COALESCE(1)`,
		`SELECT ROUND(1, 99)`,
	} {
		if _, err := parseKitSQL(source, kitSQLBindings{named: map[string]value.Value{}}); err == nil {
			t.Fatalf("parseKitSQL(%q) unexpectedly succeeded", source)
		}
	}
}

func TestKitDBRemoteSQLCoreExpressionsAndAtomicMultiInsert(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, internals } from "kitwork";
const { kitdb, struct, id, text, int } = internals;
const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  title: text().notNull(),
  price: int().default(0).index(),
  note: text()
});
const db = kitdb("sql-core.kitdb", { products }, { token: "sql-core-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	_, config, found := resolveServe(tenant, "sql-core.kitdb")
	if !found || config.database == nil {
		t.Fatal("KitDB SQL core serve registration is unavailable")
	}
	execute := func(source string) (kitDBRemoteResult, error) {
		t.Helper()
		return executeKitDBRemoteSQL(
			context.Background(), nil, config.database, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
	}
	columnStrings := func(result kitDBRemoteResult, column int) []string {
		t.Helper()
		items := make([]string, 0, len(result.rows))
		for _, row := range result.rows {
			if column >= len(row) {
				t.Fatalf("result row has %d columns, need %d: %#v", len(row), column+1, result)
			}
			items = append(items, row[column].String())
		}
		return items
	}
	lastTransaction := func() uint64 {
		t.Helper()
		managed, err := kitDBForRequest(tenant, "sql-core.kitdb", nil).database()
		if err != nil {
			t.Fatal(err)
		}
		defer managed.Release()
		last, err := managed.database.LastTransaction()
		if err != nil {
			t.Fatal(err)
		}
		return last
	}

	if _, err := execute(`SELECT COUNT(*) AS total FROM products`); err != nil {
		t.Fatal(err)
	}
	beforeMultiInsert := lastTransaction()
	inserted, err := execute(`INSERT INTO products (sku, title, price, note) VALUES
  ('A', 'Alpha shirt', 10, NULL),
  ('B', 'Beta shirt', 20, 'featured'),
  ('C', 'Gamma coat', 30, NULL)
RETURNING sku, price`)
	if err != nil {
		t.Fatal(err)
	}
	if inserted.affected != 3 || !reflect.DeepEqual(columnStrings(inserted, 0), []string{"A", "B", "C"}) {
		t.Fatalf("multi-row insert = %#v", inserted)
	}
	if after := lastTransaction(); after != beforeMultiInsert+1 {
		t.Fatalf("multi-row INSERT advanced transaction %d -> %d, want one commit", beforeMultiInsert, after)
	}
	insertedAllFields, err := execute(
		`INSERT INTO products VALUES ('manual-id', 'D', 'Delta coat', 40, NULL) RETURNING id, sku`,
	)
	if err != nil || insertedAllFields.affected != 1 || insertedAllFields.rows[0][1].String() != "D" {
		t.Fatalf("positional all-field insert = %#v, %v", insertedAllFields, err)
	}
	aliasedOrder, err := execute(
		`SELECT sku AS code, price AS cost FROM products ORDER BY cost DESC LIMIT 2`,
	)
	if err != nil || !reflect.DeepEqual(columnStrings(aliasedOrder, 0), []string{"D", "C"}) {
		t.Fatalf("projection-alias ORDER BY = %#v, %v", aliasedOrder, err)
	}
	aliasedAggregate, err := execute(`SELECT COUNT(*) AS total FROM products ORDER BY total DESC`)
	if err != nil || len(aliasedAggregate.rows) != 1 || aliasedAggregate.rows[0][0].N != 4 {
		t.Fatalf("aggregate-alias ORDER BY = %#v, %v", aliasedAggregate, err)
	}
	if _, err := execute(`SELECT sku AS duplicate, price AS duplicate FROM products ORDER BY duplicate`); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous ORDER BY alias error = %v", err)
	}

	precedence, err := execute(
		`SELECT sku FROM products WHERE sku = 'A' OR sku = 'B' AND price = 20 ORDER BY sku`,
	)
	if err != nil || !reflect.DeepEqual(columnStrings(precedence, 0), []string{"A", "B"}) {
		t.Fatalf("AND/OR precedence = %#v, %v", precedence, err)
	}
	grouped, err := execute(
		`SELECT sku FROM products WHERE (sku = 'A' OR sku = 'B') AND price = 20 ORDER BY sku`,
	)
	if err != nil || !reflect.DeepEqual(columnStrings(grouped, 0), []string{"B"}) {
		t.Fatalf("grouped predicate = %#v, %v", grouped, err)
	}
	between, err := execute(
		`SELECT sku FROM products WHERE NOT (price BETWEEN 15 AND 35) AND note IS NULL ORDER BY sku LIMIT 0, 10`,
	)
	if err != nil || !reflect.DeepEqual(columnStrings(between, 0), []string{"A", "D"}) {
		t.Fatalf("NOT/BETWEEN/IS NULL = %#v, %v", between, err)
	}
	like, err := execute(
		`SELECT sku FROM products WHERE title LIKE '%shirt' AND sku IN ('A', 'B', 'missing') ORDER BY sku`,
	)
	if err != nil || !reflect.DeepEqual(columnStrings(like, 0), []string{"A", "B"}) {
		t.Fatalf("LIKE/IN predicate = %#v, %v", like, err)
	}
	nullAwareNotIn, err := execute(`SELECT sku FROM products WHERE sku NOT IN ('A', NULL) ORDER BY sku`)
	if err != nil || len(nullAwareNotIn.rows) != 0 {
		t.Fatalf("NULL-aware NOT IN = %#v, %v", nullAwareNotIn, err)
	}

	oracle, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer oracle.Close()
	if _, err := oracle.Exec(`CREATE TABLE products (
  id TEXT PRIMARY KEY, sku TEXT NOT NULL UNIQUE, title TEXT NOT NULL,
  price INTEGER DEFAULT 0, note TEXT
);
INSERT INTO products VALUES
  ('id-a', 'A', 'Alpha shirt', 10, NULL),
  ('id-b', 'B', 'Beta shirt', 20, 'featured'),
  ('id-c', 'C', 'Gamma coat', 30, NULL),
  ('manual-id', 'D', 'Delta coat', 40, NULL);`); err != nil {
		t.Fatal(err)
	}
	expressionRows, err := execute(`SELECT sku,
  price * 2 AS doubled,
  COALESCE(note, 'none') AS note_text,
  CASE WHEN price >= 30 THEN UPPER('high') ELSE LOWER('LOW') END AS band
FROM products
ORDER BY doubled DESC, LOWER(sku) ASC
LIMIT 3`)
	if err != nil {
		t.Fatal(err)
	}
	if got := columnStrings(expressionRows, 0); !reflect.DeepEqual(got, []string{"D", "C", "B"}) ||
		expressionRows.rows[0][1].N != 80 || expressionRows.rows[0][2].String() != "none" ||
		expressionRows.rows[0][3].String() != "HIGH" || expressionRows.rows[2][2].String() != "featured" ||
		expressionRows.rows[2][3].String() != "low" {
		t.Fatalf("row expression projection = %#v", expressionRows)
	}
	distinctExpression, err := execute(`SELECT DISTINCT
  CASE WHEN price >= 30 THEN 'high' ELSE 'low' END AS band
FROM products ORDER BY band`)
	if err != nil || !reflect.DeepEqual(columnStrings(distinctExpression, 0), []string{"high", "low"}) {
		t.Fatalf("DISTINCT expression = %#v, %v", distinctExpression, err)
	}
	expressionExplain, err := execute(
		`EXPLAIN QUERY PLAN SELECT price * 2 AS doubled FROM products ORDER BY price LIMIT 2`,
	)
	if err != nil || len(expressionExplain.rows) != 1 ||
		!strings.Contains(expressionExplain.rows[0][3].String(), "PROJECT EXPRESSIONS") ||
		!strings.Contains(expressionExplain.rows[0][3].String(), "INDEX ORDER") {
		t.Fatalf("expression EXPLAIN = %#v, %v", expressionExplain, err)
	}
	if _, err := execute(`SELECT sku + 1 FROM products`); err == nil || !strings.Contains(err.Error(), "numeric operands") {
		t.Fatalf("typed expression error = %v", err)
	}
	filteredExpression, err := execute(`SELECT *, price * 2 AS doubled
FROM products
WHERE price * 2 >= 40 AND sku != 'C'
ORDER BY doubled DESC`)
	if err != nil || !reflect.DeepEqual(columnStrings(filteredExpression, 1), []string{"D", "B"}) ||
		filteredExpression.rows[0][5].N != 80 || filteredExpression.rows[1][5].N != 40 {
		t.Fatalf("WHERE expression with star projection = %#v, %v", filteredExpression, err)
	}
	filterExplain, err := execute(`EXPLAIN QUERY PLAN
SELECT price * 2 AS doubled FROM products
WHERE sku = 'B' AND price * 2 >= 40`)
	if err != nil || len(filterExplain.rows) != 1 ||
		!strings.Contains(filterExplain.rows[0][3].String(), "KITDB UNIQUE") ||
		!strings.Contains(filterExplain.rows[0][3].String(), "FILTER EXPRESSION") {
		t.Fatalf("residual filter EXPLAIN = %#v, %v", filterExplain, err)
	}
	beforeExpressionUpdate := lastTransaction()
	expressionUpdated, err := execute(`UPDATE products
SET price = price + 5, title = UPPER(title), note = title
WHERE sku = 'D' AND LENGTH(sku) = 1 AND price * 2 > 70
RETURNING sku, price, title, note`)
	if err != nil || expressionUpdated.affected != 1 || len(expressionUpdated.rows) != 1 ||
		expressionUpdated.rows[0][0].String() != "D" || expressionUpdated.rows[0][1].N != 45 ||
		expressionUpdated.rows[0][2].String() != "DELTA COAT" ||
		expressionUpdated.rows[0][3].String() != "Delta coat" {
		t.Fatalf("expression UPDATE = %#v, %v", expressionUpdated, err)
	}
	if after := lastTransaction(); after != beforeExpressionUpdate+1 {
		t.Fatalf("expression UPDATE advanced transaction %d -> %d, want one commit", beforeExpressionUpdate, after)
	}
	if _, err := oracle.Exec(`UPDATE products
SET price = price + 5, title = UPPER(title), note = title
WHERE sku = 'D' AND LENGTH(sku) = 1 AND price * 2 > 70`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(
		`INSERT INTO products (sku, title, price) VALUES ('TEMP', 'Temporary', 5)`,
	); err != nil {
		t.Fatal(err)
	}
	expressionDeleted, err := execute(`DELETE FROM products
WHERE LENGTH(sku) > 1 AND price + 1 = 6 RETURNING sku, title`)
	if err != nil || expressionDeleted.affected != 1 || len(expressionDeleted.rows) != 1 ||
		expressionDeleted.rows[0][0].String() != "TEMP" ||
		expressionDeleted.rows[0][1].String() != "Temporary" {
		t.Fatalf("expression DELETE = %#v, %v", expressionDeleted, err)
	}

	beforeFailedUpdate := lastTransaction()
	if _, err := execute(`UPDATE products
SET sku = CASE WHEN sku = 'A' THEN 'duplicate' ELSE 'duplicate' END
WHERE price <= 20`); err == nil || !strings.Contains(err.Error(), "duplicate unique values") {
		t.Fatalf("atomic expression UPDATE error = %v", err)
	}
	if after := lastTransaction(); after != beforeFailedUpdate {
		t.Fatalf("failed expression UPDATE advanced transaction %d -> %d", beforeFailedUpdate, after)
	}
	unchanged, err := execute(`SELECT sku FROM products WHERE sku IN ('A', 'B') ORDER BY sku`)
	if err != nil || !reflect.DeepEqual(columnStrings(unchanged, 0), []string{"A", "B"}) {
		t.Fatalf("failed expression UPDATE leaked changes: %#v, %v", unchanged, err)
	}
	if _, err := execute(
		`UPDATE products SET price = price + 1 WHERE sku = 'A' RETURNING missing`,
	); err == nil || !strings.Contains(err.Error(), "no such column") {
		t.Fatalf("invalid UPDATE RETURNING error = %v", err)
	}
	if after := lastTransaction(); after != beforeFailedUpdate {
		t.Fatalf("invalid UPDATE RETURNING advanced transaction %d -> %d", beforeFailedUpdate, after)
	}
	for _, source := range []string{
		`SELECT sku FROM products WHERE sku = 'A' OR sku = 'B' AND price = 20 ORDER BY sku`,
		`SELECT sku FROM products WHERE (sku = 'A' OR sku = 'B') AND price = 20 ORDER BY sku`,
		`SELECT sku FROM products WHERE NOT (price BETWEEN 15 AND 35) AND note IS NULL ORDER BY sku`,
		`SELECT sku FROM products WHERE sku NOT IN ('A', NULL) ORDER BY sku`,
		`SELECT sku FROM products WHERE note IS NOT NULL OR price >= 30 ORDER BY sku`,
		`SELECT sku FROM products WHERE note = NULL OR sku = 'B' ORDER BY sku`,
		`SELECT sku FROM products WHERE NOT (note = NULL) ORDER BY sku`,
	} {
		actual, err := execute(source)
		if err != nil {
			t.Fatalf("KitDB differential query %q: %v", source, err)
		}
		rows, err := oracle.Query(source)
		if err != nil {
			t.Fatalf("SQLite differential query %q: %v", source, err)
		}
		expected := make([]string, 0)
		for rows.Next() {
			var sku string
			if err := rows.Scan(&sku); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			expected = append(expected, sku)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if got := columnStrings(actual, 0); !reflect.DeepEqual(got, expected) {
			t.Fatalf("differential query %q: KitDB=%v SQLite=%v", source, got, expected)
		}
	}
	ignored, err := execute(
		`INSERT INTO products (sku, title, price) VALUES ('A', 'Ignored', 999)
ON CONFLICT DO NOTHING RETURNING sku`,
	)
	if err != nil || ignored.affected != 0 || len(ignored.rows) != 0 {
		t.Fatalf("ON CONFLICT DO NOTHING = %#v, %v", ignored, err)
	}
	beforeUpsert := lastTransaction()
	upserted, err := execute(
		`INSERT INTO products (sku, title, price) VALUES ('B', 'Beta updated', 25)
ON CONFLICT (sku) DO UPDATE SET title = excluded.title, price = excluded.price
RETURNING sku, title, price`,
	)
	if err != nil || upserted.affected != 1 || len(upserted.rows) != 1 ||
		upserted.rows[0][0].String() != "B" || upserted.rows[0][1].String() != "Beta updated" ||
		upserted.rows[0][2].N != 25 {
		t.Fatalf("ON CONFLICT DO UPDATE = %#v, %v", upserted, err)
	}
	if after := lastTransaction(); after != beforeUpsert+1 {
		t.Fatalf("UPSERT advanced transaction %d -> %d, want one commit", beforeUpsert, after)
	}
	if _, err := execute(
		`INSERT INTO products (sku, title) VALUES ('new-sku', 'Bad target')
ON CONFLICT (title) DO NOTHING`,
	); err == nil || !strings.Contains(err.Error(), "not primary or unique") {
		t.Fatalf("non-unique conflict target error = %v", err)
	}

	updated, err := execute(
		`UPDATE products SET title = 'selected' WHERE sku = 'A' OR sku = 'C' RETURNING sku, title`,
	)
	updatedSKUs := columnStrings(updated, 0)
	sort.Strings(updatedSKUs)
	if err != nil || updated.affected != 2 ||
		!reflect.DeepEqual(updatedSKUs, []string{"A", "C"}) {
		t.Fatalf("grouped UPDATE = %#v, %v", updated, err)
	}
	deleted, err := execute(
		`DELETE FROM products WHERE NOT (price BETWEEN 15 AND 35) RETURNING sku`,
	)
	deletedSKUs := columnStrings(deleted, 0)
	sort.Strings(deletedSKUs)
	if err != nil || deleted.affected != 2 ||
		!reflect.DeepEqual(deletedSKUs, []string{"A", "D"}) {
		t.Fatalf("grouped DELETE = %#v, %v", deleted, err)
	}

	beforeFailedInsert := lastTransaction()
	if _, err := execute(
		`INSERT INTO products (sku, title) VALUES ('E', 'Would leak'), ('B', 'Duplicate')`,
	); err == nil || !strings.Contains(err.Error(), "row 2") {
		t.Fatalf("multi-row constraint error = %v", err)
	}
	if after := lastTransaction(); after != beforeFailedInsert {
		t.Fatalf("failed multi-row INSERT advanced transaction %d -> %d", beforeFailedInsert, after)
	}
	rolledBack, err := execute(`SELECT COUNT(*) AS total FROM products WHERE sku = 'E'`)
	if err != nil || len(rolledBack.rows) != 1 || rolledBack.rows[0][0].N != 0 {
		t.Fatalf("failed multi-row INSERT leaked data: %#v, %v", rolledBack, err)
	}
}

func TestKitDBRemoteSQLCreateTableParser(t *testing.T) {
	statement, err := parseKitSQL(`CREATE TABLE IF NOT EXISTS products (
  id KITID PRIMARY KEY,
  sku VARCHAR(64) NOT NULL UNIQUE,
  price DECIMAL(10, 2) DEFAULT 0,
  status CHOICE('active', 'disabled') DEFAULT 'active',
  enabled BOOLEAN DEFAULT true,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  owner_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
  PRIMARY KEY (id)
) STRICT`, kitSQLBindings{named: map[string]value.Value{}})
	if err == nil {
		t.Fatal("duplicate inline and table PRIMARY KEY unexpectedly succeeded")
	}

	statement, err = parseKitSQL(`CREATE TABLE IF NOT EXISTS products (
  id KITID,
  sku VARCHAR(64) NOT NULL UNIQUE,
  price DECIMAL(10, 2) DEFAULT 0,
  status CHOICE('active', 'disabled') DEFAULT 'active',
  enabled BOOLEAN DEFAULT true,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  owner_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
  PRIMARY KEY (id)
) STRICT`, kitSQLBindings{named: map[string]value.Value{}})
	if err != nil {
		t.Fatal(err)
	}
	if statement.kind != "create_table" || statement.table != "products" || statement.createTable == nil || !statement.createTable.ifNotExists {
		t.Fatalf("unexpected CREATE TABLE statement: %#v", statement)
	}
	if len(statement.createTable.columns) != 7 {
		t.Fatalf("columns = %d, want 7", len(statement.createTable.columns))
	}
	columns := map[string]*ColumnSpec{}
	for _, column := range statement.createTable.columns {
		columns[column.name] = column.spec
	}
	if !columns["id"].primary || columns["id"].kind != "kitid" {
		t.Fatalf("unexpected id column: %#v", columns["id"])
	}
	if !columns["sku"].notNull || !columns["sku"].unique || columns["sku"].kind != "varchar" {
		t.Fatalf("unexpected sku column: %#v", columns["sku"])
	}
	if !columns["price"].hasDefault || columns["price"].def.N != 0 || columns["price"].kind != "decimal" {
		t.Fatalf("unexpected price column: %#v", columns["price"])
	}
	if len(columns["status"].enumVals) != 2 || columns["status"].def.String() != "active" {
		t.Fatalf("unexpected status column: %#v", columns["status"])
	}
	if !columns["created_at"].defaultNow || columns["created_at"].kind != "datetime" ||
		columns["owner_id"].fk == nil || columns["owner_id"].fk.table != "users" {
		t.Fatalf("unexpected timestamp/reference columns: %#v %#v", columns["created_at"], columns["owner_id"])
	}
	indexStatement, err := parseKitSQL(
		`CREATE INDEX IF NOT EXISTS products_status_price ON products (status, price ASC)`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if indexStatement.kind != "create_index" || indexStatement.table != "products" ||
		indexStatement.createIndex == nil || !indexStatement.createIndex.ifNotExists ||
		indexStatement.createIndex.name != "products_status_price" ||
		!reflect.DeepEqual(indexStatement.createIndex.columns, []string{"status", "price"}) {
		t.Fatalf("unexpected CREATE INDEX statement: %#v", indexStatement)
	}
	dropIndexStatement, err := parseKitSQL(
		`DROP INDEX IF EXISTS "public"."products_status_price" CASCADE`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if dropIndexStatement.kind != "drop_index" || dropIndexStatement.dropIndex == nil ||
		!dropIndexStatement.dropIndex.ifExists || !dropIndexStatement.dropIndex.cascade ||
		dropIndexStatement.dropIndex.name != "products_status_price" {
		t.Fatalf("unexpected DROP INDEX statement: %#v", dropIndexStatement)
	}
	if _, err := parseKitSQL(
		`DROP INDEX CONCURRENTLY products_status_price`,
		kitSQLBindings{named: map[string]value.Value{}},
	); err == nil || !strings.Contains(err.Error(), "already online and resumable") {
		t.Fatalf("DROP INDEX CONCURRENTLY error = %v", err)
	}
	alterStatement, err := parseKitSQL(
		`ALTER TABLE products ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT ''`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if alterStatement.kind != "alter_add_column" || alterStatement.table != "products" ||
		alterStatement.alterColumn == nil || !alterStatement.alterColumn.ifNotExists ||
		alterStatement.alterColumn.column.name != "description" ||
		!alterStatement.alterColumn.column.spec.notNull ||
		alterStatement.alterColumn.column.spec.def.String() != "" {
		t.Fatalf("unexpected ALTER TABLE statement: %#v", alterStatement)
	}
	renameStatement, err := parseKitSQL(
		`ALTER TABLE products RENAME COLUMN sku TO code`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if renameStatement.kind != "alter_rename_column" || renameStatement.alterColumn == nil ||
		renameStatement.alterColumn.name != "sku" || renameStatement.alterColumn.newName != "code" {
		t.Fatalf("unexpected RENAME COLUMN statement: %#v", renameStatement)
	}
	renameTableStatement, err := parseKitSQL(
		`ALTER TABLE "public"."products" RENAME TO archived_products`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if renameTableStatement.kind != "alter_rename_table" || renameTableStatement.table != "products" ||
		renameTableStatement.renameTable == nil ||
		renameTableStatement.renameTable.newName != "archived_products" {
		t.Fatalf("unexpected RENAME TABLE statement: %#v", renameTableStatement)
	}
	renameIndexStatement, err := parseKitSQL(
		`ALTER INDEX IF EXISTS "public"."products_status_idx" RENAME TO archived_products_status_idx`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if renameIndexStatement.kind != "alter_rename_index" || renameIndexStatement.renameIndex == nil ||
		!renameIndexStatement.renameIndex.ifExists ||
		renameIndexStatement.renameIndex.name != "products_status_idx" ||
		renameIndexStatement.renameIndex.newName != "archived_products_status_idx" {
		t.Fatalf("unexpected RENAME INDEX statement: %#v", renameIndexStatement)
	}
	dropStatement, err := parseKitSQL(
		`ALTER TABLE products DROP COLUMN IF EXISTS status`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if dropStatement.kind != "alter_drop_column" || dropStatement.alterColumn == nil ||
		!dropStatement.alterColumn.ifExists || dropStatement.alterColumn.name != "status" {
		t.Fatalf("unexpected DROP COLUMN statement: %#v", dropStatement)
	}
	dropConstraintStatement, err := parseKitSQL(
		`ALTER TABLE "public"."products" DROP CONSTRAINT IF EXISTS "products_status_check" CASCADE`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if dropConstraintStatement.kind != "alter_drop_constraint" ||
		dropConstraintStatement.table != "products" || dropConstraintStatement.alterConstraint == nil ||
		!dropConstraintStatement.alterConstraint.ifExists || !dropConstraintStatement.alterConstraint.cascade ||
		dropConstraintStatement.alterConstraint.name != "products_status_check" {
		t.Fatalf("unexpected DROP CONSTRAINT statement: %#v", dropConstraintStatement)
	}
	addUniqueStatement, err := parseKitSQL(
		`ALTER TABLE products ADD CONSTRAINT products_sku_key UNIQUE (sku)`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if addUniqueStatement.kind != "alter_add_constraint" || addUniqueStatement.alterConstraint == nil ||
		addUniqueStatement.alterConstraint.action != "add" ||
		addUniqueStatement.alterConstraint.kind != "unique" ||
		addUniqueStatement.alterConstraint.name != "products_sku_key" ||
		!reflect.DeepEqual(addUniqueStatement.alterConstraint.columns, []string{"sku"}) {
		t.Fatalf("unexpected ADD UNIQUE statement: %#v", addUniqueStatement)
	}
	addForeignStatement, err := parseKitSQL(
		`ALTER TABLE order_items ADD CONSTRAINT order_items_product_fkey `+
			`FOREIGN KEY (tenant, product_code) REFERENCES products (tenant, code) `+
			`ON DELETE CASCADE ON UPDATE RESTRICT`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if addForeignStatement.kind != "alter_add_constraint" || addForeignStatement.alterConstraint == nil ||
		addForeignStatement.alterConstraint.kind != "foreign" ||
		addForeignStatement.alterConstraint.reference == nil ||
		addForeignStatement.alterConstraint.reference.table != "products" ||
		!reflect.DeepEqual(addForeignStatement.alterConstraint.columns, []string{"tenant", "product_code"}) ||
		!reflect.DeepEqual(addForeignStatement.alterConstraint.reference.fields, []string{"tenant", "code"}) ||
		addForeignStatement.alterConstraint.reference.onDelete != "cascade" ||
		addForeignStatement.alterConstraint.reference.onUpdate != "restrict" {
		t.Fatalf("unexpected ADD FOREIGN KEY statement: %#v", addForeignStatement)
	}
	addCheckStatement, err := parseKitSQL(
		`ALTER TABLE products ADD CONSTRAINT products_price_check CHECK (price >= 0)`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if addCheckStatement.kind != "alter_add_constraint" || addCheckStatement.alterConstraint == nil ||
		addCheckStatement.alterConstraint.kind != "check" ||
		addCheckStatement.alterConstraint.check == nil ||
		addCheckStatement.alterConstraint.check.name != "products_price_check" {
		t.Fatalf("unexpected ADD CHECK statement: %#v", addCheckStatement)
	}
	if _, err := parseKitSQL(
		`ALTER TABLE products ADD CONSTRAINT products_new_pkey PRIMARY KEY (sku)`,
		kitSQLBindings{named: map[string]value.Value{}},
	); err == nil || !strings.Contains(err.Error(), "adding primary key") {
		t.Fatalf("ADD PRIMARY KEY error = %v", err)
	}
	typeStatement, err := parseKitSQL(
		`ALTER TABLE products ALTER COLUMN price TYPE DECIMAL(12, 2)`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if typeStatement.kind != "alter_column_type" || typeStatement.alterColumn == nil ||
		typeStatement.alterColumn.name != "price" || typeStatement.alterColumn.kind != "decimal" {
		t.Fatalf("unexpected ALTER COLUMN TYPE statement: %#v", typeStatement)
	}
	cancelStatement, err := parseKitSQL(
		`ALTER TABLE products CANCEL MIGRATION`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cancelStatement.kind != "alter_cancel_migration" || cancelStatement.table != "products" {
		t.Fatalf("unexpected CANCEL MIGRATION statement: %#v", cancelStatement)
	}
	dropTableStatement, err := parseKitSQL(
		`DROP TABLE IF EXISTS "public"."NhanVien" RESTRICT`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if dropTableStatement.kind != "drop_table" || dropTableStatement.table != "NhanVien" ||
		dropTableStatement.dropTable == nil || !dropTableStatement.dropTable.ifExists {
		t.Fatalf("unexpected DROP TABLE statement: %#v", dropTableStatement)
	}
	cascadeDropStatement, err := parseKitSQL(
		`DROP TABLE "public"."NhanVien" CASCADE`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cascadeDropStatement.dropTable == nil || !cascadeDropStatement.dropTable.cascade {
		t.Fatalf("unexpected CASCADE DROP TABLE statement: %#v", cascadeDropStatement)
	}
	statusStatement, err := parseKitSQL(
		`PRAGMA migration_status('products')`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if statusStatement.kind != "pragma" || statusStatement.pragma != "migration_status" ||
		statusStatement.table != "products" {
		t.Fatalf("unexpected migration_status statement: %#v", statusStatement)
	}
	indexStatusStatement, err := parseKitSQL(
		`PRAGMA index_status(products)`,
		kitSQLBindings{named: map[string]value.Value{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if indexStatusStatement.kind != "pragma" || indexStatusStatement.pragma != "index_status" ||
		indexStatusStatement.table != "products" {
		t.Fatalf("unexpected index_status statement: %#v", indexStatusStatement)
	}

	for _, source := range []string{
		`CREATE UNIQUE INDEX idx_products_sku ON products (sku)`,
		`CREATE INDEX idx_products_sku ON products (sku DESC)`,
		`CREATE INDEX idx_products_sku ON products (sku) WHERE status = 'active'`,
		`ALTER TABLE products ALTER COLUMN price SET DEFAULT 0`,
		`CREATE TABLE t (id KITID PRIMARY KEY, ID TEXT)`,
		`CREATE TABLE t (id KITID PRIMARY KEY, payload MYSTERY)`,
		`CREATE TABLE t (id KITID PRIMARY KEY,)`,
		`CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
	} {
		if _, err := parseKitSQL(source, kitSQLBindings{named: map[string]value.Value{}}); err == nil {
			t.Fatalf("parseKitSQL(%q) unexpectedly succeeded", source)
		}
	}
}

func FuzzKitDBRemoteSQLParser(f *testing.F) {
	for _, source := range []string{
		`SELECT * FROM products WHERE sku = ? LIMIT 20`,
		`INSERT INTO products (sku, price) VALUES ('KIT-1', 10) RETURNING *`,
		`UPDATE products SET price = :price WHERE sku = :sku`,
		`DELETE FROM products WHERE id IN (?, ?)`,
		`CREATE TABLE products (id KITID PRIMARY KEY, title TEXT NOT NULL)`,
		`CREATE INDEX products_title ON products (title)`,
		`ALTER TABLE products ADD COLUMN description TEXT DEFAULT ''`,
		`ALTER TABLE products RENAME COLUMN title TO name`,
		`ALTER TABLE products DROP COLUMN description`,
		`ALTER TABLE products ALTER COLUMN price TYPE INTEGER`,
		`PRAGMA table_info("products")`,
		`SELECT * FROM products; DROP TABLE products`,
		"SELECT '\xff' FROM products",
	} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > kitDBRemoteSQLBytes+1 {
			return
		}
		bindings := kitSQLBindings{
			positional: []value.Value{value.New("one"), value.New("two")},
			named: map[string]value.Value{
				"price": value.New(10), "sku": value.New("KIT-1"),
			},
		}
		_, _ = parseKitSQL(source, bindings)
	})
}

func TestKitDBRemoteCreateTablePersistsAndPublishesToORM(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, internals } from "kitwork";
const { kitdb } = internals;
const db = kitdb("dynamic.kitdb", {}, { token: "dynamic-secret", access: "readwrite" });
router.get(() => db.products.count());`
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
	// execute runs one statement through the remote SQL engine, the one the PostgreSQL wire runs, on the
	// database the tenant declared served.
	execute := func(tenant *Tenant, sql string) (string, error) {
		t.Helper()
		_, config, found := resolveServe(tenant, "dynamic.kitdb")
		if !found || config.database == nil {
			t.Fatal("dynamic.kitdb is not declared served")
		}
		result, err := executeKitDBRemoteSQL(
			context.Background(), nil, config.database, sql,
			kitSQLBindings{named: map[string]value.Value{}}, config.access != "readwrite",
		)
		if err != nil {
			return "", err
		}
		encoded, err := json.Marshal(kitDBRemoteResultMaps(result))
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded), nil
	}
	succeeds := func(tenant *Tenant, sql string, contains ...string) {
		t.Helper()
		rows, err := execute(tenant, sql)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		for _, expected := range contains {
			if !strings.Contains(rows, expected) {
				t.Fatalf("%s: rows = %s, want %s", sql, rows, expected)
			}
		}
	}
	fails := func(tenant *Tenant, sql, contains string) {
		t.Helper()
		if _, err := execute(tenant, sql); err == nil || !strings.Contains(err.Error(), contains) {
			t.Fatalf("%s: error = %v, want %q", sql, err, contains)
		}
	}
	count := func(tenant *Tenant) string {
		t.Helper()
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("ORM count status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		return strings.TrimSpace(recorder.Body.String())
	}

	tenant := start()
	defer func(initialTenant *Tenant) { initialTenant.Close() }(tenant)
	succeeds(tenant, `CREATE TABLE users (
  id KITID PRIMARY KEY,
  email TEXT NOT NULL UNIQUE
)`)
	succeeds(tenant, `CREATE TABLE products (
  id KITID PRIMARY KEY,
  sku TEXT NOT NULL UNIQUE,
  price INTEGER DEFAULT 0,
  status CHOICE('active', 'disabled') DEFAULT 'active',
  owner_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
)`)

	succeeds(tenant, `INSERT INTO users (id, email) VALUES ('user_1', 'owner@example.com') RETURNING *`, `"user_1"`, `"owner@example.com"`)
	succeeds(tenant, `INSERT INTO products (sku, owner_id) VALUES ('KIT-1', 'user_1') RETURNING id, sku, price, status, created_at`, `"KIT-1"`, `"active"`, `"price":0`)
	succeeds(tenant, `CREATE INDEX products_status_price ON products (status, price)`)
	fails(tenant, `CREATE INDEX products_status_price ON products (status, price)`, "already exists on table")
	succeeds(tenant, `CREATE INDEX IF NOT EXISTS products_status_price ON products (status, price)`)
	succeeds(tenant, `ALTER TABLE products ADD COLUMN description TEXT NOT NULL DEFAULT ''`)
	succeeds(tenant, `SELECT sku, description FROM products WHERE sku = 'KIT-1'`, `"description":""`)
	fails(tenant, `ALTER TABLE products ADD COLUMN required_note TEXT NOT NULL`, "migration refused")
	fails(tenant, `ALTER TABLE products ADD COLUMN description TEXT`, "already has column")
	succeeds(tenant, `ALTER TABLE products ADD COLUMN IF NOT EXISTS description TEXT`)

	fails(tenant, `INSERT INTO products (sku, owner_id) VALUES ('KIT-BAD', 'missing')`, "references missing users.id")
	fails(tenant, `INSERT INTO products (sku, price, owner_id) VALUES ('KIT-TYPE', 'not-an-integer', 'user_1')`, "expects an integer")
	fails(tenant, `CREATE TABLE products (id KITID PRIMARY KEY)`, "already exists")
	succeeds(tenant, `CREATE TABLE IF NOT EXISTS products (id KITID PRIMARY KEY)`)
	succeeds(tenant, `SELECT name, type FROM sqlite_master WHERE type = 'table' ORDER BY name`, `"products"`, `"users"`)
	succeeds(tenant, `PRAGMA index_list(products)`, `"products_status_price"`)
	succeeds(tenant, `PRAGMA table_info(products)`, `"owner_id"`, `"created_at"`)

	if got := count(tenant); got != "1" {
		t.Fatalf("live ORM count = %s", got)
	}
	tenant.Close()

	tenant = start()
	defer tenant.Close()
	succeeds(tenant, `SELECT sku, price, status, owner_id, description FROM products WHERE sku = 'KIT-1'`, `"KIT-1"`, `"active"`, `"user_1"`, `"description"`)
	succeeds(tenant, `PRAGMA index_list(products)`, `"products_status_price"`)
	if got := count(tenant); got != "1" {
		t.Fatalf("reopened ORM count = %s", got)
	}
}

func TestKitDBRemoteCreateTableConcurrentPublication(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, internals } from "kitwork";
const db = internals.kitdb("concurrent.kitdb", {}, { token: "concurrent-secret", access: "readwrite" });
router.get(() => db.items.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	_, config, found := resolveServe(tenant, "concurrent.kitdb")
	if !found || config.database == nil {
		t.Fatal("concurrent KitDB serve registration is unavailable")
	}

	const workers = 24
	start := make(chan struct{})
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			var source string
			if worker%2 == 0 {
				source = `CREATE TABLE IF NOT EXISTS items (id KITID PRIMARY KEY, name TEXT NOT NULL)`
			} else {
				source = `SELECT COUNT(*) AS total FROM sqlite_master WHERE type = 'table'`
			}
			_, err := executeKitDBRemoteSQL(
				context.Background(), nil, config.database, source,
				kitSQLBindings{named: map[string]value.Value{}}, false,
			)
			if err != nil {
				errors <- err
			}
		}(worker)
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Errorf("concurrent DDL/catalog operation: %v", err)
	}
	if t.Failed() {
		return
	}

	result, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`SELECT COUNT(*) AS total FROM sqlite_master WHERE type = 'table' AND name = 'items'`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.rows) != 1 || len(result.rows[0]) != 1 || result.rows[0][0].N != 1 {
		t.Fatalf("catalog item count = %#v, want 1", result.rows)
	}

	startIndexes := make(chan struct{})
	indexErrors := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-startIndexes
			name := "items_name_a"
			if worker%2 != 0 {
				name = "items_name_b"
			}
			_, err := executeKitDBRemoteSQL(
				context.Background(), nil, config.database,
				`CREATE INDEX IF NOT EXISTS `+name+` ON items (name)`,
				kitSQLBindings{named: map[string]value.Value{}}, false,
			)
			if err != nil {
				indexErrors <- err
			}
		}(worker)
	}
	close(startIndexes)
	wait.Wait()
	close(indexErrors)
	for err := range indexErrors {
		t.Errorf("concurrent CREATE INDEX: %v", err)
	}
	if t.Failed() {
		return
	}
	indexList, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `PRAGMA index_list(items)`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	indexNames := map[string]bool{}
	for _, row := range indexList.rows {
		if len(row) > 1 {
			indexNames[row[1].String()] = true
		}
	}
	if !indexNames["items_name_a"] || !indexNames["items_name_b"] {
		t.Fatalf("concurrent indexes = %#v", indexNames)
	}

	startColumns := make(chan struct{})
	columnErrors := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-startColumns
			name := "note_a"
			if worker%2 != 0 {
				name = "note_b"
			}
			_, err := executeKitDBRemoteSQL(
				context.Background(), nil, config.database,
				`ALTER TABLE items ADD COLUMN IF NOT EXISTS `+name+` TEXT DEFAULT ''`,
				kitSQLBindings{named: map[string]value.Value{}}, false,
			)
			if err != nil {
				columnErrors <- err
			}
		}(worker)
	}
	close(startColumns)
	wait.Wait()
	close(columnErrors)
	for err := range columnErrors {
		t.Errorf("concurrent ALTER TABLE: %v", err)
	}
	if t.Failed() {
		return
	}
	tableInfo, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database, `PRAGMA table_info(items)`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	fieldNames := map[string]bool{}
	for _, row := range tableInfo.rows {
		if len(row) > 1 {
			fieldNames[row[1].String()] = true
		}
	}
	if !fieldNames["note_a"] || !fieldNames["note_b"] {
		t.Fatalf("concurrent fields = %#v", fieldNames)
	}
	created, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`INSERT INTO items (name) VALUES ('published') RETURNING id, name`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.affected != 1 || len(created.rows) != 1 || created.rows[0][1].String() != "published" {
		t.Fatalf("unexpected inserted row: %#v", created)
	}
}
