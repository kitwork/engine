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

func TestKitDBRemoteSQLIndexedInnerAndLeftJoin(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int, bool, ref } = database;
const users = struct({
  id: id(),
  email: text().notNull().unique(),
  name: text().notNull(),
  active: bool().notNull()
});
const orders = struct({
  id: id(),
  code: text().notNull().unique(),
  user_id: ref(users.id).notNull().index(),
  amount: int().notNull(),
  status: text().notNull().index(),
  description: text()
});
const db = kitdb("join-sql.kitdb", { users, orders }, { token: "join-secret", access: "readwrite" });
router.get(() => db.orders.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	_, config, found := resolveServe(tenant, "join-sql.kitdb")
	if !found || config.database == nil {
		t.Fatal("KitDB joined SQL serve registration is unavailable")
	}
	execute := func(source string) (kitDBRemoteResult, error) {
		t.Helper()
		return executeKitDBRemoteSQL(
			context.Background(), nil, config.database, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
	}

	if _, err := execute(`INSERT INTO users (id, email, name, active) VALUES
  ('user_1', 'alice@example.com', 'Alice', true),
  ('user_2', 'bob@example.com', 'Bob', false),
  ('user_3', 'cara@example.com', 'Cara', true),
  ('user_4', 'none@example.com', 'NoOrder', true),
  ('user_5', 'draft@example.com', 'DraftOnly', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(`INSERT INTO orders (id, code, user_id, amount, status, description) VALUES
  ('order_1', 'O1', 'user_1', 10, 'paid', 'first'),
  ('order_2', 'O2', 'user_1', 20, 'draft', NULL),
  ('order_3', 'O3', 'user_2', 30, 'paid', 'third'),
  ('order_4', 'O4', 'user_3', 40, 'paid', NULL),
  ('order_5', 'O5', 'user_3', 50, 'paid', 'fifth'),
  ('order_6', 'O6', 'user_5', 1, 'draft', 'only draft')`); err != nil {
		t.Fatal(err)
	}

	const innerSQL = `SELECT o.code, u.name AS customer, o.amount
FROM orders AS o
INNER JOIN users u ON o.user_id = u.id
WHERE o.status = 'paid' AND u.active = true
ORDER BY o.amount DESC, customer ASC`
	actual, err := execute(innerSQL)
	if err != nil {
		t.Fatal(err)
	}
	actualRows := make([]string, 0, len(actual.rows))
	for _, row := range actual.rows {
		actualRows = append(actualRows, fmt.Sprintf("%s/%s/%g", row[0].String(), row[1].String(), row[2].N))
	}

	oracle, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer oracle.Close()
	if _, err := oracle.Exec(`CREATE TABLE users (
  id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, name TEXT NOT NULL, active INTEGER NOT NULL
);
CREATE TABLE orders (
  id TEXT PRIMARY KEY, code TEXT NOT NULL UNIQUE, user_id TEXT NOT NULL,
  amount INTEGER NOT NULL, status TEXT NOT NULL, description TEXT
);
INSERT INTO users VALUES
  ('user_1', 'alice@example.com', 'Alice', 1),
  ('user_2', 'bob@example.com', 'Bob', 0),
  ('user_3', 'cara@example.com', 'Cara', 1),
  ('user_4', 'none@example.com', 'NoOrder', 1),
  ('user_5', 'draft@example.com', 'DraftOnly', 1);
INSERT INTO orders VALUES
  ('order_1', 'O1', 'user_1', 10, 'paid', 'first'),
  ('order_2', 'O2', 'user_1', 20, 'draft', NULL),
  ('order_3', 'O3', 'user_2', 30, 'paid', 'third'),
  ('order_4', 'O4', 'user_3', 40, 'paid', NULL),
  ('order_5', 'O5', 'user_3', 50, 'paid', 'fifth'),
  ('order_6', 'O6', 'user_5', 1, 'draft', 'only draft');`); err != nil {
		t.Fatal(err)
	}
	rows, err := oracle.Query(innerSQL)
	if err != nil {
		t.Fatal(err)
	}
	expectedRows := make([]string, 0)
	for rows.Next() {
		var code, customer string
		var amount int64
		if err := rows.Scan(&code, &customer, &amount); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		expectedRows = append(expectedRows, fmt.Sprintf("%s/%s/%d", code, customer, amount))
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualRows, expectedRows) {
		t.Fatalf("INNER JOIN differential: KitDB=%v SQLite=%v", actualRows, expectedRows)
	}

	joinedExpressions, err := execute(`SELECT o.code,
  UPPER(u.name) || ':' || COALESCE(o.description, 'none') AS label,
  o.amount * 2 AS doubled
FROM orders o JOIN users u ON o.user_id = u.id
WHERE o.status = 'paid'
ORDER BY doubled DESC, LOWER(u.name) ASC`)
	if err != nil {
		t.Fatal(err)
	}
	joinedExpressionRows := make([]string, 0, len(joinedExpressions.rows))
	for _, row := range joinedExpressions.rows {
		joinedExpressionRows = append(joinedExpressionRows, fmt.Sprintf(
			"%s/%s/%g", row[0].String(), row[1].String(), row[2].N,
		))
	}
	if want := []string{
		"O5/CARA:fifth/100", "O4/CARA:none/80", "O3/BOB:third/60", "O1/ALICE:first/20",
	}; !reflect.DeepEqual(joinedExpressionRows, want) {
		t.Fatalf("joined expressions = %v, want %v", joinedExpressionRows, want)
	}
	joinedFilter, err := execute(`SELECT o.code, u.name, o.amount * 2 AS doubled
FROM orders o JOIN users u ON o.user_id = u.id
WHERE o.status = 'paid' AND o.amount * 2 >= 60 AND LOWER(u.name) != 'cara'
ORDER BY doubled DESC`)
	if err != nil || len(joinedFilter.rows) != 1 || joinedFilter.rows[0][0].String() != "O3" ||
		joinedFilter.rows[0][1].String() != "Bob" || joinedFilter.rows[0][2].N != 60 {
		t.Fatalf("joined residual filter = %#v, %v", joinedFilter, err)
	}

	const leftSQL = `SELECT u.name, o.code, o.amount
FROM users u LEFT OUTER JOIN orders o ON u.id = o.user_id
WHERE u.active = true
ORDER BY u.name ASC, o.code ASC`
	left, err := execute(leftSQL)
	if err != nil {
		t.Fatal(err)
	}
	leftRows := make([]string, 0, len(left.rows))
	for _, row := range left.rows {
		code, amount := "NULL", "NULL"
		if !row[1].IsNil() {
			code = row[1].String()
		}
		if !row[2].IsNil() {
			amount = row[2].Text()
		}
		leftRows = append(leftRows, row[0].String()+"/"+code+"/"+amount)
	}
	if want := []string{
		"Alice/O1/10", "Alice/O2/20", "Cara/O4/40", "Cara/O5/50",
		"DraftOnly/O6/1", "NoOrder/NULL/NULL",
	}; !reflect.DeepEqual(leftRows, want) {
		t.Fatalf("LEFT JOIN = %v, want %v", leftRows, want)
	}
	oracleLeft, err := oracle.Query(leftSQL)
	if err != nil {
		t.Fatal(err)
	}
	expectedLeft := make([]string, 0)
	for oracleLeft.Next() {
		var name string
		var code sql.NullString
		var amount sql.NullInt64
		if err := oracleLeft.Scan(&name, &code, &amount); err != nil {
			oracleLeft.Close()
			t.Fatal(err)
		}
		codeText, amountText := "NULL", "NULL"
		if code.Valid {
			codeText = code.String
		}
		if amount.Valid {
			amountText = fmt.Sprint(amount.Int64)
		}
		expectedLeft = append(expectedLeft, name+"/"+codeText+"/"+amountText)
	}
	if err := oracleLeft.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leftRows, expectedLeft) {
		t.Fatalf("LEFT JOIN differential: KitDB=%v SQLite=%v", leftRows, expectedLeft)
	}

	rightFiltered, err := execute(`SELECT u.name, o.code
FROM users u LEFT JOIN orders o ON u.id = o.user_id
WHERE o.status = 'paid' OR o.status IS NULL
ORDER BY u.name, o.code`)
	if err != nil {
		t.Fatal(err)
	}
	filteredRows := make([]string, 0, len(rightFiltered.rows))
	for _, row := range rightFiltered.rows {
		code := "NULL"
		if !row[1].IsNil() {
			code = row[1].String()
		}
		filteredRows = append(filteredRows, row[0].String()+"/"+code)
	}
	if want := []string{"Alice/O1", "Bob/O3", "Cara/O4", "Cara/O5", "NoOrder/NULL"}; !reflect.DeepEqual(filteredRows, want) {
		t.Fatalf("LEFT JOIN right WHERE = %v, want %v", filteredRows, want)
	}

	qualified, err := execute(`SELECT o.id AS order_id, u.id AS user_id
FROM orders o JOIN users u ON o.user_id = u.id WHERE o.code = 'O1'`)
	if err != nil || len(qualified.rows) != 1 ||
		qualified.rows[0][0].String() != "order_1" || qualified.rows[0][1].String() != "user_1" {
		t.Fatalf("qualified duplicate columns = %#v, %v", qualified, err)
	}
	unqualified, err := execute(`SELECT code, name FROM orders o JOIN users u ON o.user_id = u.id
WHERE code = 'O3'`)
	if err != nil || len(unqualified.rows) != 1 || unqualified.rows[0][1].String() != "Bob" {
		t.Fatalf("unqualified unique columns = %#v, %v", unqualified, err)
	}
	distinctJoin, err := execute(`SELECT DISTINCT o.status AS state
FROM orders o JOIN users u ON o.user_id = u.id ORDER BY state`)
	if err != nil || len(distinctJoin.rows) != 2 || distinctJoin.rows[0][0].String() != "draft" ||
		distinctJoin.rows[1][0].String() != "paid" {
		t.Fatalf("joined DISTINCT = %#v, %v", distinctJoin, err)
	}
	distinctJoinedStar, err := execute(`SELECT DISTINCT u.*
FROM orders o JOIN users u ON o.user_id = u.id ORDER BY u.id`)
	if err != nil || len(distinctJoinedStar.columns) != 4 || len(distinctJoinedStar.rows) != 4 {
		t.Fatalf("joined qualified DISTINCT star = %#v, %v", distinctJoinedStar, err)
	}
	star, err := execute(`SELECT o.* FROM orders o JOIN users u ON o.user_id = u.id WHERE o.code = 'O1'`)
	if err != nil || len(star.columns) != 6 || len(star.rows) != 1 {
		t.Fatalf("qualified star = %#v, %v", star, err)
	}
	paged, err := execute(`SELECT o.code, u.name FROM orders o JOIN users u ON o.user_id = u.id
ORDER BY o.amount DESC LIMIT 1 OFFSET 1`)
	if err != nil || len(paged.rows) != 1 || paged.rows[0][0].String() != "O4" {
		t.Fatalf("joined LIMIT/OFFSET = %#v, %v", paged, err)
	}
	zero, err := execute(`SELECT o.code, u.name FROM orders o JOIN users u ON o.user_id = u.id LIMIT 0`)
	if err != nil || len(zero.columns) != 2 || len(zero.rows) != 0 {
		t.Fatalf("joined LIMIT 0 = %#v, %v", zero, err)
	}
	catalogDistinct, err := execute(`SELECT DISTINCT type AS kind FROM sqlite_master ORDER BY kind`)
	if err != nil || len(catalogDistinct.rows) != 2 || catalogDistinct.rows[0][0].String() != "index" ||
		catalogDistinct.rows[1][0].String() != "table" {
		t.Fatalf("catalog DISTINCT = %#v, %v", catalogDistinct, err)
	}

	const aggregateJoinSQL = `SELECT
  COUNT(*) AS total,
  COUNT(o.description) AS described,
  SUM(o.amount) AS revenue,
  AVG(o.amount) AS average,
  MIN(o.amount) AS low,
  MAX(o.amount) AS high
FROM orders o JOIN users u ON o.user_id = u.id
WHERE u.active = true`
	aggregated, err := execute(aggregateJoinSQL)
	if err != nil || len(aggregated.rows) != 1 {
		t.Fatalf("joined aggregate = %#v, %v", aggregated, err)
	}
	var oracleTotal, oracleDescribed, oracleRevenue, oracleLow, oracleHigh int64
	var oracleAverage float64
	if err := oracle.QueryRow(aggregateJoinSQL).Scan(
		&oracleTotal, &oracleDescribed, &oracleRevenue, &oracleAverage, &oracleLow, &oracleHigh,
	); err != nil {
		t.Fatal(err)
	}
	wantAggregate := []float64{
		float64(oracleTotal), float64(oracleDescribed), float64(oracleRevenue),
		oracleAverage, float64(oracleLow), float64(oracleHigh),
	}
	for index, want := range wantAggregate {
		if got := aggregated.rows[0][index]; got.K != value.Number || got.N != want {
			t.Fatalf("joined aggregate column %d = %#v, want %g", index, got, want)
		}
	}

	const groupedJoinSQL = `SELECT
  u.name AS customer,
  COUNT(o.id) AS orders,
  COUNT(*) AS joined_rows,
  SUM(o.amount) AS revenue
FROM users u LEFT JOIN orders o ON u.id = o.user_id
WHERE u.active = true
GROUP BY u.id, u.name
HAVING joined_rows >= 1
ORDER BY orders DESC, customer ASC`
	grouped, err := execute(groupedJoinSQL)
	if err != nil {
		t.Fatal(err)
	}
	groupedRows := make([]string, 0, len(grouped.rows))
	for _, row := range grouped.rows {
		revenue := "NULL"
		if !row[3].IsNil() {
			revenue = row[3].Text()
		}
		groupedRows = append(groupedRows, fmt.Sprintf(
			"%s/%s/%s/%s", row[0].String(), row[1].Text(), row[2].Text(), revenue,
		))
	}
	oracleGrouped, err := oracle.Query(groupedJoinSQL)
	if err != nil {
		t.Fatal(err)
	}
	expectedGrouped := make([]string, 0)
	for oracleGrouped.Next() {
		var customer string
		var orders, joinedRows int64
		var revenue sql.NullInt64
		if err := oracleGrouped.Scan(&customer, &orders, &joinedRows, &revenue); err != nil {
			oracleGrouped.Close()
			t.Fatal(err)
		}
		revenueText := "NULL"
		if revenue.Valid {
			revenueText = fmt.Sprint(revenue.Int64)
		}
		expectedGrouped = append(expectedGrouped, fmt.Sprintf(
			"%s/%d/%d/%s", customer, orders, joinedRows, revenueText,
		))
	}
	if err := oracleGrouped.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(groupedRows, expectedGrouped) {
		t.Fatalf("joined GROUP BY differential: KitDB=%v SQLite=%v", groupedRows, expectedGrouped)
	}
	if want := []string{"Alice/2/2/30", "Cara/2/2/90", "DraftOnly/1/1/1", "NoOrder/0/1/NULL"}; !reflect.DeepEqual(groupedRows, want) {
		t.Fatalf("joined GROUP BY = %v, want %v", groupedRows, want)
	}

	groupExplained, err := execute(`EXPLAIN QUERY PLAN ` + groupedJoinSQL)
	if err != nil || len(groupExplained.rows) != 1 {
		t.Fatalf("joined GROUP BY EXPLAIN = %#v, %v", groupExplained, err)
	}
	groupDetail := groupExplained.rows[0][3].String()
	for _, marker := range []string{
		"INDEX NESTED LOOP LEFT JOIN orders", "HASH GROUP BY id,name",
		"HAVING", "TEMP SORT", "LIMIT 60", "INPUT CAP 10000", "PAIR CAP 20000",
	} {
		if !strings.Contains(groupDetail, marker) {
			t.Fatalf("joined GROUP BY EXPLAIN %q lacks %q", groupDetail, marker)
		}
	}

	explained, err := execute(`EXPLAIN QUERY PLAN SELECT o.code, u.name
FROM orders o JOIN users u ON o.user_id = u.id
WHERE o.status = 'paid' ORDER BY o.amount DESC LIMIT 2`)
	if err != nil || len(explained.rows) != 1 {
		t.Fatalf("joined EXPLAIN = %#v, %v", explained, err)
	}
	detail := explained.rows[0][3].String()
	for _, marker := range []string{
		"KITDB INDEX idx_orders_status", "INDEX NESTED LOOP INNER JOIN users",
		"USING PRIMARY", "JOIN FILTER", "TEMP SORT", "LIMIT 2",
		"INPUT CAP 10000", "PAIR CAP 20000",
	} {
		if !strings.Contains(detail, marker) {
			t.Fatalf("joined EXPLAIN %q lacks %q", detail, marker)
		}
	}

	for source, marker := range map[string]string{
		`SELECT id FROM orders o JOIN users u ON o.user_id = u.id`:                       "ambiguous column",
		`SELECT o.code FROM orders o JOIN users u ON o.description = u.name`:             "must be primary, unique, or the first field",
		`SELECT o.code FROM orders o JOIN users u ON o.amount = u.id`:                    "incompatible storage types",
		`SELECT o.code, COUNT(*) FROM orders o JOIN users u ON o.user_id = u.id`:         "without GROUP BY",
		`SELECT o.code FROM orders o JOIN users o ON o.user_id = o.id`:                   "duplicate table alias",
		`SELECT * FROM sqlite_master m JOIN users u ON m.name = u.name`:                  "JOIN is not supported on catalog metadata",
		`SELECT * FROM orders o JOIN users u ON o.user_id = u.id GROUP BY o.status`:      "* is not supported",
		`SELECT o.code FROM orders o JOIN users u ON o.user_id = u.id GROUP BY o.status`: "must appear in GROUP BY",
	} {
		if _, err := execute(source); err == nil || !strings.Contains(err.Error(), marker) {
			t.Errorf("query %q error = %v, want marker %q", source, err, marker)
		}
	}

	wide := make([]string, kitDBRemoteJoinProjectionLimit+1)
	for index := range wide {
		wide[index] = fmt.Sprintf("o.code AS code_%d", index)
	}
	if _, err := execute(
		"SELECT " + strings.Join(wide, ", ") +
			" FROM orders o JOIN users u ON o.user_id = u.id",
	); err == nil || !strings.Contains(err.Error(), "exceeds 64 projected fields") {
		t.Errorf("wide joined projection error = %v", err)
	}
	orders := make([]string, kitDBRemoteJoinOrderLimit+1)
	for index := range orders {
		orders[index] = "o.code"
	}
	if _, err := execute(
		"SELECT o.code FROM orders o JOIN users u ON o.user_id = u.id ORDER BY " +
			strings.Join(orders, ", "),
	); err == nil || !strings.Contains(err.Error(), "exceeds 8 ORDER BY fields") {
		t.Errorf("wide joined order error = %v", err)
	}

	transactionProxy, transaction, err := config.database.beginKitDBRecordTransaction(nil)
	if err != nil {
		t.Fatal(err)
	}
	executeTransaction := func(source string) (kitDBRemoteResult, error) {
		return executeKitDBRemoteSQL(
			context.Background(), nil, transactionProxy, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
	}
	if _, err := executeTransaction(
		`INSERT INTO users (id, email, name, active) VALUES ('user_tx', 'tx@example.com', 'Pending', true)`,
	); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := executeTransaction(
		`INSERT INTO orders (id, code, user_id, amount, status) VALUES ('order_tx', 'OTX', 'user_tx', 99, 'paid')`,
	); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	pending, err := executeTransaction(`SELECT o.code, u.name FROM orders o
JOIN users u ON o.user_id = u.id WHERE o.code = 'OTX'`)
	if err != nil || len(pending.rows) != 1 || pending.rows[0][1].String() != "Pending" {
		_ = transaction.Rollback()
		t.Fatalf("transaction JOIN read-your-writes = %#v, %v", pending, err)
	}
	pendingReverse, err := executeTransaction(`SELECT u.name, o.code FROM users u
LEFT JOIN orders o ON u.id = o.user_id WHERE u.id = 'user_tx'`)
	if err != nil || len(pendingReverse.rows) != 1 || pendingReverse.rows[0][1].String() != "OTX" {
		_ = transaction.Rollback()
		t.Fatalf("transaction secondary-index JOIN = %#v, %v", pendingReverse, err)
	}
	pendingAggregate, err := executeTransaction(`SELECT u.name, COUNT(o.id) AS orders, SUM(o.amount) AS revenue
FROM users u LEFT JOIN orders o ON u.id = o.user_id
WHERE u.id = 'user_tx' GROUP BY u.id, u.name`)
	if err != nil || len(pendingAggregate.rows) != 1 || pendingAggregate.rows[0][0].String() != "Pending" ||
		pendingAggregate.rows[0][1].N != 1 || pendingAggregate.rows[0][2].N != 99 {
		_ = transaction.Rollback()
		t.Fatalf("transaction grouped JOIN read-your-writes = %#v, %v", pendingAggregate, err)
	}
	outside, err := execute(`SELECT o.code FROM orders o JOIN users u ON o.user_id = u.id WHERE o.code = 'OTX'`)
	if err != nil || len(outside.rows) != 0 {
		_ = transaction.Rollback()
		t.Fatalf("uncommitted JOIN rows leaked outside transaction: %#v, %v", outside, err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestKitDBRemoteSQLJoinResourceLimits(t *testing.T) {
	if err := ensureKitDBRemoteJoinInput(kitDBRemoteJoinInputLimit); err != nil {
		t.Fatalf("last allowed JOIN input was rejected: %v", err)
	}
	if err := ensureKitDBRemoteJoinInput(kitDBRemoteJoinInputLimit + 1); err == nil ||
		!strings.Contains(err.Error(), "10000") {
		t.Fatalf("JOIN input limit error = %v", err)
	}
	if err := ensureKitDBRemoteJoinPairs(kitDBRemoteJoinPairLimit); err != nil {
		t.Fatalf("last allowed JOIN pair was rejected: %v", err)
	}
	if err := ensureKitDBRemoteJoinPairs(kitDBRemoteJoinPairLimit + 1); err == nil ||
		!strings.Contains(err.Error(), "20000") {
		t.Fatalf("JOIN pair limit error = %v", err)
	}
	if got := kitDBRemoteJoinResultLimit(kitSQLStatement{}); got != query.DefaultDBLimit {
		t.Fatalf("default JOIN result limit = %d, want %d", got, query.DefaultDBLimit)
	}
	if got := kitDBRemoteJoinResultLimit(kitSQLStatement{
		hasLimit: true, limit: kitDBRemoteSelectRowLimit + 100,
	}); got != kitDBRemoteSelectRowLimit {
		t.Fatalf("hard JOIN result limit = %d, want %d", got, kitDBRemoteSelectRowLimit)
	}
}
