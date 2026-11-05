package analytics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

// conditionSQL writes condition as SQLite's WHERE, its values as parameters.
func conditionSQL(condition *Condition, arguments *[]any) string {
	switch condition.Op {
	case OpAnd, OpOr:
		parts := make([]string, len(condition.Args))
		for position, argument := range condition.Args {
			parts[position] = conditionSQL(argument, arguments)
		}
		return "(" + strings.Join(parts, " "+strings.ToUpper(condition.Op)+" ") + ")"
	case OpNot:
		return "(NOT " + conditionSQL(condition.Args[0], arguments) + ")"
	case OpNull:
		return condition.Column + " IS NULL"
	case OpNotNull:
		return condition.Column + " IS NOT NULL"
	case OpIn:
		marks := make([]string, len(condition.Values))
		for position, value := range condition.Values {
			marks[position] = "?"
			*arguments = append(*arguments, value)
		}
		return condition.Column + " IN (" + strings.Join(marks, ", ") + ")"
	}
	*arguments = append(*arguments, condition.Value)
	return condition.Column + " " + condition.Op + " ?"
}

// querySQL writes query as SQLite's SELECT on orders.
func querySQL(query Query) (string, []any) {
	var arguments []any
	parts := append([]string(nil), query.Group...)
	for _, aggregate := range query.Aggregates {
		column := aggregate.Column
		if column == "" {
			column = "*"
		}
		parts = append(parts, aggregate.Function+"("+column+")")
	}
	text := "SELECT " + strings.Join(parts, ", ") + " FROM orders"
	if query.Filter != nil {
		text += " WHERE " + conditionSQL(query.Filter, &arguments)
	}
	if len(query.Group) > 0 {
		text += " GROUP BY " + strings.Join(query.Group, ", ") + " ORDER BY " + strings.Join(query.Group, ", ")
	}
	return text, arguments
}

// randomCondition is a condition over the columns of orders, depth at most depth.
func randomCondition(random *rand.Rand, depth int) *Condition {
	if depth > 0 && random.IntN(3) == 0 {
		switch random.IntN(3) {
		case 0:
			return &Condition{Op: OpNot, Args: []*Condition{randomCondition(random, depth-1)}}
		default:
			op := OpAnd
			if random.IntN(2) == 0 {
				op = OpOr
			}
			return &Condition{Op: op, Args: []*Condition{randomCondition(random, depth-1), randomCondition(random, depth-1)}}
		}
	}
	operators := []string{OpEqual, OpUnequal, OpLess, OpAtMost, OpMore, OpAtLeast, OpIn, OpNull, OpNotNull}
	op := operators[random.IntN(len(operators))]
	column := []string{"merchant", "amount", "price", "paid", "note"}[random.IntN(5)]
	value := func() any {
		switch column {
		case "merchant":
			return []string{"shopee", "lazada", "tiki", "sendo", "Bàn phím cơ", "日本", "", "zzz"}[random.IntN(8)]
		case "note":
			return fmt.Sprintf("ghi chú %d — tiki", 3*random.IntN(3000))
		case "paid":
			if random.IntN(4) == 0 {
				return 0.5
			}
			return int64(random.IntN(2))
		case "price":
			if random.IntN(3) == 0 {
				return int64(random.IntN(900) - 100) // an integer against a real column
			}
			return random.Float64()*1000 - 100
		}
		if random.IntN(3) == 0 {
			return float64(random.Int64N(2_000_000_000_000)-1_000_000_000_000) + 0.5 // a real against an integer column
		}
		return random.Int64N(2_000_000_000_000) - 1_000_000_000_000
	}
	condition := &Condition{Op: op, Column: column}
	switch op {
	case OpIn:
		for range 1 + random.IntN(3) {
			condition.Values = append(condition.Values, value())
		}
	case OpNull, OpNotNull:
	default:
		condition.Value = value()
	}
	return condition
}

