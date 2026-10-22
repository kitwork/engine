package relational

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

// Same fixture as BenchmarkReadPaths, with independent exact row expectations
// and observation outside the uninstrumented latency measurement.
func TestSQLIndexSuffixReadEvidence(t *testing.T) {
	ctx := context.Background()
	options := kitdbengine.OpenOptions{PageCacheBytes: 256 << 10, SnapshotMemoryBytes: 8 << 20, TransactionMemoryBytes: 8 << 20}
	manager, err := kitdbnode.NewManager(kitdbnode.Limits{MaxOpenDatabases: 1, MaxPageCacheBytes: options.PageCacheBytes, DefaultPageCacheBytes: options.PageCacheBytes, MaxConcurrentOpens: 1, MaxConcurrentMaintenance: 1, MaxQueuedMaintenance: 2, MaxMaintenancePerDatabase: 1})
	if err != nil {
		t.Fatal(err)
	}
	tenant := &mixedTenant{path: filepath.Join(t.TempDir(), "read.kitdb"), expected: make([]int64, mixedRows)}
	defer func() {
		if err := closeMixedOwners([]*mixedTenant{tenant}, manager); err != nil {
			t.Error(err)
		}
	}()
	tenant.lease, err = manager.Acquire(ctx, tenant.path, options)
	if err != nil {
		t.Fatal(err)
	}
	tenant.engine, err = Attach(tenant.lease.DB(), Options{Kernel: options})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedMixedTenant(ctx, tenant, 0); err != nil {
		t.Fatal(err)
	}
	for range 16 {
		if err := writeMixedTenant(ctx, tenant, 32); err != nil {
			t.Fatal(err)
		}
		if _, err := tenant.lease.DB().Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	const query = "SELECT id,version,bucket,payload FROM items WHERE bucket=4 ORDER BY id LIMIT 16"
	var expected [][]any
	matching := 0
	for id, version := range tenant.expected {
		if version%64 == 4 {
			matching++
			if len(expected) < 16 {
				expected = append(expected, []any{int64(id), version, int64(4), tenant.payload})
			}
		}
	}
	if matching != 40 {
		t.Fatalf("fixture changed: matching=%d", matching)
	}
	result, err := tenant.engine.Execute(ctx, query)
	if err != nil || !reflect.DeepEqual(result.Rows, expected) {
		t.Fatalf("exact index result: %v / %v", result.Rows, err)
	}
	observed, err := tenant.engine.Execute(ctx, "EXPLAIN ANALYZE "+query)
	if err != nil || observed.Execution == nil {
		t.Fatalf("observation: %v", err)
	}
	if observed.Execution.IndexEntriesScanned != 16 || observed.Execution.PointLookups != 16 || observed.Execution.RowsScanned != 16 {
		t.Fatalf("expected sixteen hydrated rows: %+v", observed.Execution)
	}
	encoded, err := json.Marshal(observed.Execution)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("matching_rows=%d returned_rows=%d stats=%s", matching, len(expected), encoded)
}
