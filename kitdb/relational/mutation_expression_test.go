package relational

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestMutationExpressionValuesAndReturning(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(q string, args ...any) Result { return functionTestExecute(t, e, q, args...) }
	run(`CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`)
	run(`CREATE FUNCTION add_tax(n NUMERIC) RETURNS NUMERIC LANGUAGE SQL RETURN n + 0.25`)
	run(`CREATE TABLE items (id SERIAL PRIMARY KEY, label TEXT DEFAULT 'fallback', amount NUMERIC(20,2), quantity INTEGER DEFAULT 2)`)
	result := run(`INSERT INTO items (label,amount,quantity) VALUES
		(clean($1), add_tax(9007199254740993.25), DEFAULT),
		(clean(NULL), CAST($2 AS NUMERIC) * 2, 1 + 2)
		RETURNING *, clean(label) AS normalized, add_tax(amount) AS taxed, quantity + $3 AS extra`, " ab ", "3.50", 4)
	want := [][]any{
		{int64(1), "AB", "9007199254740993.5", int64(2), "AB", "9007199254740993.75", int64(6)},
		{int64(2), nil, "7", int64(3), nil, "7.25", int64(7)},
	}
	if !reflect.DeepEqual(result.Rows, want) || result.CommandTag != "INSERT 0 2" {
		t.Fatalf("insert = %#v", result)
	}
	result = run(`UPDATE items SET label = clean(' cd '), amount = add_tax(amount) WHERE id = 2 RETURNING id, clean(label), amount * 2 AS doubled`)
	if fmt.Sprint(result.Rows) != "[[2 CD 14.5]]" {
		t.Fatalf("update = %#v", result)
	}
	result = run(`DELETE FROM items WHERE id = 2 RETURNING items.*, clean(label), amount + 1 AS total`)
	if fmt.Sprint(result.Rows) != "[[2 CD 7.25 3 CD 8.25]]" {
		t.Fatalf("delete = %#v", result)
	}
	result = run(`INSERT INTO items DEFAULT VALUES RETURNING id, clean(label), quantity + 1`)
	if fmt.Sprint(result.Rows) != "[[3 FALLBACK 3]]" {
		t.Fatalf("defaults = %#v", result)
	}
	for _, q := range []string{
		`UPDATE items SET label = 'none' WHERE id = 999 RETURNING clean(label) AS normalized, amount + 1 AS total`,
		`DELETE FROM items WHERE id = 999 RETURNING clean(label) AS normalized, amount + 1 AS total`,
	} {
		columns, err := e.Describe(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		result = run(q)
		if len(result.Rows) != 0 || len(columns) != 2 || !reflect.DeepEqual(columns, result.Columns) {
			t.Fatalf("empty RETURNING = %#v, describe = %#v", result, columns)
		}
	}
}

func TestMutationExpressionReturningRollback(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(q string) Result { return functionTestExecute(t, e, q) }
	run(`CREATE TABLE items (id INTEGER PRIMARY KEY, quantity INTEGER NOT NULL)`)
	run(`CREATE TABLE audit (id SERIAL PRIMARY KEY, source_id INTEGER, event TEXT)`)
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		row := "NEW"
		if event == "DELETE" {
			row = "OLD"
		}
		run(fmt.Sprintf(`CREATE TRIGGER audit_%s AFTER %s ON items FOR EACH ROW INSERT INTO audit (source_id,event) VALUES (%s.id,'%s')`, event, event, row, event))
	}
	run(`INSERT INTO items VALUES (1,10),(2,0)`)
	queries := []string{
		`INSERT INTO items VALUES (3,10),(4,0) RETURNING id, 10 / quantity`,
		`UPDATE items SET quantity = quantity + 1 RETURNING id, 10 / (quantity - 1)`,
		`DELETE FROM items RETURNING id, 10 / quantity`,
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			if result, err := e.Execute(context.Background(), query); err == nil || !strings.Contains(err.Error(), "RETURNING row 2") || len(result.Rows) != 0 {
				t.Fatalf("expected late RETURNING error and no result, got %#v, %v", result, err)
			}
			if got := run(`SELECT * FROM items ORDER BY id`).Rows; fmt.Sprint(got) != "[[1 10] [2 0]]" {
				t.Fatal(got)
			}
			triggerTestCount(t, e, "audit", 2)
			tx, err := e.BeginTransaction(context.Background(), TransactionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Execute(context.Background(), `INSERT INTO items VALUES (9,9)`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Execute(context.Background(), query); err == nil {
				t.Fatal("failed statement succeeded")
			}
			got, err := tx.Execute(context.Background(), `SELECT * FROM items ORDER BY id`)
			if err != nil || fmt.Sprint(got.Rows) != "[[1 10] [2 0] [9 9]]" {
				t.Fatalf("savepoint: %#v %v", got, err)
			}
			got, err = tx.Execute(context.Background(), `SELECT COUNT(*) FROM audit`)
			if err != nil || fmt.Sprint(got.Rows) != "[[3]]" {
				t.Fatalf("audit savepoint: %#v %v", got, err)
			}
		})
	}
}

