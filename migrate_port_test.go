package engine

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/work"
)

// kitwork migrate through the port (KITWORK_DATABASE.md "Database là gốc", slice 4): the port names a file of an app's
// database <domain>/<folder>, one of the host's <folder>, and serves no other file. A plan given the port sends its
// PostgreSQL statements there as the admin, the file's schema in them (billing.receipts): the tables are made by the
// port's own writer, with their kinds; a .searchable() weight, which the port has no statement for, is left, said.
//
// CONTROL (05/11/2026): with the statements not naming their schema, "through the port: .database/application/
// billing.db: sau các câu qua the test's port, database vẫn chưa có: bảng receipts (id id, code text)" (made in
// public); with the host's database not named by its folder, "portDatabaseOf(…shopping/kitwork.db) = "" false, want
// "shopping"".
func TestMigrateThroughThePort(t *testing.T) {
	fixture := newAppDatabaseFixture(t)
	layout := work.RootLayoutMultiTenant
	application := filepath.Join(fixture.apps, "acme", ".database", "application")
	for path, want := range map[string]string{
		filepath.Join(application, "kitwork.db"):                      "shop.example/application",
		filepath.Join(application, "billing.db"):                      "shop.example/application",
		filepath.Join(fixture.host, "shopping", "kitwork.db"):         "shopping",
		filepath.Join(fixture.root, "elsewhere", "warehouse", "x.db"): "",
	} {
		if got, ok := portDatabaseOf(fixture.host, fixture.apps, layout, path); got != want || ok != (want != "") {
			t.Errorf("portDatabaseOf(%s) = %q %v, want %q", path, got, ok, want)
		}
	}

	for name, source := range map[string]string{
		"shop.work": `import { table, id, text, decimal, timestamp } from "kitwork/schema";
import { connection } from "kitwork/database";

const notes = table({ id: id(), title: text().searchable(), amount: decimal(12, 2), createdAt: timestamp().created() });

export const db = connection().tables({ notes });
`,
		"billing.work": `import { table, id, text } from "kitwork/schema";
import { connection } from "kitwork/database";

const receipts = table({ id: id(), code: text().unique() });

export const billing = connection().schema("billing").tables({ receipts });
`,
	} {
		path := filepath.Join(fixture.apps, "acme", ".shared", "schema", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plans, err := work.CheckAppDatabases(fixture.apps, "acme", layout, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 {
		t.Fatalf("plans: %+v", plans)
	}
	for _, plan := range plans {
		database, ok := portDatabaseOf(fixture.host, fixture.apps, layout, plan.Path)
		if !ok {
			t.Fatalf("no port database for %s", plan.Path)
		}
		plan.ThroughPort("the test's port", portSender(fixture.address, "kitdb", appDatabaseTestPassword, database))
		if err := plan.Apply(context.Background()); err != nil {
			t.Fatalf("through the port: %v", err)
		}
	}
	if !strings.HasPrefix(plans[0].Statements[0], "CREATE TABLE billing.receipts (") {
		t.Fatalf("the billing plan's statements: %q", plans[0].Statements)
	}
	if left := strings.Join(plans[1].Left, ", "); left != "kiểu của cột notes.title trong kitwork_columns" {
		t.Fatalf("left by the port: %q", left)
	}

	client := fixture.connect(t, "kitdb", appDatabaseTestPassword, "shop.example/application")
	for _, query := range []string{`SELECT count(*) FROM notes`, `SELECT count(*) FROM billing.receipts`} {
		if got := appDatabaseRows(t, client, query); got != "0" {
			t.Fatalf("%s = %q", query, got)
		}
	}
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(application, "kitwork.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var kinds []string
	rows, err := file.Query(`SELECT column_name || ' ' || kind FROM kitwork_columns WHERE table_name = 'notes' ORDER BY column_name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, kind)
	}
	rows.Close()
	if got := strings.Join(kinds, ", "); got != "amount decimal, createdAt datetime, id kitid, title text" {
		t.Fatalf("the kinds the port wrote: %s", got)
	}
}
