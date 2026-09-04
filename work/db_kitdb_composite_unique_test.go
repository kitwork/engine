package work

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestKitDBCompositeUniqueDSLIsAtomicAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb, struct, text } = database;
const links = struct({
  id: text().key(),
  domain: text().unique("links_domain_code", 1),
  code: text().unique("links_domain_code", 2),
  title: text()
});
const db = kitdb("composite.kitdb", { links });
router.get((ctx) => {
  const action = ctx.query("action");
  if (action === "seed") {
    return ctx.json(db.transaction((tx) => {
      tx.links.create({ id: "l1", domain: "alpha.test", code: "home", title: "Alpha" });
      tx.links.create({ id: "l2", domain: "beta.test", code: "home", title: "Beta" });
      tx.links.create({ id: "n1", domain: "alpha.test", title: "Nullable one" });
      tx.links.create({ id: "n2", domain: "alpha.test", title: "Nullable two" });
      return { count: tx.links.count() };
    }));
  }
  if (action === "duplicate") {
    return ctx.json(db.links.create({ id: "l3", domain: "alpha.test", code: "home", title: "Duplicate" }));
  }
  if (action === "collapse") {
    return ctx.json(db.links.where("code", "=", "home").update({ domain: "same.test" }));
  }
  return ctx.json({
    count: db.links.count(),
    rows: db.links.orderBy("id", "asc").list(),
    plan: db.links.where("domain", "=", "alpha.test").where("code", "=", "home").explain()
  });
});`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}

	start := func() *Tenant {
		t.Helper()
		tenant := NewTenant(root, "localhost")
		if err := tenant.Run(); err != nil {
			t.Fatal(err)
		}
		return tenant
	}
	request := func(tenant *Tenant, target string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		tenant.Serve(response, httptest.NewRequest(http.MethodGet, "http://localhost"+target, nil))
		return response
	}

	tenant := start()
	seeded := request(tenant, "/?action=seed")
	if seeded.Code != http.StatusOK || !strings.Contains(seeded.Body.String(), `"count":4`) {
		t.Fatalf("seed response = %d %s", seeded.Code, seeded.Body.String())
	}
	duplicate := request(tenant, "/?action=duplicate")
	if duplicate.Code != http.StatusInternalServerError ||
		!strings.Contains(duplicate.Body.String(), `constraint "links_domain_code"`) {
		t.Fatalf("duplicate response = %d %s", duplicate.Code, duplicate.Body.String())
	}
	collapsed := request(tenant, "/?action=collapse")
	if collapsed.Code != http.StatusInternalServerError || !strings.Contains(collapsed.Body.String(), "duplicate unique") {
		t.Fatalf("batch collision response = %d %s", collapsed.Code, collapsed.Body.String())
	}
	assertCompositeUniqueStatus(t, request(tenant, "/"))
	tenant.Close()

	reopened := start()
	defer reopened.Close()
	assertCompositeUniqueStatus(t, request(reopened, "/"))
}

func assertCompositeUniqueStatus(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status response = %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Count int `json:"count"`
		Rows  []struct {
			ID     string  `json:"id"`
			Domain *string `json:"domain"`
			Code   *string `json:"code"`
		} `json:"rows"`
		Plan struct {
			Access string   `json:"access"`
			Index  *string  `json:"index"`
			Fields []string `json:"fields"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 4 || len(payload.Rows) != 4 {
		t.Fatalf("status rows = %#v", payload)
	}
	if payload.Rows[0].ID != "l1" || payload.Rows[0].Domain == nil || *payload.Rows[0].Domain != "alpha.test" ||
		payload.Rows[1].ID != "l2" || payload.Rows[1].Domain == nil || *payload.Rows[1].Domain != "beta.test" {
		t.Fatalf("failed batch changed persisted tuples: %#v", payload.Rows)
	}
	if payload.Rows[2].Code != nil || payload.Rows[3].Code != nil {
		t.Fatalf("nullable tuple values changed: %#v", payload.Rows)
	}
	if payload.Plan.Access != "unique" || payload.Plan.Index == nil || *payload.Plan.Index != "links_domain_code" ||
		!reflect.DeepEqual(payload.Plan.Fields, []string{"domain", "code"}) {
		t.Fatalf("composite unique plan = %#v", payload.Plan)
	}
}