// randomQuery is a query of orders: maybe a filter, maybe a group, one to three aggregates.
func randomQuery(random *rand.Rand) Query {
	var query Query
	if random.IntN(4) > 0 {
		query.Filter = randomCondition(random, 3)
	}
	query.Group = [][]string{nil, nil, {"merchant"}, {"paid"}, {"merchant", "paid"}, {"note"}}[random.IntN(6)]
	for range 1 + random.IntN(3) {
		function := []string{"count", "sum", "avg", "min", "max"}[random.IntN(5)]
		column := []string{"amount", "price", "paid", "merchant", "note"}[random.IntN(5)]
		if function == "count" && random.IntN(2) == 0 {
			column = ""
		}
		if (function == "sum" || function == "avg") && (column == "merchant" || column == "note") {
			column = "amount"
		}
		query.Aggregates = append(query.Aggregates, Aggregate{Function: function, Column: column})
	}
	return query
}

// sameValue compares a value of the copy with SQLite's: integers and texts exactly, reals within the last bits a
// different order of summing may change.
func sameValue(got, want any) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	if a, ok := got.(float64); ok {
		b, ok := want.(float64)
		if !ok {
			return false
		}
		return a == b || math.Abs(a-b) <= 1e-9*math.Max(math.Abs(a), math.Abs(b))
	}
	return got == want
}

