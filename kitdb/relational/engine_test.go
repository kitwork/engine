package relational

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestStandaloneTransactionReadYourWritesSavepointRollbackAndConflict(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "transactions.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}

	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Execute(ctx, `INSERT INTO items (id, name) VALUES (1, 'one')`); err != nil {
		t.Fatal(err)
	}
	visible, err := transaction.Execute(ctx, `SELECT name FROM items WHERE id = 1`)
	if err != nil || len(visible.Rows) != 1 || visible.Rows[0][0] != "one" {
		t.Fatalf("read-your-writes = %#v, %v", visible.Rows, err)
	}
	savepoint, err := transaction.Savepoint()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Execute(ctx, `INSERT INTO items (id, name) VALUES (2, 'two')`); err != nil {
		t.Fatal(err)
	}
	if err := transaction.RollbackTo(savepoint); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := engine.Execute(ctx, `SELECT id, name FROM items ORDER BY id`)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(1) {
		t.Fatalf("committed rows = %#v, %v", rows.Rows, err)
	}

	first, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		_ = first.Rollback()
		t.Fatal(err)
	}
	if _, err := first.Execute(ctx, `INSERT INTO items (id, name) VALUES (3, 'three')`); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Execute(ctx, `INSERT INTO items (id, name) VALUES (4, 'four')`); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Commit(ctx); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("second commit error = %v", err)
	}
	counted, err := engine.Execute(ctx, `SELECT COUNT(*) FROM items`)
	if err != nil || counted.Rows[0][0] != int64(2) {
		t.Fatalf("count after conflict = %#v, %v", counted.Rows, err)
	}

	readOnly, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.Execute(ctx, `INSERT INTO items (id, name) VALUES (5, 'five')`); err == nil {
		t.Fatal("read-only transaction accepted INSERT")
	}
	_ = readOnly.Rollback()
}

func TestStandaloneEngineCreateInsertSelectRollbackAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shop.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			merchant TEXT NOT NULL,
			id INTEGER NOT NULL,
			sku TEXT UNIQUE,
			title TEXT NOT NULL,
			price DECIMAL DEFAULT '0',
			active BOOLEAN DEFAULT true,
			PRIMARY KEY (merchant, id)
		)
	`); err != nil {
		t.Fatal(err)
	}
	inserted, err := engine.Execute(ctx, `
		INSERT INTO products (merchant, id, sku, title, price)
		VALUES ($1, $2, $3, $4, $5), ($1, $6, $7, $8, $9)
		RETURNING merchant, id, active
	`, "shopee", int64(2), "SKU-2", "Mouse", "20.50", int64(1), "SKU-1", "Keyboard", "10.25")
	if err != nil {
		t.Fatal(err)
	}
	if inserted.Affected != 2 || len(inserted.Rows) != 2 || inserted.Rows[0][2] != true {
		t.Fatalf("INSERT result = %#v", inserted)
	}

	selected, err := engine.Execute(ctx, `
		SELECT merchant, id, title, price, active
		FROM products WHERE merchant = $1 ORDER BY id ASC LIMIT 10
	`, "shopee")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Rows) != 2 || selected.Rows[0][1] != int64(1) || selected.Rows[0][2] != "Keyboard" {
		t.Fatalf("ordered SELECT = %#v", selected.Rows)
	}

	if _, err := engine.Execute(ctx, `
		INSERT INTO products (merchant, id, sku, title)
		VALUES ('shopee', 3, 'DUPLICATE', 'First'), ('shopee', 4, 'DUPLICATE', 'Second')
	`); err == nil {
		t.Fatal("duplicate batch unexpectedly committed")
	}
	counted, err := engine.Execute(ctx, `SELECT COUNT(*) AS count FROM products`)
	if err != nil {
		t.Fatal(err)
	}
	if len(counted.Rows) != 1 || counted.Rows[0][0] != int64(2) {
		t.Fatalf("count after rollback = %#v", counted.Rows)
	}

	if _, err := engine.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	reopened, err := engine.Execute(ctx, `
		SELECT title FROM products WHERE merchant = 'shopee' AND id = 2
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Rows) != 1 || reopened.Rows[0][0] != "Mouse" {
		t.Fatalf("reopened SELECT = %#v", reopened.Rows)
	}
	tables, err := engine.Tables()
	if err != nil || len(tables) != 1 || tables[0].Name != "products" {
		t.Fatalf("tables = %#v, %v", tables, err)
	}
}

