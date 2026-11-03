package relational

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

func pointSQLFixture(tb testing.TB, rows int) *Engine {
	return pointSQLFixtureWithCache(tb, rows, 256<<10)
}

func pointSQLFixtureWithCache(tb testing.TB, rows int, cacheBytes int64) *Engine {
	tb.Helper()
	e, err := OpenWithOptions(filepath.Join(tb.TempDir(), "points.kitdb"), Options{Kernel: kitdbengine.OpenOptions{PageCacheBytes: cacheBytes}})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := e.Close(); err != nil {
			tb.Error(err)
		}
	})
	ctx := context.Background()
	for _, query := range []string{"CREATE TABLE items (id INTEGER PRIMARY KEY, bucket INTEGER, payload TEXT)", "CREATE INDEX items_bucket ON items(bucket, id)"} {
		if _, err := e.Execute(ctx, query); err != nil {
			tb.Fatal(err)
		}
	}
	tx, err := e.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	for id := 0; id < rows; id++ {
		if _, err := tx.Execute(ctx, "INSERT INTO items (id,bucket,payload) VALUES ($1,$2,$3)", id, id%8, strings.Repeat("x", 512)); err != nil {
			tb.Fatal(err)
		}
	}
	if _, err := tx.Commit(ctx); err != nil {
		tb.Fatal(err)
	}
	if _, err := e.Checkpoint(); err != nil {
		tb.Fatal(err)
	}
	return e
}

func TestSQLPointReaderTransactionScope(t *testing.T) {
	e := pointSQLFixture(t, 64)
	ctx := context.Background()
	const query = "SELECT id,bucket,payload FROM items WHERE bucket=0 ORDER BY id LIMIT 16"
	old, err := e.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	before, err := old.Execute(ctx, query)
	if err != nil || len(before.Rows) != 8 {
		t.Fatal(before.Rows, err)
	}
	if _, err := e.Execute(ctx, "UPDATE items SET payload='outside' WHERE id=0"); err != nil {
		t.Fatal(err)
	}
	// Bypass the facade's lifetime-exclusive checkpoint gate while this test
	// deliberately retains an older transaction; kernel append is snapshot-safe.
	if _, err := e.database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	stable, err := old.Execute(ctx, query)
	if err != nil || !reflect.DeepEqual(stable.Rows, before.Rows) {
		t.Fatal("snapshot changed", err)
	}
	if _, err := old.Execute(ctx, "UPDATE items SET payload='inside' WHERE id=8"); err != nil {
		t.Fatal(err)
	}
	point, err := old.Execute(ctx, "SELECT payload FROM items WHERE id=8")
	if err != nil || point.Rows[0][0] != "inside" {
		t.Fatal(point.Rows, err)
	}
	visible, err := old.Execute(ctx, query)
	if err != nil || visible.Rows[1][2] != "inside" {
		t.Fatal(visible.Rows, err)
	}
	if _, err := old.Commit(ctx); !errors.Is(err, ErrTransactionConflict) {
		t.Fatal("dependency conflict missing", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.Execute(cancelled, query); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	result, err := e.Execute(ctx, query)
	if err != nil || len(result.Rows) != 8 || result.Rows[0][2] != "outside" {
		t.Fatal(result.Rows, err)
	}
}

func TestSQLPointReaderWarmIndex(t *testing.T) {
	e := pointSQLFixture(t, 1024)
	ctx := context.Background()
	const query = "SELECT id,bucket,payload FROM items WHERE bucket=0 ORDER BY id LIMIT 16"
	for range 32 {
		if _, err := e.Execute(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	result, err := e.Execute(ctx, "EXPLAIN ANALYZE "+query)
	if err != nil || result.Execution == nil {
		t.Fatal(err)
	}
	stats := result.Execution
	if stats.PointLookups != 16 || stats.PageCacheHits < 16 || stats.PageCacheMisses != 0 {
		t.Fatalf("repeated index hydration did not warm the shared cache: %+v", stats)
	}
}

// Fixed iteration counts are selected by the campaign. Fixture setup, warm-up
// and observed plan checks are excluded from query timing and allocation counts.
func BenchmarkPointReaderSQL(b *testing.B) {
	for _, size := range []struct {
		name string
		rows int
	}{{"small", 32}, {"large", 1024}} {
		b.Run(size.name, func(b *testing.B) {
			e := pointSQLFixture(b, size.rows)
			ctx := context.Background()
			payload := strings.Repeat("x", 512)
			for _, test := range []struct {
				name, query string
				rows        int
			}{
				{"point", "SELECT id,bucket,payload FROM items WHERE id=0", 1},
				{"hot_index", "SELECT id,bucket,payload FROM items WHERE bucket=0 ORDER BY id LIMIT 16", min(16, size.rows/8)},
			} {
				b.Run(test.name, func(b *testing.B) {
					for range 32 {
						if _, err := e.Execute(ctx, test.query); err != nil {
							b.Fatal(err)
						}
					}
					observed, err := e.Execute(ctx, "EXPLAIN ANALYZE "+test.query)
					if err != nil || observed.Execution == nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						result, err := e.Execute(ctx, test.query)
						if err != nil || len(result.Rows) != test.rows || result.Rows[0][0] != int64(0) || result.Rows[0][2] != payload {
							b.Fatal(result.Rows, err)
						}
					}
					b.ReportMetric(float64(observed.Execution.PointLookups), "lookups/query")
				})
			}
		})
	}
}
