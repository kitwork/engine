package relational

import (
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

func fixedReadFixture(tb testing.TB) *mixedTenant {
	tb.Helper()
	ctx := context.Background()
	options := kitdbengine.OpenOptions{PageCacheBytes: 256 << 10, SnapshotMemoryBytes: 8 << 20, TransactionMemoryBytes: 8 << 20}
	manager, err := kitdbnode.NewManager(kitdbnode.Limits{MaxOpenDatabases: 1, MaxPageCacheBytes: options.PageCacheBytes, DefaultPageCacheBytes: options.PageCacheBytes, MaxConcurrentOpens: 1, MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 2, MaxMaintenancePerDatabase: 1})
	if err != nil {
		tb.Fatal(err)
	}
	tenant := &mixedTenant{path: filepath.Join(tb.TempDir(), "fixed.kitdb"), expected: make([]int64, mixedRows)}
	tb.Cleanup(func() {
		if err := closeMixedOwners([]*mixedTenant{tenant}, manager); err != nil {
			tb.Error(err)
		}
	})
	tenant.lease, err = manager.Acquire(ctx, tenant.path, options)
	if err != nil {
		tb.Fatal(err)
	}
	tenant.engine, err = Attach(tenant.lease.DB(), Options{Kernel: options})
	if err != nil {
		tb.Fatal(err)
	}
	if err := seedMixedTenant(ctx, tenant, 0); err != nil {
		tb.Fatal(err)
	}
	for range 16 {
		if err := writeMixedTenant(ctx, tenant, 32); err != nil {
			tb.Fatal(err)
		}
		if _, err := tenant.lease.DB().Checkpoint(); err != nil {
			tb.Fatal(err)
		}
	}
	return tenant
}

type fixedReadRow struct {
	key, value []byte
	id         int64
}

func fixedReadRows(tb testing.TB, tenant *mixedTenant, tx *Transaction) ([]fixedReadRow, []byte) {
	tb.Helper()
	schema, err := tx.schema("items")
	if err != nil {
		tb.Fatal(err)
	}
	prefix, err := rowPrefix(schema, 0)
	if err != nil {
		tb.Fatal(err)
	}
	rows := make([]fixedReadRow, 0, mixedRows)
	for id, version := range tenant.expected {
		values := map[string]any{"id": int64(id), "version": version, "bucket": version % 64, "payload": tenant.payload}
		key, err := rowKey(schema, values, 0)
		if err != nil {
			tb.Fatal(err)
		}
		encoded, err := encodeRow(schema, values, nil)
		if err != nil {
			tb.Fatal(err)
		}
		rows = append(rows, fixedReadRow{key, encoded, int64(id)})
	}
	sort.Slice(rows, func(i, j int) bool { return bytes.Compare(rows[i].key, rows[j].key) < 0 })
	return rows, prefix
}

// Fixed counts, immutable generations and a 64-step seeded query schedule:
// use benchtime=128x (two complete cycles), not a timed operation count.
func BenchmarkFixedReadWork(b *testing.B) {
	tenant := fixedReadFixture(b)
	ctx := context.Background()
	model, err := tenant.engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		b.Fatal(err)
	}
	rows, prefix := fixedReadRows(b, tenant, model)
	if err := model.Rollback(); err != nil {
		b.Fatal(err)
	}
	for _, reverse := range []bool{false, true} {
		name := "tx_forward"
		if reverse {
			name = "tx_reverse"
		}
		b.Run(name, func(b *testing.B) {
			tx, err := tenant.engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
			if err != nil {
				b.Fatal(err)
			}
			defer tx.Rollback()
			options := kitdbengine.RangeOptions{Prefix: prefix, Reverse: reverse}
			seen := 0
			visit := func(key, value []byte) (bool, error) {
				if seen >= len(rows) {
					b.Fatal("extra row")
				}
				at := seen
				if reverse {
					at = len(rows) - 1 - seen
				}
				if !bytes.Equal(key, rows[at].key) || !bytes.Equal(value, rows[at].value) {
					b.Fatalf("row %d differs from independent model", seen)
				}
				seen++
				return false, nil
			}
			var stats kitdbengine.CursorStats
			if err := tx.scan(options, &stats, visit); err != nil || seen != len(rows) {
				b.Fatalf("observed scan rows=%d err=%v", seen, err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				seen = 0
				if err := tx.Scan(options, visit); err != nil || seen != len(rows) {
					b.Fatalf("scan rows=%d err=%v", seen, err)
				}
			}
			b.StopTimer()
			// One separate full traversal supplies deterministic page counters;
			// instrumentation is not on the measured ordinary Scan path.
			b.ReportMetric(float64(stats.PagesRead), "pages/op")
			b.ReportMetric(float64(stats.PageBytesRead), "page-bytes/op")
			b.ReportMetric(float64(stats.PageRecordsDecoded), "page-records/op")
		})
	}
	for _, name := range []string{"sql_scan", "sql_point", "sql_index"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				step := i % 64
				query := "SELECT id,version,bucket,payload FROM items WHERE version>=0"
				var args []any
				var expected []int64
				switch name {
				case "sql_point":
					id := int64((step * 37) % mixedRows)
					query = "SELECT id,version,bucket,payload FROM items WHERE id=$1"
					args = []any{id}
					expected = []int64{id}
				case "sql_index":
					query = "SELECT id,version,bucket,payload FROM items WHERE bucket=$1 ORDER BY id LIMIT 16"
					args = []any{step}
					for id, version := range tenant.expected {
						if version%64 == int64(step) && len(expected) < 16 {
							expected = append(expected, int64(id))
						}
					}
				}
				result, err := tenant.engine.Execute(ctx, query, args...)
				if err != nil {
					b.Fatal(err)
				}
				count := len(expected)
				if name == "sql_scan" {
					count = len(rows)
				}
				if len(result.Rows) != count {
					b.Fatalf("%s rows=%d want=%d", name, len(result.Rows), count)
				}
				for j, row := range result.Rows {
					id := int64(0)
					if name == "sql_scan" {
						id = rows[j].id
					} else {
						id = expected[j]
					}
					version := tenant.expected[id]
					if len(row) != 4 || row[0] != id || row[1] != version || row[2] != version%64 || row[3] != tenant.payload {
						b.Fatalf("invalid %s row %d", name, j)
					}
				}
			}
			b.StopTimer()
		})
	}
}
