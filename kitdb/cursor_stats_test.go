package kitdb

import (
	"path/filepath"
	"testing"
)

func TestSnapshotGetWithStatsAuditsCacheAndOverlay(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "point.kitdb"))
	defer db.Close()
	seedRows(t, db, 300, 32)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	db.main.cache = newRowPageCache(defaultMainPageCacheBytes)

	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	value, found, cold, err := snapshot.GetWithStats([]byte(rowKey(100)))
	if err != nil || !found || len(value) != 32 {
		t.Fatalf("cold GetWithStats = (%d, %v, %+v, %v)", len(value), found, cold, err)
	}
	firstBlock := db.main.blocks[0]
	if cold.PageAccesses != 1 || cold.PageCacheMisses != 1 || cold.PageCacheHits != 0 ||
		cold.PageCacheBypasses != 0 || cold.PagesRead != 1 ||
		cold.PageBytesRead != uint64(firstBlock.length) ||
		cold.PageRecordsDecoded != uint64(firstBlock.records) ||
		cold.GenerationEntriesVisited != 0 || cold.OverlayEntriesVisited != 0 {
		t.Fatalf("cold stats = %+v", cold)
	}

	_, found, warm, err := snapshot.GetWithStats([]byte(rowKey(100)))
	if err != nil || !found {
		t.Fatalf("warm GetWithStats = (%v, %+v, %v)", found, warm, err)
	}
	if warm.PageAccesses != 1 || warm.PageCacheHits != 1 || warm.PageCacheMisses != 0 ||
		warm.PageCacheBypasses != 0 || warm.PagesRead != 0 || warm.PageBytesRead != 0 ||
		warm.PageRecordsDecoded != 0 {
		t.Fatalf("warm stats = %+v", warm)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}

	db.main.cache = newRowPageCache(0)
	snapshot, err = db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	_, found, bypassed, err := snapshot.GetWithStats([]byte(rowKey(100)))
	if err != nil || !found {
		t.Fatalf("uncached GetWithStats = (%v, %+v, %v)", found, bypassed, err)
	}
	if bypassed.PageAccesses != 1 || bypassed.PageCacheBypasses != 1 ||
		bypassed.PageCacheHits != 0 || bypassed.PageCacheMisses != 0 || bypassed.PagesRead != 1 {
		t.Fatalf("uncached stats = %+v", bypassed)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}

	commitPut(t, db, "overlay", "value")
	snapshot, err = db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	_, found, overlay, err := snapshot.GetWithStats([]byte("overlay"))
	if err != nil || !found {
		t.Fatalf("overlay GetWithStats = (%v, %+v, %v)", found, overlay, err)
	}
	if overlay.OverlayEntriesVisited != 1 || overlay.PageAccesses != 0 || overlay.PagesRead != 0 {
		t.Fatalf("overlay stats = %+v", overlay)
	}
}

func TestSnapshotCursorStatsAuditForwardReverseAndCacheBypass(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "cursor.kitdb"))
	defer db.Close()
	seedRows(t, db, 300, 32)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	db.main.cache = newRowPageCache(defaultMainPageCacheBytes)
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()

	cursor, err := snapshot.Cursor(RangeOptions{Prefix: []byte("row/"), Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for cursor.Next() {
		rows++
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	limited := cursor.Stats()
	if rows != 5 || limited.PageAccesses != 1 || limited.PagesRead != 1 ||
		limited.PageCacheBypasses != 1 || limited.PageCacheHits != 0 ||
		limited.PageCacheMisses != 0 || limited.PageRecordsDecoded != 128 ||
		limited.GenerationEntriesVisited != 7 || limited.OverlayEntriesVisited != 0 {
		t.Fatalf("limited cursor rows/stats = %d/%+v", rows, limited)
	}
	if err := cursor.Close(); err != nil {
		t.Fatal(err)
	}
	if used, pages := db.main.cache.usage(); used != 0 || pages != 0 {
		t.Fatalf("range cursor polluted point cache: %d bytes in %d pages", used, pages)
	}
	for _, test := range []struct {
		name           string
		options        RangeOptions
		pages          uint64
		recordsDecoded uint64
	}{
		{name: "forward-seek", options: RangeOptions{Start: []byte(rowKey(130)), Limit: 5}, pages: 1, recordsDecoded: 128},
		{name: "reverse-seek", options: RangeOptions{End: []byte(rowKey(130)), Limit: 5, Reverse: true}, pages: 2, recordsDecoded: 256},
	} {
		cursor, err = snapshot.Cursor(test.options)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		rows = 0
		for cursor.Next() {
			rows++
		}
		if err := cursor.Err(); err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		seek := cursor.Stats()
		if rows != 5 || seek.PagesRead != test.pages || seek.PageCacheBypasses != test.pages ||
			seek.PageRecordsDecoded != test.recordsDecoded || seek.GenerationEntriesVisited != 7 {
			t.Fatalf("%s rows/stats = %d/%+v", test.name, rows, seek)
		}
		if err := cursor.Close(); err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
	}

	cursor, err = snapshot.Cursor(RangeOptions{Prefix: []byte("row/"), Reverse: true})
	if err != nil {
		t.Fatal(err)
	}
	rows = 0
	for cursor.Next() {
		rows++
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	reverse := cursor.Stats()
	var bytesRead, records uint64
	for _, block := range db.main.blocks {
		bytesRead += uint64(block.length)
		records += uint64(block.records)
	}
	if rows != 300 || reverse.PageAccesses != uint64(len(db.main.blocks)) ||
		reverse.PagesRead != uint64(len(db.main.blocks)) ||
		reverse.PageCacheBypasses != uint64(len(db.main.blocks)) ||
		reverse.PageBytesRead != bytesRead || reverse.PageRecordsDecoded != records ||
		reverse.GenerationEntriesVisited != records {
		t.Fatalf("reverse cursor rows/stats = %d/%+v", rows, reverse)
	}
	if err := cursor.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotCursorStatsCountImmutableGenerationEntries(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "generations.kitdb"))
	defer db.Close()
	seedRows(t, db, 3, 16)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, rowKey(1), "updated")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if len(db.main.segments) < 2 {
		t.Fatalf("main segments = %d, want at least 2", len(db.main.segments))
	}

	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(RangeOptions{Prefix: []byte("row/")})
	if err != nil {
		t.Fatal(err)
	}
	logicalRows := 0
	for cursor.Next() {
		logicalRows++
	}
	if err := cursor.Err(); err != nil {
		t.Fatal(err)
	}
	stats := cursor.Stats()
	var physicalEntries uint64
	for _, block := range db.main.blocks {
		physicalEntries += uint64(block.records)
	}
	if logicalRows != 3 || physicalEntries != 4 ||
		stats.GenerationEntriesVisited != physicalEntries ||
		stats.PageRecordsDecoded != physicalEntries ||
		stats.PagesRead != uint64(len(db.main.blocks)) {
		t.Fatalf("generation cursor logical/physical/stats = %d/%d/%+v", logicalRows, physicalEntries, stats)
	}
}
