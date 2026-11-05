package analytics

import (
	"context"
	"errors"
	"math/rand/v2"
	"reflect"
	"testing"
)

// TestFoldWritesTheChangedBlocksAsTheRowsAre: rounds of updates, deletes, rows inserted past the last rowid and between
// rowids, rows inserted then deleted, each folded at the revision of a read transaction: the main part then holds
// exactly what SQLite holds in it, and a view of a later snapshot through the folded table too. A round changing
// the rows past the end alone writes the last block and refers to the others where they are.
//
// CONTROL (05/11/2026): with the rows past the last block given to no block, "round 0: the folded copy holds 4286
// rows, SQLite 4291"; with the row of a changed rowid kept beside its change, "round 0: row 6: copy [18  -400348272592
// …], SQLite [24 日本 170907380451 …]"; with every block written again, "round 3 (rows past the end): the fold wrote
// 269412 bytes of a section of 268960".
func TestFoldWritesTheChangedBlocksAsTheRowsAre(t *testing.T) {
	path, database := ordersFile(t, 5000)
	ctx := context.Background()
	copies := copiesOf(t, path)
	if _, err := copies.Build(ctx, readTransaction(t, path), "main", "orders", viewColumns); err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewPCG(11, 5))
	next := int64(20000)
	for round := range 6 {
		switch round {
		case 3: // past the end alone
			for range 5 {
				next++
				write(t, database, `INSERT INTO orders (id, merchant, amount, price, paid) VALUES (?, 'cuối', ?, 2.5, 1)`, next, next)
			}
		default:
			for step := range 30 {
				switch step % 6 {
				case 0:
					next++
					write(t, database, `INSERT INTO orders (id, merchant, amount, price, paid) VALUES (?, 'mới', ?, 1.5, 1)`, next, step)
				case 1:
					write(t, database, `UPDATE orders SET amount = amount + 1, note = 'sửa' WHERE id = ?`, 3*(1+random.IntN(5000)))
				case 2:
					write(t, database, `DELETE FROM orders WHERE id = ?`, 3*(1+random.IntN(5000)))
				case 3: // between two rowids of a block
					write(t, database, `INSERT OR IGNORE INTO orders (id, merchant, paid) VALUES (?, 'giữa', 0)`, 3*(1+random.IntN(5000))+1)
				case 4:
					write(t, database, `INSERT INTO orders (id, merchant, paid) VALUES (?, 'thoáng qua', 0)`, 900000+round*100+step)
					write(t, database, `DELETE FROM orders WHERE id = ?`, 900000+round*100+step)
				case 5:
					write(t, database, `UPDATE orders SET merchant = 'đổi', price = NULL WHERE id % 89 = ?`, random.IntN(89))
				}
			}
		}
		table := copies.tableOf(t, "orders")
		tx := readTransaction(t, path)
		view, err := table.View(ctx, tx, "main")
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		before := copies.Info().FileBytes
		manifest, err := copies.Fold(ctx, view)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		written := copies.Info().FileBytes - before
		want := cells(t, tx, viewQuery)
		folded := copies.tableOf(t, "orders")
		got := copyCells(t, folded.copy, viewColumns)
		if !reflect.DeepEqual(got, want) {
			for position := range min(len(got), len(want)) {
				if !reflect.DeepEqual(got[position], want[position]) {
					t.Fatalf("round %d: row %d: copy %v, SQLite %v", round, position, got[position], want[position])
				}
			}
			t.Fatalf("round %d: the folded copy holds %d rows, SQLite %d", round, len(got), len(want))
		}
		if manifest.Revision != view.Revision || manifest.Rows != uint64(len(want)) || manifest.MaxRowID != want[len(want)-1][0].(int64) {
			t.Fatalf("round %d: manifest %+v, want revision %d, %d rows", round, manifest, view.Revision, len(want))
		}
		if round == 3 && written*2 > manifest.Bytes {
			t.Fatalf("round 3 (rows past the end): the fold wrote %d bytes of a section of %d", written, manifest.Bytes)
		}
		// the folded table read through a view of a later snapshot
		write(t, database, `UPDATE orders SET amount = 7 WHERE id = (SELECT max(id) FROM orders)`)
		later := readTransaction(t, path)
		laterView, err := folded.View(ctx, later, "main")
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if got, want := viewCells(t, laterView, viewColumns), cells(t, later, viewQuery); !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: the view of the folded copy differs from SQLite", round)
		}
	}
}

