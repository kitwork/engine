package work

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	requestscope "github.com/kitwork/engine/request"
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
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int } = database;
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

func TestKitDBHranaOverRealURLAndBearerAuth(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int, choice } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  title: text().notNull(),
  price: int().default(0).index(),
  status: choice("active", "disabled").default("active").index()
});
const remote = kitdb("remote.kitdb", { products }, { token: "remote-secret", access: "readwrite" });
const readonly = kitdb("readonly.kitdb", { products }, { token: "readonly-secret" });
router.get((ctx) => {
  const item = remote.products.where("sku", "=", "KIT-1").first();
  return ctx.json({ count: remote.products.count(), price: item ? item.price : null });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Host = "localhost"
		tenant.Serve(writer, request)
	}))
	defer server.Close()

	response, err := http.Get(server.URL + "/remote.kitdb/v3")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("version probe status = %d", response.StatusCode)
	}

	pipeline := func(path, token string, body any) (int, map[string]any) {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		decoded := map[string]any{}
		_ = json.NewDecoder(response.Body).Decode(&decoded)
		return response.StatusCode, decoded
	}
	execute := func(path, token, sql string, args []map[string]any, wantRows bool) (int, map[string]any) {
		t.Helper()
		return pipeline(path, token, map[string]any{
			"baton": nil,
			"requests": []any{
				map[string]any{"type": "execute", "stmt": map[string]any{"sql": sql, "args": args, "want_rows": wantRows}},
				map[string]any{"type": "close"},
			},
		})
	}
	textArg := func(text string) map[string]any { return map[string]any{"type": "text", "value": text} }
	intArg := func(number string) map[string]any { return map[string]any{"type": "integer", "value": number} }

	if status, _ := execute("/remote.kitdb/v3/pipeline", "wrong", `SELECT * FROM products`, nil, true); status != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", status)
	}

	status, inserted := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`INSERT INTO products (sku, title, price) VALUES (?, ?, ?) RETURNING id, sku, status`,
		[]map[string]any{textArg("KIT-1"), textArg("KitDB One"), intArg("100")}, true,
	)
	assertHranaOK(t, status, inserted, `"KIT-1"`, `"active"`)
	status, inserted = execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`INSERT INTO products (sku, title, price, status) VALUES (?, ?, ?, ?) RETURNING *`,
		[]map[string]any{textArg("KIT-2"), textArg("KitDB Two"), intArg("200"), textArg("disabled")}, true,
	)
	assertHranaOK(t, status, inserted, `"KIT-2"`, `"disabled"`)

	status, selected := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT sku, title, price FROM products WHERE price >= ? ORDER BY price DESC LIMIT ?`,
		[]map[string]any{intArg("100"), intArg("20")}, true,
	)
	assertHranaOK(t, status, selected, `"KIT-2"`, `"KIT-1"`, `"value":"200"`)
	status, selectedRange := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT sku, price FROM products WHERE price >= ? AND price < ? ORDER BY price ASC LIMIT ?`,
		[]map[string]any{intArg("100"), intArg("200"), intArg("1")}, true,
	)
	assertHranaOK(t, status, selectedRange, `"KIT-1"`, `"value":"100"`)
	status, explainedRange := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`EXPLAIN QUERY PLAN SELECT sku FROM products WHERE price >= ? AND price < ? ORDER BY price ASC LIMIT 1`,
		[]map[string]any{intArg("100"), intArg("200")}, true,
	)
	assertHranaOK(
		t, status, explainedRange,
		`KITDB INDEX RANGE idx_products_price (price) ON price; FILTER; STATS MISSING; INDEX ORDER; LIMIT 1; EARLY STOP`,
	)

	// DB managers browse LibSQL tables with bound pagination and a separate count.
	status, browsed := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT * FROM "products" LIMIT ? OFFSET ?`,
		[]map[string]any{intArg("50"), intArg("0")}, true,
	)
	assertHranaOK(t, status, browsed, `"KIT-1"`, `"KIT-2"`)
	status, counted := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT COUNT(*) AS total FROM "products"`, nil, true,
	)
	assertHranaOK(t, status, counted, `"total"`, `"value":"2"`)
	status, aggregated := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT COUNT(price) AS priced, SUM(price) AS total, AVG(price) AS mean, MIN(price) AS cheapest, MAX(price) AS costliest FROM products`, nil, true,
	)
	assertHranaOK(
		t, status, aggregated,
		`"priced"`, `"total"`, `"mean"`, `"cheapest"`, `"costliest"`,
		`"value":"300"`, `"value":150`, `"value":"100"`, `"value":"200"`,
	)
	status, explained := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`EXPLAIN QUERY PLAN SELECT sku FROM products WHERE status = ?`, []map[string]any{textArg("active")}, true,
	)
	assertHranaOK(
		t, status, explained, `"detail"`,
		`KITDB INDEX idx_products_status (status); FILTER; STATS MISSING; LIMIT 60; EARLY STOP`,
	)
	status, explainedAggregate := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`EXPLAIN QUERY PLAN SELECT SUM(price) FROM products WHERE status = ? ORDER BY sku DESC LIMIT 1`,
		[]map[string]any{textArg("active")}, true,
	)
	assertHranaOK(
		t, status, explainedAggregate,
		`KITDB INDEX idx_products_status (status); FILTER; STATS MISSING; STREAM AGGREGATE`,
	)
	explainedAggregateJSON, _ := json.Marshal(explainedAggregate)
	if strings.Contains(string(explainedAggregateJSON), "TEMP SORT") ||
		strings.Contains(string(explainedAggregateJSON), "LIMIT 1") {
		t.Fatalf("aggregate explain reports work that execution skips: %s", explainedAggregateJSON)
	}
	status, mixedAggregate := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT status, COUNT(*) FROM products`, nil, true,
	)
	assertHranaError(t, status, mixedAggregate, "cannot be mixed")
	status, staleColumn := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT * FROM "products" ORDER BY "test" ASC LIMIT ? OFFSET ?`,
		[]map[string]any{intArg("50"), intArg("0")}, true,
	)
	assertHranaError(t, status, staleColumn, "no such column: test")
	status, staleTable := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT * FROM "archived_products" LIMIT ? OFFSET ?`,
		[]map[string]any{intArg("50"), intArg("0")}, true,
	)
	assertHranaError(t, status, staleTable, "no such table: archived_products")

	status, stored := pipeline("/remote.kitdb/v3/pipeline", "remote-secret", map[string]any{
		"baton": nil,
		"requests": []any{
			map[string]any{"type": "store_sql", "sql_id": 7, "sql": `SELECT sku FROM products WHERE sku = :sku`},
			map[string]any{"type": "execute", "stmt": map[string]any{
				"sql_id": 7, "named_args": []any{map[string]any{"name": "sku", "value": textArg("KIT-2")}}, "want_rows": true,
			}},
			map[string]any{"type": "close_sql", "sql_id": 7},
			map[string]any{"type": "close"},
		},
	})
	assertHranaOK(t, status, stored, `"KIT-2"`, `"store_sql"`, `"close_sql"`)

	status, updated := execute(
		"/remote.kitdb/v2/pipeline", "remote-secret",
		`UPDATE products SET price = ? WHERE sku = ?`,
		[]map[string]any{intArg("125"), textArg("KIT-1")}, false,
	)
	assertHranaOK(t, status, updated, `"affected_row_count":1`)

	status, catalog := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT name, type FROM sqlite_master WHERE type = 'table' ORDER BY name LIMIT 20`, nil, true,
	)
	assertHranaOK(t, status, catalog, `"products"`, `"table"`)
	status, pragma := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret", `PRAGMA table_info(products)`, nil, true,
	)
	assertHranaOK(t, status, pragma, `"sku"`, `"price"`, `"status"`)
	status, indexStatus := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret", `PRAGMA index_status(products)`, nil, true,
	)
	assertHranaOK(t, status, indexStatus, `"mode"`, `"processed_rows"`, `"target_published"`)

	status, unsafe := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret", `DELETE FROM products`, nil, false,
	)
	assertHranaError(t, status, unsafe, "DELETE requires WHERE")
	status, transaction := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret", `BEGIN`, nil, false,
	)
	assertHranaError(t, status, transaction, "interactive transactions")
	status, readonlyWrite := execute(
		"/readonly.kitdb/v3/pipeline", "readonly-secret",
		`INSERT INTO products (sku, title) VALUES (?, ?)`, []map[string]any{textArg("NO"), textArg("No")}, false,
	)
	assertHranaError(t, status, readonlyWrite, "read-only")
	status, readonlyDDL := execute(
		"/readonly.kitdb/v3/pipeline", "readonly-secret",
		`CREATE TABLE denied (id KITID PRIMARY KEY)`, nil, false,
	)
	assertHranaError(t, status, readonlyDDL, "read-only")
	status, readonlyIndex := execute(
		"/readonly.kitdb/v3/pipeline", "readonly-secret",
		`CREATE INDEX denied_products_price ON products (price)`, nil, false,
	)
	assertHranaError(t, status, readonlyIndex, "read-only")
	status, readonlyAlter := execute(
		"/readonly.kitdb/v3/pipeline", "readonly-secret",
		`ALTER TABLE products ADD COLUMN denied TEXT`, nil, false,
	)
	assertHranaError(t, status, readonlyAlter, "read-only")

	request, err := http.NewRequest(http.MethodPost, server.URL+dataAPIQueryPath, strings.NewReader(
		`{"db":"remote.kitdb","sql":"SELECT sku, price FROM products WHERE sku = ?","args":["KIT-1"]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer remote-secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	dataBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(dataBody), `"sku":"KIT-1"`) || !strings.Contains(string(dataBody), `"price":125`) {
		t.Fatalf("KitDB data API status = %d, body = %s", response.StatusCode, dataBody)
	}

	response, err = http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	routeBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(routeBody), `"count":2`) || !strings.Contains(string(routeBody), `"price":125`) {
		t.Fatalf("ORM view status = %d, body = %s", response.StatusCode, routeBody)
	}
}

