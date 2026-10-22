package kitdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/benchclock"
)

func BenchmarkEncodeTimedWALFrame(b *testing.B) {
	for _, count := range []int{1, 128} {
		b.Run(fmt.Sprintf("operations_%d", count), func(b *testing.B) {
			operations := make([]operation, count)
			for i := range operations {
				operations[i] = operation{kind: operationPut, key: []byte(fmt.Sprintf("row/%08d", i)), value: bytes.Repeat([]byte{'v'}, 512)}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := encodeFrameAt(uint64(i+1), 1, operations); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type writePhaseWAL struct {
	commitWAL
	writeTime atomic.Int64
	syncTime  atomic.Int64
	syncs     atomic.Uint64
}

func (wal *writePhaseWAL) Write(data []byte) (int, error) {
	at := benchclock.Now()
	n, err := wal.commitWAL.Write(data)
	wal.writeTime.Add(benchclock.Since(at).Nanoseconds())
	return n, err
}

func (wal *writePhaseWAL) Sync() error {
	at := benchclock.Now()
	err := wal.commitWAL.Sync()
	wal.syncTime.Add(benchclock.Since(at).Nanoseconds())
	wal.syncs.Add(1)
	return err
}

// Measures real durable writes, not a no-op Sync or an in-memory WAL. These
// wall-clock subphase samples include instrumentation overhead, not only I/O.
func BenchmarkDurableWritePhases(b *testing.B) {
	for _, batch := range []int{1, 128} {
		b.Run(fmt.Sprintf("operations_%d", batch), func(b *testing.B) {
			db, err := Open(filepath.Join(b.TempDir(), "wal.kitdb"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			db.commitMu.Lock()
			db.mu.Lock()
			wal := &writePhaseWAL{commitWAL: db.wal}
			db.wal = wal
			db.mu.Unlock()
			db.commitMu.Unlock()
			key := make([]byte, 8)
			value := bytes.Repeat([]byte{'v'}, 512)
			var stage time.Duration
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				at := benchclock.Now()
				tx, err := db.Begin()
				if err != nil {
					b.Fatal(err)
				}
				for j := range batch {
					binary.BigEndian.PutUint64(key, uint64(i*batch+j))
					if err := tx.Put(key, value); err != nil {
						b.Fatal(err)
					}
				}
				stage += benchclock.Since(at)
				if _, err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if wal.syncs.Load() != uint64(b.N) {
				b.Fatal("durable transaction lost its WAL sync")
			}
			b.ReportMetric(float64(stage.Nanoseconds())/float64(b.N), "stage-ns/op")
			b.ReportMetric(float64(wal.writeTime.Load())/float64(b.N), "wal-write-ns/op")
			b.ReportMetric(float64(wal.syncTime.Load())/float64(b.N), "wal-sync-ns/op")
			b.ReportMetric(float64(batch), "rows/op")
			b.ReportMetric(float64(wal.syncs.Load())/float64(b.N), "sync/op")
			if err := db.Verify(); err != nil {
				b.Fatal(err)
			}
		})
	}
}

func BenchmarkSnapshotCaptureWALOverlay(b *testing.B) {
	for _, size := range []int{128, 4096} {
		b.Run(fmt.Sprintf("value_%d", size), func(b *testing.B) {
			db, err := Open(filepath.Join(b.TempDir(), "snapshot.kitdb"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			tx, err := db.Begin()
			if err != nil {
				b.Fatal(err)
			}
			for i := range 2048 {
				if err := tx.Put([]byte(fmt.Sprintf("row/%08d", i)), bytes.Repeat([]byte{'v'}, size)); err != nil {
					b.Fatal(err)
				}
			}
			if _, err := tx.Commit(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				snapshot, err := db.Snapshot()
				if err != nil {
					b.Fatal(err)
				}
				if err := snapshot.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// One iteration checkpoints 128 updates over an existing 8192-row main file.
// Run with a fixed small iteration count; enough iterations trigger compaction.
func BenchmarkCheckpointOverlay128(b *testing.B) {
	db, err := Open(filepath.Join(b.TempDir(), "checkpoint.kitdb"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	value := bytes.Repeat([]byte{'v'}, 512)
	put := func(start, count int) {
		tx, err := db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		for i := start; i < start+count; i++ {
			if err := tx.Put([]byte(fmt.Sprintf("row/%08d", i)), value); err != nil {
				b.Fatal(err)
			}
		}
		if _, err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
	put(0, 8192)
	if _, err := db.Checkpoint(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		put((i*128)%8192, 128)
		b.StartTimer()
		if _, err := db.Checkpoint(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := db.Verify(); err != nil {
		b.Fatal(err)
	}
}
