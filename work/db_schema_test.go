package work

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
		`const { sqlite, kitid, text, int, datetime } = database;` + "\n" +
		`const vouchers = {` + "\n" +
		`  id: kitid().primaryKey(),` + "\n" +
		`  code: text().notNull().unique(),` + "\n" +
		`  title: text().notNull(),` + "\n" +
		`  discount: int().default(0),` + "\n" +
		`  status: text().default("active"),` + "\n" +
		`  created_at: datetime().defaultNow()` + "\n" +
		`};` + "\n" +
		`const db = sqlite("app.db", { vouchers: vouchers });` + "\n" +
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
	defer tenant.Close()
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

// The type vocabulary end to end through a real tenant VM: bool/array/json/choice + auto now()/year().
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
		`const { sqlite, id, text, int, float, bool, choice, now, year, jsonb, array } = database;` + "\n" +
		`const items = {` + "\n" +
		`  id: id(),` + "\n" +
		`  name: text().notNull(),` + "\n" +
		`  active: bool().default(true),` + "\n" +
		`  count: int().default(0),` + "\n" +
		`  rating: float().default(4.5),` + "\n" +
		`  status: choice("draft", "published").default("draft"),` + "\n" +
		`  tags: array().default([]),` + "\n" +
		`  meta: jsonb().default({}),` + "\n" +
		`  created: now(),` + "\n" +
		`  fy: year()` + "\n" +
		`};` + "\n" +
		`const db = sqlite("app.db", { items });` + "\n" +
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
	defer tenant.Close()
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
		`"status":"published"`: "choice value round-trip failed",
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

// Choice validation happens at write time (engine-agnostic, no CHECK constraint): a value outside the set
// is rejected with a clear message.
func TestSchemaChoiceRejectsInvalid(t *testing.T) {
	columns := map[string]*ColumnSpec{
		"status": {kind: "enum", enumVals: []string{"draft", "published"}},
	}
	_, errMsg := fillRow(columns, map[string]value.Value{"status": value.New("bogus")})
	if errMsg == "" {
		t.Fatal("invalid choice value was accepted")
	}
	if !strings.Contains(errMsg, "status") || !strings.Contains(errMsg, "bogus") {
		t.Errorf("choice error should name the column and value, got: %q", errMsg)
	}
	if _, errMsg := fillRow(columns, map[string]value.Value{"status": value.New(1)}); !strings.Contains(errMsg, "string or null") {
		t.Fatalf("numeric choice write = %q, want a type error", errMsg)
	}
	if _, errMsg := fillRow(columns, map[string]value.Value{"status": value.NewNull()}); errMsg != "" {
		t.Fatalf("nullable choice rejected null: %q", errMsg)
	}
	// A valid value passes.
	if _, errMsg := fillRow(columns, map[string]value.Value{"status": value.New("draft")}); errMsg != "" {
		t.Errorf("valid choice value rejected: %q", errMsg)
	}
}