func TestKitDBRemoteCreateTablePersistsAndPublishesToORM(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb } = database;
const db = kitdb("dynamic.kitdb", {}, { token: "dynamic-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	start := func() (*Tenant, *httptest.Server) {
		t.Helper()
		tenant := NewTenant(root, "localhost")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			request.Host = "localhost"
			tenant.Serve(writer, request)
		}))
		return tenant, server
	}
	execute := func(server *httptest.Server, sql string, wantRows bool) (int, map[string]any) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"baton": nil,
			"requests": []any{
				map[string]any{"type": "execute", "stmt": map[string]any{"sql": sql, "want_rows": wantRows}},
				map[string]any{"type": "close"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+"/dynamic.kitdb/v3/pipeline", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer dynamic-secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		decoded := map[string]any{}
		_ = json.NewDecoder(response.Body).Decode(&decoded)
		return response.StatusCode, decoded
	}

	tenant, server := start()
	defer func(initialTenant *Tenant, initialServer *httptest.Server) {
		initialServer.Close()
		initialTenant.Close()
	}(tenant, server)
	status, created := execute(server, `CREATE TABLE users (
  id KITID PRIMARY KEY,
  email TEXT NOT NULL UNIQUE
)`, false)
	assertHranaOK(t, status, created)
	status, created = execute(server, `CREATE TABLE products (
  id KITID PRIMARY KEY,
  sku TEXT NOT NULL UNIQUE,
  price INTEGER DEFAULT 0,
  status CHOICE('active', 'disabled') DEFAULT 'active',
  owner_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
)`, false)
	assertHranaOK(t, status, created)

	status, insertedUser := execute(server, `INSERT INTO users (id, email) VALUES ('user_1', 'owner@example.com') RETURNING *`, true)
	assertHranaOK(t, status, insertedUser, `"user_1"`, `"owner@example.com"`)
	status, insertedProduct := execute(server, `INSERT INTO products (sku, owner_id) VALUES ('KIT-1', 'user_1') RETURNING id, sku, price, status, created_at`, true)
	assertHranaOK(t, status, insertedProduct, `"KIT-1"`, `"active"`, `"value":"0"`)
	status, createdIndex := execute(server, `CREATE INDEX products_status_price ON products (status, price)`, false)
	assertHranaOK(t, status, createdIndex)
	status, duplicateIndex := execute(server, `CREATE INDEX products_status_price ON products (status, price)`, false)
	assertHranaError(t, status, duplicateIndex, "already exists on table")
	status, idempotentIndex := execute(server, `CREATE INDEX IF NOT EXISTS products_status_price ON products (status, price)`, false)
	assertHranaOK(t, status, idempotentIndex)
	status, altered := execute(server, `ALTER TABLE products ADD COLUMN description TEXT NOT NULL DEFAULT ''`, false)
	assertHranaOK(t, status, altered)
	status, backfilled := execute(server, `SELECT sku, description FROM products WHERE sku = 'KIT-1'`, true)
	assertHranaOK(t, status, backfilled, `"description"`, `"value":""`)
	status, unsafeAlter := execute(server, `ALTER TABLE products ADD COLUMN required_note TEXT NOT NULL`, false)
	assertHranaError(t, status, unsafeAlter, "migration refused")
	status, duplicateColumn := execute(server, `ALTER TABLE products ADD COLUMN description TEXT`, false)
	assertHranaError(t, status, duplicateColumn, "already has column")
	status, idempotentColumn := execute(server, `ALTER TABLE products ADD COLUMN IF NOT EXISTS description TEXT`, false)
	assertHranaOK(t, status, idempotentColumn)

	status, invalidReference := execute(server, `INSERT INTO products (sku, owner_id) VALUES ('KIT-BAD', 'missing')`, false)
	assertHranaError(t, status, invalidReference, "references missing users.id")
	status, invalidType := execute(server, `INSERT INTO products (sku, price, owner_id) VALUES ('KIT-TYPE', 'not-an-integer', 'user_1')`, false)
	assertHranaError(t, status, invalidType, "expects an integer")
	status, duplicateTable := execute(server, `CREATE TABLE products (id KITID PRIMARY KEY)`, false)
	assertHranaError(t, status, duplicateTable, "already exists")
	status, idempotentTable := execute(server, `CREATE TABLE IF NOT EXISTS products (id KITID PRIMARY KEY)`, false)
	assertHranaOK(t, status, idempotentTable)
	status, catalog := execute(server, `SELECT name, type FROM sqlite_master WHERE type = 'table' ORDER BY name`, true)
	assertHranaOK(t, status, catalog, `"products"`, `"users"`)
	status, indexes := execute(server, `PRAGMA index_list(products)`, true)
	assertHranaOK(t, status, indexes, `"products_status_price"`)
	status, pragma := execute(server, `PRAGMA table_info(products)`, true)
	assertHranaOK(t, status, pragma, `"owner_id"`, `"created_at"`)

	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "1" {
		t.Fatalf("live ORM count status = %d, body = %s", response.StatusCode, body)
	}
	server.Close()
	tenant.Close()

	tenant, server = start()
	defer server.Close()
	defer tenant.Close()
	status, selected := execute(server, `SELECT sku, price, status, owner_id, description FROM products WHERE sku = 'KIT-1'`, true)
	assertHranaOK(t, status, selected, `"KIT-1"`, `"active"`, `"user_1"`, `"description"`)
	status, reopenedIndexes := execute(server, `PRAGMA index_list(products)`, true)
	assertHranaOK(t, status, reopenedIndexes, `"products_status_price"`)
	response, err = http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "1" {
		t.Fatalf("reopened ORM count status = %d, body = %s", response.StatusCode, body)
	}
}

