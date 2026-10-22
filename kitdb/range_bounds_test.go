package kitdb

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func rangeOracle(model map[string]string, options RangeOptions) []string {
	var keys []string
	for key := range model {
		if len(options.Start) > 0 && key < string(options.Start) || len(options.End) > 0 && key >= string(options.End) || !bytes.HasPrefix([]byte(key), options.Prefix) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if options.Reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	}
	if options.Limit > 0 && len(keys) > options.Limit {
		keys = keys[:options.Limit]
	}
	return keys
}

func checkRangeMethods(t *testing.T, snapshot *Snapshot, model map[string]string, options RangeOptions) {
	t.Helper()
	want := rangeOracle(model, options)
	for _, method := range []string{"cursor", "rows", "keys"} {
		var got []string
		visit := func(key, value []byte) (bool, error) {
			got = append(got, string(key))
			if method != "keys" && string(value) != model[string(key)] {
				t.Fatalf("%s changed value for %q", method, key)
			}
			return false, nil
		}
		var err error
		switch method {
		case "cursor":
			cursor, createErr := snapshot.Cursor(options)
			if createErr != nil {
				t.Fatal(createErr)
			}
			for cursor.Next() {
				visit(cursor.Key(), cursor.Value())
			}
			err = cursor.Err()
			cursor.Close()
		case "rows":
			_, err = snapshot.Scan(options, visit)
		case "keys":
			_, err = snapshot.ScanKeys(options, func(key []byte) (bool, error) { return visit(key, nil) })
		}
		if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s options=%+v got=%v want=%v: %v", method, options, got, want, err)
		}
	}
}

