package relational

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

func TestExplainAnalyzeReportsActualColumnarWorkAndFallback(t *testing.T) {
	engine, err := OpenWithOptions(
		filepath.Join(t.TempDir(), "explain-analyze.kitdb"),
		Options{ExperimentalProjections: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `
		CREATE TABLE metrics (
			id INTEGER PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			value INTEGER NOT NULL
		) PARTITION BY RANGE (tenant_id)
	`); err != nil {
		t.Fatal(err)
	}
	insertPartitionRows(t, engine, columnarChunkRows, func(row int) int { return row % 8 })
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}

	analyzed, err := engine.Execute(ctx, `
		EXPLAIN ANALYZE
		SELECT COUNT(*), SUM(value) FROM metrics WHERE tenant_id = 7
	`)
	if err != nil {
		t.Fatal(err)
	}
	if analyzed.CommandTag != "EXPLAIN" || len(analyzed.Columns) != 3 || analyzed.Execution == nil ||
		analyzed.Execution.Path != "kcol-batch" {
		t.Fatalf("EXPLAIN ANALYZE result = %+v", analyzed)
	}
	actual := explainAnalyzeDetail(t, analyzed, "actual")
	if !strings.Contains(actual, "path=kcol-batch") || !strings.Contains(actual, "loops=1") ||
		!strings.Contains(actual, "result_rows=1") || !strings.Contains(actual, "planning_ns=") ||
		!strings.Contains(actual, "execution_ns=") {
		t.Fatalf("actual detail = %q", actual)
	}
	if chunks := explainAnalyzeDetail(t, analyzed, "chunks"); chunks != "scanned=1 skipped=0" {
		t.Fatalf("chunk detail = %q", chunks)
	}
	if cache := explainAnalyzeDetail(t, analyzed, "projection_cache"); cache != "hits=0 misses=1 bypasses=0" {
		t.Fatalf("cold projection cache detail = %q", cache)
	}
	if directory := explainAnalyzeDetail(t, analyzed, "kcol_directory"); directory != "blocks_pruned=7 rows_pruned=7168" {
		t.Fatalf("directory detail = %q", directory)
	}
	if blocks := explainAnalyzeDetail(t, analyzed, "blocks"); blocks != "scanned=0 skipped=7 metadata=1" {
		t.Fatalf("block detail = %q", blocks)
	}
	if rows := explainAnalyzeDetail(t, analyzed, "rows"); rows != "scanned=0 skipped=7168 metadata=1024 groups=0" {
		t.Fatalf("row detail = %q", rows)
	}
	stats := analyzed.Execution
	if stats.ColumnarBlockHeadersRead != 1 || stats.ColumnarHeaderBytesRead == 0 ||
		stats.ColumnarPayloadsRead != 0 || stats.ColumnarPayloadBytesRead != 0 {
		t.Fatalf("metadata-only KCOL I/O = %+v", stats)
	}
	if physical := explainAnalyzeDetail(t, analyzed, "kcol_io"); physical != fmt.Sprintf(
		"header_reads=1 header_bytes=%d payload_reads=0 payload_bytes=0 total_bytes=%d",
		stats.ColumnarHeaderBytesRead, stats.ColumnarHeaderBytesRead,
	) {
		t.Fatalf("metadata-only KCOL detail = %q", physical)
	}

	payload, err := engine.Execute(ctx, `
		EXPLAIN ANALYZE
		SELECT SUM(value) FROM metrics WHERE value >= 48
	`)
	if err != nil {
		t.Fatal(err)
	}
	stats = payload.Execution
	if stats == nil || stats.Path != "kcol-batch" || stats.Batches != 8 ||
		stats.RowsScanned != columnarChunkRows || stats.ColumnarBlockHeadersRead != 8 ||
		stats.ColumnarHeaderBytesRead == 0 || stats.ColumnarPayloadsRead != 8 ||
		stats.ColumnarPayloadBytesRead != uint64(columnarChunkRows*9) {
		t.Fatalf("payload KCOL I/O = %+v", stats)
	}
	if physical := explainAnalyzeDetail(t, payload, "kcol_io"); physical != fmt.Sprintf(
		"header_reads=8 header_bytes=%d payload_reads=8 payload_bytes=%d total_bytes=%d",
		stats.ColumnarHeaderBytesRead, stats.ColumnarPayloadBytesRead,
		stats.ColumnarHeaderBytesRead+stats.ColumnarPayloadBytesRead,
	) {
		t.Fatalf("payload KCOL detail = %q", physical)
	}
	if cache := explainAnalyzeDetail(t, payload, "projection_cache"); cache != "hits=1 misses=0 bypasses=0" {
		t.Fatalf("warm projection cache detail = %q", cache)
	}

	if _, err := engine.Execute(ctx, `INSERT INTO metrics VALUES (9000, 7, 1)`); err != nil {
		t.Fatal(err)
	}
	stale, err := engine.Execute(ctx, `
		EXPLAIN ANALYZE
		SELECT COUNT(*), SUM(value) FROM metrics WHERE tenant_id = 7
	`)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Execution == nil || stale.Execution.Path != "krow-batch" {
		t.Fatalf("stale EXPLAIN ANALYZE stats = %+v", stale.Execution)
	}
	if actual := explainAnalyzeDetail(t, stale, "actual"); !strings.Contains(actual, "path=krow-batch") {
		t.Fatalf("stale actual detail = %q", actual)
	}
	if fallback := explainAnalyzeDetail(t, stale, "fallback"); !strings.Contains(fallback, "stale") {
		t.Fatalf("fallback detail = %q", fallback)
	}
	if cache := explainAnalyzeDetail(t, stale, "projection_cache"); cache != "hits=0 misses=1 bypasses=0" {
		t.Fatalf("stale projection cache detail = %q", cache)
	}
}