func TestChoiceDeclarationRejectsInvalidValues(t *testing.T) {
	database := &Database{}
	tests := []struct {
		name string
		args []value.Value
		want string
	}{
		{name: "empty", want: "at least one"},
		{name: "non string", args: []value.Value{value.New(1)}, want: "must be a string"},
		{name: "blank", args: []value.Value{value.New(" ")}, want: "cannot be empty"},
		{name: "duplicate", args: []value.Value{value.New("active"), value.New("active")}, want: "more than once"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			built := database.Choice().Call("choice", test.args...)
			spec, ok := built.V.(*ColumnSpec)
			if !ok || spec == nil {
				t.Fatalf("choice() returned %T", built.V)
			}
			err := validateSchema(map[string]*ColumnSpec{"status": spec})
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "status") {
				t.Fatalf("validateSchema() = %v, want field-scoped %q error", err, test.want)
			}
		})
	}

	built := database.Choice().Call("choice", value.New("active"), value.New("disabled"))
	spec, ok := built.V.(*ColumnSpec)
	if !ok || spec == nil || spec.kind != "enum" || !reflect.DeepEqual(spec.enumVals, []string{"active", "disabled"}) {
		t.Fatalf("choice() compatibility spec = %#v", spec)
	}
	if err := validateSchema(map[string]*ColumnSpec{"status": spec}); err != nil {
		t.Fatalf("valid choice declaration: %v", err)
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
		`const { sqlite, kitid, text } = database;` + "\n" +
		`const vouchers = { id: kitid().primaryKey(), code: text() };` + "\n" +
		`const db = sqlite("app.db", { vouchers });` + "\n" +
		`router.get((ctx) => ctx.json({ ok: 1 }));`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "planhost")
	defer tenant.Close()
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}

	plans := MigrationPlansFor(tenant)
	var vouchers *TablePlan
	for i := range plans {
		if plans[i].Table == "vouchers" && plans[i].Engine == "sqlite" {
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
const { sqlite, kitid, text, int, datetime } = database;
const links = {
  id: kitid().primaryKey(),
  code: text().notNull().unique(),
  original_url: text().notNull(),
  clicks: int().default(0),
  created_at: datetime().defaultNow()
};
const db = sqlite("app.db", { links: links });
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
	defer tenant.Close()
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
	assert(`"afterClicks":1`) // update() wrote — the regression guard
	assert(`"hasKit":true`)   // exists() true for a present row
	assert(`"hasMissing":false`)
	assert(`"hasGone":false`) // delete() removed the row
	assert(`"total":1`)       // 2 created, 1 deleted
}

// migrate() used to be void and swallowed every failure (a failed ALTER only printed a line), so a
// migration that could not apply looked identical to one that did — the kiturl bug. It now returns an
// error. This pins error propagation: a closed connection makes migrate fail instead of silently
// "succeeding".
func TestMigrateReturnsErrorOnFailure(t *testing.T) {
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "m.db"))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Skipf("sqlite driver not available: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t (id TEXT PRIMARY KEY, a TEXT)`); err != nil {
		t.Fatal(err)
	}
	schema := map[string]*ColumnSpec{"id": {kind: "kitid", primary: true}, "a": {kind: "text"}, "b": {kind: "text"}}

	// Happy path: migrate returns nil and the ADD lands.
	if err := migrate(db, "t", schema, false, false); err != nil {
		t.Fatalf("healthy migrate should return nil, got: %v", err)
	}
	cols, _ := tableColumns(db, "t")
	if _, ok := cols["b"]; !ok {
		t.Fatal("column b was not added on the healthy path")
	}

	// Failure path: a CLOSED connection must yield a non-nil error, not a silent no-op.
	db.Close()
	schema["c"] = &ColumnSpec{kind: "text"}
	if err := migrate(db, "t", schema, false, false); err == nil {
		t.Fatal("migrate on a closed db returned nil — the failure was swallowed")
	} else {
		t.Logf("migrate surfaced the failure: %v", err)
	}
}

// A migration that cannot run must SURFACE to the request as a clear "migration failed", not proceed
// against an unmigrated table and throw a cryptic "no such column". A poisoned (non-database) file at
// the db path makes the connection fail deterministically.
func TestSchemaMigrationFailureSurfacesToHandler(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(filepath.Join(dir, ".data"), 0755); err != nil {
		t.Fatal(err)
	}
	// Poison the db file: SQLite cannot open garbage, so the connection (and migration) fails.
	if err := os.WriteFile(filepath.Join(dir, ".data", "app.db"), []byte("this is not a sqlite database"), 0644); err != nil {
		t.Fatal(err)
	}

	router := `import { router, database } from "kitwork";
const { sqlite, kitid, text } = database;
const notes = { id: kitid().primaryKey(), body: text() };
const db = sqlite("app.db", { notes: notes });
router.get((ctx) => ctx.json({ n: db.notes.count() }));`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	defer tenant.Close()
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	body := rec.Body.String()

	if rec.Code == 200 {
		t.Fatalf("a broken migration should not return 200 with data, body: %s", body)
	}
	if !strings.Contains(body, "migration failed") {
		t.Errorf("expected a clear 'migration failed' message, got: %s", body)
	}
}