func TestStandaloneEngineAutoKitIDAndJSON(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "events.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE events (
			id KITID PRIMARY KEY,
			payload JSONB NOT NULL,
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		t.Fatal(err)
	}
	inserted, err := engine.Execute(ctx, `
		INSERT INTO events (payload) VALUES ($1) RETURNING id, payload, created_at
	`, `{"click":1,"source":"test"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(inserted.Rows) != 1 || len(inserted.Rows[0][0].(string)) != 36 {
		t.Fatalf("generated event = %#v", inserted.Rows)
	}
	payload, ok := inserted.Rows[0][1].(map[string]any)
	if !ok || payload["source"] != "test" {
		t.Fatalf("decoded JSON = %#v", inserted.Rows[0][1])
	}
}

func TestStandaloneEngineUpdateDeleteRollbackAndBounds(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "members.kitdb"), Options{
		MaximumMutationRows: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE members (
			id INTEGER PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO members (id, email, name) VALUES
		(1, 'one@example.test', 'One'),
		(2, 'two@example.test', 'Two'),
		(3, 'three@example.test', 'Three')
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := engine.Execute(ctx, `
		UPDATE members SET email = 'two@example.test' WHERE id = 1
	`); err == nil {
		t.Fatal("unique-conflicting UPDATE unexpectedly committed")
	}
	unchanged, err := engine.Execute(ctx, `SELECT email FROM members WHERE id = 1`)
	if err != nil || len(unchanged.Rows) != 1 || unchanged.Rows[0][0] != "one@example.test" {
		t.Fatalf("row after failed UPDATE = %#v, %v", unchanged.Rows, err)
	}
	if _, err := engine.Execute(ctx, `
		UPDATE members SET email = 'one-new@example.test' WHERE id = 1
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		UPDATE members SET email = 'one@example.test' WHERE id = 2
	`); err != nil {
		t.Fatalf("released unique value was not reusable: %v", err)
	}

	updated, err := engine.Execute(ctx, `
		UPDATE members SET name = $1 WHERE id >= $2 RETURNING id, name
	`, "Changed", int64(2))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Affected != 2 || len(updated.Rows) != 2 || updated.Rows[0][1] != "Changed" {
		t.Fatalf("UPDATE result = %#v", updated)
	}
	if _, err := engine.Execute(ctx, `UPDATE members SET name = 'All'`); err == nil {
		t.Fatal("over-limit UPDATE unexpectedly committed")
	}
	countChanged, err := engine.Execute(ctx, `SELECT COUNT(*) AS count FROM members WHERE name = 'Changed'`)
	if err != nil || countChanged.Rows[0][0] != int64(2) {
		t.Fatalf("bounded rollback count = %#v, %v", countChanged.Rows, err)
	}

	deleted, err := engine.Execute(ctx, `
		DELETE FROM members WHERE id >= 2 RETURNING id, email
	`)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Affected != 2 || len(deleted.Rows) != 2 {
		t.Fatalf("DELETE result = %#v", deleted)
	}
	remaining, err := engine.Execute(ctx, `SELECT id, name FROM members`)
	if err != nil || len(remaining.Rows) != 1 || remaining.Rows[0][0] != int64(1) || remaining.Rows[0][1] != "One" {
		t.Fatalf("remaining rows = %#v, %v", remaining.Rows, err)
	}
}

func TestStandaloneUpdatePreservesUnknownTaggedFields(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "forward.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE records (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	engine.mu.Lock()
	schema, err := engine.schemaLocked("records")
	if err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	row := map[string]any{"id": int64(1), "name": "before"}
	key, err := rowKey(schema, row, 0)
	if err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	encoded, err := encodeRow(schema, row, []rawField{{tag: 99, kind: valueString, payload: []byte("future")}})
	if err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	transaction, err := engine.database.Begin()
	if err == nil {
		err = transaction.Put(key, encoded)
	}
	if err == nil {
		_, err = transaction.Commit()
	}
	engine.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := engine.Execute(ctx, `UPDATE records SET name = 'after' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	stored, found, err := engine.database.Get(key)
	if err != nil || !found {
		t.Fatalf("updated row found=%v err=%v", found, err)
	}
	decoded, err := decodeRow(schema, stored)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.values["name"] != "after" || len(decoded.unknown) != 1 ||
		decoded.unknown[0].tag != 99 || string(decoded.unknown[0].payload) != "future" {
		t.Fatalf("updated row = %#v unknown=%#v", decoded.values, decoded.unknown)
	}
}

