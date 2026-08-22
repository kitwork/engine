package kitdb

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkPointLookup100K(b *testing.B) {
	path := filepath.Join(b.TempDir(), "tenant.kitdb")
	db, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	value := bytes.Repeat([]byte{'v'}, 128)
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 100_000; index++ {
		if err := tx.Put([]byte(rowKey(index)), value); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(value)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		key := rowKey((index * 7919) % 100_000)
		got, found, err := db.Get([]byte(key))
		if err != nil || !found || len(got) != len(value) {
			b.Fatalf("Get(%q) = (%d, %v, %v)", key, len(got), found, err)
		}
	}
	b.ReportMetric(float64(len(db.main.blocks)), "pages")
	b.ReportMetric(float64(info.Size())/(1<<20), "main-MiB")
}

func BenchmarkPointLookup100KWith16Deltas(b *testing.B) {
	path := filepath.Join(b.TempDir(), "tenant.kitdb")
	db, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	value := bytes.Repeat([]byte{'v'}, 128)
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 100_000; index++ {
		if err := tx.Put([]byte(rowKey(index)), value); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	for delta := 1; delta < 16; delta++ {
		tx, err := db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		if err := tx.Put([]byte(rowKey(0)), value); err != nil {
			b.Fatal(err)
		}
		if _, err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
		if _, err := db.Checkpoint(); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(value)))
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		key := rowKey((index * 7919) % 100_000)
		got, found, err := db.Get([]byte(key))
		if err != nil || !found || len(got) != len(value) {
			b.Fatalf("Get(%q) = (%d, %v, %v)", key, len(got), found, err)
		}
	}
	b.ReportMetric(float64(len(db.main.segments)), "segments")
}

func BenchmarkSnapshotCursorSeek100K(b *testing.B) {
	path := filepath.Join(b.TempDir(), "tenant.kitdb")
	db, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	value := bytes.Repeat([]byte{'v'}, 128)
	for index := 0; index < 100_000; index++ {
		if err := tx.Put([]byte(rowKey(index)), value); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		b.Fatal(err)
	}
	defer snapshot.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		start := []byte(rowKey((index * 7919) % 100_000))
		cursor, err := snapshot.Cursor(RangeOptions{Start: start, Limit: 1})
		if err != nil {
			b.Fatal(err)
		}
		if !cursor.Next() || cursor.Err() != nil || len(cursor.Value()) != len(value) {
			b.Fatalf("cursor seek %q failed: %v", start, cursor.Err())
		}
		_ = cursor.Close()
	}
}

func BenchmarkOpenGenerationMainMetadata100K(b *testing.B) {
	path := filepath.Join(b.TempDir(), "tenant.kitdb")
	db, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	value := bytes.Repeat([]byte{'v'}, 128)
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 100_000; index++ {
		if err := tx.Put([]byte(rowKey(index)), value); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	directoryBytes := db.main.directoryBytes
	blocks := len(db.main.blocks)
	segments := len(db.main.segments)
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		file, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		metadata, decodeErr := decodeMainSnapshot(path, file, info.Size())
		closeErr := file.Close()
		if decodeErr != nil || closeErr != nil {
			b.Fatalf("decode main = decode %v, close %v", decodeErr, closeErr)
		}
		if metadata.records != 100_000 {
			b.Fatalf("decode main records = %d", metadata.records)
		}
	}
	manifestBytes := generationManifestHeaderSize + segments*generationSegmentSize
	b.ReportMetric(float64(8+generationHeaderSize+2*generationSlotSize+manifestBytes)+float64(directoryBytes), "read-bytes")
	b.ReportMetric(float64(blocks), "pages")
	b.ReportMetric(float64(info.Size())/(1<<20), "main-MiB")
}

func BenchmarkIncrementalCheckpointOneRow100K(b *testing.B) {
	path := filepath.Join(b.TempDir(), "tenant.kitdb")
	db, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	value := bytes.Repeat([]byte{'v'}, 128)
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 100_000; index++ {
		if err := tx.Put([]byte(rowKey(index)), value); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	baseInfo, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	update := make([]byte, 128)

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		binary.LittleEndian.PutUint64(update, uint64(index))
		tx, err := db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		if err := tx.Put([]byte(rowKey(50_000)), update); err != nil {
			b.Fatal(err)
		}
		if _, err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
		if _, err := db.Checkpoint(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	if b.N != 0 {
		b.ReportMetric(float64(info.Size()-baseInfo.Size())/float64(b.N), "file-delta-B/op")
	}
}
