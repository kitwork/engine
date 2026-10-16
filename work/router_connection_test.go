package work

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hostdatabase "github.com/kitwork/engine/database"
)

// ownedSQLite publishes a temporary SQLite file as a host-owned database under alias, the way
// app.database's managed root publishes each of its folders, with one table of n rows.
func ownedSQLite(t *testing.T, alias, table string, rows int) *sql.DB {
	t.Helper()
	db := sqliteWithRows(t, filepath.Join(t.TempDir(), alias+".db"), table, rows)
	release, err := hostdatabase.RegisterOwned(alias, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return db
}

// sqliteWithRows creates a SQLite file at path with one table of n rows.
func sqliteWithRows(t *testing.T, path, table string, rows int) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		if _, err := db.Exec("INSERT INTO " + table + " (id) VALUES (NULL)"); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// connectionTenant boots one site, identity "test", domain "localhost", from router files (paths
// relative to the site folder) and hands back the boot error for a site that must not boot.
func connectionTenant(t *testing.T, files map[string]string) (*Tenant, string, error) {
	t.Helper()
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "test", "localhost")
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tenant := NewTenant(tmp, "localhost")
	t.Cleanup(tenant.Close)
	return tenant, dir, tenant.Run()
}

func mustBoot(t *testing.T, files map[string]string) (*Tenant, string) {
	t.Helper()
	tenant, dir, err := connectionTenant(t, files)
	if err != nil {
		t.Fatal(err)
	}
	return tenant, dir
}

func hitJSON(t *testing.T, tenant *Tenant, path string) (int, map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
	rec := httptest.NewRecorder()
	tenant.Serve(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body, rec.Body.String()
}

// Plugging nothing, ctx.database is the application's own database: the host root's database named
// after the site. router.connection("application") says the same out loud, and any declared
// connection is reachable by name.
func TestContextDatabaseIsTheApplicationsByDefault(t *testing.T) {
	ownedSQLite(t, "localhost", "items", 2)
	ownedSQLite(t, "system", "users", 3)
	tenant, _ := mustBoot(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router } from "kitwork";
router.get((ctx) => ctx.json({
  name: ctx.database.name,
  items: ctx.database.table("items").count(),
  users: ctx.database("system").table("users").count(),
  again: ctx.database("application").name,
}));`,
		"said/router.kitwork.js": `import { router } from "kitwork";
router.connection("application");
router.get((ctx) => ctx.json({ name: ctx.database.name }));`,
	})
	code, body, raw := hitJSON(t, tenant, "/")
	if code != 200 || body["name"] != "localhost" || body["items"] != float64(2) || body["users"] != float64(3) || body["again"] != "localhost" {
		t.Fatalf("default: code=%d body=%s", code, raw)
	}
	if code, body, raw := hitJSON(t, tenant, "/said"); code != 200 || body["name"] != "localhost" {
		t.Fatalf(`router.connection("application") is the default said out loud: code=%d body=%s`, code, raw)
	}
}

// router.connection(alias) plugs the folder and the folders under it into a host connection; a
// deeper folder plugs back to the application's own; the plug never hides the other names.
func TestRouterConnectionPlugsAHostAlias(t *testing.T) {
	ownedSQLite(t, "localhost", "items", 2)
	ownedSQLite(t, "system", "users", 3)
	tenant, _ := mustBoot(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router } from "kitwork";
router.get((ctx) => ctx.json({ name: ctx.database.name }));`,
		"admin/router.kitwork.js": `import { router } from "kitwork";
router.connection("system");
router.get((ctx) => ctx.json({ name: ctx.database.name, users: ctx.database.table("users").count(), items: ctx.database("localhost").table("items").count() }));`,
		"admin/deep/router.kitwork.js": `import { router } from "kitwork";
router.get((ctx) => ctx.json({ name: ctx.database.name }));`,
		"admin/deep/own/router.kitwork.js": `import { router } from "kitwork";
router.connection("application");
router.get((ctx) => ctx.json({ name: ctx.database.name }));`,
	})
	if code, body, raw := hitJSON(t, tenant, "/"); code != 200 || body["name"] != "localhost" {
		t.Fatalf("root: code=%d body=%s", code, raw)
	}
	if code, body, raw := hitJSON(t, tenant, "/admin"); code != 200 || body["name"] != "system" || body["users"] != float64(3) || body["items"] != float64(2) {
		t.Fatalf("admin plugs system and still reaches localhost by name: code=%d body=%s", code, raw)
	}
	if code, body, raw := hitJSON(t, tenant, "/admin/deep"); code != 200 || body["name"] != "system" {
		t.Fatalf("a folder under admin inherits the plug: code=%d body=%s", code, raw)
	}
	if code, body, raw := hitJSON(t, tenant, "/admin/deep/own"); code != 200 || body["name"] != "localhost" {
		t.Fatalf("a deeper folder plugs back to the application's own: code=%d body=%s", code, raw)
	}
}