func TestKitDBCompositeUniqueIdentitySurvivesFieldRename(t *testing.T) {
	stored := bindStructDef("links", nil, map[string]*ColumnSpec{
		"id":     {kind: "text", primary: true, seq: 1},
		"domain": {kind: "text", seq: 2, uniques: []colUniqueRef{{name: "links_domain_code", pos: 1}}},
		"code":   {kind: "text", seq: 3, uniques: []colUniqueRef{{name: "links_domain_code", pos: 2}}},
	})
	current := bindStructDef("links", nil, map[string]*ColumnSpec{
		"id":   {kind: "text", primary: true, seq: 1},
		"host": {kind: "text", from: "domain", seq: 2, uniques: []colUniqueRef{{name: "links_domain_code", pos: 1}}},
		"code": {kind: "text", seq: 3, uniques: []colUniqueRef{{name: "links_domain_code", pos: 2}}},
	})
	before := append([]uint32(nil), stored.UniqueConstraints[0].Fields...)
	if err := reconcileKitDBDefinition(stored, current); err != nil {
		t.Fatal(err)
	}
	if current.UniqueConstraints[0].ID != stored.UniqueConstraints[0].ID ||
		!reflect.DeepEqual(current.UniqueConstraints[0].Fields, before) {
		t.Fatalf(
			"tuple identity changed across rename: stored=%#v current=%#v",
			stored.UniqueConstraints[0], current.UniqueConstraints[0],
		)
	}
}

func TestCompositeUniqueSchemaContractLowersToSQLBackends(t *testing.T) {
	columns := map[string]*ColumnSpec{
		"id":     {kind: "text", primary: true, seq: 1},
		"domain": {kind: "text", seq: 2, uniques: []colUniqueRef{{name: "links_domain_code", pos: 1}}},
		"code":   {kind: "text", seq: 3, uniques: []colUniqueRef{{name: "links_domain_code", pos: 2}}},
	}
	if err := validateSchema(columns); err != nil {
		t.Fatal(err)
	}
	indexes := collectSQLIndexes("links", columns)
	if len(indexes) != 1 || !indexes[0].unique || indexes[0].name != "links_domain_code" ||
		!reflect.DeepEqual(indexes[0].columns, []string{"domain", "code"}) {
		t.Fatalf("SQL tuple-unique lowering = %#v", indexes)
	}
	if sql := indexSQL("links", indexes[0]); !strings.HasPrefix(sql, `CREATE UNIQUE INDEX`) {
		t.Fatalf("SQL tuple-unique DDL = %q", sql)
	}

	malformed := map[string]*ColumnSpec{
		"domain": {kind: "text", seq: 1, uniques: []colUniqueRef{{name: "mixed", pos: 1}}},
		"code":   {kind: "text", seq: 2, uniques: []colUniqueRef{{name: "mixed"}}},
	}
	if err := validateSchema(malformed); err == nil || !strings.Contains(err.Error(), "position every field or none") {
		t.Fatalf("mixed tuple positions error = %v", err)
	}

	mismatched := bindStructDef("links", nil, columns)
	mismatched.UniqueConstraints[0].Fields[0], mismatched.UniqueConstraints[0].Fields[1] =
		mismatched.UniqueConstraints[0].Fields[1], mismatched.UniqueConstraints[0].Fields[0]
	if err := validateStructUniqueConstraints(mismatched); err == nil ||
		!strings.Contains(err.Error(), "field order does not match") {
		t.Fatalf("mismatched schema IR error = %v", err)
	}
}

