package work

import (
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	hostdatabase "github.com/kitwork/engine/database"
	"github.com/kitwork/engine/internal/loopback"
	"github.com/kitwork/engine/value"
)

// KITWORK_DATABASE.md slice 8 (22/10/2026): a database the host names in app.databases is
// connection("tên") in .shared/schema/; a database outside the app is connect({ … }) with only query();
// and every older spelling of the module is an error that says what the spec writes instead.

// bootApp writes the app acme (paths from the app's folder) and boots the site of domain, giving its
// boot error instead of failing the test.
func bootApp(t *testing.T, files map[string]string, domain string) (*Tenant, string, error) {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{}
	for name, source := range files {
		all["acme/"+name] = source
	}
	writeAppTree(t, root, all)
	runtime := app.NewRuntime("acme")
	t.Cleanup(runtime.Close)
	siteRuntime, err := runtime.Site(root, domain)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := generation.SetBytecodeCache(compiler.NewFileCache(filepath.Join(t.TempDir(), "bytecode"))); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenantWithRuntimeLayout(root, domain, RootLayoutMultiTenant, runtime, siteRuntime, generation)
	t.Cleanup(tenant.Close)
	return tenant, root, tenant.Run()
}

// withCatalogue sets the host's app.databases for the test.
func withCatalogue(t *testing.T, entries map[string]hostdatabase.Config) {
	t.Helper()
	t.Cleanup(hostdatabase.SetCatalog(entries))
}

const warehouseSchema = `import { table, id, text, integer } from "kitwork/schema";
import { connection } from "kitwork/database";

const stocks = table({ id: id(), item: text().unique(), quantity: integer() });

export const warehouse = connection("warehouse").tables({ stocks });
`

const warehouseRoute = `import * as router from "kitwork/router";
import { warehouse } from "app/.shared/schema/warehouse.work";

router.post((context) => {
  const item = context.request.form("item");
  const quantity = Number(context.request.form("quantity"));
  return context.json(warehouse.insert((stocks) => ({ item, quantity })));
});

router.get((context) => context.json(warehouse.list((stocks) => stocks.quantity < 5)));
`

// connection("warehouse") declares its tables on the SQLite file the host named in app.databases: the
// statements are the app's own (compiled, checked, bound), the rows live in the host's file, not in the
// app's folder, and a second app that names the same database reads the same rows.
func TestNamedConnectionRunsOnTheHostsSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host", "warehouse.db")
	withCatalogue(t, map[string]hostdatabase.Config{"warehouse": {Type: "sqlite", Name: path}})
	files := map[string]string{
		".shared/schema/warehouse.work": warehouseSchema,
		"shop.example/router.work":      warehouseRoute,
	}
	tenant, root, err := bootApp(t, files, "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	migrateAppForTest(t, root, "acme", RootLayoutMultiTenant)
	for _, stock := range [][]string{{"coffee", "2"}, {"tea", "9"}, {"milk", "4"}} {
		status, body := managedPost(tenant, "/", url.Values{"item": {stock[0]}, "quantity": {stock[1]}})
		if status != http.StatusOK || !strings.Contains(body, `"id":`) {
			t.Fatalf("insert %v = %d %s", stock, status, body)
		}
	}
	status, body := managedGet(tenant, "/")
	rows := rowsOf(t, body)
	if status != http.StatusOK || len(rows) != 2 {
		t.Fatalf("warehouse.list(quantity < 5) = %d %s, want coffee and milk", status, body)
	}

	// the rows are in the host's file
	file, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var stored int
	if err := file.QueryRow(`SELECT COUNT(*) FROM "stocks"`).Scan(&stored); err != nil || stored != 3 {
		t.Fatalf("the host's SQLite holds %d stocks (%v), want 3", stored, err)
	}
	// and nowhere in the app
	for _, private := range []string{filepath.Join(root, "acme", ".database"), filepath.Join(root, "acme", "shop.example", ".data")} {
		if _, err := os.Stat(private); !os.IsNotExist(err) {
			t.Fatalf("%s was made (%v): a named connection wrote into the app", private, err)
		}
	}

	// another site of the app names the same database and reads the same rows
	other, _, err := bootApp(t, map[string]string{
		".shared/schema/warehouse.work": warehouseSchema,
		"depot.example/router.work":     warehouseRoute,
	}, "depot.example")
	if err != nil {
		t.Fatal(err)
	}
	if status, body := managedGet(other, "/"); status != http.StatusOK || len(rowsOf(t, body)) != 2 {
		t.Fatalf("another app reading warehouse = %d %s, want the same two rows", status, body)
	}
}