// now().onUpdate() = auto-touch: created_at is stamped once and stays frozen; updated_at is re-stamped
// on EVERY update() even when the caller never mentions it. Driven through the VM.
func TestSchemaNowOnUpdateAutoTouch(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	// The trick that makes this deterministic despite second-granularity timestamps: first PIN
	// updated_at to the year 2000 explicitly (caller-provided value must win — touch skips it), then
	// do a normal update that never mentions updated_at. Auto-touch must move it OFF 2000 to now().
	router := `import { router, database } from "kitwork";
const { sqlite, kitid, text, int, now } = database;
const posts = {
  id: kitid().primaryKey(),
  title: text(),
  views: int().default(0),
  created_at: now(),
  updated_at: now().onUpdate()
};
const db = sqlite("app.db", { posts: posts });
router.get((ctx) => {
  const created = db.posts.create({ title: "hello" });
  // 1) explicit updated_at → caller wins, touch must NOT override it.
  db.posts.where("id", "=", created.id).update({ views: 1, updated_at: "2000-01-01T00:00:00Z" });
  const pinned = db.posts.where("id", "=", created.id).first();
  // 2) caller never mentions updated_at → auto-touch must re-stamp it to now().
  db.posts.where("id", "=", created.id).update({ views: 2 });
  const touched = db.posts.where("id", "=", created.id).first();
  return ctx.json({
    createdAt: created.created_at,
    pinnedUpdatedAt: pinned.updated_at,
    touchedUpdatedAt: touched.updated_at,
    touchedCreatedAt: touched.created_at,
    views: touched.views
  });
});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	defer tenant.Close()
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
	t.Logf("body: %s", body)

	f := decodeJSONObject(t, body)

	// Explicit value wins: after update #1, updated_at is exactly what the caller passed.
	if f["pinnedUpdatedAt"] != "2000-01-01T00:00:00Z" {
		t.Errorf("caller-provided updated_at should win over touch, got %q", f["pinnedUpdatedAt"])
	}
	// Auto-touch fired on update #2 (no updated_at in the payload): it moved OFF 2000 to the current year.
	if f["touchedUpdatedAt"] == "2000-01-01T00:00:00Z" {
		t.Errorf("auto-touch did not fire — updated_at is still the pinned 2000 value")
	}
	if !strings.HasPrefix(f["touchedUpdatedAt"], "2026-") {
		t.Errorf("touched updated_at should be a current (2026) timestamp, got %q", f["touchedUpdatedAt"])
	}
	// created_at was stamped once and never re-stamped by either update.
	if f["createdAt"] == "" || f["createdAt"] == "<nil>" || f["touchedCreatedAt"] != f["createdAt"] {
		t.Errorf("created_at must be stamped once and frozen: created=%q afterTouch=%q", f["createdAt"], f["touchedCreatedAt"])
	}
	if !strings.Contains(body, `"views":2`) {
		t.Errorf("the unrelated update should have applied, body: %s", body)
	}
}

// A schema migration must flush its DDL out of the write-ahead log into the MAIN database file, so a
// separate SQLite process/instance opening the same file sees the new columns instead of "no such
// column". This pins that: after migrate() adds a column, the -wal file is truncated (the checkpoint
// ran). Without checkpointWAL the ALTER frames sit in a growing WAL and the fix is undone.
func TestMigrationCheckpointsWALToMainFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.ToSlash(filepath.Join(dir, "c.db"))
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Skipf("sqlite driver not available: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}

	schema := map[string]*ColumnSpec{"id": {kind: "kitid", primary: true}, "code": {kind: "text"}}
	if err := migrate(db, "t", schema, false, false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ { // enough writes to grow the WAL
		if _, err := db.Exec(`INSERT INTO t (id, code) VALUES (?, ?)`, fmt.Sprintf("row-%04d", i), "c"); err != nil {
			t.Fatal(err)
		}
	}
	// The LAST operation is a DDL migration — its checkpoint must truncate the WAL.
	schema["extra"] = &ColumnSpec{kind: "text"}
	if err := migrate(db, "t", schema, false, false); err != nil {
		t.Fatal(err)
	}

	wal := filepath.Join(dir, "c.db-wal")
	fi, err := os.Stat(wal)
	if err != nil {
		return // WAL removed entirely = fully checkpointed, ideal.
	}
	// TRUNCATE leaves a 0-byte (or header-only) WAL; anything larger means DDL is stranded in the log.
	if fi.Size() > 4096 {
		t.Errorf("WAL not checkpointed after migration: %d bytes still in the log (DDL stranded, separate processes would miss the column)", fi.Size())
	}
}

// decodeJSONObject reads a flat {"k":"v"|n|null} object from a handler body without pulling in a JSON
// dependency shape assumption — values come back as strings ("<nil>" for null/absent).
func decodeJSONObject(t *testing.T, body string) map[string]string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("body is not JSON: %v (%s)", err, body)
	}
	out := map[string]string{}
	for k, v := range raw {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// Columns come out in DECLARED order, not alphabetical. Declared id, zebra, apple, mango — a table
// built by the OLD sort.Strings would show apple, id, mango, zebra. Driven through the VM so the
// column builders get their real declaration-order seq.
func TestSchemaColumnDeclarationOrder(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { sqlite, kitid, text } = database;
const things = { id: kitid().primaryKey(), zebra: text(), apple: text(), mango: text() };
const db = sqlite("app.db", { things: things }, { token: "tok", access: "readwrite" });
router.get((ctx) => { db.things.create({ zebra: "z", apple: "a", mango: "m" }); return ctx.json({ ok: true }); });`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	defer tenant.Close()
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	// seed (creates the table)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if rec.Code != 200 {
		t.Fatalf("seed failed: %d %s", rec.Code, rec.Body.String())
	}
	// SELECT * via libSQL and check the column order in the response.
	body := `{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"SELECT * FROM things","want_rows":true}},{"type":"close"}]}`
	req := httptest.NewRequest(http.MethodPost, "http://localhost/v2/pipeline", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	tenant.Serve(rec, req)
	got := rec.Body.String()
	// The cols must appear in declared order.
	idx := func(s string) int { return strings.Index(got, `"name":"`+s+`"`) }
	if !(idx("id") < idx("zebra") && idx("zebra") < idx("apple") && idx("apple") < idx("mango")) {
		t.Errorf("columns not in declared order (id,zebra,apple,mango); body: %s", got)
	}
}

