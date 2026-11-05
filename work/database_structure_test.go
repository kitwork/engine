package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/gateway"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// TestStructureFileBesideTheDatabase: the writer the engine opens writes <file>.sql beside the file, again when its
// schema changes, leaving it untouched when nothing changed; a folder holding the .sql and not the file gets the file
// made from it, its tables and their kinds there before anything writes, the .sql as it was; the last table dropped
// leaves the header alone.
//
// CONTROL (05/11/2026): without Options.Structure given, "no kitwork.db.sql beside the file: open …: The system cannot
// find the file specified."; without the file made from its .sql, "the file made from kitwork.db.sql has no table notes
// with its kinds: "", SQL logic error: no such table: kitwork_columns (1)".
func TestStructureFileBesideTheDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "application", "kitwork.db")
	database, release, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Migrate(ctx, "t", `CREATE TABLE notes (id INTEGER PRIMARY KEY, price INTEGER NOT NULL) STRICT`,
		sqliteColumnsSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Write(ctx, "t", func(transaction *kitdbsqlite.Transaction) error {
		_, err := transaction.Exec(`INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale) VALUES ('notes', 'price', 'decimal', 10, 2)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path + ".sql")
	if err != nil {
		t.Fatalf("no kitwork.db.sql beside the file: %v", err)
	}
	if !strings.Contains(string(content), "price numeric(10, 2) NOT NULL") {
		t.Fatalf("kitwork.db.sql:\n%s", content)
	}
	before, err := os.Stat(path + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Write(ctx, "t", func(transaction *kitdbsqlite.Transaction) error {
		_, err := transaction.Exec(`INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale) VALUES ('notes', 'price', 'decimal', 10, 2)
			ON CONFLICT DO NOTHING`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path + ".sql"); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("kitwork.db.sql was written again with the same structure")
	}
	release()

	// a machine with the folder's kitwork.db.sql and no kitwork.db
	for _, suffix := range []string{"", "-wal", "-shm", ".lock"} {
		_ = os.Remove(path + suffix)
	}
	if err := os.RemoveAll(path + ".history"); err != nil {
		t.Fatal(err)
	}
	made, release, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var price string
	err = made.Read(ctx, func(snapshot *kitdbsqlite.Snapshot) error {
		rows, err := snapshot.Query(`SELECT kind || '(' || precision || ',' || scale || ')' FROM kitwork_columns WHERE table_name = 'notes' AND column_name = 'price'`)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			return os.ErrNotExist
		}
		return rows.Scan(&price)
	})
	if err != nil || price != "decimal(10,2)" {
		t.Fatalf("the file made from kitwork.db.sql has no table notes with its kinds: %q, %v", price, err)
	}
	if again, err := os.ReadFile(path + ".sql"); err != nil || string(again) != string(content) {
		t.Fatalf("the structure after the file was made from it: %v\n%s\nwant\n%s", err, again, content)
	}
	// the last table dropped: the header alone, a structure of no table
	if _, err := made.Migrate(ctx, "t", `DROP TABLE notes`); err != nil {
		t.Fatal(err)
	}
	if empty, err := os.ReadFile(path + ".sql"); err != nil || string(empty) != gateway.StructureHeader {
		t.Fatalf("after the last table went: %v\n%s", err, empty)
	}
}
