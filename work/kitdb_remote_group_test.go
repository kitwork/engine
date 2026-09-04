package work

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

func TestKitDBRemoteSQLGroupByHaving(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int, bool } = database;
const sales = struct({
  id: id(),
  region: text().notNull().index(),
  category: text().notNull(),
  amount: int().notNull(),
  score: int(),
  enabled: bool().notNull()
});
const db = kitdb("group-sql.kitdb", { sales }, { token: "group-secret", access: "readwrite" });
router.get(() => db.sales.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	_, config, found := resolveServe(tenant, "group-sql.kitdb")
	if !found || config.database == nil {
		t.Fatal("KitDB grouped SQL serve registration is unavailable")
	}
	execute := func(source string) (kitDBRemoteResult, error) {
		t.Helper()
		return executeKitDBRemoteSQL(
			context.Background(), nil, config.database, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
	}

	if _, err := execute(`INSERT INTO sales (region, category, amount, score, enabled) VALUES
  ('north', 'book', 10, NULL, true),
  ('north', 'book', 20, 4, true),
  ('north', 'game', 30, 5, false),
  ('south', 'book', 15, NULL, true),
  ('south', 'game', 25, 2, false),
  ('south', 'game', 35, 8, true),
  ('east', 'book', 5, NULL, false),
  ('east', 'book', 5, 1, true)`); err != nil {
		t.Fatal(err)
	}

	const aggregateSQL = `SELECT
  region AS area,
  COUNT(*) AS products,
  COUNT(score) AS scored,
  SUM(amount) AS revenue,
  AVG(amount) AS mean,
  MIN(amount) AS low,
  MAX(amount) AS high
FROM sales
WHERE amount >= 5
GROUP BY region
HAVING (COUNT(*) >= 3 OR revenue = 10) AND scored >= 1
ORDER BY revenue DESC, area ASC`
	parsed, err := parseKitSQL(aggregateSQL, kitSQLBindings{named: map[string]value.Value{}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.groups, []string{"region"}) || parsed.having == nil || len(parsed.orders) != 2 {
		t.Fatalf("unexpected grouped statement: %#v", parsed)
	}

	type aggregateRow struct {
		area                     string
		products, scored         int
		revenue, mean, low, high float64
	}
	actualResult, err := execute(aggregateSQL)
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]aggregateRow, 0, len(actualResult.rows))
	for _, row := range actualResult.rows {
		actual = append(actual, aggregateRow{
			area: row[0].String(), products: int(row[1].N), scored: int(row[2].N),
			revenue: row[3].N, mean: row[4].N, low: row[5].N, high: row[6].N,
		})
	}

	oracle, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer oracle.Close()
	if _, err := oracle.Exec(`CREATE TABLE sales (
  region TEXT NOT NULL, category TEXT NOT NULL, amount INTEGER NOT NULL, score INTEGER
);
INSERT INTO sales VALUES
  ('north', 'book', 10, NULL),
  ('north', 'book', 20, 4),
  ('north', 'game', 30, 5),
  ('south', 'book', 15, NULL),
  ('south', 'game', 25, 2),
  ('south', 'game', 35, 8),
  ('east', 'book', 5, NULL),
  ('east', 'book', 5, 1);`); err != nil {
		t.Fatal(err)
	}
	rows, err := oracle.Query(aggregateSQL)
	if err != nil {
		t.Fatal(err)
	}
	expected := make([]aggregateRow, 0)
	for rows.Next() {
		var item aggregateRow
		if err := rows.Scan(
			&item.area, &item.products, &item.scored, &item.revenue, &item.mean, &item.low, &item.high,
		); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		expected = append(expected, item)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("GROUP BY differential: KitDB=%#v SQLite=%#v", actual, expected)
	}

	computed, err := execute(`SELECT
  UPPER(region) AS area,
  SUM(amount) * 2 AS doubled,
  ROUND(AVG(amount), 1) AS mean,
  CASE WHEN SUM(amount) >= 60 THEN 'large' ELSE 'small' END AS band
FROM sales
GROUP BY region
HAVING doubled >= 20
ORDER BY doubled DESC`)
	if err != nil {
		t.Fatal(err)
	}
	computedRows := make([]string, 0, len(computed.rows))
	for _, row := range computed.rows {
		computedRows = append(computedRows, fmt.Sprintf(
			"%s/%g/%g/%s", row[0].String(), row[1].N, row[2].N, row[3].String(),
		))
	}
	if want := []string{"SOUTH/150/25/large", "NORTH/120/20/large", "EAST/20/5/small"}; !reflect.DeepEqual(computedRows, want) {
		t.Fatalf("computed GROUP BY = %v, want %v", computedRows, want)
	}
	scalarExpression, err := execute(`SELECT SUM(amount) + COUNT(*) AS score FROM sales`)
	if err != nil || len(scalarExpression.rows) != 1 || scalarExpression.rows[0][0].N != 153 {
		t.Fatalf("computed scalar aggregate = %#v, %v", scalarExpression, err)
	}
	computedExplain, err := execute(`EXPLAIN QUERY PLAN
SELECT region, SUM(amount) * 2 AS doubled FROM sales GROUP BY region ORDER BY doubled DESC`)
	if err != nil || len(computedExplain.rows) != 1 ||
		!strings.Contains(computedExplain.rows[0][3].String(), "PROJECT EXPRESSIONS") {
		t.Fatalf("computed GROUP BY EXPLAIN = %#v, %v", computedExplain, err)
	}
	residualGroup, err := execute(`SELECT region, SUM(amount) AS revenue
FROM sales
WHERE amount * 2 >= 20
GROUP BY region
HAVING SUM(amount) * 2 >= 100 AND LOWER(region) != 'east'
ORDER BY revenue DESC`)
	if err != nil {
		t.Fatal(err)
	}
	residualGroupRows := make([]string, 0, len(residualGroup.rows))
	for _, row := range residualGroup.rows {
		residualGroupRows = append(residualGroupRows, fmt.Sprintf("%s/%g", row[0].String(), row[1].N))
	}
	if want := []string{"south/75", "north/60"}; !reflect.DeepEqual(residualGroupRows, want) {
		t.Fatalf("residual GROUP/HAVING expressions = %v, want %v", residualGroupRows, want)
	}

	multiple, err := execute(`SELECT region, category, COUNT(*) AS total, SUM(amount) AS revenue
FROM sales GROUP BY region, category HAVING total >= 2 ORDER BY revenue DESC, region ASC`)
	if err != nil {
		t.Fatal(err)
	}
	multipleRows := make([]string, 0, len(multiple.rows))
	for _, row := range multiple.rows {
		multipleRows = append(multipleRows, fmt.Sprintf("%s/%s/%g/%g", row[0].String(), row[1].String(), row[2].N, row[3].N))
	}
	if want := []string{"south/game/2/60", "north/book/2/30", "east/book/2/10"}; !reflect.DeepEqual(multipleRows, want) {
		t.Fatalf("multi-field GROUP BY = %v, want %v", multipleRows, want)
	}

	distinct, err := execute(`SELECT category FROM sales GROUP BY category ORDER BY category`)
	if err != nil || len(distinct.rows) != 2 || distinct.rows[0][0].String() != "book" || distinct.rows[1][0].String() != "game" {
		t.Fatalf("group-only query = %#v, %v", distinct, err)
	}
	distinctSelect, err := execute(`SELECT DISTINCT category AS kind FROM sales ORDER BY kind`)
	if err != nil || len(distinctSelect.rows) != 2 || distinctSelect.rows[0][0].String() != "book" ||
		distinctSelect.rows[1][0].String() != "game" {
		t.Fatalf("DISTINCT query = %#v, %v", distinctSelect, err)
	}
	distinctPage, err := execute(`SELECT DISTINCT category AS kind FROM sales ORDER BY kind DESC LIMIT 1 OFFSET 1`)
	if err != nil || len(distinctPage.rows) != 1 || distinctPage.rows[0][0].String() != "book" {
		t.Fatalf("DISTINCT LIMIT/OFFSET = %#v, %v", distinctPage, err)
	}
	distinctExplained, err := execute(`EXPLAIN QUERY PLAN SELECT DISTINCT category FROM sales ORDER BY category`)
	if err != nil || len(distinctExplained.rows) != 1 ||
		!strings.Contains(distinctExplained.rows[0][3].String(), "HASH DISTINCT category") {
		t.Fatalf("DISTINCT EXPLAIN = %#v, %v", distinctExplained, err)
	}
	booleanGroup, err := execute(`SELECT enabled, COUNT(*) AS total FROM sales
GROUP BY enabled HAVING enabled = true ORDER BY enabled`)
	if err != nil || len(booleanGroup.rows) != 1 || booleanGroup.rows[0][0].K != value.Bool ||
		booleanGroup.rows[0][0].N != 1 || booleanGroup.rows[0][1].N != 5 {
		t.Fatalf("boolean GROUP BY/HAVING = %#v, %v", booleanGroup, err)
	}
	booleanAggregate, err := execute(`SELECT MIN(enabled) AS minimum FROM sales HAVING minimum = false`)
	if err != nil || len(booleanAggregate.rows) != 1 || booleanAggregate.rows[0][0].K != value.Bool ||
		booleanAggregate.rows[0][0].N != 0 {
		t.Fatalf("boolean aggregate HAVING = %#v, %v", booleanAggregate, err)
	}
	scalar, err := execute(`SELECT COUNT(*) AS total, SUM(amount) AS revenue FROM sales HAVING total > 7`)
	if err != nil || len(scalar.rows) != 1 || scalar.rows[0][0].N != 8 || scalar.rows[0][1].N != 145 {
		t.Fatalf("scalar HAVING = %#v, %v", scalar, err)
	}
	emptyAggregate, err := execute(`SELECT COUNT(*) AS total, SUM(amount) AS revenue
FROM sales WHERE region = 'missing' HAVING COUNT(*) = 0`)
	if err != nil || len(emptyAggregate.rows) != 1 || emptyAggregate.rows[0][0].N != 0 || !emptyAggregate.rows[0][1].IsNil() {
		t.Fatalf("empty scalar aggregate = %#v, %v", emptyAggregate, err)
	}
	filteredScalar, err := execute(`SELECT COUNT(*) AS total FROM sales HAVING COUNT(*) > 10`)
	if err != nil || len(filteredScalar.rows) != 0 {
		t.Fatalf("filtered scalar HAVING = %#v, %v", filteredScalar, err)
	}
	paged, err := execute(`SELECT region, SUM(amount) AS revenue FROM sales
GROUP BY region ORDER BY revenue DESC LIMIT 1 OFFSET 1`)
	if err != nil || len(paged.rows) != 1 || paged.rows[0][0].String() != "north" {
		t.Fatalf("group LIMIT/OFFSET = %#v, %v", paged, err)
	}
	zero, err := execute(`SELECT region, COUNT(*) AS total FROM sales GROUP BY region LIMIT 0`)
	if err != nil || len(zero.columns) != 2 || len(zero.rows) != 0 {
		t.Fatalf("group LIMIT 0 = %#v, %v", zero, err)
	}

	explained, err := execute(`EXPLAIN QUERY PLAN SELECT category, COUNT(*) AS total
FROM sales WHERE region = 'north' GROUP BY category HAVING total > 0 ORDER BY total DESC`)
	if err != nil || len(explained.rows) != 1 {
		t.Fatalf("group EXPLAIN = %#v, %v", explained, err)
	}
	detail := explained.rows[0][3].String()
	for _, marker := range []string{"KITDB INDEX", "HASH GROUP BY category", "HAVING", "TEMP SORT", "LIMIT 60"} {
		if !strings.Contains(detail, marker) {
			t.Fatalf("group EXPLAIN %q lacks %q", detail, marker)
		}
	}

	for source, marker := range map[string]string{
		`SELECT DISTINCT category FROM sales GROUP BY category`:                                                    "cannot be combined",
		`SELECT region, amount, COUNT(*) FROM sales GROUP BY region`:                                               "must appear in GROUP BY",
		`SELECT * FROM sales GROUP BY region`:                                                                      "* is not supported",
		`SELECT region FROM sales GROUP BY region HAVING amount > 10`:                                              "is not grouped or aggregated",
		`SELECT region, SUM(amount) AS region FROM sales GROUP BY region`:                                          "ambiguous",
		`SELECT region, SUM(amount) AS revenue FROM sales GROUP BY region, region`:                                 "duplicate GROUP BY",
		`SELECT region, SUM(amount) AS revenue FROM sales GROUP BY region ORDER BY amount`:                         "must be grouped or be a SELECT alias",
		`SELECT region FROM sales HAVING region = 'north'`:                                                         "must appear in GROUP BY",
		`SELECT region, amount * 2 FROM sales GROUP BY region`:                                                     "must appear in GROUP BY",
		`SELECT region FROM sales GROUP BY region, category, amount, score, enabled, id, region, category, amount`: "GROUP BY exceeds 8 fields",
		`SELECT region,
COUNT(*), COUNT(score), COUNT(amount), COUNT(category), COUNT(enabled),
SUM(amount), SUM(score), AVG(amount), AVG(score),
MIN(amount), MIN(score), MIN(region), MIN(category), MIN(enabled),
MAX(amount), MAX(score), MAX(region)
FROM sales GROUP BY region`: "exceeds 16 aggregate states",
	} {
		if _, err := execute(source); err == nil || !strings.Contains(err.Error(), marker) {
			t.Errorf("query %q error = %v, want marker %q", source, err, marker)
		}
	}
	wide := make([]string, kitDBRemoteGroupProjectionLimit+1)
	for index := range wide {
		wide[index] = fmt.Sprintf("region AS region_%d", index)
	}
	if _, err := execute("SELECT " + strings.Join(wide, ", ") + " FROM sales GROUP BY region"); err == nil ||
		!strings.Contains(err.Error(), "exceeds 32 projections") {
		t.Errorf("wide grouped projection error = %v", err)
	}
}

func TestKitDBRemoteSQLGroupLimit(t *testing.T) {
	if err := ensureKitDBRemoteGroupCapacity(kitDBRemoteGroupLimit - 1); err != nil {
		t.Fatalf("last allowed group was rejected: %v", err)
	}
	if err := ensureKitDBRemoteGroupCapacity(kitDBRemoteGroupLimit); err == nil || !strings.Contains(err.Error(), "10000") {
		t.Fatalf("group limit error = %v", err)
	}

	rows := make([]kitDBRemoteGroupedRow, kitDBRemoteSelectRowLimit+1)
	if got := len(boundKitDBRemoteGroupedRows(rows, kitSQLStatement{})); got != query.DefaultDBLimit {
		t.Fatalf("default grouped result bound = %d, want %d", got, query.DefaultDBLimit)
	}
	unboundedRequest := kitSQLStatement{hasLimit: true, limit: kitDBRemoteSelectRowLimit + 100}
	if got := len(boundKitDBRemoteGroupedRows(rows, unboundedRequest)); got != kitDBRemoteSelectRowLimit {
		t.Fatalf("hard grouped result bound = %d, want %d", got, kitDBRemoteSelectRowLimit)
	}
}
