package relational

import (
	"context"
	"path/filepath"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestSelectSourceRowCapacity(t *testing.T) {
	ctx := context.Background()
	engine, err := Open(filepath.Join(t.TempDir(), "capacity.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, query := range []string{
		"CREATE TABLE items(id BIGINT PRIMARY KEY, email TEXT UNIQUE, n BIGINT NOT NULL)",
		"INSERT INTO items VALUES(1,'one',30),(2,'two',10),(3,'three',20)",
	} {
		if _, err := engine.Execute(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	schema, err := tx.schema("items")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := activeRowGeneration(tx, schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, path     string
		rows, capacity, total int
		count                 bool
		ids                   []int64
	}{
		{"primary", "SELECT id FROM items WHERE id=2", "primary-lookup", 1, 1, 1, false, []int64{2}},
		{"primary_missing", "SELECT id FROM items WHERE id=99", "primary-lookup", 0, 1, 0, false, nil},
		{"unique", "SELECT id FROM items WHERE email='two'", "unique-lookup", 1, 1, 1, false, []int64{2}},
		{"count", "SELECT COUNT(*) FROM items WHERE n>=0", "sequential-scan", 0, 0, 3, true, nil},
		{"range_limit", "SELECT id FROM items WHERE n>=0 LIMIT 2", "sequential-scan", 2, 2, 2, false, nil},
		{"top_n", "SELECT id FROM items WHERE n>=0 ORDER BY n LIMIT 1", "sequential-scan", 1, 1, 3, false, []int64{2}},
		{"offset", "SELECT id FROM items WHERE id=2 OFFSET 1", "primary-lookup", 0, 1, 1, false, nil},
		{"zero_limit", "SELECT id FROM items WHERE id=2 LIMIT 0", "primary-lookup", 0, 0, 0, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statement, err := kitdbsql.ParseStatement(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			plan := statement.Select
			conditions, err := bindConditions(schema, plan.Conditions, nil)
			if err != nil {
				t.Fatal(err)
			}
			predicate, err := bindPredicate(schema, plan.Predicate, nil)
			if err != nil {
				t.Fatal(err)
			}
			orders, err := bindOrders(schema, plan.Order)
			if err != nil {
				t.Fatal(err)
			}
			limit := engine.maximumResultRows
			if plan.HasLimit {
				limit = plan.Limit
			}
			rows, total, overflow, stats, err := tx.readMatchingRows(ctx, schema, generation, conditions, predicate, orders, plan.Offset, limit, plan.HasLimit, tc.count, plan.Distinct, true, nil, []string{"id", "n"})
			if err != nil || overflow || stats == nil || stats.Path != tc.path || len(rows) != tc.rows || cap(rows) != tc.capacity || total != tc.total {
				t.Fatalf("rows=%d cap=%d total=%d stats=%+v overflow=%t err=%v", len(rows), cap(rows), total, stats, overflow, err)
			}
			for i, id := range tc.ids {
				if rows[i]["id"] != id {
					t.Fatalf("row=%v want=%d", rows[i], id)
				}
			}
		})
	}
}
