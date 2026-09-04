package relational

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	_ "github.com/lib/pq"
)

func TestStandalonePostgresWireCreateInsertSelectAndCatalog(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "shop.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.ServePostgres(ctx, listener, PostgresServerOptions{
			PostgresOptions: PostgresOptions{
				Database: "shop", User: "kitdb", Password: "standalone-secret",
			},
			MaxConnections: 8,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case serveErr := <-done:
			if serveErr != nil {
				t.Errorf("ServePostgres: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServePostgres did not stop")
		}
		if err := engine.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	dsn := fmt.Sprintf(
		"postgres://kitdb:standalone-secret@%s/shop?sslmode=disable",
		listener.Addr().String(),
	)
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE products (
			merchant TEXT NOT NULL,
			id INTEGER NOT NULL,
			title TEXT NOT NULL UNIQUE,
			PRIMARY KEY (merchant, id)
		)
	`); err != nil {
		t.Fatal(err)
	}
	inserted, err := database.Exec(
		`INSERT INTO products (merchant, id, title) VALUES ($1, $2, $3)`,
		"shopee", int64(42), "Keyboard",
	)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := inserted.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("RowsAffected = %d, %v", affected, err)
	}
	var title string
	if err := database.QueryRow(
		`SELECT title FROM public.products WHERE merchant = $1 AND id = $2`,
		"shopee", int64(42),
	).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Keyboard" {
		t.Fatalf("title = %q", title)
	}
	compoundRows, err := database.Query(`
		SELECT id AS item_id, title FROM products WHERE merchant = $1
		UNION ALL
		SELECT id, title FROM products WHERE merchant = $2
		ORDER BY item_id DESC
	`, "shopee", "shopee")
	if err != nil {
		t.Fatal(err)
	}
	var compoundCount int
	for compoundRows.Next() {
		var id int32
		var itemTitle string
		if err := compoundRows.Scan(&id, &itemTitle); err != nil {
			_ = compoundRows.Close()
			t.Fatal(err)
		}
		if id != 42 || itemTitle != "Keyboard" {
			_ = compoundRows.Close()
			t.Fatalf("UNION ALL row = (%d, %q)", id, itemTitle)
		}
		compoundCount++
	}
	if err := compoundRows.Close(); err != nil {
		t.Fatal(err)
	}
	if compoundCount != 2 {
		t.Fatalf("UNION ALL row count = %d", compoundCount)
	}
	preparedCTE, err := database.Prepare(`
		WITH chosen(item_id, label) AS (
			SELECT id, title FROM products WHERE merchant = $1
		)
		SELECT item_id, UPPER(label) AS label
		FROM chosen WHERE item_id = $2
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer preparedCTE.Close()
	var cteID int32
	var cteLabel string
	if err := preparedCTE.QueryRow("shopee", int64(42)).Scan(&cteID, &cteLabel); err != nil {
		t.Fatal(err)
	}
	if cteID != 42 || cteLabel != "KEYBOARD" {
		t.Fatalf("prepared CTE row = (%d, %q)", cteID, cteLabel)
	}
	explained, err := database.Query(`
		EXPLAIN ANALYZE
		SELECT title FROM products WHERE merchant = $1 AND id = $2
	`, "shopee", int64(42))
	if err != nil {
		t.Fatal(err)
	}
	var actualDetail, indexDetail, rowDetail, pageDetail, cacheDetail, physicalDetail string
	for explained.Next() {
		var id int64
		var operation, detail string
		if err := explained.Scan(&id, &operation, &detail); err != nil {
			explained.Close()
			t.Fatal(err)
		}
		switch operation {
		case "actual":
			actualDetail = detail
		case "index":
			indexDetail = detail
		case "rows":
			rowDetail = detail
		case "pages":
			pageDetail = detail
		case "cache":
			cacheDetail = detail
		case "physical":
			physicalDetail = detail
		}
	}
	if err := explained.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(actualDetail, "path=primary-lookup") ||
		indexDetail != "entries_scanned=0 point_lookups=1" ||
		rowDetail != "scanned=1 matched=1" ||
		pageDetail != "accessed=0 read=0 bytes=0 records_decoded=0" ||
		cacheDetail != "hits=0 misses=0 bypasses=0" ||
		physicalDetail != "generation_entries=0 overlay_entries=1" {
		t.Fatalf(
			"wire row EXPLAIN ANALYZE: actual=%q index=%q rows=%q pages=%q cache=%q physical=%q",
			actualDetail, indexDetail, rowDetail, pageDetail, cacheDetail, physicalDetail,
		)
	}
	var extendedID float64
	var normalizedTitle string
	if err := database.QueryRow(`
		SELECT id * 1.5 AS extended_id, UPPER(title) AS normalized_title
		FROM products WHERE merchant = $1 AND id = $2
	`, "shopee", int64(42)).Scan(&extendedID, &normalizedTitle); err != nil {
		t.Fatal(err)
	}
	if extendedID != 63 || normalizedTitle != "KEYBOARD" {
		t.Fatalf("expression result = (%v, %q)", extendedID, normalizedTitle)
	}
	var updatedTitle string
	if err := database.QueryRow(
		`UPDATE products SET title = UPPER($1) || ' keyboard' WHERE merchant = $2 AND id = $3 RETURNING title`,
		"mechanical", "shopee", int64(42),
	).Scan(&updatedTitle); err != nil {
		t.Fatal(err)
	}
	if updatedTitle != "MECHANICAL keyboard" {
		t.Fatalf("updated title = %q", updatedTitle)
	}
	var table string
	if err := database.QueryRow(`
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'
	`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table != "products" {
		t.Fatalf("catalog table = %q", table)
	}
	var managerOID uint32
	var managerTable, managerSchema string
	if err := database.QueryRow(`
		SELECT p.oid AS oid, p.relname AS table_name, n.nspname AS table_schema
		FROM pg_class AS p
		JOIN pg_namespace AS n ON p.relnamespace = n.oid
		WHERE p.relkind = 'r'
	`).Scan(&managerOID, &managerTable, &managerSchema); err != nil {
		t.Fatal(err)
	}
	if managerOID == 0 || managerTable != "products" || managerSchema != "public" {
		t.Fatalf("manager pg_class table = (%d, %q, %q)", managerOID, managerTable, managerSchema)
	}
	var unionTable, unionSchema, unionType string
	if err := database.QueryRow(`
		(SELECT table_name, table_schema, table_type FROM information_schema.tables)
		UNION
		(SELECT c.relname AS table_name, n.nspname AS table_schema, 'MATERIALIZED VIEW'
		 FROM pg_catalog.pg_class c
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE c.relkind = 'm')
	`).Scan(&unionTable, &unionSchema, &unionType); err != nil {
		t.Fatal(err)
	}
	if unionTable != "products" || unionSchema != "public" || unionType != "BASE TABLE" {
		t.Fatalf("manager union table = (%q, %q, %q)", unionTable, unionSchema, unionType)
	}
	enums, err := database.Query(`
		SELECT t.oid AS type_oid, t.typname AS type_name, e.enumlabel AS type_define
		FROM pg_type AS t JOIN pg_enum AS e ON e.enumtypid = t.oid
	`)
	if err != nil {
		t.Fatal(err)
	}
	if enums.Next() {
		_ = enums.Close()
		t.Fatal("KitDB scalar types were exposed as PostgreSQL enums")
	}
	if err := enums.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE INDEX products_merchant_title_idx ON products (merchant, title)`); err != nil {
		t.Fatal(err)
	}
	columns, err := database.Query(`
		SELECT column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'products'
		ORDER BY ordinal_position
	`)
	if err != nil {
		t.Fatal(err)
	}
	var columnNames []string
	for columns.Next() {
		var name, dataType, nullable string
		if err := columns.Scan(&name, &dataType, &nullable); err != nil {
			_ = columns.Close()
			t.Fatal(err)
		}
		columnNames = append(columnNames, name)
		if dataType == "" || nullable != "NO" {
			t.Fatalf("column %q metadata type=%q nullable=%q", name, dataType, nullable)
		}
	}
	if err := columns.Close(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(columnNames) != "[merchant id title]" {
		t.Fatalf("catalog columns = %v", columnNames)
	}

	constraints, err := database.Query(`
		SELECT constraint_name, constraint_type
		FROM information_schema.table_constraints
		WHERE table_schema = 'public' AND table_name = 'products'
	`)
	if err != nil {
		t.Fatal(err)
	}
	constraintTypes := make(map[string]string)
	for constraints.Next() {
		var name, kind string
		if err := constraints.Scan(&name, &kind); err != nil {
			_ = constraints.Close()
			t.Fatal(err)
		}
		constraintTypes[name] = kind
	}
	_ = constraints.Close()
	if constraintTypes["products_pkey"] != "PRIMARY KEY" ||
		constraintTypes["unique_products_title"] != "UNIQUE" {
		t.Fatalf("catalog constraints = %#v", constraintTypes)
	}
	primaryColumns, err := database.Query(`
		SELECT kc.ordinal_position AS ordinal_position,
		       tc.constraint_name AS constraint_name,
		       kc.column_name AS column_name
		FROM information_schema.table_constraints tc,
		     information_schema.key_column_usage kc
		WHERE tc.constraint_type = 'PRIMARY KEY'
		  AND kc.table_name = tc.table_name
		  AND kc.table_schema = tc.table_schema
		  AND kc.constraint_name = tc.constraint_name
		  AND tc.table_schema = 'public'
		  AND tc.table_name = 'products'
		ORDER BY kc.ordinal_position ASC
	`)
	if err != nil {
		t.Fatal(err)
	}
	var primaryNames []string
	for primaryColumns.Next() {
		var ordinal int64
		var constraint, name string
		if err := primaryColumns.Scan(&ordinal, &constraint, &name); err != nil {
			_ = primaryColumns.Close()
			t.Fatal(err)
		}
		if constraint != "products_pkey" || ordinal != int64(len(primaryNames)+1) {
			_ = primaryColumns.Close()
			t.Fatalf("primary key metadata = (%d, %q, %q)", ordinal, constraint, name)
		}
		primaryNames = append(primaryNames, name)
	}
	if err := primaryColumns.Close(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(primaryNames) != "[merchant id]" {
		t.Fatalf("primary key columns = %v", primaryNames)
	}

	var indexDefinition string
	if err := database.QueryRow(`
		SELECT indexdef FROM pg_catalog.pg_indexes
		WHERE schemaname = 'public' AND tablename = 'products'
		AND indexname = 'products_merchant_title_idx'
	`).Scan(&indexDefinition); err != nil {
		t.Fatal(err)
	}
	if indexDefinition != `CREATE INDEX "products_merchant_title_idx" ON public."products" ("merchant", "title")` {
		t.Fatalf("index definition = %q", indexDefinition)
	}

	engine.mu.RLock()
	products, err := engine.schemaLocked("products")
	engine.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	countKey, err := tableCountKey(products)
	if err != nil {
		t.Fatal(err)
	}
	engine.writeMu.Lock()
	countTransaction, err := engine.database.Begin()
	if err == nil {
		err = countTransaction.Put(countKey, encodeTableCount(13_773_074))
	}
	if err == nil {
		_, err = countTransaction.Commit()
	} else if countTransaction != nil {
		_ = countTransaction.Rollback()
	}
	engine.writeMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	var estimatedRows int64
	if err := database.QueryRow(`
		select reltuples::int8 as count
		from pg_class c join pg_catalog.pg_namespace n on n.oid=c.relnamespace
		where nspname='public' and relname='products'
	`).Scan(&estimatedRows); err != nil {
		t.Fatal(err)
	}
	if estimatedRows != 13_773_074 {
		t.Fatalf("catalog reltuples = %d", estimatedRows)
	}
	var routines int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM information_schema.routines`).Scan(&routines); err != nil {
		t.Fatal(err)
	}
	if routines != 0 {
		t.Fatalf("catalog routines = %d", routines)
	}
	var typeName string
	var typeLength int64
	if err := database.QueryRow(`SELECT typname, typlen FROM pg_catalog.pg_type WHERE oid = 20`).Scan(
		&typeName, &typeLength,
	); err != nil {
		t.Fatal(err)
	}
	if typeName != "int8" || typeLength != 8 {
		t.Fatalf("pg_type int8 = (%q, %d)", typeName, typeLength)
	}
	var numericOID uint32
	if err := database.QueryRow(`SELECT oid FROM pg_type WHERE typname = 'numeric'`).Scan(&numericOID); err != nil {
		t.Fatal(err)
	}
	if numericOID != kitdbsql.PostgreSQLOIDNumeric {
		t.Fatalf("numeric OID = %d", numericOID)
	}
	deleted, err := database.Exec(
		`DELETE FROM products WHERE merchant = $1 AND id = $2`, "shopee", int64(42),
	)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := deleted.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("DELETE RowsAffected = %d, %v", affected, err)
	}
}