// TestFoldWritesHalfEmptyBlocksWhole: when deletes left the blocks half empty on average, the fold writes every block
// again in full blocks, the section then one range of the file.
//
// CONTROL (05/11/2026): with half-empty blocks folded as any, "blocks 5 (extents 2), want 2 (extents 1)".
func TestFoldWritesHalfEmptyBlocksWhole(t *testing.T) {
	path, database := ordersFile(t, 5000)
	ctx := context.Background()
	copies := copiesOf(t, path)
	if _, err := copies.Build(ctx, readTransaction(t, path), "main", "orders", viewColumns); err != nil {
		t.Fatal(err)
	}
	table := copies.tableOf(t, "orders")
	write(t, database, `DELETE FROM orders WHERE id % 5 <> 0 AND id < 14000`)
	tx := readTransaction(t, path)
	view, err := table.View(ctx, tx, "main")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := copies.Fold(ctx, view)
	if err != nil {
		t.Fatal(err)
	}
	want := cells(t, tx, viewQuery)
	full := (len(want) + 1023) / 1024
	if extents := copies.SectionExtents("orders"); manifest.Blocks != full || extents != 1 {
		t.Fatalf("blocks %d (extents %d), want %d (extents 1)", manifest.Blocks, extents, full)
	}
	if got := copyCells(t, copies.tableOf(t, "orders").copy, viewColumns); !reflect.DeepEqual(got, want) {
		t.Fatal("the copy written whole differs from SQLite")
	}
}

// TestFoldOfACopyThatMovedIsNothing: a view of a copy a build replaced since, or of no revision past the copy's,
// folds nothing and writes nothing.
func TestFoldOfACopyThatMovedIsNothing(t *testing.T) {
	path, database := ordersFile(t, 1200)
	ctx := context.Background()
	copies := copiesOf(t, path)
	if _, err := copies.Build(ctx, readTransaction(t, path), "main", "orders", viewColumns); err != nil {
		t.Fatal(err)
	}
	table := copies.tableOf(t, "orders")
	same, err := table.View(ctx, readTransaction(t, path), "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copies.Fold(ctx, same); !errors.Is(err, errFoldMoved) {
		t.Fatalf("a view at the copy's own revision: %v", err)
	}
	write(t, database, `UPDATE orders SET amount = 1 WHERE id = 3`)
	view, err := table.View(ctx, readTransaction(t, path), "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copies.Build(ctx, readTransaction(t, path), "main", "orders", viewColumns); err != nil {
		t.Fatal(err)
	}
	before := copies.Info()
	if _, err := copies.Fold(ctx, view); !errors.Is(err, errFoldMoved) {
		t.Fatalf("a view of the copy before the build: %v", err)
	}
	if after := copies.Info(); after != before {
		t.Fatalf("a fold of nothing wrote: %+v after %+v", after, before)
	}
}

