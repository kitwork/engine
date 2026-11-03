package work

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/storage"
	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

// The tests of `kitwork migrate kitdb` (kitdb_copy.go). The KitDB file of each is made as the engine made it
// before 29/10/2026: the app's tables through the ORM of KitDB (connection().tables declared on "kitdb", as
// database_module.go did), the runs through KitDB's SQL and queuestore (relational, as queue_runs.go did),
// each closed before the copy.

// kitDBCopyShop is the app's .shared/schema/shop.work, the demo's.
const kitDBCopyShop = `import { table, id, text, decimal, date, timestamp } from "kitwork/schema";
import { connection } from "kitwork/database";

const orders = table({
  id: id(),
  email: text(),
  item: text(),
  amount: decimal(12, 2),
  note: text().nullable(),
  day: date().index(),
  createdAt: timestamp().created()
});

export const db = connection().tables({ orders });
`

// kitDBCopyRoot writes the app acme, its files given from its folder, and its site localhost as newQueueApp
// does, and gives the root.
func kitDBCopyRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if _, site := files["localhost/router.work"]; !site {
		files["localhost/router.work"] = `import * as router from "kitwork/router";` + "\n" + `router.get((context) => context.text("ok"));`
	}
	writeQueueApp(t, root, files)
	return root
}

// kitDBCopyTenant is the app tenant of acme with a runtime of its own, never Run, closed with the test.
func kitDBCopyTenant(t *testing.T, root string) *Tenant {
	t.Helper()
	runtime := app.NewRuntime("acme")
	tenant := NewAppTenantWithRuntimeLayout(root, "acme", RootLayoutMultiTenant, runtime)
	t.Cleanup(func() {
		tenant.Close()
		runtime.Close()
	})
	return tenant
}

// copyKitDB runs `kitwork migrate kitdb` on acme as core does: a tenant of its own, closed after.
func copyKitDB(t *testing.T, root string, dryRun bool) (KitDBCopyReport, error) {
	t.Helper()
	runtime := app.NewRuntime("acme")
	tenant := NewAppTenantWithRuntimeLayout(root, "acme", RootLayoutMultiTenant, runtime)
	defer func() {
		tenant.Close()
		runtime.Close()
	}()
	return tenant.CopyKitDB(KitDBCopyOptions{DryRun: dryRun})
}

// kitDBFixture puts the KitDB files of a fixture of testdata/kitdb/ in acme's .database/ and gives, for each
// table it wrote rows into, the rows as written (their ids, their createdAt). The files were written by the engine
// as it was before KROW went (engine f210bef8), the ORM of KitDB writing the tables .shared/schema/ declares and
// KitDB's SQL the runs (queuestore), so a copy reads the format an app's old file has, not one this test makes.
// The fixtures: orders (kitDBCopyOrders), runs (a job's run pending, one completed, one with a key, a schedule's
// run; the job's definition and the schedule's state), orders-runs (both), decimal (two orders a copy rounds),
// billing-drafts (invoices HD-1, drafts empty, orders), billing-schema (invoices of .schema("billing") and orders),
// schedule-sau (the state of TestKitDBCopyResumesTheSchedules), datastore (storage.kitdb).
func kitDBFixture(t *testing.T, root, name string) [][]map[string]value.Value {
	t.Helper()
	source := filepath.Join("testdata", "kitdb", name)
	for _, folder := range []string{"application", "storage"} {
		entries, err := os.ReadDir(filepath.Join(source, folder))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "acme", ".database", folder)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(source, folder, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, entry.Name()), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(source, "written.json"))
	if err != nil {
		t.Fatal(err)
	}
	var texts [][]map[string]string
	if err := json.Unmarshal(data, &texts); err != nil {
		t.Fatal(err)
	}
	written := make([][]map[string]value.Value, len(texts))
	for index, call := range texts {
		for _, text := range call {
			row := map[string]value.Value{}
			for key, field := range text {
				row[key] = value.New(field)
			}
			written[index] = append(written[index], row)
		}
	}
	return written
}

