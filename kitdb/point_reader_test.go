package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func pointReaderFixture(t *testing.T, size int) (*DB, *Snapshot, *PointReader) {
	t.Helper()
	db := mustOpen(t, filepath.Join(t.TempDir(), "reader.kitdb"))
	t.Cleanup(func() { _ = db.Close() })
	rows := 300
	if size > mainIndexBlockBytes {
		rows = 3
	}
	seedRows(t, db, rows, size)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	db.main.cache = newRowPageCache(0)
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = snapshot.Close() })
	reader, err := snapshot.PointReader()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	return db, snapshot, reader
}

func TestPointReaderOwnershipAndBounds(t *testing.T) {
	db, snapshot, reader := pointReaderFixture(t, 256)
	var retained [][]byte
	for _, id := range []int{0, 129, 299, 0} {
		key := []byte(rowKey(id))
		want, _, err := snapshot.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		got, found, stats, err := reader.GetWithStats(key)
		if err != nil || !found || !bytes.Equal(got, want) || stats.PagesRead != 1 || stats.PageCacheBypasses != 1 {
			t.Fatalf("id=%d value=%d found=%v stats=%+v error=%v", id, len(got), found, stats, err)
		}
		retained = append(retained, got)
		if cap(reader.page.data) > pointReaderPageBytes || cap(reader.page.rows) > mainIndexBlockRecords {
			t.Fatal("private scratch exceeded structural bounds")
		}
	}
	for i, id := range []int{0, 129, 299, 0} {
		want, _, _ := snapshot.Get([]byte(rowKey(id)))
		if !bytes.Equal(retained[i], want) {
			t.Fatal("later lookup changed returned value")
		}
	}
	retained[0][0] ^= 1
	got, _, _ := reader.Get([]byte(rowKey(0)))
	if bytes.Equal(retained[0], got) {
		t.Fatal("returned value aliases scratch")
	}
	if used, pages := db.main.cache.usage(); used != 0 || pages != 0 {
		t.Fatal("private page entered shared cache")
	}
	reader.Close()
	reader.Close()
	if reader.page.data != nil || reader.page.rows != nil || reader.snapshot != nil {
		t.Fatal("close retained scratch")
	}
	if _, _, err := reader.Get([]byte("missing")); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatal(err)
	}
}

func TestPointReaderSnapshotAndOverlay(t *testing.T) {
	db, old, reader := pointReaderFixture(t, 32)
	want, _, _ := reader.Get([]byte(rowKey(1)))
	commitPut(t, db, rowKey(1), "new")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte(rowKey(2))); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "overlay", "yes")
	current, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	r, err := current.PointReader()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, found, err := reader.Get([]byte(rowKey(1)))
	if err != nil || !found || !bytes.Equal(got, want) {
		t.Fatal("old generation changed", err)
	}
	for _, test := range []struct {
		key, value string
		found      bool
	}{
		{rowKey(1), "new", true}, {rowKey(2), "", false}, {"overlay", "yes", true}, {"missing", "", false},
	} {
		got, found, _, err := r.GetWithStats([]byte(test.key))
		if err != nil || found != test.found || string(got) != test.value || !found && got != nil {
			t.Fatalf("%s: %q %v %v", test.key, got, found, err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Get([]byte(rowKey(1))); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatal(err)
	}
}

func TestPointReaderLargePageAndWarmCache(t *testing.T) {
	_, snapshot, reader := pointReaderFixture(t, pointReaderPageBytes+1)
	got, found, err := reader.Get([]byte(rowKey(0)))
	if err != nil || !found || len(got) != pointReaderPageBytes+1 {
		t.Fatal(len(got), found, err)
	}
	if reader.page.data != nil || reader.page.rows != nil {
		t.Fatal("oversized page retained as scratch")
	}
	snapshot.main.cache = newRowPageCache(defaultMainPageCacheBytes)
	if _, _, err := snapshot.Get([]byte(rowKey(1))); err != nil {
		t.Fatal(err)
	}
	_, _, stats, err := reader.GetWithStats([]byte(rowKey(1)))
	if err != nil || stats.PageCacheHits != 1 || stats.PagesRead != 0 {
		t.Fatal(stats, err)
	}
}

func TestPointReaderPageTargetOvershoot(t *testing.T) {
	_, _, reader := pointReaderFixture(t, 4096)
	if _, found, err := reader.Get([]byte(rowKey(0))); err != nil || !found {
		t.Fatal(found, err)
	}
	if len(reader.page.data) <= mainIndexBlockBytes || cap(reader.page.data) > pointReaderPageBytes {
		t.Fatal("target-overshoot page did not use bounded scratch", len(reader.page.data))
	}
}

func TestPointReaderChecksumRevalidation(t *testing.T) {
	db, _, reader := pointReaderFixture(t, 32)
	key := []byte(rowKey(0))
	if _, _, err := reader.Get(key); err != nil {
		t.Fatal(err)
	}
	block := db.main.blocks[0]
	f, err := os.OpenFile(db.path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	original := make([]byte, 1)
	if _, err := f.ReadAt(original, block.offset); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{original[0] ^ 1}, block.offset); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.Get(key); err == nil {
		t.Fatal("corrupt page accepted")
	}
	if _, err := f.WriteAt(original, block.offset); err != nil {
		t.Fatal(err)
	}
	if _, found, err := reader.Get(key); err != nil || !found {
		t.Fatal("failed read poisoned scratch", found, err)
	}
}

func TestPointReaderConcurrentClose(t *testing.T) {
	_, snapshot, reader := pointReaderFixture(t, 32)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			for n := 0; n < 80; n++ {
				_, _, err := reader.Get([]byte(rowKey((n + id) % 300)))
				if err != nil && !errors.Is(err, ErrSnapshotClosed) {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	workers.Add(1)
	go func() { defer workers.Done(); _ = snapshot.Close(); reader.Close() }()
	workers.Wait()
}

func TestPointReaderAllocationReuse(t *testing.T) {
	_, snapshot, reader := pointReaderFixture(t, 256)
	key := []byte(rowKey(1))
	if _, _, err := reader.Get(key); err != nil {
		t.Fatal(err)
	}
	ordinary := testing.AllocsPerRun(10, func() {
		if _, _, err := snapshot.Get(key); err != nil {
			panic(err)
		}
	})
	private := testing.AllocsPerRun(10, func() {
		if _, _, err := reader.Get(key); err != nil {
			panic(err)
		}
	})
	if private >= ordinary {
		t.Fatal(fmt.Sprintf("private=%g ordinary=%g", private, ordinary))
	}
}

func TestPointReaderWarmsSharedCache(t *testing.T) {
	_, snapshot, reader := pointReaderFixture(t, 256)
	snapshot.main.cache = newRowPageCache(defaultMainPageCacheBytes)
	first := []byte(rowKey(0))
	second := []byte(rowKey(129))
	if _, _, err := reader.Get(first); err != nil {
		t.Fatal(err)
	}
	if reader.page.data != nil || !reader.warmed {
		t.Fatal("first miss did not warm shared cache")
	}
	if _, _, err := reader.Get(second); err != nil {
		t.Fatal(err)
	}
	if reader.page.data == nil {
		t.Fatal("later miss did not reuse private buffer")
	}
	reader.Close()
	next, err := snapshot.PointReader()
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if _, _, err := next.Get(second); err != nil {
		t.Fatal(err)
	}
	for _, key := range [][]byte{first, second} {
		_, found, stats, err := next.GetWithStats(key)
		if err != nil || !found || stats.PageCacheHits != 1 || stats.PagesRead != 0 {
			t.Fatal(stats, err)
		}
	}
}
