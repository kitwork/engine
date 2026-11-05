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

// TestBuildCopiesTheColumnsAsTheSnapshotHoldsThem: the main part holds every row of the snapshot in rowid order, each
// value of each column as SQLite gives it (integers, reals, texts of any script, NULLs), names the snapshot's
// revision and schema_version, and opens again.
//
// CONTROL (05/11/2026): with a NULL kept as a value, "row 0: copy [3 tiki -961073219662 847.2139302442847 1 ], SQLite
// [3 tiki -961073219662 847.2139302442847 1 <nil>]".
func TestBuildCopiesTheColumnsAsTheSnapshotHoldsThem(t *testing.T) {
	path, database := ordersFile(t, 5000)
	tx := readTransaction(t, path)
	directory := Directory(path)
	columns := []string{"merchant", "amount", "price", "paid", "note"}
	manifest, err := Build(context.Background(), tx, "main", directory, "orders", columns)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Revision != database.Revision() || manifest.Rows != 4286 {
		t.Fatalf("manifest at revision %d with %d rows, want %d and 4286", manifest.Revision, manifest.Rows, database.Revision())
	}
	want := cells(t, tx, `SELECT _rowid_, merchant, amount, price, paid, note FROM orders ORDER BY _rowid_`)
	copy, err := Open(directory, "orders")
	if err != nil || copy == nil {
		t.Fatalf("open: %v", err)
	}
	defer copy.Close()
	if got := copyCells(t, copy, columns); !reflect.DeepEqual(got, want) {
		for position := range min(len(got), len(want)) {
			if !reflect.DeepEqual(got[position], want[position]) {
				t.Fatalf("row %d: copy %v, SQLite %v", position, got[position], want[position])
			}
		}
		t.Fatalf("the copy holds %d rows, SQLite %d", len(got), len(want))
	}
	// a subset of the columns in another order
	if got := copyCells(t, copy, []string{"note", "paid"}); !reflect.DeepEqual(got, cells(t, tx, `SELECT _rowid_, note, paid FROM orders ORDER BY _rowid_`)) {
		t.Fatal("the copy read for note and paid differs from SQLite")
	}
}

// TestBuildRefusesWhatItCannotHold: a BLOB column, a column that is not there, and a value of another type than its
// column's are refused, naming the column; nothing is left behind.
//
// CONTROL (05/11/2026): with BLOB taken as an integer, "a BLOB column: <nil>"; with an integer's type not checked, "a
// text in an integer column: <nil>"; with the file of a refused build left, "a refused build left 1 files". And for
// TestBuildAgainReplacesTheMainPart, with the data file before not removed, "files [orders-5-….kcol orders-6-….kcol
// orders.json], want [orders-6-….kcol orders.json]".
func TestBuildRefusesWhatItCannotHold(t *testing.T) {
	path, database := ordersFile(t, 600)
	ctx := context.Background()
	directory := Directory(path)
	tx := readTransaction(t, path)
	if _, err := Build(ctx, tx, "main", directory, "orders", []string{"photo"}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "orders.photo") {
		t.Fatalf("a BLOB column: %v", err)
	}
	if _, err := Build(ctx, tx, "main", directory, "orders", []string{"nothing"}); err == nil || !strings.Contains(err.Error(), `no column "nothing"`) {
		t.Fatalf("a column that is not there: %v", err)
	}
	// a text in the integer column amount (the table is not STRICT, so SQLite keeps it)
	if _, err := database.Write(ctx, "t", func(tx *kitdb.Transaction) error {
		_, err := tx.Exec(`UPDATE orders SET amount = 'many' WHERE id = 3`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fresh := readTransaction(t, path)
	if _, err := Build(ctx, fresh, "main", directory, "orders", []string{"amount"}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "orders.amount, rowid 3") {
		t.Fatalf("a text in an integer column: %v", err)
	}
	if entries, _ := os.ReadDir(directory); len(entries) != 0 {
		t.Fatalf("a refused build left %d files", len(entries))
	}
}

// TestBuildAgainReplacesTheMainPart: a second build names a new data file, the manifest moves to it, and the file
// before goes.
func TestBuildAgainReplacesTheMainPart(t *testing.T) {
	path, database := ordersFile(t, 1200)
	ctx := context.Background()
	directory := Directory(path)
	first, err := Build(ctx, readTransaction(t, path), "main", directory, "orders", []string{"amount"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Write(ctx, "t", func(tx *kitdb.Transaction) error {
		_, err := tx.Exec(`INSERT INTO orders (id, merchant, amount, paid) VALUES (999999, 'x', 1, 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	second, err := Build(ctx, readTransaction(t, path), "main", directory, "orders", []string{"amount"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Data == first.Data || second.Revision != first.Revision+1 || second.Rows != first.Rows+1 || second.MaxRowID != 999999 {
		t.Fatalf("second build %+v after %+v", second, first)
	}
	entries, _ := os.ReadDir(directory)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{second.Data, "orders.json"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}
}
