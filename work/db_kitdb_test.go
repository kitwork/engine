package work

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBTenantEntryAndRuntimeLifecycle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int, enum, now, updated } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  title: text().notNull(),
  price: int().default(0),
  status: enum("active", "disabled").default("active").index(),
  created_at: now(),
  updated_at: updated()
});
const db = kitdb("catalog.kitdb", { products });
router.get((ctx) => {
  const alpha = db.products.create({ sku: "A", title: "Alpha", price: 10 });
  db.products.create({ sku: "B", title: "Beta", price: 20 });
  db.products.create({ sku: "C", title: "Gamma", price: 30, status: "disabled" });
  db.products.where("sku", "=", "A").update({ price: 25 });
  db.products.where("sku", "=", "B").delete();
  const active = db.products.where("status", "=", "active").sort("price", "desc").list();
  const found = db.products.find(alpha.id);
  return ctx.json({
    count: db.products.count(),
    active: active.length,
    first: active[0].title,
    price: found.price,
    status: found.status,
    costly: db.products.where((product) => product.price >= 20).count(),
    deleted: db.products.where("sku", "=", "B").exists()
  });
});`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	request := func(path string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil)
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, req)
		return recorder.Code, recorder.Body.String()
	}

	code, body := request("/")
	if code != http.StatusOK {
		t.Fatalf("route status = %d, body = %s", code, body)
	}
	for _, expected := range []string{
		`"count":2`, `"active":1`, `"first":"Alpha"`, `"price":25`,
		`"status":"active"`, `"costly":2`, `"deleted":false`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("response does not contain %s: %s", expected, body)
		}
	}

	databasePath := filepath.Join(dir, ".data", "catalog.kitdb")
	if _, err := os.Stat(databasePath); err != nil {
		t.Fatalf("KitDB file is not at .data/catalog.kitdb: %v", err)
	}
	if code, _ := request("/.data/catalog.kitdb"); code == http.StatusOK {
		t.Fatalf("tenant KitDB file is downloadable over HTTP")
	}

	// Closing the owning app runtime must release the writer lock. A fresh VM
	// then reads the same rows through the same struct/ORM contract.
	tenant.Close()
	reopenRouter := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int, enum, now, updated } = database;
const products = struct({
  id: id(), sku: text().notNull().unique().index(), title: text().notNull(),
  price: int().default(0), status: enum("active", "disabled").default("active").index(),
  created_at: now(), updated_at: updated()
});
const db = kitdb("catalog.kitdb", { products });
router.get((ctx) => ctx.json({ title: db.products.where("sku", "=", "A").first().title, count: db.products.count() }));`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(reopenRouter), 0o644); err != nil {
		t.Fatal(err)
	}
	reopenedTenant := NewTenant(root, "localhost")
	if err := reopenedTenant.Run(); err != nil {
		t.Fatalf("restart tenant: %v", err)
	}
	defer reopenedTenant.Close()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	recorder := httptest.NewRecorder()
	reopenedTenant.Serve(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"title":"Alpha"`) || !strings.Contains(recorder.Body.String(), `"count":2`) {
		t.Fatalf("ORM reopen status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestKitDBStructConstraintsFailClosed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, ref } = database;
const users = struct({ id: id(), email: text().notNull().unique() });
const links = struct({
  id: id(),
  user_id: ref(users.id).notNull(),
  user_email: ref(users.email),
  code: text().notNull().unique()
});
const db = kitdb("constraints.kitdb", { users, links });
router.get((ctx) => {
  const test = ctx.query("case");
  if (test === "setup") {
    const user = db.users.create({ email: "a@kitwork.dev" });
    return ctx.json({ linked: db.links.create({ user_id: user.id, user_email: user.email, code: "kitwork" }).code });
  }
  if (test === "duplicate") return db.users.create({ email: "a@kitwork.dev" });
  if (test === "missing") return db.links.create({ user_id: "missing", code: "missing" });
  if (test === "required") return db.users.create({});
  if (test === "update-referenced") {
    return db.users.where("email", "=", "a@kitwork.dev").update({ email: "renamed@kitwork.dev" });
  }
  if (test === "referenced") {
    const user = db.users.where("email", "=", "a@kitwork.dev").first();
    return db.users.where("id", "=", user.id).delete();
  }
  return ctx.json({ users: db.users.count(), links: db.links.count() });
});`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	request := func(testCase string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/?case="+testCase, nil)
		recorder := httptest.NewRecorder()
		tenant.Serve(recorder, req)
		return recorder.Code, recorder.Body.String()
	}
	if code, body := request("setup"); code != http.StatusOK || !strings.Contains(body, `"linked":"kitwork"`) {
		t.Fatalf("setup status = %d, body = %s", code, body)
	}
	for testCase, expected := range map[string]string{
		"duplicate":         "must be unique",
		"missing":           "references missing",
		"required":          "cannot be null",
		"referenced":        "still references",
		"update-referenced": "still references",
	} {
		if code, body := request(testCase); code != http.StatusInternalServerError || !strings.Contains(body, expected) {
			t.Errorf("%s status = %d, body = %s, want 500 containing %q", testCase, code, body, expected)
		}
	}
	if code, body := request("status"); code != http.StatusOK || !strings.Contains(body, `"users":1`) || !strings.Contains(body, `"links":1`) {
		t.Fatalf("post-failure status = %d, body = %s", code, body)
	}
}

