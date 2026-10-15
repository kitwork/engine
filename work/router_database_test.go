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
// app.database's managed root publishes each of its databases, with one table of n rows.
func ownedSQLite(t *testing.T, alias, table string, rows int) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), alias+".db"))
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
	release, err := hostdatabase.RegisterOwned(alias, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return db
}

// databaseTenant boots one site from the given router files (paths relative to the site folder).
func databaseTenant(t *testing.T, files map[string]string) *Tenant {
	t.Helper()
	tenant, err := bootDatabaseTenant(t, files)
	if err != nil {
		t.Fatal(err)
	}
	return tenant
}

// bootDatabaseTenant is databaseTenant that hands the boot error back, for a site that must not boot.
func bootDatabaseTenant(t *testing.T, files map[string]string) (*Tenant, error) {
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
	return tenant, tenant.Run()
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

// ctx.database is the database the nearest folder chose with router.database(name): the site root's
// for the site, a branch's for the branch and the folders under it; any other declared database is
// reached by name, ctx.database("ledger").
func TestContextDatabaseFollowsTheFolder(t *testing.T) {
	ownedSQLite(t, "shop", "items", 2)
	ownedSQLite(t, "ledger", "entries", 3)
	tenant := databaseTenant(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router } from "kitwork";
router.database("shop");
router.get((ctx) => ctx.json({
  name: ctx.database.name,
  items: ctx.database.table("items").count(),
  entries: ctx.database("ledger").table("entries").count(),
  ledgerName: ctx.database("ledger").name,
  same: ctx.database("shop").name,
}));`,
		"admin/router.kitwork.js": `import { router } from "kitwork";
router.database("ledger");
router.get((ctx) => ctx.json({ name: ctx.database.name, entries: ctx.database.table("entries").count() }));`,
		"deep/down/router.kitwork.js": `import { router } from "kitwork";
router.get((ctx) => ctx.json({ name: ctx.database.name, items: ctx.database.table("items").count() }));`,
	})

	code, body, raw := hitJSON(t, tenant, "/")
	if code != 200 || body["name"] != "shop" || body["items"] != float64(2) || body["entries"] != float64(3) ||
		body["ledgerName"] != "ledger" || body["same"] != "shop" {
		t.Fatalf("root: code=%d body=%s", code, raw)
	}
	code, body, raw = hitJSON(t, tenant, "/admin")
	if code != 200 || body["name"] != "ledger" || body["entries"] != float64(3) {
		t.Fatalf("branch overrides the site's database: code=%d body=%s", code, raw)
	}
	code, body, raw = hitJSON(t, tenant, "/deep/down")
	if code != 200 || body["name"] != "shop" || body["items"] != float64(2) {
		t.Fatalf("a folder without a declaration inherits the nearest one: code=%d body=%s", code, raw)
	}
}

// ctx.database(name) reaches only a database the host declared; another name is a failure naming
// it and the manifest, and a non-name says what the call takes.
func TestContextDatabaseByUnknownNameFails(t *testing.T) {
	ownedSQLite(t, "shop", "items", 1)
	tenant := databaseTenant(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router, safe } from "kitwork";
router.database("shop");
router.get((ctx) => {
  const unknown = safe(() => ctx.database("nope").table("items").count());
  const wrong = safe(() => ctx.database(3));
  return ctx.json({ unknown: unknown.error.message, wrong: wrong.error.message });
});`,
	})
	code, body, raw := hitJSON(t, tenant, "/")
	if code != 200 {
		t.Fatalf("code=%d body=%s", code, raw)
	}
	if message, _ := body["unknown"].(string); !strings.Contains(message, `"nope"`) || !strings.Contains(message, "manifest") {
		t.Fatalf("an unknown name must be refused, naming it and the manifest: %s", raw)
	}
	if message, _ := body["wrong"].(string); !strings.Contains(message, `ctx.database("ledger")`) {
		t.Fatalf("a non-name must say what the call takes: %s", raw)
	}
}