// A name the host did not declare is a boot error listing the names there are; the names the spec keeps
// for what they hold are refused; a database on a connector whose tables the engine cannot migrate yet is
// refused saying no way reaches it today (the app never sees the host's URL, and connect() refuses a
// private address), and what the host does instead.
func TestNamedConnectionErrors(t *testing.T) {
	files := func(name string) map[string]string {
		return map[string]string{
			".shared/schema/warehouse.work": strings.Replace(warehouseSchema, `connection("warehouse")`, `connection("`+name+`")`, 1),
			"shop.example/router.work":      warehouseRoute,
		}
	}
	withCatalogue(t, map[string]hostdatabase.Config{
		"warehouse": {Type: "postgres", URL: "postgres://kit:secret@db.example:5432/warehouse"},
		"archive":   {Type: "sqlite", Name: filepath.Join(t.TempDir(), "archive.db")},
	})
	_, _, err := bootApp(t, files("stock"), "shop.example")
	if err == nil || !strings.Contains(err.Error(), `connection("stock"): host không khai database tên stock trong app.databases({ … }) của application.work; có: archive, warehouse`) {
		t.Fatalf("an unknown name must stop the boot listing the catalogue: %v", err)
	}
	_, _, err = bootApp(t, files("warehouse"), "shop.example")
	if err == nil || !strings.Contains(err.Error(), `connection("warehouse") trên postgresql chưa có trong engine hôm nay`) ||
		!strings.Contains(err.Error(), "nên app chưa có đường nào tới database này; host khai tên warehouse bằng { sqlite: đường_dẫn } thì chạy") ||
		strings.Contains(err.Error(), "connect(") || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "db.example") {
		t.Fatalf("a named connection on postgresql: %v", err)
	}

	withCatalogue(t, map[string]hostdatabase.Config{})
	_, _, err = bootApp(t, files("stock"), "shop.example")
	if err == nil || !strings.Contains(err.Error(), "host chưa khai tên nào") {
		t.Fatalf("an empty catalogue: %v", err)
	}

	// what the spec reserves, refused by the module where the compiler did not see it
	module := &DatabaseModule{}
	for name, want := range map[string]string{
		"application": `connection("application") không có: database của app là connection() không tên`,
		"storage":     `"storage" là tên dành cho datastore()`,
		"files":       `"files" là tên dành cho nơi lưu file dùng chung`,
	} {
		code, message := failureOf(module.Connection().Call("connection", value.New(name)))
		if code != value.InvalidArgumentCode || !strings.HasPrefix(message, want) {
			t.Errorf("connection(%q): %s %q", name, code, message)
		}
	}
}