func TestKitDBRemoteCreateTableConcurrentPublication(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("concurrent.kitdb", {}, { token: "concurrent-secret", access: "readwrite" });
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

func TestKitDBHranaBatchTransactionIsAtomicAndBounded(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, ref } = database;
const users = struct({ id: id(), email: text().notNull().unique() });
const products = struct({
  id: id(),
  code: text().notNull().unique().index(),
  owner_id: ref(users.id).notNull()
});
const db = kitdb("batch.kitdb", { users, products }, { token: "batch-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	_, config, found := resolveServe(tenant, "batch.kitdb")
	if !found || config.database == nil {
		t.Fatal("KitDB batch serve registration is unavailable")
	}
	if _, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`SELECT COUNT(*) AS total FROM users`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	); err != nil {
		t.Fatal(err)
	}
	lastTransaction := func() uint64 {
		t.Helper()
		managed, err := kitDBForRequest(tenant, "batch.kitdb", nil).database()
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
	beforeAtomicBatch := lastTransaction()

	step := func(sql string, wantRows ...bool) hranaBatchStep {
		want := len(wantRows) != 0 && wantRows[0]
		return hranaBatchStep{Stmt: hranaStmt{SQL: &sql, WantRows: want}}
	}
	run := func(steps ...hranaBatchStep) ([]any, []any) {
		t.Helper()
		return runKitDBHranaBatch(
			context.Background(), nil, config.database,
			&hranaBatch{Steps: steps}, map[int32]string{}, false,
		)
	}
	assertNoStepErrors := func(errors []any) {
		t.Helper()
		for index, item := range errors {
			if item != nil {
				t.Fatalf("batch step %d error = %#v", index, item)
			}
		}
	}

	results, errors := run(
		step("BEGIN"),
		step(`INSERT INTO users (id, email) VALUES ('user_1', 'owner@example.com')`),
		step(`INSERT INTO products (id, code, owner_id) VALUES ('product_1', 'KIT-1', 'user_1')`),
		step(`SELECT id, code FROM products WHERE code = 'KIT-1'`, true),
		step("COMMIT"),
	)
	assertNoStepErrors(errors)
	if after := lastTransaction(); after != beforeAtomicBatch+1 {
		t.Fatalf("two-row atomic batch advanced transaction %d -> %d, want one WAL transaction", beforeAtomicBatch, after)
	}
	encodedRead, err := json.Marshal(results[3])
	if err != nil || !bytes.Contains(encodedRead, []byte(`KIT-1`)) {
		t.Fatalf("transaction did not read its own indexed write: %s, %v", encodedRead, err)
	}

	beforeResidualRollback := lastTransaction()
	results, errors = run(
		step("BEGIN"),
		step(`UPDATE products SET code = LOWER(code) || '-draft' WHERE LENGTH(code) > 3 RETURNING code`, true),
		step(`DELETE FROM products WHERE LENGTH(code) > 8 RETURNING code`, true),
		step(`SELECT COUNT(*) AS total FROM products WHERE code = 'kit-1-draft'`, true),
		step("ROLLBACK"),
	)
	assertNoStepErrors(errors)
	encodedUpdate, updateErr := json.Marshal(results[1])
	encodedDelete, deleteErr := json.Marshal(results[2])
	encodedCount, countErr := json.Marshal(results[3])
	if updateErr != nil || deleteErr != nil || countErr != nil ||
		!bytes.Contains(encodedUpdate, []byte(`kit-1-draft`)) ||
		!bytes.Contains(encodedDelete, []byte(`kit-1-draft`)) ||
		!bytes.Contains(encodedCount, []byte(`"0"`)) {
		t.Fatalf(
			"residual transaction visibility update=%s delete=%s count=%s errors=%v/%v/%v",
			encodedUpdate, encodedDelete, encodedCount, updateErr, deleteErr, countErr,
		)
	}
	if after := lastTransaction(); after != beforeResidualRollback {
		t.Fatalf("rolled-back residual mutations advanced transaction %d -> %d", beforeResidualRollback, after)
	}
	rolledBackProduct, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`SELECT code FROM products WHERE id = 'product_1'`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || len(rolledBackProduct.rows) != 1 || rolledBackProduct.rows[0][0].String() != "KIT-1" {
		t.Fatalf("residual transaction rollback = %#v, %v", rolledBackProduct, err)
	}

	_, errors = run(
		step("BEGIN IMMEDIATE"),
		step(`INSERT INTO users (id, email) VALUES ('user_2', 'rollback@example.com')`),
		step(`INSERT INTO products (id, code, owner_id) VALUES ('product_2', 'KIT-1', 'user_2')`),
		step("COMMIT"),
	)
	if errors[2] == nil || !strings.Contains(fmt.Sprint(errors[2]), "must be unique") {
		t.Fatalf("duplicate batch error = %#v", errors)
	}
	rolledBack, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`SELECT COUNT(*) AS total FROM users WHERE id = 'user_2'`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || len(rolledBack.rows) != 1 || rolledBack.rows[0][0].N != 0 {
		t.Fatalf("failed batch leaked user_2: %#v, %v", rolledBack, err)
	}

	_, errors = run(
		step("BEGIN TRANSACTION"),
		step(`INSERT INTO users (id, email) VALUES ('user_3', 'explicit-rollback@example.com')`),
		step("ROLLBACK"),
	)
	assertNoStepErrors(errors)
	explicitRollback, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`SELECT COUNT(*) AS total FROM users WHERE id = 'user_3'`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || explicitRollback.rows[0][0].N != 0 {
		t.Fatalf("explicit rollback leaked user_3: %#v, %v", explicitRollback, err)
	}

	_, errors = run(step("BEGIN"), step(`CREATE TABLE denied (id KITID PRIMARY KEY)`), step("COMMIT"))
	if errors[1] == nil || !strings.Contains(fmt.Sprint(errors[1]), "schema changes are not allowed") {
		t.Fatalf("transactional DDL error = %#v", errors)
	}
	if _, _, err := kitDBRemoteDefinition(config.database, "denied"); err == nil {
		t.Fatal("refused transactional DDL published a struct")
	}

	transactionProxy, transaction, err := config.database.beginKitDBRecordTransaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeKitDBRemoteSQL(
		context.Background(), nil, transactionProxy,
		`INSERT INTO users (id, email) VALUES ('user_conflict', 'conflict@example.com')`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`INSERT INTO users (id, email) VALUES ('user_winner', 'winner@example.com')`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err == nil || !strings.Contains(err.Error(), "transaction conflict") ||
		!stderrors.Is(err, errKitDBTransactionConflict) ||
		kitDBRemoteErrorPayload(err)["code"] != kitDBTransactionConflictCode {
		t.Fatalf("stale transaction commit error = %v", err)
	}
	conflictRows, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`SELECT id FROM users WHERE id IN ('user_conflict', 'user_winner') ORDER BY id`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || len(conflictRows.rows) != 1 || conflictRows.rows[0][0].String() != "user_winner" {
		t.Fatalf("conflict publication = %#v, %v", conflictRows, err)
	}

	requestContext, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "http://localhost/batch.kitdb/v3/pipeline", nil).WithContext(requestContext)
	scope := requestscope.New(tenant, httptest.NewRecorder(), request)
	cancelProxy, canceledTransaction, err := config.database.beginKitDBRecordTransaction(scope)
	if err != nil {
		scope.Close()
		t.Fatal(err)
	}
	if _, err := executeKitDBRemoteSQL(
		requestContext, scope, cancelProxy,
		`INSERT INTO users (id, email) VALUES ('user_canceled', 'canceled@example.com')`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	); err != nil {
		_ = canceledTransaction.Rollback()
		scope.Close()
		t.Fatal(err)
	}
	cancel()
	if _, err := canceledTransaction.Commit(); !stderrors.Is(err, context.Canceled) {
		scope.Close()
		t.Fatalf("canceled transaction commit error = %v", err)
	}
	scope.Close()
	canceledRows, err := executeKitDBRemoteSQL(
		context.Background(), nil, config.database,
		`SELECT COUNT(*) AS total FROM users WHERE id = 'user_canceled'`,
		kitSQLBindings{named: map[string]value.Value{}}, false,
	)
	if err != nil || canceledRows.rows[0][0].N != 0 {
		t.Fatalf("canceled transaction leaked a row: %#v, %v", canceledRows, err)
	}

	tooMany := make([]hranaBatchStep, kitDBRemoteTransactionStatementLimit+1)
	for index := range tooMany {
		tooMany[index] = step("SELECT 1")
	}
	_, errors = run(tooMany...)
	if errors[0] == nil || !strings.Contains(fmt.Sprint(errors[0]), "exceeds 256 statements") {
		t.Fatalf("oversized batch error = %#v", errors[0])
	}
}

func TestKitDBOfficialLibSQLClient(t *testing.T) {
	clientDirectory := os.Getenv("KITDB_LIBSQL_CLIENT_DIR")
	if clientDirectory == "" {
		t.Skip("set KITDB_LIBSQL_CLIENT_DIR to a directory containing @libsql/client")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int } = database;
const products = struct({ id: id(), sku: text().notNull().unique().index(), title: text().notNull(), price: int().default(0) });
const db = kitdb("official.kitdb", { products }, { token: "official-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Host = "localhost"
		tenant.Serve(writer, request)
	}))
	defer server.Close()

	script := `
import { createClient } from "@libsql/client";
const client = createClient({ url: process.env.KITDB_URL, authToken: "official-secret" });
const probe = await client.execute("SELECT 1 AS connected");
if (Number(probe.rows[0].connected) !== 1) {
  throw new Error("connection probe failed: " + JSON.stringify(probe.rows));
}
const inserted = await client.execute({
  sql: "INSERT INTO products (sku, title, price) VALUES (?, ?, ?) RETURNING sku, price",
  args: ["NODE-1", "Official client", 42],
});
const selected = await client.execute({
  sql: "SELECT sku, price FROM products WHERE sku = ?",
  args: ["NODE-1"],
});
if (inserted.rows[0].sku !== "NODE-1" || Number(selected.rows[0].price) !== 42) {
  throw new Error("unexpected KitDB rows: " + JSON.stringify({ inserted: inserted.rows, selected: selected.rows }));
}
client.close();
console.log("official-libsql-client-ok");`
	command := exec.Command("node", "--input-type=module", "--eval", script)
	command.Dir = clientDirectory
	command.Env = append(os.Environ(), "KITDB_URL="+server.URL+"/official.kitdb")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("@libsql/client failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "official-libsql-client-ok") {
		t.Fatalf("unexpected @libsql/client output: %s", output)
	}
}

func assertHranaOK(t *testing.T, status int, body map[string]any, contains ...string) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	if status != http.StatusOK || strings.Contains(string(encoded), `"type":"error"`) {
		t.Fatalf("Hrana status = %d, body = %s", status, encoded)
	}
	for _, expected := range contains {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("Hrana body = %s, want %s", encoded, expected)
		}
	}
}

func assertHranaError(t *testing.T, status int, body map[string]any, contains string) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	if status != http.StatusOK || !strings.Contains(string(encoded), `"type":"error"`) || !strings.Contains(string(encoded), contains) {
		t.Fatalf("Hrana error status = %d, body = %s, want %q", status, encoded, contains)
	}
}