// router.connect({ … }) is the tenant's own connection, opened at the first request into the app's
// pool; router.connect("name", { … }) is reachable as ctx.database("name"); a sqlite path stays
// inside the tenant's folder.
func TestRouterConnectIsTheTenantsOwnConnection(t *testing.T) {
	ownedSQLite(t, "localhost", "items", 1)
	tenant, dir := mustBoot(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router } from "kitwork";
router.connect({ sqlite: "./.data/own.db" });
router.connect("crm", { sqlite: "./.data/crm.db" });
router.get((ctx) => ctx.json({
  name: ctx.database.name,
  notes: ctx.database.table("notes").count(),
  leads: ctx.database("crm").table("leads").count(),
  crmName: ctx.database("crm").name,
  items: ctx.database("application").table("items").count(),
}));`,
		"deep/router.kitwork.js": `import { router } from "kitwork";
router.get((ctx) => ctx.json({ name: ctx.database.name, leads: ctx.database("crm").table("leads").count() }));`,
	})
	sqliteWithRows(t, filepath.Join(dir, ".data", "own.db"), "notes", 4)
	sqliteWithRows(t, filepath.Join(dir, ".data", "crm.db"), "leads", 5)

	code, body, raw := hitJSON(t, tenant, "/")
	if code != 200 || body["name"] != "connect" || body["notes"] != float64(4) || body["leads"] != float64(5) || body["crmName"] != "crm" || body["items"] != float64(1) {
		t.Fatalf("tenant connections: code=%d body=%s", code, raw)
	}
	if code, body, raw := hitJSON(t, tenant, "/deep"); code != 200 || body["name"] != "connect" || body["leads"] != float64(5) {
		t.Fatalf("a folder below inherits the plug and the named connection: code=%d body=%s", code, raw)
	}
	if manager := tenant.databaseManager(); manager == nil || manager.Count() != 2 {
		t.Fatalf("two connections, two pools, however many requests: %v", manager)
	}
}

// The declarations that are wrong fail the site at boot, each saying what to write instead.
func TestRouterConnectionDeclarationErrors(t *testing.T) {
	ownedSQLite(t, "system", "users", 1)
	for name, scenario := range map[string]struct{ router, want string }{
		"connection with a config":  {`router.connection({ sqlite: "./.data/x.db" });`, `router.connect(`},
		"connect with a name only":  {`router.connect("system");`, `router.connection("system")`},
		"unknown host alias":        {`router.connection("sytem");`, `"sytem"`},
		"unknown lists the names":   {`router.connection("sytem");`, `application, system`},
		"two plugs":                 {`router.connection("system");` + "\n" + `router.connect({ sqlite: "./.data/x.db" });`, `đã cắm`},
		"named connect as an alias": {`router.connect("system", { sqlite: "./.data/x.db" });`, `đã là một kết nối host`},
		"named connect application": {`router.connect("application", { sqlite: "./.data/x.db" });`, `tên dành cho database của chính app`},
		"sqlite outside the folder": {`router.connect({ sqlite: "../../elsewhere.db" });`, `trong thư mục của app`},
		"empty config":              {`router.connect({});`, `cấu hình rỗng`},
		"url of another provider":   {`router.connect({ postgresql: "mysql://u:p@h/db" });`, `scheme của mysql`},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := connectionTenant(t, map[string]string{
				"filesystem.kitwork": "",
				"router.kitwork.js":  `import { router } from "kitwork";` + "\n" + scenario.router + "\n" + `router.get((ctx) => ctx.json({ ok: true }));`,
			})
			if err == nil || !strings.Contains(err.Error(), scenario.want) {
				t.Fatalf("the site must not boot, saying %q: %v", scenario.want, err)
			}
		})
	}
}

// At request time an unknown name is a failure listing the names there are: "application", the
// host's, and the tenant's own.
func TestContextDatabaseUnknownNameListsTheNames(t *testing.T) {
	ownedSQLite(t, "localhost", "items", 1)
	ownedSQLite(t, "system", "users", 1)
	tenant, _ := mustBoot(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router, safe } from "kitwork";
router.connect("crm", { sqlite: "./.data/crm.db" });
router.get((ctx) => {
  const { error } = safe(() => ctx.database("sytem").name);
  return ctx.json({ message: error.message });
});`,
	})
	code, body, raw := hitJSON(t, tenant, "/")
	if message, _ := body["message"].(string); code != 200 || !strings.Contains(message, `"sytem"`) || !strings.Contains(message, "application, localhost, system, crm") {
		t.Fatalf("an unknown name lists what there is: code=%d body=%s", code, raw)
	}
}

