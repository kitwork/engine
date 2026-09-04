package work

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

func TestKitDBSafeMigrationSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}

	v1 := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, choice } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique(),
  title: text().notNull(),
  status: choice("active", "disabled").default("active")
});
const db = kitdb("migration.kitdb", { products });
router.get((ctx) => ctx.json(db.products.create({ sku: "KIT-1", title: "kept" })));`
	writeKitDBMigrationRouter(t, directory, v1)
	tenant := runKitDBMigrationTenant(t, root)
	response := requestKitDBMigrationRoute(t, tenant)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"title":"kept"`) {
		t.Fatalf("v1 response = %d %s", response.Code, response.Body.String())
	}
	tenant.Close()

	v2 := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, choice } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  name: text().from("title").notNull().index(),
  description: text().default("migrated"),
  status: choice("active", "disabled", "archived").default("active")
});
const db = kitdb("migration.kitdb", { products }, { migrate: true });
router.get((ctx) => {
  const item = db.products.where("name", "=", "kept").first();
  return ctx.json({ count: db.products.count(), sku: item.sku, name: item.name, description: item.description, status: item.status });
});`
	writeKitDBMigrationRouter(t, directory, v2)
	tenant = runKitDBMigrationTenant(t, root)
	plan := strings.Join(kitDBMigrationPlanLines(tenant, "migration.kitdb", "products"), "\n")
	for _, expected := range []string{"add field description", "rename field title", "rebuild indexes for name", "change constraints for status"} {
		if !strings.Contains(plan, expected) {
			t.Fatalf("v2 migration plan = %q, want %q", plan, expected)
		}
	}
	response = requestKitDBMigrationRoute(t, tenant)
	if response.Code != http.StatusOK {
		t.Fatalf("v2 response = %d %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{`"count":1`, `"sku":"KIT-1"`, `"name":"kept"`, `"description":"migrated"`, `"status":"active"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("v2 response does not contain %s: %s", expected, response.Body.String())
		}
	}
	tenant.Close()

	assertKitDBMigrationAudit(t, filepath.Join(directory, ".data", "migration.kitdb"), "products")

	// The rename hint is needed only for the transition. The persisted identity
	// is reconciled by the new name on every later open.
	v3 := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, choice } = database;
const products = struct({
  id: id(),
  sku: text().notNull().unique().index(),
  name: text().notNull().index(),
  description: text().default("migrated"),
  status: choice("active", "disabled", "archived").default("active")
});
const db = kitdb("migration.kitdb", { products });
router.get((ctx) => ctx.json(db.products.where("name", "=", "kept").first()));`
	writeKitDBMigrationRouter(t, directory, v3)
	tenant = runKitDBMigrationTenant(t, root)
	defer tenant.Close()
	plan = strings.Join(kitDBMigrationPlanLines(tenant, "migration.kitdb", "products"), "\n")
	if !strings.Contains(plan, "up to date") {
		t.Fatalf("v3 migration plan = %q", plan)
	}
	response = requestKitDBMigrationRoute(t, tenant)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"kept"`) {
		t.Fatalf("v3 response = %d %s", response.Code, response.Body.String())
	}
}

