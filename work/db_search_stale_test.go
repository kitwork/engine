package work

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

func TestSchemaTableSearchRevisionTracksRawWrites(t *testing.T) {
	root := t.TempDir()
	tenant := NewTenant(root, "localhost")
	defer tenant.Close()
	db := sqliteFor(tenant, "raw.db").db()
	if db == nil {
		t.Fatal("SQLite database is unavailable")
	}
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE "notes" ("id" TEXT PRIMARY KEY, "body" TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO "notes" ("id", "body") VALUES ('n1', 'hoc react ngay')`); err != nil {
		t.Fatal(err)
	}
	table := &SchemaTable{
		tenant: tenant, engine: "sqlite", dbName: "raw.db", table: "notes",
		columns: map[string]*ColumnSpec{
			"id":   {kind: "text", primary: true, seq: 1},
			"body": {kind: "text", searchable: true, searchWt: 1, seq: 2},
		},
		searchText: "react", limitN: 5,
	}
	before := table.runSearch()
	if before.K != value.Array || len(before.Array()) != 1 {
		t.Fatalf("baseline raw search = %#v", before)
	}

	// This bypasses SchemaTable.Update, so only the durable database trigger can
	// invalidate the projection. The replacement has the same byte length.
	if _, err := db.ExecContext(context.Background(),
		`UPDATE "notes" SET "body" = 'hoc vuejs ngay' WHERE "id" = 'n1'`); err != nil {
		t.Fatal(err)
	}
	table.searchText = "vuejs"
	afterVue := table.runSearch()
	if afterVue.K != value.Array || len(afterVue.Array()) != 1 {
		t.Fatalf("raw-write vuejs search = %#v", afterVue)
	}
	table.searchText = "react"
	afterReact := table.runSearch()
	if afterReact.K != value.Array || len(afterReact.Array()) != 0 {
		t.Fatalf("raw-write stale react search = %#v", afterReact)
	}
}

func TestSchemaTableSearchRevisionSurvivesManagerRestart(t *testing.T) {
	root := t.TempDir()
	first := NewTenant(root, "localhost")
	db := sqliteFor(first, "restart.db").db()
	if db == nil {
		t.Fatal("SQLite database is unavailable")
	}
	if _, err := db.Exec(`CREATE TABLE "notes" ("id" TEXT PRIMARY KEY, "body" TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO "notes" ("id", "body") VALUES ('n1', 'hoc react ngay')`); err != nil {
		t.Fatal(err)
	}
	if result := searchRevisionTestTable(first, "react").runSearch(); result.K != value.Array || len(result.Array()) != 1 {
		t.Fatalf("initial search = %#v", result)
	}
	first.Close()

	unchanged := NewTenant(root, "localhost")
	if result := searchRevisionTestTable(unchanged, "react").runSearch(); result.K != value.Array || len(result.Array()) != 1 {
		t.Fatalf("unchanged restart search = %#v", result)
	}
	if stats := unchanged.searchManager.Stats(); stats.Commits != 0 {
		t.Fatalf("unchanged restart rebuilt the projection: %#v", stats)
	}
	unchanged.Close()

	path := filepath.Join(root, "localhost", ".data", "restart.db")
	external, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := external.Exec(`UPDATE "notes" SET "body" = 'hoc vuejs ngay' WHERE "id" = 'n1'`); err != nil {
		_ = external.Close()
		t.Fatal(err)
	}
	if err := external.Close(); err != nil {
		t.Fatal(err)
	}

	changed := NewTenant(root, "localhost")
	defer changed.Close()
	if result := searchRevisionTestTable(changed, "vuejs").runSearch(); result.K != value.Array || len(result.Array()) != 1 {
		t.Fatalf("changed restart search = %#v", result)
	}
	if stats := changed.searchManager.Stats(); stats.Commits != 1 {
		t.Fatalf("changed restart commits = %#v", stats)
	}
}

func searchRevisionTestTable(tenant *Tenant, query string) *SchemaTable {
	return &SchemaTable{
		tenant: tenant, engine: "sqlite", dbName: "restart.db", table: "notes",
		columns: map[string]*ColumnSpec{
			"id":   {kind: "text", primary: true, seq: 1},
			"body": {kind: "text", searchable: true, searchWt: 1, seq: 2},
		},
		searchText: query, limitN: 5,
	}
}

// A same-length edit leaves row count and total text length unchanged. The
// durable same-transaction revision trigger must still force a replacement, so
// the old term cannot survive and the new term is immediately searchable.
func TestSchemaTableSearchStaleOnSameLengthEdit(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-dbstale-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	router := `import { router, database } from "kitwork";` + "\n" +
		`const { sqlite, text } = database;` + "\n" +
		`const db = sqlite("notes.db", { notes: { id: text().key(), body: text().searchable() } });` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  db.notes.create({ id: "n1", body: "học react ngay" });` + "\n" +
		`  const before = db.notes.search("react").limit(5).list();` + "\n" +
		`  db.notes.where("id", "n1").update({ body: "học vuejs ngay" });` + "\n" + // same length, count unchanged
		`  const afterVue = db.notes.search("vuejs").limit(5).list();` + "\n" +
		`  const afterReact = db.notes.search("react").limit(5).list();` + "\n" +
		`  return ctx.json({ before: before, afterVue: afterVue, afterReact: afterReact });` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	body := rec.Body.String()

	before := section(body, `"before":[`)
	if !strings.Contains(before, `"id":"n1"`) {
		t.Fatalf("baseline: search 'react' must find n1 before the edit; got: %s", before)
	}
	afterVue := section(body, `"afterVue":[`)
	if !strings.Contains(afterVue, `"id":"n1"`) {
		t.Errorf("after same-length edit: search 'vuejs' must find n1 (stale index not refreshed); got: %s", afterVue)
	}
	afterReact := section(body, `"afterReact":[`)
	if strings.Contains(afterReact, `"id":"n1"`) {
		t.Errorf("after edit: search 'react' must NOT find n1 (old term still indexed); got: %s", afterReact)
	}
}
