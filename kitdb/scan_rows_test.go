package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSnapshotScanMatchesCursorAcrossGenerations(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "scan.kitdb"))
	defer db.Close()
	for generation := range 3 {
		tx := mustBegin(t, db)
		for i := range 600 {
			if generation != 0 && i%7 != 0 {
				continue
			}
			key := []byte(fmt.Sprintf("row/%04d", i))
			var err error
			if generation != 0 && i%11 == 0 {
				err = tx.Delete(key)
			} else {
				value := bytes.Repeat([]byte(fmt.Sprintf("g%d-r%d;", generation, i)), 40+i%200)
				err = tx.Put(key, value)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Put([]byte(fmt.Sprintf("row/060%d", generation)), []byte("new row")); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if generation < 2 {
			if _, err := db.Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for _, options := range []RangeOptions{
		{}, {Prefix: []byte("row/")},
		{Start: []byte("row/0100"), End: []byte("row/0400"), Limit: 71},
		{Prefix: []byte("row/05"), Reverse: true, Limit: 37},
		{Start: []byte("row/0020"), End: []byte("row/0020")},
		{Prefix: []byte("missing/")},
	} {
		cursor, err := snapshot.Cursor(options)
		if err != nil {
			t.Fatal(err)
		}
		var want, got [][2]string
		for cursor.Next() {
			want = append(want, [2]string{string(cursor.Key()), string(cursor.Value())})
		}
		if err := cursor.Err(); err != nil {
			t.Fatal(err)
		}
		cursor.Close()
		stats, err := snapshot.Scan(options, func(key, value []byte) (bool, error) {
			got = append(got, [2]string{string(key), string(value)})
			return false, nil
		})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("scan options=%+v rows=%d want=%d err=%v", options, len(got), len(want), err)
		}
		if len(options.Prefix) == 0 && options.Start == nil && stats.PagesRead < 2 {
			t.Fatalf("test did not cross page boundaries: %+v", stats)
		}
	}
	// A live commit and checkpoint do not refresh the captured snapshot.
	tx := mustBegin(t, db)
	if err := tx.Put([]byte("row/0001"), []byte("later")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	oldRows := 0
	_, err = snapshot.Scan(RangeOptions{Start: []byte("row/0001"), Limit: 1}, func(key, value []byte) (bool, error) {
		oldRows++
		if string(key) != "row/0001" || string(value) == "later" {
			t.Fatal("scan changed snapshot")
		}
		return false, nil
	})
	if err != nil || oldRows != 1 {
		t.Fatalf("old snapshot rows=%d error=%v", oldRows, err)
	}
}

func TestSnapshotScanLegacyMain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-scan.kitdb")
	rows := make(map[string]string)
	for i := range 300 {
		rows[fmt.Sprintf("row/%04d", i)] = fmt.Sprintf("value/%04d", i)
	}
	writeLegacyMainForTest(t, path, rows)
	db := mustOpen(t, path)
	defer db.Close()
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	count := 0
	_, err = snapshot.Scan(RangeOptions{}, func(key, value []byte) (bool, error) {
		if want, found := rows[string(key)]; !found || string(value) != want {
			t.Fatalf("legacy row %q=%q", key, value)
		}
		count++
		return false, nil
	})
	if err != nil || count != len(rows) {
		t.Fatalf("legacy rows=%d error=%v", count, err)
	}
}

func TestSnapshotScanVisitorAndClose(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "visitor.kitdb"))
	defer db.Close()
	tx := mustBegin(t, db)
	for _, key := range []string{"a", "b"} {
		if err := tx.Put([]byte(key), []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	sentinel := errors.New("visitor failed")
	if _, err := snapshot.Scan(RangeOptions{}, func(_, _ []byte) (bool, error) { return false, sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	calls := 0
	if _, err := snapshot.Scan(RangeOptions{}, func(_, _ []byte) (bool, error) { calls++; return true, nil }); err != nil || calls != 1 {
		t.Fatalf("early stop calls=%d error=%v", calls, err)
	}
	if _, err := snapshot.Scan(RangeOptions{}, nil); err == nil {
		t.Fatal("nil visitor accepted")
	}
	if _, err := snapshot.Scan(RangeOptions{Limit: -1}, func(_, _ []byte) (bool, error) { return false, nil }); err == nil {
		t.Fatal("negative limit accepted")
	}
	if _, err := (*Snapshot)(nil).Scan(RangeOptions{}, func(_, _ []byte) (bool, error) { return false, nil }); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatal(err)
	}
	_, err = snapshot.Scan(RangeOptions{}, func(_, _ []byte) (bool, error) { return false, snapshot.Close() })
	if !errors.Is(err, ErrSnapshotClosed) {
		t.Fatalf("callback close error=%v", err)
	}
}

func TestSnapshotScanStillChecksPageChecksum(t *testing.T) {
	for _, block := range []int{0, 1} {
		t.Run(fmt.Sprintf("block_%d", block), func(t *testing.T) {
			testSnapshotScanPageChecksum(t, block)
		})
	}
}

func testSnapshotScanPageChecksum(t *testing.T, block int) {
	path := filepath.Join(t.TempDir(), "corrupt-scan.kitdb")
	db := mustOpen(t, path)
	defer db.Close()
	tx := mustBegin(t, db)
	for i := range 300 {
		if err := tx.Put([]byte(fmt.Sprintf("row/%04d", i)), bytes.Repeat([]byte{'x'}, 512)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if len(snapshot.main.blocks) <= block {
		t.Fatal("test did not create enough pages")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteAt([]byte{0xff}, snapshot.main.blocks[block].offset)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	visited := 0
	if _, err := snapshot.Scan(RangeOptions{}, func(_, _ []byte) (bool, error) { visited++; return false, nil }); err == nil {
		t.Fatal("corrupt page accepted")
	}
	if block > 0 && visited == 0 {
		t.Fatal("test did not visit a valid page before corruption")
	}
}

func TestSnapshotScanConcurrentClose(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "close-scan.kitdb"))
	defer db.Close()
	tx := mustBegin(t, db)
	for _, key := range []string{"a", "b"} {
		if err := tx.Put([]byte(key), []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	entered, resume := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := snapshot.Scan(RangeOptions{}, func(_, _ []byte) (bool, error) {
			close(entered)
			<-resume
			return false, nil
		})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(resume)
		t.Fatal("scan did not reach callback")
	}
	closeErr := snapshot.Close()
	close(resume)
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrSnapshotClosed) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not release after close")
	}
}

func TestSnapshotScanAllocationEnvelope(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "allocation.kitdb"))
	defer db.Close()
	tx := mustBegin(t, db)
	for i := range 2048 {
		if err := tx.Put([]byte(fmt.Sprintf("row/%08d", i)), bytes.Repeat([]byte{'x'}, 512)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	allocations := testing.AllocsPerRun(3, func() {
		count := 0
		_, err := snapshot.Scan(RangeOptions{Prefix: []byte("row/")}, func(_, value []byte) (bool, error) {
			if len(value) != 512 || value[0] != 'x' {
				t.Fatal("bad scan value")
			}
			count++
			return false, nil
		})
		if err != nil || count != 2048 {
			t.Fatalf("count=%d error=%v", count, err)
		}
	})
	if allocations > 100 {
		t.Fatalf("scan allocates per row/page: %.0f allocations", allocations)
	}
	t.Logf("2048-row scan allocations: %.0f (limit 100)", allocations)
}