// The engine's tables of the runs, made by KitDB's SQL (relational, queuestore.InitSchema), read through the
// ORM's decoders (decodeKitDBCatalog, decodeKitDBRow) row for row as KitDB's SQL read them when the fixture was
// written (testdata/kitdb/runs/sql.json): the digest of kitwork_runs, kitwork_definitions and kitwork_schedules is
// the same either way.
//
// CONTROL: with readKitDBCopyStructs keeping no row (the scan's append left out), "kitwork_runs: the ORM read
// 0 rows (digest …), KitDB's SQL 3 (digest …)" (03/11/2026, on the fixture).
func TestKitDBCopyReadsTheQueueTables(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	kitDBFixture(t, root, "runs")
	engine, err := kitDBCopyEngineTables(queuestore.Schema)
	if err != nil {
		t.Fatal(err)
	}
	// what KitDB's SQL read
	data, err := os.ReadFile(filepath.Join("testdata", "kitdb", "runs", "sql.json"))
	if err != nil {
		t.Fatal(err)
	}
	var bySQL map[string]struct {
		Rows   int      `json:"rows"`
		Digest [32]byte `json:"digest"`
	}
	if err := json.Unmarshal(data, &bySQL); err != nil {
		t.Fatal(err)
	}

	source, err := openKitDBCopySource(filepath.Join(root, "acme", ".database", "application", kitDBApplicationFile))
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	structs, err := readKitDBCopyStructs(source.db)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, read := range structs {
		table := engine[read.definition.Name]
		if table == nil {
			continue
		}
		seen++
		if problems := table.fill(read); len(problems) > 0 {
			t.Fatalf("%s: %v", table.name, problems)
		}
		want := bySQL[table.name]
		if len(table.rows) != want.Rows || table.digest(table.rows) != want.Digest {
			t.Errorf("%s: the ORM read %d rows (digest %x), KitDB's SQL %d (digest %x)", table.name, len(table.rows), table.digest(table.rows), want.Rows, want.Digest)
		}
	}
	if seen != 3 || bySQL["kitwork_runs"].Rows != 3 {
		t.Fatalf("the engine's tables read = %d, runs = %d; want 3 and 3", seen, bySQL["kitwork_runs"].Rows)
	}
}

// kitDBCopyOrders are the demo's orders: decimals written as text and as a number, a note left out (null),
// two days.
func kitDBCopyOrders() []map[string]value.Value {
	order := func(email, amount string, note any, day string) map[string]value.Value {
		row := map[string]value.Value{"email": value.New(email), "item": value.New("trà"), "day": value.New(day)}
		if amount != "" {
			row["amount"] = value.New(amount)
		}
		if note != nil {
			row["note"] = value.New(note)
		}
		return row
	}
	sixty := order("d@example.com", "", "y", "2026-10-29")
	sixty["amount"] = value.New(60000)
	return []map[string]value.Value{
		order("a@example.com", "35000", "giao sáng", "2026-10-29"),
		order("b@example.com", "4.5", nil, "2026-10-29"),
		order("c@example.com", "0.05", "x", "2026-10-28"),
		sixty,
	}
}

// kitDBCopyApplication is acme's application.kitdb.
func kitDBCopyApplication(root string) string {
	return filepath.Join(root, "acme", ".database", "application", kitDBApplicationFile)
}

// kitDBCopyPublic is acme's kitwork.db.
func kitDBCopyPublic(root string) string {
	return filepath.Join(root, "acme", ".database", "application", "kitwork.db")
}