func TestExplainAnalyzeConstantSelect(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "constant.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	result, err := engine.Execute(context.Background(), `EXPLAIN ANALYZE SELECT 1 AS answer`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 2 || result.Rows[0][1] != "constant projection" {
		t.Fatalf("constant EXPLAIN ANALYZE = %#v", result.Rows)
	}
	if actual := explainAnalyzeDetail(t, result, "actual"); !strings.Contains(actual, "path=constant-projection") || !strings.Contains(actual, "result_rows=1") {
		t.Fatalf("constant actual detail = %q", actual)
	}
}

func TestExplainAnalyzeReportsCatalogCount(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "count.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE items (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO items VALUES (1), (2), (3)`); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(ctx, `EXPLAIN ANALYZE SELECT COUNT(*) FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution == nil || result.Execution.Path != "catalog-count" ||
		result.Execution.RowsFromMetadata != 3 || result.Rows[0][1] != "catalog count" {
		t.Fatalf("catalog count EXPLAIN ANALYZE = %+v", result)
	}
	if actual := explainAnalyzeDetail(t, result, "actual"); !strings.Contains(actual, "path=catalog-count") || !strings.Contains(actual, "result_rows=1") {
		t.Fatalf("catalog count actual detail = %q", actual)
	}
}

func TestExplainAnalyzeReportsLogicalRowAccessWork(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "row-access.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, query := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, code TEXT UNIQUE, bucket INTEGER NOT NULL, title TEXT NOT NULL)`,
		`CREATE INDEX items_bucket_id_idx ON items (bucket, id)`,
		`INSERT INTO items VALUES
			(1,'code-1',1,'item-1'), (2,'code-2',0,'item-2'),
			(3,'code-3',1,'item-3'), (4,'code-4',0,'item-4'),
			(5,'code-5',1,'item-5'), (6,'code-6',0,'item-6'),
			(7,'code-7',1,'item-7'), (8,'code-8',0,'item-8'),
			(9,'code-9',1,'item-9'), (10,'code-10',0,'item-10'),
			(11,'code-11',1,'item-11'), (12,'code-12',0,'item-12')`,
		`CREATE TABLE buckets (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO buckets VALUES (0,'even'), (1,'odd')`,
	} {
		if _, err := engine.Execute(ctx, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	plain, err := engine.Execute(ctx, `SELECT id FROM items WHERE id = 7`)
	if err != nil || plain.Execution != nil {
		t.Fatalf("ordinary row SELECT changed observability contract: %+v, %v", plain, err)
	}

	tests := []struct {
		name         string
		query        string
		path         string
		indexEntries uint64
		pointLookups uint64
		rowsScanned  uint64
		rowsMatched  uint64
		returned     string
	}{
		{
			name: "primary-hit", query: `EXPLAIN ANALYZE SELECT id,title FROM items WHERE id = 7`,
			path: "primary-lookup", pointLookups: 1, rowsScanned: 1, rowsMatched: 1, returned: "result_rows=1",
		},
		{
			name: "primary-miss", query: `EXPLAIN ANALYZE SELECT id FROM items WHERE id = 99`,
			path: "primary-lookup", pointLookups: 1, returned: "result_rows=0",
		},
		{
			name: "unique-hit", query: `EXPLAIN ANALYZE SELECT id FROM items WHERE code = 'code-7'`,
			path: "unique-lookup", indexEntries: 1, pointLookups: 2, rowsScanned: 1, rowsMatched: 1, returned: "result_rows=1",
		},
		{
			name: "unique-miss", query: `EXPLAIN ANALYZE SELECT id FROM items WHERE code = 'missing'`,
			path: "unique-lookup", pointLookups: 1, returned: "result_rows=0",
		},
		{
			name: "secondary-early-stop", query: `EXPLAIN ANALYZE SELECT id FROM items WHERE bucket = 1 ORDER BY id LIMIT 3`,
			path: "index-scan", indexEntries: 3, pointLookups: 3, rowsScanned: 3, rowsMatched: 3, returned: "result_rows=3",
		},
		{
			name: "sequential-early-stop", query: `EXPLAIN ANALYZE SELECT id FROM items WHERE title LIKE 'item-%' LIMIT 4`,
			path: "sequential-scan", rowsScanned: 4, rowsMatched: 4, returned: "result_rows=4",
		},
		{
			name: "indexed-aggregate", query: `EXPLAIN ANALYZE SELECT COUNT(*) FROM items WHERE bucket = 1`,
			path: "index-only-aggregate", indexEntries: 6, rowsMatched: 6, returned: "result_rows=1",
		},
		{
			name: "primary-expression", query: `EXPLAIN ANALYZE SELECT id + 1 AS next_id FROM items WHERE id = 3`,
			path: "primary-lookup", pointLookups: 1, rowsScanned: 1, rowsMatched: 1, returned: "result_rows=1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := engine.Execute(ctx, test.query)
			if err != nil {
				t.Fatal(err)
			}
			if result.Execution == nil || result.Execution.Path != test.path ||
				result.Execution.IndexEntriesScanned != test.indexEntries ||
				result.Execution.PointLookups != test.pointLookups ||
				result.Execution.RowsScanned != test.rowsScanned ||
				result.Execution.RowsMatched != test.rowsMatched {
				t.Fatalf("execution stats = %+v", result.Execution)
			}
			if actual := explainAnalyzeDetail(t, result, "actual"); !strings.Contains(actual, "path="+test.path) || !strings.Contains(actual, test.returned) {
				t.Fatalf("actual detail = %q", actual)
			}
			if index := explainAnalyzeDetail(t, result, "index"); index !=
				fmt.Sprintf("entries_scanned=%d point_lookups=%d", test.indexEntries, test.pointLookups) {
				t.Fatalf("index detail = %q", index)
			}
			wantRows := fmt.Sprintf("scanned=%d matched=%d", test.rowsScanned, test.rowsMatched)
			if test.path == "index-only-aggregate" {
				wantRows += " groups=1"
			}
			if rows := explainAnalyzeDetail(t, result, "rows"); rows != wantRows {
				t.Fatalf("row detail = %q", rows)
			}
		})
	}

	joined, err := engine.Execute(ctx, `
		EXPLAIN ANALYZE
		SELECT i.id, b.name
		FROM items i JOIN buckets b ON b.id = i.bucket
		WHERE i.id <= 3
		ORDER BY i.id
		LIMIT 3
	`)
	if err != nil {
		t.Fatal(err)
	}
	if joined.Execution == nil || joined.Execution.Path != "nested-loop-join" ||
		joined.Execution.RowsMatched != 3 || joined.Execution.RowsScanned < 6 ||
		joined.Execution.PointLookups < 3 {
		t.Fatalf("join execution stats = %+v", joined.Execution)
	}
}

