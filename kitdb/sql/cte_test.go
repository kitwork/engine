package sql

import (
	"strings"
	"testing"
)

func TestParseCommonTableExpressionsAndDerivedTable(t *testing.T) {
	statement, err := ParseStatement(`
		WITH expensive(id, label, price) AS (
			SELECT id, title, price FROM products WHERE price >= $1
		), labeled AS (
			SELECT id, UPPER(label) AS title, price FROM expensive WHERE label ILIKE $2
		)
		SELECT chosen.id, chosen.title
		FROM (SELECT id, title FROM labeled UNION ALL SELECT 99, 'fallback') AS chosen
		ORDER BY chosen.id DESC LIMIT 5
	`)
	if err != nil {
		t.Fatal(err)
	}
	plan := statement.Select
	if plan == nil || len(plan.CommonTables) != 2 || plan.CommonTables[0].Name != "expensive" {
		t.Fatalf("WITH plan = %#v", plan)
	}
	if len(plan.CommonTables[0].Columns) != 3 || plan.CommonTables[1].Select.Table != "expensive" {
		t.Fatalf("common tables = %#v", plan.CommonTables)
	}
	if plan.Source == nil || plan.TableAlias != "chosen" || len(plan.Source.SetOperations) != 1 {
		t.Fatalf("derived source = %#v", plan.Source)
	}
	if plan.CommonTables[0].Select.Predicate.Arguments[1].Literal.Parameter != 1 ||
		plan.CommonTables[1].Select.Predicate.Arguments[1].Literal.Parameter != 2 {
		t.Fatalf("nested parameters were not retained")
	}
}

func TestParseCommonTableExpressionBoundsAndUnsupportedShapes(t *testing.T) {
	for _, test := range []struct {
		source string
		want   string
	}{
		{`WITH RECURSIVE values AS (SELECT 1) SELECT * FROM values`, "WITH RECURSIVE"},
		{`WITH one AS (SELECT 1), one AS (SELECT 2) SELECT * FROM one`, "repeats common table"},
		{`SELECT * FROM (SELECT 1)`, "requires an alias"},
		{`WITH found AS (SELECT * FROM products WHERE * SEARCH 'keyboard') SELECT * FROM found`, "cannot be materialized"},
		{`WITH one(a, a) AS (SELECT 1, 2) SELECT * FROM one`, "field list repeats"},
	} {
		if _, err := ParseStatement(test.source); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("ParseStatement(%q) error = %v, want %q", test.source, err, test.want)
		}
	}

	tooMany := "WITH "
	for index := 0; index <= MaximumCommonTables; index++ {
		if index != 0 {
			tooMany += ", "
		}
		tooMany += "c" + strings.Repeat("x", index) + " AS (SELECT 1)"
	}
	tooMany += " SELECT 1"
	if _, err := ParseStatement(tooMany); err == nil || !strings.Contains(err.Error(), "common table expressions") {
		t.Fatalf("CTE count error = %v", err)
	}

	tooDeep := "SELECT 1 AS id"
	for index := 0; index <= MaximumQueryNesting; index++ {
		tooDeep = "SELECT * FROM (" + tooDeep + ") AS nested"
	}
	if _, err := ParseStatement(tooDeep); err == nil || !strings.Contains(err.Error(), "query nesting") {
		t.Fatalf("query nesting error = %v", err)
	}
}
