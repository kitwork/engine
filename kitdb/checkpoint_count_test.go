package kitdb

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Retained pre-optimization algorithm, used only as an independent point-lookup
// oracle and for paired allocation measurements on the exact same generations.
func referenceMergedCount(main *mainImage, overlay map[string]rowMutation) (uint64, error) {
	count := main.records
	for key, mutation := range overlay {
		_, exists, err := main.get([]byte(key))
		if err != nil {
			return 0, err
		}
		if mutation.deleted && exists {
			count--
		}
		if !mutation.deleted && !exists {
			if count == math.MaxUint64 {
				return 0, fmt.Errorf("overflow")
			}
			count++
		}
	}
	return count, nil
}

func TestCheckpointCountScratchVisibility(t *testing.T) {
	for _, cache := range []int64{-1, 256 << 10} {
		t.Run(fmt.Sprint(cache), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "count.kitdb")
			db, err := OpenWithOptions(path, OpenOptions{PageCacheBytes: cache})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			model := make(map[string]bool)
			for generation := 0; generation < 35; generation++ {
				tx := mustBegin(t, db)
				for i := 0; i < 300; i++ {
					key := fmt.Sprintf("row/%04d", i)
					if (i+generation)%5 == 0 {
						err = tx.Delete([]byte(key))
						delete(model, key)
					} else {
						err = tx.Put([]byte(key), bytes.Repeat([]byte{byte(generation)}, 512))
						model[key] = true
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				got, err := countMergedRecords(db.main, db.overlay)
				want, refErr := referenceMergedCount(db.main, db.overlay)
				if err != nil || refErr != nil || got != want || got != uint64(len(model)) {
					t.Fatalf("generation %d: %d want %d model %d: %v/%v", generation, got, want, len(model), err, refErr)
				}
				if _, err = db.Checkpoint(); err != nil {
					t.Fatal(err)
				}
				if db.main.records != got {
					t.Fatal("checkpoint count changed")
				}
			}
			if err = db.Verify(); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = OpenWithOptions(path, OpenOptions{PageCacheBytes: cache})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if db.main.records != uint64(len(model)) {
				t.Fatal("reopen count changed")
			}
		})
	}
}

func TestCheckpointCountLegacyAndChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.kitdb")
	writeLegacyMainForTest(t, path, map[string]string{"a": "one", "b": "two"})
	db := mustOpen(t, path)
	defer db.Close()
	got, err := countMergedRecords(db.main, map[string]rowMutation{"a": {deleted: true}, "c": {value: []byte("three")}})
	if err != nil || got != 2 {
		t.Fatalf("legacy count=%d: %v", got, err)
	}
	if _, err = db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	db.main.cache = newRowPageCache(0)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteAt([]byte{0xff}, db.main.blocks[0].offset)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	if _, err = countMergedRecords(db.main, map[string]rowMutation{"a": {value: []byte("new")}}); err == nil {
		t.Fatal("corrupt page accepted")
	}
}

func TestCheckpointCountScratchAllocationBudget(t *testing.T) {
	db, err := OpenWithOptions(filepath.Join(t.TempDir(), "budget.kitdb"), OpenOptions{PageCacheBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRows(t, db, 2048, 512)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	overlay := make(map[string]rowMutation)
	for index := 0; index < 2048; index += 2 {
		overlay[rowKey(index)] = rowMutation{value: []byte("updated")}
	}
	var count uint64
	allocations := testing.AllocsPerRun(3, func() {
		count, err = countMergedRecords(db.main, overlay)
	})
	if err != nil || count != 2048 {
		t.Fatalf("count=%d: %v", count, err)
	}
	if allocations > 32 {
		t.Fatalf("existence probes allocated %.0f objects; must reuse one page, not copy each payload", allocations)
	}
}

func BenchmarkCheckpointCountProbes(b *testing.B) {
	db, err := OpenWithOptions(filepath.Join(b.TempDir(), "count.kitdb"), OpenOptions{PageCacheBytes: 256 << 10})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	for generation := 0; generation < 8; generation++ {
		tx, err := db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		for i := 0; i < 2048; i++ {
			if generation != 0 && i%8 != generation%8 {
				continue
			}
			if err = tx.Put([]byte(fmt.Sprintf("row/%04d", i)), bytes.Repeat([]byte{'v'}, 512)); err != nil {
				b.Fatal(err)
			}
		}
		if _, err = tx.Commit(); err != nil {
			b.Fatal(err)
		}
		if _, err = db.Checkpoint(); err != nil {
			b.Fatal(err)
		}
	}
	overlay := make(map[string]rowMutation)
	for i := 0; i < 2048; i += 2 {
		overlay[fmt.Sprintf("row/%04d", i)] = rowMutation{value: []byte("new")}
	}
	for _, algorithm := range []struct {
		name  string
		count func(*mainImage, map[string]rowMutation) (uint64, error)
	}{{"reference", referenceMergedCount}, {"ordered_scratch", countMergedRecords}} {
		b.Run(algorithm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got, err := algorithm.count(db.main, overlay)
				if err != nil || got != 2048 {
					b.Fatalf("count %d: %v", got, err)
				}
			}
		})
	}
}
