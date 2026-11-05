package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/work"
)

// kitwork check holds every app's databases against their files (KITWORK_DATABASE.md "Database là gốc", slice 4),
// reading only: a database that lacks a table the app declares is an issue naming it and kitwork migrate, and nothing
// is made; after kitwork migrate, the same check passes and says the database has everything.
//
// CONTROL (05/11/2026): with CheckWithLayout not adding an issue for what a database lacks, "check passed with a
// database that lacks orders".
func TestCheckSaysWhatADatabaseLacks(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		filepath.Join(root, "acme", ".shared", "schema", "shop.work"): `import { table, id, text } from "kitwork/schema";
import { connection } from "kitwork/database";

const orders = table({ id: id(), email: text() });

export const db = connection().tables({ orders });
`,
		filepath.Join(root, "acme", "shop.example", work.UIRouterFileName): `import * as router from "kitwork/router";
router.get((context) => context.text("ok"));`,
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report := CheckWithLayout(root, 0, work.RootLayoutMultiTenant)
	found := false
	for _, issue := range report.Issues {
		if issue.Stage == "database" && strings.Contains(issue.Error(), "acme: database (.database/application/kitwork.db): chưa có bảng orders (id id, email text); chạy kitwork migrate") {
			found = true
		}
	}
	if !found {
		t.Fatalf("check passed with a database that lacks orders: %v", report.Issues)
	}
	if _, err := os.Stat(filepath.Join(root, "acme", ".database")); !os.IsNotExist(err) {
		t.Fatalf("kitwork check made the app's database (%v)", err)
	}

	plans, release, err := work.PlanAppMigrations(root, "acme", work.RootLayoutMultiTenant, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if err := plan.Apply(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	release()
	report = CheckWithLayout(root, 0, work.RootLayoutMultiTenant)
	if !report.OK() || len(report.Databases) != 1 || report.Databases[0].Pending() || len(report.Databases[0].Differ) != 0 {
		t.Fatalf("after kitwork migrate: issues %v, databases %+v", report.Issues, report.Databases)
	}
}