func TestStandaloneOrderByLimitUsesBoundedTopN(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "bounded-top-n.kitdb"), Options{
		MaximumResultRows: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			id INTEGER PRIMARY KEY,
			rank INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (id, rank) VALUES
		(1, 30), (2, 10), (3, 20), (4, 10), (5, 20), (6, 40)
	`); err != nil {
		t.Fatal(err)
	}
	selected, err := engine.Execute(ctx, `
		SELECT id, rank
		FROM products
		ORDER BY rank ASC, id DESC
		LIMIT 2 OFFSET 1
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Rows) != 2 ||
		selected.Rows[0][0] != int64(2) || selected.Rows[0][1] != int64(10) ||
		selected.Rows[1][0] != int64(5) || selected.Rows[1][1] != int64(20) {
		t.Fatalf("bounded Top-N rows = %#v", selected.Rows)
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM products ORDER BY rank`); err == nil {
		t.Fatal("unbounded ORDER BY silently exceeded the result ceiling")
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM products ORDER BY rank LIMIT 2 OFFSET 2`); err == nil {
		t.Fatal("Top-N working set silently exceeded the result ceiling")
	}
}

func TestStandaloneSecondaryAndUniqueIndexDDL(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "indexes.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			merchant TEXT NOT NULL,
			id INTEGER NOT NULL,
			sku TEXT NOT NULL,
			status TEXT NOT NULL,
			PRIMARY KEY (merchant, id)
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (merchant, id, sku, status) VALUES
		('shopee', 1, 'SKU-1', 'active'),
		('shopee', 2, 'SKU-2', 'active'),
		('lazada', 3, 'SKU-1', 'disabled')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE INDEX products_status_id_idx ON products (status, id)
	`); err != nil {
		t.Fatal(err)
	}

	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	statement, err := kitdbsql.ParseStatement(`
		SELECT id, sku FROM products WHERE status = 'active' ORDER BY id DESC
	`)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := transaction.schema("products")
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
	access, err := transaction.planRowAccess(schema, 0, conditions, orders)
	if err != nil {
		t.Fatal(err)
	}
	if access.kind != rowAccessSecondary || access.name != "products_status_id_idx" || !access.orderCovered {
		t.Fatalf("access = %#v", access)
	}
	_ = transaction.Rollback()

	selected, err := engine.Execute(ctx, `
		SELECT id, sku FROM products WHERE status = 'active' ORDER BY id DESC
	`)
	if err != nil || len(selected.Rows) != 2 || selected.Rows[0][0] != int64(2) {
		t.Fatalf("indexed SELECT = %#v, %v", selected.Rows, err)
	}
	if _, err := engine.Execute(ctx, `UPDATE products SET status = 'active' WHERE merchant = 'lazada' AND id = 3`); err != nil {
		t.Fatal(err)
	}
	selected, err = engine.Execute(ctx, `SELECT id FROM products WHERE status = 'active' ORDER BY id DESC`)
	if err != nil || len(selected.Rows) != 3 || selected.Rows[0][0] != int64(3) {
		t.Fatalf("updated indexed SELECT = %#v, %v", selected.Rows, err)
	}
	if _, err := engine.Execute(ctx, `DROP INDEX products_status_id_idx`); err != nil {
		t.Fatal(err)
	}
	transaction, err = engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	schema, _ = transaction.schema("products")
	conditions, _ = bindConditions(schema, statement.Select.Conditions, nil)
	orders, _ = bindOrders(schema, statement.Select.Order)
	access, err = transaction.planRowAccess(schema, 0, conditions, orders)
	_ = transaction.Rollback()
	if err != nil || access.name == "products_status_id_idx" {
		t.Fatalf("access after DROP = %#v, %v", access, err)
	}

	if _, err := engine.Execute(ctx, `CREATE UNIQUE INDEX products_sku_key ON products (sku)`); err == nil {
		t.Fatal("duplicate CREATE UNIQUE INDEX unexpectedly succeeded")
	}
	if _, err := engine.Execute(ctx, `UPDATE products SET sku = 'SKU-3' WHERE merchant = 'lazada' AND id = 3`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `CREATE UNIQUE INDEX products_sku_key ON products (sku)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO products (merchant, id, sku, status) VALUES ('tiki', 4, 'SKU-1', 'active')`); err == nil {
		t.Fatal("unique index accepted duplicate")
	}
	if _, err := engine.Execute(ctx, `DROP INDEX products_sku_key`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO products (merchant, id, sku, status) VALUES ('tiki', 4, 'SKU-1', 'active')`); err != nil {
		t.Fatalf("dropped unique index still enforced: %v", err)
	}
}

