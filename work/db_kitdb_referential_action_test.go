package work

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBDSLReferentialActionsUseORMPipeline(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, text, ref } = database;
const parents = struct({ id: text().key(), code: text().notNull().unique() });
const children = struct({
  id: text().key(),
  parent_code: ref(parents.code, { onUpdate: "cascade", onDelete: "cascade" }).notNull()
});
const db = kitdb("actions-dsl.kitdb", { parents, children });
router.get((ctx) => {
  const action = ctx.query("action");
  if (action === "seed") return ctx.json(db.transaction((tx) => {
    tx.parents.create({ id: "p1", code: "old" });
    tx.children.create({ id: "c1", parent_code: "old" });
    return { children: tx.children.count() };
  }));
  if (action === "update") return ctx.json(db.parents.where("id", "=", "p1").update({ code: "new" }));
  if (action === "delete") return ctx.json({ removed: db.parents.where("id", "=", "p1").delete() });
  return ctx.json({ child: db.children.first(), count: db.children.count() });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		tenant.Serve(response, httptest.NewRequest(http.MethodGet, "http://localhost"+path, nil))
		return response
	}
	if response := request("/?action=seed"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"children":1`) {
		t.Fatalf("DSL action seed = %d %s", response.Code, response.Body.String())
	}
	if response := request("/?action=update"); response.Code != http.StatusOK {
		t.Fatalf("DSL cascade update = %d %s", response.Code, response.Body.String())
	}
	if response := request("/"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"parent_code":"new"`) {
		t.Fatalf("DSL cascaded child = %d %s", response.Code, response.Body.String())
	}
	if response := request("/?action=delete"); response.Code != http.StatusOK {
		t.Fatalf("DSL cascade delete = %d %s", response.Code, response.Body.String())
	}
	if response := request("/"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"count":0`) {
		t.Fatalf("DSL cascade final = %d %s", response.Code, response.Body.String())
	}
}

func TestKitDBReferentialActionsAreAtomicAndSurviveRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("actions.kitdb", {}, { token: "actions-secret", access: "readwrite" });
router.get(() => 1);`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	start := func() (*Tenant, *dbProxy) {
		t.Helper()
		tenant := NewTenant(root, "localhost")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		_, config, found := resolveServe(tenant, "actions.kitdb")
		if !found || config.database == nil {
			tenant.Close()
			t.Fatal("referential-action SQL registration is unavailable")
		}
		return tenant, config.database
	}
	execute := func(database *dbProxy, source string) (kitDBRemoteResult, error) {
		t.Helper()
		return executeKitDBRemoteSQL(
			context.Background(), nil, database, source,
			kitSQLBindings{named: map[string]value.Value{}}, false,
		)
	}

	tenant, database := start()
	for _, statement := range []string{
		`CREATE TABLE parents (id TEXT PRIMARY KEY, code TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE cascade_children (
  id TEXT PRIMARY KEY,
  parent_code TEXT REFERENCES parents (code) ON UPDATE CASCADE ON DELETE CASCADE
)`,
		`CREATE TABLE null_children (
  id TEXT PRIMARY KEY,
  parent_code TEXT REFERENCES parents (code) ON UPDATE SET NULL ON DELETE SET NULL
)`,
		`CREATE TABLE default_children (
  id TEXT PRIMARY KEY,
  parent_code TEXT DEFAULT 'fallback'
    REFERENCES parents (code) ON UPDATE SET DEFAULT ON DELETE SET DEFAULT
)`,
		`CREATE TABLE blockers (
  id TEXT PRIMARY KEY,
  child_id TEXT NOT NULL REFERENCES cascade_children (id) ON DELETE RESTRICT
)`,
		`CREATE TABLE nodes (
  id TEXT PRIMARY KEY,
  parent_id TEXT REFERENCES nodes (id) ON DELETE CASCADE
)`,
	} {
		if _, err := execute(database, statement); err != nil {
			t.Fatalf("execute %q: %v", statement, err)
		}
	}
	if _, err := execute(database, `INSERT INTO parents (id, code) VALUES
  ('fallback', 'fallback'), ('alpha', 'alpha'), ('beta', 'beta'),
  ('gamma', 'gamma'), ('delta', 'delta'), ('blocked', 'blocked')`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO cascade_children (id, parent_code) VALUES ('cascade', 'alpha'), ('blocked-child', 'blocked')`,
		`INSERT INTO null_children (id, parent_code) VALUES ('null-update', 'beta'), ('null-delete', 'gamma')`,
		`INSERT INTO default_children (id, parent_code) VALUES ('default-update', 'beta'), ('default-delete', 'delta')`,
		`INSERT INTO blockers (id, child_id) VALUES ('guard', 'blocked-child')`,
		`INSERT INTO nodes (id, parent_id) VALUES ('self', NULL)`,
		`UPDATE nodes SET parent_id = 'self' WHERE id = 'self'`,
	} {
		if _, err := execute(database, statement); err != nil {
			t.Fatalf("seed %q: %v", statement, err)
		}
	}

	if _, err := execute(database, `UPDATE parents SET code = 'alpha-2' WHERE id = 'alpha'`); err != nil {
		t.Fatalf("single cascade update: %v", err)
	}
	cascadeUpdated, err := execute(database, `SELECT parent_code FROM cascade_children WHERE id = 'cascade'`)
	if err != nil || len(cascadeUpdated.rows) != 1 || cascadeUpdated.rows[0][0].String() != "alpha-2" {
		t.Fatalf("single cascade update row = %#v, %v", cascadeUpdated, err)
	}
	if _, err := execute(database, `UPDATE parents SET code = 'beta-2' WHERE id = 'beta'`); err != nil {
		t.Fatalf("set actions on update: %v", err)
	}
	nullUpdated, err := execute(database, `SELECT parent_code FROM null_children WHERE id = 'null-update'`)
	if err != nil || len(nullUpdated.rows) != 1 || !nullUpdated.rows[0][0].IsNil() {
		t.Fatalf("SET NULL update row = %#v, %v", nullUpdated, err)
	}
	defaultUpdated, err := execute(database, `SELECT parent_code FROM default_children WHERE id = 'default-update'`)
	if err != nil || len(defaultUpdated.rows) != 1 || defaultUpdated.rows[0][0].String() != "fallback" {
		t.Fatalf("SET DEFAULT update row = %#v, %v", defaultUpdated, err)
	}

	for _, statement := range []string{
		`DELETE FROM parents WHERE id = 'alpha'`,
		`DELETE FROM parents WHERE id = 'gamma'`,
		`DELETE FROM parents WHERE id = 'delta'`,
		`DELETE FROM nodes WHERE id = 'self'`,
	} {
		if _, err := execute(database, statement); err != nil {
			t.Fatalf("delete %q: %v", statement, err)
		}
	}
	cascadeCount, err := execute(database, `SELECT COUNT(*) FROM cascade_children WHERE id = 'cascade'`)
	if err != nil || len(cascadeCount.rows) != 1 || cascadeCount.rows[0][0].N != 0 {
		t.Fatalf("single cascade delete count = %#v, %v", cascadeCount, err)
	}
	nullDeleted, err := execute(database, `SELECT parent_code FROM null_children WHERE id = 'null-delete'`)
	if err != nil || len(nullDeleted.rows) != 1 || !nullDeleted.rows[0][0].IsNil() {
		t.Fatalf("SET NULL delete row = %#v, %v", nullDeleted, err)
	}
	defaultDeleted, err := execute(database, `SELECT parent_code FROM default_children WHERE id = 'default-delete'`)
	if err != nil || len(defaultDeleted.rows) != 1 || defaultDeleted.rows[0][0].String() != "fallback" {
		t.Fatalf("SET DEFAULT delete row = %#v, %v", defaultDeleted, err)
	}

	if _, err := execute(database, `DELETE FROM parents WHERE id = 'blocked'`); err == nil ||
		!strings.Contains(err.Error(), "blockers.child_id still references") {
		t.Fatalf("nested restrict error = %v", err)
	}
	for _, assertion := range []struct {
		query string
		want  float64
	}{
		{`SELECT COUNT(*) FROM parents WHERE id = 'blocked'`, 1},
		{`SELECT COUNT(*) FROM cascade_children WHERE id = 'blocked-child'`, 1},
		{`SELECT COUNT(*) FROM nodes`, 0},
	} {
		result, err := execute(database, assertion.query)
		if err != nil || len(result.rows) != 1 || result.rows[0][0].N != assertion.want {
			t.Fatalf("atomic assertion %q = %#v, %v", assertion.query, result, err)
		}
	}
	tenant.Close()

	reopened, reopenedDatabase := start()
	defer reopened.Close()
	for _, assertion := range []struct {
		query string
		want  float64
	}{
		{`SELECT COUNT(*) FROM parents`, 3},
		{`SELECT COUNT(*) FROM cascade_children`, 1},
		{`SELECT COUNT(*) FROM null_children WHERE parent_code IS NULL`, 2},
		{`SELECT COUNT(*) FROM default_children WHERE parent_code = 'fallback'`, 2},
	} {
		result, err := execute(reopenedDatabase, assertion.query)
		if err != nil || len(result.rows) != 1 || result.rows[0][0].N != assertion.want {
			t.Fatalf("restart assertion %q = %#v, %v", assertion.query, result, err)
		}
	}
	metadata, err := execute(reopenedDatabase, `PRAGMA foreign_key_list(default_children)`)
	if err != nil || len(metadata.rows) != 1 || metadata.rows[0][5].String() != "SET DEFAULT" ||
		metadata.rows[0][6].String() != "SET DEFAULT" {
		t.Fatalf("reopened referential metadata = %#v, %v", metadata, err)
	}
}

func TestKitDBReferentialActionContextBoundsAndDetectsCycles(t *testing.T) {
	definition := bindStructDef("items", nil, map[string]*ColumnSpec{
		"id": {kind: "text", primary: true, seq: 1},
	})
	row := kitDBStoredRow{key: []byte("row"), values: map[string]value.Value{"id": value.New("one")}}
	context := &kitDBReferentialActionContext{rows: kitDBMutationRowLimit}
	if _, err := context.enter("delete", definition, []kitDBStoredRow{row}); err == nil ||
		!strings.Contains(err.Error(), "more than") {
		t.Fatalf("referential row bound = %v", err)
	}
	context = &kitDBReferentialActionContext{}
	leave, err := context.enter("update", definition, []kitDBStoredRow{row})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := context.enter("update", definition, []kitDBStoredRow{row}); err == nil ||
		!strings.Contains(err.Error(), "cycle revisits") {
		t.Fatalf("referential cycle = %v", err)
	}
	leave()
}
