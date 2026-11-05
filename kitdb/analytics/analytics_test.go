package analytics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/columnar"
	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

const ordersSchema = `CREATE TABLE orders (id INTEGER PRIMARY KEY, merchant TEXT NOT NULL, amount INTEGER, price REAL,
	paid INTEGER NOT NULL CHECK (paid IN (0, 1)), note TEXT, photo BLOB)`

// ordersFile is a KitDB file of orders written through the writer: rowids with gaps, NULLs, negative and large
// numbers, texts of many scripts, a boolean.
func ordersFile(t *testing.T, rows int) (string, *kitdb.Database) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "kitwork.db")
	database, err := kitdb.Open(ctx, path, kitdb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Migrate(ctx, "boot", ordersSchema); err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewPCG(7, 7))
	merchants := []string{"shopee", "lazada", "tiki", "sendo", "Bàn phím cơ", "日本", ""}
	for first := 0; first < rows; first += 500 {
		if _, err := database.Write(ctx, "t", func(tx *kitdb.Transaction) error {
			for id := first + 1; id <= min(first+500, rows); id++ {
				var amount, price, note any
				if random.IntN(10) > 0 {
					amount = random.Int64N(2_000_000_000_000) - 1_000_000_000_000
				}
				if random.IntN(10) > 0 {
					price = random.Float64()*1000 - 100
				}
				if random.IntN(3) > 0 {
					note = fmt.Sprintf("ghi chú %d — %s", id, merchants[random.IntN(len(merchants))])
				}
				if _, err := tx.Exec(`INSERT INTO orders (id, merchant, amount, price, paid, note) VALUES (?, ?, ?, ?, ?, ?)`,
					id*3, merchants[random.IntN(len(merchants))], amount, price, random.IntN(2), note); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Write(ctx, "t", func(tx *kitdb.Transaction) error {
		_, err := tx.Exec(`DELETE FROM orders WHERE id % 7 = 0`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return path, database
}

// readTransaction is a read transaction of the file at path.
func readTransaction(t *testing.T, path string) *sql.Tx {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// copiesOf is the File of the copies of the database file at path, closed when the test ends.
func copiesOf(t *testing.T, path string) *File {
	t.Helper()
	file, err := OpenFile(Path(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

// tableOf opens table's copy of the file, closed when the test ends.
func (f *File) tableOf(t *testing.T, name string) *Table {
	t.Helper()
	table, err := f.Table(name)
	if err != nil || table == nil {
		t.Fatalf("table %q: %v", name, err)
	}
	t.Cleanup(func() { _ = table.Close() })
	return table
}

// builtTable builds the copy of columns of orders from a read transaction of the file at path, and opens it; it is
// closed when the test ends.
func builtTable(t *testing.T, path string, columns []string) *Table {
	t.Helper()
	copies := copiesOf(t, path)
	if _, err := copies.Build(context.Background(), readTransaction(t, path), "main", "orders", columns); err != nil {
		t.Fatal(err)
	}
	return copies.tableOf(t, "orders")
}

// cells reads the rows of a SELECT as values.
func cells(t *testing.T, source Querier, query string) [][]any {
	t.Helper()
	rows, err := source.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, _ := rows.Columns()
	var all [][]any
	for rows.Next() {
		values := make([]any, len(names))
		pointers := make([]any, len(names))
		for position := range values {
			pointers[position] = &values[position]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		all = append(all, values)
	}
	return all
}

// copyCells reads every row of the copy's main part back as values, in rowid order.
func copyCells(t *testing.T, copy *Copy, columns []string) [][]any {
	t.Helper()
	var all [][]any
	_, err := copy.Scan(context.Background(), columns, nil, func(batch *columnar.Batch) error {
		for row := range batch.Rows {
			values := make([]any, len(batch.Columns))
			for position, vector := range batch.Columns {
				if vector.Valid[row] == 0 {
					continue
				}
				switch vector.Field.Kind {
				case columnar.Float:
					values[position] = vector.Floats[row]
				case columnar.Text:
					values[position] = vector.Texts[row]
				default:
					values[position] = vector.Integers[row]
				}
			}
			all = append(all, values)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// temporaryFiles are the files a generation being written or a compaction leaves in dir.
func temporaryFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".projection") || strings.HasSuffix(entry.Name(), ".compact") {
			names = append(names, entry.Name())
		}
	}
	return names
}

// TestBuildCopiesTheColumnsAsTheSnapshotHoldsThem: the main part holds every row of the snapshot in rowid order, each
// value of each column as SQLite gives it (integers, reals, texts of any script, NULLs), names the snapshot's
// revision and schema_version, and opens again from the file.
//
// CONTROL (05/11/2026): with a NULL kept as a value, "row 0: copy [3 tiki -961073219662 847.2139302442847 1 ], SQLite
// [3 tiki -961073219662 847.2139302442847 1 <nil>]".
func TestBuildCopiesTheColumnsAsTheSnapshotHoldsThem(t *testing.T) {
	path, database := ordersFile(t, 5000)
	tx := readTransaction(t, path)
	copies := copiesOf(t, path)
	columns := []string{"merchant", "amount", "price", "paid", "note"}
	manifest, err := copies.Build(context.Background(), tx, "main", "orders", columns)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Revision != database.Revision() || manifest.Rows != 4286 || manifest.Blocks != 5 {
		t.Fatalf("manifest at revision %d with %d rows in %d blocks, want %d, 4286 and 5", manifest.Revision, manifest.Rows,
			manifest.Blocks, database.Revision())
	}
	want := cells(t, tx, `SELECT _rowid_, merchant, amount, price, paid, note FROM orders ORDER BY _rowid_`)
	if err := copies.Close(); err != nil {
		t.Fatal(err)
	}
	table := copiesOf(t, path).tableOf(t, "orders") // opened again from the file
	if got := copyCells(t, table.copy, columns); !reflect.DeepEqual(got, want) {
		for position := range min(len(got), len(want)) {
			if !reflect.DeepEqual(got[position], want[position]) {
				t.Fatalf("row %d: copy %v, SQLite %v", position, got[position], want[position])
			}
		}
		t.Fatalf("the copy holds %d rows, SQLite %d", len(got), len(want))
	}
	// a subset of the columns in another order
	if got := copyCells(t, table.copy, []string{"note", "paid"}); !reflect.DeepEqual(got, cells(t, tx, `SELECT _rowid_, note, paid FROM orders ORDER BY _rowid_`)) {
		t.Fatal("the copy read for note and paid differs from SQLite")
	}
}

// TestBuildRefusesWhatItCannotHold: a BLOB column, a column that is not there, and a value of another type than its
// column's are refused, naming the column; a refused first build leaves no file behind.
//
// CONTROL (05/11/2026): with BLOB taken as an integer, "a BLOB column: <nil>"; with an integer's type not checked, "a
// text in an integer column: <nil>".
func TestBuildRefusesWhatItCannotHold(t *testing.T) {
	path, database := ordersFile(t, 600)
	ctx := context.Background()
	copies := copiesOf(t, path)
	tx := readTransaction(t, path)
	if _, err := copies.Build(ctx, tx, "main", "orders", []string{"photo"}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "orders.photo") {
		t.Fatalf("a BLOB column: %v", err)
	}
	if _, err := copies.Build(ctx, tx, "main", "orders", []string{"nothing"}); err == nil || !strings.Contains(err.Error(), `no column "nothing"`) {
		t.Fatalf("a column that is not there: %v", err)
	}
	// a text in the integer column amount (the table is not STRICT, so SQLite keeps it)
	write(t, database, `UPDATE orders SET amount = 'many' WHERE id = 3`)
	fresh := readTransaction(t, path)
	if _, err := copies.Build(ctx, fresh, "main", "orders", []string{"amount"}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "orders.amount, rowid 3") {
		t.Fatalf("a text in an integer column: %v", err)
	}
	if _, err := os.Stat(Path(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused build made the file: %v", err)
	}
	if left := temporaryFiles(t, filepath.Dir(path)); len(left) != 0 {
		t.Fatalf("a refused build left %v", left)
	}
}

// TestBuildAgainReplacesTheSectionAndCompactionDropsTheOld: a second build names a new section; a reader of the first
// keeps reading it; the file then holds the first section's bytes unread, which a compaction drops once that reader
// is done, the tables then read from the compacted file. The file opens again the same.
//
// CONTROL (05/11/2026): with the compaction not waiting for the readers of the file before, "the compaction ended
// while the first build's reader still read the file: analytics: the compacted copies stay in …: Access is denied";
// with the generations reading a compacted file not renamed with it, "the second compaction ended while a reader of
// the compacted file still read it: … Access is denied".
func TestBuildAgainReplacesTheSectionAndCompactionDropsTheOld(t *testing.T) {
	attempts := moveAttempts
	moveAttempts = 1 // a move tried while a reader holds the file fails at once (Windows) instead of retrying
	t.Cleanup(func() { moveAttempts = attempts })
	path, database := ordersFile(t, 1200)
	ctx := context.Background()
	copies := copiesOf(t, path)
	first, err := copies.Build(ctx, readTransaction(t, path), "main", "orders", []string{"amount"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := copies.Table("orders")
	if err != nil {
		t.Fatal(err)
	}
	write(t, database, `INSERT INTO orders (id, merchant, amount, paid) VALUES (999999, 'x', 1, 0)`)
	second, err := copies.Build(ctx, readTransaction(t, path), "main", "orders", []string{"amount"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Section == first.Section || second.Revision != first.Revision+1 || second.Rows != first.Rows+1 || second.MaxRowID != 999999 {
		t.Fatalf("second build %+v after %+v", second, first)
	}
	if info := copies.Info(); info.ObsoleteBytes < first.Bytes {
		t.Fatalf("obsolete %d bytes after the second build, want the first's %d bytes", info.ObsoleteBytes, first.Bytes)
	}
	if got := copyCells(t, reader.copy, []string{"amount"}); uint64(len(got)) != first.Rows {
		t.Fatalf("the reader of the first build reads %d rows, want %d", len(got), first.Rows)
	}
	compacted := make(chan error, 1)
	go func() { compacted <- copies.Compact(ctx) }()
	select {
	case err := <-compacted:
		t.Fatalf("the compaction ended while the first build's reader still read the file: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-compacted; err != nil {
		t.Fatal(err)
	}
	if info := copies.Info(); info.ObsoleteBytes != 0 || info.LiveBytes != info.FileBytes {
		t.Fatalf("after the compaction %+v", info)
	}
	if left := temporaryFiles(t, filepath.Dir(path)); len(left) != 0 {
		t.Fatalf("the compaction left %v", left)
	}
	want := cells(t, readTransaction(t, path), `SELECT _rowid_, amount FROM orders ORDER BY _rowid_`)
	compactedTable, err := copies.Table("orders")
	if err != nil {
		t.Fatal(err)
	}
	if got := copyCells(t, compactedTable.copy, []string{"amount"}); !reflect.DeepEqual(got, want) {
		t.Fatal("the compacted copy differs from SQLite")
	}
	// a second compaction waits for the readers of the file the first moved over the path, read there
	again := make(chan error, 1)
	go func() { again <- copies.Compact(ctx) }()
	select {
	case err := <-again:
		t.Fatalf("the second compaction ended while a reader of the compacted file still read it: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	compactedTable.Close()
	select {
	case err := <-again:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second compaction did not end")
	}
	if err := copies.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := copiesOf(t, path).tableOf(t, "orders")
	if reopened.Manifest().Section != second.Section || !reflect.DeepEqual(copyCells(t, reopened.copy, []string{"amount"}), want) {
		t.Fatal("the compacted file opened again differs")
	}
}

// TestOpenFileRemovesWhatItCannotRead: the directory of copies the first KCOL engine on SQLite wrote (05/11/2026) is
// removed, its own files alone (a directory holding another file is refused); a file that is no copies' file is
// removed, and a compaction a process ended before moving; a build then makes the file anew.
//
// CONTROL (05/11/2026): with the files of the directory not checked, "a directory holding notes.txt: <nil>".
func TestOpenFileRemovesWhatItCannotRead(t *testing.T) {
	path, _ := ordersFile(t, 300)
	target := Path(path)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"orders-12-1730.kcol", "orders.json", "orders-1730.tmp"} {
		if err := os.WriteFile(filepath.Join(target, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(target, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(target); err == nil || !strings.Contains(err.Error(), "notes.txt") {
		t.Fatalf("a directory holding notes.txt: %v", err)
	}
	if err := os.Remove(filepath.Join(target, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	file, err := OpenFile(target)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the old directory stays: %v", err)
	}
	for _, leftover := range []string{target, target + ".compact"} {
		if err := os.WriteFile(leftover, []byte("not a copies' file"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copies := copiesOf(t, path)
	if left := temporaryFiles(t, filepath.Dir(path)); len(left) != 0 {
		t.Fatalf("open left %v", left)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file that is no copies' file stays: %v", err)
	}
	if _, err := copies.Build(context.Background(), readTransaction(t, path), "main", "orders", []string{"amount"}); err != nil {
		t.Fatal(err)
	}
	if table := copies.tableOf(t, "orders"); table.Manifest().Rows == 0 {
		t.Fatal("the copy built anew is empty")
	}
}
