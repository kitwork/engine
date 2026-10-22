package kitdb

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerationPagePrefixFences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefix.kitdb")
	db, err := OpenWithOptions(path, OpenOptions{PageCacheBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for generation := 0; generation < 35; generation++ {
		tx := mustBegin(t, db)
		for _, prefix := range []byte{0, 2, 16, 255} {
			for id := 0; id < 150; id++ {
				key := append([]byte{prefix}, []byte(fmt.Sprintf("/%04d", id))...)
				if generation%3 == 1 && id%7 == 0 {
					err = tx.Delete(key)
				} else {
					err = tx.Put(key, bytes.Repeat([]byte{byte(generation)}, 512))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if _, err = db.CheckpointContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, block := range db.main.blocks {
			if block.firstKey[0] != block.lastKey[0] {
				t.Fatalf("mixed prefix page: %x..%x", block.firstKey, block.lastKey)
			}
			if block.records > mainIndexBlockRecords {
				t.Fatal("record ceiling changed")
			}
		}
		snapshot, err := db.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, prefix := range []byte{3, 15, 17, 254} {
			_, found, stats, err := snapshot.GetWithStats([]byte{prefix, '/', 'x'})
			if err != nil || found || stats.PagesRead != 0 {
				t.Fatalf("unrelated prefix %d: found=%t stats=%+v err=%v", prefix, found, stats, err)
			}
		}
		if err := snapshot.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenWithOptions(path, OpenOptions{PageCacheBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Verify(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for _, prefix := range []byte{0, 2, 16, 255} {
		key := append([]byte{prefix}, []byte("/0001")...)
		value, found, err := snapshot.Get(key)
		if err != nil || !found || !bytes.Equal(value, bytes.Repeat([]byte{34}, 512)) {
			t.Fatalf("reopen %x found=%t: %v", key, found, err)
		}
		// The newest generation's tombstone must stop an older live value.
		key = append([]byte{prefix}, []byte("/0000")...)
		if _, found, err := snapshot.Get(key); err != nil || found {
			t.Fatalf("tombstone %x found=%t: %v", key, found, err)
		}
	}
}

func TestGenerationPrefixFencesKeepSnapshots(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "snapshot.kitdb"))
	defer db.Close()
	commitPut(t, db, "a", "old")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	commitPut(t, db, "a", "new")
	commitPut(t, db, "z", "last")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	value, found, err := snapshot.Get([]byte("a"))
	if err != nil || !found || string(value) != "old" {
		t.Fatalf("old snapshot=%q found=%t: %v", value, found, err)
	}
	if _, found, err := snapshot.Get([]byte("z")); err != nil || found {
		t.Fatalf("later key visible: %t %v", found, err)
	}
}

func TestGenerationPrefixFencesAppendToMixedV3(t *testing.T) {
	compressed, err := base64.StdEncoding.DecodeString(kitDBV1MainFixture)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, 1<<20))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(readErr, closeErr)
	}
	path := filepath.Join(t.TempDir(), "mixed-v3.kitdb")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := OpenWithOptions(path, OpenOptions{PageCacheBytes: -1, VerifyOnOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(db.main.blocks) != 1 || db.main.blocks[0].firstKey[0] == db.main.blocks[0].lastKey[0] {
		t.Fatal("frozen fixture must contain a mixed-prefix v3 page")
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	commitPut(t, db, "middle", "updated")
	commitPut(t, db, "new", "added")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"alpha": "one", "middle": "updated", "new": "added", "zulu": "three"} {
		if got := requireValue(t, db, key); string(got) != want {
			t.Fatalf("%s=%q, want %q", key, got, want)
		}
	}
	old, found, err := snapshot.Get([]byte("middle"))
	if err != nil || !found || string(old) != "two" {
		t.Fatalf("old view=%q found=%t: %v", old, found, err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenWithOptions(path, OpenOptions{VerifyOnOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := requireValue(t, db, "middle"); string(got) != "updated" {
		t.Fatalf("reopen=%q", got)
	}
}

func TestGenerationPrefixFencesDirectoryBound(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "fanout.kitdb"))
	defer db.Close()
	tx := mustBegin(t, db)
	for prefix := 0; prefix < 256; prefix++ {
		if prefix == 1 {
			continue
		} // Reserved catalog keys require schema IDs.
		if err := tx.Put([]byte{byte(prefix)}, []byte{'v'}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.MainBlocks != 255 || stats.MainDataBytes != 255*(operationHeaderSize+2) || stats.MainDirectoryBytes != 255*(mainDirectoryEntryHeaderSize+2) {
		t.Fatalf("unexpected packing: blocks=%d data=%d directory=%d", stats.MainBlocks, stats.MainDataBytes, stats.MainDirectoryBytes)
	}
	if err := db.Verify(); err != nil {
		t.Fatal(err)
	}
}
