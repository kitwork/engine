package relational

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/benchclock"
)

func BenchmarkRowEncodeAndIndexEntries(b *testing.B) {
	engine, err := Open(filepath.Join(b.TempDir(), "encode.kitdb"))
	if err != nil {
		b.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, source := range []string{
		"CREATE TABLE events (id INTEGER PRIMARY KEY, category INTEGER, payload TEXT)",
		"CREATE INDEX events_category ON events(category)",
	} {
		if _, err := engine.Execute(ctx, source); err != nil {
			b.Fatal(err)
		}
	}
	schema, err := engine.schemaLocked("events")
	if err != nil {
		b.Fatal(err)
	}
	row := map[string]any{"id": int64(1), "category": int64(7), "payload": strings.Repeat("v", 512)}
	key, err := rowKey(schema, row, 0)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := encodeRow(schema, row, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("index_entries", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			entries, err := secondaryIndexEntries(schema, row, key)
			if err != nil || len(entries) == 0 {
				b.Fatalf("entries=%d: %v", len(entries), err)
			}
		}
	})
}

// Each operation ingests a fresh 4096-row fixture, checkpointing every 1024
// rows. ns/op and allocations exclude setup, validation and the checkpoints;
// checkpoint-ms/op is reported separately. Commit includes durable WAL sync.
func BenchmarkSQLWritePipeline(b *testing.B) {
	for _, indexed := range []bool{false, true} {
		for _, batch := range []int{1, 128} {
			b.Run(fmt.Sprintf("indexed_%t/batch_%d", indexed, batch), func(b *testing.B) {
				benchmarkSQLWritePipeline(b, indexed, batch)
			})
		}
	}
}

func benchmarkSQLWritePipeline(b *testing.B, indexed bool, batch int) {
	b.StopTimer()
	const rows = 4096
	ctx := context.Background()
	var source strings.Builder
	source.WriteString("INSERT INTO events (id,category,payload) VALUES ")
	parameters := make([]any, batch*3)
	for i := range batch {
		if i != 0 {
			source.WriteByte(',')
		}
		fmt.Fprintf(&source, "($%d,$%d,$%d)", i*3+1, i*3+2, i*3+3)
		parameters[i*3+2] = strings.Repeat("v", 512)
	}
	query := source.String()
	var capture, stage, commit, checkpoint, ingest time.Duration
	var syncs uint64
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		path := filepath.Join(b.TempDir(), "write.kitdb")
		engine, err := Open(path)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := engine.Execute(ctx, "CREATE TABLE events (id INTEGER PRIMARY KEY, category INTEGER, payload TEXT)"); err != nil {
			b.Fatal(err)
		}
		if indexed {
			if _, err := engine.Execute(ctx, "CREATE INDEX events_category ON events(category)"); err != nil {
				b.Fatal(err)
			}
		}
		before, err := engine.database.Stats()
		if err != nil {
			b.Fatal(err)
		}
		for start := 0; start < rows; start += batch {
			for i := range batch {
				parameters[i*3] = int64(start + i)
				parameters[i*3+1] = int64((start + i) % 64)
			}
			b.StartTimer()
			at := benchclock.Now()
			started := at
			tx, err := engine.BeginTransaction(ctx, TransactionOptions{})
			capture += benchclock.Since(at)
			if err != nil {
				b.Fatal(err)
			}
			at = benchclock.Now()
			result, err := tx.Execute(ctx, query, parameters...)
			stage += benchclock.Since(at)
			if err != nil || result.Affected != int64(batch) {
				_ = tx.Rollback()
				b.Fatalf("stage rows=%d: %v", result.Affected, err)
			}
			at = benchclock.Now()
			_, err = tx.Commit(ctx)
			commit += benchclock.Since(at)
			if err != nil {
				b.Fatal(err)
			}
			ingest += benchclock.Since(started)
			b.StopTimer()
			if (start+batch)%1024 == 0 {
				at = benchclock.Now()
				if _, err := engine.Checkpoint(); err != nil {
					b.Fatal(err)
				}
				checkpoint += benchclock.Since(at)
			}
		}
		after, err := engine.database.Stats()
		if err != nil {
			b.Fatal(err)
		}
		syncs += after.CommitSyncs - before.CommitSyncs
		if after.CommitSyncs-before.CommitSyncs != rows/uint64(batch) {
			b.Fatal("each serial transaction must retain its WAL sync")
		}
		if err := engine.database.Verify(); err != nil {
			b.Fatal(err)
		}
		if err := engine.Close(); err != nil {
			b.Fatal(err)
		}
		reopened, err := Open(path)
		if err != nil {
			b.Fatal(err)
		}
		result, err := reopened.Execute(ctx, "SELECT COUNT(*), SUM(id) FROM events")
		if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(rows) || result.Rows[0][1] != int64(rows*(rows-1)/2) {
			b.Fatalf("reopened result=%v error=%v", result.Rows, err)
		}
		if err := reopened.Close(); err != nil {
			b.Fatal(err)
		}
	}
	for name, duration := range map[string]time.Duration{"capture-ms/op": capture, "stage-ms/op": stage, "commit-ms/op": commit, "checkpoint-ms/op": checkpoint, "ingest-ms/op": ingest} {
		b.ReportMetric(float64(duration.Nanoseconds())/1e6/float64(b.N), name)
	}
	b.ReportMetric(float64(syncs)/float64(b.N), "sync/op")
	b.ReportMetric(rows, "rows/op")
}
