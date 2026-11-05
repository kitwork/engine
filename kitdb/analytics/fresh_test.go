package analytics

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/columnar"
	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

// viewCells reads the table as view holds it, rows in rowid order: the main part's rows not skipped and the changed
// rows, merged.
func viewCells(t *testing.T, view *View, columns []string) [][]any {
	t.Helper()
	byRowID := map[int64][]any{}
	_, err := view.Scan(context.Background(), columns, nil, func(batch *columnar.Batch, skip []bool) error {
		for row := range batch.Rows {
			if skip != nil && skip[row] {
				continue
			}
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
			rowID := values[0].(int64)
			if _, twice := byRowID[rowID]; twice {
				t.Fatalf("rowid %d read twice", rowID)
			}
			byRowID[rowID] = values
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var all [][]any
	for _, rowID := range slices.Sorted(func(yield func(int64) bool) {
		for rowID := range byRowID {
			if !yield(rowID) {
				return
			}
		}
	}) {
		all = append(all, byRowID[rowID])
	}
	return all
}

// write runs one request through the writer.
func write(t *testing.T, database *kitdb.Database, query string, arguments ...any) {
	t.Helper()
	if _, err := database.Write(context.Background(), "t", func(tx *kitdb.Transaction) error {
		_, err := tx.Exec(query, arguments...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// begin starts the snapshot of tx: SQLite takes it at the first statement of a transaction, not at BEGIN.
func begin(t *testing.T, tx *sql.Tx) {
	t.Helper()
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM orders`).Scan(&count); err != nil {
		t.Fatal(err)
	}
}

const viewQuery = `SELECT _rowid_, merchant, amount, price, paid, note FROM orders ORDER BY _rowid_`

var viewColumns = []string{"merchant", "amount", "price", "paid", "note"}

// TestViewReadsEachSnapshotAsTheRowsDo: after inserts, updates (of the copy's columns and others), deletes and a row
// inserted then deleted, a view of each snapshot reads exactly what SQLite reads in the same read transaction; a
// snapshot older than one already read is still read as it was.
//
// CONTROL (05/11/2026): with the changed rows not skipped in the main part, "rowid 90 read twice"; with the newest
// revision read for every snapshot, "the older snapshot (revision 21) read after newer ones differs from SQLite".
func TestViewReadsEachSnapshotAsTheRowsDo(t *testing.T) {
	path, database := ordersFile(t, 3000)
	ctx := context.Background()
	directory := Directory(path)
	if _, err := Build(ctx, readTransaction(t, path), directory, "orders", viewColumns); err != nil {
		t.Fatal(err)
	}
	table, err := OpenTable(directory, "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	random := rand.New(rand.NewPCG(3, 9))
	var older *sql.Tx
	for step := range 40 {
		switch step % 5 {
		case 0:
			write(t, database, `INSERT INTO orders (id, merchant, amount, price, paid) VALUES (?, 'mới', ?, 1.5, 1)`, 100000+step, step)
		case 1:
			write(t, database, `UPDATE orders SET amount = amount + 1, note = 'sửa' WHERE id = ?`, 3*(1+random.IntN(3000)))
		case 2:
			write(t, database, `DELETE FROM orders WHERE id = ?`, 3*(1+random.IntN(3000)))
		case 3:
			write(t, database, `INSERT INTO orders (id, merchant, paid) VALUES (?, 'thoáng qua', 0)`, 200000+step)
			write(t, database, `DELETE FROM orders WHERE id = ?`, 200000+step)
		case 4:
			write(t, database, `UPDATE orders SET merchant = 'đổi', price = NULL WHERE id % 97 = ?`, random.IntN(97))
		}
		tx := readTransaction(t, path)
		if step == 10 {
			begin(t, tx) // its snapshot starts now, and is read after newer ones were
			older = tx
			continue
		}
		view, err := table.View(ctx, tx)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if got, want := viewCells(t, view, viewColumns), cells(t, tx, viewQuery); !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d (revision %d, %d changed rows): the view reads %d rows, SQLite %d, or other values", step,
				view.Revision, view.Changed(), len(got), len(want))
		}
	}
	view, err := table.View(ctx, older)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := viewCells(t, view, []string{"amount", "note"}), cells(t, older, `SELECT _rowid_, amount, note FROM orders ORDER BY _rowid_`); !reflect.DeepEqual(got, want) {
		t.Fatalf("the older snapshot (revision %d) read after newer ones differs from SQLite", view.Revision)
	}
}

// TestViewRefusesWhatItCannotReach: a schema change, rows changed outside the history, a hole in the history, a file
// without one and a snapshot older than the copy are ErrStale.
//
// CONTROL (05/11/2026): with the schema_version not compared, the schema subtest still stale but by the history's
// Statement ("revision 6 changed the schema"), a second guard; with the revisions not checked to follow one another,
// the hole still stale by the history's end ("the history ends at revision 6, the snapshot is at 7"); with a Barrier
// on the table not checked, "view: <nil>, want ErrStale saying "changed rows of orders outside the history""; with a
// file without a history taken as fresh, "view: <nil>, want ErrStale saying "the file has no history"".
func TestViewRefusesWhatItCannotReach(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T) (string, *kitdb.Database, *Table, *sql.Tx) {
		path, database := ordersFile(t, 600)
		before := readTransaction(t, path)
		begin(t, before)
		write(t, database, `UPDATE orders SET amount = 1 WHERE id = 3`)
		if _, err := Build(ctx, readTransaction(t, path), Directory(path), "orders", viewColumns); err != nil {
			t.Fatal(err)
		}
		table, err := OpenTable(Directory(path), "orders")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { table.Close() })
		return path, database, table, before
	}
	stale := func(t *testing.T, table *Table, tx *sql.Tx, want string) {
		t.Helper()
		if _, err := table.View(ctx, tx); !errors.Is(err, ErrStale) || !strings.Contains(err.Error(), want) {
			t.Fatalf("view: %v, want ErrStale saying %q", err, want)
		}
	}
	t.Run("older snapshot", func(t *testing.T) {
		_, _, table, before := open(t)
		stale(t, table, before, "the snapshot is at revision")
	})
	t.Run("schema", func(t *testing.T) {
		path, database, table, _ := open(t)
		if _, err := database.Migrate(ctx, "t", `CREATE TABLE other (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		stale(t, table, readTransaction(t, path), "the schema moved")
	})
	t.Run("rows outside the history", func(t *testing.T) {
		path, database, table, _ := open(t)
		if _, err := database.Maintain(ctx, "t", func(conn *sql.Conn) error {
			_, err := conn.ExecContext(ctx, `UPDATE orders SET amount = 7 WHERE id = 6`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		stale(t, table, readTransaction(t, path), "changed rows of orders outside the history")
	})
	t.Run("hole in the history", func(t *testing.T) {
		path, database, table, _ := open(t)
		write(t, database, `UPDATE orders SET amount = 2 WHERE id = 3`)
		write(t, database, `UPDATE orders SET amount = 3 WHERE id = 3`)
		manifest := table.Manifest()
		if _, err := database.Maintain(ctx, "t", func(conn *sql.Conn) error {
			_, err := conn.ExecContext(ctx, `DELETE FROM kitwork_history WHERE rev = ?`, manifest.Revision+1)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		stale(t, table, readTransaction(t, path), "the history goes from revision")
	})
	t.Run("no history", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "plain.db")
		db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(ordersSchema); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO orders (id, merchant, paid) VALUES (1, 'a', 0)`); err != nil {
			t.Fatal(err)
		}
		if _, err := Build(ctx, readTransaction(t, path), Directory(path), "orders", []string{"merchant"}); err != nil {
			t.Fatal(err)
		}
		table, err := OpenTable(Directory(path), "orders")
		if err != nil {
			t.Fatal(err)
		}
		defer table.Close()
		stale(t, table, readTransaction(t, path), "the file has no history")
	})
}

// TestViewScansABlockHoldingAChangedRow: a count that takes each block's row count from its statistics is right
// after updates and deletes, since a block holding a changed row is scanned and the row is skipped there.
//
// CONTROL (05/11/2026): with the statistics of a block holding a changed row used, "counted 2573 rows by statistics,
// SQLite 2563".
func TestViewScansABlockHoldingAChangedRow(t *testing.T) {
	path, database := ordersFile(t, 3000)
	ctx := context.Background()
	if _, err := Build(ctx, readTransaction(t, path), Directory(path), "orders", viewColumns); err != nil {
		t.Fatal(err)
	}
	table, err := OpenTable(Directory(path), "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer table.Close()
	write(t, database, `DELETE FROM orders WHERE id BETWEEN 300 AND 330`)
	write(t, database, `UPDATE orders SET amount = 5 WHERE id = 3003`)
	write(t, database, `INSERT INTO orders (id, merchant, paid) VALUES (77777, 'x', 1)`)
	tx := readTransaction(t, path)
	view, err := table.View(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	var counted int
	_, err = view.Scan(ctx, []string{"paid"}, func(statistics *columnar.BlockStatistics) (columnar.BlockAction, error) {
		counted += statistics.Rows // taken if the block is not scanned; given back below if it is
		return columnar.UseBlockStatistics, nil
	}, func(batch *columnar.Batch, skip []bool) error {
		if skip != nil {
			counted -= batch.Rows // a main block scanned after all
		}
		for row := range batch.Rows {
			if skip == nil || !skip[row] {
				counted++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var want int
	if err := tx.QueryRow(`SELECT count(*) FROM orders`).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if counted != want {
		t.Fatalf("counted %d rows by statistics, SQLite %d", counted, want)
	}
}