// An EXISTING table whose columns are in the wrong order is REBUILT into the declared order on
// migrate, preserving the data.
func TestSchemaMigrationReordersToDeclared(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Live table in a non-declared order, with a row.
	if _, err := db.Exec(`CREATE TABLE t (apple TEXT, id TEXT PRIMARY KEY, zebra TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t (id, apple, zebra) VALUES ('x','a1','z1')`); err != nil {
		t.Fatal(err)
	}
	// Schema declares id, zebra, apple (seq set to mimic the VM's declaration order).
	schema := map[string]*ColumnSpec{
		"id":    {kind: "kitid", primary: true, seq: 1},
		"zebra": {kind: "text", seq: 2},
		"apple": {kind: "text", seq: 3},
	}
	if err := migrate(db, "t", schema, false, false); err != nil {
		t.Fatal(err)
	}
	order, err := tableColumnsOrdered(db, "t")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "zebra", "apple"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("column order = %v, want %v", order, want)
	}
	// Data survived the reorder rebuild.
	var apple, zebra string
	if err := db.QueryRow(`SELECT apple, zebra FROM t WHERE id='x'`).Scan(&apple, &zebra); err != nil {
		t.Fatalf("row lost in reorder: %v", err)
	}
	if apple != "a1" || zebra != "z1" {
		t.Errorf("data changed in reorder: apple=%q zebra=%q", apple, zebra)
	}
}

// A primary key must be NOT NULL. SQLite lets a non-INTEGER PRIMARY KEY (our TEXT kitid) hold NULLs
// unless declared NOT NULL — so a manager reported "primary key can be null". New tables get NOT NULL,
// and an existing table with a nullable PK is rebuilt to fix it (data preserved).
func TestSchemaPrimaryKeyIsNotNull(t *testing.T) {
	pkNotNull := func(db *sql.DB, table string) (bool, bool) {
		rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dflt sql.NullString
			rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk)
			if pk == 1 {
				return notnull == 1, true
			}
		}
		return false, false
	}
	schema := map[string]*ColumnSpec{"id": {kind: "kitid", primary: true, seq: 1}, "code": {kind: "text", seq: 2}}

	// New table.
	fresh, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "a.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := migrate(fresh, "t", schema, false, false); err != nil {
		t.Fatal(err)
	}
	if nn, ok := pkNotNull(fresh, "t"); !ok || !nn {
		t.Error("a new table's primary key must be NOT NULL")
	}

	// Existing table with a nullable PK (declared order already), plus a row.
	old, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "b.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	old.Exec(`CREATE TABLE t (id TEXT PRIMARY KEY, code TEXT)`)
	old.Exec(`INSERT INTO t (id, code) VALUES ('x','c1')`)
	if nn, _ := pkNotNull(old, "t"); nn {
		t.Fatal("precondition: the PK should start nullable")
	}
	if err := migrate(old, "t", schema, false, false); err != nil {
		t.Fatal(err)
	}
	if nn, _ := pkNotNull(old, "t"); !nn {
		t.Error("an existing nullable primary key must be rebuilt to NOT NULL")
	}
	var code string
	if err := old.QueryRow(`SELECT code FROM t WHERE id='x'`).Scan(&code); err != nil || code != "c1" {
		t.Errorf("data lost in the pk-fix rebuild: err=%v code=%q", err, code)
	}
}

