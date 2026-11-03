package work

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// historyOf is every commit of the history of the file at path, read on a connection of its own: one line per
// commit, its changes as "Barrier", "Insert orders a@x.vn".
func historyOf(t *testing.T, path string) ([]string, map[uint64]time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var lines []string
	moments := map[uint64]time.Time{}
	err = kitdbsqlite.ReadChanges(context.Background(), db, 0, func(commit kitdbsqlite.Commit) error {
		moments[commit.Revision] = commit.At
		var parts []string
		for _, change := range commit.Changes {
			switch change.Operation {
			case kitdbsqlite.Barrier:
				parts = append(parts, "Barrier")
			case kitdbsqlite.Insert, kitdbsqlite.Update, kitdbsqlite.Delete:
				email := ""
				image := change.After
				if change.Operation == kitdbsqlite.Delete {
					image = change.Before // a deleted row names its columns for its image before (03/11/2026)
				}
				for position, column := range change.Columns {
					if column == "email" && position < len(image) {
						email = " " + fmt.Sprint(image[position])
					}
				}
				parts = append(parts, map[kitdbsqlite.Operation]string{kitdbsqlite.Insert: "Insert", kitdbsqlite.Update: "Update",
					kitdbsqlite.Delete: "Delete"}[change.Operation]+" "+change.Table+email)
			default:
				parts = append(parts, fmt.Sprint(change.Operation))
			}
		}
		lines = append(lines, strings.Join(parts, ", "))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return lines, moments
}

// The app's writes are the history of its database (KITWORK_KITDB.md mục 4–5): the first statement opens the file's
// one writer, the tables the engine makes there are a Barrier with an anchor at it, each insert is one commit naming
// its row, and any moment restores the rows as they were. The app holds the writer while it runs and lets it go when
// it closes, kitwork.db.lock with it.
//
// CONTROL (03/11/2026): with Create writing on the pool as before, the inserts are not in the history: "history =
// ["Barrier"], want the tables then each insert"; with the migration on the pool, the tables are not a Barrier and
// no anchor holds them, "history = ["Insert orders a@x.vn" "Insert orders b@x.vn"], want …"; with the app not letting
// the writer go, "the writer of kitwork.db is still open after the app closed".
func TestAppWritesKeepTheHistory(t *testing.T) {
	managed := newManagedApp(t, map[string]string{"shop.example/router.work": managedRoute(`  return context.json(db.list((orders) => orders));`)})
	tenant := managed.site("shop.example")
	seed(t, tenant,
		[]string{"a@x.vn", "Cà phê", "35000", "2026-10-22"},
		[]string{"b@x.vn", "Trà đào", "40000", "2026-10-22"},
	)
	path := filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db")
	history, moments := historyOf(t, path)
	want := []string{"Barrier", "Insert orders a@x.vn", "Insert orders b@x.vn"}
	if strings.Join(history, " | ") != strings.Join(want, " | ") {
		t.Fatalf("history = %q, want the tables then each insert %q", history, want)
	}

	absolute, _ := filepath.Abs(path)
	historyWriters.Lock()
	entry := historyWriters.files[filepath.Clean(absolute)]
	historyWriters.Unlock()
	if entry == nil {
		t.Fatal("no writer of kitwork.db is open while the app runs")
	}
	for revision, rows := range map[uint64]string{2: "a@x.vn", 3: "a@x.vn,b@x.vn"} {
		target := filepath.Join(t.TempDir(), "restored.db")
		if _, err := entry.database.Restore(context.Background(), moments[revision], target); err != nil {
			t.Fatalf("restore at revision %d: %v", revision, err)
		}
		if got := strings.Join(fileRows(t, target, `SELECT email FROM orders ORDER BY email`), ","); got != rows {
			t.Fatalf("restored at revision %d = %q, want %q", revision, got, rows)
		}
	}

	tenant.Close()
	managed.runtime.Close()
	historyWriters.Lock()
	open := historyWriters.files[filepath.Clean(absolute)] != nil
	historyWriters.Unlock()
	if open {
		t.Fatal("the writer of kitwork.db is still open after the app closed")
	}
}

// A file the engine wrote before 03/11/2026 tracked its searchable tables with triggers, which the writer refuses:
// the first statement after the app starts takes them out with their two tables, and the app writes and searches as
// before.
//
// CONTROL (03/11/2026): with the triggers left, the writer does not open: "insert = 500 Internal Server Error".
func TestAppFileLosesTheSearchTriggers(t *testing.T) {
	managed := newManagedApp(t, map[string]string{
		".shared/schema/shop.work": `import { table, id, text } from "kitwork/schema";
import { connection } from "kitwork/database";

const posts = table({ id: id(), title: text().searchable() });

export const db = connection().tables({ posts });
`,
		"shop.example/router.work": `import * as router from "kitwork/router";
import { db } from "app/.shared/schema/shop.work";

router.post((context) => {
  const title = context.request.form("title");
  return context.json(db.insert((posts) => ({ title })));
});
router.get((context) => {
  const q = context.request.query("q");
  return context.json(db.list((posts) => posts.matches(q)));
});
`,
	})
	path := filepath.Join(managed.root, "acme", ".database", "application", "kitwork.db")
	first := managed.site("shop.example")
	if status, body := managedPost(first, "/", map[string][]string{"title": {"Bánh mì thịt"}}); status != 200 {
		t.Fatalf("first insert = %d %s", status, body)
	}
	first.Close()
	managed.runtime.Close()

	// what the engine wrote before 03/11/2026, the app closed
	legacy, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE kitwork_search_revisions (table_name TEXT PRIMARY KEY, revision INTEGER NOT NULL) WITHOUT ROWID`,
		`CREATE TABLE kitwork_search_changes (table_name TEXT NOT NULL, row_id INTEGER NOT NULL, revision INTEGER NOT NULL, PRIMARY KEY (table_name, row_id)) WITHOUT ROWID`,
		`CREATE TRIGGER kitwork_search_0011223344556677_insert AFTER INSERT ON posts BEGIN UPDATE kitwork_search_revisions SET revision = revision + 1; END`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	legacy.Close()

	again := &managedApp{t: t, root: managed.root, runtime: app.NewRuntime("acme"), cache: managed.cache}
	t.Cleanup(again.runtime.Close)
	tenant := again.site("shop.example")
	if status, body := managedPost(tenant, "/", map[string][]string{"title": {"Cà phê sữa đá"}}); status != 200 {
		t.Fatalf("insert = %d %s", status, body)
	}
	if status, body := managedGet(tenant, "/?q=ca+phe"); status != 200 || !strings.Contains(body, "Cà phê sữa đá") {
		t.Fatalf("matches = %d %s", status, body)
	}
	if left := fileRows(t, path, `SELECT name FROM sqlite_master WHERE name LIKE 'kitwork_search%'`); len(left) != 0 {
		t.Fatalf("the file still holds %v", left)
	}
}