func TestKitDBStructIsRequiredAndUnsupportedPoliciesAreRefused(t *testing.T) {
	tests := []struct {
		name   string
		router string
		want   string
	}{
		{
			name: "plain schema object",
			router: `import { router, database } from "kitwork";
const { kitdb, id, text } = database;
const items = { id: id(), name: text() };
const db = kitdb("plain.kitdb", { items });
router.get(() => db.items.count());`,
			want: "must be declared with struct",
		},
		{
			name: "unsupported cascade",
			router: `import { router, database } from "kitwork";
const { kitdb, struct, id, ref } = database;
const parents = struct({ id: id() });
const children = struct({ id: id(), parent_id: ref(parents.id, { onDelete: "cascade" }) });
const db = kitdb("cascade.kitdb", { parents, children });
router.get(() => db.parents.count());`,
			want: "not implemented yet; constraint refused",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "test", "localhost")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(test.router), 0o644); err != nil {
				t.Fatal(err)
			}
			tenant := NewTenant(root, "localhost")
			if err := tenant.Run(); err != nil {
				if strings.Contains(err.Error(), test.want) {
					return
				}
				t.Fatalf("tenant Run error = %v, want rejection containing %q", err, test.want)
			}
			defer tenant.Close()
			req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			recorder := httptest.NewRecorder()
			tenant.Serve(recorder, req)
			if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), test.want) {
				t.Fatalf("status = %d, body = %q, want 500 containing %q", recorder.Code, recorder.Body.String(), test.want)
			}
		})
	}
}

