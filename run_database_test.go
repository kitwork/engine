package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// freePort is a port of the loopback nothing listens on as it is asked.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// `kitwork database` (RunDatabase) on the manifest of a host that declares a website, a connection and the database
// port: only the port opens. The host's database (shopping, app.database's path read from the manifest's folder, not
// the folder the command runs in) and an app's (shop.example/application, found by the folders of app.root, no app's
// code run) read and write over it; the website's port answers nothing, and the connection to a PostgreSQL that is not
// there is never tried. Once the context ends it returns nil, the port is closed and the files are let go.
//
// CONTROL (08/11/2026): with app.database's path left as the manifest writes it, "SELECT … FROM products: pq: database
// \"shopping\" does not exist: no shopping/kitwork.db"; with the apps' root not given to the port, "INSERT through the
// port: pq: database of \"shop.example\" does not exist: no app has that domain"; with the ports not closed when the
// context ends (their context still cancelled), on Windows "the files of …\.database once the command returned:
// unlinkat …\.database\.catalog\kitwork.db: The process cannot access the file because it is being used by another
// process".
func TestRunDatabaseOpensOnlyThePort(t *testing.T) {
	root := t.TempDir()
	writeAppDatabaseFile(t, filepath.Join(root, ".database", "shopping", "kitwork.db"),
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT NOT NULL, price INTEGER NOT NULL)`,
		`INSERT INTO products (name, price) VALUES ('cà phê', 35000), ('trà', 450)`)
	apps := filepath.Join(root, "apps")
	site := filepath.Join(apps, "acme", "shop.example")
	if err := os.MkdirAll(site, 0o755); err != nil {
		t.Fatal(err)
	}
	// a site whose code would fail to boot: the database alone never runs it
	if err := os.WriteFile(filepath.Join(site, "router.work"), []byte(`import * as router from "kitwork/router"; router.nothing(`), 0o644); err != nil {
		t.Fatal(err)
	}
	orders := filepath.Join(apps, "acme", ".database", "application", "kitwork.db")
	writeAppDatabaseFile(t, orders,
		`CREATE TABLE orders (id TEXT PRIMARY KEY, email TEXT NOT NULL)`,
		`INSERT INTO orders VALUES ('o1', 'an@example.com')`)

	databasePort, websitePort, nobody := freePort(t), freePort(t), freePort(t)
	manifest := filepath.Join(root, "application.work")
	source := fmt.Sprintf(`import * as app from "kitwork/application";
app.root(%q);
app.postgresql({ alias: "system", host: "127.0.0.1", port: %d, user: "u", password: "p", name: "n", sslmode: "disable" });
app.database({ path: "./.database/", port: %d, user: "kitdb", password: %q });
app.website({ hostname: "kitwork.test", allowLocal: true });
app.run(%d);
`, filepath.ToSlash(apps), nobody, databasePort, appDatabaseTestPassword, websitePort)
	if err := os.WriteFile(manifest, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listening := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- runDatabase(ctx, listening, manifest) }()
	var address string
	select {
	case address = <-listening:
	case err := <-done:
		t.Fatalf("kitwork database ended before it listened: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("kitwork database did not listen in 30 s")
	}
	if want := fmt.Sprintf("127.0.0.1:%d", databasePort); address != want {
		t.Errorf("the port listens on %s, want %s", address, want)
	}

	fixture := &appDatabaseFixture{address: address}
	shopping := fixture.connect(t, "kitdb", appDatabaseTestPassword, "shopping")
	if got := appDatabaseRows(t, shopping, `SELECT name, price FROM products ORDER BY id`); got != "cà phê 35000; trà 450" {
		t.Errorf("shopping.products = %q", got)
	}
	application := fixture.connect(t, "kitdb", appDatabaseTestPassword, "shop.example/application")
	if _, err := application.Exec(`INSERT INTO orders VALUES ('o2', 'binh@example.com')`); err != nil {
		t.Fatalf("INSERT through the port: %v", err)
	}
	if got := appDatabaseRows(t, application, `SELECT id, email FROM orders ORDER BY id`); got != "o1 an@example.com; o2 binh@example.com" {
		t.Errorf("shop.example/application orders = %q", got)
	}
	if connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", websitePort), time.Second); err == nil {
		connection.Close()
		t.Errorf("the website's port %d answers: kitwork database opened the website", websitePort)
	}
	shopping.Close()
	application.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("kitwork database returned %v, want nil", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("kitwork database did not return 30 s after its context ended")
	}
	if connection, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		connection.Close()
		t.Fatal("the port still answers after the command returned")
	}
	database, err := kitdbsqlite.Open(context.Background(), orders, kitdbsqlite.Options{HistoryOfEveryTable: true})
	if err != nil {
		t.Fatalf("the app's database once the command returned: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	// every file the port opened is closed: Windows refuses to remove a file a process holds open (the catalog of a
	// cluster, a database read)
	for _, folder := range []string{filepath.Join(root, ".database"), filepath.Join(apps, "acme", ".database")} {
		if err := os.RemoveAll(folder); err != nil {
			t.Errorf("the files of %s once the command returned: %v", folder, err)
		}
	}
}

// A manifest without app.database has nothing for `kitwork database` to open: an error that says so, before anything
// listens.
func TestRunDatabaseWithoutADatabase(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "application.work")
	source := fmt.Sprintf(`import * as app from "kitwork/application";
app.root(%q);
app.website({ hostname: "kitwork.test", allowLocal: true });
app.run(%d);
`, filepath.ToSlash(filepath.Join(root, "apps")), freePort(t))
	if err := os.WriteFile(manifest, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runDatabase(context.Background(), nil, manifest)
	if err == nil || !strings.Contains(err.Error(), "không khai app.database") {
		t.Fatalf("kitwork database without app.database = %v, want the error that says so", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("it waited for a context instead of refusing")
	}
}
