package fulltext

import (
	"context"
	"path/filepath"
	"testing"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// A tracked table the history follows for its search alone (a search_weight in kitwork_columns, no history of its
// own) is recorded light (KITWORK_KITDB.md câu 13.11, lát 2b): which row changed, not how. Its index reads the rows
// again and holds what was written, as from changes with values: a row added is found, a title changed is found by
// its new words and not by its old ones, a row deleted is gone, a row whose id moved is found by its new id.
//
// CONTROL (09/11/2026): with a light update skipped as one that changed no searchable column, "after a change of
// title: ca phe muoi = \"\", want 1".
func TestLightTableFoldsWhatIsWritten(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database, err := kitdbsqlite.Open(ctx, path, kitdbsqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "test",
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL, stock INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS kitwork_columns (table_name TEXT NOT NULL, column_name TEXT NOT NULL, kind TEXT NOT NULL, precision INTEGER NOT NULL, scale INTEGER NOT NULL, search_weight INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (table_name, column_name))`,
	); err != nil {
		t.Fatal(err)
	}
	write := func(statement string) {
		t.Helper()
		if _, err := database.Write(ctx, "test", func(transaction *kitdbsqlite.Transaction) error {
			_, err := transaction.Exec(statement)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	write(`INSERT INTO kitwork_columns VALUES ('posts', 'title', 'text', 0, 0, 1)`)
	write(`INSERT INTO posts (id, title) VALUES (1, 'Cà phê sữa đá'), (2, 'Trà đào cam sả'), (3, 'Bánh mì thịt')`)
	light := false
	if err := database.Changes(ctx, database.Revision()-1, func(commit kitdbsqlite.Commit) error {
		for _, change := range commit.Changes {
			light = light || change.Light && change.Table == "posts"
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !light {
		t.Fatal("posts' rows are not recorded light")
	}
	table, release := openPosts(t, path)
	defer release()
	if !table.Tracked() {
		t.Fatal("posts is not tracked")
	}
	if got := found(t, table, "ca phe"); got != "1" {
		t.Fatalf("ca phe = %q, want 1", got)
	}
	for _, step := range []struct {
		name, statement, text, want string
	}{
		{"an insert", `INSERT INTO posts (id, title) VALUES (4, 'Bánh xèo miền Tây')`, "banh xeo", "4"},
		{"a change of title", `UPDATE posts SET title = 'Cà phê muối' WHERE id = 1`, "ca phe muoi", "1"},
		{"the old title", ``, "sua da", ""},
		{"a change of stock", `UPDATE posts SET stock = 5 WHERE id = 1`, "ca phe muoi", "1"},
		{"a delete", `DELETE FROM posts WHERE id = 2`, "tra dao", ""},
		{"a change of key", `UPDATE posts SET id = 9 WHERE id = 3`, "banh mi", "9"},
	} {
		if step.statement != "" {
			write(step.statement)
		}
		if got := found(t, table, step.text); got != step.want {
			t.Fatalf("after %s: %s = %q, want %q", step.name, step.text, got, step.want)
		}
	}
}
