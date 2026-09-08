package work

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestSQLiteInspectorCatalogAndPagedObjects(t *testing.T) {
	db, closeDatabase := newSQLiteInspectorTestDatabase(t, "catalog.db")
	defer closeDatabase()

	mustSQLiteInspectorExec(t, db, `PRAGMA foreign_keys=ON`)
	mustSQLiteInspectorExec(t, db, `CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		email TEXT NOT NULL UNIQUE,
		status TEXT NOT NULL DEFAULT 'active'
	)`)
	mustSQLiteInspectorExec(t, db, `CREATE TABLE orders (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON UPDATE CASCADE ON DELETE RESTRICT,
		total REAL
	)`)
	mustSQLiteInspectorExec(t, db, `CREATE INDEX orders_user_id_idx ON orders(user_id)`)
	mustSQLiteInspectorExec(t, db, `CREATE VIEW active_users AS SELECT id, email, status FROM users WHERE status = 'active'`)
	mustSQLiteInspectorExec(t, db, `CREATE TABLE "quote"" table" ("odd"" id" INTEGER PRIMARY KEY, payload BLOB)`)
	mustSQLiteInspectorExec(t, db, `CREATE TABLE sqliteXvisible (id INTEGER PRIMARY KEY)`)
	mustSQLiteInspectorExec(t, db, `CREATE TABLE generated_values (base INTEGER, doubled INTEGER GENERATED ALWAYS AS (base * 2) STORED)`)
	mustSQLiteInspectorExec(t, db, `CREATE TABLE rowid_shadow (rowid TEXT, value TEXT)`)
	mustSQLiteInspectorExec(t, db, `CREATE TABLE all_rowid_aliases_shadowed (rowid TEXT, _rowid_ TEXT, oid TEXT, value TEXT)`)
	// AUTOINCREMENT creates sqlite_sequence, which must never appear in the public catalog.
	mustSQLiteInspectorExec(t, db, `CREATE TABLE sequence_owner (id INTEGER PRIMARY KEY AUTOINCREMENT)`)
	if virtual := db.Exec(`CREATE VIRTUAL TABLE hidden_search USING rtree(id, minimum, maximum)`); virtual.K == value.Invalid {
		t.Fatalf("create virtual table fixture: %s", virtual.String())
	}

	for _, statement := range []string{
		`INSERT INTO users (id, email, status) VALUES (3, 'c@example.test', 'active')`,
		`INSERT INTO users (id, email, status) VALUES (1, 'a@example.test', 'active')`,
		`INSERT INTO users (id, email, status) VALUES (2, 'b@example.test', 'disabled')`,
		`INSERT INTO orders (id, user_id, total) VALUES (10, 1, 14.5)`,
		`INSERT INTO "quote"" table" ("odd"" id", payload) VALUES (7, X'CAFE')`,
		`INSERT INTO generated_values (base) VALUES (4)`,
		`INSERT INTO rowid_shadow (rowid, value) VALUES ('second', 'inserted first')`,
		`INSERT INTO rowid_shadow (rowid, value) VALUES ('first', 'inserted second')`,
	} {
		mustSQLiteInspectorExec(t, db, statement)
	}

	catalog := sqliteInspectorResultMap(t, db.ReadCatalog())
	if catalog["ok"] != true {
		t.Fatalf("catalog failed: %#v", catalog)
	}
	version, ok := catalog["catalogVersion"].(float64)
	if !ok || version <= 0 || math.Trunc(version) != version {
		t.Fatalf("catalog version = %#v", catalog["catalogVersion"])
	}
	objects := sqliteInspectorObjectMap(t, catalog["objects"])
	for _, required := range []string{"table-users", "table-orders", "table-quote\" table", "table-sqliteXvisible", "table-generated_values", "view-active_users"} {
		if _, exists := objects[required]; !exists {
			t.Errorf("catalog missing %q: %#v", required, objects)
		}
	}
	for _, excluded := range []string{"table-sqlite_sequence", "table-hidden_search", "table-hidden_search_node", "table-hidden_search_parent", "table-hidden_search_rowid"} {
		if _, exists := objects[excluded]; exists {
			t.Errorf("catalog exposed internal/virtual object %q", excluded)
		}
	}

	users := objects["table-users"]
	columns := sqliteInspectorMaps(t, users["columns"])
	if len(columns) != 3 || columns[0]["name"] != "id" || columns[0]["keyLabel"] != "PRIMARY KEY" || columns[0]["nullable"] != "No" {
		t.Fatalf("users columns = %#v", columns)
	}
	if columns[2]["defaultValue"] != "'active'" {
		t.Fatalf("status default = %#v", columns[2])
	}
	indexes := sqliteInspectorMaps(t, users["indexes"])
	if !sqliteInspectorHasMetadata(indexes, "kind", "Primary key") || !sqliteInspectorHasMetadata(indexes, "kind", "Unique") {
		t.Fatalf("users indexes = %#v", indexes)
	}
	orders := objects["table-orders"]
	foreignKeys := sqliteInspectorMaps(t, orders["foreignKeys"])
	if len(foreignKeys) != 1 || foreignKeys[0]["columns"] != "user_id" || foreignKeys[0]["target"] != "main.users (id)" || foreignKeys[0]["onUpdate"] != "CASCADE" || foreignKeys[0]["onDelete"] != "RESTRICT" {
		t.Fatalf("orders foreign keys = %#v", foreignKeys)
	}
	generated := sqliteInspectorMaps(t, objects["table-generated_values"]["columns"])
	if len(generated) != 2 || generated[1]["generated"] != true {
		t.Fatalf("generated columns = %#v", generated)
	}

	page := sqliteInspectorResultMap(t, db.ReadTablePage(sqliteInspectorPageValue("table", "users", int(version), 1, 2)))
	if page["ok"] != true || page["totalRows"] != float64(3) || page["pageCount"] != float64(2) || page["pageStart"] != float64(1) || page["pageEnd"] != float64(2) || page["hasNextPage"] != true || page["hasPreviousPage"] != false {
		t.Fatalf("users page metadata = %#v", page)
	}
	pageRows := page["rows"].([]any)
	if len(pageRows) != 2 || pageRows[0].([]any)[0] != float64(1) || pageRows[1].([]any)[0] != float64(2) {
		t.Fatalf("users rows were not ordered by primary key: %#v", pageRows)
	}
	secondPage := sqliteInspectorResultMap(t, db.ReadTablePage(sqliteInspectorPageValue("table", "users", int(version), 2, 2)))
	if secondPage["pageStart"] != float64(3) || secondPage["pageEnd"] != float64(3) || secondPage["hasPreviousPage"] != true || secondPage["hasNextPage"] != false {
		t.Fatalf("users second page = %#v", secondPage)
	}
	viewPage := sqliteInspectorResultMap(t, db.ReadTablePage(sqliteInspectorPageValue("view", "active_users", int(version), 1, 120)))
	if viewPage["ok"] != true || viewPage["type"] != "view" || viewPage["totalRows"] != float64(2) {
		t.Fatalf("view page = %#v", viewPage)
	}
	quotedPage := sqliteInspectorResultMap(t, db.ReadTablePage(sqliteInspectorPageValue("table", `quote" table`, int(version), 1, 10)))
	quotedRows := quotedPage["rows"].([]any)
	if quotedPage["ok"] != true || len(quotedRows) != 1 || quotedRows[0].([]any)[0] != float64(7) {
		t.Fatalf("quoted identifier page = %#v", quotedPage)
	}
	blob := quotedRows[0].([]any)[1].(map[string]any)
	if blob["type"] != "blob" || blob["base64"] != "yv4=" || blob["bytes"] != float64(2) {
		t.Fatalf("blob envelope = %#v", blob)
	}
	rowIDPage := sqliteInspectorResultMap(t, db.ReadTablePage(sqliteInspectorPageValue("table", "rowid_shadow", int(version), 1, 10)))
	rowIDRows := rowIDPage["rows"].([]any)
	if rowIDPage["ok"] != true || len(rowIDRows) != 2 || rowIDRows[0].([]any)[0] != "second" {
		t.Fatalf("unshadowed _rowid_ ordering = %#v", rowIDPage)
	}
	unstable := sqliteInspectorResultMap(t, db.ReadTablePage(sqliteInspectorPageValue("table", "all_rowid_aliases_shadowed", int(version), 1, 10)))
	if unstable["ok"] != false || unstable["code"] != "UNSTABLE_ORDER" {
		t.Fatalf("fully shadowed rowid table = %#v", unstable)
	}

	// Inspector state is connection-local and must be restored before the pooled connection is reused.
	mustSQLiteInspectorExec(t, db, `INSERT INTO users (id, email, status) VALUES (4, 'after@example.test', 'active')`)
}

