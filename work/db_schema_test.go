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

	// v2: discount becomes INTEGER. A type change is destructive (CAST), so it runs ONLY with the
	// explicit flag → rebuild + CAST.
	v2 := map[string]*ColumnSpec{
		"id":       {kind: "kitid", primary: true},
		"discount": {kind: "integer"},
	}
	db2 := open()
	defer db2.Close()
	migrate(db2, "prices", v2, false, true) // allowDrop=true opts into the destructive rebuild

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

// Safety: WITHOUT the flag, a type change is REFUSED — no silent rebuild/CAST that could lose data.
// The column keeps its old type and its data.
func TestSchemaMigrationRefusesTypeChangeWithoutFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	open := func() *sql.DB {
		d, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	v1 := map[string]*ColumnSpec{"id": {kind: "kitid", primary: true}, "note": {kind: "text"}}
	db1 := open()
	migrate(db1, "notes", v1, false, false)
	if _, err := db1.Exec(`INSERT INTO notes (id, note) VALUES ('x1', 'keep-me')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db1.Close()

	// note: text → integer, but NO flag → must be refused.
	v2 := map[string]*ColumnSpec{"id": {kind: "kitid", primary: true}, "note": {kind: "integer"}}
	db2 := open()
	defer db2.Close()
	migrate(db2, "notes", v2, false, false)

	types, err := tableColumns(db2, "notes")
	if err != nil {
		t.Fatal(err)
	}
	if types["note"] != "TEXT" {
		t.Errorf("type change was applied without the flag: note type = %q, want TEXT (unchanged)", types["note"])
	}
	var note string
	if err := db2.QueryRow(`SELECT note FROM notes WHERE id='x1'`).Scan(&note); err != nil {
		t.Fatalf("data lost: %v", err)
	}
	if note != "keep-me" {
		t.Errorf("note = %q, want keep-me", note)
	}
	var rebuilds int
	db2.QueryRow(`SELECT count(*) FROM _kitwork_migrations WHERE table_name='notes' AND action LIKE 'rebuild%'`).Scan(&rebuilds)
	if rebuilds != 0 {
		t.Errorf("a rebuild ran without the flag (%d recorded)", rebuilds)
	}
}

// ADD COLUMN with a STATIC default backfills existing rows (SQLite applies the DEFAULT to old rows).
func TestSchemaMigrationBackfillsDefaultOnAddColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	open := func() *sql.DB {
		d, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	v1 := map[string]*ColumnSpec{"id": {kind: "kitid", primary: true}, "code": {kind: "text"}}
	db1 := open()
	migrate(db1, "vouchers", v1, false, false)
	if _, err := db1.Exec(`INSERT INTO vouchers (id, code) VALUES ('x1', 'A1')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db1.Close()

	// v2 adds status with a default → the OLD row must get 'active', not NULL.
	v2 := map[string]*ColumnSpec{
		"id":     {kind: "kitid", primary: true},
		"code":   {kind: "text"},
		"status": {kind: "text", hasDefault: true, def: value.New("active")},
	}
	db2 := open()
	defer db2.Close()
	migrate(db2, "vouchers", v2, false, false)

	var status string
	if err := db2.QueryRow(`SELECT status FROM vouchers WHERE id='x1'`).Scan(&status); err != nil {
		t.Fatalf("old row lost: %v", err)
	}
	if status != "active" {
		t.Errorf("existing row not backfilled: status = %q, want active", status)
	}
}

// planMigration classifies the delta (the dry-run's engine): create for a fresh table, nothing when
// up-to-date, and add/retype/drop steps with WillApply reflecting the { drop: true } flag.
func TestMigrationPlanClassifies(t *testing.T) {
	schema := map[string]*ColumnSpec{
		"id":     {kind: "kitid", primary: true},
		"code":   {kind: "text"},
		"amount": {kind: "integer"},
	}

	// Fresh table → one "create" step.
	if steps := planMigration(map[string]string{}, schema, false, false); len(steps) != 1 || steps[0].Action != "create" {
		t.Fatalf("fresh: want [create], got %+v", steps)
	}
	// Exact match → no steps.
	upToDate := map[string]string{"id": "TEXT", "code": "TEXT", "amount": "INTEGER"}
	if steps := planMigration(upToDate, schema, false, false); len(steps) != 0 {
		t.Errorf("up-to-date: want no steps, got %+v", steps)
	}

	// Live table: amount is TEXT (retype), 'old' is extra (drop), 'code' matches.
	current := map[string]string{"id": "TEXT", "code": "TEXT", "amount": "TEXT", "old": "TEXT"}
	byCol := func(steps []planStep) map[string]planStep {
		m := map[string]planStep{}
		for _, s := range steps {
			m[s.Action+":"+s.Column] = s
		}
		return m
	}

	// Without the flag: destructive steps are planned but NOT WillApply.
	noFlag := byCol(planMigration(current, schema, false, false))
	if s := noFlag["retype:amount"]; s.From != "TEXT" || s.To != "INTEGER" || !s.Destructive || s.WillApply {
		t.Errorf("retype:amount (no flag) = %+v", s)
	}
	if s, ok := noFlag["drop:old"]; !ok || s.WillApply {
		t.Errorf("drop:old (no flag) = %+v ok=%v", s, ok)
	}

	// With the flag: destructive steps WillApply.
	for _, s := range planMigration(current, schema, false, true) {
		if s.Destructive && !s.WillApply {
			t.Errorf("with flag, destructive step should WillApply: %+v", s)
		}
	}

	// A new column → an "add" step that always applies.
	addSteps := planMigration(map[string]string{"id": "TEXT"},
		map[string]*ColumnSpec{"id": {kind: "kitid", primary: true}, "brand": {kind: "text"}}, false, false)
	if len(addSteps) != 1 || addSteps[0].Action != "add" || addSteps[0].Column != "brand" || !addSteps[0].WillApply {
		t.Fatalf("add: want [add brand], got %+v", addSteps)
	}
}

// The type vocabulary end to end through a real tenant VM: bool/array/json/enum + auto now()/year().
// Proves COERCION both ways — writes store 0/1 and JSON strings, reads come back as booleans/objects.
func TestSchemaColumnTypesRoundTrip(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-types-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	router := `import { router, database } from "kitwork";` + "\n" +
		`const { turso, id, text, int, float, bool, enum, now, year, jsonb, array } = database;` + "\n" +
		`const items = {` + "\n" +
		`  id: id(),` + "\n" +
		`  name: text().notNull(),` + "\n" +
		`  active: bool().default(true),` + "\n" +
		`  count: int().default(0),` + "\n" +
		`  rating: float().default(4.5),` + "\n" +
		`  status: enum("draft", "published").default("draft"),` + "\n" +
		`  tags: array().default([]),` + "\n" +
		`  meta: jsonb().default({}),` + "\n" +
		`  created: now(),` + "\n" +
		`  fy: year()` + "\n" +
		`};` + "\n" +
		`const db = turso("app.db", { items });` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  db.items.create({ name: "A", active: false, tags: ["x","y"], meta: { k: 1 }, status: "published" });` + "\n" +
		`  const row = db.items.where("name", "=", "A").first();` + "\n" +
		`  return ctx.json({` + "\n" +
		`    active: row.active,` + "\n" +
		`    tagsLen: row.tags.length,` + "\n" +
		`    metaK: row.meta.k,` + "\n" +
		`    status: row.status,` + "\n" +
		`    fy: row.fy,` + "\n" +
		`    hasCreated: row.created != "",` + "\n" +
		`    hasId: row.id != ""` + "\n" +
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
		t.Fatalf("status %d, body: %s", rec.Code, body)
	}

	checks := map[string]string{
		`"active":false`:       "bool read-coercion (stored 0/1) failed",
		`"tagsLen":2`:          "array read-coercion (JSON string → array) failed",
		`"metaK":1`:            "json read-coercion (JSON string → object) failed",
		`"status":"published"`: "enum value round-trip failed",
		`"fy":2026`:            "year() did not auto-fill the current year",
		`"hasCreated":true`:    "now() did not auto-fill a timestamp",
		`"hasId":true`:         "id() did not generate a kitid",
	}
	for want, msg := range checks {
		if !strings.Contains(body, want) {
			t.Errorf("%s — want %s in body: %s", msg, want, body)
		}
	}
}

// Enum validation happens at write time (engine-agnostic, no CHECK constraint): a value outside the set
// is rejected with a clear message.
func TestSchemaEnumRejectsInvalid(t *testing.T) {
	columns := map[string]*ColumnSpec{
		"status": {kind: "enum", enumVals: []string{"draft", "published"}},
	}
	_, errMsg := fillRow(columns, map[string]value.Value{"status": value.New("bogus")})
	if errMsg == "" {
		t.Fatal("invalid enum value was accepted")
	}
	if !strings.Contains(errMsg, "status") || !strings.Contains(errMsg, "bogus") {
		t.Errorf("enum error should name the column and value, got: %q", errMsg)
	}
	// A valid value passes.
	if _, errMsg := fillRow(columns, map[string]value.Value{"status": value.New("draft")}); errMsg != "" {
		t.Errorf("valid enum value rejected: %q", errMsg)
	}
}

// A schema declared during Tenant.Run (no request needed) is discoverable via MigrationPlansFor — the
// hook kitwork check uses to preview a deploy. A fresh tenant (no .data DB yet) → a "create" plan.
func TestMigrationPlansForPreviewsSchema(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "planhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";` + "\n" +
		`const { turso, kitid, text } = database;` + "\n" +
		`const vouchers = { id: kitid().primaryKey(), code: text() };` + "\n" +
		`const db = turso("app.db", { vouchers });` + "\n" +
		`router.get((ctx) => ctx.json({ ok: 1 }));`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "planhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	plans := MigrationPlansFor(tenant)
	var vouchers *TablePlan
	for i := range plans {
		if plans[i].Table == "vouchers" && plans[i].Engine == "turso" {
			vouchers = &plans[i]
		}
	}
	if vouchers == nil {
		t.Fatalf("no plan for vouchers; got %+v", plans)
	}
	if len(vouchers.Steps) != 1 || vouchers.Steps[0].Action != "create" {
		t.Errorf("fresh vouchers plan = %+v, want [create]", vouchers.Steps)
	}
	if lines := vouchers.Lines(); len(lines) != 1 || !strings.Contains(lines[0], "create table") {
		t.Errorf("Lines() = %v, want a create line", lines)
	}
}

// The dry run (planMigration / db.plan()) must be PURE: computing a plan never changes the table.
func TestMigrationPlanDoesNotMutate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migrate(db, "t", map[string]*ColumnSpec{"id": {kind: "kitid", primary: true}, "amount": {kind: "text"}}, false, false)
	if _, err := db.Exec(`INSERT INTO t (id, amount) VALUES ('x1','40000')`); err != nil {
		t.Fatal(err)
	}

	// Plan an evolved schema (retype amount) WITHOUT applying.
	v2 := map[string]*ColumnSpec{"id": {kind: "kitid", primary: true}, "amount": {kind: "integer"}}
	current, _ := tableColumns(db, "t")
	_ = planMigration(current, v2, false, true) // even with the flag, planning must not apply

	after, _ := tableColumns(db, "t")
	if after["amount"] != "TEXT" {
		t.Errorf("planning mutated the schema: amount = %q, want TEXT (unchanged)", after["amount"])
	}
	var amt string
	if err := db.QueryRow(`SELECT amount FROM t WHERE id='x1'`).Scan(&amt); err != nil || amt != "40000" {
		t.Errorf("planning mutated data: amount=%q err=%v", amt, err)
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

// The write-path methods on db.<table> — update(), exists(), delete() — driven end to end through the
// VM, modelled on the kiturl.com URL-shortener redirect handler that first exposed them missing:
// where(code).first() → update({clicks:+1}) → read back. Before the fix db.<table>.update() did not
// exist, so the VM's reflection found no method and the click counter silently stayed 0; exists()
// likewise returned undefined. The afterClicks==1 assertion pins exactly that regression.
func TestSchemaTableWritePathThroughVM(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	router := `import { router, database } from "kitwork";
const { turso, kitid, text, int, datetime } = database;
const links = {
  id: kitid().primaryKey(),
  code: text().notNull().unique(),
  original_url: text().notNull(),
  clicks: int().default(0),
  created_at: datetime().defaultNow()
};
const db = turso("app.db", { links: links });
router.get((ctx) => {
  db.links.create({ code: "kitwork", original_url: "https://github.com/kitwork", clicks: 0 });
  db.links.create({ code: "gone", original_url: "https://x.example", clicks: 0 });
  const before = db.links.where("code", "=", "kitwork").first();
  db.links.where("code", "=", "kitwork").update({ clicks: (before.clicks || 0) + 1 });
  const after = db.links.where("code", "=", "kitwork").first();
  db.links.where("code", "=", "gone").delete();
  return ctx.json({
    beforeClicks: before.clicks,
    afterClicks: after.clicks,
    hasKit: db.links.where("code", "=", "kitwork").exists(),
    hasMissing: db.links.where("code", "=", "does-not-exist").exists(),
    hasGone: db.links.where("code", "=", "gone").exists(),
    total: db.links.count()
  });
});`
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

	assert := func(want string) {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s — body: %s", want, body)
		}
	}
	assert(`"beforeClicks":0`)
	assert(`"afterClicks":1`)  // update() wrote — the regression guard
	assert(`"hasKit":true`)    // exists() true for a present row
	assert(`"hasMissing":false`)
	assert(`"hasGone":false`)  // delete() removed the row
	assert(`"total":1`)        // 2 created, 1 deleted
}
