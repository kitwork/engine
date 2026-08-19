//go:build turso

package work

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/database"
	"github.com/kitwork/engine/value"
	_ "modernc.org/sqlite"
)

// The schema-DSL spike end to end through a real tenant VM: define a table with the kitwork/db column
// builders, defineDb() it, then drive db.<table> in a handler. Asserts the four things that were in
// question — db.<table> resolves, create() auto-fills the kitid PK + defaults, a query round-trips,
// and an unknown column fails loudly instead of returning an empty result.
func TestSchemaDbTenantEntry(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-schema-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	router := `import { router, database } from "kitwork";` + "\n" +
		`const { turso, kitid, text, int, datetime } = database;` + "\n" +
		`const vouchers = {` + "\n" +
		`  id: kitid().primaryKey(),` + "\n" +
		`  code: text().notNull().unique(),` + "\n" +
		`  title: text().notNull(),` + "\n" +
		`  discount: int().default(0),` + "\n" +
		`  status: text().default("active"),` + "\n" +
		`  created_at: datetime().defaultNow()` + "\n" +
		`};` + "\n" +
		`const db = turso("app.db", { vouchers: vouchers });` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  db.vouchers.create({ code: "HD169K40", title: "Giam 40K", discount: 40000 });` + "\n" +
		`  const found = db.vouchers.where("code", "=", "HD169K40").first();` + "\n" +
		`  const active = db.vouchers.where("status", "=", "active").list();` + "\n" +
		`  return ctx.json({` + "\n" +
		`    foundCode: found.code,` + "\n" +
		`    foundStatus: found.status,` + "\n" +
		`    foundId: found.id,` + "\n" +
		`    activeCount: active.length` + "\n" +
		`  });` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("route status %d, body: %s", rec.Code, body)
	}

	// db.<table> resolved and the round-trip worked.
	if !strings.Contains(body, `"foundCode":"HD169K40"`) {
		t.Errorf("db.vouchers query round-trip failed — body: %s", body)
	}
	// A default the caller never passed was applied.
	if !strings.Contains(body, `"foundStatus":"active"`) {
		t.Errorf("schema default (status) was not applied on create — body: %s", body)
	}
	// The kitid primary key was generated (non-empty), not left blank.
	if strings.Contains(body, `"foundId":""`) || !strings.Contains(body, `"foundId":"`) {
		t.Errorf("kitid primary key was not generated on create — body: %s", body)
	}
	// One row, found by the status query.
	if !strings.Contains(body, `"activeCount":1`) {
		t.Errorf("where/list on schema table failed — body: %s", body)
	}

	if _, err := os.Stat(filepath.Join(dir, ".data", "app.db")); err != nil {
		t.Errorf("schema database not created at .data/app.db: %v", err)
	}
}

// Unknown-column validation, unit-tested without the VM: a bad column short-circuits to an in-band
// Invalid naming the column (the handler then bubbles it as a loud 500 — an unknown column is a
// programming error, so failing loudly is the intended behavior).
func TestSchemaColumnValidationRejectsUnknown(t *testing.T) {
	st := &SchemaTable{table: "vouchers", columns: map[string]*ColumnSpec{"status": {kind: "text"}}}
	res := st.Where(value.New("statuss"), value.New("="), value.New("x")).List()
	if res.K != value.Invalid {
		t.Fatalf("unknown column should yield an Invalid, got kind %v", res.K)
	}
	if msg := fmt.Sprint(res.V); !strings.Contains(msg, "statuss") {
		t.Errorf("error should name the offending column, got: %q", msg)
	}
}