// .index() creates indexes: a bare .index() → auto-named single column; a shared name → composite in
// declaration order; an explicit position → composite pinned in that order; and a rebuild recreates
// them.
func TestSchemaIndexDefinitions(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "a.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := map[string]*ColumnSpec{
		"id":     {kind: "kitid", primary: true, seq: 1},
		"slug":   {kind: "text", seq: 2, indexes: []colIndexRef{{name: ""}}},
		"code":   {kind: "text", seq: 3, indexes: []colIndexRef{{name: "status_code"}}},
		"status": {kind: "text", seq: 4, indexes: []colIndexRef{{name: "status_code"}}},
		"a":      {kind: "text", seq: 5, indexes: []colIndexRef{{name: "ab", pos: 2}}},
		"b":      {kind: "text", seq: 6, indexes: []colIndexRef{{name: "ab", pos: 1}}},
	}
	if err := migrate(db, "links", schema, false, false); err != nil {
		t.Fatal(err)
	}
	cols := func(name string) string {
		c, _ := liveIndexColumns(db, name)
		return strings.Join(c, ",")
	}
	if cols("idx_links_slug") != "slug" {
		t.Errorf("bare .index() should be a single-column auto-named index, got %q", cols("idx_links_slug"))
	}
	if cols("status_code") != "code,status" {
		t.Errorf("composite (no position) should follow declaration order, got %q", cols("status_code"))
	}
	if cols("ab") != "b,a" {
		t.Errorf("composite with positions should be pinned (b,a), got %q", cols("ab"))
	}
}