func TestStandalonePostgresWireTransactions(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "transactions.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.ServePostgres(ctx, listener, PostgresServerOptions{
			PostgresOptions: PostgresOptions{Database: "transactions", User: "kitdb", Password: "secret"},
			MaxConnections:  4,
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = engine.Close()
	})

	database, err := sql.Open("postgres", fmt.Sprintf(
		"postgres://kitdb:secret@%s/transactions?sslmode=disable", listener.Addr().String(),
	))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE ledger (id INTEGER PRIMARY KEY, note TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}

	rolledBack, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rolledBack.Exec(`INSERT INTO ledger (id, note) VALUES (1, 'temporary')`); err != nil {
		t.Fatal(err)
	}
	var note string
	if err := rolledBack.QueryRow(`SELECT note FROM ledger WHERE id = 1`).Scan(&note); err != nil || note != "temporary" {
		t.Fatalf("transaction SELECT note=%q err=%v", note, err)
	}
	if err := rolledBack.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("count after rollback=%d err=%v", count, err)
	}

	committed, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := committed.Exec(`INSERT INTO ledger (id, note) VALUES (2, 'durable')`); err != nil {
		t.Fatal(err)
	}
	if err := committed.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count after commit=%d err=%v", count, err)
	}

	failed, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failed.Exec(`INSERT INTO ledger (id, note) VALUES (3, 'durable')`); err == nil {
		t.Fatal("duplicate write unexpectedly succeeded")
	}
	if _, err := failed.Exec(`SELECT COUNT(*) FROM ledger`); err == nil {
		t.Fatal("aborted transaction accepted another statement")
	}
	if err := failed.Rollback(); err != nil {
		t.Fatal(err)
	}
}