// database.define(...) — the SHARED world: two apps write to ONE physical table, each sees only its
// own rows (identity scope), and the schema still fills kitid + defaults on top. Uses in-memory sqlite
// as database.System, exactly as entity_scope_test.go does (the builder emits the same SQL for the
// shared Postgres).
func TestEntitySchemaSharedTableIsolatesByIdentity(t *testing.T) {
	shared, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	shared.SetMaxOpenConns(1) // one in-memory database across every op
	prev := database.System
	database.System = shared
	t.Cleanup(func() { database.System = prev; shared.Close() })

	columns := map[string]*ColumnSpec{
		"id":     {kind: "kitid", primary: true},
		"code":   {kind: "text", notNull: true},
		"status": {kind: "text", hasDefault: true, def: value.New("active")},
	}
	table := func(identity string) *EntityTable {
		return &EntityTable{tenant: tenantFor(identity), table: "vouchers", columns: columns}
	}
	row := func(code string) value.Value {
		return value.New(map[string]value.Value{"code": value.New(code)})
	}

	// Two apps write to the SAME physical table.
	table("acme").Create(row("A1"))
	table("acme").Create(row("A2"))
	table("victim").Create(row("V1"))

	// Each app reads only its own rows.
	acme := table("acme").List().String()
	if strings.Contains(acme, "V1") {
		t.Fatalf("cross-tenant leak: acme saw victim's row:\n%s", acme)
	}
	if !strings.Contains(acme, "A1") || !strings.Contains(acme, "A2") {
		t.Errorf("acme did not see its own rows:\n%s", acme)
	}
	if n := int(table("acme").Count().N); n != 2 {
		t.Errorf("acme count = %d, want 2", n)
	}
	if n := int(table("victim").Count().N); n != 1 {
		t.Errorf("victim count = %d, want 1", n)
	}

	// Schema still fills defaults + kitid on the shared table.
	first := table("acme").Where(value.New("code"), value.New("="), value.New("A1")).First()
	if got := first.Get("status").String(); got != "active" {
		t.Errorf("schema default not applied on shared create: status = %q", got)
	}
	if first.Get("id").String() == "" {
		t.Error("kitid not generated on shared create")
	}
}

// Real migration, the headline: a table created under schema v1 + a seeded row, then migrated to a v2
// schema that ADDS columns → the new columns appear (ALTER ADD), the OLD ROW SURVIVES, and the history
// is recorded. This is what makes it a migration and not blind CREATE IF NOT EXISTS.
func TestSchemaMigrationEvolvesTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	open := func() *sql.DB {
		d, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(2000)")
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	// v1: id + code. Create + seed a row.
	v1 := map[string]*ColumnSpec{
		"id":   {kind: "kitid", primary: true},
		"code": {kind: "text", notNull: true},
	}
	db1 := open()
	migrate(db1, "vouchers", v1, false, false)
	if _, err := db1.Exec(`INSERT INTO vouchers (id, code) VALUES ('x1', 'A1')`); err != nil {
		t.Fatalf("seed v1 row: %v", err)
	}
	db1.Close()

	// v2: adds discount + status. Migrate the EXISTING table.
	v2 := map[string]*ColumnSpec{
		"id":       {kind: "kitid", primary: true},
		"code":     {kind: "text", notNull: true},
		"discount": {kind: "integer"},
		"status":   {kind: "text"},
	}
	db2 := open()
	defer db2.Close()
	migrate(db2, "vouchers", v2, false, false)

	// 1) old data survives.
	var code string
	if err := db2.QueryRow(`SELECT code FROM vouchers WHERE id='x1'`).Scan(&code); err != nil {
		t.Fatalf("old row lost after ALTER: %v", err)
	}
	if code != "A1" {
		t.Errorf("code = %q, want A1", code)
	}
	// 2) the new column is really there and usable.
	if _, err := db2.Exec(`UPDATE vouchers SET discount = 40000 WHERE id='x1'`); err != nil {
		t.Fatalf("new column not added by migration: %v", err)
	}
	var discount int
	if err := db2.QueryRow(`SELECT discount FROM vouchers WHERE id='x1'`).Scan(&discount); err != nil {
		t.Fatal(err)
	}
	if discount != 40000 {
		t.Errorf("discount = %d, want 40000", discount)
	}
	// 3) the history recorded the create + the two adds.
	var n int
	if err := db2.QueryRow(`SELECT count(*) FROM _kitwork_migrations WHERE table_name='vouchers'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Errorf("migration history = %d rows, want >=3 (create + 2 adds)", n)
	}
}

// The data-safety guarantee: a column that exists in the DB but is NOT in the schema is left intact —
// migration never drops data.
func TestSchemaMigrationNeverDropsColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE vouchers (id TEXT PRIMARY KEY, code TEXT, secret TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO vouchers (id, code, secret) VALUES ('x1','A1','keep-me')`); err != nil {
		t.Fatal(err)
	}

	// Schema omits `secret`. Migration must NOT drop it.
	schema := map[string]*ColumnSpec{
		"id":   {kind: "kitid", primary: true},
		"code": {kind: "text"},
	}
	migrate(db, "vouchers", schema, false, false)

	var secret string
	if err := db.QueryRow(`SELECT secret FROM vouchers WHERE id='x1'`).Scan(&secret); err != nil {
		t.Fatalf("migration dropped a column not in the schema — data loss: %v", err)
	}
	if secret != "keep-me" {
		t.Errorf("secret = %q, want keep-me", secret)
	}
}

