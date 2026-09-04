package relational

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

const shoppingTextAnalyticsQuery = `
	SELECT merchant, COUNT(*) AS products, SUM(price) AS value
	FROM shopping
	GROUP BY merchant
	ORDER BY merchant`

// BenchmarkShoppingProductionAnalytics is opt-in because it exercises a real,
// potentially large database instead of creating disposable test data.
func BenchmarkShoppingProductionAnalytics(b *testing.B) {
	path := os.Getenv("KITDB_SHOPPING_BENCHMARK")
	if path == "" {
		b.Skip("set KITDB_SHOPPING_BENCHMARK to a verified KitDB copy")
	}
	workloads := []struct {
		name   string
		query  string
		path   string
		groups uint64
	}{
		{
			name:  "count-all",
			query: `SELECT COUNT(*) FROM shopping`,
		},
		{
			name:  "category-common",
			query: `SELECT COUNT(*), SUM(price), AVG(price) FROM shopping WHERE category = 100636`,
			path:  "kcol-batch",
		},
		{
			name:  "category-directory",
			query: `SELECT SUM(price), AVG(stock), MAX(sold) FROM shopping WHERE category = 4459`,
			path:  "kcol-batch",
		},
		{
			name:  "id-high-range",
			query: `SELECT COUNT(*), SUM(price), AVG(price) FROM shopping WHERE id >= 20000000000`,
			path:  "kcol-batch",
		},
		{
			name:   "group-category",
			query:  `SELECT category, COUNT(*) AS products, SUM(price) AS value FROM shopping GROUP BY category ORDER BY category`,
			path:   "kcol-batch",
			groups: 5_699,
		},
		{
			name:   "group-merchant-index-only",
			query:  `SELECT merchant, COUNT(*) AS products FROM shopping GROUP BY merchant ORDER BY merchant`,
			path:   "index-only-group",
			groups: 3,
		},
		{
			name:   "group-merchant-kcol-value",
			query:  shoppingTextAnalyticsQuery,
			path:   "kcol-batch",
			groups: 3,
		},
		{
			name: "merchant-covering-count",
			query: `SELECT COUNT(*) AS products, COUNT(id) AS ids,
				MIN(id) AS first_id, MAX(id) AS last_id
				FROM shopping WHERE merchant = 'lazada' AND id >= 0`,
			path:   "index-only-aggregate",
			groups: 1,
		},
		{
			name: "merchant-covering-exact-aggregate",
			query: `SELECT COUNT(*) AS products, COUNT(id) AS ids, SUM(id) AS id_sum,
				AVG(id) AS id_avg, MIN(id) AS first_id, MAX(id) AS last_id
				FROM shopping WHERE merchant = 'lazada' AND id >= 0`,
			path:   "index-only-aggregate",
			groups: 1,
		},
	}
	for _, workload := range workloads {
		b.Run(workload.name, func(b *testing.B) {
			engine, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
			if err != nil {
				b.Fatal(err)
			}
			defer engine.Close()

			ctx := context.Background()
			observed, err := engine.Execute(ctx, "EXPLAIN ANALYZE "+workload.query)
			if err != nil {
				b.Fatal(err)
			}
			if workload.path != "" && (observed.Execution == nil || observed.Execution.Path != workload.path) {
				b.Fatalf("expected %s execution, got %+v", workload.path, observed.Execution)
			}
			if workload.groups != 0 && (observed.Execution == nil || observed.Execution.Groups != workload.groups) {
				b.Fatalf("expected %d groups, got %+v", workload.groups, observed.Execution)
			}
			b.ReportAllocs()

			b.ResetTimer()
			for b.Loop() {
				if _, err := engine.Execute(ctx, workload.query); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if observed.Execution != nil {
				b.ReportMetric(float64(observed.Execution.ChunksSkipped), "chunks_skipped")
				b.ReportMetric(float64(observed.Execution.ColumnarBlocksPruned), "blocks_pruned")
				b.ReportMetric(float64(observed.Execution.ColumnarBlockHeadersRead), "headers_read")
				b.ReportMetric(float64(observed.Execution.ColumnarHeaderBytesRead+observed.Execution.ColumnarPayloadBytesRead), "kcol_bytes")
				b.ReportMetric(float64(observed.Execution.RowsScanned), "rows_scanned")
				b.ReportMetric(float64(observed.Execution.RowsSkipped), "rows_skipped")
				b.ReportMetric(float64(observed.Execution.RowsFromMetadata), "rows_metadata")
				b.ReportMetric(float64(observed.Execution.Groups), "groups")
				b.ReportMetric(float64(observed.Execution.IndexEntriesScanned), "index_entries")
				b.ReportMetric(float64(observed.Execution.PageBytesRead), "page_bytes")
			}
		})
	}
}

// BenchmarkShoppingProductionKROWTextAnalytics retains the row-backed typed
// batch baseline separately because each iteration scans the 30 GiB source.
func BenchmarkShoppingProductionKROWTextAnalytics(b *testing.B) {
	path := os.Getenv("KITDB_SHOPPING_BENCHMARK")
	if path == "" {
		b.Skip("set KITDB_SHOPPING_BENCHMARK to a verified KitDB copy")
	}
	engine, err := OpenWithOptions(path, Options{BatchAggregates: true})
	if err != nil {
		b.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	observed, err := engine.Execute(ctx, "EXPLAIN ANALYZE "+shoppingTextAnalyticsQuery)
	if err != nil || observed.Execution == nil || observed.Execution.Path != "krow-batch" || observed.Execution.Groups != 3 {
		b.Fatalf("expected three krow-batch groups: result=%+v error=%v", observed.Execution, err)
	}

	b.ReportAllocs()
	b.ReportMetric(float64(observed.Execution.PageBytesRead), "page_bytes")
	b.ReportMetric(float64(observed.Execution.RowsScanned), "rows_scanned")
	b.ReportMetric(float64(observed.Execution.Groups), "groups")
	b.ResetTimer()
	for b.Loop() {
		if _, err := engine.Execute(ctx, shoppingTextAnalyticsQuery); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkShoppingProductionRows keeps selective OLTP paths beside the
// analytical measurements. It prevents a faster scan from hiding a point or
// ordered-page regression on the same wide production schema.
func BenchmarkShoppingProductionRows(b *testing.B) {
	path := os.Getenv("KITDB_SHOPPING_BENCHMARK")
	if path == "" {
		b.Skip("set KITDB_SHOPPING_BENCHMARK to a verified KitDB copy")
	}
	workloads := []struct {
		name  string
		query string
		path  string
		rows  int
	}{
		{
			name:  "composite-primary",
			query: `SELECT merchant, id, name, price FROM shopping WHERE merchant = 'lazada' AND id = 24384 LIMIT 1`,
			path:  "primary-lookup",
			rows:  1,
		},
		{
			name:  "primary-order-page",
			query: `SELECT merchant, id, name, price FROM shopping ORDER BY merchant, id LIMIT 100`,
			path:  "index-scan",
			rows:  100,
		},
		{
			name:  "merchant-order-page",
			query: `SELECT merchant, id, name, price FROM shopping WHERE merchant = 'shopee' ORDER BY merchant, id LIMIT 100`,
			path:  "index-scan",
			rows:  100,
		},
	}
	for _, workload := range workloads {
		b.Run(workload.name, func(b *testing.B) {
			engine, err := Open(path)
			if err != nil {
				b.Fatal(err)
			}
			defer engine.Close()
			ctx := context.Background()
			observed, err := engine.Execute(ctx, "EXPLAIN ANALYZE "+workload.query)
			if err != nil || observed.Execution == nil || observed.Execution.Path != workload.path {
				b.Fatalf("expected %s execution, got %+v error=%v", workload.path, observed.Execution, err)
			}
			warm, err := engine.Execute(ctx, workload.query)
			if err != nil || len(warm.Rows) != workload.rows {
				b.Fatalf("warm rows=%d error=%v", len(warm.Rows), err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := engine.Execute(ctx, workload.query); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(observed.Execution.PageBytesRead), "page_bytes")
			b.ReportMetric(float64(observed.Execution.PagesRead), "pages_read")
			b.ReportMetric(float64(observed.Execution.RowsScanned), "rows_scanned")
		})
	}
}

// BenchmarkShoppingProductionMaterialization exercises the query-wide memory
// ledger with real wide rows. Only projected fields should survive the source
// scan; retaining all 30 source fields exceeds the 32 MiB budget.
func BenchmarkShoppingProductionMaterialization(b *testing.B) {
	path := os.Getenv("KITDB_SHOPPING_BENCHMARK")
	if path == "" {
		b.Skip("set KITDB_SHOPPING_BENCHMARK to a verified KitDB copy")
	}
	engine, err := OpenWithOptions(path, Options{MaximumResultRows: 10_000})
	if err != nil {
		b.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	query := `WITH sample AS (
		SELECT merchant, id, price FROM shopping
		WHERE merchant = 'shopee'
		ORDER BY merchant, id LIMIT 10000
	) SELECT COUNT(*), SUM(price) FROM sample`
	observed, err := engine.Execute(ctx, "EXPLAIN ANALYZE "+query)
	if err != nil || observed.Execution == nil || observed.Execution.Path != "materialized-aggregate" {
		b.Fatalf("materialized observation=%+v error=%v", observed.Execution, err)
	}
	if observed.Execution.RowsMaterialized != 10_000 ||
		observed.Execution.MaterializationPeakBytes == 0 ||
		observed.Execution.MaterializationPeakBytes > maximumMaterializedBytes {
		b.Fatalf("materialization boundary=%+v", observed.Execution)
	}
	warm, err := engine.Execute(ctx, query)
	if err != nil || len(warm.Rows) != 1 {
		b.Fatalf("warm materialization rows=%d error=%v", len(warm.Rows), err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := engine.Execute(ctx, query); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(observed.Execution.RowsMaterialized), "rows_materialized")
	b.ReportMetric(float64(observed.Execution.MaterializationBytes), "retained_bytes")
	b.ReportMetric(float64(observed.Execution.MaterializationPeakBytes), "peak_bytes")
}

// BenchmarkShoppingProductionSearch uses the retained 13M search projection.
// Keep its path separate because the analytical benchmark copy intentionally
// carries no search sidecar.
func BenchmarkShoppingProductionSearch(b *testing.B) {
	path := os.Getenv("KITDB_SHOPPING_SEARCH_BENCHMARK")
	if path == "" {
		b.Skip("set KITDB_SHOPPING_SEARCH_BENCHMARK to a verified KitDB search copy")
	}
	workloads := []struct {
		name  string
		query string
		rows  int
	}{
		{
			name:  "name-20",
			query: `SELECT merchant, id, name, _score FROM shopping WHERE name SEARCH 'ban phim logitech' ORDER BY _score DESC LIMIT 20`,
			rows:  20,
		},
		{
			name:  "all-fields-20",
			query: `SELECT merchant, id, name, _score FROM shopping WHERE * SEARCH 'ban phim logitech' ORDER BY _score DESC LIMIT 20`,
			rows:  20,
		},
		{
			name:  "all-fields-merchant-120",
			query: `SELECT merchant, id, name, _score FROM shopping WHERE * SEARCH 'ban phim logitech' AND merchant = 'shopee' ORDER BY _score DESC LIMIT 120`,
			rows:  120,
		},
	}
	for _, workload := range workloads {
		b.Run(workload.name, func(b *testing.B) {
			engine, err := OpenWithOptions(path, Options{
				MaximumResultRows:       1_000,
				MaximumSearchResults:    1_000,
				MaximumSearchCandidates: 1_000_000,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer engine.Close()
			ctx := context.Background()
			explained, err := engine.Execute(ctx, "EXPLAIN "+workload.query)
			if err != nil || !resultContainsOperation(explained, "ranked search") {
				b.Fatalf("search plan=%+v error=%v", explained.Rows, err)
			}
			warm, err := engine.Execute(ctx, workload.query)
			if err != nil || len(warm.Rows) != workload.rows {
				b.Fatalf("warm search rows=%d error=%v", len(warm.Rows), err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := engine.Execute(ctx, workload.query); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func resultContainsOperation(result Result, operation string) bool {
	for _, row := range result.Rows {
		value, ok := rowValue[string](row, 1)
		if ok && strings.EqualFold(operation, value) {
			return true
		}
	}
	return false
}

func rowValue[T any](row []any, index int) (T, bool) {
	var zero T
	if index < 0 || index >= len(row) {
		return zero, false
	}
	value, ok := row[index].(T)
	return value, ok
}

// BenchmarkShoppingProductionConcurrentAnalytics measures the bounded two-scan
// admission path rather than spawning one unbounded worker per query.
func BenchmarkShoppingProductionConcurrentAnalytics(b *testing.B) {
	path := os.Getenv("KITDB_SHOPPING_BENCHMARK")
	if path == "" {
		b.Skip("set KITDB_SHOPPING_BENCHMARK to a verified KitDB copy")
	}
	engine, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		b.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	query := `SELECT SUM(price), AVG(stock), MAX(sold) FROM shopping WHERE category = 4459`
	if _, err := engine.Execute(ctx, query); err != nil {
		b.Fatal(err)
	}
	var failure error
	var failureMu sync.Mutex
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(worker *testing.PB) {
		for worker.Next() {
			result, err := engine.Execute(ctx, query)
			if err != nil || result.Execution == nil || result.Execution.Path != "kcol-batch" {
				failureMu.Lock()
				if failure == nil {
					if err != nil {
						failure = err
					} else {
						failure = fmt.Errorf("expected kcol-batch execution, got %+v", result.Execution)
					}
				}
				failureMu.Unlock()
				return
			}
		}
	})
	if failure != nil {
		b.Fatal(failure)
	}
}

// BenchmarkShoppingProjectionReaderCache isolates the reader/directory cache
// while retaining one real KCOL workload. It is opt-in for the same reason as
// BenchmarkShoppingProductionAnalytics.
func BenchmarkShoppingProjectionReaderCache(b *testing.B) {
	path := os.Getenv("KITDB_SHOPPING_BENCHMARK")
	if path == "" {
		b.Skip("set KITDB_SHOPPING_BENCHMARK to a verified KitDB copy")
	}
	engine, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		b.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	query := `SELECT SUM(price), AVG(stock), MAX(sold) FROM shopping WHERE category = 4459`
	warm, err := engine.Execute(ctx, query)
	if err != nil || warm.Execution == nil || warm.Execution.Path != "kcol-batch" {
		b.Fatalf("warmup: result=%+v error=%v", warm, err)
	}

	b.Run("warm-reader", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			result, err := engine.Execute(ctx, query)
			if err != nil || result.Execution == nil || result.Execution.ProjectionCacheHits != 1 {
				b.Fatalf("warm query: result=%+v error=%v", result, err)
			}
		}
	})
	b.Run("reopen-directory", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			engine.projectionMu.Lock()
			err := engine.projectionCache.invalidate("columnar")
			engine.projectionMu.Unlock()
			if err != nil {
				b.Fatal(err)
			}
			result, err := engine.Execute(ctx, query)
			if err != nil || result.Execution == nil || result.Execution.ProjectionCacheMisses != 1 {
				b.Fatalf("cold query: result=%+v error=%v", result, err)
			}
		}
	})
}
