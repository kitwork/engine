package fulltext

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// openDeclared opens posts as a table application.work declares: untracked, its index checked by its signature.
func openDeclared(t *testing.T, path string) (*Table, func()) {
	t.Helper()
	release, err := Shared().Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	table, err := Shared().Open(path, "posts", "test.posts", []Column{{Name: "title", Boost: 1}}, true)
	if err != nil {
		release()
		t.Fatal(err)
	}
	return table, release
}

// A table application.work declares keeps its index while only other tables of its file change: a row written to
// another table, a table made, a column of its own that is not searched; across a restart the index is used as it is.
// A searchable column of its own updated builds it again. The signature it was built with before 09/11/2026 (the
// file's schema version, no definition) still holds while the file's schema has not moved.
//
// CONTROL (09/11/2026): with any later revision taken as a change, as before, "after a row of another table: posts'
// index was built again"; with a commit of any table counted as one of posts, the same; with the file's schema
// version in place of the table's definition, "after a table made: posts' index was built again"; with updates of
// posts counted whatever column they change, "after posts' stock: …"; with reuse not storing the state it moved to,
// "the index's stored state {\"revision\":4,…} after the restart, want the file's revision 7"; with updates of posts
// not counted, "after posts' title: com tam = \"\", want 3".
func TestDeclaredTableKeepsItsIndexWhileOtherTablesChange(t *testing.T) {
	previous := Watch
	Watch = 20 * time.Millisecond
	t.Cleanup(func() { Watch = previous })
	path, database, write := trackedFile(t)
	if _, err := database.Migrate(context.Background(), "test", `CREATE TABLE logs (id INTEGER PRIMARY KEY, line TEXT)`); err != nil {
		t.Fatal(err)
	}
	table, release := openDeclared(t, path)
	if table.Tracked() {
		t.Fatal("a declared table is tracked")
	}
	if got := found(t, table, "ca phe"); got != "1" {
		t.Fatalf("ca phe = %q, want 1", got)
	}
	builds := table.builds.Load()
	settle := func() { time.Sleep(10 * Watch) } // several looks of the watch
	steps := []struct {
		name string
		run  func()
	}{
		{"a row of another table", func() { write(`INSERT INTO logs (line) VALUES ('một dòng')`) }},
		{"a table made", func() {
			if _, err := database.Migrate(context.Background(), "test", `CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
				t.Fatal(err)
			}
		}},
		{"posts' stock", func() { write(`UPDATE posts SET stock = 7 WHERE id = 1`) }},
	}
	for _, step := range steps {
		step.run()
		settle()
		if table.builds.Load() != builds {
			t.Fatalf("after %s: posts' index was built again", step.name)
		}
	}
	release()

	// Restarted: the index is used as it is, its state moved past the commits read, so it is not read from them again.
	write(`INSERT INTO logs (line) VALUES ('khi đóng')`)
	table, release = openDeclared(t, path)
	defer release()
	if !table.Ready() || table.Built() != 0 {
		t.Fatalf("after the restart: ready %v, %d rows read again, want the index used as it is", table.Ready(), table.Built())
	}
	revision := func() int64 {
		var last int64
		if err := database.Read(context.Background(), func(snapshot *kitdbsqlite.Snapshot) error {
			rows, err := snapshot.Query(`SELECT max(rev) FROM kitwork_history`)
			if err != nil {
				return err
			}
			defer rows.Close()
			rows.Next()
			return rows.Scan(&last)
		}); err != nil {
			t.Fatal(err)
		}
		return last
	}
	container, err := table.file.open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	index, encoded, err := container.Index(table.name, table.definition)
	if err != nil {
		t.Fatal(err)
	}
	_ = index.Close()
	var stored state
	if err := json.Unmarshal(encoded, &stored); err != nil || stored.Signature == nil || stored.Signature.Revision != revision() {
		t.Fatalf("the index's stored state %s after the restart, want the file's revision %d", encoded, revision())
	}

	// posts' own searchable column changed: built again, the new title found.
	write(`UPDATE posts SET title = 'Cơm tấm sườn' WHERE id = 3`)
	deadline := time.Now().Add(5 * time.Second)
	for found(t, table, "com tam") != "3" && time.Now().Before(deadline) {
		time.Sleep(Watch)
	}
	if got := found(t, table, "com tam"); got != "3" {
		t.Fatalf("after posts' title: com tam = %q, want 3", got)
	}

	// A signature written before 09/11/2026: no definition, read by the file's schema version.
	check := func(old Signature) bool {
		t.Helper()
		_, same, err := table.checkSignature(context.Background(), old)
		if err != nil {
			t.Fatal(err)
		}
		return same
	}
	table.mutex.RLock()
	now := table.signature
	table.mutex.RUnlock()
	old := now
	old.Definition = ""
	if !check(old) {
		t.Fatal("a signature without a definition, the schema version unchanged: not the same")
	}
	old.SchemaVersion--
	if check(old) {
		t.Fatal("a signature without a definition, the schema version moved: the same")
	}
}