// Rebuild-table on a TYPE CHANGE: a TEXT column becomes INTEGER. SQLite can't ALTER a type, so migrate
// rebuilds — and the data must survive, CAST across. This is the headline of the rebuild engine.
func TestSchemaMigrationRebuildsOnTypeChange(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	open := func() *sql.DB {
		d, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	// v1: discount is TEXT. Seed a numeric-looking string.
	v1 := map[string]*ColumnSpec{
		"id":       {kind: "kitid", primary: true},
		"discount": {kind: "text"},
	}
	db1 := open()
	migrate(db1, "prices", v1, false, false)
	if _, err := db1.Exec(`INSERT INTO prices (id, discount) VALUES ('x1', '40000')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db1.Close()

	// v2: discount becomes INTEGER → rebuild + CAST.
	v2 := map[string]*ColumnSpec{
		"id":       {kind: "kitid", primary: true},
		"discount": {kind: "integer"},
	}
	db2 := open()
	defer db2.Close()
	migrate(db2, "prices", v2, false, false)

	// The column is now INTEGER...
	types, err := tableColumns(db2, "prices")
	if err != nil {
		t.Fatal(err)
	}
	if types["discount"] != "INTEGER" {
		t.Errorf("discount type after rebuild = %q, want INTEGER", types["discount"])
	}
	// ...and the data survived, CAST to a real integer.
	var discount int
	if err := db2.QueryRow(`SELECT discount FROM prices WHERE id='x1'`).Scan(&discount); err != nil {
		t.Fatalf("row lost in rebuild: %v", err)
	}
	if discount != 40000 {
		t.Errorf("discount after CAST = %d, want 40000", discount)
	}
	var n int
	db2.QueryRow(`SELECT count(*) FROM _kitwork_migrations WHERE table_name='prices' AND action LIKE 'rebuild-table%'`).Scan(&n)
	if n < 1 {
		t.Error("rebuild-table not recorded in migration history")
	}
}

// Drop only happens with the EXPLICIT flag. With allowDrop=true the extra column is rebuilt away (and
// its data goes with it — that is the point of the opt-in); with allowDrop=false it is preserved (the
// TestSchemaMigrationNeverDropsColumn case).
func TestSchemaMigrationDropsColumnOnlyWithFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE vouchers (id TEXT PRIMARY KEY, code TEXT, secret TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO vouchers (id, code, secret) VALUES ('x1','A1','drop-me')`); err != nil {
		t.Fatal(err)
	}

	schema := map[string]*ColumnSpec{
		"id":   {kind: "kitid", primary: true},
		"code": {kind: "text"},
	}
	migrate(db, "vouchers", schema, false, true) // allowDrop = true

	// secret is gone from the schema of the rebuilt table...
	types, err := tableColumns(db, "vouchers")
	if err != nil {
		t.Fatal(err)
	}
	if _, still := types["secret"]; still {
		t.Error("allowDrop=true did not drop the extra column")
	}
	// ...but code + its row survived the rebuild.
	var code string
	if err := db.QueryRow(`SELECT code FROM vouchers WHERE id='x1'`).Scan(&code); err != nil {
		t.Fatalf("row lost during drop-rebuild: %v", err)
	}
	if code != "A1" {
		t.Errorf("code = %q, want A1", code)
	}
}
