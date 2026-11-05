package work

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/kitdb/gateway"
)

// followsRoute inserts an order of the form's email and lists today's orders, each answering its failure as
// { code, message } (safe), so a test reads what a statement says when the database lacks what the code declares.
const followsRoute = `import * as router from "kitwork/router";
import { safe } from "kitwork";
import { db } from "app/.shared/schema/shop.work";

router.post((context) => {
  const email = context.request.form("email");
  const { ok, value, error } = safe(() => db.insert((orders) => ({ email, amount: 12.5, day: "2026-11-05" })));
  return context.json(ok ? { row: value } : { code: error.code, message: error.message });
});

router.get((context) => {
  const { ok, value, error } = safe(() => db.list((orders) => orders.day === "2026-11-05"));
  return context.json(ok ? { rows: value } : { code: error.code, message: error.message });
});
`

// followsSchema is the shop's schema with the columns of orders given.
func followsSchema(columns string) string {
	return `import { table, id, text, decimal, date, timestamp } from "kitwork/schema";
import { connection } from "kitwork/database";

const orders = table({ ` + columns + ` });

export const db = connection().tables({ orders });
`
}

// followsPost posts an order and reads the answer.
func followsPost(t *testing.T, tenant *Tenant, email string) map[string]any {
	t.Helper()
	status, body := managedPost(tenant, "/", url.Values{"email": {email}})
	var result map[string]any
	if status != 200 || json.Unmarshal([]byte(body), &result) != nil {
		t.Fatalf("POST / = %d %s", status, body)
	}
	return result
}

// managedAppAt is a managedApp of a root the test has written.
func managedAppAt(t *testing.T, root string) *managedApp {
	t.Helper()
	runtime := app.NewRuntime("acme")
	t.Cleanup(runtime.Close)
	return &managedApp{t: t, root: root, runtime: runtime, cache: compiler.NewFileCache(filepath.Join(t.TempDir(), "bytecode"))}
}