// Without a database named after the site in the host root, and nothing plugged, ctx.database says
// where it would be and what to plug instead.
func TestContextDatabaseWithoutApplicationDatabaseFails(t *testing.T) {
	tenant, _ := mustBoot(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router, safe } from "kitwork";
router.get((ctx) => {
  const { error } = safe(() => ctx.database.name);
  return ctx.json({ message: error.message });
});`,
	})
	code, body, raw := hitJSON(t, tenant, "/")
	if message, _ := body["message"].(string); code != 200 || !strings.Contains(message, ".database/localhost/") || !strings.Contains(message, "router.connect") {
		t.Fatalf("a missing application database says where it would be and what to plug instead: code=%d body=%s", code, raw)
	}
}

// routeConnection is the nearest plug up the chain and the named connections down to the route.
func TestRouteConnectionIsTheNearestPlug(t *testing.T) {
	crm := &hostdatabase.Config{Type: "sqlite", Name: "crm.db"}
	crm2 := &hostdatabase.Config{Type: "sqlite", Name: "crm2.db"}
	root := &RouteNode{folder: &FolderRouter{connection: &folderConnection{alias: "system"}, connections: map[string]*hostdatabase.Config{"crm": crm}}}
	child := &RouteNode{parent: root, folder: &FolderRouter{connections: map[string]*hostdatabase.Config{"crm": crm2}}}
	grandchild := &RouteNode{parent: child}
	plug, named := routeConnection(grandchild)
	if plug == nil || plug.alias != "system" || named["crm"] != crm2 {
		t.Fatalf("grandchild: plug=%v crm=%v", plug, named["crm"])
	}
	if plug, named := routeConnection(root); plug.alias != "system" || named["crm"] != crm {
		t.Fatalf("root: plug=%v crm=%v", plug, named["crm"])
	}
	if plug, named := routeConnection(&RouteNode{}); plug != nil || len(named) != 0 {
		t.Fatalf("nothing plugged: plug=%v named=%v", plug, named)
	}
}

// In a Kitwork UI site the same handle reaches router.view through its ctx: the data a .work reads
// from ctx.database is what content.ui prints.
func TestUIViewReadsContextDatabase(t *testing.T) {
	ownedSQLite(t, "localhost", "items", 2)
	ownedSQLite(t, "system", "users", 3)
	files := withFiles(map[string]string{
		"localhost/router.work": `import { router } from "kitwork";
router.view((ctx) => {
  const count = ctx.database.table("items").count();
  const name = ctx.database.name;
  const users = ctx.database("system").table("users").count();
  ctx.title("Kho");
});`,
		"localhost/content.ui": `<content><text>{{ count }} món trong {{ name }}, {{ users }} người dùng</text></content>`,
	})
	tenant := buildUISite(t, files)
	page := serveUI(t, tenant, "http://localhost/")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "2 món trong localhost, 3 người dùng") {
		t.Fatalf("a .work view reads ctx.database: code=%d body=%s", page.Code, page.Body.String())
	}
}