// A schema-declared index survives a table rebuild (the rebuild drops the table's indexes; syncIndexes
// recreates them). Triggered here by the primary-key NOT NULL fix.
func TestSchemaIndexSurvivesRebuild(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "b.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`CREATE TABLE t (id TEXT PRIMARY KEY, code TEXT)`) // nullable PK forces a rebuild
	db.Exec(`INSERT INTO t (id, code) VALUES ('x','c1')`)
	schema := map[string]*ColumnSpec{
		"id":   {kind: "kitid", primary: true, seq: 1},
		"code": {kind: "text", seq: 2, indexes: []colIndexRef{{name: "idx_code"}}},
	}
	if err := migrate(db, "t", schema, false, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := liveIndexColumns(db, "idx_code"); strings.Join(c, ",") != "code" {
		t.Errorf("index should survive the rebuild, got %v", c)
	}
	var code string
	if err := db.QueryRow(`SELECT code FROM t WHERE id='x'`).Scan(&code); err != nil || code != "c1" {
		t.Errorf("data lost in the rebuild: %v %q", err, code)
	}
}

// End to end through the VM: the .index() modifier in JS creates the index, visible in sqlite_master
// exactly as a db manager would list it.
func TestSchemaIndexThroughVM(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { sqlite, kitid, text } = database;
const links = {
  id: kitid().primaryKey(),
  slug: text().index(),
  code: text().notNull().index("status_code"),
  status: text().default("active").index("status_code"),
};
const db = sqlite("app.db", { links: links }, { token: "tok", access: "readwrite" });
router.get((ctx) => { db.links.create({ slug: "s", code: "c", status: "active" }); return ctx.json({ ok: true }); });`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	defer tenant.Close()
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	// create (runs the migration + index creation)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if rec.Code != 200 {
		t.Fatalf("seed failed: %d %s", rec.Code, rec.Body.String())
	}
	// list indexes the way a manager does
	body := `{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"SELECT name FROM sqlite_master WHERE type='index' AND sql IS NOT NULL","want_rows":true}},{"type":"close"}]}`
	req := httptest.NewRequest(http.MethodPost, "http://localhost/v2/pipeline", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	tenant.Serve(rec, req)
	got := rec.Body.String()
	if !strings.Contains(got, "idx_links_slug") || !strings.Contains(got, "status_code") {
		t.Errorf(".index() indexes not found via sqlite_master; body: %s", got)
	}
}

// .index() options: { unique: true } builds a UNIQUE index; { where: "..." } a partial index. Changing
// an index's definition (adding/removing the partial filter) is detected and DROP+CREATEd, not left
// stale. The partial filter is a STRUCTURED object ({ status: "active", deleted_at: null }), not raw SQL.
func TestSchemaIndexPartialFilter(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "a.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlOf := func(name string) string { s, _ := liveIndexSQL(db, name); return s }

	schema := map[string]*ColumnSpec{
		"id":         {kind: "kitid", primary: true, seq: 1},
		"status":     {kind: "text", seq: 2},
		"deleted_at": {kind: "datetime", seq: 3},
		"created_at": {kind: "datetime", seq: 4, indexes: []colIndexRef{{name: "active_recent", filter: []indexCond{
			{col: "status", val: value.New("active")},
			{col: "deleted_at", val: value.Value{K: value.Nil}},
		}}}},
	}
	if err := migrate(db, "links", schema, false, false); err != nil {
		t.Fatal(err)
	}
	// The object filter renders to a real partial predicate — equality quoted, null → IS NULL.
	got := sqlOf("active_recent")
	if !strings.Contains(got, "WHERE") || !strings.Contains(got, `"status" = 'active'`) || !strings.Contains(got, `"deleted_at" IS NULL`) {
		t.Errorf("partial filter not rendered correctly: %q", got)
	}

	// Change detection: drop the filter → index rebuilt to a full index.
	schema["created_at"].indexes = []colIndexRef{{name: "active_recent"}}
	if err := migrate(db, "links", schema, false, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sqlOf("active_recent"), "WHERE") {
		t.Errorf("active_recent should no longer be partial after the change, got %q", sqlOf("active_recent"))
	}
}

// A choice default must be null or one of the declared string values.
func TestSchemaChoiceDefaultValidated(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "e.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bad := map[string]*ColumnSpec{
		"id":     {kind: "kitid", primary: true, seq: 1},
		"status": {kind: "enum", enumVals: []string{"active", "disabled"}, hasDefault: true, def: value.New("archived"), seq: 2},
	}
	if err := migrate(db, "t", bad, false, false); err == nil {
		t.Error("a choice default outside the declared values must be rejected")
	} else if !strings.Contains(err.Error(), "archived") {
		t.Errorf("error should name the bad default, got: %v", err)
	}
	good := map[string]*ColumnSpec{
		"id":     {kind: "kitid", primary: true, seq: 1},
		"status": {kind: "enum", enumVals: []string{"active", "disabled"}, hasDefault: true, def: value.New("active"), seq: 2},
	}
	if err := migrate(db, "t2", good, false, false); err != nil {
		t.Errorf("a valid choice default should be accepted: %v", err)
	}
	badType := map[string]*ColumnSpec{
		"id":     {kind: "kitid", primary: true, seq: 1},
		"status": {kind: "enum", enumVals: []string{"active", "disabled"}, hasDefault: true, def: value.New(1), seq: 2},
	}
	if err := migrate(db, "t3", badType, false, false); err == nil || !strings.Contains(err.Error(), "string or null") {
		t.Fatalf("numeric choice default = %v, want a type error", err)
	}
}

// ref() foreign keys end to end on SQLite: the DDL carries REFERENCES … ON DELETE CASCADE, the FK is
// enforced, a delete cascades, and — critically — a ref to a kitid does NOT auto-generate a random id
// (the FK column keeps the value it was given).
func TestSchemaRefForeignKey(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { sqlite, id, text, ref } = database;
const users = { id: id(), email: text().notNull().unique() };
const links = {
  id: id(),
  user_id: ref(users.id, { onDelete: "cascade" }).notNull(),
  code: text().notNull().unique(),
};
const db = sqlite("app.db", { users, links }, { token: "tok", access: "readwrite" });
router.get((ctx) => {
  const u = db.users.create({ email: "a@b.c" });
  const ln = db.links.create({ user_id: u.id, code: "x" });
  return ctx.json({ uid: u.id, link_user: ln.user_id });
});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	defer tenant.Close()
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if rec.Code != 200 {
		t.Fatalf("handler failed: %s", rec.Body.String())
	}
	f := decodeJSONObject(t, rec.Body.String())
	if f["uid"] == "" || f["link_user"] != f["uid"] {
		t.Errorf("FK column must keep the given id (no auto-gen): uid=%q link_user=%q", f["uid"], f["link_user"])
	}

	q := func(sql string, wantRows bool) string {
		w := "false"
		if wantRows {
			w = "true"
		}
		body := `{"baton":null,"requests":[{"type":"execute","stmt":{"sql":"` + sql + `","want_rows":` + w + `}},{"type":"close"}]}`
		req := httptest.NewRequest(http.MethodPost, "http://localhost/v2/pipeline", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rr := httptest.NewRecorder()
		tenant.Serve(rr, req)
		return rr.Body.String()
	}
	if ddl := q("SELECT sql FROM sqlite_master WHERE type='table' AND name='links'", true); !strings.Contains(ddl, "REFERENCES") || !strings.Contains(ddl, "ON DELETE CASCADE") {
		t.Errorf("links DDL missing the FK: %s", ddl)
	}
	if bad := q("INSERT INTO links(id,user_id,code) VALUES('l9','GHOST','z')", false); !strings.Contains(bad, "FOREIGN KEY") && !strings.Contains(bad, "constraint") {
		t.Errorf("FK not enforced: %s", bad)
	}
	q("DELETE FROM users", false)
	if after := q("SELECT count(*) c FROM links", true); !strings.Contains(after, `"value":"0"`) {
		t.Errorf("ON DELETE CASCADE did not fire: %s", after)
	}
}

// ref() validation: the target must be a primary key or unique, types must match, and onDelete:setNull
// cannot apply to a NOT NULL column.
func TestSchemaRefValidation(t *testing.T) {
	pk := &ColumnSpec{kind: "kitid", primary: true, seq: 1}
	open := func() *sql.DB {
		db, _ := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "v.db")))
		return db
	}
	cases := []struct {
		name string
		fk   *fkRef
		col  *ColumnSpec
		want string
	}{
		{"non-pk target", &fkRef{target: &ColumnSpec{kind: "text"}, table: "users", column: "name"}, nil, "not a primary key or unique"},
		{"type mismatch", &fkRef{target: pk, table: "users", column: "id"}, &ColumnSpec{kind: "integer"}, "does not match"},
		{"setNull on notNull", &fkRef{target: pk, table: "users", column: "id", onDelete: "setNull"}, &ColumnSpec{kind: "text", notNull: true}, "setNull"},
		{"update setNull on notNull", &fkRef{target: pk, table: "users", column: "id", onUpdate: "set null"}, &ColumnSpec{kind: "text", notNull: true}, "setNull"},
		{"unresolved", &fkRef{target: pk}, &ColumnSpec{kind: "text"}, "could not be resolved"},
	}
	for _, c := range cases {
		col := c.col
		if col == nil {
			col = &ColumnSpec{kind: "text"}
		}
		col.fk = c.fk
		col.seq = 2
		schema := map[string]*ColumnSpec{"id": pk, "user_id": col}
		db := open()
		err := migrate(db, "t", schema, false, false)
		db.Close()
		if err == nil {
			t.Errorf("%s: expected a validation error", c.name)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q should contain %q", c.name, err.Error(), c.want)
		}
	}
}

func TestPlainKindForPreservesExactIntegerKeyCodec(t *testing.T) {
	for source, want := range map[string]string{
		"smallint": "smallint",
		"int32":    "int32",
		"bigint":   "bigint",
		"serial":   "integer",
	} {
		if got := plainKindFor(source); got != want {
			t.Fatalf("plainKindFor(%q) = %q, want %q", source, got, want)
		}
	}
}
