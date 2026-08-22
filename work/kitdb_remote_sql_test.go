package work

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBRemoteSQLParserFailsClosed(t *testing.T) {
	bindings := kitSQLBindings{
		positional: []value.Value{value.New("KIT-1"), value.New(20)},
		named:      map[string]value.Value{"status": value.New("active")},
	}
	statement, err := parseKitSQL(
		`SELECT sku, price FROM products WHERE sku = ? AND price >= ? AND status = :status ORDER BY price DESC LIMIT 20`,
		bindings,
	)
	if err != nil {
		t.Fatal(err)
	}
	if statement.kind != "select" || statement.table != "products" || len(statement.conditions) != 3 || len(statement.orders) != 1 || statement.limit != 20 {
		t.Fatalf("unexpected statement: %#v", statement)
	}

	for _, source := range []string{
		`DROP TABLE products`,
		`SELECT * FROM products; DELETE FROM products`,
		`SELECT * FROM products -- hidden`,
		`SELECT * FROM products WHERE sku = 'A' OR sku = 'B'`,
		`SELECT * FROM products JOIN users ON users.id = products.id`,
		`UPDATE products SET price = 1`,
	} {
		parsed, err := parseKitSQL(source, kitSQLBindings{named: map[string]value.Value{}})
		if err == nil && parsed.kind != "update" {
			t.Errorf("parseKitSQL(%q) unexpectedly succeeded: %#v", source, parsed)
		}
		if parsed.kind == "update" && len(parsed.conditions) != 0 {
			t.Errorf("unsafe update unexpectedly gained conditions: %#v", parsed)
		}
	}
}

