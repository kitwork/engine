package analytics

import (
	"context"
	"math/rand/v2"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/columnar"
)

// shoppingColumns are the columns of shopping the analytic queries of RESULTS_2026-10-27.md read.
var shoppingColumns = []string{"merchant", "id", "shop", "category", "price", "pricing", "discount", "sold", "stock",
	"review", "official"}

// TestMeasureFoldOnShopping measures folds of the column copy of shopping (KITWORK_KITDB.md mục 6; the numbers are in
// engine/benchmarks/kitdbsqlite/RESULTS_2026-11-05.md). It runs only when KITWORK_SHOPPING names shopping's kitwork.db,
// read-only, and KITWORK_ANALYTICS_COPIES a copies' file for it, built there first when it holds no copy of shopping.
// The database is never written, so the changes are made up rather than read from its history: updates at random
// rowids, rows inserted past the end, updates of the newest rows, deletes, each folded at a revision past the copy's as
// the registry folds after a view; the copy then holds the rows and the sum of sold expected. The file is compacted
// last.
func TestMeasureFoldOnShopping(t *testing.T) {
	database, copiesPath := os.Getenv("KITWORK_SHOPPING"), os.Getenv("KITWORK_ANALYTICS_COPIES")
	if database == "" || copiesPath == "" {
		t.Skip("KITWORK_SHOPPING and KITWORK_ANALYTICS_COPIES name shopping's kitwork.db and a copies' file for it")
	}
	ctx := context.Background()
	start := time.Now()
	copies, err := OpenFile(copiesPath)
	if err != nil {
		t.Fatal(err)
	}
	defer copies.Close()
	t.Logf("opened %s: %s", copiesPath, time.Since(start).Round(time.Millisecond))
	if !slices.Contains(copies.Tables(), "shopping") {
		tx := readTransaction(t, database)
		start := time.Now()
		manifest, err := copies.Build(ctx, tx, "main", "shopping", shoppingColumns)
		if err != nil {
			t.Fatal(err)
		}
		_ = tx.Rollback()
		t.Logf("built: %d rows, %d blocks, %d bytes, %s", manifest.Rows, manifest.Blocks, manifest.Bytes, time.Since(start).Round(time.Millisecond))
	}
	sold := slices.Index(shoppingColumns, "sold")

	// current reads the copy as it is: its manifest, every rowid, the sum of sold, and the rows of wanted, as images
	current := func(wanted map[int64]bool) (*Manifest, []int64, int64, map[int64][]any) {
		table, err := copies.Table("shopping")
		if err != nil || table == nil {
			t.Fatalf("table: %v", err)
		}
		defer table.Close()
		var rowIDs []int64
		var sum int64
		images := map[int64][]any{}
		_, err = table.copy.Scan(ctx, shoppingColumns, nil, func(batch *columnar.Batch) error {
			for row := range batch.Rows {
				rowID := batch.Columns[0].Integers[row]
				rowIDs = append(rowIDs, rowID)
				if batch.Columns[sold+1].Valid[row] != 0 {
					sum += batch.Columns[sold+1].Integers[row]
				}
				if !wanted[rowID] {
					continue
				}
				image := make([]any, len(shoppingColumns))
				for position := range shoppingColumns {
					vector := batch.Columns[position+1]
					if vector.Valid[row] == 0 {
						continue
					}
					switch vector.Field.Kind {
					case columnar.Float:
						image[position] = vector.Floats[row]
					case columnar.Text:
						image[position] = vector.Texts[row]
					default:
						image[position] = vector.Integers[row]
					}
				}
				images[rowID] = image
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return table.Manifest(), rowIDs, sum, images
	}

	random := rand.New(rand.NewPCG(5, 11))
	_, rowIDs, _, _ := current(nil)
	t.Logf("%-40s %10s %12s %8s %8s %10s", "fold", "took", "written", "blocks", "extents", "rows")
	for _, scenario := range []struct {
		name    string
		pick    func(rowIDs []int64) (updated []int64, inserted int, deleted []int64)
		inserts int
	}{
		{name: "5,000 updates at random rowids", pick: func(rowIDs []int64) ([]int64, int, []int64) {
			return sample(random, rowIDs, 5000), 0, nil
		}},
		{name: "5,000 rows inserted past the end", pick: func(rowIDs []int64) ([]int64, int, []int64) {
			return nil, 5000, nil
		}},
		{name: "1,000 updates of the newest 10,000 rows", pick: func(rowIDs []int64) ([]int64, int, []int64) {
			return sample(random, rowIDs[len(rowIDs)-10000:], 1000), 0, nil
		}},
		{name: "5,000 deletes at random rowids", pick: func(rowIDs []int64) ([]int64, int, []int64) {
			return nil, 0, sample(random, rowIDs, 5000)
		}},
		{name: "50,000 updates at random rowids", pick: func(rowIDs []int64) ([]int64, int, []int64) {
			return sample(random, rowIDs, 50000), 0, nil
		}},
	} {
		updated, inserted, deleted := scenario.pick(rowIDs)
		wanted := map[int64]bool{rowIDs[len(rowIDs)-1]: true}
		for _, rowID := range updated {
			wanted[rowID] = true
		}
		manifest, _, sumBefore, images := current(wanted)
		changes := &changed{revision: manifest.Revision + 1, images: map[int64][]any{}}
		delta, rows := int64(0), int64(manifest.Rows)
		for _, rowID := range updated {
			image := slices.Clone(images[rowID])
			if image[sold] == nil {
				image[sold] = int64(0)
			}
			image[sold] = image[sold].(int64) + 1
			changes.images[rowID] = image
			delta++
		}
		last := images[rowIDs[len(rowIDs)-1]]
		for position := range inserted {
			image := slices.Clone(last)
			image[sold] = int64(position)
			changes.images[manifest.MaxRowID+1+int64(position)] = image
			delta += int64(position)
			rows++
		}
		deletedSold := map[int64]bool{}
		for _, rowID := range deleted {
			changes.images[rowID] = nil
			deletedSold[rowID] = true
			rows--
		}
		changes.rowIDs = slices.Sorted(func(yield func(int64) bool) {
			for rowID := range changes.images {
				if !yield(rowID) {
					return
				}
			}
		})
		if len(deleted) > 0 {
			_, _, _, gone := current(deletedSold)
			for _, image := range gone {
				if image[sold] != nil {
					delta -= image[sold].(int64)
				}
			}
		}
		before := copies.Info().FileBytes
		start := time.Now()
		folded, err := copies.fold(ctx, "shopping", manifest.Section, changes, changes.revision, true)
		if err != nil {
			t.Fatalf("%s: %v", scenario.name, err)
		}
		took := time.Since(start)
		written := copies.Info().FileBytes - before
		after, afterRowIDs, sumAfter, _ := current(nil)
		if after.Rows != uint64(rows) || int64(len(afterRowIDs)) != rows || sumAfter != sumBefore+delta || folded.Revision != changes.revision {
			t.Fatalf("%s: %d rows (%d read), sum of sold %d; want %d rows, sum %d", scenario.name, after.Rows, len(afterRowIDs),
				sumAfter, rows, sumBefore+delta)
		}
		t.Logf("%-40s %10s %12d %8d %8d %10d", scenario.name, took.Round(time.Millisecond), written, folded.Blocks,
			copies.SectionExtents("shopping"), folded.Rows)
		rowIDs = afterRowIDs
	}
	info := copies.Info()
	start = time.Now()
	if err := copies.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("compaction: %d bytes (%d unread) to %d, %s", info.FileBytes, info.ObsoleteBytes, copies.Info().FileBytes,
		time.Since(start).Round(time.Millisecond))
}

// sample is count rowids of rowIDs at random, each once.
func sample(random *rand.Rand, rowIDs []int64, count int) []int64 {
	picked := map[int64]bool{}
	for len(picked) < count {
		picked[rowIDs[random.IntN(len(rowIDs))]] = true
	}
	return slices.Sorted(func(yield func(int64) bool) {
		for rowID := range picked {
			if !yield(rowID) {
				return
			}
		}
	})
}
