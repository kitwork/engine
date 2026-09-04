package relational

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type errAfterContext struct {
	context.Context
	remaining int
}

func (ctx *errAfterContext) Err() error {
	if ctx.remaining == 0 {
		return context.Canceled
	}
	ctx.remaining--
	return nil
}

func TestStandaloneReindexBuildsOrderedPrimaryAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reindex.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			merchant TEXT NOT NULL,
			id BIGINT NOT NULL,
			name TEXT,
			PRIMARY KEY (merchant, id)
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (merchant, id, name) VALUES
		('tiki', 2, 'two'), ('shopee', 3, 'three'),
		('tiki', 1, 'one'), ('lazada', 4, 'four')
	`); err != nil {
		t.Fatal(err)
	}
	schema, index := testImplicitPrimaryIndex(t, engine, "products")
	removeTestIndex(t, engine, schema, index)

	before := testSelectAccess(t, engine, `SELECT * FROM products ORDER BY merchant, id LIMIT 2`)
	if before.kind != rowAccessScan {
		t.Fatalf("access before REINDEX = %#v", before)
	}
	result, err := engine.Execute(ctx, `REINDEX TABLE public.products`)
	if err != nil {
		t.Fatal(err)
	}
	if result.CommandTag != "REINDEX" {
		t.Fatalf("REINDEX result = %#v", result)
	}
	after := testSelectAccess(t, engine, `SELECT * FROM products ORDER BY merchant, id LIMIT 2`)
	if after.kind != rowAccessSecondary || after.name != "products_pkey" || !after.orderCovered {
		t.Fatalf("access after REINDEX = %#v", after)
	}
	selected, err := engine.Execute(ctx, `SELECT merchant, id FROM products ORDER BY merchant, id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Rows) != 4 || selected.Rows[0][0] != "lazada" ||
		selected.Rows[1][0] != "shopee" || selected.Rows[2][1] != int64(1) {
		t.Fatalf("ordered rows = %#v", selected.Rows)
	}
	stateKey, _ := reindexStateKey(schema)
	if _, found, err := engine.database.Get(stateKey); err != nil || found {
		t.Fatalf("REINDEX state remains: found=%t err=%v", found, err)
	}
	progressKey, _ := indexBuildProgressKey(schema, index)
	if _, found, err := engine.database.Get(progressKey); err != nil || found {
		t.Fatalf("index progress remains: found=%t err=%v", found, err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if access := testSelectAccess(t, engine, `SELECT * FROM products ORDER BY merchant, id LIMIT 2`); access.kind != rowAccessSecondary || !access.orderCovered {
		t.Fatalf("reopened access = %#v", access)
	}
}

func TestStandaloneReindexResumesAfterCancellation(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "resume.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			merchant TEXT NOT NULL,
			id BIGINT NOT NULL,
			PRIMARY KEY (merchant, id)
		)
	`); err != nil {
		t.Fatal(err)
	}
	for first := 1; first <= 6_000; first += 500 {
		var source strings.Builder
		source.WriteString(`INSERT INTO products (merchant, id) VALUES `)
		for id := first; id < first+500; id++ {
			if id != first {
				source.WriteByte(',')
			}
			fmt.Fprintf(&source, "('shop',%d)", id)
		}
		if _, err := engine.Execute(ctx, source.String()); err != nil {
			t.Fatal(err)
		}
	}
	schema, index := testImplicitPrimaryIndex(t, engine, "products")
	removeTestIndex(t, engine, schema, index)
	canceling := &errAfterContext{Context: context.Background(), remaining: 44}
	if _, err := engine.Execute(canceling, `REINDEX TABLE products`); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted REINDEX error = %v", err)
	}
	progressKey, _ := indexBuildProgressKey(schema, index)
	progress, found, err := readIndexBuildProgress(engine.database, progressKey)
	if err != nil || !found || len(progress.LastRowKey) == 0 {
		t.Fatalf("durable progress = %#v, found=%t, err=%v", progress, found, err)
	}
	if ready, err := indexIsReady(engine.database, schema, index); err != nil || ready {
		t.Fatalf("interrupted index ready=%t err=%v", ready, err)
	}
	if _, err := engine.Execute(ctx, `REINDEX TABLE products`); err != nil {
		t.Fatal(err)
	}
	if ready, err := indexIsReady(engine.database, schema, index); err != nil || !ready {
		t.Fatalf("resumed index ready=%t err=%v", ready, err)
	}
	selected, err := engine.Execute(ctx, `SELECT id FROM products ORDER BY merchant, id LIMIT 3`)
	if err != nil || len(selected.Rows) != 3 || selected.Rows[0][0] != int64(1) || selected.Rows[2][0] != int64(3) {
		t.Fatalf("resumed rows = %#v, err=%v", selected.Rows, err)
	}
}

func TestStandaloneReindexAdoptsCompleteUnreadyIndex(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "adopt.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			merchant TEXT NOT NULL,
			id BIGINT NOT NULL,
			PRIMARY KEY (merchant, id)
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (merchant, id) VALUES
		('tiki', 2), ('shopee', 3), ('tiki', 1), ('lazada', 4)
	`); err != nil {
		t.Fatal(err)
	}
	schema, index := testImplicitPrimaryIndex(t, engine, "products")
	markTestIndexUnready(t, engine, schema, index)
	before, err := engine.database.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `REINDEX TABLE products`); err != nil {
		t.Fatal(err)
	}
	after, err := engine.database.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if after.MainGeneration != before.MainGeneration {
		t.Fatalf("complete index was rebuilt: generation %d -> %d", before.MainGeneration, after.MainGeneration)
	}
	if ready, err := indexIsReady(engine.database, schema, index); err != nil || !ready {
		t.Fatalf("adopted index ready=%t err=%v", ready, err)
	}
}

func testImplicitPrimaryIndex(t *testing.T, engine *Engine, table string) (kitdbsql.Schema, secondaryIndex) {
	t.Helper()
	engine.mu.RLock()
	schema, err := engine.schemaLocked(table)
	engine.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		if index.implicit {
			return schema, index
		}
	}
	t.Fatal("implicit primary index was not declared")
	return kitdbsql.Schema{}, secondaryIndex{}
}

func removeTestIndex(t *testing.T, engine *Engine, schema kitdbsql.Schema, index secondaryIndex) {
	t.Helper()
	prefix, err := secondaryIndexBasePrefix(schema, index)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.deletePrefixBatched(context.Background(), prefix); err != nil {
		t.Fatal(err)
	}
	markTestIndexUnready(t, engine, schema, index)
}

func markTestIndexUnready(t *testing.T, engine *Engine, schema kitdbsql.Schema, index secondaryIndex) {
	t.Helper()
	readyKey, err := indexReadyKey(schema, index)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := engine.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Delete(readyKey); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func testSelectAccess(t *testing.T, engine *Engine, source string) rowAccess {
	t.Helper()
	statement, err := kitdbsql.ParseStatement(source)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := engine.BeginTransaction(context.Background(), TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	schema, err := transaction.schema(statement.Select.Table)
	if err != nil {
		t.Fatal(err)
	}
	conditions, err := bindConditions(schema, statement.Select.Conditions, nil)
	if err != nil {
		t.Fatal(err)
	}
	orders, err := bindOrders(schema, statement.Select.Order)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		t.Fatal(err)
	}
	access, err := transaction.planRowAccess(schema, generation, conditions, orders)
	if err != nil {
		t.Fatal(err)
	}
	return access
}