// A dry run reads and plans, and makes no file. The copy then moves every row of application.kitdb into
// kitwork.db as it was: the orders of .shared/schema/shop.work (a decimal as the integer × 100, ids and
// createdAt as KitDB wrote them, a null note null), the runs of both kinds, a job's definition and a
// schedule's state; the mark in kitwork_copies holds the digest the report gives, and the file passes
// quick_check. The app booted on kitwork.db reads them back with its own statements: amount > 50000 finds the
// one order of 60000 (compared as text, "4.5" > "50000" would be true), and the largest come in the order of
// their amounts.
//
// CONTROL: with kitDBCopyRead writing a decimal read back as its whole integer (no point), "copy: không chép
// .database/application/application.kitdb sang .database/application/kitwork.db, không ghi gì: bảng orders
// đọc lại 4 dòng, KitDB có 4, digest khác; các khoá khác nhau: …" (the four ids).
func TestKitDBCopyMovesTheAppsRows(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{
		".shared/schema/shop.work": kitDBCopyShop,
		"localhost/router.work": `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";
import { desc } from "kitwork/database";

router.get((context) => {
  const day = "2026-10-29";
  const big = db.list((orders) => orders.day === day && orders.amount > 50000);
  const largest = db.orderBy((orders) => desc(orders.amount)).list(5);
  const count = db.count((orders) => orders.day === day);
  return context.json({ big: big, largest: largest, count: count });
});
`,
	})
	written := kitDBFixture(t, root, "orders-runs")[0]

	planned, err := copyKitDB(t, root, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(kitDBCopyPublic(root)); !os.IsNotExist(err) {
		t.Fatalf("the dry run made kitwork.db (%v)", err)
	}
	if len(planned.Files) != 1 || planned.Files[0].Rows != 9 || planned.Files[0].Copied {
		t.Fatalf("the dry run planned %+v, want 9 rows into kitwork.db, not copied", planned.Files)
	}

	report, err := copyKitDB(t, root, false)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(report.Files) != 1 || !report.Files[0].Copied || report.Files[0].Rows != 9 || report.Files[0].Digest != planned.Files[0].Digest {
		t.Fatalf("copy = %+v, want 9 rows copied with the digest the dry run gave", report.Files)
	}
	tables := map[string]int{}
	for _, table := range report.Files[0].Tables {
		tables[table.Name] = table.Rows
	}
	if tables["orders"] != 4 || tables["kitwork_runs"] != 3 || tables["kitwork_definitions"] != 1 || tables["kitwork_schedules"] != 1 {
		t.Errorf("tables copied = %v", tables)
	}
	path := kitDBCopyPublic(root)
	if mark := fileRows(t, path, `SELECT source, digest, rows FROM kitwork_copies`); len(mark) != 1 ||
		mark[0] != ".database/application/application.kitdb "+report.Files[0].Digest+" 9" {
		t.Errorf("kitwork_copies = %v", mark)
	}
	if check := fileRows(t, path, `PRAGMA quick_check`); len(check) != 1 || check[0] != "ok" {
		t.Errorf("quick_check = %v", check)
	}
	var amounts []string
	for _, row := range written {
		got := fileRows(t, path, `SELECT amount, COALESCE(note, 'NULL'), "createdAt" FROM orders WHERE id = '`+row["id"].String()+`'`)
		if len(got) != 1 || !strings.HasSuffix(got[0], " "+row["createdAt"].String()) {
			t.Errorf("order %s in kitwork.db = %v, want its createdAt %s", row["id"].String(), got, row["createdAt"].String())
			continue
		}
		amounts = append(amounts, got[0][:strings.LastIndex(got[0], " ")])
	}
	if strings.Join(amounts, ",") != "3500000 giao sáng,450 NULL,5 x,6000000 y" {
		t.Errorf("amount and note of each order = %v", amounts)
	}
	if runs := fileRows(t, path, `SELECT kind, status FROM kitwork_runs ORDER BY seq`); strings.Join(runs, ",") != "queue completed,queue pending,schedule pending" {
		t.Errorf("kitwork_runs = %v", runs)
	}

	q := bootQueueApp(t, root)
	answer := q.serve(t, http.MethodGet, "/", nil)
	big, _ := answer["big"].([]any)
	if len(big) != 1 || big[0].(map[string]any)["id"] != written[3]["id"].String() || big[0].(map[string]any)["amount"] != float64(60000) {
		t.Errorf("amount > 50000 = %v, want the order of 60000", answer["big"])
	}
	var largest []string
	for _, item := range answer["largest"].([]any) {
		order := item.(map[string]any)
		largest = append(largest, fmt.Sprint(order["amount"]))
	}
	if strings.Join(largest, ",") != "60000,35000,4.5,0.05" || answer["count"] != float64(3) {
		t.Errorf("largest = %v, count = %v; want 60000,35000,4.5,0.05 and 3", largest, answer["count"])
	}
}

// A decimal the copy would have to round (12.345 in decimal(12, 2)), or one past 15 digits once scaled,
// stops the copy before anything is written: the error names the table, the key and the column, never the
// value, and no kitwork.db is made.
//
// CONTROL: with the exactness check replaced by dropping the extra digits, the cross-check against the
// engine's own write still refused the row ("… cột amount: số nguyên của decimal không khớp số engine ghi cho
// cùng giá trị"); with that off as well, the row of 12.345 went through: "the refusal does not say \"bảng
// orders, khoá …, cột amount: có nhiều chữ số sau dấu phẩy hơn 2 …\"", only the row past 15 digits refused.
func TestKitDBCopyRefusesADecimalItWouldRound(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	// two orders: amount "12.345" and "12345678901234.5"
	written := kitDBFixture(t, root, "decimal")[0]
	_, err := copyKitDB(t, root, false)
	if err == nil {
		t.Fatalf("copy = %v, want refused", err)
	}
	for _, want := range []string{
		"bảng orders, khoá " + written[0]["id"].String() + ", cột amount: có nhiều chữ số sau dấu phẩy hơn 2 của decimal(…, 2): chép phải làm tròn",
		"bảng orders, khoá " + written[1]["id"].String() + ", cột amount: vượt 15 chữ số của decimal",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "12.345") || strings.Contains(err.Error(), "12345678901234") {
		t.Errorf("the refusal quotes a value:\n%v", err)
	}
	if _, statErr := os.Stat(kitDBCopyPublic(root)); !os.IsNotExist(statErr) {
		t.Errorf("a refused copy made kitwork.db (%v)", statErr)
	}
}

