package relational

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func BenchmarkIdentityInsertCache(b *testing.B) {
	for _, cache := range []int64{1, 32, 128} {
		b.Run(fmt.Sprintf("cache_%d", cache), func(b *testing.B) {
			engine, err := Open(filepath.Join(b.TempDir(), "identity.kitdb"))
			if err != nil {
				b.Fatal(err)
			}
			defer engine.Close()
			ctx := context.Background()
			if _, err := engine.Execute(ctx, fmt.Sprintf(
				`CREATE TABLE events (id BIGINT GENERATED ALWAYS AS IDENTITY (CACHE %d) PRIMARY KEY, payload TEXT)`, cache,
			)); err != nil {
				b.Fatal(err)
			}
			before, _ := engine.database.Stats()
			b.ResetTimer()
			for b.Loop() {
				if _, err := engine.Execute(ctx, `INSERT INTO events(payload) VALUES ('event')`); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			after, _ := engine.database.Stats()
			syncs := after.CommitSyncs - before.CommitSyncs
			b.ReportMetric(float64(syncs)/float64(b.N), "sync/op")
		})
	}
}