func TestKitDBRemoteSQLCompositeUniqueDDLUpsertAndMigration(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "test", "localhost")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const db = database.kitdb("composite-sql.kitdb", {}, { token: "composite-secret", access: "readwrite" });
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
		_, config, found := resolveServe(tenant, "composite-sql.kitdb")
		if !found || config.database == nil {
			tenant.Close()
			t.Fatal("composite SQL serve registration is unavailable")
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
	defer tenant.Close()
	if _, err := execute(database, `CREATE TABLE links (
  id TEXT PRIMARY KEY,
  domain TEXT,
  code TEXT,
  title TEXT,
  CONSTRAINT links_domain_code UNIQUE (domain, code)
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `CREATE TABLE conflicting_names (
  id TEXT PRIMARY KEY,
  tenant TEXT,
  code TEXT,
  CONSTRAINT links_domain_code UNIQUE (tenant, code)
)`); err == nil || !strings.Contains(err.Error(), `index "links_domain_code" already exists`) {
		t.Fatalf("cross-table constraint collision error = %v", err)
	}
	missingConflictTable, err := execute(database,
		`SELECT COUNT(*) AS total FROM sqlite_master WHERE type = 'table' AND name = 'conflicting_names'`)
	if err != nil || len(missingConflictTable.rows) != 1 || missingConflictTable.rows[0][0].N != 0 {
		t.Fatalf("failed CREATE TABLE published catalog state: %#v, %v", missingConflictTable, err)
	}
	if _, err := execute(database, `CREATE TABLE index_owner (id TEXT PRIMARY KEY, marker TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `CREATE INDEX unique_auto_names_email ON index_owner (marker)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database,
		`CREATE TABLE auto_names (id TEXT PRIMARY KEY, email TEXT UNIQUE)`); err == nil ||
		!strings.Contains(err.Error(), `index "unique_auto_names_email" already exists`) {
		t.Fatalf("auto unique index collision error = %v", err)
	}
	if _, err := execute(database, `INSERT INTO links (id, domain, code, title) VALUES
  ('l1', 'alpha.test', 'home', 'Alpha'),
  ('l2', 'beta.test', 'home', 'Beta'),
  ('n1', 'alpha.test', NULL, 'Nullable one'),
  ('n2', 'alpha.test', NULL, 'Nullable two')`); err != nil {
		t.Fatal(err)
	}
	upserted, err := execute(database, `INSERT INTO links (id, domain, code, title)
VALUES ('ignored', 'alpha.test', 'home', 'Updated')
ON CONFLICT (code, domain) DO UPDATE SET title = excluded.title
RETURNING id, title`)
	if err != nil || upserted.affected != 1 || len(upserted.rows) != 1 ||
		upserted.rows[0][0].String() != "l1" || upserted.rows[0][1].String() != "Updated" {
		t.Fatalf("composite UPSERT = %#v, %v", upserted, err)
	}
	for _, source := range []string{
		`UPDATE links SET code = 'moved' WHERE id = 'l1'`,
		`INSERT INTO links (id, domain, code, title) VALUES ('claim-reuse', 'alpha.test', 'home', 'Reuse')`,
		`DELETE FROM links WHERE id = 'claim-reuse'`,
		`UPDATE links SET code = 'home' WHERE id = 'l1'`,
	} {
		if _, err := execute(database, source); err != nil {
			t.Fatalf("composite claim cleanup for %q: %v", source, err)
		}
	}
	ignored, err := execute(database, `INSERT INTO links (id, domain, code, title)
VALUES ('ignored-again', 'alpha.test', 'home', 'Ignored') ON CONFLICT DO NOTHING`)
	if err != nil || ignored.affected != 0 {
		t.Fatalf("untargeted DO NOTHING = %#v, %v", ignored, err)
	}
	if _, err := execute(database, `INSERT INTO links (id, domain, code, title) VALUES
  ('atomic-ok', 'gamma.test', 'about', 'Should roll back'),
  ('atomic-duplicate', 'alpha.test', 'home', 'Duplicate')`); err == nil ||
		!strings.Contains(err.Error(), `constraint "links_domain_code"`) {
		t.Fatalf("atomic duplicate error = %v", err)
	}
	rolledBack, err := execute(database, `SELECT COUNT(*) AS total FROM links WHERE id = 'atomic-ok'`)
	if err != nil || rolledBack.rows[0][0].N != 0 {
		t.Fatalf("failed multi-insert leaked a row: %#v, %v", rolledBack, err)
	}

	indexList, err := execute(database, `PRAGMA index_list(links)`)
	if err != nil || !remoteResultHasText(indexList, 1, "links_domain_code") ||
		!remoteResultHasNumber(indexList, 2, 1) {
		t.Fatalf("composite PRAGMA index_list = %#v, %v", indexList, err)
	}
	indexInfo, err := execute(database, `PRAGMA index_info(links_domain_code)`)
	if err != nil || !reflect.DeepEqual(remoteResultColumnStrings(indexInfo, 2), []string{"domain", "code"}) {
		t.Fatalf("composite PRAGMA index_info = %#v, %v", indexInfo, err)
	}
	catalog, err := execute(database, `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'links_domain_code'`)
	if err != nil || len(catalog.rows) != 1 || !strings.Contains(catalog.rows[0][0].String(), "CREATE UNIQUE INDEX") {
		t.Fatalf("composite sqlite_master = %#v, %v", catalog, err)
	}
	explain, err := execute(database, `EXPLAIN QUERY PLAN SELECT id FROM links WHERE domain = 'alpha.test' AND code = 'home'`)
	if err != nil || len(explain.rows) != 1 || !strings.Contains(explain.rows[0][3].String(), "links_domain_code") {
		t.Fatalf("composite explain = %#v, %v", explain, err)
	}

	if _, err := execute(database, `CREATE TABLE aliases (id TEXT PRIMARY KEY, domain TEXT, code TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `INSERT INTO aliases (id, domain, code) VALUES
  ('a1', 'same.test', 'same'), ('a2', 'same.test', 'same')`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `CREATE UNIQUE INDEX aliases_domain_code ON aliases (domain, code)`); err == nil ||
		!strings.Contains(err.Error(), "duplicate unique values") {
		t.Fatalf("dirty unique-index migration error = %v", err)
	}
	failedIndexList, err := execute(database, `PRAGMA index_list(aliases)`)
	if err != nil || remoteResultHasText(failedIndexList, 1, "aliases_domain_code") {
		t.Fatalf("failed migration published index: %#v, %v", failedIndexList, err)
	}
	if _, err := execute(database, `DELETE FROM aliases WHERE id = 'a2'`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `CREATE UNIQUE INDEX aliases_domain_code ON aliases (domain, code)`); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(database, `INSERT INTO aliases (id, domain, code) VALUES ('a3', 'same.test', 'same')`); err == nil ||
		!strings.Contains(err.Error(), `constraint "aliases_domain_code"`) {
		t.Fatalf("migrated composite constraint error = %v", err)
	}
	tenant.Close()

	reopened, reopenedDatabase := start()
	defer reopened.Close()
	rows, err := execute(reopenedDatabase, `SELECT id, title FROM links WHERE code = 'home' AND domain = 'alpha.test'`)
	if err != nil || len(rows.rows) != 1 || rows.rows[0][0].String() != "l1" || rows.rows[0][1].String() != "Updated" {
		t.Fatalf("reopened composite lookup = %#v, %v", rows, err)
	}
	reopenedIndex, err := execute(reopenedDatabase, `PRAGMA index_info(aliases_domain_code)`)
	if err != nil || !reflect.DeepEqual(remoteResultColumnStrings(reopenedIndex, 2), []string{"domain", "code"}) {
		t.Fatalf("reopened migrated constraint = %#v, %v", reopenedIndex, err)
	}
}

func remoteResultColumnStrings(result kitDBRemoteResult, column int) []string {
	items := make([]string, 0, len(result.rows))
	for _, row := range result.rows {
		if column < len(row) {
			items = append(items, row[column].String())
		}
	}
	return items
}

func remoteResultHasText(result kitDBRemoteResult, column int, expected string) bool {
	for _, row := range result.rows {
		if column < len(row) && row[column].String() == expected {
			return true
		}
	}
	return false
}

func remoteResultHasNumber(result kitDBRemoteResult, column int, expected float64) bool {
	for _, row := range result.rows {
		if column < len(row) && row[column].N == expected {
			return true
		}
	}
	return false
}
