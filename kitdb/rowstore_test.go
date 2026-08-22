package kitdb

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMainSnapshotUsesSparseIndexAndLazyPages(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "tenant.kitdb"))
	defer db.Close()
	seedRows(t, db, 1024, 1024)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	if db.main.records != 1024 {
		t.Fatalf("main records = %d, want 1024", db.main.records)
	}
	if len(db.main.blocks) <= 1 || len(db.main.blocks) >= int(db.main.records) {
		t.Fatalf("sparse blocks = %d for %d records", len(db.main.blocks), db.main.records)
	}
	if len(db.overlay) != 0 {
		t.Fatalf("overlay contains %d mutations after checkpoint", len(db.overlay))
	}
	if used, pages := db.main.cache.usage(); used != 0 || pages != 0 {
		t.Fatalf("new main cache = %d bytes in %d pages, want empty", used, pages)
	}

	if got := requireValue(t, db, rowKey(513)); len(got) != 1024 {
		t.Fatalf("lazy value length = %d, want 1024", len(got))
	}
	used, pages := db.main.cache.usage()
	if used <= 0 || used > defaultMainPageCacheBytes || pages != 1 {
		t.Fatalf("lazy cache = %d bytes in %d pages", used, pages)
	}
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.MainRecords != 1024 || stats.MainBlocks != len(db.main.blocks) || stats.CachedPages != 1 || stats.CachedBytes != used {
		t.Fatalf("Stats = %+v", stats)
	}
}

func TestMainPageCacheStaysWithinBudgetAndEvicts(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "tenant.kitdb"))
	defer db.Close()
	seedRows(t, db, 600, 2048)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if len(db.main.blocks) < 4 {
		t.Fatalf("main blocks = %d, want at least 4", len(db.main.blocks))
	}

	const budget = int64(160 << 10)
	db.main.cache = newRowPageCache(budget)
	for _, block := range db.main.blocks {
		value, found, err := db.Get(block.firstKey)
		if err != nil || !found || len(value) != 2048 {
			t.Fatalf("Get(%q) = (%d, %v, %v)", block.firstKey, len(value), found, err)
		}
	}
	used, pages := db.main.cache.usage()
	if used <= 0 || used > budget {
		t.Fatalf("cache used %d bytes with budget %d", used, budget)
	}
	if pages <= 0 || pages >= len(db.main.blocks) {
		t.Fatalf("cached pages = %d, blocks = %d; eviction did not occur", pages, len(db.main.blocks))
	}
}

func TestPersistedPageBoundsRejectGapMissWithoutDiskRead(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "tenant.kitdb"))
	defer db.Close()
	seedRows(t, db, 300, 16)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if len(db.main.blocks) < 3 || string(db.main.blocks[0].lastKey) != rowKey(127) {
		t.Fatalf("unexpected page bounds: pages=%d first-last=%q", len(db.main.blocks), db.main.blocks[0].lastKey)
	}
	db.main.cache = newRowPageCache(defaultMainPageCacheBytes)
	if _, found, err := db.Get([]byte(rowKey(127) + "x")); err != nil || found {
		t.Fatalf("gap Get = found %v, error %v", found, err)
	}
	if used, pages := db.main.cache.usage(); used != 0 || pages != 0 {
		t.Fatalf("gap miss read a page: %d bytes in %d pages", used, pages)
	}
}

func TestOpenOptionsControlPageCachePerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db, err := OpenWithOptions(path, OpenOptions{PageCacheBytes: 96 << 10})
	if err != nil {
		t.Fatal(err)
	}
	seedRows(t, db, 256, 1024)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if got := requireValue(t, db, rowKey(100)); len(got) != 1024 {
		t.Fatalf("value length = %d, want 1024", len(got))
	}
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.PageCacheLimitBytes != 96<<10 || stats.CachedBytes > 96<<10 {
		t.Fatalf("configured cache Stats = %+v", stats)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = OpenWithOptions(path, OpenOptions{PageCacheBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := requireValue(t, db, rowKey(100)); len(got) != 1024 {
		t.Fatalf("uncached value length = %d, want 1024", len(got))
	}
	stats, err = db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.PageCacheLimitBytes != 0 || stats.CachedBytes != 0 || stats.CachedPages != 0 {
		t.Fatalf("disabled cache Stats = %+v", stats)
	}
}

func TestWALOverlayBypassesMainAndCheckpointClearsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	seedNamedRows(t, db, map[string]string{
		"delete": "old-delete",
		"stable": "disk",
		"update": "old-update",
	})
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	db.main.cache = newRowPageCache(defaultMainPageCacheBytes)

	tx := mustBegin(t, db)
	if err := tx.Put([]byte("update"), []byte("new-update")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte("delete")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("insert"), []byte("new-insert")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(db.overlay) != 3 {
		t.Fatalf("overlay mutations = %d, want 3", len(db.overlay))
	}
	if got := requireValue(t, db, "update"); string(got) != "new-update" {
		t.Fatalf("updated value = %q", got)
	}
	if got := requireValue(t, db, "insert"); string(got) != "new-insert" {
		t.Fatalf("inserted value = %q", got)
	}
	if _, found, err := db.Get([]byte("delete")); err != nil || found {
		t.Fatalf("deleted value found=%v err=%v", found, err)
	}
	if used, pages := db.main.cache.usage(); used != 0 || pages != 0 {
		t.Fatalf("overlay reads touched main cache: %d bytes in %d pages", used, pages)
	}
	if got := requireValue(t, db, "stable"); string(got) != "disk" {
		t.Fatalf("stable value = %q", got)
	}
	if used, pages := db.main.cache.usage(); used == 0 || pages == 0 {
		t.Fatalf("main read did not populate cache: %d bytes in %d pages", used, pages)
	}

	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if len(db.overlay) != 0 || db.main.records != 3 {
		t.Fatalf("post-checkpoint overlay/records = %d/%d, want 0/3", len(db.overlay), db.main.records)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, path)
	defer db.Close()
	if got := requireValue(t, db, "update"); string(got) != "new-update" {
		t.Fatalf("reopened updated value = %q", got)
	}
	if _, found, err := db.Get([]byte("delete")); err != nil || found {
		t.Fatalf("reopened deleted value found=%v err=%v", found, err)
	}
}

func TestStreamingCheckpointMergesInsertUpdateDeleteInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	seedNamedRows(t, db, map[string]string{"a": "a1", "c": "c1", "e": "e1"})
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	tx := mustBegin(t, db)
	for key, value := range map[string]string{"b": "b2", "c": "c2", "d": "d2"} {
		if err := tx.Put([]byte(key), []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"a", "e", "missing"} {
		if err := tx.Delete([]byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	for key, want := range map[string]string{"b": "b2", "c": "c2", "d": "d2"} {
		if got := requireValue(t, db, key); string(got) != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"a", "e", "missing"} {
		if _, found, err := db.Get([]byte(key)); err != nil || found {
			t.Fatalf("%s found=%v err=%v", key, found, err)
		}
	}
	if db.main.records != 3 {
		t.Fatalf("merged main records = %d, want 3", db.main.records)
	}
}

func TestConcurrentMainReadsDuringCheckpointSwap(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "tenant.kitdb"))
	defer db.Close()
	seedRows(t, db, 512, 256)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	errorsSeen := make(chan error, 8)
	var readers sync.WaitGroup
	for reader := 0; reader < 8; reader++ {
		reader := reader
		readers.Add(1)
		go func() {
			defer readers.Done()
			position := reader
			for !stop.Load() {
				key := rowKey(position % 512)
				value, found, err := db.Get([]byte(key))
				if err != nil || !found || len(value) != 256 {
					errorsSeen <- fmt.Errorf("Get(%s) = (%d, %v, %v)", key, len(value), found, err)
					return
				}
				position += 17
			}
		}()
	}
	for generation := 0; generation < 8; generation++ {
		commitPut(t, db, fmt.Sprintf("tail/%02d", generation), "value")
		if _, err := db.Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	readers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
}

func seedRows(t *testing.T, db *DB, count, valueSize int) {
	t.Helper()
	value := bytes.Repeat([]byte{'v'}, valueSize)
	tx := mustBegin(t, db)
	for index := 0; index < count; index++ {
		if err := tx.Put([]byte(rowKey(index)), value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func seedNamedRows(t *testing.T, db *DB, rows map[string]string) {
	t.Helper()
	tx := mustBegin(t, db)
	for key, value := range rows {
		if err := tx.Put([]byte(key), []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func rowKey(index int) string {
	return fmt.Sprintf("row/%06d", index)
}
