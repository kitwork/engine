package kitdb

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
)

func BenchmarkSequenceCache(b *testing.B) {
	for _, cache := range []int64{1, 32, 128} {
		b.Run(fmt.Sprintf("cache_%d", cache), func(b *testing.B) {
			db, err := Open(filepath.Join(b.TempDir(), "benchmark.kitdb"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			ctx := context.Background()
			if _, err := db.CreateSequence(ctx, Sequence{
				Name: "ids", Start: 1, Increment: 1, Minimum: 1, Maximum: math.MaxInt64, Cache: cache,
			}); err != nil {
				b.Fatal(err)
			}
			before, _ := db.Stats()
			b.ResetTimer()
			for b.Loop() {
				if _, _, err := db.NextSequence(ctx, "ids"); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			after, _ := db.Stats()
			syncs := after.CommitSyncs - before.CommitSyncs
			b.ReportMetric(float64(syncs)/float64(b.N), "sync/op")
		})
	}
}
