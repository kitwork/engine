package relational

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStandalonePredicatesAcrossReadAndMutation(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "predicates.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			status TEXT NOT NULL,
			active BOOLEAN NOT NULL,
			note TEXT
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (id, title, status, active, note) VALUES
		(1, 'Mechanical Keyboard', 'active', true, NULL),
		(2, 'Wireless Mouse', 'disabled', false, 'clearance'),
		(3, 'Keyboard Cover', 'disabled', true, 'accessory'),
		(4, 'USB Cable', 'active', true, NULL)
	`); err != nil {
		t.Fatal(err)
	}

	selected, err := engine.Execute(ctx, `
		SELECT id FROM products
		WHERE (status = 'active' OR title ILIKE '%keyboard%')
		  AND id IN (1, 3, 4)
		  AND active = true
		ORDER BY id
	`)
	if err != nil {
		t.Fatal(err)
	}
	assertIntegerColumn(t, selected.Rows, 1, 3, 4)

	notIn, err := engine.Execute(ctx, `SELECT id FROM products WHERE id NOT IN (2, NULL) ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(notIn.Rows) != 0 {
		t.Fatalf("NOT IN with NULL rows = %#v", notIn.Rows)
	}

	updated, err := engine.Execute(ctx, `
		UPDATE products SET status = 'review'
		WHERE id = 2 OR title LIKE 'Keyboard%'
		RETURNING id
	`)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Affected != 2 {
		t.Fatalf("UPDATE affected = %d rows=%#v", updated.Affected, updated.Rows)
	}

	deleted, err := engine.Execute(ctx, `
		DELETE FROM products WHERE title LIKE '%Cable' OR note IS NOT NULL RETURNING id
	`)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Affected != 3 {
		t.Fatalf("DELETE affected = %d rows=%#v", deleted.Affected, deleted.Rows)
	}
	remaining, err := engine.Execute(ctx, `SELECT id FROM products`)
	if err != nil {
		t.Fatal(err)
	}
	assertIntegerColumn(t, remaining.Rows, 1)
}