// A host's database of connection("tên") is the root of its structure, as the app's own is (KITWORK_DATABASE.md
// "Database là gốc", slice 4): an app whose table retypes a column the file has fails its statements naming it, and the
// file keeps its columns and rows; a column the app adds fails them too, until kitwork migrate adds it, the rows
// staying. A column the file has and the app does not declare is the database's own.
func TestNamedConnectionRefusesALossOfData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host", "warehouse.db")
	withCatalogue(t, map[string]hostdatabase.Config{"warehouse": {Type: "sqlite", Name: path}})
	tenant, root, err := bootApp(t, map[string]string{
		".shared/schema/warehouse.work": warehouseSchema,
		"shop.example/router.work":      warehouseRoute,
	}, "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	migrateAppForTest(t, root, "acme", RootLayoutMultiTenant)
	if status, body := managedPost(tenant, "/", url.Values{"item": {"coffee"}, "quantity": {"2"}}); status != http.StatusOK {
		t.Fatalf("insert = %d %s", status, body)
	}

	listing := `import * as router from "kitwork/router";
import { safe } from "kitwork";
import { warehouse } from "app/.shared/schema/warehouse.work";

router.get((context) => {
  const { ok, value, error } = safe(() => warehouse.list((stocks) => stocks.id !== null));
  return context.json(ok ? { rows: value } : { code: error.code, message: error.message });
});
`
	declaring := func(columns string) map[string]string {
		return map[string]string{
			".shared/schema/warehouse.work": strings.Replace(warehouseSchema, `item: text().unique(), quantity: integer()`, columns, 1),
			"depot.example/router.work":     listing,
		}
	}

	// quantity retyped: refused, nothing applied; item, which this app does not declare, is the database's own and stays
	other, _, err := bootApp(t, declaring(`quantity: text()`), "depot.example")
	if err != nil {
		t.Fatal(err)
	}
	refused := answer(t, other, "")
	const want = `database connection("warehouse") (warehouse.db) chưa khớp bảng .shared/schema/ khai (database là gốc: engine không tự tạo bảng, thêm cột); ` +
		`khác database: cột stocks.quantity: database lưu INTEGER, code khai text (lưu TEXT); sửa table() theo database (kitwork migrate không đổi kiểu cột đã có)`
	if refused["code"] != value.DatabaseCode || refused["message"] != want {
		t.Fatalf("a table that retypes a column: %v, want DATABASE %q", refused, want)
	}
	file, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	columns := func() map[string]string {
		t.Helper()
		rows, err := file.Query(`SELECT name, type FROM pragma_table_info('stocks')`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		found := map[string]string{}
		for rows.Next() {
			var name, kind string
			if err := rows.Scan(&name, &kind); err != nil {
				t.Fatal(err)
			}
			found[name] = kind
		}
		return found
	}
	var item string
	if live := columns(); live["item"] == "" || live["quantity"] != "INTEGER" {
		t.Fatalf("the host's file after the refusal: %v, want item kept and quantity INTEGER", live)
	}
	if err := file.QueryRow(`SELECT item FROM stocks`).Scan(&item); err != nil || item != "coffee" {
		t.Fatalf("the row after the refusal: %q %v", item, err)
	}

	// a column added: the statements fail until kitwork migrate adds it, the rows staying
	added, addedRoot, err := bootApp(t, declaring(`item: text().unique(), quantity: integer(), note: text().nullable()`), "depot.example")
	if err != nil {
		t.Fatal(err)
	}
	if lacking := answer(t, added, ""); lacking["code"] != value.DatabaseCode ||
		!strings.Contains(fmt.Sprint(lacking["message"]), "database chưa có: bảng stocks chưa có cột note text; thêm bằng kitwork migrate") {
		t.Fatalf("a table that adds a column, before kitwork migrate: %v", lacking)
	}
	if live := columns(); live["note"] != "" {
		t.Fatalf("a statement added the column: %v", live)
	}
	migrateAppForTest(t, addedRoot, "acme", RootLayoutMultiTenant)
	if listed := answer(t, added, ""); count(t, listed, "rows") != 1 {
		t.Fatalf("a struct that adds a column: %v, want the one row", listed)
	}
	if live := columns(); live["note"] != "TEXT" || live["item"] == "" {
		t.Fatalf("the host's file after an addition: %v, want note added", live)
	}

	// what kitwork check prints for such a plan names no removed option
	for _, step := range planMigration(map[string]string{"id": "TEXT", "item": "TEXT", "quantity": "INTEGER"},
		map[string]*ColumnSpec{"id": {kind: "text"}, "quantity": {kind: "text"}}, false) {
		if line := step.describe(); strings.Contains(line, "drop: true") || !strings.Contains(line, "REFUSED") {
			t.Errorf("plan line %q", line)
		}
	}
}

// The older words of kitwork/database are errors by their name wherever they are read, saying what the
// spec writes; the compiler refuses them where it sees them (compiler/database_removed_test.go), and the
// module refuses a read the compiler did not see. An unknown word is nothing, as on any module.
func TestDatabaseModuleOldWordsAreErrors(t *testing.T) {
	module := value.New(&DatabaseModule{})
	for word, want := range map[string]string{
		"kitdb":     "database.kitdb đã bỏ: database của app khai export const db = connection().tables({ bảng }) trong app/.shared/schema/<tên>.work; mở KitDB ra ngoài qua PostgreSQL wire (token, access) là app.database(path, { port }) của host trong application.work",
		"sqlite":    `database.sqlite đã bỏ: SQLite là connection("tên")`,
		"struct":    `database.struct đã bỏ: bảng khai bằng table() của "kitwork/schema"`,
		"text":      `database.text đã bỏ: kiểu cột import từ "kitwork/schema"`,
		"kitid":     "database.kitid đã bỏ: kiểu cột của \"kitwork/schema\", chỉ trong app/.shared/schema/: viết id()",
		"entity":    "database.entity đã bỏ: mỗi app có database của nó",
		"system":    "database.system đã bỏ: mỗi app có database của nó",
		"define":    "database.define đã bỏ: database của app khai",
		"atomic":    "database.atomic đã bỏ: giao dịch là db.transaction((database) => …)",
		"connected": `database.connected đã bỏ: database có tên là connection("tên")`,
		"table":     `database.table đã bỏ: câu viết trên database khai trong app/.shared/schema/: import { db } from "app/.shared/schema/<tên>.work" rồi db.list((bảng) => …)`,
		"where":     "database.where đã bỏ: câu viết trên database",
	} {
		code, message := failureOf(module.Get(word))
		if code != value.InvalidArgumentCode || !strings.HasPrefix(message, want) {
			t.Errorf("database.%s: %s %q, want INVALID_ARGUMENT %q…", word, code, message, want)
		}
	}
	if got := module.Get("frobnicate"); !got.IsNil() {
		t.Errorf("an unknown word: %v, want nothing", got)
	}
	for _, word := range []string{"connection", "connect", "desc", "one", "count"} {
		if got := module.Get(word); got.K != value.Func {
			t.Errorf("database.%s: %v, want the function of the spec", word, got)
		}
	}

	// in a site: a read by a key the compiler does not see is the same error where it is read
	tenant, _ := mustBoot(t, map[string]string{
		"router.work": `import * as router from "kitwork/router";
import * as database from "kitwork/database";
import { safe } from "kitwork";
router.get((ctx) => {
  const word = ctx.query("word");
  const { error } = safe(() => database[word]);
  return ctx.json({ code: error.code, message: error.message });
});`,
	})
	code, body, raw := hitJSON(t, tenant, "/?word=kitdb")
	if message, _ := body["message"].(string); code != 200 || body["code"] != value.InvalidArgumentCode || !strings.HasPrefix(message, "database.kitdb đã bỏ:") {
		t.Fatalf("database[\"kitdb\"] at run time: code=%d body=%s", code, raw)
	}
}

// crmApp is an app whose .shared/schema/crm.work connects to the database at address (a SQLite file served
// over the PostgreSQL wire protocol, startPostgresTestServer, which lib/pq speaks to as to any Postgres), with
// the password given, and whose route queries it with a value from the request.
func crmApp(t *testing.T, address, password string) map[string]string {
	t.Helper()
	connection := &url.URL{Scheme: "postgres", User: url.UserPassword("kitdb", password), Host: address, Path: "/transactions"}
	query := connection.Query()
	query.Set("sslmode", "disable")
	query.Set("connect_timeout", "3")
	connection.RawQuery = query.Encode()
	return crmAppAt(connection.String())
}

// crmAppAt is crmApp with the URL written as it is.
func crmAppAt(connection string) map[string]string {
	return map[string]string{
		".shared/schema/crm.work": `import { environment } from "kitwork";
import { connect } from "kitwork/database";

export const crm = connect({ postgresql: environment.CRM_URL });
`,
		"shop.example/.env": "CRM_URL=" + connection + "\n",
		"shop.example/router.work": `import * as router from "kitwork/router";
import { safe } from "kitwork";
import { crm } from "app/.shared/schema/crm.work";

router.get((context) => {
  const sku = context.query("sku");
  const { ok, value, error } = safe(() => crm.query("SELECT sku, title, price FROM products WHERE sku = $1", [sku]));
  return context.json(ok ? { rows: value } : { code: error.code, message: error.message });
});
`,
	}
}

// connect({ postgresql: url }) is the app's own connection to a database outside it: query(sql, [giá
// trị]) binds every value (an injection in the value matches nothing), gives the rows by column name, and
// dials through the SSRF guard of kitwork/http. Its errors never hold the address or the password.
func TestConnectQueryBindsValuesThroughTheGuard(t *testing.T) {
	address := startPostgresTestServer(t, "transaction-secret", `INSERT INTO products (id, sku, title, price) VALUES ('a', 'CAF', 'Cà phê', 35000), ('b', 'TEA', 'Trà', 20000)`)

	// the guard first: the database is on this machine, which an app's code does not reach
	tenant, _, err := bootApp(t, crmApp(t, address, "transaction-secret"), "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	denied := answer(t, tenant, "?sku=CAF")
	if denied["code"] != value.DeniedCode || denied["message"] != "connect({ postgresql: … }): địa chỉ của database nằm trong mạng riêng hay trên chính máy này, bị chặn" {
		t.Fatalf("a database on this machine: %v", denied)
	}

	// the engine's own tests may open loopback (internal/loopback), as kitwork/http's do
	restore := loopback.Open()
	defer restore()
	tenant, _, err = bootApp(t, crmApp(t, address, "transaction-secret"), "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	found := answer(t, tenant, "?sku=CAF")
	rows, _ := found["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("crm.query(…, [\"CAF\"]) = %v, want one row", found)
	}
	row := rows[0].(map[string]any)
	if row["sku"] != "CAF" || row["title"] != "Cà phê" || fmt.Sprint(row["price"]) != "35000" {
		t.Fatalf("the row = %v", row)
	}
	injected := answer(t, tenant, "?sku="+url.QueryEscape("CAF' OR '1'='1"))
	if rows, _ := injected["rows"].([]any); injected["rows"] == nil || len(rows) != 0 {
		t.Fatalf("a value that holds SQL must match nothing: %v", injected)
	}

	// a password the database refuses, and an address nothing answers at: each error says what happened,
	// never the password, the address, the port or the user (the driver's own text names the address of a
	// dial, "dial tcp 127.0.0.1:1: …", and the user of a login)
	_, port, _ := net.SplitHostPort(address)
	for _, test := range []struct {
		name, address, password, code, message string
	}{
		{"a refused login", address, "wrong-secret-value", value.DeniedCode, "connect({ postgresql: … }): database từ chối đăng nhập hay không có database đó"},
		{"a refused dial", "127.0.0.1:1", "transaction-secret", value.UnavailableCode, "connect({ postgresql: … }): không nối được database"},
	} {
		tenant, _, err = bootApp(t, crmApp(t, test.address, test.password), "shop.example")
		if err != nil {
			t.Fatal(err)
		}
		failed := answer(t, tenant, "?sku=CAF")
		message, _ := failed["message"].(string)
		if failed["code"] != test.code || message != test.message {
			t.Fatalf("%s: %v, want %s %q", test.name, failed, test.code, test.message)
		}
		for _, secret := range []string{test.password, "127.0.0.1", ":" + port, ":1", "kitdb"} {
			if strings.Contains(message, secret) {
				t.Fatalf("%s: the message holds %q: %q", test.name, secret, message)
			}
		}
	}
}

// The host turns connect() off with app.policy({ tenantConnections: false }): an app that declares one does
// not boot, saying why and what reaches a database instead.
func TestConnectOffByTheHost(t *testing.T) {
	SetTenantConnections(false)
	t.Cleanup(func() { SetTenantConnections(true) })
	_, _, err := bootApp(t, crmApp(t, "127.0.0.1:1", "x"), "shop.example")
	if err == nil || !strings.Contains(err.Error(), "connect(…) bị host tắt: application.work khai app.policy({ tenantConnections: false })") {
		t.Fatalf("connect() with the host's policy off: %v", err)
	}
}

// connect({ … }) checks its declaration before anything opens, and its handle answers only query.
func TestConnectDeclaration(t *testing.T) {
	for _, test := range []struct {
		source value.Value
		code   string
		want   string
	}{
		{value.New(map[string]value.Value{"sqlite": value.New("./x.db")}), value.InvalidArgumentCode, "connect({ sqlite: … }) không có"},
		{value.New(map[string]value.Value{"redis": value.New("redis://h")}), value.InvalidArgumentCode, "connect({ redis: … }): connect nhận một object có một bộ nối"},
		{value.New(map[string]value.Value{"postgresql": value.NewNil()}), value.InvalidArgumentCode, "connect({ postgresql: … }) cần URL của database là một chuỗi, như environment.CRM_URL; giá trị ở đây là null"},
		{value.New(map[string]value.Value{"postgresql": value.New("mysql://u:secret@h/db")}), value.InvalidArgumentCode, "URL có scheme mysql, không phải của postgresql"},
		{value.New(map[string]value.Value{"mysql": value.New("mysql://u:secret@h/db")}), value.UnsupportedCode, "bản Kitwork này không có bộ nối mysql"},
		{value.New("postgres://u:secret@h/db"), value.InvalidArgumentCode, "connect nhận một object có một bộ nối"},
	} {
		code, message := failureOf(connectExternal(nil, []value.Value{test.source}))
		if code != test.code || !strings.Contains(message, test.want) || strings.Contains(message, "secret") {
			t.Errorf("connect(%v): %s %q, want %s …%s…", test.source, code, message, test.code, test.want)
		}
	}
	handle := connectExternal(nil, []value.Value{value.New(map[string]value.Value{"postgresql": value.New("postgres://u:p@db.example/x")})})
	external, ok := handle.V.(*ExternalDatabase)
	if !ok {
		t.Fatalf("connect({ postgresql }): %v", handle)
	}
	if code, message := failureOf(external.OnInvoke("list", value.NewFunc(func(...value.Value) value.Value { return value.NewNil() }))); code != value.InvalidArgumentCode ||
		message != "list không có: database ngoài khai bằng connect(…) chỉ có query(sql, [giá trị])" {
		t.Errorf("crm.list: %s %q", code, message)
	}
	for _, args := range [][]value.Value{
		{},
		{value.New(1)},
		{value.New("SELECT 1"), value.New("not an array")},
		{value.New("SELECT $1"), value.New([]value.Value{value.New(map[string]value.Value{})})},
	} {
		if code, _ := failureOf(external.OnInvoke("query", args...)); code != value.InvalidArgumentCode {
			t.Errorf("crm.query(%v): %s, want INVALID_ARGUMENT before anything opens", args, code)
		}
	}
}

// A query that runs past the bound of rows fails TOO_LARGE rather than holding them all; a statement that
// gives no rows gives []. The pool of the handle's configuration is put in the app's pool beforehand, a
// SQLite file of 10001 rows, so the bound is reached without a server.
func TestConnectQueryBounds(t *testing.T) {
	tenant, _, err := bootApp(t, map[string]string{"shop.example/router.work": `import * as router from "kitwork/router";`}, "shop.example")
	if err != nil {
		t.Fatal(err)
	}
	handle := connectExternal(tenant, []value.Value{value.New(map[string]value.Value{"postgresql": value.New("postgres://u:p@db.example/crm")})})
	external, ok := handle.V.(*ExternalDatabase)
	if !ok {
		t.Fatalf("connect: %v", handle)
	}
	rows, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "rows.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rows.Exec(fmt.Sprintf(`CREATE TABLE products AS WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d) SELECT 'S' || i AS sku FROM n`, externalRowCeiling+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := tenant.databaseManager().Open(external.poolKey(), func() (*sql.DB, error) { return rows, nil }); err != nil {
		t.Fatal(err)
	}
	code, message := failureOf(external.OnInvoke("query", value.New("SELECT sku FROM products"), value.New([]value.Value{})))
	if code != value.TooLargeCode || message != "query trả quá 10000 dòng: thêm LIMIT vào câu, đọc theo trang" {
		t.Fatalf("past the bound: %s %q", code, message)
	}
	if bounded := external.OnInvoke("query", value.New("SELECT sku FROM products LIMIT 3")); bounded.K != value.Array || len(bounded.Array()) != 3 {
		t.Fatalf("under the bound: %v", bounded)
	}
	if none := external.OnInvoke("query", value.New("SELECT sku FROM products WHERE sku = $1"), value.New([]value.Value{value.New("nothing")})); none.K != value.Array || len(none.Array()) != 0 {
		t.Fatalf("no rows: %v, want []", none)
	}
}