// TestExecuteAnswersAsSQLite: hundreds of random queries (nested AND, OR and NOT, comparisons of integers with reals,
// NULLs, IN, the five aggregates, groups over nullable columns) answer what SQLite answers in the same read
// transaction, on a copy whose main part is behind the snapshot by inserts, updates and deletes.
//
// CONTROL (05/11/2026): with NOT of NULL taken as true, "query 0 SELECT sum(paid), … WHERE (NOT note = ?) …: row 0
// cell 0 is 1626, SQLite 1040"; with OR of NULL taken as false, "query 140 … WHERE (NOT (paid <= ? OR note IN (?, ?)))
// …: 8 rows, SQLite 7"; with a block skipped by its maximum for <, "query 27 … WHERE paid < ? GROUP BY note …: 113
// rows, SQLite 1071"; with the groups left unsorted, "query 1 … GROUP BY merchant, paid …: row 0 cell 0 is "mới",
// SQLite """; with the sum of no value 0, "query 27 …: row 4 cell 2 is 0, SQLite <nil>".
func TestExecuteAnswersAsSQLite(t *testing.T) {
	path, database := ordersFile(t, 4000)
	ctx := context.Background()
	if _, err := Build(ctx, readTransaction(t, path), Directory(path), "orders", viewColumns); err != nil {
		t.Fatal(err)
	}
	table, err := OpenTable(Directory(path), "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	write(t, database, `DELETE FROM orders WHERE id BETWEEN 3000 AND 3600`)
	write(t, database, `UPDATE orders SET amount = NULL, merchant = 'tiki' WHERE id % 11 = 0`)
	write(t, database, `INSERT INTO orders (id, merchant, amount, price, paid, note) VALUES (500000, 'mới', 1, 2.5, 1, NULL)`)
	tx := readTransaction(t, path)
	view, err := table.View(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Changed() == 0 {
		t.Fatal("the view holds no changed row")
	}
	random := rand.New(rand.NewPCG(11, 5))
	answered := 0
	for attempt := range 400 {
		query := randomQuery(random)
		text, arguments := querySQL(query)
		want := cellsWith(t, tx, text, arguments)
		got, err := view.Execute(ctx, query)
		if errors.Is(err, ErrNotAnswered) {
			continue
		}
		if err != nil {
			t.Fatalf("query %d %s %v: %v", attempt, text, arguments, err)
		}
		answered++
		if len(got.Rows) != len(want) {
			t.Fatalf("query %d %s %v: %d rows, SQLite %d", attempt, text, arguments, len(got.Rows), len(want))
		}
		for position := range want {
			for cell := range want[position] {
				if !sameValue(got.Rows[position][cell], want[position][cell]) {
					t.Fatalf("query %d %s %v: row %d cell %d is %#v, SQLite %#v", attempt, text, arguments, position, cell,
						got.Rows[position][cell], want[position][cell])
				}
			}
		}
	}
	if answered < 300 {
		t.Fatalf("only %d of 400 queries answered", answered)
	}
}

// TestExecuteTakesStatisticsAndRefuses: a count, an integer sum, a minimum and a maximum of the whole table come
// from the blocks' statistics but for the blocks holding a changed row; an overflowing sum is ErrOverflow, as
// SQLite's "integer overflow"; a text column compared with a number is not answered.
//
// CONTROL (05/11/2026): with the statistics of a block holding a changed row taken, "SELECT count(*), … FROM orders:
// cell 0 is 4424, SQLite 3400"; with an overflow not seen, "an overflowing sum: <nil>, want ErrOverflow".
func TestExecuteTakesStatisticsAndRefuses(t *testing.T) {
	path, database := ordersFile(t, 4000)
	ctx := context.Background()
	if _, err := Build(ctx, readTransaction(t, path), Directory(path), "orders", viewColumns); err != nil {
		t.Fatal(err)
	}
	table, err := OpenTable(Directory(path), "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	write(t, database, `UPDATE orders SET amount = 999999999999999 WHERE id = 3003`)
	write(t, database, `DELETE FROM orders WHERE id BETWEEN 6000 AND 6100`)
	tx := readTransaction(t, path)
	view, err := table.View(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	query := Query{Aggregates: []Aggregate{{"count", ""}, {"count", "amount"}, {"sum", "amount"}, {"min", "amount"}, {"max", "amount"}, {"sum", "paid"}}}
	got, err := view.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := querySQL(query)
	want := cells(t, tx, text)
	for cell := range want[0] {
		if !sameValue(got.Rows[0][cell], want[0][cell]) {
			t.Fatalf("%s: cell %d is %#v, SQLite %#v", text, cell, got.Rows[0][cell], want[0][cell])
		}
	}
	write(t, database, `UPDATE orders SET amount = 9000000000000000000 WHERE id IN (3, 6)`)
	tx = readTransaction(t, path)
	if view, err = table.View(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if _, err := view.Execute(ctx, Query{Aggregates: []Aggregate{{"sum", "amount"}}}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("an overflowing sum: %v, want ErrOverflow", err)
	}
	var total any
	sqliteErr := tx.QueryRow(`SELECT sum(amount) FROM orders`).Scan(&total) // modernc reports it as the first step runs
	if sqliteErr == nil || !strings.Contains(sqliteErr.Error(), "integer overflow") {
		t.Fatalf("SQLite's sum: %v, want integer overflow", sqliteErr)
	}
	if _, err := view.Execute(ctx, Query{Filter: &Condition{Op: OpEqual, Column: "merchant", Value: int64(1)},
		Aggregates: []Aggregate{{"count", ""}}}); !errors.Is(err, ErrNotAnswered) {
		t.Fatalf("a text column compared with a number: %v, want ErrNotAnswered", err)
	}
}

// cellsWith reads the rows of a SELECT with its arguments.
func cellsWith(t *testing.T, tx *sql.Tx, query string, arguments []any) [][]any {
	t.Helper()
	rows, err := tx.Query(query, arguments...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	names, _ := rows.Columns()
	var all [][]any
	for rows.Next() {
		values := make([]any, len(names))
		pointers := make([]any, len(names))
		for position := range values {
			pointers[position] = &values[position]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		all = append(all, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return all
}

// TestCompareIntegerWithRealExactly: an integer past 2^53 against a real orders as SQLite orders it, exactly, not
// through the integer rounded to a real.
//
// CONTROL (05/11/2026): with the two compared as reals, "9007199254740993 against 9.007199254740992e+15: 0, SQLite
// 1".
func TestCompareIntegerWithRealExactly(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, pair := range []struct {
		integer int64
		real    float64
	}{
		{9007199254740993, 9007199254740992.0}, {9007199254740991, 9007199254740992.0}, {-9007199254740993, -9007199254740992.0},
		{math.MaxInt64, 9223372036854775807.0}, {math.MinInt64, -9223372036854775808.0}, {5, 5.5}, {6, 5.5}, {-6, -5.5}, {0, -0.0},
	} {
		var sign int
		if err := db.QueryRow(`SELECT CASE WHEN ? < ? THEN -1 WHEN ? > ? THEN 1 ELSE 0 END`, pair.integer, pair.real,
			pair.integer, pair.real).Scan(&sign); err != nil {
			t.Fatal(err)
		}
		if got := compareIntegerReal(pair.integer, pair.real); got != sign {
			t.Fatalf("%d against %v: %d, SQLite %d", pair.integer, pair.real, got, sign)
		}
	}
}