func TestStructORMIsBackendNeutralAcrossSQLiteAndKitDB(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { sqlite, kitdb, struct, id, text, int } = database;
const items = struct({ id: id(), code: text().notNull().unique().index(), amount: int().default(0) });
const sql = sqlite("same-api.db", { items });
const native = kitdb("same-api.kitdb", { items });
router.get((ctx) => {
  sql.items.create({ code: "sqlite", amount: 10 });
  native.items.create({ code: "kitdb", amount: 20 });
  sql.items.where("code", "=", "sqlite").update({ amount: 11 });
  native.items.where("code", "=", "kitdb").update({ amount: 21 });
  return ctx.json({
    sqlite: sql.items.where("code", "=", "sqlite").first().amount,
    kitdb: native.items.where("code", "=", "kitdb").first().amount,
    sqliteCount: sql.items.count(),
    kitdbCount: native.items.count()
  });
});`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{`"sqlite":11`, `"kitdb":21`, `"sqliteCount":1`, `"kitdbCount":1`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Errorf("response does not contain %s: %s", expected, recorder.Body.String())
		}
	}
}

func TestKitDBStructChangeRequiresExplicitMigration(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const products = struct({ id: id(), title: text().notNull() });
const db = kitdb("migration.kitdb", { products });
router.get((ctx) => ctx.json(db.products.create({ title: "kept" })));`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("v1 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	tenant.Close()

	v2 := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const products = struct({ id: id(), title: text().notNull(), description: text() });
const db = kitdb("migration.kitdb", { products });
router.get(() => db.products.count());`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := NewTenant(root, "localhost")
	if err := changed.Run(); err != nil {
		t.Fatal(err)
	}
	defer changed.Close()
	plans := MigrationPlansFor(changed)
	var planText string
	for _, plan := range plans {
		if plan.Engine == "kitdb" && plan.Table == "products" {
			planText = strings.Join(plan.Lines(), "\n")
		}
	}
	if !strings.Contains(planText, "requires explicit migration") {
		t.Fatalf("KitDB migration preflight = %q", planText)
	}
	req = httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	recorder = httptest.NewRecorder()
	changed.Serve(recorder, req)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "explicit migration is required") {
		t.Fatalf("v2 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestKitDBRelSafety(t *testing.T) {
	cases := map[string]string{
		"app.kitdb":             "app.kitdb",
		"catalog.kitdb":         "catalog.kitdb",
		"archive/2026.kitdb":    "archive/2026.kitdb",
		"../secrets.kitdb":      "secrets.kitdb",
		"../../etc/passwd":      "passwd",
		"/absolute.kitdb":       "absolute.kitdb",
		"C:/windows/evil.kitdb": "evil.kitdb",
		"a/../../escape.kitdb":  "escape.kitdb",
		"..":                    kitDBDefaultFile,
		"../..":                 kitDBDefaultFile,
		"":                      kitDBDefaultFile,
	}
	for input, want := range cases {
		if got := kitDBRel(input); got != want {
			t.Errorf("kitDBRel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestKitDBCapabilitySerializesConcurrentWrites(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const items = struct({ id: id(), code: text().notNull().unique().index() });
const db = kitdb("concurrent.kitdb", { items });
router.get((ctx) => {
  const code = ctx.query("code");
  if (code) return ctx.json({ id: db.items.create({ code }).id });
  return ctx.json({ count: db.items.count() });
});`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	const writers = 64
	start := make(chan struct{})
	failures := make(chan string, writers)
	var wait sync.WaitGroup
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost/?code=item-%03d", index), nil)
			recorder := httptest.NewRecorder()
			tenant.Serve(recorder, req)
			if recorder.Code != http.StatusOK {
				failures <- recorder.Body.String()
			}
		}(index)
	}
	close(start)
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Errorf("concurrent set failed: %s", failure)
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"count":64`) {
		t.Fatalf("rows after concurrent writes: status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestKitDBCapabilityRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test", "localhost")
	dataDir := filepath.Join(dir, ".data")
	outside := t.TempDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dataDir, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const items = struct({ id: id(), value: text() });
const db = kitdb("link/escape.kitdb", { items });
router.get(() => db.items.create({ value: "secret" }));`
	if err := os.WriteFile(filepath.Join(dir, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	req := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, req)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "escapes") {
		t.Fatalf("symlink escape status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(outside, "escape.kitdb")); !os.IsNotExist(err) {
		t.Fatalf("symlink escape created an outside database: %v", err)
	}
}

func TestKitDBStructRejectsExecutableOrUnboundedValues(t *testing.T) {
	function := value.NewFunc(func(args ...value.Value) value.Value { return value.NewNil() })
	if err := validateKitDBJSON(function); err == nil || !strings.Contains(err.Error(), "func") {
		t.Fatalf("function validation = %v, want rejection", err)
	}

	nested := value.NewNil()
	for depth := 0; depth <= kitDBJSONDepthLimit; depth++ {
		nested = value.New(map[string]value.Value{"next": nested})
	}
	if err := validateKitDBJSON(nested); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("deep JSON validation = %v, want depth rejection", err)
	}

}