// kitDBCopyTargetWith makes acme's kitwork.db holding what statements write, as a file the app wrote.
func kitDBCopyTargetWith(t *testing.T, root string, statements ...string) {
	t.Helper()
	path := kitDBCopyPublic(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// kitDBCopyOrdersTable is the statement the engine makes orders with in kitwork.db.
func kitDBCopyOrdersTable(t *testing.T, root string) string {
	t.Helper()
	tenant := kitDBCopyTenant(t, root)
	if err := tenant.prepareProgram(); err != nil {
		t.Fatal(err)
	}
	declared, _, _, err := tenant.kitDBCopyDeclarations()
	if err != nil || declared["orders"] == nil {
		t.Fatalf("declared = %v, %v", declared, err)
	}
	return schemaDDL("orders", declared["orders"].columns)
}

// A kitwork.db whose orders hold a row already is not copied into: the copy names the table and its count,
// and writes nothing (the order stays the one there, no table of the runs is made).
//
// CONTROL: with no table refused for its rows (refusals giving none), the copy inserted the four orders beside
// o1 and only the read back stopped it: "copy = không chép … bảng orders đọc lại 5 dòng, KitDB có 4, digest
// khác; các khoá khác nhau: o1, want refused".
func TestKitDBCopyRefusesATargetWithRows(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	kitDBFixture(t, root, "orders-runs")
	kitDBCopyTargetWith(t, root, kitDBCopyOrdersTable(t, root),
		`INSERT INTO orders (id, email, item, amount, day, "createdAt") VALUES ('o1', 'z@example.com', 'trà', 100, '2026-10-30', '2026-10-30T01:00:00Z')`)
	_, err := copyKitDB(t, root, false)
	if err == nil || !strings.Contains(err.Error(), "bảng orders của .database/application/kitwork.db đã có 1 dòng: chép vào sẽ trộn dữ liệu cũ với dữ liệu mới") {
		t.Fatalf("copy = %v, want refused", err)
	}
	path := kitDBCopyPublic(root)
	if orders := fileRows(t, path, `SELECT id FROM orders`); strings.Join(orders, ",") != "o1" {
		t.Errorf("orders after a refused copy = %v, want o1 alone", orders)
	}
	if tables := fileRows(t, path, `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'kitwork_%'`); len(tables) != 0 {
		t.Errorf("a refused copy made %v", tables)
	}
}

// kitwork.db of an app booted on the new engine holds the definitions and the schedules boot synced from the
// app's files: those rows win, the copy takes from KitDB only the names kitwork.db lacks, and says how many
// it kept.
//
// CONTROL: with kitwork_definitions and kitwork_schedules not merged (merge false), the copy was refused:
// "bảng kitwork_definitions của .database/application/kitwork.db đã có 1 dòng: chép vào sẽ trộn dữ liệu cũ với
// dữ liệu mới", and the same of kitwork_schedules.
func TestKitDBCopyKeepsWhatBootSynced(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	kitDBFixture(t, root, "runs")
	statements := append([]string(nil), queuestore.Schema...)
	statements = append(statements,
		`INSERT INTO kitwork_definitions (kind, name, source, retry, timeout, timeout_ms, content_hash, updated_at) VALUES ('queue', 'camOn', '.shared/queue/camOn.work', 0, NULL, 0, 'synced', '2026-10-30T00:00:00.000Z')`,
		`INSERT INTO kitwork_schedules (name, rule, declared_at, once_at, last_at, updated_at) VALUES ('other', 'every("1m")', '2026-10-30T00:00:00.000Z', NULL, '2026-10-30T00:00:00.000Z', '2026-10-30T00:00:00.000Z')`)
	kitDBCopyTargetWith(t, root, statements...)
	report, err := copyKitDB(t, root, false)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	kept := map[string]string{}
	for _, table := range report.Files[0].Tables {
		kept[table.Name] = fmt.Sprintf("%d+%d", table.Rows, table.Kept)
	}
	if kept["kitwork_definitions"] != "0+1" || kept["kitwork_schedules"] != "1+0" || kept["kitwork_runs"] != "3+0" {
		t.Errorf("copied+kept = %v", kept)
	}
	path := kitDBCopyPublic(root)
	if definitions := fileRows(t, path, `SELECT name, content_hash FROM kitwork_definitions`); strings.Join(definitions, ",") != "camOn synced" {
		t.Errorf("kitwork_definitions = %v, want the synced row kept", definitions)
	}
	if schedules := fileRows(t, path, `SELECT name FROM kitwork_schedules ORDER BY name`); strings.Join(schedules, ",") != "beat,other" {
		t.Errorf("kitwork_schedules = %v, want beat copied beside other", schedules)
	}
}

// Each SQLite file is one transaction: a copy that fails after its first table leaves no table and no row; the
// file's history the writer made when it opened the file stays.
//
// CONTROL: with the transaction committed after each table (COMMIT, BEGIN IMMEDIATE after its rows), the
// failure after the first left it: "kitwork_definitions holds 1 rows after a failed copy", and "a failed copy
// left the tables [kitwork_runs kitwork_definitions kitwork_schedules orders _kitwork_migrations kitwork_copies]".
func TestKitDBCopyIsOneTransaction(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	kitDBFixture(t, root, "orders-runs")
	var first string
	kitDBCopyInserted = func(_ queuestore.SQL, table string) error {
		if first == "" {
			first = table
			return nil
		}
		return fmt.Errorf("hỏng sau bảng %s", first)
	}
	t.Cleanup(func() { kitDBCopyInserted = nil })
	_, err := copyKitDB(t, root, false)
	if err == nil || !strings.Contains(err.Error(), "hỏng sau bảng kitwork_definitions") {
		t.Fatalf("copy = %v, want the failure after kitwork_definitions", err)
	}
	path := kitDBCopyPublic(root)
	for _, table := range []string{"kitwork_definitions", "kitwork_runs", "orders", "kitwork_copies"} {
		if held := fileRows(t, path, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = '`+table+`'`); len(held) != 0 {
			if count := fileRows(t, path, `SELECT COUNT(*) FROM "`+table+`"`); count[0] != "0" {
				t.Errorf("%s holds %s rows after a failed copy", table, count[0])
			}
		}
	}
	// the file's history (kitwork_history) is the writer's, there from when it opened the file (03/11/2026)
	if tables := fileRows(t, path, `SELECT name FROM sqlite_master WHERE type = 'table' AND name != 'kitwork_history'`); len(tables) != 0 {
		t.Errorf("a failed copy left the tables %v", tables)
	}
}

// What is read back in the transaction is held against KitDB: a value changed between the insert and the read
// (here by the test, in the copy's own transaction) fails the copy, naming the key, and nothing is written.
//
// CONTROL: with the read back in the transaction skipped, the changed row was committed: "orders holds 4 rows
// after a copy that read back a changed value".
func TestKitDBCopyCatchesAChangedValue(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	written := kitDBFixture(t, root, "orders")[0]
	changed := written[1]["id"].String()
	kitDBCopyInserted = func(db queuestore.SQL, table string) error {
		if table != "orders" {
			return nil
		}
		_, err := db.Exec(`UPDATE orders SET amount = 451 WHERE id = ?`, changed)
		return err
	}
	t.Cleanup(func() { kitDBCopyInserted = nil })
	_, err := copyKitDB(t, root, false)
	if err == nil || !strings.Contains(err.Error(), "bảng orders đọc lại 4 dòng, KitDB có 4, digest khác; các khoá khác nhau: "+changed) {
		t.Fatalf("copy = %v, want the read back to name %s", err, changed)
	}
	path := kitDBCopyPublic(root)
	if held := fileRows(t, path, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'orders'`); len(held) != 0 {
		if count := fileRows(t, path, `SELECT COUNT(*) FROM orders`); count[0] != "0" {
			t.Errorf("orders holds %s rows after a copy that read back a changed value", count[0])
		}
	}
}

// kitDBCopyFolderSums is the SHA-256 of every file of a folder, by name.
func kitDBCopyFolderSums(t *testing.T, folder string) map[string]string {
	t.Helper()
	sums := map[string]string{}
	err := filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		relative, _ := filepath.Rel(folder, path)
		sums[filepath.ToSlash(relative)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sums
}

// The KitDB file is never written: every file of .database/application/ (application.kitdb, its WAL, its lock)
// is the same before and after a copy, and the KitDB the copy reads is a private copy outside the app, opened as
// a replica that refuses Begin.
//
// CONTROL: with the private copy opened by kitdbengine.OpenWithOptions instead of OpenReplica, "Begin on the
// KitDB the copy reads = <nil>, want kitdb: replica handle is read-only"; with the KitDB file itself opened
// (as a replica) instead of the private copy, "the copy reads …\acme\.database\application\application.kitdb,
// inside the app" (its bytes were the same after: the sums alone do not see it).
func TestKitDBCopyLeavesTheSourceAlone(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	kitDBFixture(t, root, "orders-runs")
	folder := filepath.Dir(kitDBCopyApplication(root))
	before := kitDBCopyFolderSums(t, folder)
	opened := 0
	kitDBCopyOpened = func(db *kitdbengine.DB) {
		opened++
		if inside, _ := filepath.Rel(filepath.Join(root, "acme"), db.Path()); !strings.HasPrefix(inside, "..") {
			t.Errorf("the copy reads %s, inside the app", db.Path())
		}
		if tx, err := db.Begin(); !errors.Is(err, kitdbengine.ErrReplicaReadOnly) {
			if tx != nil {
				_ = tx.Rollback()
			}
			t.Errorf("Begin on the KitDB the copy reads = %v, want %v", err, kitdbengine.ErrReplicaReadOnly)
		}
	}
	t.Cleanup(func() { kitDBCopyOpened = nil })
	if _, err := copyKitDB(t, root, false); err != nil {
		t.Fatalf("copy: %v", err)
	}
	after := kitDBCopyFolderSums(t, folder)
	for name, sum := range before {
		if after[name] != sum {
			t.Errorf("%s changed: %s, then %s", name, sum, after[name])
		}
	}
	if _, wal := before[kitDBApplicationFile+".wal"]; !wal || opened != 1 {
		t.Errorf("files before = %v, KitDB opened %d times; want a WAL beside application.kitdb, opened once", before, opened)
	}
}

// A KitDB file that changes while it is copied out (a server writing it) stops the copy, and nothing is
// written.
//
// CONTROL: with the files not hashed again after the private copy is made, the copy succeeded: "copy =
// <nil>, want refused".
func TestKitDBCopyRefusesASourceBeingWritten(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	kitDBFixture(t, root, "orders")
	kitDBCopyCopied = func(source string) {
		file, err := os.OpenFile(source+".wal", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Error(err)
			return
		}
		file.Write([]byte{0})
		file.Close()
	}
	t.Cleanup(func() { kitDBCopyCopied = nil })
	_, err := copyKitDB(t, root, false)
	if err == nil || !strings.Contains(err.Error(), "application.kitdb đổi trong lúc được chép ra để đọc: một server đang ghi nó; dừng server rồi chạy lại") {
		t.Fatalf("copy = %v, want refused", err)
	}
	if _, statErr := os.Stat(kitDBCopyPublic(root)); !os.IsNotExist(statErr) {
		t.Errorf("a refused copy made kitwork.db (%v)", statErr)
	}
}

// A table of KitDB that holds rows and that no file of .shared/schema/ declares stops the copy, by name: its
// data would be left behind. An empty one is said and left.
//
// CONTROL: with an undeclared table passed over, the copy succeeded without it: "copy = <nil>, want refused".
func TestKitDBCopyRefusesUndeclaredTables(t *testing.T) {
	const billing = `import { table, id, text } from "kitwork/schema";
import { connection } from "kitwork/database";

const invoices = table({ id: id(), code: text() });
const drafts = table({ id: id(), code: text() });

export const billing = connection().tables({ invoices, drafts });
`
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop, ".shared/schema/billing.work": billing})
	kitDBFixture(t, root, "billing-drafts") // invoices HD-1, drafts empty, orders
	// billing.work goes: invoices holds a row no file declares, drafts none
	if err := os.Remove(filepath.Join(root, "acme", ".shared", "schema", "billing.work")); err != nil {
		t.Fatal(err)
	}
	report, err := copyKitDB(t, root, false)
	if err == nil || !strings.Contains(err.Error(), "bảng invoices (1 dòng): không file nào của .shared/schema/ khai nó trên connection(); dữ liệu của nó sẽ bị bỏ lại") {
		t.Fatalf("copy = %v, want refused", err)
	}
	if !strings.Contains(strings.Join(report.Notes, "\n"), "bảng drafts của .database/application/application.kitdb trống và không file nào của .shared/schema/ khai nó: bỏ qua") {
		t.Errorf("the empty table is not said: %v", report.Notes)
	}
	if _, statErr := os.Stat(kitDBCopyPublic(root)); !os.IsNotExist(statErr) {
		t.Errorf("a refused copy made kitwork.db (%v)", statErr)
	}
}

// Boot of an app whose application.kitdb was not copied says the file is no longer read and that `kitwork
// migrate kitdb` copies the runs left in it, without opening it (KROW goes); once kitwork.db holds the mark of
// the copy, boot neither opens the KitDB file (here no longer a KitDB file at all) nor says anything of it, and
// the worker claims and completes the pending run the copy brought. kitwork.db was made by the first boot, its
// definitions synced: the copy keeps them.
//
// CONTROL (03/11/2026): with appKitDBNotCopied not looking for the mark, "the boot after the copy said: [Queue]
// .database/application/application.kitdb (KitDB cũ) không còn đọc: …".
func TestKitDBCopyBootStopsReadingTheKitDBFile(t *testing.T) {
	watch := watchQueue(t)
	root := kitDBCopyRoot(t, map[string]string{
		".shared/schema/shop.work": kitDBCopyShop,
		".shared/queue/camOn.work": camOnSchedulesJob,
	})
	kitDBFixture(t, root, "runs")
	first := bootQueueApp(t, root)
	said := "[Queue] .database/application/application.kitdb (KitDB cũ) không còn đọc: các lần chạy của job nay nằm ở .database/application/kitwork.db; lần chạy chưa xong trong file cũ ở lại đó và không chạy cho tới khi `kitwork migrate kitdb` chép chúng sang .database/application/kitwork.db"
	if !strings.Contains(first.output, said) {
		t.Fatalf("the boot before the copy did not say %q:\n%s", said, first.output)
	}
	first.close()

	report, err := copyKitDB(t, root, false)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	for _, table := range report.Files[0].Tables {
		if table.Name == "kitwork_definitions" && (table.Rows != 0 || table.Kept != 1) {
			t.Errorf("kitwork_definitions copied %d, kept %d; want the one boot synced kept", table.Rows, table.Kept)
		}
	}
	application := kitDBCopyApplication(root)
	if err := os.WriteFile(application, []byte("not a KitDB file: nothing may open it"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(application + ".wal"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	pending := fileRows(t, kitDBCopyPublic(root), `SELECT id FROM kitwork_runs WHERE kind = 'queue' AND status = 'pending'`)
	if len(pending) != 1 {
		t.Fatalf("pending runs copied = %v, want one", pending)
	}
	second := bootQueueApp(t, root)
	if strings.Contains(second.output, "application.kitdb") {
		t.Errorf("the boot after the copy said:\n%s", second.output)
	}
	watch.until(t, pending[0], "completed")
}

// A schedule's state comes with the copy: a once() that ran before it (its last due time handled at its once
// time) does not run again on kitwork.db, and list() has no next for it.
//
// CONTROL: with kitwork_schedules left out of the copy, boot at 09:00 chose the once() again: "next of sau
// after the copy = 2026-10-23T10:00:00Z, want none".
func TestKitDBCopyResumesTheSchedules(t *testing.T) {
	clock := setClock(t, instant(t, "2026-10-23T03:10:00Z"))
	files := func() map[string]string {
		return map[string]string{
			".schedule/once.work": `import * as schedule from "kitwork/schedule";
export const sau = schedule.every("2h").once((context) => context.job.log("một lần " + context.job.at));
`,
			"localhost/read/router.work": `import * as router from "kitwork/router";
import * as schedule from "kitwork/schedule";
router.get((context) => context.json({ list: schedule.list() }));
`,
		}
	}
	// the same app whose database is still application.kitdb, holding the state the engine kept: synced at 03:10
	// (once at 04:00), run at a boot at 05:00 (testdata/kitdb/schedule-sau, made by that sequence)
	root := kitDBCopyRoot(t, files())
	kitDBFixture(t, root, "schedule-sau")
	if _, err := copyKitDB(t, root, false); err != nil {
		t.Fatalf("copy: %v", err)
	}

	clock.set(instant(t, "2026-10-23T09:00:00Z"))
	after := bootQueueApp(t, root)
	list := listed(t, after.serve(t, http.MethodGet, "/read", nil))
	if next := list["sau"]["next"]; next != nil {
		t.Errorf("next of sau after the copy = %v, want none", next)
	}
	time.Sleep(10 * queuePollInterval)
	if runs := scheduleRuns(t, after, "sau"); len(runs) != 0 {
		t.Errorf("sau ran again after the copy: %+v", runs)
	}
}

// storage.kitdb is copied into kitwork_storage of .database/storage/kitwork.db: each key as the bytes
// datastore() reads it by, each value as it was encoded, its expiry in milliseconds (the nanoseconds of the
// KitDB record divided down, floor), NULL for never, an expired value copied as it is. The engine reads a
// copied value back, and boot says nothing of storage.kitdb once the mark is there.
//
// CONTROL: with the expiry left out of the copy (always NULL), "9:datastore5:codes3:otp expire = {0 false},
// want {1800000060123 true}", and the same of old.
func TestKitDBCopyMovesTheDatastore(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	encode := func(item value.Value) []byte {
		data, err := storage.Encode(item, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	flag := storage.Key{Place: storage.Datastore, Name: "flags", Segments: []string{"checkout"}}.Encoded()
	otp := storage.Key{Place: storage.Datastore, Name: "codes", Segments: []string{"otp"}}.Encoded()
	old := storage.Key{Place: storage.Datastore, Name: "codes", Segments: []string{"old"}}.Encoded()
	values := map[string][]byte{
		flag: encode(value.New(map[string]value.Value{"newFlow": value.New(true)})),
		otp:  encode(value.New("123456")),
		old:  encode(value.New("654321")),
	}
	// written with these expiries, in nanoseconds: flag 0, otp 1_800_000_060_123_456_789, old 1e18
	kitDBFixture(t, root, "datastore")

	report, err := copyKitDB(t, root, false)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(report.Files) != 1 || report.Files[0].Target != ".database/storage/kitwork.db" || report.Files[0].Rows != 3 || !report.Files[0].Copied {
		t.Fatalf("copy = %+v, want 3 rows into .database/storage/kitwork.db", report.Files)
	}
	rows := readDatastoreFile(t, filepath.Join(root, "acme", ".database", "storage", "kitwork.db"))
	for key, want := range map[string]sql.NullInt64{
		flag: {},
		otp:  {Int64: 1_800_000_060_123, Valid: true},
		old:  {Int64: 1_000_000_000_000, Valid: true},
	} {
		if got := rows[key]; got.expire != want || string(got.value) != string(values[key]) {
			t.Errorf("%s expire = %v, want %v; value equal %v", key, got.expire, want, string(got.value) == string(values[key]))
		}
	}
	tenant := kitDBCopyTenant(t, root)
	if err := tenant.prepareProgram(); err != nil {
		t.Fatal(err)
	}
	data, found, err := datastoreAccess{place: &datastorePlace{}, tenant: tenant}.get(flag)
	if decoded, decodeErr := storage.Decode(data); err != nil || !found || decodeErr != nil || !decoded.Get("newFlow").Truthy() {
		t.Errorf("the engine reads checkout = %v %v %v, want { newFlow: true }", decoded, found, err)
	}
	if !datastoreCopied(tenant) {
		t.Errorf("kitwork.db of the datastore holds no mark of the copy")
	}
}

// A column KitDB holds data in that .shared/schema/ no longer declares refuses the copy: the digest is taken on
// the declared columns only, so this check is all that stands between the copy and that data left behind
// unseen. The refusal names the table and the column, and nothing is written.
//
// CONTROL: with fill not looking at the fields no column takes, the copy went through without note: "copy =
// <nil>, want refused".
func TestKitDBCopyRefusesAColumnNoFileDeclares(t *testing.T) {
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop})
	kitDBFixture(t, root, "orders")
	shop := filepath.Join(root, "acme", ".shared", "schema", "shop.work")
	if err := os.WriteFile(shop, []byte(strings.Replace(kitDBCopyShop, "  note: text().nullable(),\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := copyKitDB(t, root, false)
	if err == nil || !strings.Contains(err.Error(), "bảng orders: cột note có dữ liệu trong KitDB mà bảng không có cột đó") {
		t.Fatalf("copy = %v, want refused", err)
	}
	if strings.Contains(err.Error(), "giao sáng") {
		t.Errorf("the refusal quotes a value:\n%v", err)
	}
	if _, statErr := os.Stat(kitDBCopyPublic(root)); !os.IsNotExist(statErr) {
		t.Errorf("a refused copy made kitwork.db (%v)", statErr)
	}
}

// Every SQLite file of a copy is looked at before any is written: with orders in kitwork.db holding a row
// already, the copy is refused for kitwork.db, and billing.db, which would have taken the invoices first, is
// never made.
//
// CONTROL: with the files looked at only as each is written, billing.db was committed before kitwork.db was
// refused: "billing.db = <nil>, want none: a refused copy wrote it".
func TestKitDBCopyLooksAtEveryFileFirst(t *testing.T) {
	const billing = `import { table, id, text } from "kitwork/schema";
import { connection } from "kitwork/database";

const invoices = table({ id: id(), code: text() });

export const billing = connection().schema("billing").tables({ invoices });
`
	root := kitDBCopyRoot(t, map[string]string{".shared/schema/shop.work": kitDBCopyShop, ".shared/schema/billing.work": billing})
	kitDBFixture(t, root, "billing-schema") // invoices HD-1, orders
	kitDBCopyTargetWith(t, root, kitDBCopyOrdersTable(t, root),
		`INSERT INTO orders (id, email, item, amount, day, "createdAt") VALUES ('o1', 'z@example.com', 'trà', 100, '2026-10-30', '2026-10-30T01:00:00Z')`)
	_, err := copyKitDB(t, root, false)
	if err == nil || !strings.Contains(err.Error(), "bảng orders của .database/application/kitwork.db đã có 1 dòng") {
		t.Fatalf("copy = %v, want refused", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "acme", ".database", "application", "billing.db")); !os.IsNotExist(statErr) {
		t.Fatalf("billing.db = %v, want none: a refused copy wrote it", statErr)
	}
}