func TestKitDBRemoteSQLConnectionProbe(t *testing.T) {
	statement, err := parseKitSQL(`SELECT 1 AS connected, ? AS label, true AS ready`, kitSQLBindings{
		positional: []value.Value{value.New("kitdb")},
		named:      map[string]value.Value{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if statement.kind != "select_scalar" || len(statement.scalars) != 3 {
		t.Fatalf("unexpected scalar statement: %#v", statement)
	}
	result := executeKitDBRemoteScalar(statement)
	if len(result.rows) != 1 || len(result.rows[0]) != 3 || result.columns[0].name != "connected" || result.rows[0][0].N != 1 {
		t.Fatalf("unexpected scalar result: %#v", result)
	}

	for _, source := range []string{
		`SELECT 1; DELETE FROM products`,
		`SELECT sqlite_version()`,
		`SELECT (SELECT 1)`,
	} {
		if _, err := parseKitSQL(source, kitSQLBindings{named: map[string]value.Value{}}); err == nil {
			t.Fatalf("parseKitSQL(%q) unexpectedly succeeded", source)
		}
	}
}

func FuzzKitDBRemoteSQLParser(f *testing.F) {
	for _, source := range []string{
		`SELECT * FROM products WHERE sku = ? LIMIT 20`,
		`INSERT INTO products (sku, price) VALUES ('KIT-1', 10) RETURNING *`,
		`UPDATE products SET price = :price WHERE sku = :sku`,
		`DELETE FROM products WHERE id IN (?, ?)`,
		`PRAGMA table_info("products")`,
		`SELECT * FROM products; DROP TABLE products`,
		"SELECT '\xff' FROM products",
	} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > kitDBRemoteSQLBytes+1 {
			return
		}
		bindings := kitSQLBindings{
			positional: []value.Value{value.New("one"), value.New("two")},
			named: map[string]value.Value{
				"price": value.New(10), "sku": value.New("KIT-1"),
			},
		}
		_, _ = parseKitSQL(source, bindings)
	})
}

func TestKitDBHranaOverRealURLAndBearerAuth(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int, enum } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  title: text().notNull(),
  price: int().default(0).index(),
  status: enum("active", "disabled").default("active").index()
});
const remote = kitdb("remote.kitdb", { products }, { token: "remote-secret", access: "readwrite" });
const readonly = kitdb("readonly.kitdb", { products }, { token: "readonly-secret" });
router.get((ctx) => {
  const item = remote.products.where("sku", "=", "KIT-1").first();
  return ctx.json({ count: remote.products.count(), price: item ? item.price : null });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Host = "localhost"
		tenant.Serve(writer, request)
	}))
	defer server.Close()

	response, err := http.Get(server.URL + "/remote.kitdb/v3")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("version probe status = %d", response.StatusCode)
	}

	pipeline := func(path, token string, body any) (int, map[string]any) {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		decoded := map[string]any{}
		_ = json.NewDecoder(response.Body).Decode(&decoded)
		return response.StatusCode, decoded
	}
	execute := func(path, token, sql string, args []map[string]any, wantRows bool) (int, map[string]any) {
		t.Helper()
		return pipeline(path, token, map[string]any{
			"baton": nil,
			"requests": []any{
				map[string]any{"type": "execute", "stmt": map[string]any{"sql": sql, "args": args, "want_rows": wantRows}},
				map[string]any{"type": "close"},
			},
		})
	}
	textArg := func(text string) map[string]any { return map[string]any{"type": "text", "value": text} }
	intArg := func(number string) map[string]any { return map[string]any{"type": "integer", "value": number} }

	if status, _ := execute("/remote.kitdb/v3/pipeline", "wrong", `SELECT * FROM products`, nil, true); status != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", status)
	}

	status, inserted := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`INSERT INTO products (sku, title, price) VALUES (?, ?, ?) RETURNING id, sku, status`,
		[]map[string]any{textArg("KIT-1"), textArg("KitDB One"), intArg("100")}, true,
	)
	assertHranaOK(t, status, inserted, `"KIT-1"`, `"active"`)
	status, inserted = execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`INSERT INTO products (sku, title, price, status) VALUES (?, ?, ?, ?) RETURNING *`,
		[]map[string]any{textArg("KIT-2"), textArg("KitDB Two"), intArg("200"), textArg("disabled")}, true,
	)
	assertHranaOK(t, status, inserted, `"KIT-2"`, `"disabled"`)

	status, selected := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT sku, title, price FROM products WHERE price >= ? ORDER BY price DESC LIMIT ?`,
		[]map[string]any{intArg("100"), intArg("20")}, true,
	)
	assertHranaOK(t, status, selected, `"KIT-2"`, `"KIT-1"`, `"value":"200"`)
	status, stored := pipeline("/remote.kitdb/v3/pipeline", "remote-secret", map[string]any{
		"baton": nil,
		"requests": []any{
			map[string]any{"type": "store_sql", "sql_id": 7, "sql": `SELECT sku FROM products WHERE sku = :sku`},
			map[string]any{"type": "execute", "stmt": map[string]any{
				"sql_id": 7, "named_args": []any{map[string]any{"name": "sku", "value": textArg("KIT-2")}}, "want_rows": true,
			}},
			map[string]any{"type": "close_sql", "sql_id": 7},
			map[string]any{"type": "close"},
		},
	})
	assertHranaOK(t, status, stored, `"KIT-2"`, `"store_sql"`, `"close_sql"`)

	status, updated := execute(
		"/remote.kitdb/v2/pipeline", "remote-secret",
		`UPDATE products SET price = ? WHERE sku = ?`,
		[]map[string]any{intArg("125"), textArg("KIT-1")}, false,
	)
	assertHranaOK(t, status, updated, `"affected_row_count":1`)

	status, catalog := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret",
		`SELECT name, type FROM sqlite_master WHERE type = 'table' ORDER BY name LIMIT 20`, nil, true,
	)
	assertHranaOK(t, status, catalog, `"products"`, `"table"`)
	status, pragma := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret", `PRAGMA table_info(products)`, nil, true,
	)
	assertHranaOK(t, status, pragma, `"sku"`, `"price"`, `"status"`)

	status, unsafe := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret", `DELETE FROM products`, nil, false,
	)
	assertHranaError(t, status, unsafe, "DELETE requires WHERE")
	status, transaction := execute(
		"/remote.kitdb/v3/pipeline", "remote-secret", `BEGIN`, nil, false,
	)
	assertHranaError(t, status, transaction, "interactive transactions")
	status, readonlyWrite := execute(
		"/readonly.kitdb/v3/pipeline", "readonly-secret",
		`INSERT INTO products (sku, title) VALUES (?, ?)`, []map[string]any{textArg("NO"), textArg("No")}, false,
	)
	assertHranaError(t, status, readonlyWrite, "read-only")

	request, err := http.NewRequest(http.MethodPost, server.URL+dataAPIQueryPath, strings.NewReader(
		`{"db":"remote.kitdb","sql":"SELECT sku, price FROM products WHERE sku = ?","args":["KIT-1"]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer remote-secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	dataBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(dataBody), `"sku":"KIT-1"`) || !strings.Contains(string(dataBody), `"price":125`) {
		t.Fatalf("KitDB data API status = %d, body = %s", response.StatusCode, dataBody)
	}

	response, err = http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	routeBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(routeBody), `"count":2`) || !strings.Contains(string(routeBody), `"price":125`) {
		t.Fatalf("ORM view status = %d, body = %s", response.StatusCode, routeBody)
	}
}

