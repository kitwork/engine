package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/database/fulltext"
	"github.com/kitwork/engine/work"
)

// startSearchPort starts the port on host with search declared, and returns its address and runtime.
func startSearchPort(t *testing.T, host string, search []AppDatabaseSearch) (*appDatabaseFixture, *appDatabaseRuntime) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	runtime, err := startAppDatabaseRuntime(ctx, AppDatabaseConfig{
		Path: host, Host: "127.0.0.1", Port: port, User: "kitdb", Password: appDatabaseTestPassword, Search: search,
	}, appDatabaseApps{root: filepath.Join(filepath.Dir(host), "apps"), layout: work.RootLayoutMultiTenant}, make(chan error, 1))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = runtime.Close() // once: a test may have closed it
	})
	return &appDatabaseFixture{host: host, address: fmt.Sprintf("127.0.0.1:%d", port)}, runtime
}

// waitForSearch runs query until the index answers (not 55000, being built) or ten seconds pass.
func waitForSearch(t *testing.T, client *sql.DB, query string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := appDatabaseQuery(client, query)
		if err == nil {
			return appDatabaseRows(t, client, query)
		}
		if appDatabaseCode(err) != "55000" || time.Now().After(deadline) {
			t.Fatalf("%s: %v", query, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// MATCH on the port: the declared columns of a host database's table are indexed in kitwork.db.search at start,
// found without accents and case, counted; a restart uses the index the table has not moved from; a row another
// connection adds is found once the watch built the index again; an undeclared table says how to declare it.
//
// CONTROL: with reuse always failing, "after a restart: index ready false, 0 rows built again, want the index used
// as it is"; with watch never queueing a build, "after another connection added a row: "", want trà sữa"; with Count
// giving every kind to the index's count, "… MATCH 'akko OR logitech': … Count supports implicit-AND text queries
// only".
func TestAppDatabasePortSearch(t *testing.T) {
	previous := fulltext.Watch
	fulltext.Watch = 50 * time.Millisecond
	t.Cleanup(func() { fulltext.Watch = previous })

	host := filepath.Join(t.TempDir(), ".database")
	file := filepath.Join(host, "shopping", "kitwork.db")
	writeAppDatabaseFile(t, file,
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT NOT NULL, brand TEXT NOT NULL, merchant TEXT NOT NULL)`,
		`INSERT INTO products (name, brand, merchant) VALUES ('Cà phê sữa đá', 'Highlands', 'shopee'), ('Bàn phím Logitech MX', 'Logitech', 'shopee'), ('Bàn phím cơ', 'Akko', 'lazada')`,
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`)
	declared := []AppDatabaseSearch{{Database: "shopping", Schema: "public", Table: "products",
		Columns: []AppDatabaseSearchColumn{{Name: "brand", Boost: 1}, {Name: "name", Boost: 5}}}}

	fixture, runtime := startSearchPort(t, host, declared)
	client := fixture.connect(t, "kitdb", appDatabaseTestPassword, "shopping")
	if got := waitForSearch(t, client, `SELECT name, merchant FROM products WHERE products MATCH 'ca phe' LIMIT 5`); got != "Cà phê sữa đá shopee" {
		t.Fatalf("table MATCH = %q", got)
	}
	if got := appDatabaseRows(t, client, `SELECT name FROM products WHERE products MATCH $1 AND merchant = $2`, "BAN PHIM", "lazada"); got != "Bàn phím cơ" {
		t.Fatalf("MATCH $1 AND … = %q", got)
	}
	if got := appDatabaseRows(t, client, `SELECT count(*) FROM products WHERE name MATCH 'ban phim'`); got != "2" {
		t.Fatalf("count = %q", got)
	}
	if got := appDatabaseRows(t, client, `SELECT name FROM products WHERE brand MATCH 'logitech'`); got != "Bàn phím Logitech MX" {
		t.Fatalf("brand MATCH = %q", got)
	}
	// The four kinds of query and _snippet_, on the search engine itself.
	for query, want := range map[string]string{
		`SELECT name FROM products WHERE products MATCH 'highlands OR akko'`:                       "Cà phê sữa đá; Bàn phím cơ",
		`SELECT name FROM products WHERE products MATCH '"ban phim logitech"'`:                     "Bàn phím Logitech MX",
		`SELECT name FROM products WHERE name MATCH 'logi*'`:                                       "Bàn phím Logitech MX",
		`SELECT name FROM products WHERE products MATCH 'ca ph*'`:                                  "Cà phê sữa đá",
		`SELECT name FROM products WHERE products MATCH 'ban phim NOT logitech'`:                   "Bàn phím cơ",
		`SELECT name FROM products WHERE products MATCH 'ban phim NOT Cơ'`:                         "Bàn phím Logitech MX",
		`SELECT count(*) FROM products WHERE products MATCH 'ban phim NOT co'`:                     "1",
		`SELECT count(*) FROM products WHERE products MATCH 'akko OR logitech'`:                    "2",
		`SELECT _snippet_ FROM products WHERE products MATCH 'ca phe'`:                             "<b>Cà</b> <b>phê</b> sữa đá",
		`SELECT name, _score_ > 0 FROM products WHERE products MATCH 'akko' ORDER BY _score_ DESC`: "Bàn phím cơ 1",
	} {
		if got := appDatabaseRows(t, client, query); got != want {
			t.Fatalf("%s = %q, want %q", query, got, want)
		}
	}
	for query, message := range map[string]string{
		`SELECT id FROM notes WHERE notes MATCH 'x'`:       "declare its columns in the search of app.database",
		`SELECT id FROM products WHERE merchant MATCH 'x'`: "not in its search index (searchable: brand, name)",
	} {
		if err := appDatabaseQuery(client, query); err == nil || !strings.Contains(err.Error(), message) {
			t.Fatalf("%s: %v, want %q", query, err, message)
		}
	}
	client.Close()
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	// A restart: the table has not moved, so its index is used as it is.
	fixture, runtime = startSearchPort(t, host, declared)
	entry := fulltext.Shared().Lookup(file, "products")
	if entry == nil || !entry.Ready() || entry.Built() != 0 {
		t.Fatalf("after a restart: index %v, want the index used as it is (ready, no row built again)", entry)
	}
	client = fixture.connect(t, "kitdb", appDatabaseTestPassword, "shopping")
	if got := appDatabaseRows(t, client, `SELECT name FROM products WHERE products MATCH 'highlands'`); got != "Cà phê sữa đá" {
		t.Fatalf("after a restart: MATCH = %q", got)
	}

	// Another connection adds a row: the watch builds the index again.
	writeAppDatabaseFile(t, file, `INSERT INTO products (name, brand, merchant) VALUES ('Trà sữa trân châu', 'Phúc Long', 'shopee')`)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := appDatabaseRows(t, client, `SELECT name FROM products WHERE products MATCH 'tra sua'`); got == "Trà sữa trân châu" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("after another connection added a row: %q, want trà sữa", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// What app.database's search accepts: database → table → a list of columns or their weights; a schema other than
// public written schema.table; anything else is refused by name.
func TestAppDatabaseSearchConfig(t *testing.T) {
	base := func(search interface{}) map[string]interface{} {
		return map[string]interface{}{"path": "./.database/", "port": float64(5455), "password": "x", "search": search}
	}
	config, err := parseAppDatabaseConfig(base(map[string]interface{}{
		"shopping": map[string]interface{}{
			"shopping":         map[string]interface{}{"name": float64(5), "brand": float64(2)},
			"billing.invoices": []interface{}{"note", "code"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(config.Search); got != "[{shopping billing invoices [{note 1} {code 1}]} {shopping public shopping [{brand 2} {name 5}]}]" {
		t.Fatalf("search = %s", got)
	}
	for _, refused := range []struct {
		search  interface{}
		message string
	}{
		{"shopping", "search must be an object"},
		{map[string]interface{}{"../up": map[string]interface{}{"t": []interface{}{"a"}}}, "not a database"},
		{map[string]interface{}{"shopping": map[string]interface{}{"t": []interface{}{}}}, "from 1 to 32 columns"},
		{map[string]interface{}{"shopping": map[string]interface{}{"t": []interface{}{"a", "A"}}}, "named twice"},
		{map[string]interface{}{"shopping": map[string]interface{}{"t": map[string]interface{}{"a": float64(0)}}}, "weight"},
		{map[string]interface{}{"shopping": map[string]interface{}{"t": "a"}}, "write the columns"},
	} {
		if _, err := parseAppDatabaseConfig(base(refused.search)); err == nil || !strings.Contains(err.Error(), refused.message) {
			t.Errorf("%v: %v, want %q", refused.search, err, refused.message)
		}
	}
}

// MATCH on a table of an app's database: its .searchable() columns, recorded with their weight in kitwork_columns,
// make its index when a session opens the database, kept in the app's .database/application/kitwork.db.search; rows
// the app adds to two tables of the file are found once the watch built both again; when the columns change the next
// session builds the index from them; a table with none says to mark them .searchable().
//
// CONTROL: with OpenSession not calling discover, "SELECT count(*) FROM posts WHERE posts MATCH 'ca phe': pq: table
// public.posts has no search index"; with the watch looking at the file once per index, "after the app added a row
// to each table: posts "p3", notes "", want p3 and n2" (the first index took the change); with discover keeping an
// index of other columns, "posts MATCH after the change = "p1; p2", want p1".
func TestAppDatabasePortSearchesAnAppsTable(t *testing.T) {
	previous := fulltext.Watch
	fulltext.Watch = 50 * time.Millisecond
	t.Cleanup(func() { fulltext.Watch = previous })
	root := t.TempDir()
	host := filepath.Join(root, ".database")
	writeAppDatabaseFile(t, filepath.Join(host, "shopping", "kitwork.db"), `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT)`)
	site := filepath.Join(root, "apps", "acme", "shop.example")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "router.work"), []byte(`import * as router from "kitwork/router";`), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "apps", "acme", ".database", "application", "kitwork.db")
	writeAppDatabaseFile(t, file,
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE posts (id TEXT PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL, published INTEGER NOT NULL)`,
		`INSERT INTO posts VALUES ('p1', 'Cà phê sữa đá', 'Pha phin buổi sáng', 1), ('p2', 'Trà đào', 'Một ly cà phê không đường', 0)`,
		`CREATE TABLE tags (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE notes (id TEXT PRIMARY KEY, body TEXT NOT NULL)`,
		`INSERT INTO notes VALUES ('n1', 'Ghi chú đầu tiên')`,
		`CREATE TABLE kitwork_columns (table_name TEXT NOT NULL, column_name TEXT NOT NULL, kind TEXT NOT NULL, precision INTEGER NOT NULL, scale INTEGER NOT NULL, search_weight INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (table_name, column_name))`,
		`INSERT INTO kitwork_columns VALUES ('posts', 'id', 'kitid', 0, 0, 0), ('posts', 'title', 'text', 0, 0, 1), ('posts', 'body', 'text', 0, 0, 1), ('posts', 'published', 'bool', 0, 0, 0), ('tags', 'name', 'text', 0, 0, 0), ('notes', 'body', 'text', 0, 0, 1)`)

	fixture, _ := startSearchPort(t, host, nil)
	client := fixture.connect(t, "kitdb", appDatabaseTestPassword, "shop.example/application")
	if got := waitForSearch(t, client, `SELECT count(*) FROM posts WHERE posts MATCH 'ca phe'`); got != "2" {
		t.Fatalf("posts MATCH 'ca phe' counts %q, want 2 (p1 by its title, p2 by its body)", got)
	}
	if got := appDatabaseRows(t, client, `SELECT id FROM posts WHERE body MATCH 'ca phe' AND NOT published`); got != "p2" {
		t.Fatalf("body MATCH = %q, want p2", got)
	}
	if err := appDatabaseQuery(client, `SELECT id FROM tags WHERE tags MATCH 'x'`); err == nil || !strings.Contains(err.Error(), "mark its columns .searchable() in the app's .shared/schema/") {
		t.Fatalf("tags MATCH: %v, want mark its columns .searchable()", err)
	}
	if !fileExists(file + ".search") {
		t.Fatalf("no %s.search beside the app's database", filepath.Base(file))
	}
	if got := waitForSearch(t, client, `SELECT id FROM notes WHERE notes MATCH 'ghi chu'`); got != "n1" {
		t.Fatalf("notes MATCH = %q, want n1", got)
	}

	// The app adds a row to each table of the file: the watch builds both indexes again.
	writeAppDatabaseFile(t, file, `INSERT INTO posts VALUES ('p3', 'Bánh mì', 'Nướng giòn', 1)`, `INSERT INTO notes VALUES ('n2', 'Bánh mì nóng')`)
	deadline := time.Now().Add(10 * time.Second)
	for {
		posts := appDatabaseRows(t, client, `SELECT id FROM posts WHERE posts MATCH 'banh mi'`)
		notes := appDatabaseRows(t, client, `SELECT id FROM notes WHERE notes MATCH 'banh mi'`)
		if posts == "p3" && notes == "n2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the app added a row to each table: posts %q, notes %q, want p3 and n2", posts, notes)
		}
		time.Sleep(20 * time.Millisecond)
	}
	client.Close()

	// The schema keeps only title searchable: the next session builds the index from title alone.
	writeAppDatabaseFile(t, file, `UPDATE kitwork_columns SET search_weight = 0 WHERE table_name = 'posts' AND column_name = 'body'`)
	client = fixture.connect(t, "kitdb", appDatabaseTestPassword, "shop.example/application")
	if got := waitForSearch(t, client, `SELECT id FROM posts WHERE posts MATCH 'ca phe'`); got != "p1" {
		t.Fatalf("posts MATCH after the change = %q, want p1", got)
	}
	if err := appDatabaseQuery(client, `SELECT id FROM posts WHERE body MATCH 'x'`); err == nil || !strings.Contains(err.Error(), "not in its search index (searchable: title)") {
		t.Fatalf("body MATCH after the change: %v, want not in its search index (searchable: title)", err)
	}
}