func TestKitDBUnsafeMigrationIsAtomicAndRefused(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `import { router, database } from "kitwork";
const { kitdb, struct, id, text, int } = database;
const products = struct({ id: id(), title: text().notNull(), price: int().default(0) });
const db = kitdb("refuse.kitdb", { products });
router.get((ctx) => {
  const found = db.products.first();
  return ctx.json(found || db.products.create({ title: "kept", price: 42 }));
});`
	writeKitDBMigrationRouter(t, directory, v1)
	tenant := runKitDBMigrationTenant(t, root)
	response := requestKitDBMigrationRoute(t, tenant)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"price":42`) {
		t.Fatalf("v1 response = %d %s", response.Code, response.Body.String())
	}
	tenant.Close()

	v2 := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const products = struct({ id: id(), title: text().notNull() });
const db = kitdb("refuse.kitdb", { products }, { migrate: true });
router.get(() => db.products.count());`
	writeKitDBMigrationRouter(t, directory, v2)
	tenant = runKitDBMigrationTenant(t, root)
	plan := strings.Join(kitDBMigrationPlanLines(tenant, "refuse.kitdb", "products"), "\n")
	if !strings.Contains(plan, "remove field price REFUSED") {
		t.Fatalf("unsafe migration plan = %q", plan)
	}
	response = requestKitDBMigrationRoute(t, tenant)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "migration refused") {
		t.Fatalf("unsafe response = %d %s", response.Code, response.Body.String())
	}
	tenant.Close()

	writeKitDBMigrationRouter(t, directory, v1)
	tenant = runKitDBMigrationTenant(t, root)
	defer tenant.Close()
	response = requestKitDBMigrationRoute(t, tenant)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"price":42`) {
		t.Fatalf("recovered v1 response = %d %s", response.Code, response.Body.String())
	}
}

func TestKitDBMigrationValidatesNewUniqueConstraintBeforeCommit(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const items = struct({ id: id(), code: text().notNull() });
const db = kitdb("unique.kitdb", { items });
router.get((ctx) => {
  db.items.create({ code: "same" });
  db.items.create({ code: "same" });
  return ctx.json({ count: db.items.count() });
});`
	writeKitDBMigrationRouter(t, directory, v1)
	tenant := runKitDBMigrationTenant(t, root)
	response := requestKitDBMigrationRoute(t, tenant)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"count":2`) {
		t.Fatalf("v1 response = %d %s", response.Code, response.Body.String())
	}
	tenant.Close()

	v2 := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const items = struct({ id: id(), code: text().notNull().unique() });
const db = kitdb("unique.kitdb", { items }, { migrate: true });
router.get(() => db.items.count());`
	writeKitDBMigrationRouter(t, directory, v2)
	tenant = runKitDBMigrationTenant(t, root)
	response = requestKitDBMigrationRoute(t, tenant)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "duplicate unique values") {
		t.Fatalf("unique migration response = %d %s", response.Code, response.Body.String())
	}
	tenant.Close()

	v1Read := `import { router, database } from "kitwork";
const { kitdb, struct, id, text } = database;
const items = struct({ id: id(), code: text().notNull() });
const db = kitdb("unique.kitdb", { items });
router.get((ctx) => ctx.json({ count: db.items.count() }));`
	writeKitDBMigrationRouter(t, directory, v1Read)
	tenant = runKitDBMigrationTenant(t, root)
	defer tenant.Close()
	response = requestKitDBMigrationRoute(t, tenant)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"count":2`) {
		t.Fatalf("unchanged catalog response = %d %s", response.Code, response.Body.String())
	}
}

func writeKitDBMigrationRouter(t *testing.T, directory, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runKitDBMigrationTenant(t *testing.T, root string) *Tenant {
	t.Helper()
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tenant.Close)
	return tenant
}

func requestKitDBMigrationRoute(t *testing.T, tenant *Tenant) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	response := httptest.NewRecorder()
	tenant.Serve(response, request)
	return response
}

func kitDBMigrationPlanLines(tenant *Tenant, database, table string) []string {
	for _, plan := range MigrationPlansFor(tenant) {
		if plan.Engine == "kitdb" && plan.DB == database && plan.Table == table {
			return plan.Lines()
		}
	}
	return nil
}

func assertKitDBMigrationAudit(t *testing.T, path, structure string) {
	t.Helper()
	database, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	structID := stableSchemaID("struct", structure)
	prefix, err := kitDBFixedKey(kitDBMigrationNamespace, structID, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	if !cursor.Next() {
		if err := cursor.Err(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("KitDB migration audit record is missing")
	}
	var audit kitDBMigrationAudit
	if err := json.Unmarshal(cursor.Value(), &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Struct != structure || audit.From == "" || audit.To == "" || len(audit.Steps) == 0 {
		t.Fatalf("invalid migration audit: %#v", audit)
	}
}