func TestSQLiteInspectorRejectsInvalidStaleAndNonCatalogRequests(t *testing.T) {
	db, closeDatabase := newSQLiteInspectorTestDatabase(t, "policy.db")
	defer closeDatabase()
	mustSQLiteInspectorExec(t, db, `CREATE TABLE users (id INTEGER PRIMARY KEY, label TEXT)`)
	catalog := sqliteInspectorResultMap(t, db.ReadCatalog())
	version := int(catalog["catalogVersion"].(float64))

	invalid := []value.Value{
		value.New("users"),
		value.New(map[string]any{"type": "table", "name": "users", "catalogVersion": version, "page": 1}),
		value.New(map[string]any{"type": "table", "name": "users", "catalogVersion": version, "page": 1, "pageSize": 10, "sql": "SELECT 1"}),
		sqliteInspectorPageValue("index", "users", version, 1, 10),
		sqliteInspectorPageValue("table", "", version, 1, 10),
		sqliteInspectorPageValue("table", "users", version, 0, 10),
		sqliteInspectorPageValue("table", "users", version, 1, sqliteReadQueryMaxRows+1),
		sqliteInspectorPageValue("table", "users", version, 10_000, sqliteReadQueryMaxRows),
		value.New(map[string]any{"type": "table", "name": "users", "catalogVersion": version, "page": 1.5, "pageSize": 10}),
	}
	for index, request := range invalid {
		result := sqliteInspectorResultMap(t, db.ReadTablePage(request))
		if result["ok"] != false || result["code"] != "INVALID_REQUEST" {
			t.Errorf("invalid request %d = %#v", index, result)
		}
	}

	for _, request := range []value.Value{
		sqliteInspectorPageValue("view", "users", version, 1, 10),
		sqliteInspectorPageValue("table", `users" UNION SELECT 1 --`, version, 1, 10),
		sqliteInspectorPageValue("table", "sqlite_schema", version, 1, 10),
	} {
		result := sqliteInspectorResultMap(t, db.ReadTablePage(request))
		if result["code"] != "OBJECT_NOT_FOUND" {
			t.Errorf("non-catalog request = %#v", result)
		}
	}

	mustSQLiteInspectorExec(t, db, `CREATE TABLE later (id INTEGER PRIMARY KEY)`)
	stale := sqliteInspectorResultMap(t, db.ReadTablePage(sqliteInspectorPageValue("table", "users", version, 1, 10)))
	if stale["ok"] != false || stale["code"] != "SCHEMA_CHANGED" {
		t.Fatalf("stale catalog request = %#v", stale)
	}
}