// fileTables are the tables of a SQLite file other than the engine's.
func fileTables(t *testing.T, path string) string {
	t.Helper()
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := file.Query(`SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'kitwork_%' AND name NOT LIKE '_kitwork_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := textRowsOf(rows)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(names, ",")
}

func textRowsOf(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	return out, rows.Err()
}

// The code follows the database (KITWORK_DATABASE.md "Database là gốc", slice 4): a statement never makes a table
// or adds a column. A table the file lacks fails every statement saying what is missing and how to add it, the file
// left as it was; kitwork migrate adds it. A column added to the declaration, the same until kitwork migrate. An index
// the file lacks is said once and the statements run. A decimal whose scale differs from the file's fails them, and
// kitwork migrate leaves it: the code is to follow the database there.
//
// CONTROL (05/11/2026): with ensureTable migrating the file as before slice 4, "a statement before kitwork migrate
// answered map[row:map[amount:12.5 …]]" (the insert made the table); with checkTable not holding a decimal's scale
// against kitwork_columns, "decimal(12, 4) against the file's (12, 2): map[rows:[map[amount:0.125 …]]]", 12.50 read as
// 0.125.
func TestStatementsFollowTheDatabase(t *testing.T) {
	v1 := `id: id(), email: text().unique(), amount: decimal(12, 2), day: date().index()`
	managed := newManagedAppAsIs(t, map[string]string{".shared/schema/shop.work": followsSchema(v1), "shop.example/router.work": followsRoute})
	path := filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db")
	tenant := managed.site("shop.example")

	refused := followsPost(t, tenant, "an@example.com")
	const lacking = "database .database/application/kitwork.db chưa khớp bảng .shared/schema/ khai (database là gốc: engine không tự tạo bảng, thêm cột); " +
		"database chưa có: bảng orders (id id, email text, amount decimal(12, 2), day date); thêm bằng kitwork migrate"
	if refused["code"] != "DATABASE" || !strings.HasPrefix(refused["message"].(string), lacking) {
		t.Fatalf("a statement before kitwork migrate answered %v, want DATABASE %q…", refused, lacking)
	}
	if tables := fileTables(t, path); tables != "" {
		t.Fatalf("a statement made %q in the file", tables)
	}

	migrateAppForTest(t, managed.root, "acme", RootLayoutMultiTenant)
	if added := followsPost(t, tenant, "an@example.com"); added["row"] == nil {
		t.Fatalf("after kitwork migrate: %v", added)
	}

	// a column the declaration adds: refused until kitwork migrate, the file untouched
	v2 := v1 + `, note: text().nullable()`
	writeAppTree(t, managed.root, map[string]string{"acme/.shared/schema/shop.work": followsSchema(v2), "acme/two.example/router.work": followsRoute})
	tenant = managed.site("two.example") // another site of the app, booted on the declaration as it is now
	if refused := answer(t, tenant, ""); refused["code"] != "DATABASE" || !strings.Contains(refused["message"].(string), "database chưa có: cột note text của bảng orders;") {
		t.Fatalf("a column the file lacks: %v", refused)
	}
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	columns := func() string {
		t.Helper()
		rows, err := file.Query(`SELECT name FROM pragma_table_info('orders') ORDER BY cid`)
		if err != nil {
			t.Fatal(err)
		}
		names, err := textRowsOf(rows)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(names, ",")
	}
	if got := columns(); got != "id,email,amount,day" {
		t.Fatalf("a statement changed orders: %s", got)
	}
	migrateAppForTest(t, managed.root, "acme", RootLayoutMultiTenant)
	if listed := answer(t, tenant, ""); count(t, listed, "rows") != 1 {
		t.Fatalf("after kitwork migrate: %v", listed)
	}
	if got := columns(); got != "id,email,amount,day,note" {
		t.Fatalf("kitwork migrate made orders %s", got)
	}

	// an index the file lacks: said once, the statements run
	v3 := strings.Replace(v2, "amount: decimal(12, 2)", "amount: decimal(12, 2).index()", 1)
	writeAppTree(t, managed.root, map[string]string{"acme/.shared/schema/shop.work": followsSchema(v3), "acme/three.example/router.work": followsRoute})
	tenant = managed.site("three.example") // another site of the app, booted on the declaration as it is now
	var listed map[string]any
	said := captureOutput(t, func() { listed = answer(t, tenant, "") })
	if count(t, listed, "rows") != 1 {
		t.Fatalf("with an index the file lacks: %v", listed)
	}
	if !strings.Contains(said, "[database] .database/application/kitwork.db: câu vẫn chạy; kitwork migrate sẽ ghi thêm chỉ mục idx_orders_amount của bảng orders (amount)") {
		t.Fatalf("an index the file lacks was said as %q", said)
	}

	// a decimal's scale the file does not hold: refused, and kitwork migrate leaves it
	v4 := strings.Replace(v3, "decimal(12, 2)", "decimal(12, 4)", 1)
	writeAppTree(t, managed.root, map[string]string{"acme/.shared/schema/shop.work": followsSchema(v4), "acme/four.example/router.work": followsRoute})
	tenant = managed.site("four.example") // another site of the app, booted on the declaration as it is now
	const differ = "khác database: cột orders.amount: database ghi decimal(12, 2) (kitwork_columns), code khai decimal(12, 4); sửa table() theo database"
	if refused := answer(t, tenant, ""); refused["code"] != "DATABASE" || !strings.Contains(refused["message"].(string), differ) {
		t.Fatalf("decimal(12, 4) against the file's (12, 2): %v", refused)
	}
	plans := migrateAppForTest(t, managed.root, "acme", RootLayoutMultiTenant)
	if len(plans) != 1 || len(plans[0].Differ) != 1 || len(plans[0].Add) != 0 {
		t.Fatalf("kitwork migrate's plan: %+v", plans)
	}
	var kind string
	if err := file.QueryRow(`SELECT kind || '(' || precision || ',' || scale || ')' FROM kitwork_columns WHERE table_name = 'orders' AND column_name = 'amount'`).Scan(&kind); err != nil || kind != "decimal(12,2)" {
		t.Fatalf("kitwork migrate wrote amount as %q (%v), want the file's decimal(12,2) kept", kind, err)
	}
}

// A machine whose app folder has the structure file and no database (a clone of the repo) makes the database from it
// at the first statement and serves without kitwork migrate: the tables, and every kind and .searchable() weight the
// statements need, are in the file, so nothing is said to add.
//
// CONTROL (05/11/2026): with the structure file's rows writing 0 for every search_weight, "the clone said "[database]
// kitwork.db tạo từ kitwork.db.sql … [database] .database/application/kitwork.db: câu vẫn chạy; kitwork migrate sẽ ghi
// thêm …"" (title's weight lost).
func TestANewMachineServesFromTheStructureFile(t *testing.T) {
	schema := `import { table, id, text, timestamp } from "kitwork/schema";
import { connection } from "kitwork/database";

const posts = table({ id: id(), title: text().searchable(), createdAt: timestamp().created() });

export const db = connection().tables({ posts });
`
	route := `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";

router.post((context) => {
  const title = context.request.form("title");
  return context.json(db.insert((posts) => ({ title })));
});
router.get((context) => context.json({ rows: db.list((posts) => posts.title !== "") }));
`
	files := map[string]string{".shared/schema/shop.work": schema, "shop.example/router.work": route}
	first := newManagedApp(t, files)
	if status, body := managedPost(first.site("shop.example"), "/", url.Values{"title": {"Cà phê"}}); status != 200 {
		t.Fatalf("insert = %d %s", status, body)
	}
	structure, err := os.ReadFile(filepath.Join(first.root, "acme", ".database", "application", "kitwork.db.sql"))
	if err != nil {
		t.Fatalf("no structure file beside the database: %v", err)
	}

	// the clone: the app's source and the structure file, no database
	root := t.TempDir()
	tree := map[string]string{"acme/.database/application/kitwork.db.sql": string(structure)}
	for name, source := range files {
		tree["acme/"+name] = source
	}
	writeAppTree(t, root, tree)
	clone := managedAppAt(t, root)
	tenant := clone.site("shop.example")
	var status int
	var body string
	said := captureOutput(t, func() { status, body = managedPost(tenant, "/", url.Values{"title": {"Trà"}}) })
	if status != 200 || !strings.Contains(body, `"title":"Trà"`) {
		t.Fatalf("insert on the clone = %d %s", status, body)
	}
	if !strings.Contains(said, "kitwork.db tạo từ kitwork.db.sql") || strings.Contains(said, "kitwork migrate") {
		t.Fatalf("the clone said %q", said)
	}
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(root, "acme", ".database", "application", "kitwork.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var weight int
	var kind string
	if err := file.QueryRow(`SELECT search_weight FROM kitwork_columns WHERE table_name = 'posts' AND column_name = 'title'`).Scan(&weight); err != nil || weight != 1 {
		t.Fatalf("the made file holds title's weight %d (%v), want 1", weight, err)
	}
	if err := file.QueryRow(`SELECT kind FROM kitwork_columns WHERE table_name = 'posts' AND column_name = 'createdAt'`).Scan(&kind); err != nil || kind != "datetime" {
		t.Fatalf("the made file holds createdAt as %q (%v), want datetime", kind, err)
	}
}

// A table made with the port's schema statements, as an admin writes them in PostgreSQL's words (KITWORK_DATABASE.md
// "Database là gốc", slice 2), serves the app that declares it, without kitwork migrate: its storage is what the
// declaration's is. The kinds the port does not know (a kitid, a date) are said once; kitwork migrate writes them.
//
// CONTROL (05/11/2026): with checkTable holding a kind of kitwork_columns other than decimal as a difference, "the
// table made through the port: map[code:DATABASE message:… khác database: cột orders.id: database ghi text; cột
// orders.day: database ghi text; …]".
func TestATableMadeThroughThePortServesTheApp(t *testing.T) {
	v1 := `id: id(), email: text().unique(), amount: decimal(12, 2), day: date()`
	managed := newManagedAppAsIs(t, map[string]string{".shared/schema/shop.work": followsSchema(v1), "shop.example/router.work": followsRoute})
	path := filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db")
	writer, release, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.LoadStructure(context.Background(), writer, "port:admin", `CREATE TABLE orders (
  id text PRIMARY KEY,
  email text NOT NULL UNIQUE,
  amount numeric(12, 2) NOT NULL,
  day date NOT NULL
);`); err != nil {
		t.Fatal(err)
	}
	release()

	tenant := managed.site("shop.example")
	var added map[string]any
	said := captureOutput(t, func() { added = followsPost(t, tenant, "an@example.com") })
	if added["row"] == nil {
		t.Fatalf("the table made through the port: %v", added)
	}
	if !strings.Contains(said, "kitwork migrate sẽ ghi thêm kiểu của cột orders.id, orders.day trong kitwork_columns") {
		t.Fatalf("the kinds the port does not know were said as %q", said)
	}
	if listed := answer(t, tenant, ""); count(t, listed, "rows") != 1 {
		t.Fatalf("list: %v", listed)
	}
	plans := migrateAppForTest(t, managed.root, "acme", RootLayoutMultiTenant)
	if len(plans) != 1 || len(plans[0].Add) != 0 || len(plans[0].Differ) != 0 || len(plans[0].Write) != 1 {
		t.Fatalf("kitwork migrate's plan for the port's table: %+v", plans)
	}
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var kind string
	if err := file.QueryRow(`SELECT kind FROM kitwork_columns WHERE table_name = 'orders' AND column_name = 'id'`).Scan(&kind); err != nil || kind != "kitid" {
		t.Fatalf("after kitwork migrate orders.id is %q (%v), want kitid", kind, err)
	}
}

// CheckAppDatabases, what a server's boot and kitwork check say (KITWORK_DATABASE.md "Database là gốc", slice 4),
// reads only. A database that is not there lacks every table, and nothing is made; one whose folder holds only its
// structure file is held as the file the structure would make (a clone), still not made; a database that lacks a
// column says it, as the boot prints it, and keeps its columns.
//
// CONTROL (05/11/2026): with the plans read through the writer as kitwork migrate reads them, "CheckAppDatabases made
// the clone's database"; with a missing file held as a file of no table even beside its structure file, "the clone
// lacks [bảng orders (id id, email text, amount decimal(12, 2))]".
func TestCheckAppDatabasesReadsOnly(t *testing.T) {
	v1 := `id: id(), email: text().unique(), amount: decimal(12, 2)`
	root := t.TempDir()
	writeAppTree(t, root, map[string]string{"acme/.shared/schema/shop.work": followsSchema(v1)})
	folder := filepath.Join(root, "acme", ".database")
	plans, err := CheckAppDatabases(root, "acme", RootLayoutMultiTenant, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || len(plans[0].Add) != 1 || !strings.HasPrefix(plans[0].Add[0], "bảng orders (") {
		t.Fatalf("a database that is not there: %+v", plans)
	}
	if err := plans[0].Apply(context.Background()); err == nil {
		t.Fatal("a plan of CheckAppDatabases applied")
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("CheckAppDatabases made the app's database (%v)", err)
	}

	// a clone: the structure file alone
	migrateAppForTest(t, root, "acme", RootLayoutMultiTenant)
	structure, err := os.ReadFile(filepath.Join(folder, "application", "kitwork.db.sql"))
	if err != nil {
		t.Fatal(err)
	}
	clone := t.TempDir()
	writeAppTree(t, clone, map[string]string{"acme/.shared/schema/shop.work": followsSchema(v1), "acme/.database/application/kitwork.db.sql": string(structure)})
	if plans, err := CheckAppDatabases(clone, "acme", RootLayoutMultiTenant, nil); err != nil || len(plans) != 1 ||
		len(plans[0].Add)+len(plans[0].Differ)+len(plans[0].Write) != 0 {
		t.Fatalf("the clone lacks %v %v %v (%v)", plans[0].Add, plans[0].Differ, plans[0].Write, err)
	}
	if _, err := os.Stat(filepath.Join(clone, "acme", ".database", "application", "kitwork.db")); !os.IsNotExist(err) {
		t.Fatalf("CheckAppDatabases made the clone's database (%v)", err)
	}

	// a column the declaration adds
	writeAppTree(t, root, map[string]string{"acme/.shared/schema/shop.work": followsSchema(v1 + `, note: text().nullable()`)})
	plans, err = CheckAppDatabases(root, "acme", RootLayoutMultiTenant, nil)
	if err != nil {
		t.Fatal(err)
	}
	said := captureOutput(t, func() { plans[0].Say() })
	if !strings.Contains(said, "[database] .database/application/kitwork.db (app acme) chưa có: cột note text của bảng orders; câu trên database này lỗi tới khi thêm: dừng server rồi chạy go run . migrate") {
		t.Fatalf("the boot said %q", said)
	}
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(folder, "application", "kitwork.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := file.Query(`SELECT name FROM pragma_table_info('orders') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	if names, err := textRowsOf(rows); err != nil || strings.Join(names, ",") != "id,email,amount" {
		t.Fatalf("orders after CheckAppDatabases: %v (%v)", names, err)
	}
}