func TestMutationExpressionValidationAndTriggerFailure(t *testing.T) {
	e := triggerTestOpen(t)
	run := func(q string) Result { return functionTestExecute(t, e, q) }
	run(`CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`)
	run(`CREATE DOMAIN positive AS INTEGER CHECK (VALUE > 0)`)
	run(`CREATE TABLE items (id INTEGER PRIMARY KEY, label TEXT UNIQUE NOT NULL, quantity positive)`)
	run(`CREATE TABLE audit (id SERIAL PRIMARY KEY, source_id INTEGER, label TEXT CHECK (label <> 'FAIL'))`)
	run(`CREATE TRIGGER inserted AFTER INSERT ON items FOR EACH ROW INSERT INTO audit (source_id,label) VALUES (NEW.id,NEW.label)`)
	queries := []string{
		`INSERT INTO items VALUES (1,clean('a'),1),(2,clean('b'),1 / 0)`,
		`INSERT INTO items VALUES (1,clean('a'),1),(2,clean('a'),2)`,
		`INSERT INTO items VALUES (1,clean('a'),1),(2,clean(NULL),2)`,
		`INSERT INTO items VALUES (1,clean('a'),1),(2,clean('b'),0 - 1)`,
		`INSERT INTO items VALUES (1,clean('a'),1),(2,clean('fail'),2) RETURNING clean(label)`,
		`INSERT INTO items VALUES (1,label,1)`,
		`INSERT INTO items VALUES (1,clean($1),1)`,
		`INSERT INTO items VALUES (1,clean('a'),1) RETURNING unknown_fn(id)`,
		`INSERT INTO items VALUES (1,clean('a'),1) RETURNING SUM(quantity)`,
		`UPDATE items SET quantity = 2 WHERE id = 999 RETURNING clean(missing)`,
		`DELETE FROM items WHERE id = 999 RETURNING clean()`,
		`DELETE FROM items WHERE id = 999 RETURNING id + $1`,
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			triggerTestFail(t, e, q, "")
			triggerTestCount(t, e, "items", 0)
			triggerTestCount(t, e, "audit", 0)
		})
	}
	rows := make([]string, 65)
	for i := range rows {
		rows[i] = fmt.Sprintf("(%d,clean('%d'),1)", i, i)
	}
	triggerTestFail(t, e, "INSERT INTO items VALUES "+strings.Join(rows, ","), "64 user function")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Execute(ctx, `INSERT INTO items VALUES (1,clean('a'),1) RETURNING clean(label)`); err == nil {
		t.Fatal("cancel ignored")
	}
	run(`INSERT INTO items VALUES (1,clean('a'),coalesce(2,1 / 0)) RETURNING clean(label)`)
	triggerTestCount(t, e, "audit", 1)
	path := e.database.Path()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	result := functionTestExecute(t, e, `INSERT INTO items VALUES (2,clean('b'),2) RETURNING id, clean(label)`)
	if fmt.Sprint(result.Rows) != "[[2 B]]" {
		t.Fatal(result.Rows)
	}
	triggerTestCount(t, e, "audit", 2)
}