func TestStandaloneJoinUsesQualifiedPredicateTree(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "join-predicates.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER, status TEXT NOT NULL)`,
		`INSERT INTO customers (id, name) VALUES (1, 'Acme'), (2, 'Beta')`,
		`INSERT INTO orders (id, customer_id, status) VALUES (10, 1, 'active'), (11, 2, 'disabled'), (12, NULL, 'active')`,
	} {
		if _, err := engine.Execute(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	result, err := engine.Execute(ctx, `
		SELECT o.id, c.name
		FROM orders o LEFT JOIN customers c ON c.id = o.customer_id
		WHERE c.name ILIKE 'ac%' OR (o.status = 'active' AND c.id IS NULL)
		ORDER BY o.id
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 2 || result.Rows[0][0] != int64(10) || result.Rows[0][1] != "Acme" ||
		result.Rows[1][0] != int64(12) || result.Rows[1][1] != nil {
		t.Fatalf("JOIN predicate rows = %#v", result.Rows)
	}
	between, err := engine.Execute(ctx, `
		SELECT o.id, c.name
		FROM orders o LEFT JOIN customers c ON c.id = o.customer_id
		WHERE o.id BETWEEN $1 AND $2
		ORDER BY o.id
	`, int64(10), int64(11))
	if err != nil {
		t.Fatal(err)
	}
	if len(between.Rows) != 2 || between.Rows[0][0] != int64(10) || between.Rows[1][0] != int64(11) {
		t.Fatalf("JOIN BETWEEN rows = %#v", between.Rows)
	}
}

func TestStandaloneExplainFallsBackSafelyForOrPredicate(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "explain-predicate.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE products (id INTEGER PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `CREATE INDEX products_status_idx ON products (status)`); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(ctx, `EXPLAIN SELECT * FROM products WHERE status = 'active' OR id = 7`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 3 || result.Rows[0][1] != "sequential scan" {
		t.Fatalf("EXPLAIN rows = %#v", result.Rows)
	}
}

func TestStandaloneUpdateExpressionsUseOriginalRowAndRollback(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "update-expressions.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE counters (
			id INTEGER PRIMARY KEY,
			current INTEGER NOT NULL,
			previous INTEGER NOT NULL,
			label TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO counters (id, current, previous, label) VALUES
		(1, 5, 1, 'first'), (2, 10, 2, 'second')
	`); err != nil {
		t.Fatal(err)
	}

	updated, err := engine.Execute(ctx, `
		UPDATE counters
		SET current = current + 2 * $1,
		    previous = current,
		    label = label || '-updated'
		WHERE id = 1
		RETURNING current, previous, label
	`, int64(3))
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Rows) != 1 || updated.Rows[0][0] != int64(11) ||
		updated.Rows[0][1] != int64(5) || updated.Rows[0][2] != "first-updated" {
		t.Fatalf("UPDATE expression rows = %#v", updated.Rows)
	}

	if _, err := engine.Execute(ctx, `UPDATE counters SET current = current / (id - id) WHERE id >= 1`); err == nil {
		t.Fatal("division-by-zero UPDATE unexpectedly committed")
	}
	unchanged, err := engine.Execute(ctx, `SELECT id, current FROM counters ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Rows) != 2 || unchanged.Rows[0][1] != int64(11) || unchanged.Rows[1][1] != int64(10) {
		t.Fatalf("rows after failed UPDATE = %#v", unchanged.Rows)
	}

	if _, err := engine.Execute(ctx, `UPDATE counters SET current = 9223372036854775807 + 1 WHERE id = 1`); err == nil {
		t.Fatal("overflowing UPDATE unexpectedly committed")
	}
}

func TestStandaloneScalarProjectionExpressionsAndDescribe(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "select-expressions.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			price FLOAT NOT NULL,
			quantity INTEGER NOT NULL,
			note TEXT
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (id, title, price, quantity, note) VALUES
		(1, 'Keyboard', 10, 2, NULL),
		(2, 'Mouse', 30, 1, 'sale'),
		(3, 'Cable', 5, 3, NULL),
		(4, 'Stand', 20, 3, 'large')
	`); err != nil {
		t.Fatal(err)
	}
	query := `
		SELECT id,
		       UPPER(title) AS title,
		       price * quantity AS total,
		       COALESCE(note, 'none') note,
		       LENGTH(title) AS title_length
		FROM products
		WHERE price * quantity BETWEEN $1 AND $2
		ORDER BY total DESC, id
		LIMIT 3
	`
	columns, err := engine.Describe(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 5 || columns[0].Kind != "int32" || columns[1].Kind != "text" ||
		columns[2].Kind != "float" || columns[4].Kind != "int32" {
		t.Fatalf("Describe columns = %#v", columns)
	}
	result, err := engine.Execute(ctx, query, float64(15), float64(60))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("expression rows = %#v", result.Rows)
	}
	want := [][]any{
		{int64(4), "STAND", float64(60), "large", int64(5)},
		{int64(2), "MOUSE", float64(30), "sale", int64(5)},
		{int64(1), "KEYBOARD", float64(20), "none", int64(8)},
	}
	for rowIndex := range want {
		for columnIndex := range want[rowIndex] {
			if result.Rows[rowIndex][columnIndex] != want[rowIndex][columnIndex] {
				t.Fatalf(
					"row %d column %d = %#v (%T), want %#v (%T)",
					rowIndex, columnIndex, result.Rows[rowIndex][columnIndex], result.Rows[rowIndex][columnIndex],
					want[rowIndex][columnIndex], want[rowIndex][columnIndex],
				)
			}
		}
	}

	filtered, err := engine.Execute(ctx, `
		SELECT id, LOWER(title) AS normalized
		FROM products
		WHERE LOWER(title) = 'keyboard' OR id + 1 IN (4, 9)
		ORDER BY id
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Rows) != 2 || filtered.Rows[0][0] != int64(1) || filtered.Rows[1][0] != int64(3) {
		t.Fatalf("expression predicate rows = %#v", filtered.Rows)
	}
}

func TestStandaloneScalarExpressionIntermediateRowsAreBounded(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "bounded-expressions.kitdb"), Options{
		MaximumResultRows: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE values_table (id INTEGER PRIMARY KEY, value INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO values_table (id, value) VALUES (1, 3), (2, 2), (3, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `SELECT id + 1 AS next_id FROM values_table`); err == nil {
		t.Fatal("unbounded scalar SELECT silently truncated")
	}
	limited, err := engine.Execute(ctx, `SELECT id + 1 AS next_id FROM values_table LIMIT 2`)
	if err != nil || len(limited.Rows) != 2 {
		t.Fatalf("bounded scalar SELECT = %#v, %v", limited.Rows, err)
	}
	if _, err := engine.Execute(ctx, `SELECT id + 1 AS next_id FROM values_table ORDER BY value LIMIT 1`); err == nil {
		t.Fatal("ORDER BY intermediate set exceeded the configured bound without error")
	}
}

func assertIntegerColumn(t *testing.T, rows [][]any, want ...int64) {
	t.Helper()
	if len(rows) != len(want) {
		t.Fatalf("row count = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for index := range want {
		if len(rows[index]) != 1 || rows[index][0] != want[index] {
			t.Fatalf("row %d = %#v, want %d", index, rows[index], want[index])
		}
	}
}