func TestStandaloneCheckAndForeignKeyConstraints(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "constraints.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE merchants (
			tenant TEXT NOT NULL,
			id INTEGER NOT NULL,
			name TEXT UNIQUE,
			PRIMARY KEY (tenant, id)
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE TABLE products (
			id INTEGER PRIMARY KEY,
			tenant TEXT,
			merchant_id INTEGER,
			price INTEGER NOT NULL CHECK (price >= 0),
			discount INTEGER,
			CONSTRAINT discount_range CHECK (
				discount IS NULL OR (discount >= 0 AND discount <= price)
			),
			CONSTRAINT products_merchant_fk FOREIGN KEY (tenant, merchant_id)
				REFERENCES merchants (tenant, id) ON DELETE RESTRICT
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (id, tenant, merchant_id, price)
		VALUES (1, 'shop', 7, 100)
	`); err == nil {
		t.Fatal("missing foreign target unexpectedly accepted")
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO merchants (tenant, id, name) VALUES ('shop', 7, 'Shop Seven')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (id, tenant, merchant_id, price, discount) VALUES
		(1, 'shop', 7, 100, 10),
		(2, 'shop', 7, 50, 60)
	`); err == nil {
		t.Fatal("CHECK-violating batch unexpectedly accepted")
	}
	counted, err := engine.Execute(ctx, `SELECT COUNT(*) FROM products`)
	if err != nil || counted.Rows[0][0] != int64(0) {
		t.Fatalf("failed batch count = %#v, %v", counted.Rows, err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO products (id, tenant, merchant_id, price, discount)
		VALUES (1, 'shop', 7, 100, 10)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `UPDATE products SET discount = 101 WHERE id = 1`); err == nil {
		t.Fatal("CHECK-violating UPDATE unexpectedly accepted")
	}
	if _, err := engine.Execute(ctx, `DELETE FROM merchants WHERE tenant = 'shop' AND id = 7`); err == nil {
		t.Fatal("referenced parent DELETE unexpectedly accepted")
	}
	if _, err := engine.Execute(ctx, `DELETE FROM products WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `DELETE FROM merchants WHERE tenant = 'shop' AND id = 7`); err != nil {
		t.Fatalf("unreferenced parent DELETE: %v", err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO merchants (tenant, id, name) VALUES ('drop', 1, 'Drop')`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO products (id, tenant, merchant_id, price) VALUES (2, 'drop', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `DROP TABLE merchants`); err == nil {
		t.Fatal("DROP TABLE ignored incoming foreign key")
	}
	if _, err := engine.Execute(ctx, `DROP TABLE products`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `DROP TABLE merchants`); err != nil {
		t.Fatal(err)
	}
	if tables, err := engine.Tables(); err != nil || len(tables) != 0 {
		t.Fatalf("tables after DROP = %#v, %v", tables, err)
	}
	if _, err := engine.Execute(ctx, `DROP TABLE IF EXISTS merchants`); err != nil {
		t.Fatal(err)
	}
}

func TestStandaloneMetadataSafeAlterTablePreservesRowsAndReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alter.kitdb")
	engine, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE accounts (id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			account_id INTEGER REFERENCES accounts(id),
			note TEXT
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO accounts (id, name) VALUES (1, 'first')`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO orders (id, account_id, note) VALUES (1, 1, 'before')`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE accounts ADD COLUMN description TEXT`); err != nil {
		t.Fatal(err)
	}
	row, err := engine.Execute(ctx, `SELECT id, name, description FROM accounts WHERE id = 1`)
	if err != nil || len(row.Rows) != 1 || row.Rows[0][0] != int64(1) || row.Rows[0][1] != "first" || row.Rows[0][2] != nil {
		t.Fatalf("row after ADD COLUMN = %#v, %v", row.Rows, err)
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE accounts ADD COLUMN required TEXT NOT NULL`); err == nil {
		t.Fatal("ADD COLUMN requiring backfill unexpectedly succeeded")
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE accounts RENAME COLUMN id TO account_key`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE accounts RENAME COLUMN name TO label`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO orders (id, account_id, note) VALUES (2, 1, 'after-column-rename')`); err != nil {
		t.Fatalf("foreign key after column rename: %v", err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO accounts (account_key, label) VALUES (2, 'first')`); err == nil {
		t.Fatal("renamed unique field lost enforcement")
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE accounts RENAME TO customers`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO customers (account_key, label) VALUES (2, 'second')`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO orders (id, account_id, note) VALUES (3, 2, 'after-table-rename')`); err != nil {
		t.Fatalf("foreign key after table rename: %v", err)
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE customers DROP COLUMN account_key`); err == nil {
		t.Fatal("DROP COLUMN ignored key/reference dependency")
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE orders DROP COLUMN note`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `SELECT note FROM orders`); err == nil {
		t.Fatal("dropped column remained queryable")
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	reopened, err := engine.Execute(ctx, `SELECT account_key, label FROM customers ORDER BY account_key`)
	if err != nil || len(reopened.Rows) != 2 || reopened.Rows[0][1] != "first" {
		t.Fatalf("reopened ALTER rows = %#v, %v", reopened.Rows, err)
	}
}

func TestStandaloneStreamingAggregatesAndExplain(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "aggregates.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE sales (
			id INTEGER PRIMARY KEY,
			merchant TEXT NOT NULL,
			amount INTEGER NOT NULL,
			discount INTEGER
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO sales (id, merchant, amount, discount) VALUES
		(1, 'shop', 10, NULL),
		(2, 'shop', 30, 5),
		(3, 'mall', 50, 10)
	`); err != nil {
		t.Fatal(err)
	}
	grouped, err := engine.Execute(ctx, `
		SELECT merchant, COUNT(*) AS orders, COUNT(discount) AS discounted,
			SUM(amount) AS revenue, AVG(amount) AS average,
			MIN(amount) AS minimum, MAX(amount) AS maximum
		FROM sales
		GROUP BY merchant
		ORDER BY revenue DESC
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped.Rows) != 2 || grouped.Rows[0][0] != "mall" || grouped.Rows[0][1] != int64(1) ||
		grouped.Rows[0][3] != int64(50) || grouped.Rows[1][0] != "shop" ||
		grouped.Rows[1][1] != int64(2) || grouped.Rows[1][2] != int64(1) ||
		grouped.Rows[1][4] != "20" || grouped.Rows[1][5] != int64(10) || grouped.Rows[1][6] != int64(30) {
		t.Fatalf("grouped aggregates = %#v", grouped.Rows)
	}
	global, err := engine.Execute(ctx, `SELECT COUNT(*) AS rows, SUM(amount) AS total FROM sales`)
	if err != nil || len(global.Rows) != 1 || global.Rows[0][0] != int64(3) || global.Rows[0][1] != int64(90) {
		t.Fatalf("global aggregates = %#v, %v", global.Rows, err)
	}
	if _, err := engine.Execute(ctx, `CREATE INDEX sales_merchant_amount_idx ON sales (merchant, amount)`); err != nil {
		t.Fatal(err)
	}
	explained, err := engine.Execute(ctx, `
		EXPLAIN SELECT id, amount FROM sales
		WHERE merchant = 'shop' ORDER BY amount DESC
	`)
	if err != nil {
		t.Fatal(err)
	}
	if len(explained.Rows) != 1 || explained.Rows[0][1] != "index scan" ||
		!strings.Contains(explained.Rows[0][2].(string), "sales_merchant_amount_idx") ||
		!strings.Contains(explained.Rows[0][2].(string), "estimated_rows=3") {
		t.Fatalf("EXPLAIN = %#v", explained.Rows)
	}
}

func TestStandaloneGroupByWithoutAggregateAndHaving(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "having.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE sales (
			id INTEGER PRIMARY KEY,
			status TEXT NOT NULL,
			amount INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO sales (id, status, amount) VALUES
		(1, 'paid', 40), (2, 'paid', 80), (3, 'pending', 30), (4, 'cancelled', 500)
	`); err != nil {
		t.Fatal(err)
	}

	groups, err := engine.Execute(ctx, `SELECT status FROM sales GROUP BY status ORDER BY status`)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups.Rows) != 3 || groups.Rows[0][0] != "cancelled" || groups.Rows[1][0] != "paid" ||
		groups.Rows[2][0] != "pending" {
		t.Fatalf("GROUP BY rows = %#v", groups.Rows)
	}

	having, err := engine.Execute(ctx, `
		SELECT status, COUNT(*) AS orders, SUM(amount) AS revenue
		FROM sales
		GROUP BY status
		HAVING orders >= $1 AND revenue BETWEEN 100 AND 200
		ORDER BY revenue DESC
	`, int64(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(having.Rows) != 1 || having.Rows[0][0] != "paid" || having.Rows[0][1] != int64(2) ||
		having.Rows[0][2] != int64(120) {
		t.Fatalf("HAVING rows = %#v", having.Rows)
	}

	empty, err := engine.Execute(ctx, `
		SELECT COUNT(*) AS rows FROM sales WHERE id < 0 HAVING rows = 0
	`)
	if err != nil || len(empty.Rows) != 1 || empty.Rows[0][0] != int64(0) {
		t.Fatalf("empty aggregate HAVING = %#v, %v", empty.Rows, err)
	}
	if _, err := engine.Execute(ctx, `
		SELECT status, COUNT(*) AS orders FROM sales GROUP BY status HAVING missing > 0
	`); err == nil {
		t.Fatal("HAVING accepted an unprojected field")
	}
}

func TestStandaloneIndexNestedLoopInnerAndLeftJoin(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "joins.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		CREATE TABLE orders (
			id INTEGER PRIMARY KEY,
			customer_id INTEGER REFERENCES customers(id),
			status TEXT NOT NULL,
			total INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `CREATE INDEX orders_status_id_idx ON orders (status, id)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO customers (id, name) VALUES (1, 'Alice'), (2, 'Bob')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `
		INSERT INTO orders (id, customer_id, status, total) VALUES
		(10, 1, 'active', 100),
		(11, 2, 'disabled', 200),
		(12, NULL, 'active', 50)
	`); err != nil {
		t.Fatal(err)
	}
	inner, err := engine.Execute(ctx, `
		SELECT o.id AS order_id, c.name AS customer, o.total
		FROM orders o
		JOIN customers c ON c.id = o.customer_id
		WHERE o.status = 'active'
		ORDER BY order_id
	`)
	if err != nil || len(inner.Rows) != 1 || inner.Rows[0][0] != int64(10) || inner.Rows[0][1] != "Alice" {
		t.Fatalf("INNER JOIN = %#v, %v", inner.Rows, err)
	}
	left, err := engine.Execute(ctx, `
		SELECT o.id AS order_id, c.name AS customer
		FROM orders o
		LEFT JOIN customers c ON o.customer_id = c.id
		WHERE o.status = 'active'
		ORDER BY order_id
	`)
	if err != nil || len(left.Rows) != 2 || left.Rows[0][1] != "Alice" || left.Rows[1][1] != nil {
		t.Fatalf("LEFT JOIN = %#v, %v", left.Rows, err)
	}
	star, err := engine.Execute(ctx, `
		SELECT c.* FROM orders o JOIN customers c ON c.id = o.customer_id
		WHERE o.id = 10
	`)
	if err != nil || len(star.Columns) != 2 || len(star.Rows) != 1 || star.Rows[0][1] != "Alice" {
		t.Fatalf("qualified star = columns=%#v rows=%#v err=%v", star.Columns, star.Rows, err)
	}
	if _, err := engine.Execute(ctx, `
		SELECT id FROM orders o JOIN customers c ON c.id = o.customer_id
	`); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous JOIN field error = %v", err)
	}
}
