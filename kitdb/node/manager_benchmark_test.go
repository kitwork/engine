package node

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func BenchmarkManagerTenantChurn(b *testing.B) {
	for _, tenants := range []int{100, 1_000} {
		b.Run(fmt.Sprintf("tenants_%d", tenants), func(b *testing.B) {
			root := b.TempDir()
			manager, err := NewManager(Limits{
				MaxOpenDatabases: 64, MaxPageCacheBytes: 4 << 20,
				DefaultPageCacheBytes: 64 << 10, MaxConcurrentOpens: 4,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer manager.Close()
			paths := make([]string, tenants)
			for index := range paths {
				paths[index] = filepath.Join(root, fmt.Sprintf("tenant-%06d.kitdb", index))
			}

			b.ResetTimer()
			for range b.N {
				for _, path := range paths {
					lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
					if err != nil {
						b.Fatal(err)
					}
					if err := lease.Release(); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.StopTimer()
			stats := manager.Stats()
			if stats.ManagedDatabases > 64 || stats.ReservedPageCacheBytes > 4<<20 || stats.ActiveLeases != 0 {
				b.Fatalf("resource bounds escaped: %#v", stats)
			}
			b.ReportMetric(float64(stats.ManagedDatabases), "warm_handles")
			b.ReportMetric(float64(stats.ReservedPageCacheBytes), "reserved_cache_B")
			b.ReportMetric(float64(stats.Evictions), "evictions")
		})
	}
}
