package relational

import (
	"context"
	"path/filepath"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

// A deliberately small cache and 17 generations expose irrelevant page reads
// during negative metadata probes and lookups outside each delta's row range.
func BenchmarkReadPaths(b *testing.B) {
	ctx := context.Background()
	options := kitdbengine.OpenOptions{PageCacheBytes: 256 << 10, SnapshotMemoryBytes: 8 << 20, TransactionMemoryBytes: 8 << 20}
	manager, err := kitdbnode.NewManager(kitdbnode.Limits{MaxOpenDatabases: 1, MaxPageCacheBytes: options.PageCacheBytes, DefaultPageCacheBytes: options.PageCacheBytes, MaxConcurrentOpens: 1, MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 2, MaxMaintenancePerDatabase: 1})
	if err != nil {
		b.Fatal(err)
	}
	tenant := &mixedTenant{path: filepath.Join(b.TempDir(), "read.kitdb"), expected: make([]int64, mixedRows)}
	defer func() {
		if err := closeMixedOwners([]*mixedTenant{tenant}, manager); err != nil {
			b.Error(err)
		}
	}()
	tenant.lease, err = manager.Acquire(ctx, tenant.path, options)
	if err != nil {
		b.Fatal(err)
	}
	tenant.engine, err = Attach(tenant.lease.DB(), Options{Kernel: options})
	if err != nil {
		b.Fatal(err)
	}
	if err := seedMixedTenant(ctx, tenant, 0); err != nil {
		b.Fatal(err)
	}
	for range 16 {
		if err := writeMixedTenant(ctx, tenant, 32); err != nil {
			b.Fatal(err)
		}
		if _, err := tenant.lease.DB().Checkpoint(); err != nil {
			b.Fatal(err)
		}
	}
	tx, err := tenant.engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	schema, err := tx.schema("items")
	if err != nil {
		b.Fatal(err)
	}
	pointKey, err := rowKey(schema, map[string]any{"id": int64(900)}, 0)
	if err != nil {
		b.Fatal(err)
	}
	for _, name := range []string{"metadata", "kernel_point", "sql_point", "sql_index"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			var pages, pageBytes uint64
			for range b.N {
				switch name {
				case "metadata":
					generation, epoch, err := activeRowLayout(tx, schema)
					if err != nil || generation != 0 || epoch != 0 {
						b.Fatalf("layout=%d/%d: %v", generation, epoch, err)
					}
				case "kernel_point":
					value, found, stats, err := tx.snapshot.GetWithStats(pointKey)
					if err != nil || !found || len(value) == 0 {
						b.Fatalf("point found=%t: %v", found, err)
					}
					pages += stats.PagesRead
					pageBytes += stats.PageBytesRead
				case "sql_point":
					result, err := tenant.engine.Execute(ctx, "SELECT id,version,bucket,payload FROM items WHERE id=$1", 900)
					if err != nil || len(result.Rows) != 1 {
						b.Fatalf("point: %v", err)
					}
					if err := checkMixedVisible(result.Rows, tenant.payload, -1); err != nil {
						b.Fatal(err)
					}
				case "sql_index":
					result, err := tenant.engine.Execute(ctx, "SELECT id,version,bucket,payload FROM items WHERE bucket=$1 ORDER BY id LIMIT 16", 4)
					if err != nil || len(result.Rows) == 0 {
						b.Fatalf("index: %v", err)
					}
					if err := checkMixedVisible(result.Rows, tenant.payload, 4); err != nil {
						b.Fatal(err)
					}
				}
			}
			if name == "kernel_point" {
				b.ReportMetric(float64(pages)/float64(b.N), "pages/op")
				b.ReportMetric(float64(pageBytes)/float64(b.N), "page-bytes/op")
			}
		})
	}
}