func TestSQLiteInspectorRestoresExactConnectionPolicy(t *testing.T) {
	db, closeDatabase := newSQLiteInspectorTestDatabase(t, "connection-policy.db")
	defer closeDatabase()
	mustSQLiteInspectorExec(t, db, `CREATE TABLE users (id INTEGER PRIMARY KEY)`)
	sqlDB := db.db()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA trusted_schema=OFF`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	if result := sqliteInspectorResultMap(t, db.ReadCatalog()); result["ok"] != true {
		t.Fatalf("catalog = %#v", result)
	}
	conn, err = sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var queryOnly, trustedSchema int
	if err := conn.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, `PRAGMA trusted_schema`).Scan(&trustedSchema); err != nil {
		t.Fatal(err)
	}
	if queryOnly != 1 || trustedSchema != 0 {
		t.Fatalf("connection policy changed: query_only=%d trusted_schema=%d", queryOnly, trustedSchema)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA trusted_schema=ON`); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteInspectorCellAndPageBoundsFailClosed(t *testing.T) {
	if _, _, ok := sqliteReadQueryCell(math.Inf(1)); ok {
		t.Fatal("non-finite float was accepted")
	}
	if _, _, ok := sqliteReadQueryCell(string([]byte{0xff})); ok {
		t.Fatal("invalid UTF-8 string was accepted")
	}
	largeInteger, _, ok := sqliteReadQueryCell(int64(1<<62 + 1))
	if !ok || largeInteger != "4611686018427387905" {
		t.Fatalf("large integer cell = %#v, %v", largeInteger, ok)
	}

	db, closeDatabase := newSQLiteInspectorTestDatabase(t, "bounds.db")
	defer closeDatabase()
	mustSQLiteInspectorExec(t, db, `CREATE TABLE records (id INTEGER PRIMARY KEY, payload BLOB)`)
	tooLarge := make([]byte, sqliteReadQueryMaxCellBytes+1)
	if result := db.Exec(`INSERT INTO records (id, payload) VALUES (?, ?)`, value.New(1), value.New(tooLarge)); result.K == value.Invalid {
		t.Fatal(result.String())
	}
	catalog := sqliteInspectorResultMap(t, db.ReadCatalog())
	version := int(catalog["catalogVersion"].(float64))
	page := sqliteInspectorResultMap(t, db.ReadTablePage(sqliteInspectorPageValue("table", "records", version, 1, 1)))
	if page["ok"] != false || page["code"] != "RESULT_TOO_LARGE" {
		t.Fatalf("oversized cell page = %#v", page)
	}
}

