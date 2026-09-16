package work

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
	"github.com/lib/pq"
)

func TestKitDBPostgresLegacyUUIDRemainsText(t *testing.T) {
	column := kitDBPostgresColumn(kitDBRemoteColumn{name: "id", kind: "uuid"})
	if column.DataTypeOID != pgwire.OIDText || column.DataTypeSize != -1 {
		t.Fatalf("legacy Kitwork UUID column = %#v", column)
	}
}

func TestKitDBPostgresWireWithLibPQ(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int, choice, ref } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  title: text().notNull(),
  price: int().default(0),
  status: choice("active", "disabled").default("active")
});
const warehouses = struct({
  id: id(),
  code: text().notNull().unique()
});
const warehouse_items = struct({
  id: id(),
  warehouse_id: ref(warehouses.id, { onDelete: "cascade" }).notNull().index(),
  sku: text().notNull().index()
});
const variants = struct({
  merchant: text().key(1),
  id: int().key(2),
  title: text().notNull()
});
const db = kitdb("postgres.kitdb", { products, warehouses, warehouse_items, variants }, { token: "postgres-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- tenant.ServeKitDBPostgres(ctx, listener, KitDBPostgresOptions{
			Database: "postgres.kitdb", User: "kitdb", MaxConnections: 8,
			IdleTimeout: 5 * time.Second, QueryTimeout: 5 * time.Second,
		})
	}()
	defer func() {
		cancel()
		select {
		case serveErr := <-serverDone:
			if serveErr != nil {
				t.Errorf("ServeKitDBPostgres: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServeKitDBPostgres did not stop")
		}
	}()

	database := openKitDBPostgresTestClient(t, listener.Addr().String(), "postgres-secret")
	defer database.Close()
	queryCtx, queryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer queryCancel()
	if err := database.PingContext(queryCtx); err != nil {
		t.Fatalf("PostgreSQL Ping: %v", err)
	}

	var scalar int64
	if err := database.QueryRowContext(queryCtx, "SELECT 1").Scan(&scalar); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if scalar != 1 {
		t.Fatalf("SELECT 1 = %d", scalar)
	}
	var databaseName string
	if err := database.QueryRowContext(queryCtx, "SELECT current_database()").Scan(&databaseName); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	if databaseName != "postgres" {
		t.Fatalf("current_database = %q", databaseName)
	}
	if _, err := database.ExecContext(queryCtx, `
INSERT INTO variants (merchant, id, title) VALUES ($1, $2, $3)`,
		"tiki", int64(7), "Keyboard",
	); err != nil {
		t.Fatalf("PostgreSQL parameterized composite INSERT: %v", err)
	}
	var variantTitle string
	if err := database.QueryRowContext(queryCtx, `
SELECT title FROM variants WHERE merchant=$1 AND id=$2`,
		"tiki", int64(7),
	).Scan(&variantTitle); err != nil {
		t.Fatalf("PostgreSQL parameterized composite SELECT: %v", err)
	}
	if variantTitle != "Keyboard" {
		t.Fatalf("PostgreSQL parameterized composite title = %q", variantTitle)
	}

	tables, err := database.QueryContext(queryCtx, `
SELECT table_catalog, table_schema, table_name, table_type
FROM information_schema.tables
WHERE table_schema = 'public'
ORDER BY table_name`)
	if err != nil {
		t.Fatalf("information_schema.tables: %v", err)
	}
	var tableCatalog, tableSchema, tableName, tableType string
	if !tables.Next() {
		t.Fatalf("information_schema.tables has no products row: %v", tables.Err())
	}
	if err := tables.Scan(&tableCatalog, &tableSchema, &tableName, &tableType); err != nil {
		t.Fatalf("scan information_schema.tables: %v", err)
	}
	if err := tables.Close(); err != nil {
		t.Fatalf("close information_schema.tables: %v", err)
	}
	if tableCatalog != "postgres" || tableSchema != "public" || tableName != "products" || tableType != "BASE TABLE" {
		t.Fatalf("information_schema.tables row = (%q, %q, %q, %q)", tableCatalog, tableSchema, tableName, tableType)
	}

	columns, err := database.QueryContext(queryCtx, `
SELECT table_name, column_name, ordinal_position, is_nullable, data_type, udt_name
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = $1
ORDER BY ordinal_position`, "products")
	if err != nil {
		t.Fatalf("information_schema.columns: %v", err)
	}
	columnNames := make([]string, 0, 5)
	for columns.Next() {
		var name, column, nullable, dataType, udtName string
		var position int64
		if err := columns.Scan(&name, &column, &position, &nullable, &dataType, &udtName); err != nil {
			t.Fatalf("scan information_schema.columns: %v", err)
		}
		if name != "products" || position != int64(len(columnNames)+1) {
			t.Fatalf("information_schema.columns identity = (%q, %q, %d)", name, column, position)
		}
		columnNames = append(columnNames, column)
	}
	if err := columns.Close(); err != nil {
		t.Fatalf("close information_schema.columns: %v", err)
	}
	if got := fmt.Sprint(columnNames); got != "[id sku title price status]" {
		t.Fatalf("information_schema.columns names = %s", got)
	}
	var exactColumnCount int64
	if err := database.QueryRowContext(queryCtx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema='public' AND table_name='products' AND column_name='title'`,
	).Scan(&exactColumnCount); err != nil {
		t.Fatalf("filtered information_schema.columns count: %v", err)
	}
	if exactColumnCount != 1 {
		t.Fatalf("filtered information_schema.columns count = %d", exactColumnCount)
	}
	if err := database.QueryRowContext(queryCtx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema='public' AND table_name='products' AND column_name='_key'`,
	).Scan(&exactColumnCount); err != nil {
		t.Fatalf("missing information_schema.columns count: %v", err)
	}
	if exactColumnCount != 0 {
		t.Fatalf("missing information_schema.columns count = %d", exactColumnCount)
	}

	var tableOID int64
	if err := database.QueryRowContext(queryCtx, `
SELECT c.oid, n.nspname, c.relname, c.relkind
FROM pg_catalog.pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2`, "public", "products").Scan(
		&tableOID, &tableSchema, &tableName, &tableType,
	); err != nil {
		t.Fatalf("pg_catalog.pg_class: %v", err)
	}
	if tableOID < 16384 || tableSchema != "public" || tableName != "products" || tableType != "r" {
		t.Fatalf("pg_catalog.pg_class row = (%d, %q, %q, %q)", tableOID, tableSchema, tableName, tableType)
	}

	attributes, err := database.QueryContext(queryCtx, `
SELECT a.attname, a.attnum, a.attnotnull, t.typname,
       pg_catalog.format_type(a.atttypid, a.atttypmod) AS formatted_type
FROM pg_catalog.pg_attribute AS a
JOIN pg_catalog.pg_type AS t ON t.oid = a.atttypid
WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`, tableOID)
	if err != nil {
		t.Fatalf("pg_catalog.pg_attribute: %v", err)
	}
	attributeCount := 0
	for attributes.Next() {
		var name, typeName, formattedType string
		var position int64
		var notNull bool
		if err := attributes.Scan(&name, &position, &notNull, &typeName, &formattedType); err != nil {
			t.Fatalf("scan pg_catalog.pg_attribute: %v", err)
		}
		attributeCount++
		if name == "price" && (typeName != "int8" || formattedType != "bigint") {
			t.Fatalf("price PostgreSQL type = (%q, %q)", typeName, formattedType)
		}
	}
	if err := attributes.Close(); err != nil {
		t.Fatalf("close pg_catalog.pg_attribute: %v", err)
	}
	if attributeCount != 5 {
		t.Fatalf("pg_catalog.pg_attribute count = %d", attributeCount)
	}

	primaryRows, err := database.QueryContext(queryCtx, `
SELECT kc.ordinal_position AS ordinal_position,
       tc.constraint_name AS constraint_name,
       kc.column_name AS column_name
FROM information_schema.table_constraints tc,
     information_schema.key_column_usage kc
WHERE tc.constraint_type='PRIMARY KEY'
  AND kc.table_name=tc.table_name
  AND kc.table_schema=tc.table_schema
  AND kc.constraint_name=tc.constraint_name
  AND tc.table_schema='public'
  AND tc.table_name='products'
ORDER BY kc.ordinal_position ASC`)
	if err != nil {
		t.Fatalf("TablePlus primary-key discovery: %v", err)
	}
	if !primaryRows.Next() {
		t.Fatalf("TablePlus primary-key discovery has no row: %v", primaryRows.Err())
	}
	var primaryPosition int64
	var primaryName, primaryColumn string
	if err := primaryRows.Scan(&primaryPosition, &primaryName, &primaryColumn); err != nil {
		t.Fatalf("scan TablePlus primary-key discovery: %v", err)
	}
	if primaryRows.Next() {
		t.Fatal("TablePlus primary-key discovery returned more than one products key field")
	}
	if err := primaryRows.Close(); err != nil {
		t.Fatalf("close TablePlus primary-key discovery: %v", err)
	}
	if primaryPosition != 1 || primaryName != "products_pkey" || primaryColumn != "id" {
		t.Fatalf("TablePlus primary key = (%d, %q, %q)", primaryPosition, primaryName, primaryColumn)
	}

	uniqueRows, err := database.QueryContext(queryCtx, `
SELECT constraint_name, constraint_type
FROM information_schema.table_constraints
WHERE table_schema='public' AND table_name='products' AND constraint_type='UNIQUE'`)
	if err != nil {
		t.Fatalf("information_schema unique discovery: %v", err)
	}
	if !uniqueRows.Next() {
		t.Fatalf("information_schema unique discovery has no row: %v", uniqueRows.Err())
	}
	var uniqueName, uniqueType string
	if err := uniqueRows.Scan(&uniqueName, &uniqueType); err != nil {
		t.Fatalf("scan information_schema unique discovery: %v", err)
	}
	if uniqueRows.Next() {
		t.Fatal("information_schema unique discovery returned an unexpected second products constraint")
	}
	if err := uniqueRows.Close(); err != nil {
		t.Fatalf("close information_schema unique discovery: %v", err)
	}
	if uniqueName != "unique_products_sku" || uniqueType != "UNIQUE" {
		t.Fatalf("information_schema unique = (%q, %q)", uniqueName, uniqueType)
	}

	indexRows, err := database.QueryContext(queryCtx, `
SELECT ix.relname AS index_name,
       upper(am.amname) AS index_algorithm,
       indisunique AS is_unique,
       pg_get_indexdef(indexrelid) AS index_definition,
       REPLACE(regexp_replace(regexp_replace(pg_get_indexdef(indexrelid),' WHERE .+',''),'.*\((.*)\)','\1'),' ','') AS column_name,
       CASE WHEN position(' WHERE ' IN pg_get_indexdef(indexrelid))>0
            THEN regexp_replace(pg_get_indexdef(indexrelid),'.+WHERE ','') ELSE '' END AS condition,
       pg_catalog.obj_description(i.indexrelid,'pg_class') AS comment
FROM pg_index i
JOIN pg_class t ON t.oid=i.indrelid
JOIN pg_class ix ON ix.oid=i.indexrelid
JOIN pg_namespace n ON t.relnamespace=n.oid
JOIN pg_am AS am ON ix.relam=am.oid
WHERE t.relname='products' AND n.nspname='public'`)
	if err != nil {
		t.Fatalf("TablePlus index discovery: %v", err)
	}
	type postgresIndex struct {
		name, algorithm, definition, columns, condition string
		unique                                          bool
	}
	indexes := make(map[string]postgresIndex)
	for indexRows.Next() {
		var index postgresIndex
		var comment sql.NullString
		if err := indexRows.Scan(
			&index.name, &index.algorithm, &index.unique, &index.definition,
			&index.columns, &index.condition, &comment,
		); err != nil {
			t.Fatalf("scan TablePlus index discovery: %v", err)
		}
		if comment.Valid {
			t.Fatalf("KitDB invented index comment %q", comment.String)
		}
		indexes[index.name] = index
	}
	if err := indexRows.Close(); err != nil {
		t.Fatalf("close TablePlus index discovery: %v", err)
	}
	for name, want := range map[string]struct {
		column string
		unique bool
	}{
		"products_pkey":       {column: "id", unique: true},
		"unique_products_sku": {column: "sku", unique: true},
		"idx_products_sku":    {column: "sku", unique: false},
	} {
		index, found := indexes[name]
		if !found {
			t.Fatalf("TablePlus indexes %v are missing %q", indexes, name)
		}
		if index.algorithm != "BTREE" || index.unique != want.unique || index.columns != want.column ||
			!strings.Contains(index.definition, `INDEX "`+name+`"`) {
			t.Fatalf("TablePlus index %q = %#v", name, index)
		}
	}
	if len(indexes) != 3 {
		t.Fatalf("TablePlus products indexes = %#v", indexes)
	}

	var warehouseItemsOID int64
	if err := database.QueryRowContext(
		queryCtx, `SELECT oid FROM pg_class WHERE relkind='r' AND relname='warehouse_items'`,
	).Scan(&warehouseItemsOID); err != nil {
		t.Fatalf("warehouse_items oid: %v", err)
	}
	foreignRows, err := database.QueryContext(queryCtx, fmt.Sprintf(`
SELECT c.conname AS constraint_name,
       tf.schema AS child_schema,
       tf.name AS child_name,
       (SELECT STRING_AGG(QUOTE_IDENT(a.attname),',' ORDER BY t.seq)
          FROM (SELECT ROW_NUMBER() OVER (ROWS UNBOUNDED PRECEDING) AS seq,attnum
                  FROM UNNEST(c.conkey) AS t(attnum)) AS t
          INNER JOIN pg_attribute AS a ON a.attrelid=c.conrelid AND a.attnum=t.attnum) AS child_column,
       tt.schema AS parent_schema,
       tt.name AS parent_name,
       (SELECT STRING_AGG(QUOTE_IDENT(a.attname),',' ORDER BY t.seq)
          FROM (SELECT ROW_NUMBER() OVER (ROWS UNBOUNDED PRECEDING) AS seq,attnum
                  FROM UNNEST(c.confkey) AS t(attnum)) AS t
          INNER JOIN pg_attribute AS a ON a.attrelid=c.confrelid AND a.attnum=t.attnum) AS parent_column,
       CASE confupdtype WHEN 'r' THEN 'restrict' WHEN 'c' THEN 'cascade' WHEN 'n' THEN 'set null'
            WHEN 'd' THEN 'set default' WHEN 'a' THEN 'no action' ELSE NULL END AS on_update,
       CASE confdeltype WHEN 'r' THEN 'restrict' WHEN 'c' THEN 'cascade' WHEN 'n' THEN 'set null'
            WHEN 'd' THEN 'set default' WHEN 'a' THEN 'no action' ELSE NULL END AS on_delete
FROM pg_catalog.pg_constraint AS c
INNER JOIN (SELECT pg_class.oid,QUOTE_IDENT(pg_namespace.nspname) AS schema,
                   QUOTE_IDENT(pg_class.relname) AS name
              FROM pg_class INNER JOIN pg_namespace ON pg_class.relnamespace=pg_namespace.oid) AS tf
        ON tf.oid=c.conrelid
INNER JOIN (SELECT pg_class.oid,QUOTE_IDENT(pg_namespace.nspname) AS schema,
                   QUOTE_IDENT(pg_class.relname) AS name
              FROM pg_class INNER JOIN pg_namespace ON pg_class.relnamespace=pg_namespace.oid) AS tt
        ON tt.oid=c.confrelid
WHERE tf.oid='%d' AND c.contype='f'`, warehouseItemsOID))
	if err != nil {
		t.Fatalf("TablePlus foreign-key discovery: %v", err)
	}
	if !foreignRows.Next() {
		t.Fatalf("TablePlus foreign-key discovery has no row: %v", foreignRows.Err())
	}
	var foreignName, childSchema, childName, childColumn string
	var parentSchema, parentName, parentColumn, onUpdate, onDelete string
	if err := foreignRows.Scan(
		&foreignName, &childSchema, &childName, &childColumn,
		&parentSchema, &parentName, &parentColumn, &onUpdate, &onDelete,
	); err != nil {
		t.Fatalf("scan TablePlus foreign-key discovery: %v", err)
	}
	if foreignRows.Next() {
		t.Fatal("TablePlus foreign-key discovery returned an unexpected second constraint")
	}
	if err := foreignRows.Close(); err != nil {
		t.Fatalf("close TablePlus foreign-key discovery: %v", err)
	}
	if foreignName != "warehouse_items_warehouse_id_fkey" || childSchema != "public" ||
		childName != "warehouse_items" || childColumn != "warehouse_id" || parentSchema != "public" ||
		parentName != "warehouses" || parentColumn != "id" || onUpdate != "no action" || onDelete != "cascade" {
		t.Fatalf(
			"TablePlus foreign key = (%q, %q, %q, %q, %q, %q, %q, %q, %q)",
			foreignName, childSchema, childName, childColumn, parentSchema, parentName, parentColumn, onUpdate, onDelete,
		)
	}

	inserted, err := database.ExecContext(queryCtx, `
INSERT INTO products (id, sku, title, price, status)
VALUES ('product_1', 'KIT-1', 'KitDB Alpha', 125, 'active')`)
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	if affected, err := inserted.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("INSERT RowsAffected = %d, %v", affected, err)
	}

	var title string
	var price int64
	if err := database.QueryRowContext(
		queryCtx,
		"SELECT title, price FROM products WHERE sku = $1",
		"KIT-1",
	).Scan(&title, &price); err != nil {
		t.Fatalf("parameterized SELECT: %v", err)
	}
	if title != "KitDB Alpha" || price != 125 {
		t.Fatalf("product = (%q, %d)", title, price)
	}
	var estimatedRows int64
	if err := database.QueryRowContext(queryCtx, `
SELECT reltuples::int8 as count from pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
where nspname='public'AND relname='products'`).Scan(&estimatedRows); err != nil {
		t.Fatalf("pg_class reltuples before ANALYZE: %v", err)
	}
	if estimatedRows != -1 {
		t.Fatalf("pg_class reltuples before ANALYZE = %d, want -1", estimatedRows)
	}

	if _, err := database.ExecContext(queryCtx, `ANALYZE products`); err != nil {
		t.Fatalf("PostgreSQL ANALYZE: %v", err)
	}
	if err := database.QueryRowContext(queryCtx, `
SELECT reltuples::int8 AS count
FROM pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relname = 'products'`).Scan(&estimatedRows); err != nil {
		t.Fatalf("pg_class reltuples after ANALYZE: %v", err)
	}
	if estimatedRows != 1 {
		t.Fatalf("pg_class reltuples after ANALYZE = %d, want 1", estimatedRows)
	}
	var statisticsKind, statisticsName, statisticsFields string
	var statisticsRows, statisticsEntries int64
	var statisticsPrefixes, statisticsStatus, statisticsTransaction string
	if err := database.QueryRowContext(queryCtx, `PRAGMA statistics(products)`).Scan(
		&statisticsKind,
		&statisticsName,
		&statisticsFields,
		&statisticsRows,
		&statisticsEntries,
		&statisticsPrefixes,
		&statisticsStatus,
		&statisticsTransaction,
	); err != nil {
		t.Fatalf("PostgreSQL PRAGMA statistics: %v", err)
	}
	if statisticsKind != "table" || statisticsName != "products" ||
		statisticsFields != "[]" || statisticsRows != 1 || statisticsEntries != 1 ||
		statisticsPrefixes != "[]" || statisticsStatus != "current" || statisticsTransaction == "" {
		t.Fatalf(
			"PostgreSQL statistics = (%q, %q, %q, %d, %d, %q, %q, %q)",
			statisticsKind, statisticsName, statisticsFields, statisticsRows, statisticsEntries,
			statisticsPrefixes, statisticsStatus, statisticsTransaction,
		)
	}
	var explainID, explainParent, explainUnused int64
	var explainDetail string
	if err := database.QueryRowContext(
		queryCtx,
		`EXPLAIN QUERY PLAN SELECT * FROM products WHERE sku = 'KIT-1' LIMIT 1`,
	).Scan(&explainID, &explainParent, &explainUnused, &explainDetail); err != nil {
		t.Fatalf("PostgreSQL statistics EXPLAIN: %v", err)
	}
	if !strings.Contains(explainDetail, "STATS CURRENT@") ||
		!strings.Contains(explainDetail, "ESTIMATE 1 ROWS (UPPER_BOUND)") {
		t.Fatalf("PostgreSQL statistics EXPLAIN detail = %q", explainDetail)
	}

	wireConnection, err := database.Conn(queryCtx)
	if err != nil {
		t.Fatalf("pin PostgreSQL connection: %v", err)
	}
	defer wireConnection.Close()
	if _, err := wireConnection.QueryContext(queryCtx, `SELECT "id", "slug" FROM products`); err == nil {
		t.Fatal("missing column query succeeded")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42703" {
			t.Fatalf("missing column error = %T %v", err, err)
		}
	}

	var recoveredID, recoveredSKU string
	if err := wireConnection.QueryRowContext(
		queryCtx,
		`SELECT "id", "sku" FROM products WHERE "id" = $1`,
		"product_1",
	).Scan(&recoveredID, &recoveredSKU); err != nil {
		t.Fatalf("query after missing column error: %v", err)
	}
	if recoveredID != "product_1" || recoveredSKU != "KIT-1" {
		t.Fatalf("query after missing column error = (%q, %q)", recoveredID, recoveredSKU)
	}

	if _, err := wireConnection.ExecContext(queryCtx, `
CREATE TABLE "NhanVien" (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL
)`); err != nil {
		t.Fatalf("PostgreSQL CREATE TABLE for DROP: %v", err)
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`INSERT INTO "NhanVien" (id, name) VALUES ('employee_1', 'Kitwork')`,
	); err != nil {
		t.Fatalf("PostgreSQL INSERT before DROP: %v", err)
	}
	rolledBackDrop, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for rolled-back DROP: %v", err)
	}
	if _, err := rolledBackDrop.ExecContext(queryCtx, `DROP TABLE "public"."NhanVien" CASCADE`); err != nil {
		_ = rolledBackDrop.Rollback()
		t.Fatalf("PostgreSQL staged DROP TABLE: %v", err)
	}
	if err := rolledBackDrop.Rollback(); err != nil {
		t.Fatalf("PostgreSQL ROLLBACK DROP TABLE: %v", err)
	}
	var retainedEmployees int64
	if err := wireConnection.QueryRowContext(
		queryCtx,
		`SELECT COUNT(*) FROM "NhanVien"`,
	).Scan(&retainedEmployees); err != nil || retainedEmployees != 1 {
		t.Fatalf("rolled-back DROP retained employees = %d, %v", retainedEmployees, err)
	}

	committedDrop, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for committed DROP: %v", err)
	}
	if _, err := committedDrop.ExecContext(queryCtx, `DROP TABLE "public"."NhanVien" CASCADE`); err != nil {
		_ = committedDrop.Rollback()
		t.Fatalf("PostgreSQL transactional DROP TABLE: %v", err)
	}
	if err := committedDrop.Commit(); err != nil {
		t.Fatalf("PostgreSQL COMMIT DROP TABLE: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP TABLE IF EXISTS "public"."NhanVien"`); err != nil {
		t.Fatalf("PostgreSQL DROP TABLE IF EXISTS: %v", err)
	}
	if _, err := wireConnection.QueryContext(queryCtx, `SELECT * FROM "NhanVien"`); err == nil {
		t.Fatal("PostgreSQL SELECT found dropped table")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42P01" {
			t.Fatalf("dropped table error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP TABLE products`); err == nil {
		t.Fatal("PostgreSQL DROP removed source-declared table")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("source-declared DROP error = %T %v", err, err)
		}
	}

	if _, err := wireConnection.ExecContext(queryCtx, `
CREATE TABLE pg_index_drop_test (id TEXT PRIMARY KEY, status TEXT NOT NULL)`); err != nil {
		t.Fatalf("PostgreSQL CREATE TABLE for DROP INDEX: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
CREATE INDEX pg_index_drop_status_idx ON pg_index_drop_test (status)`); err != nil {
		t.Fatalf("PostgreSQL CREATE INDEX for DROP INDEX: %v", err)
	}
	indexNames := func() []string {
		return queryKitDBPostgresStrings(t, queryCtx, wireConnection, `
SELECT indexname FROM pg_catalog.pg_indexes
WHERE schemaname='public' AND tablename='pg_index_drop_test'`)
	}
	if names := indexNames(); !containsString(names, "pg_index_drop_status_idx") {
		t.Fatalf("PostgreSQL indexes before DROP = %v", names)
	}
	rolledBackIndexDrop, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for rolled-back DROP INDEX: %v", err)
	}
	if _, err := rolledBackIndexDrop.ExecContext(
		queryCtx, `DROP INDEX "public"."pg_index_drop_status_idx" CASCADE`,
	); err != nil {
		_ = rolledBackIndexDrop.Rollback()
		t.Fatalf("PostgreSQL staged DROP INDEX: %v", err)
	}
	if err := rolledBackIndexDrop.Rollback(); err != nil {
		t.Fatalf("PostgreSQL ROLLBACK DROP INDEX: %v", err)
	}
	if names := indexNames(); !containsString(names, "pg_index_drop_status_idx") {
		t.Fatalf("rolled-back PostgreSQL DROP INDEX indexes = %v", names)
	}
	committedIndexDrop, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for committed DROP INDEX: %v", err)
	}
	if _, err := committedIndexDrop.ExecContext(
		queryCtx, `DROP INDEX "public"."pg_index_drop_status_idx" RESTRICT`,
	); err != nil {
		_ = committedIndexDrop.Rollback()
		t.Fatalf("PostgreSQL transactional DROP INDEX: %v", err)
	}
	if err := committedIndexDrop.Commit(); err != nil {
		t.Fatalf("PostgreSQL COMMIT DROP INDEX: %v", err)
	}
	if names := indexNames(); containsString(names, "pg_index_drop_status_idx") {
		t.Fatalf("committed PostgreSQL DROP INDEX indexes = %v", names)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP INDEX IF EXISTS pg_index_drop_status_idx`); err != nil {
		t.Fatalf("PostgreSQL DROP INDEX IF EXISTS: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP INDEX pg_index_drop_test_pkey`); err == nil {
		t.Fatal("PostgreSQL DROP INDEX removed a primary constraint index")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "2BP01" {
			t.Fatalf("protected primary index error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP INDEX missing_pg_index`); err == nil {
		t.Fatal("PostgreSQL DROP INDEX accepted a missing index")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42704" {
			t.Fatalf("missing index error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP INDEX idx_products_sku`); err == nil {
		t.Fatal("PostgreSQL DROP INDEX changed a source-declared struct")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("source-declared index error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP TABLE pg_index_drop_test`); err != nil {
		t.Fatalf("PostgreSQL cleanup DROP TABLE: %v", err)
	}

	if _, err := wireConnection.ExecContext(queryCtx, `
CREATE TABLE pg_constraint_add_parent (
  id TEXT PRIMARY KEY,
  code TEXT NOT NULL
)`); err != nil {
		t.Fatalf("PostgreSQL CREATE parent for ADD CONSTRAINT: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
CREATE TABLE pg_constraint_add_child (
  id TEXT PRIMARY KEY,
  parent_code TEXT NOT NULL,
  amount INTEGER NOT NULL
)`); err != nil {
		t.Fatalf("PostgreSQL CREATE child for ADD CONSTRAINT: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
INSERT INTO pg_constraint_add_parent (id, code) VALUES ('add_parent_1', 'PARENT')`); err != nil {
		t.Fatalf("PostgreSQL INSERT parent before ADD CONSTRAINT: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
INSERT INTO pg_constraint_add_child (id, parent_code, amount)
VALUES ('add_child_1', 'PARENT', 10)`); err != nil {
		t.Fatalf("PostgreSQL INSERT child before ADD CONSTRAINT: %v", err)
	}
	addConstraintNames := func(table string) []string {
		return queryKitDBPostgresStrings(t, queryCtx, wireConnection, `
SELECT constraint_name FROM information_schema.table_constraints
WHERE table_schema='public' AND table_name='`+table+`'
ORDER BY constraint_name`)
	}
	rolledBackConstraintAdd, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for rolled-back ADD CONSTRAINT: %v", err)
	}
	if _, err := rolledBackConstraintAdd.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_add_child ADD CONSTRAINT pg_add_amount_check CHECK (amount >= 0)`,
	); err != nil {
		_ = rolledBackConstraintAdd.Rollback()
		t.Fatalf("PostgreSQL staged ADD CONSTRAINT: %v", err)
	}
	if err := rolledBackConstraintAdd.Rollback(); err != nil {
		t.Fatalf("PostgreSQL ROLLBACK ADD CONSTRAINT: %v", err)
	}
	if names := addConstraintNames("pg_constraint_add_child"); containsString(names, "pg_add_amount_check") {
		t.Fatalf("rolled-back PostgreSQL ADD CONSTRAINT constraints = %v", names)
	}
	committedConstraintAdd, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for committed ADD CONSTRAINT: %v", err)
	}
	if _, err := committedConstraintAdd.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_add_child ADD CONSTRAINT pg_add_amount_check CHECK (amount >= 0)`,
	); err != nil {
		_ = committedConstraintAdd.Rollback()
		t.Fatalf("PostgreSQL transactional ADD CONSTRAINT: %v", err)
	}
	if err := committedConstraintAdd.Commit(); err != nil {
		t.Fatalf("PostgreSQL COMMIT ADD CONSTRAINT: %v", err)
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_add_parent ADD CONSTRAINT pg_add_parent_code_key UNIQUE (code)`,
	); err != nil {
		t.Fatalf("PostgreSQL ADD UNIQUE CONSTRAINT: %v", err)
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_add_child ADD CONSTRAINT pg_add_parent_fkey `+
			`FOREIGN KEY (parent_code) REFERENCES pg_constraint_add_parent(code) ON DELETE RESTRICT`,
	); err != nil {
		t.Fatalf("PostgreSQL ADD FOREIGN KEY CONSTRAINT: %v", err)
	}
	if names := addConstraintNames("pg_constraint_add_parent"); !containsString(names, "pg_add_parent_code_key") {
		t.Fatalf("PostgreSQL parent constraints after ADD = %v", names)
	}
	if names := addConstraintNames("pg_constraint_add_child"); !containsString(names, "pg_add_amount_check") || !containsString(names, "pg_add_parent_fkey") {
		t.Fatalf("PostgreSQL child constraints after ADD = %v", names)
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_add_child ADD CONSTRAINT pg_add_amount_check CHECK (amount >= 0)`,
	); err == nil {
		t.Fatal("PostgreSQL ADD CONSTRAINT accepted a duplicate name")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42710" {
			t.Fatalf("duplicate constraint error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
INSERT INTO pg_constraint_add_parent (id, code) VALUES ('add_parent_duplicate', 'PARENT')`); err == nil {
		t.Fatal("PostgreSQL added UNIQUE accepted a duplicate")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "23505" {
			t.Fatalf("added UNIQUE error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
INSERT INTO pg_constraint_add_child (id, parent_code, amount)
VALUES ('add_child_missing', 'MISSING', 10)`); err == nil {
		t.Fatal("PostgreSQL added FOREIGN KEY accepted a missing parent")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "23503" {
			t.Fatalf("added FOREIGN KEY error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
INSERT INTO pg_constraint_add_child (id, parent_code, amount)
VALUES ('add_child_negative', 'PARENT', -1)`); err == nil {
		t.Fatal("PostgreSQL added CHECK accepted an invalid value")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "23514" {
			t.Fatalf("added CHECK error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE products ADD CONSTRAINT products_title_key UNIQUE (title)`,
	); err == nil {
		t.Fatal("PostgreSQL ADD CONSTRAINT changed a source-declared struct")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("source-declared ADD CONSTRAINT error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_add_child ADD CONSTRAINT pg_add_child_new_pkey PRIMARY KEY (parent_code)`,
	); err == nil {
		t.Fatal("PostgreSQL ADD CONSTRAINT accepted a new primary key")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("ADD PRIMARY KEY error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP TABLE pg_constraint_add_child`); err != nil {
		t.Fatalf("PostgreSQL cleanup ADD CONSTRAINT child: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP TABLE pg_constraint_add_parent`); err != nil {
		t.Fatalf("PostgreSQL cleanup ADD CONSTRAINT parent: %v", err)
	}

	if _, err := wireConnection.ExecContext(queryCtx, `
CREATE TABLE pg_constraint_drop_test (
  id TEXT PRIMARY KEY,
  code TEXT UNIQUE,
  amount INTEGER NOT NULL,
  CONSTRAINT pg_constraint_amount_check CHECK (amount >= 0)
)`); err != nil {
		t.Fatalf("PostgreSQL CREATE TABLE for DROP CONSTRAINT: %v", err)
	}
	constraintNames := func() []string {
		return queryKitDBPostgresStrings(t, queryCtx, wireConnection, `
SELECT constraint_name FROM information_schema.table_constraints
WHERE table_schema='public' AND table_name='pg_constraint_drop_test'
ORDER BY constraint_name`)
	}
	if names := constraintNames(); !containsString(names, "pg_constraint_amount_check") ||
		!containsString(names, "unique_pg_constraint_drop_test_code") {
		t.Fatalf("PostgreSQL constraints before DROP = %v", names)
	}
	rolledBackConstraintDrop, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for rolled-back DROP CONSTRAINT: %v", err)
	}
	if _, err := rolledBackConstraintDrop.ExecContext(
		queryCtx,
		`ALTER TABLE "public".pg_constraint_drop_test DROP CONSTRAINT pg_constraint_amount_check`,
	); err != nil {
		_ = rolledBackConstraintDrop.Rollback()
		t.Fatalf("PostgreSQL staged DROP CONSTRAINT: %v", err)
	}
	if err := rolledBackConstraintDrop.Rollback(); err != nil {
		t.Fatalf("PostgreSQL ROLLBACK DROP CONSTRAINT: %v", err)
	}
	if names := constraintNames(); !containsString(names, "pg_constraint_amount_check") {
		t.Fatalf("rolled-back PostgreSQL DROP CONSTRAINT constraints = %v", names)
	}
	committedConstraintDrop, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for committed DROP CONSTRAINT: %v", err)
	}
	if _, err := committedConstraintDrop.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_drop_test DROP CONSTRAINT pg_constraint_amount_check RESTRICT`,
	); err != nil {
		_ = committedConstraintDrop.Rollback()
		t.Fatalf("PostgreSQL transactional DROP CONSTRAINT: %v", err)
	}
	if err := committedConstraintDrop.Commit(); err != nil {
		t.Fatalf("PostgreSQL COMMIT DROP CONSTRAINT: %v", err)
	}
	if names := constraintNames(); containsString(names, "pg_constraint_amount_check") {
		t.Fatalf("committed PostgreSQL DROP CONSTRAINT constraints = %v", names)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
INSERT INTO pg_constraint_drop_test (id, code, amount) VALUES ('constraint_1', 'same', -1)`); err != nil {
		t.Fatalf("PostgreSQL INSERT after dropped CHECK: %v", err)
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_drop_test DROP CONSTRAINT unique_pg_constraint_drop_test_code`,
	); err != nil {
		t.Fatalf("PostgreSQL DROP UNIQUE CONSTRAINT: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
INSERT INTO pg_constraint_drop_test (id, code, amount) VALUES ('constraint_2', 'same', 1)`); err != nil {
		t.Fatalf("PostgreSQL duplicate INSERT after dropped UNIQUE: %v", err)
	}
	if names := constraintNames(); containsString(names, "unique_pg_constraint_drop_test_code") {
		t.Fatalf("PostgreSQL constraints after UNIQUE DROP = %v", names)
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_drop_test DROP CONSTRAINT IF EXISTS missing_constraint`,
	); err != nil {
		t.Fatalf("PostgreSQL DROP CONSTRAINT IF EXISTS: %v", err)
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_drop_test DROP CONSTRAINT missing_constraint`,
	); err == nil {
		t.Fatal("PostgreSQL DROP CONSTRAINT accepted a missing constraint")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42704" {
			t.Fatalf("missing constraint error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE pg_constraint_drop_test DROP CONSTRAINT pg_constraint_drop_test_pkey`,
	); err == nil {
		t.Fatal("PostgreSQL DROP CONSTRAINT removed a primary key")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("protected primary constraint error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(
		queryCtx,
		`ALTER TABLE products DROP CONSTRAINT unique_products_sku`,
	); err == nil {
		t.Fatal("PostgreSQL DROP CONSTRAINT changed a source-declared struct")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("source-declared constraint error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP TABLE pg_constraint_drop_test`); err != nil {
		t.Fatalf("PostgreSQL cleanup constraint table: %v", err)
	}

	if _, err := wireConnection.ExecContext(queryCtx, `
CREATE TABLE pg_rename_parent (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  price INTEGER NOT NULL
)`); err != nil {
		t.Fatalf("PostgreSQL CREATE TABLE for rename: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
CREATE TABLE pg_rename_child (
  id TEXT PRIMARY KEY,
  parent_id TEXT NOT NULL REFERENCES pg_rename_parent(id) ON DELETE RESTRICT
)`); err != nil {
		t.Fatalf("PostgreSQL CREATE child for rename: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
CREATE INDEX pg_rename_status_idx ON pg_rename_parent (status)`); err != nil {
		t.Fatalf("PostgreSQL CREATE INDEX for rename: %v", err)
	}
	waitForKitDBPostgresIndexIdle(t, queryCtx, wireConnection, "pg_rename_parent")
	if _, err := wireConnection.ExecContext(queryCtx, `
INSERT INTO pg_rename_parent (id, status, price) VALUES ('parent_1', 'active', 10)`); err != nil {
		t.Fatalf("PostgreSQL INSERT parent for rename: %v", err)
	}

	rolledBackTableRename, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for rolled-back table rename: %v", err)
	}
	if _, err := rolledBackTableRename.ExecContext(
		queryCtx, `ALTER TABLE pg_rename_parent RENAME TO pg_renamed_parent`,
	); err != nil {
		_ = rolledBackTableRename.Rollback()
		t.Fatalf("PostgreSQL staged table rename: %v", err)
	}
	if err := rolledBackTableRename.Rollback(); err != nil {
		t.Fatalf("PostgreSQL ROLLBACK table rename: %v", err)
	}
	if tables := queryKitDBPostgresStrings(t, queryCtx, wireConnection, `
SELECT table_name FROM information_schema.tables WHERE table_schema='public'`); !containsString(tables, "pg_rename_parent") || containsString(tables, "pg_renamed_parent") {
		t.Fatalf("rolled-back table rename catalog = %v", tables)
	}

	committedTableRename, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for committed table rename: %v", err)
	}
	if _, err := committedTableRename.ExecContext(
		queryCtx, `ALTER TABLE "public".pg_rename_parent RENAME TO pg_renamed_parent`,
	); err != nil {
		_ = committedTableRename.Rollback()
		t.Fatalf("PostgreSQL transactional table rename: %v", err)
	}
	if err := committedTableRename.Commit(); err != nil {
		t.Fatalf("PostgreSQL COMMIT table rename: %v", err)
	}
	if tables := queryKitDBPostgresStrings(t, queryCtx, wireConnection, `
SELECT table_name FROM information_schema.tables WHERE table_schema='public'`); containsString(tables, "pg_rename_parent") || !containsString(tables, "pg_renamed_parent") {
		t.Fatalf("committed table rename catalog = %v", tables)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `
INSERT INTO pg_rename_child (id, parent_id) VALUES ('child_1', 'parent_1')`); err != nil {
		t.Fatalf("PostgreSQL foreign key after table rename: %v", err)
	}

	rolledBackIndexRename, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for rolled-back index rename: %v", err)
	}
	if _, err := rolledBackIndexRename.ExecContext(
		queryCtx, `ALTER INDEX pg_rename_status_idx RENAME TO pg_renamed_status_idx`,
	); err != nil {
		_ = rolledBackIndexRename.Rollback()
		t.Fatalf("PostgreSQL staged index rename: %v", err)
	}
	if err := rolledBackIndexRename.Rollback(); err != nil {
		t.Fatalf("PostgreSQL ROLLBACK index rename: %v", err)
	}
	if indexes := queryKitDBPostgresStrings(t, queryCtx, wireConnection, `
SELECT indexname FROM pg_catalog.pg_indexes
WHERE schemaname='public' AND tablename='pg_renamed_parent'`); !containsString(indexes, "pg_rename_status_idx") || containsString(indexes, "pg_renamed_status_idx") {
		t.Fatalf("rolled-back index rename catalog = %v", indexes)
	}

	committedIndexRename, err := wireConnection.BeginTx(queryCtx, nil)
	if err != nil {
		t.Fatalf("PostgreSQL BEGIN for committed index rename: %v", err)
	}
	if _, err := committedIndexRename.ExecContext(
		queryCtx, `ALTER INDEX "public".pg_rename_status_idx RENAME TO pg_renamed_status_idx`,
	); err != nil {
		_ = committedIndexRename.Rollback()
		t.Fatalf("PostgreSQL transactional index rename: %v", err)
	}
	if err := committedIndexRename.Commit(); err != nil {
		t.Fatalf("PostgreSQL COMMIT index rename: %v", err)
	}
	if indexes := queryKitDBPostgresStrings(t, queryCtx, wireConnection, `
SELECT indexname FROM pg_catalog.pg_indexes
WHERE schemaname='public' AND tablename='pg_renamed_parent'`); containsString(indexes, "pg_rename_status_idx") || !containsString(indexes, "pg_renamed_status_idx") {
		t.Fatalf("committed index rename catalog = %v", indexes)
	}

	if _, err := wireConnection.ExecContext(queryCtx, `CREATE TABLE pg_rename_conflict (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("PostgreSQL CREATE conflict table: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `ALTER TABLE pg_renamed_parent RENAME TO pg_rename_conflict`); err == nil {
		t.Fatal("PostgreSQL table rename accepted a duplicate name")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42P07" {
			t.Fatalf("duplicate table rename error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `CREATE INDEX pg_rename_price_idx ON pg_renamed_parent (price)`); err != nil {
		t.Fatalf("PostgreSQL CREATE conflict index: %v", err)
	}
	waitForKitDBPostgresIndexIdle(t, queryCtx, wireConnection, "pg_renamed_parent")
	if _, err := wireConnection.ExecContext(queryCtx, `ALTER INDEX pg_renamed_status_idx RENAME TO pg_rename_price_idx`); err == nil {
		t.Fatal("PostgreSQL index rename accepted a duplicate name")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "42P07" {
			t.Fatalf("duplicate index rename error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `ALTER TABLE products RENAME TO archived_products`); err == nil {
		t.Fatal("PostgreSQL renamed a source-declared table")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("source-declared table rename error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `ALTER INDEX idx_products_sku RENAME TO archived_products_sku_idx`); err == nil {
		t.Fatal("PostgreSQL renamed a source-declared index")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "0A000" {
			t.Fatalf("source-declared index rename error = %T %v", err, err)
		}
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP TABLE pg_rename_child`); err != nil {
		t.Fatalf("PostgreSQL cleanup rename child: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP TABLE pg_renamed_parent`); err != nil {
		t.Fatalf("PostgreSQL cleanup renamed parent: %v", err)
	}
	if _, err := wireConnection.ExecContext(queryCtx, `DROP TABLE pg_rename_conflict`); err != nil {
		t.Fatalf("PostgreSQL cleanup rename conflict: %v", err)
	}

	unauthorized := openKitDBPostgresTestClient(t, listener.Addr().String(), "wrong-secret")
	defer unauthorized.Close()
	if err := unauthorized.PingContext(queryCtx); err == nil {
		t.Fatal("wrong PostgreSQL password was accepted")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "28P01" {
			t.Fatalf("wrong password error = %T %v", err, err)
		}
	}
}

func waitForKitDBPostgresIndexIdle(
	t *testing.T,
	ctx context.Context,
	connection *sql.Conn,
	table string,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := connection.QueryContext(ctx, `PRAGMA index_status(`+table+`)`)
		if err != nil {
			t.Fatal(err)
		}
		pending := rows.Next()
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !pending {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("PostgreSQL index maintenance for %q did not become idle", table)
		}
		time.Sleep(time.Millisecond)
	}
}

func containsString(values []string, expected string) bool {
	for _, item := range values {
		if item == expected {
			return true
		}
	}
	return false
}

func TestKitDBPostgresNodeDatabaseSelection(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int } = database;
const products = struct({
  id: id(),
  title: text().notNull(),
  price: int().default(0)
});
const events = struct({
  id: id(),
  name: text().notNull()
});
const secrets = struct({
  id: id(),
  value: text().notNull()
});
const shop = kitdb("shop.kitdb", { products }, { token: "shared-secret", access: "readwrite" });
const analytics = kitdb("analytics", { events }, { token: "shared-secret", access: "readonly" });
const privateDb = kitdb("private.kitdb", { secrets }, { token: "private-secret", access: "readonly" });
router.get(() => ({ shop: shop.products.count(), analytics: analytics.events.count(), private: privateDb.secrets.count() }));`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- tenant.ServeKitDBPostgres(ctx, listener, KitDBPostgresOptions{
			MaintenanceDatabase: "kitdb", User: "kitdb", MaxConnections: 8,
			IdleTimeout: 5 * time.Second, QueryTimeout: 5 * time.Second,
		})
	}()
	defer func() {
		cancel()
		select {
		case serveErr := <-serverDone:
			if serveErr != nil {
				t.Errorf("ServeKitDBPostgres node: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServeKitDBPostgres node did not stop")
		}
	}()

	queryCtx, queryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer queryCancel()
	maintenance := openKitDBPostgresDatabaseTestClient(
		t, listener.Addr().String(), "kitdb", "shared-secret",
	)
	defer maintenance.Close()
	if err := maintenance.PingContext(queryCtx); err != nil {
		t.Fatalf("maintenance Ping: %v", err)
	}
	var databaseName string
	if err := maintenance.QueryRowContext(queryCtx, "SELECT current_database()").Scan(&databaseName); err != nil {
		t.Fatalf("maintenance current_database: %v", err)
	}
	if databaseName != "kitdb" {
		t.Fatalf("maintenance current_database = %q", databaseName)
	}
	if names := queryKitDBPostgresStrings(t, queryCtx, maintenance, `
SELECT datname
FROM pg_catalog.pg_database
WHERE datallowconn = true AND NOT datistemplate
ORDER BY datname`); fmt.Sprint(names) != "[analytics kitdb shop]" {
		t.Fatalf("shared-token databases = %v", names)
	}
	if names := queryKitDBPostgresStrings(t, queryCtx, maintenance, `
SELECT datname FROM pg_catalog.pg_database WHERE datname = current_database()`); fmt.Sprint(names) != "[kitdb]" {
		t.Fatalf("current maintenance database = %v", names)
	}
	if names := queryKitDBPostgresStrings(t, queryCtx, maintenance, `
SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = 'public'`); len(names) != 0 {
		t.Fatalf("maintenance tables = %v", names)
	}
	if _, err := maintenance.QueryContext(queryCtx, "SELECT * FROM products"); err == nil {
		t.Fatal("maintenance database accepted a user-struct query")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "3D000" {
			t.Fatalf("maintenance user-struct error = %T %v", err, err)
		}
	}

	shop := openKitDBPostgresDatabaseTestClient(
		t, listener.Addr().String(), "shop", "shared-secret",
	)
	defer shop.Close()
	var selectedDatabase string
	if err := shop.QueryRowContext(queryCtx, "SELECT current_database()").Scan(&selectedDatabase); err != nil {
		t.Fatalf("shop current_database: %v", err)
	}
	if selectedDatabase != "shop" {
		t.Fatalf("shop current_database = %q", selectedDatabase)
	}
	legacyShop := openKitDBPostgresDatabaseTestClient(
		t, listener.Addr().String(), "shop.kitdb", "shared-secret",
	)
	if err := legacyShop.QueryRowContext(queryCtx, "SELECT current_database()").Scan(&selectedDatabase); err != nil {
		legacyShop.Close()
		t.Fatalf("legacy shop alias: %v", err)
	}
	if closeErr := legacyShop.Close(); closeErr != nil {
		t.Fatalf("close legacy shop alias: %v", closeErr)
	}
	if selectedDatabase != "shop" {
		t.Fatalf("legacy shop current_database = %q", selectedDatabase)
	}
	typeRows, err := shop.QueryContext(queryCtx, `SELECT oid, typname FROM pg_type;`)
	if err != nil {
		t.Fatalf("TablePlus pg_type discovery: %v", err)
	}
	typeCount := 0
	for typeRows.Next() {
		var oid int64
		var name string
		if err := typeRows.Scan(&oid, &name); err != nil {
			t.Fatalf("scan TablePlus pg_type discovery: %v", err)
		}
		typeCount++
	}
	if err := typeRows.Close(); err != nil {
		t.Fatalf("close TablePlus pg_type discovery: %v", err)
	}
	if typeCount < 10 {
		t.Fatalf("TablePlus pg_type count = %d", typeCount)
	}
	if names := queryKitDBPostgresStrings(t, queryCtx, shop, `
SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = 'public' ORDER BY tablename`); fmt.Sprint(names) != "[products]" {
		t.Fatalf("shop tables = %v", names)
	}
	var tableOID int64
	var tableName, tableSchema string
	if err := shop.QueryRowContext(queryCtx, `
SELECT p.oid AS oid,p.relname AS table_name,n.nspname AS table_schema
FROM pg_class AS p JOIN pg_namespace AS n ON p.relnamespace=n.oid
WHERE p.relkind='r'`).Scan(&tableOID, &tableName, &tableSchema); err != nil {
		t.Fatalf("TablePlus pg_class discovery: %v", err)
	}
	if tableOID < 16384 || tableName != "products" || tableSchema != "public" {
		t.Fatalf("TablePlus pg_class row = (%d, %q, %q)", tableOID, tableName, tableSchema)
	}
	unionRows, err := shop.QueryContext(queryCtx, `
(SELECT table_name, table_schema, table_type FROM information_schema.tables)
UNION
(SELECT c.relname AS table_name, n.nspname AS table_schema, 'MATERIALIZED VIEW'
 FROM pg_catalog.pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE c.relkind = 'm');`)
	if err != nil {
		t.Fatalf("TablePlus UNION discovery: %v", err)
	}
	if !unionRows.Next() {
		t.Fatalf("TablePlus UNION discovery has no products row: %v", unionRows.Err())
	}
	var tableType string
	if err := unionRows.Scan(&tableName, &tableSchema, &tableType); err != nil {
		t.Fatalf("scan TablePlus UNION discovery: %v", err)
	}
	if err := unionRows.Close(); err != nil {
		t.Fatalf("close TablePlus UNION discovery: %v", err)
	}
	if tableName != "products" || tableSchema != "public" || tableType != "BASE TABLE" {
		t.Fatalf("TablePlus UNION row = (%q, %q, %q)", tableName, tableSchema, tableType)
	}
	functionRows, err := shop.QueryContext(queryCtx, `
SELECT pg_catalog.pg_get_userbyid(p.proowner) AS owner,p.oid AS oid,
       pg_get_function_identity_arguments(p.oid) AS args,n.nspname AS function_schema,
       p.proname AS function_name,
       CASE WHEN p.proisagg THEN 'aggregate' ELSE 'function' END AS function_type
FROM pg_catalog.pg_proc p
LEFT JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname<>'pg_catalog' AND n.nspname<>'information_schema';`)
	if err != nil {
		t.Fatalf("TablePlus function discovery: %v", err)
	}
	if functionRows.Next() {
		t.Fatal("KitDB invented a stored function")
	}
	if err := functionRows.Close(); err != nil {
		t.Fatalf("close TablePlus function discovery: %v", err)
	}
	pgTables, err := shop.QueryContext(queryCtx, `SELECT * FROM pg_catalog.pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatalf("SELECT * pg_catalog.pg_tables: %v", err)
	}
	pgTableColumns, err := pgTables.Columns()
	if err != nil {
		t.Fatalf("pg_catalog.pg_tables columns: %v", err)
	}
	if err := pgTables.Close(); err != nil {
		t.Fatalf("close pg_catalog.pg_tables: %v", err)
	}
	if fmt.Sprint(pgTableColumns) != "[schemaname tablename tableowner tablespace hasindexes hasrules hastriggers rowsecurity]" {
		t.Fatalf("pg_catalog.pg_tables columns = %v", pgTableColumns)
	}

	analytics := openKitDBPostgresDatabaseTestClient(
		t, listener.Addr().String(), "analytics", "shared-secret",
	)
	defer analytics.Close()
	if err := analytics.QueryRowContext(queryCtx, "SELECT current_database()").Scan(&selectedDatabase); err != nil {
		t.Fatalf("analytics current_database: %v", err)
	}
	if selectedDatabase != "analytics" {
		t.Fatalf("analytics current_database = %q", selectedDatabase)
	}
	if names := queryKitDBPostgresStrings(t, queryCtx, analytics, `
SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name`); fmt.Sprint(names) != "[events]" {
		t.Fatalf("analytics tables = %v", names)
	}

	privateMaintenance := openKitDBPostgresDatabaseTestClient(
		t, listener.Addr().String(), "kitdb", "private-secret",
	)
	defer privateMaintenance.Close()
	if names := queryKitDBPostgresStrings(t, queryCtx, privateMaintenance, `
SELECT datname FROM pg_catalog.pg_database ORDER BY datname`); fmt.Sprint(names) != "[kitdb private]" {
		t.Fatalf("private-token databases = %v", names)
	}

	unauthorized := openKitDBPostgresDatabaseTestClient(
		t, listener.Addr().String(), "private", "shared-secret",
	)
	defer unauthorized.Close()
	if err := unauthorized.PingContext(queryCtx); err == nil {
		t.Fatal("shared token opened private.kitdb")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "3D000" {
			t.Fatalf("cross-token database error = %T %v", err, err)
		}
	}
}

func TestKitDBPostgresInteractiveDMLTransactions(t *testing.T) {
	tenant, address, databaseProxy := startKitDBPostgresTransactionTest(t, 5*time.Second)
	first := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	second := openKitDBPostgresDatabaseTestClient(t, address, "transactions", "transaction-secret")
	defer first.Close()
	defer second.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := first.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ExecContext(ctx, `INSERT INTO products (id, sku, title, price)
VALUES ('seed', 'SEED', 'Before', 10)`); err != nil {
		t.Fatalf("seed product: %v", err)
	}

	managed, err := kitDBForRequest(tenant, databaseProxy.dbName, nil).database()
	if err != nil {
		t.Fatal(err)
	}
	defer managed.Release()
	beforeCommit, err := managed.database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}

	transaction, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BEGIN data transaction: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO products (id, sku, title, price) VALUES
('committed_1', 'COMMIT-1', 'First', 20),
('committed_2', 'COMMIT-2', 'Second', 30)`); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("transactional INSERT: %v", err)
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE products SET title = 'First updated', price = price + 5
WHERE id = 'committed_1'`); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("transactional UPDATE: %v", err)
	}
	var title string
	var price int64
	if err := transaction.QueryRowContext(ctx, `SELECT title, price FROM products WHERE id = 'committed_1'`).Scan(&title, &price); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("read own write: %v", err)
	}
	if title != "First updated" || price != 25 {
		_ = transaction.Rollback()
		t.Fatalf("read own write = (%q, %d)", title, price)
	}
	var count int64
	if err := second.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE id = 'committed_1'`).Scan(&count); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if count != 0 {
		_ = transaction.Rollback()
		t.Fatalf("uncommitted row leaked to another connection: count=%d", count)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("COMMIT data transaction: %v", err)
	}
	afterCommit, err := managed.database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if afterCommit != beforeCommit+1 {
		t.Fatalf("three SQL mutations published %d kernel transactions, want one", afterCommit-beforeCommit)
	}
	if err := second.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE id IN ('committed_1', 'committed_2')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("committed rows = %d, want 2", count)
	}

	rolledBack, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rolledBack.ExecContext(ctx, `INSERT INTO products (id, sku, title)
VALUES ('rolled_back', 'ROLLBACK', 'Discard me')`); err != nil {
		_ = rolledBack.Rollback()
		t.Fatal(err)
	}
	if err := rolledBack.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := second.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE id = 'rolled_back'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("ROLLBACK retained %d rows", count)
	}

	snapshot, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.QueryRowContext(ctx, `SELECT title FROM products WHERE id = 'seed'`).Scan(&title); err != nil {
		_ = snapshot.Rollback()
		t.Fatal(err)
	}
	if _, err := second.ExecContext(ctx, `UPDATE products SET title = 'After' WHERE id = 'seed'`); err != nil {
		_ = snapshot.Rollback()
		t.Fatal(err)
	}
	var repeated string
	if err := snapshot.QueryRowContext(ctx, `SELECT title FROM products WHERE id = 'seed'`).Scan(&repeated); err != nil {
		_ = snapshot.Rollback()
		t.Fatal(err)
	}
	if title != "Before" || repeated != "Before" {
		_ = snapshot.Rollback()
		t.Fatalf("fixed snapshot reads = (%q, %q)", title, repeated)
	}
	if err := snapshot.Rollback(); err != nil {
		t.Fatal(err)
	}

	failed, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failed.QueryContext(ctx, `SELECT missing_column FROM products`); err == nil {
		_ = failed.Rollback()
		t.Fatal("invalid statement did not abort transaction")
	}
	if _, err := failed.ExecContext(ctx, `INSERT INTO products (id, sku, title)
VALUES ('must_not_commit', 'FAILED', 'Failed')`); err == nil {
		_ = failed.Rollback()
		t.Fatal("aborted transaction accepted another statement")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "25P02" {
			_ = failed.Rollback()
			t.Fatalf("aborted transaction SQLSTATE = %T %v", err, err)
		}
	}
	if err := failed.Rollback(); err != nil {
		t.Fatal(err)
	}

	conflicted, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conflicted.ExecContext(ctx, `UPDATE products SET price = 99 WHERE id = 'seed'`); err != nil {
		_ = conflicted.Rollback()
		t.Fatal(err)
	}
	if _, err := second.ExecContext(ctx, `INSERT INTO products (id, sku, title)
VALUES ('concurrent_writer', 'CONCURRENT', 'Writer')`); err != nil {
		_ = conflicted.Rollback()
		t.Fatal(err)
	}
	if err := conflicted.Commit(); err == nil {
		t.Fatal("conflicting transaction committed")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "40001" {
			t.Fatalf("data conflict SQLSTATE = %T %v", err, err)
		}
	}
	if err := second.QueryRowContext(ctx, `SELECT price FROM products WHERE id = 'seed'`).Scan(&price); err != nil {
		t.Fatal(err)
	}
	if price != 10 {
		t.Fatalf("conflicting update leaked price %d", price)
	}

	schemaConflict, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := schemaConflict.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil {
		_ = schemaConflict.Rollback()
		t.Fatal(err)
	}
	if _, err := second.ExecContext(ctx, `CREATE TABLE transaction_schema_change (
id TEXT PRIMARY KEY, note TEXT NOT NULL)`); err != nil {
		_ = schemaConflict.Rollback()
		t.Fatal(err)
	}
	if err := schemaConflict.Commit(); err == nil {
		t.Fatal("transaction committed across a schema revision change")
	} else {
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "40001" {
			t.Fatalf("schema conflict SQLSTATE = %T %v", err, err)
		}
	}
	if _, err := second.ExecContext(ctx, `DROP TABLE transaction_schema_change`); err != nil {
		t.Fatal(err)
	}
}

func TestKitDBPostgresTransactionTimeoutAndCloseReleaseResources(t *testing.T) {
	tenant, _, database := startKitDBPostgresTransactionTest(t, 25*time.Millisecond)
	newSession := func(timeout time.Duration) *kitDBPostgresSession {
		return &kitDBPostgresSession{
			tenant: tenant, database: database, databaseName: "transactions",
			databaseNames: []string{"transactions"}, user: "kitdb",
			transactionTimeout: timeout,
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	disconnected := newSession(time.Second)
	if _, err := disconnected.Execute(ctx, "BEGIN", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := disconnected.Execute(ctx, `INSERT INTO products (id, sku, title)
VALUES ('disconnected', 'DISCONNECTED', 'Disconnect')`, nil); err != nil {
		t.Fatal(err)
	}
	disconnected.transactionMu.Lock()
	record := disconnected.transaction.record
	scope := disconnected.transaction.scope
	disconnected.transactionMu.Unlock()
	if record == nil || scope == nil {
		t.Fatal("data statement did not acquire transaction resources")
	}
	if err := disconnected.Close(); err != nil {
		t.Fatal(err)
	}
	if !scope.Closed() {
		t.Fatal("Session.Close left the transaction scope open")
	}
	if _, _, err := record.Get([]byte("probe")); err == nil {
		t.Fatal("Session.Close left the record snapshot usable")
	}
	if err := disconnected.Close(); err != nil {
		t.Fatalf("second Session.Close: %v", err)
	}

	// Keep the test lifetime comfortably above one valid statement on a loaded Windows runner.
	timed := newSession(500 * time.Millisecond)
	if _, err := timed.Execute(ctx, "BEGIN", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := timed.Execute(ctx, `INSERT INTO products (id, sku, title)
VALUES ('timed_out', 'TIMED-OUT', 'Timeout')`, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for timed.TransactionStatus() != pgwire.TransactionFailed && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if timed.TransactionStatus() != pgwire.TransactionFailed {
		t.Fatal("transaction lifetime did not expire")
	}
	if _, err := timed.Execute(ctx, `SELECT COUNT(*) FROM products`, nil); err == nil {
		t.Fatal("expired transaction accepted a statement")
	} else {
		var postgresErr *pgwire.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "25P03" {
			t.Fatalf("expired transaction SQLSTATE = %T %v", err, err)
		}
	}
	if _, err := timed.Execute(ctx, "ROLLBACK", nil); err != nil {
		t.Fatal(err)
	}

	canceledCommit := newSession(time.Second)
	if _, err := canceledCommit.Execute(ctx, "BEGIN", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := canceledCommit.Execute(ctx, `INSERT INTO products (id, sku, title)
VALUES ('canceled_commit', 'CANCELED-COMMIT', 'Canceled commit')`, nil); err != nil {
		t.Fatal(err)
	}
	commitContext, cancelCommit := context.WithCancel(ctx)
	cancelCommit()
	if _, err := canceledCommit.Execute(commitContext, "COMMIT", nil); err == nil {
		t.Fatal("canceled COMMIT published a transaction")
	} else {
		var postgresErr *pgwire.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "57014" {
			t.Fatalf("canceled COMMIT SQLSTATE = %T %v", err, err)
		}
	}

	bounded := newSession(time.Second)
	if _, err := bounded.Execute(ctx, "BEGIN", nil); err != nil {
		t.Fatal(err)
	}
	bounded.transactionMu.Lock()
	bounded.transaction.statements = kitDBRemoteTransactionStatementLimit
	bounded.transactionMu.Unlock()
	if _, err := bounded.Execute(ctx, `SELECT COUNT(*) FROM products`, nil); err == nil {
		t.Fatal("transaction statement limit was not enforced")
	} else {
		var postgresErr *pgwire.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "54000" {
			t.Fatalf("statement limit SQLSTATE = %T %v", err, err)
		}
	}
	if _, err := bounded.Execute(ctx, "ROLLBACK", nil); err != nil {
		t.Fatal(err)
	}

	observer := newSession(time.Second)
	defer observer.Close()
	for _, id := range []string{"disconnected", "timed_out", "canceled_commit"} {
		result, err := observer.Execute(ctx, `SELECT COUNT(*) FROM products WHERE id = '`+id+`'`, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != 1 || len(result.Rows[0]) != 1 || string(result.Rows[0][0].Data) != "0" {
			t.Fatalf("rolled-back row %q result = %#v", id, result.Rows)
		}
	}
}

func startKitDBPostgresTransactionTest(
	t *testing.T,
	transactionTimeout time.Duration,
) (*Tenant, string, *dbProxy) {
	return startKitDBPostgresTransactionTestWithCopyLimit(t, transactionTimeout, 0)
}

func startKitDBPostgresTransactionTestWithCopyLimit(
	t *testing.T,
	transactionTimeout time.Duration,
	maxCopyBytes int64,
) (*Tenant, string, *dbProxy) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique(),
  title: text().notNull(),
  price: int().default(0)
});
const db = kitdb("transactions.kitdb", { products }, { token: "transaction-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	entries := listServes(tenant, "kitdb")
	if len(entries) != 1 || entries[0].config.database == nil {
		tenant.Close()
		t.Fatalf("KitDB serves = %#v", entries)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tenant.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- tenant.ServeKitDBPostgres(ctx, listener, KitDBPostgresOptions{
			Database: "transactions.kitdb", User: "kitdb", MaxConnections: 8,
			IdleTimeout: 5 * time.Second, QueryTimeout: 5 * time.Second,
			TransactionTimeout: transactionTimeout, MaxCopyBytes: maxCopyBytes,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case serveErr := <-done:
			if serveErr != nil {
				t.Errorf("ServeKitDBPostgres: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("ServeKitDBPostgres did not stop")
		}
		tenant.Close()
	})
	return tenant, listener.Addr().String(), entries[0].config.database
}

type kitDBPostgresQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryKitDBPostgresStrings(
	t *testing.T,
	ctx context.Context,
	database kitDBPostgresQueryer,
	query string,
) []string {
	t.Helper()
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("PostgreSQL query %q: %v", query, err)
	}
	defer rows.Close()
	values := make([]string, 0)
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			t.Fatalf("scan PostgreSQL query %q: %v", query, err)
		}
		values = append(values, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate PostgreSQL query %q: %v", query, err)
	}
	return values
}

// dropKitDBPostgresDatabase drops a database whose last client has just closed. lib/pq's Close
// sends Terminate and returns without waiting for the server to read it, so a DROP DATABASE that
// follows on another connection can still find that session registered and get PostgreSQL's own
// 55006 — the same race a real server has, and the client's to absorb. Only that error is
// retried, for a bounded window; anything else is the test's failure.
func dropKitDBPostgresDatabase(t *testing.T, ctx context.Context, maintenance *sql.DB, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := maintenance.ExecContext(ctx, `DROP DATABASE `+name)
		if err == nil {
			return
		}
		var postgresErr *pq.Error
		if !errors.As(err, &postgresErr) || postgresErr.Code != "55006" ||
			!strings.Contains(postgresErr.Message, "other session") || time.Now().After(deadline) {
			t.Fatalf("DROP DATABASE %s: %v", name, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func openKitDBPostgresTestClient(t *testing.T, address, password string) *sql.DB {
	return openKitDBPostgresDatabaseTestClient(t, address, "postgres", password)
}

func openKitDBPostgresDatabaseTestClient(t *testing.T, address, databaseName, password string) *sql.DB {
	t.Helper()
	connection := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword("kitdb", password),
		Host:   address,
		Path:   "/" + databaseName,
	}
	query := connection.Query()
	query.Set("sslmode", "disable")
	query.Set("connect_timeout", "3")
	connection.RawQuery = query.Encode()
	database, err := sql.Open("postgres", connection.String())
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	return database
}

func TestKitDBPostgresRejectsNonLoopbackListener(t *testing.T) {
	listener := &postgresTestListener{address: postgresTestAddress("0.0.0.0:5432")}
	err := (&Tenant{}).ServeKitDBPostgres(
		context.Background(), listener, KitDBPostgresOptions{Database: "demo.kitdb"},
	)
	if err == nil || err.Error() != "pgwire: the cleartext profile accepts loopback listeners only" {
		t.Fatalf("ServeKitDBPostgres error = %v", err)
	}
}

type postgresTestAddress string

func (address postgresTestAddress) Network() string { return "tcp" }
func (address postgresTestAddress) String() string  { return string(address) }

type postgresTestListener struct{ address net.Addr }

func (listener *postgresTestListener) Accept() (net.Conn, error) {
	return nil, fmt.Errorf("not reached")
}
func (listener *postgresTestListener) Close() error   { return nil }
func (listener *postgresTestListener) Addr() net.Addr { return listener.address }