func TestKitDBOfficialLibSQLClient(t *testing.T) {
	clientDirectory := os.Getenv("KITDB_LIBSQL_CLIENT_DIR")
	if clientDirectory == "" {
		t.Skip("set KITDB_LIBSQL_CLIENT_DIR to a directory containing @libsql/client")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int } = database;
const products = struct({ id: id(), sku: text().notNull().unique().index(), title: text().notNull(), price: int().default(0) });
const db = kitdb("official.kitdb", { products }, { token: "official-secret", access: "readwrite" });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Host = "localhost"
		tenant.Serve(writer, request)
	}))
	defer server.Close()

	script := `
import { createClient } from "@libsql/client";
const client = createClient({ url: process.env.KITDB_URL, authToken: "official-secret" });
const probe = await client.execute("SELECT 1 AS connected");
if (Number(probe.rows[0].connected) !== 1) {
  throw new Error("connection probe failed: " + JSON.stringify(probe.rows));
}
const inserted = await client.execute({
  sql: "INSERT INTO products (sku, title, price) VALUES (?, ?, ?) RETURNING sku, price",
  args: ["NODE-1", "Official client", 42],
});
const selected = await client.execute({
  sql: "SELECT sku, price FROM products WHERE sku = ?",
  args: ["NODE-1"],
});
if (inserted.rows[0].sku !== "NODE-1" || Number(selected.rows[0].price) !== 42) {
  throw new Error("unexpected KitDB rows: " + JSON.stringify({ inserted: inserted.rows, selected: selected.rows }));
}
client.close();
console.log("official-libsql-client-ok");`
	command := exec.Command("node", "--input-type=module", "--eval", script)
	command.Dir = clientDirectory
	command.Env = append(os.Environ(), "KITDB_URL="+server.URL+"/official.kitdb")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("@libsql/client failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "official-libsql-client-ok") {
		t.Fatalf("unexpected @libsql/client output: %s", output)
	}
}

func assertHranaOK(t *testing.T, status int, body map[string]any, contains ...string) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	if status != http.StatusOK || strings.Contains(string(encoded), `"type":"error"`) {
		t.Fatalf("Hrana status = %d, body = %s", status, encoded)
	}
	for _, expected := range contains {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("Hrana body = %s, want %s", encoded, expected)
		}
	}
}

func assertHranaError(t *testing.T, status int, body map[string]any, contains string) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	if status != http.StatusOK || !strings.Contains(string(encoded), `"type":"error"`) || !strings.Contains(string(encoded), contains) {
		t.Fatalf("Hrana error status = %d, body = %s, want %q", status, encoded, contains)
	}
}