// TestRegistryDefersAFoldOfFewRowsSpreadOverTheBlocks: changed rows in every block, fewer than a 256th of the rows,
// are not folded (nothing written) until a view reads that many; then they are.
//
// CONTROL (05/11/2026): with spread rows folded as any, "a fold of 12 spread rows wrote 74542 bytes".
func TestRegistryDefersAFoldOfFewRowsSpreadOverTheBlocks(t *testing.T) {
	path, database := ordersFile(t, 5000) // 4,286 rows in 5 blocks: a 256th is 16 rows
	ctx := context.Background()
	registry := NewRegistry()
	registry.MinimumRows, registry.FoldRows = 1, 10
	defer registry.Close()
	if table, release := registry.Acquire(path, "orders", []string{"amount"}); table != nil {
		release()
		t.Fatal("a copy before any build")
	}
	registry.Wait()
	copies := registry.files[path].file
	view := func() *View {
		table, release := registry.Acquire(path, "orders", []string{"amount"})
		defer release()
		view, err := table.View(ctx, readTransaction(t, path), "main")
		if err != nil {
			t.Fatal(err)
		}
		registry.Changed(path, "orders", view)
		registry.Wait()
		return view
	}
	present := func(id int) int { // a multiple of 7 was deleted
		if id%7 == 0 {
			return id + 3
		}
		return id
	}
	for step := range 12 {
		write(t, database, `UPDATE orders SET amount = ? WHERE id = ?`, step, present(3*(1+step*400)))
	}
	before := copies.Info()
	if spread := view(); spread.Changed() != 12 {
		t.Fatalf("%d changed rows, want 12", spread.Changed())
	}
	if after := copies.Info(); after != before {
		t.Fatalf("a fold of 12 spread rows wrote %d bytes", after.FileBytes-before.FileBytes)
	}
	for step := range 6 {
		write(t, database, `UPDATE orders SET amount = ? WHERE id = ?`, step, present(3*(2+step*400)))
	}
	folded := view()
	table, release := registry.Acquire(path, "orders", []string{"amount"})
	defer release()
	if table.Manifest().Revision != folded.Revision {
		t.Fatalf("18 spread rows: the copy at revision %d, want %d", table.Manifest().Revision, folded.Revision)
	}
}

// TestRegistryFoldsWhatAViewReadsChanged: a view reading FoldRows changed rows or more has the registry fold the copy
// in the background; the registry's table then holds the rows at that revision, and changed rows after it alone.
//
// CONTROL (05/11/2026): with Changed ignoring the view, "after the fold the registry's copy is at revision 8, want 26".
func TestRegistryFoldsWhatAViewReadsChanged(t *testing.T) {
	path, database := ordersFile(t, 3000)
	ctx := context.Background()
	registry := NewRegistry()
	registry.MinimumRows, registry.FoldRows = 1, 10
	defer registry.Close()
	if table, release := registry.Acquire(path, "orders", []string{"amount"}); table != nil {
		release()
		t.Fatal("a copy before any build")
	}
	registry.Wait()
	for step := range 20 {
		write(t, database, `UPDATE orders SET amount = ? WHERE id = ?`, step, 3*(1+step*50))
	}
	table, release := registry.Acquire(path, "orders", []string{"amount"})
	if table == nil {
		t.Fatal("no copy after the build")
	}
	tx := readTransaction(t, path)
	view, err := table.View(ctx, tx, "main")
	if err != nil {
		t.Fatal(err)
	}
	registry.Changed(path, "orders", view)
	release()
	registry.Wait()
	folded, release := registry.Acquire(path, "orders", []string{"amount"})
	defer release()
	if folded == nil || folded.Manifest().Revision != view.Revision {
		revision := uint64(0)
		if folded != nil {
			revision = folded.Manifest().Revision
		}
		t.Fatalf("after the fold the registry's copy is at revision %d, want %d", revision, view.Revision)
	}
	again, err := folded.View(ctx, readTransaction(t, path), "main")
	if err != nil || again.Changed() != 0 {
		t.Fatalf("the folded copy's view: %v, %d changed rows", err, again.Changed())
	}
	if got, want := viewCells(t, again, []string{"amount"}), cells(t, tx, `SELECT _rowid_, amount FROM orders ORDER BY _rowid_`); !reflect.DeepEqual(got, want) {
		t.Fatal("the folded copy differs from SQLite")
	}
}