func TestMutationExpressionTypedPlanAndLimits(t *testing.T) {
	e := triggerTestOpen(t)
	functionTestExecute(t, e, `CREATE FUNCTION clean(s TEXT) RETURNS TEXT LANGUAGE SQL RETURN upper(trim(s))`)
	functionTestExecute(t, e, `CREATE TABLE items (id INTEGER PRIMARY KEY, label TEXT)`)
	number := func(n string) kitdbsql.ExpressionPlan {
		return kitdbsql.ExpressionPlan{Kind: "literal", Literal: kitdbsql.Literal{Kind: kitdbsql.LiteralNumber, Text: n}}
	}
	text := func(s string) kitdbsql.ExpressionPlan {
		return kitdbsql.ExpressionPlan{Kind: "literal", Literal: kitdbsql.Literal{Kind: kitdbsql.LiteralString, Text: s}}
	}
	value := kitdbsql.ExpressionPlan{Kind: "function", Operator: "clean", Arguments: []kitdbsql.ExpressionPlan{text(" typed ")}}
	returned := kitdbsql.ExpressionPlan{Kind: "function", Operator: "clean", Arguments: []kitdbsql.ExpressionPlan{{Kind: "field", Field: "label"}}}
	plan := &kitdbsql.ParsedStatement{Kind: kitdbsql.StatementInsert, Insert: &kitdbsql.InsertStatement{
		Table: "items", Values: [][]kitdbsql.ExpressionPlan{{number("1"), value}}, Returning: []kitdbsql.Projection{{Expression: &returned, Alias: "normalized"}},
	}}
	result, err := e.ExecutePlan(context.Background(), plan)
	if err != nil || fmt.Sprint(result.Rows) != "[[TYPED]]" {
		t.Fatalf("typed: %#v %v", result, err)
	}
	for _, bad := range []*kitdbsql.InsertStatement{
		nil,
		{Table: "items"},
		{Table: "items", DefaultRows: -1},
		{Table: "items", DefaultRows: kitdbsql.MaximumInsertRows + 1},
		{Table: "items", Values: [][]kitdbsql.ExpressionPlan{{number("2")}, {number("3"), text("a")}}},
		{Table: "items", Rows: [][]kitdbsql.Literal{{}, {number("2").Literal}}},
		{Table: "items", Columns: []string{"id"}, Values: [][]kitdbsql.ExpressionPlan{{number("2"), text("a")}}},
		{Table: "items", Rows: [][]kitdbsql.Literal{{number("2").Literal}}, Values: [][]kitdbsql.ExpressionPlan{{number("3")}}},
		{Table: "items", DefaultRows: 1, Values: [][]kitdbsql.ExpressionPlan{{number("2")}}},
	} {
		_, err := e.ExecutePlan(context.Background(), &kitdbsql.ParsedStatement{Kind: kitdbsql.StatementInsert, Insert: bad})
		if err == nil {
			t.Fatalf("accepted invalid plan: %#v", bad)
		}
	}
	triggerTestCount(t, e, "items", 1)
	deep := number("2")
	for range 25 {
		deep = kitdbsql.ExpressionPlan{Kind: "unary", Operator: "+", Arguments: []kitdbsql.ExpressionPlan{deep}}
	}
	_, err = e.ExecutePlan(context.Background(), &kitdbsql.ParsedStatement{Kind: kitdbsql.StatementInsert, Insert: &kitdbsql.InsertStatement{Table: "items", Values: [][]kitdbsql.ExpressionPlan{{deep, text("a")}}}})
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("unbounded typed expression: %v", err)
	}
	limited, err := OpenWithOptions(filepath.Join(t.TempDir(), "limited.kitdb"), Options{MaximumResultRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	functionTestExecute(t, limited, `CREATE TABLE items (id INTEGER PRIMARY KEY, quantity INTEGER DEFAULT 1)`)
	triggerTestFail(t, limited, `INSERT INTO items (id) VALUES (1 + 0),(2 + 0) RETURNING id + 1`, "result limit")
	triggerTestCount(t, limited, "items", 0)
	functionTestExecute(t, limited, `INSERT INTO items (id) VALUES (1 + 0),(2 + 0)`)
	triggerTestFail(t, limited, `UPDATE items SET quantity = quantity + 2 RETURNING id + 1`, "result limit")
	triggerTestFail(t, limited, `DELETE FROM items RETURNING id + 1`, "result limit")
	triggerTestCount(t, limited, "items", 2)
}