// Without router.database anywhere up the chain there is no default, and ctx.database says so,
// naming the declaration to write.
func TestContextDatabaseWithoutDeclarationFails(t *testing.T) {
	tenant := databaseTenant(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router, safe } from "kitwork";
router.get((ctx) => {
  const { error } = safe(() => ctx.database.name);
  return ctx.json({ code: error.code, message: error.message });
});`,
	})
	if code, _, raw := hitJSON(t, tenant, "/"); code != 200 || !strings.Contains(raw, "router.database") {
		t.Fatalf("ctx.database with no declaration: code=%d body=%s", code, raw)
	}
}

// router.database names only a database the host declared, once per folder; anything else fails
// the site at boot, naming the alias and where to declare it, or the earlier choice.
func TestRouterDatabaseDeclarationErrors(t *testing.T) {
	ownedSQLite(t, "shop", "items", 1)
	ownedSQLite(t, "ledger", "entries", 1)
	for name, scenario := range map[string]struct{ router, want string }{
		"unknown alias":  {`router.database("nope");`, `"nope"`},
		"declared twice": {`router.database("shop");` + "\n" + `router.database("ledger");`, `đã chọn "shop"`},
		"not a name":     {`router.database({ use: ["ledger"] });`, `router.database("shop")`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := bootDatabaseTenant(t, map[string]string{
				"filesystem.kitwork": "",
				"router.kitwork.js":  `import { router } from "kitwork";` + "\n" + scenario.router + "\n" + `router.get((ctx) => ctx.json({ ok: true }));`,
			})
			if err == nil || !strings.Contains(err.Error(), scenario.want) {
				t.Fatalf("the site must not boot, saying %s: %v", scenario.want, err)
			}
		})
	}
}

// routeDatabase is the nearest folder's choice up the chain, or none.
func TestRouteDatabaseIsTheNearestChoice(t *testing.T) {
	root := &RouteNode{folder: &FolderRouter{database: "shop"}}
	child := &RouteNode{parent: root, folder: &FolderRouter{database: "ledger"}}
	grandchild := &RouteNode{parent: child}
	cousin := &RouteNode{parent: root}
	for node, want := range map[*RouteNode]string{root: "shop", child: "ledger", grandchild: "ledger", cousin: "shop", {}: ""} {
		if got := routeDatabase(node); got != want {
			t.Fatalf("routeDatabase = %q, want %q", got, want)
		}
	}
}

// In a Kitwork UI site the same handle reaches router.view through its ctx: the data a .work reads
// from ctx.database is what content.ui prints.
func TestUIViewReadsContextDatabase(t *testing.T) {
	ownedSQLite(t, "shop", "items", 2)
	ownedSQLite(t, "ledger", "entries", 3)
	files := withFiles(map[string]string{
		"localhost/router.work": `import { router } from "kitwork";
router.database("shop");
router.view((ctx) => {
  const count = ctx.database.table("items").count();
  const name = ctx.database.name;
  const entries = ctx.database("ledger").table("entries").count();
  ctx.title("Kho");
});`,
		"localhost/content.ui": `<content><text>{{ count }} món trong {{ name }}, {{ entries }} dòng sổ</text></content>`,
	})
	tenant := buildUISite(t, files)
	page := serveUI(t, tenant, "http://localhost/")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "2 món trong shop, 3 dòng sổ") {
		t.Fatalf("a .work view reads ctx.database: code=%d body=%s", page.Code, page.Body.String())
	}
}

// A name the manifest declares for an external database and a managed root also publishes is refused
// at boot, naming both, so nothing is ever chosen by luck at the first request.
func TestRouterDatabaseRefusesANameDeclaredTwice(t *testing.T) {
	ownedSQLite(t, "system", "users", 1)
	hostdatabase.Configs["system"] = hostdatabase.Config{Alias: "system", Type: "postgres"}
	t.Cleanup(func() { delete(hostdatabase.Configs, "system") })
	_, err := bootDatabaseTenant(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": `import { router } from "kitwork";
router.database("system");
router.get((ctx) => ctx.json({ ok: true }));`,
	})
	if err == nil || !strings.Contains(err.Error(), `"system"`) || !strings.Contains(err.Error(), "hai chỗ") {
		t.Fatalf("a name declared by both the manifest and a managed root must fail the boot naming both: %v", err)
	}
}