func newSQLiteInspectorTestDatabase(t *testing.T, name string) (*SQLite, func()) {
	t.Helper()
	root := t.TempDir()
	tenant := NewTenant(root, "localhost")
	db := sqliteFor(tenant, name)
	return db, tenant.Close
}

func mustSQLiteInspectorExec(t *testing.T, db *SQLite, statement string) {
	t.Helper()
	if result := db.Exec(statement); result.K == value.Invalid {
		t.Fatalf("exec %q: %s", statement, result.String())
	}
}

func sqliteInspectorResultMap(t *testing.T, result value.Value) map[string]any {
	t.Helper()
	payload, ok := result.Interface().(map[string]any)
	if !ok {
		t.Fatalf("result = %T %#v", result.Interface(), result.Interface())
	}
	return payload
}

func sqliteInspectorPageValue(kind, name string, catalogVersion, page, pageSize int) value.Value {
	return value.New(map[string]any{
		"type": kind, "name": name, "catalogVersion": catalogVersion, "page": page, "pageSize": pageSize,
	})
}

func sqliteInspectorMaps(t *testing.T, input any) []map[string]any {
	t.Helper()
	items, ok := input.([]any)
	if !ok {
		t.Fatalf("metadata = %T %#v", input, input)
	}
	result := make([]map[string]any, len(items))
	for index, item := range items {
		result[index], ok = item.(map[string]any)
		if !ok {
			t.Fatalf("metadata item = %T %#v", item, item)
		}
	}
	return result
}

func sqliteInspectorObjectMap(t *testing.T, input any) map[string]map[string]any {
	t.Helper()
	result := make(map[string]map[string]any)
	for _, object := range sqliteInspectorMaps(t, input) {
		id, ok := object["id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			t.Fatalf("catalog object id = %#v", object)
		}
		result[id] = object
	}
	return result
}

func sqliteInspectorHasMetadata(items []map[string]any, field, expected string) bool {
	for _, item := range items {
		if item[field] == expected {
			return true
		}
	}
	return false
}