func TestExplainAnalyzeReportsAuditedKernelPageWork(t *testing.T) {
	engine, err := OpenWithOptions(
		filepath.Join(t.TempDir(), "physical.kitdb"),
		Options{BatchAggregates: true, Kernel: kitdbengine.OpenOptions{PageCacheBytes: -1}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE items (id INTEGER PRIMARY KEY, title TEXT NOT NULL, amount INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO items VALUES (1,'one',10),(2,'two',20),(3,'three',30)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.database.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	point, err := engine.Execute(ctx, `EXPLAIN ANALYZE SELECT title FROM items WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	stats := point.Execution
	if stats == nil || stats.Path != "primary-lookup" || stats.PageAccesses != 1 ||
		stats.PagesRead != 1 || stats.PageCacheBypasses != 1 ||
		stats.PageCacheHits != 0 || stats.PageCacheMisses != 0 ||
		stats.PageBytesRead == 0 || stats.PageRecordsDecoded == 0 ||
		stats.GenerationEntriesVisited != 0 || stats.OverlayEntriesVisited != 0 {
		t.Fatalf("point physical stats = %+v", stats)
	}
	if pages := explainAnalyzeDetail(t, point, "pages"); pages != fmt.Sprintf(
		"accessed=1 read=1 bytes=%d records_decoded=%d",
		stats.PageBytesRead, stats.PageRecordsDecoded,
	) {
		t.Fatalf("point pages = %q", pages)
	}
	if cache := explainAnalyzeDetail(t, point, "cache"); cache != "hits=0 misses=0 bypasses=1" {
		t.Fatalf("point cache = %q", cache)
	}
	if physical := explainAnalyzeDetail(t, point, "physical"); physical != "generation_entries=0 overlay_entries=0" {
		t.Fatalf("point physical = %q", physical)
	}

	scan, err := engine.Execute(ctx, `EXPLAIN ANALYZE SELECT id FROM items WHERE title LIKE '%' LIMIT 2`)
	if err != nil {
		t.Fatal(err)
	}
	stats = scan.Execution
	if stats == nil || stats.Path != "sequential-scan" || stats.PagesRead == 0 ||
		stats.PageAccesses != stats.PagesRead || stats.PageCacheBypasses != stats.PagesRead ||
		stats.GenerationEntriesVisited <= stats.RowsScanned {
		t.Fatalf("scan physical stats = %+v", stats)
	}

	ordinary, err := engine.Execute(ctx, `SELECT SUM(amount) FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	if stats = ordinary.Execution; stats == nil || stats.Path != "krow-batch" ||
		stats.PageAccesses != 0 || stats.PagesRead != 0 || stats.PageBytesRead != 0 ||
		stats.PageRecordsDecoded != 0 || stats.GenerationEntriesVisited != 0 {
		t.Fatalf("ordinary KROW batch should keep the unobserved fast path: %+v", stats)
	}

	batch, err := engine.Execute(ctx, `EXPLAIN ANALYZE SELECT SUM(amount) FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	stats = batch.Execution
	if stats == nil || stats.Path != "krow-batch" || stats.RowsScanned != 3 ||
		stats.PageAccesses == 0 || stats.PageAccesses != stats.PagesRead ||
		stats.PageCacheBypasses != stats.PagesRead || stats.PageCacheHits != 0 ||
		stats.PageCacheMisses != 0 || stats.PageBytesRead == 0 ||
		stats.PageRecordsDecoded == 0 || stats.GenerationEntriesVisited < stats.RowsScanned ||
		stats.OverlayEntriesVisited != 0 {
		t.Fatalf("KROW batch physical stats = %+v", stats)
	}
	if pages := explainAnalyzeDetail(t, batch, "pages"); pages != fmt.Sprintf(
		"accessed=%d read=%d bytes=%d records_decoded=%d",
		stats.PageAccesses, stats.PagesRead, stats.PageBytesRead, stats.PageRecordsDecoded,
	) {
		t.Fatalf("KROW batch pages = %q", pages)
	}
	if cache := explainAnalyzeDetail(t, batch, "cache"); cache != fmt.Sprintf(
		"hits=0 misses=0 bypasses=%d", stats.PageCacheBypasses,
	) {
		t.Fatalf("KROW batch cache = %q", cache)
	}
	if physical := explainAnalyzeDetail(t, batch, "physical"); physical != fmt.Sprintf(
		"generation_entries=%d overlay_entries=0", stats.GenerationEntriesVisited,
	) {
		t.Fatalf("KROW batch physical = %q", physical)
	}
}

func explainAnalyzeDetail(t testing.TB, result Result, operation string) string {
	t.Helper()
	for _, row := range result.Rows {
		if len(row) == 3 && row[1] == operation {
			detail, ok := row[2].(string)
			if !ok {
				t.Fatalf("EXPLAIN ANALYZE %s detail has type %T", operation, row[2])
			}
			return detail
		}
	}
	t.Fatalf("EXPLAIN ANALYZE has no %q row: %#v", operation, result.Rows)
	return ""
}