func TestBoundedRangeMatrix(t *testing.T) {
	db := seedRangeBounds(t)
	model := make(map[string]string)
	for _, group := range []string{"a/", "m/", "z/"} {
		for id := 0; id < 200; id++ {
			model[fmt.Sprintf("%s%04d", group, id)] = string(bytes.Repeat([]byte{7}, 512))
		}
	}
	old, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	tx := mustBegin(t, db)
	if err := tx.Delete([]byte("m/0010")); err != nil {
		t.Fatal(err)
	}
	changes := map[string]string{"m/0200": "new", "m/0011": "replaced", "n/0001": "overlay-only", string([]byte{255, 255, 1}): "ff"}
	for key, value := range changes {
		if err := tx.Put([]byte(key), []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	current, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	newModel := make(map[string]string, len(model)+len(changes))
	for key, value := range model {
		newModel[key] = value
	}
	delete(newModel, "m/0010")
	for key, value := range changes {
		newModel[key] = value
	}
	// Replace the live generation while both readers retain their old view.
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	options := []RangeOptions{
		{}, {Prefix: []byte("m/")}, {Start: []byte("m/0050"), End: []byte("m/0060")},
		{End: []byte("m/")}, {Start: []byte("m/")}, {Start: []byte("n/"), End: []byte("p/")},
		{Prefix: []byte{255}}, {Prefix: []byte{255, 255}},
		{Prefix: []byte("m/"), Start: []byte("m/0050"), End: []byte("m/0060")},
		{Prefix: []byte("m/"), Start: []byte("z/")},
		{Start: []byte("m/9999"), End: []byte("n/")},
		{Start: []byte("m/0050"), End: []byte("m/0050")}, {Start: []byte("p/"), End: []byte("n/")},
	}
	for _, view := range []struct {
		name     string
		snapshot *Snapshot
		model    map[string]string
	}{{"old", old, model}, {"wal", current, newModel}} {
		t.Run(view.name, func(t *testing.T) {
			for _, option := range options {
				for _, reverse := range []bool{false, true} {
					for _, limit := range []int{0, 1, 9, 250} {
						option.Reverse, option.Limit = reverse, limit
						checkRangeMethods(t, view.snapshot, view.model, option)
					}
				}
			}
		})
	}
}

func TestBoundedRangeFencesAvoidUnselectedPages(t *testing.T) {
	db := seedRangeBounds(t)
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for _, reverse := range []bool{false, true} {
		for _, method := range []string{"cursor", "rows", "keys"} {
			options := RangeOptions{Start: []byte("n/"), End: []byte("p/"), Reverse: reverse}
			var stats CursorStats
			switch method {
			case "cursor":
				cursor, err := snapshot.Cursor(options)
				if err != nil {
					t.Fatal(err)
				}
				if cursor.Next() || cursor.Err() != nil {
					t.Fatal("gap returned rows or error")
				}
				stats = cursor.Stats()
				cursor.Close()
			case "rows":
				stats, err = snapshot.Scan(options, func([]byte, []byte) (bool, error) { t.Fatal("gap returned row"); return true, nil })
			case "keys":
				stats, err = snapshot.ScanKeys(options, func([]byte) (bool, error) { t.Fatal("gap returned key"); return true, nil })
			}
			if err != nil || stats.PagesRead != 0 {
				t.Fatalf("%s reverse=%t read excluded pages: %+v %v", method, reverse, stats, err)
			}
		}
	}
	// Corruption outside a bounded query remains observable by Verify and by
	// queries selecting that page; pruning never weakens selected-page checks.
	file, err := os.OpenFile(db.path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range db.main.blocks {
		if block.firstKey[0] == 'a' || block.firstKey[0] == 'z' {
			if _, err := file.WriteAt([]byte{0xff}, block.offset); err != nil {
				file.Close()
				t.Fatal(err)
			}
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, reverse := range []bool{false, true} {
		count := 0
		stats, err := snapshot.Scan(RangeOptions{Prefix: []byte("m/"), Reverse: reverse}, func([]byte, []byte) (bool, error) { count++; return false, nil })
		if err != nil || count != 200 || stats.PagesRead != 16 {
			t.Fatalf("bounded scan count=%d stats=%+v: %v", count, stats, err)
		}
	}
	if err := db.Verify(); err == nil {
		t.Fatal("Verify ignored damaged pages")
	}
	if _, err := snapshot.Scan(RangeOptions{Prefix: []byte("z/")}, func([]byte, []byte) (bool, error) { return false, nil }); err == nil {
		t.Fatal("selected corrupt page accepted")
	}
}

func TestBoundedRangeLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.kitdb")
	model := map[string]string{"a": "one", "middle": "two", "z": "three"}
	writeLegacyMainForTest(t, path, model)
	db := mustOpen(t, path)
	defer db.Close()
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for _, reverse := range []bool{false, true} {
		for _, options := range []RangeOptions{{End: []byte("middle")}, {Start: []byte("middle"), End: []byte("z")}, {Prefix: []byte("mid")}, {Start: []byte("n"), End: []byte("p")}} {
			options.Reverse = reverse
			checkRangeMethods(t, snapshot, model, options)
		}
	}
}

func TestBoundedRangeExactPageEdges(t *testing.T) {
	db := seedRangeBounds(t)
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	first := 0
	for db.main.blocks[first].firstKey[0] != 'm' {
		first++
	}
	end := db.main.blocks[first+1].firstKey
	start := db.main.blocks[first].lastKey
	for _, reverse := range []bool{false, true} {
		for _, lower := range [][]byte{[]byte("m/"), start} {
			count := 0
			stats, err := snapshot.Scan(RangeOptions{Start: lower, End: end, Reverse: reverse}, func(key, value []byte) (bool, error) {
				if bytes.Compare(key, lower) < 0 || bytes.Compare(key, end) >= 0 {
					t.Fatal("range edge escaped")
				}
				count++
				return false, nil
			})
			want := int(db.main.blocks[first].records)
			if bytes.Equal(lower, start) {
				want = 1
			}
			if err != nil || count != want || stats.PagesRead != 8 {
				t.Fatalf("reverse=%t lower=%q count=%d want=%d stats=%+v: %v", reverse, lower, count, want, stats, err)
			}
		}
	}
}

func TestBoundedRangeFrozenV3(t *testing.T) {
	compressed, err := base64.StdEncoding.DecodeString(kitDBV1MainFixture)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	path := filepath.Join(t.TempDir(), "frozen.kitdb")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	db := mustOpen(t, path)
	defer db.Close()
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	model := map[string]string{"alpha": "one", "middle": "two", "zulu": "three"}
	for _, reverse := range []bool{false, true} {
		for _, options := range []RangeOptions{{End: []byte("middle")}, {Start: []byte("middle"), End: []byte("zulu")}, {Prefix: []byte("mid")}, {Start: []byte("n"), End: []byte("p")}, {Start: []byte("zulu")}} {
			options.Reverse = reverse
			checkRangeMethods(t, snapshot, model, options)
		}
	}
}

func TestBoundedRangeLimitAfterTombstones(t *testing.T) {
	db := seedRangeBounds(t)
	tx := mustBegin(t, db)
	model := make(map[string]string)
	for id := 0; id < 200; id++ {
		key := fmt.Sprintf("m/%04d", id)
		if id < 100 || id >= 190 {
			if err := tx.Delete([]byte(key)); err != nil {
				t.Fatal(err)
			}
		} else {
			model[key] = string(bytes.Repeat([]byte{7}, 512))
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
	for _, reverse := range []bool{false, true} {
		checkRangeMethods(t, snapshot, model, RangeOptions{Prefix: []byte("m/"), Limit: 5, Reverse: reverse})
	}
}

func seedRangeBounds(tb testing.TB) *DB {
	tb.Helper()
	db, err := OpenWithOptions(filepath.Join(tb.TempDir(), "range.kitdb"), OpenOptions{PageCacheBytes: -1})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := db.Close(); err != nil {
			tb.Error(err)
		}
	})
	for generation := 0; generation < 8; generation++ {
		tx, err := db.Begin()
		if err != nil {
			tb.Fatal(err)
		}
		for _, group := range []string{"a/", "m/", "z/"} {
			for id := 0; id < 200; id++ {
				if err := tx.Put([]byte(fmt.Sprintf("%s%04d", group, id)), bytes.Repeat([]byte{byte(generation)}, 512)); err != nil {
					tb.Fatal(err)
				}
			}
		}
		if _, err := tx.Commit(); err != nil {
			tb.Fatal(err)
		}
		if _, err := db.Checkpoint(); err != nil {
			tb.Fatal(err)
		}
	}
	return db
}

func BenchmarkBoundedRanges(b *testing.B) {
	db := seedRangeBounds(b)
	snapshot, err := db.Snapshot()
	if err != nil {
		b.Fatal(err)
	}
	defer snapshot.Close()
	for _, method := range []string{"cursor", "rows", "keys"} {
		for _, reverse := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				b.Run(fmt.Sprintf("%s/reverse_%t/empty_%t", method, reverse, empty), func(b *testing.B) {
					options := RangeOptions{Prefix: []byte("m/"), Reverse: reverse}
					want := 200
					if empty {
						options = RangeOptions{Start: []byte("n/"), End: []byte("p/"), Reverse: reverse}
						want = 0
					}
					var pages, pageBytes uint64
					b.ReportAllocs()
					for range b.N {
						count := 0
						var stats CursorStats
						var err error
						switch method {
						case "cursor":
							cursor, createErr := snapshot.Cursor(options)
							if createErr != nil {
								b.Fatal(createErr)
							}
							for cursor.Next() {
								count++
							}
							stats, err = cursor.Stats(), cursor.Err()
							cursor.Close()
						case "rows":
							stats, err = snapshot.Scan(options, func(key, value []byte) (bool, error) { count++; return false, nil })
						case "keys":
							stats, err = snapshot.ScanKeys(options, func(key []byte) (bool, error) { count++; return false, nil })
						}
						if err != nil || count != want {
							b.Fatalf("count=%d want=%d: %v", count, want, err)
						}
						pages += stats.PagesRead
						pageBytes += stats.PageBytesRead
					}
					b.ReportMetric(float64(pages)/float64(b.N), "pages/op")
					b.ReportMetric(float64(pageBytes)/float64(b.N), "page-bytes/op")
				})
			}
		}
	}
}
