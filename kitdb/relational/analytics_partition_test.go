package relational

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/internal/snapshotfile"
	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestProjectionChunkPartitionEncodingIsOptionalAndBackwardCompatible(t *testing.T) {
	encoded, err := json.Marshal(projectionChunk{Rows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"Partition"`)) {
		t.Fatalf("unpartitioned chunk encoded partition routing metadata: %s", encoded)
	}
	tableEncoded, err := json.Marshal(projectionTable{Rows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(tableEncoded, []byte(`"BlockDirectory"`)) {
		t.Fatalf("unpartitioned table encoded a block directory: %s", tableEncoded)
	}

	legacy := []byte(`{"Rows":8,"Partition":{"Version":1,"HasValue":true,"Minimum":7,"Maximum":9}}`)
	var chunk projectionChunk
	if err := json.Unmarshal(legacy, &chunk); err != nil {
		t.Fatal(err)
	}
	if chunk.Rows != 8 || chunk.Partition.Version != 1 || !chunk.Partition.HasValue ||
		chunk.Partition.Minimum != 7 || chunk.Partition.Maximum != 9 {
		t.Fatalf("decoded legacy partition chunk = %+v", chunk)
	}
}

func TestAlterPartitioningInvalidatesRebuildsAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alter-partition.kitdb")
	engine, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	const rows = 2 * columnarChunkRows
	insertPartitionRows(t, engine, rows, func(row int) int { return row / columnarChunkRows * 100 })
	if _, err := engine.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := engine.Execute(ctx, `ALTER TABLE metrics SET PARTITION BY RANGE (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	assertPartitionPolicy(t, engine, "range", "tenant_id")
	query := `SELECT COUNT(*), SUM(value) FROM metrics WHERE tenant_id >= 100`
	stale := projectionExecute(t, engine, query)
	if stale.Execution == nil || stale.Execution.Path != "krow-batch" || !strings.Contains(stale.Execution.Fallback, "stale") {
		t.Fatalf("stale analytics did not fail open to KROW: %+v", stale.Execution)
	}
	rebuilt, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.AnalyticsBuiltChunks != 2 || rebuilt.AnalyticsReusedChunks != 0 {
		t.Fatalf("partition rebuild report: %+v", rebuilt)
	}
	partitioned := projectionExecute(t, engine, query)
	if !reflect.DeepEqual(partitioned.Rows, stale.Rows) || partitioned.Execution == nil ||
		partitioned.Execution.Path != "kcol-batch" || partitioned.Execution.ChunksSkipped != 1 ||
		partitioned.Execution.ChunksScanned != 1 {
		t.Fatalf("partitioned result=%+v stats=%+v, baseline=%+v", partitioned.Rows, partitioned.Execution, stale.Rows)
	}

	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err = OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	assertPartitionPolicy(t, engine, "range", "tenant_id")
	reopened := projectionExecute(t, engine, query)
	if !reflect.DeepEqual(reopened.Rows, partitioned.Rows) || reopened.Execution == nil ||
		reopened.Execution.Path != "kcol-batch" || reopened.Execution.ChunksSkipped != 1 {
		t.Fatalf("reopened result=%+v stats=%+v", reopened.Rows, reopened.Execution)
	}

	if _, err := engine.Execute(ctx, `ALTER TABLE metrics DROP PARTITIONING`); err != nil {
		t.Fatal(err)
	}
	assertPartitionPolicy(t, engine, "", "")
	stale = projectionExecute(t, engine, query)
	if stale.Execution == nil || stale.Execution.Path != "krow-batch" || !strings.Contains(stale.Execution.Fallback, "stale") {
		t.Fatalf("dropped partitioning did not invalidate analytics: %+v", stale.Execution)
	}
	rebuilt, err = engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.AnalyticsBuiltChunks != 2 || rebuilt.AnalyticsReusedChunks != 0 {
		t.Fatalf("drop partitioning rebuild report: %+v", rebuilt)
	}
	unpartitioned := projectionExecute(t, engine, query)
	if !reflect.DeepEqual(unpartitioned.Rows, stale.Rows) || unpartitioned.Execution == nil ||
		unpartitioned.Execution.Path != "kcol-batch" || unpartitioned.Execution.ChunksSkipped != 0 {
		t.Fatalf("unpartitioned result=%+v stats=%+v", unpartitioned.Rows, unpartitioned.Execution)
	}
}

func TestAlterPartitioningFailsClosedWithoutChangingCatalog(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "partition-safety.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER, label TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE metrics SET PARTITION BY HASH (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	before, err := engine.schemaLocked("metrics")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`ALTER TABLE metrics SET PARTITION BY HASH (tenant_id)`,
		`ALTER TABLE metrics SET PARTITION BY RANGE (label)`,
		`ALTER TABLE metrics SET PARTITION BY RANGE (missing)`,
		`ALTER TABLE metrics DROP COLUMN tenant_id`,
	} {
		if _, err := engine.Execute(ctx, query); err == nil {
			t.Fatalf("%q unexpectedly succeeded", query)
		}
		after, schemaErr := engine.schemaLocked("metrics")
		if schemaErr != nil {
			t.Fatal(schemaErr)
		}
		if after.Hash != before.Hash || after.Partition == nil || *after.Partition != *before.Partition {
			t.Fatalf("failed ALTER changed catalog: before=%+v after=%+v", before.Partition, after.Partition)
		}
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE metrics DROP PARTITIONING`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `ALTER TABLE metrics DROP PARTITIONING`); err == nil {
		t.Fatal("second DROP PARTITIONING unexpectedly succeeded")
	}
}

func assertPartitionPolicy(t *testing.T, engine *Engine, strategy, fieldName string) {
	t.Helper()
	schema, err := engine.schemaLocked("metrics")
	if err != nil {
		t.Fatal(err)
	}
	if strategy == "" {
		if schema.Partition != nil {
			t.Fatalf("partition = %+v, want nil", schema.Partition)
		}
		return
	}
	if schema.Version != kitdbsql.SchemaVersion8 || schema.Partition == nil || schema.Partition.Strategy != strategy {
		t.Fatalf("partition = %+v schema version=%d", schema.Partition, schema.Version)
	}
	_, field, found := schema.FieldByName(fieldName)
	if !found || schema.Partition.Field != field.Tag {
		t.Fatalf("partition field tag=%d, field=%+v found=%t", schema.Partition.Field, field, found)
	}
}

func TestAnalyticsRangePartitionPrunesWholeChunkExtents(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "range.kitdb"), Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL) PARTITION BY RANGE (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	const rows = 4 * columnarChunkRows
	insertPartitionRows(t, engine, rows, func(row int) int { return row / columnarChunkRows * 100 })
	query := `SELECT COUNT(*), SUM(value) FROM metrics WHERE tenant_id >= 200`
	engine.experimentalProjections, engine.batchAggregates = false, false
	want := projectionExecute(t, engine, query)
	engine.experimentalProjections, engine.batchAggregates = true, true
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	unchanged, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.AnalyticsPublication != "unchanged" || unchanged.AnalyticsBuiltChunks != 0 ||
		unchanged.AnalyticsReusedChunks != 4 || unchanged.AnalyticsWrittenBytes != 0 {
		t.Fatalf("unchanged partition refresh: %+v", unchanged)
	}
	got := projectionExecute(t, engine, query)
	if !reflect.DeepEqual(got.Rows, want.Rows) || got.Execution == nil || got.Execution.Path != "kcol-batch" {
		t.Fatalf("range result: got=%+v stats=%+v want=%+v", got.Rows, got.Execution, want.Rows)
	}
	if got.Execution.ChunksSkipped != 2 || got.Execution.ChunksScanned != 2 ||
		got.Execution.RowsSkipped != 2*columnarChunkRows ||
		got.Execution.RowsFromMetadata != 2*columnarChunkRows || got.Execution.RowsScanned != 0 {
		t.Fatalf("range chunk pruning: %+v", got.Execution)
	}
}

func TestAnalyticsRangePartitionClustersBlocksWithinChunk(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "range-cluster.kitdb"), Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL) PARTITION BY RANGE (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	insertPartitionRows(t, engine, columnarChunkRows, func(row int) int { return row % 8 })
	query := `SELECT COUNT(*), SUM(value) FROM metrics WHERE tenant_id = 7`
	engine.experimentalProjections, engine.batchAggregates = false, false
	want := projectionExecute(t, engine, query)
	engine.experimentalProjections, engine.batchAggregates = true, true
	report, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.AnalyticsBuiltChunks != 1 {
		t.Fatalf("cluster build report: %+v", report)
	}
	manifest := readProjectionManifest(t, report.AnalyticsFile)
	schema := projectionSchema(t, engine, "metrics")
	if manifest.Tables[schema.ID].ChunkVersion != columnarRangeChunkVersion {
		t.Fatalf("cluster chunk version = %d", manifest.Tables[schema.ID].ChunkVersion)
	}
	table := manifest.Tables[schema.ID]
	if table.BlockDirectoryVersion != blockPartitionVersion || len(table.BlockDirectory) != 8*blockPartitionEntryBytes {
		t.Fatalf("block directory version=%d bytes=%d", table.BlockDirectoryVersion, len(table.BlockDirectory))
	}
	got := projectionExecute(t, engine, query)
	if !reflect.DeepEqual(got.Rows, want.Rows) || got.Execution == nil || got.Execution.Path != "kcol-batch" ||
		got.Execution.ChunksScanned != 1 || got.Execution.ChunksSkipped != 0 ||
		got.Execution.RowsSkipped != 7*columnar.BatchRows || got.Execution.RowsFromMetadata != columnar.BatchRows ||
		got.Execution.RowsScanned != 0 || got.Execution.ColumnarBlockHeadersRead != 1 ||
		got.Execution.ColumnarPayloadsRead != 0 || got.Execution.ColumnarBlocksPruned != 7 ||
		got.Execution.ColumnarRowsPruned != 7*columnar.BatchRows {
		t.Fatalf("range-clustered result=%+v stats=%+v want=%+v", got.Rows, got.Execution, want.Rows)
	}
	unchanged, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.AnalyticsPublication != "unchanged" || unchanged.AnalyticsReusedChunks != 1 {
		t.Fatalf("cluster reuse report: %+v", unchanged)
	}
}

func TestAnalyticsRefreshUpgradesBlockDirectoryWithoutRewritingKCOL(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "range-directory-upgrade.kitdb"), Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL) PARTITION BY RANGE (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	insertPartitionRows(t, engine, columnarChunkRows, func(row int) int { return row % 8 })
	first, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	appendProjectionManifest(t, first.AnalyticsFile, func(manifest *projectionManifest) {
		for id, table := range manifest.Tables {
			table.BlockDirectoryVersion, table.BlockDirectory = 0, nil
			manifest.Tables[id] = table
		}
	})

	upgraded, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.AnalyticsPublication != "append" || upgraded.AnalyticsSourceRows != 0 ||
		upgraded.AnalyticsReusedRows != columnarChunkRows || upgraded.AnalyticsReferencedBytes == 0 ||
		upgraded.AnalyticsCopiedBytes != 0 || upgraded.AnalyticsWrittenBytes >= upgraded.AnalyticsReferencedBytes {
		t.Fatalf("directory upgrade report: %+v", upgraded)
	}
	manifest := readProjectionManifest(t, upgraded.AnalyticsFile)
	for _, table := range manifest.Tables {
		if len(table.Chunks) != 1 || tableBlockDirectoryIsZero(table) {
			t.Fatalf("upgraded block directory version=%d bytes=%d", table.BlockDirectoryVersion, len(table.BlockDirectory))
		}
	}
	unchanged, err := engine.RefreshAnalytics(ctx)
	if err != nil || unchanged.AnalyticsPublication != "unchanged" || unchanged.AnalyticsWrittenBytes != 0 {
		t.Fatalf("post-upgrade refresh: %+v, %v", unchanged, err)
	}
}

func TestAnalyticsBlockDirectoryCorruptionFallsBackToKROW(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "range-directory-corrupt.kitdb"), Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL) PARTITION BY RANGE (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	insertPartitionRows(t, engine, columnarChunkRows, func(row int) int { return row % 8 })
	report, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	appendProjectionManifest(t, report.AnalyticsFile, func(manifest *projectionManifest) {
		for id, table := range manifest.Tables {
			table.BlockDirectory[2] ^= 1
			manifest.Tables[id] = table
		}
	})
	result := projectionExecute(t, engine, `SELECT COUNT(*) FROM metrics WHERE tenant_id = 7`)
	if result.Execution == nil || result.Execution.Path != "krow-batch" ||
		!strings.Contains(result.Execution.Fallback, "invalid analytics chunk manifest") {
		t.Fatalf("corrupt directory did not fail open: %+v", result.Execution)
	}
}

func TestColumnarBlockDirectoryBudgetDropsOnlyOptionalRoutingMetadata(t *testing.T) {
	var stats columnarRefreshStats
	var table projectionTable
	entries := make([]byte, projectionBlockCount(columnarChunkRows)*blockPartitionEntryBytes)
	chunks := columnarBlockDirectoryBytes/len(entries) + 1
	for range chunks {
		chunk := projectionChunk{
			Rows: columnarChunkRows,
			blocks: projectionBlockDirectory{
				Version: blockPartitionVersion,
				Entries: entries,
			},
		}
		if err := stats.addChunk(&table, chunk, false); err != nil {
			t.Fatal(err)
		}
	}
	if !tableBlockDirectoryIsZero(table) || stats.blockDirectory != 0 || len(table.Chunks) != chunks {
		t.Fatalf("budget result: directory=%d stats=%d chunks=%d", len(table.BlockDirectory), stats.blockDirectory, len(table.Chunks))
	}
}

func appendProjectionManifest(t *testing.T, path string, mutate func(*projectionManifest)) {
	t.Helper()
	reader, err := snapshotfile.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest projectionManifest
	if err := reader.Metadata(&manifest); err != nil {
		reader.Close()
		t.Fatal(err)
	}
	writer, err := snapshotfile.AppendGeneration(path, reader)
	if err != nil {
		reader.Close()
		t.Fatal(err)
	}
	defer writer.Close()
	for id := range manifest.Tables {
		section, err := reader.Section(id)
		if err != nil {
			t.Fatal(err)
		}
		length := section.Size()
		if err := writer.Add(id, func(output io.Writer) error {
			return snapshotfile.Reference(output, reader, id, 0, length)
		}); err != nil {
			t.Fatal(err)
		}
	}
	mutate(&manifest)
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Publish(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyticsRangeClusterRebuildsOnlyDirtyKROWExtent(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "range-cluster-incremental.kitdb"), Options{
		ExperimentalProjections: true,
		Kernel:                  kitdbengine.OpenOptions{RetainHistory: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL) PARTITION BY RANGE (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	insertPartitionRows(t, engine, 2*columnarChunkRows, func(row int) int { return row % 8 })
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `UPDATE metrics SET tenant_id = 99 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	query := `SELECT COUNT(*), SUM(value) FROM metrics WHERE tenant_id = 99`
	engine.experimentalProjections, engine.batchAggregates = false, false
	want := projectionExecute(t, engine, query)
	engine.experimentalProjections, engine.batchAggregates = true, true
	report, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.AnalyticsBuiltChunks != 1 || report.AnalyticsReusedChunks != 1 ||
		report.AnalyticsSourceRows != columnarChunkRows || report.AnalyticsReusedRows != columnarChunkRows {
		t.Fatalf("cluster incremental report: %+v", report)
	}
	got := projectionExecute(t, engine, query)
	if !reflect.DeepEqual(got.Rows, want.Rows) || got.Execution == nil || got.Execution.Path != "kcol-batch" ||
		got.Execution.ChunksSkipped != 1 {
		t.Fatalf("cluster incremental result=%+v stats=%+v want=%+v", got.Rows, got.Execution, want.Rows)
	}
	unchanged, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.AnalyticsPublication != "unchanged" || unchanged.AnalyticsReusedChunks != 2 {
		t.Fatalf("cluster incremental reuse report: %+v", unchanged)
	}
}

func TestAnalyticsRangeClusterOrdersNullsLast(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "range-cluster-nulls.kitdb"), Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER, value INTEGER NOT NULL) PARTITION BY RANGE (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	const rows = 2 * columnar.BatchRows
	for start := 0; start < rows; start += 256 {
		var query strings.Builder
		query.WriteString(`INSERT INTO metrics (id,tenant_id,value) VALUES `)
		for row := start; row < min(start+256, rows); row++ {
			if row != start {
				query.WriteByte(',')
			}
			if row&1 == 0 {
				fmt.Fprintf(&query, "(%d,7,%d)", row, row)
			} else {
				fmt.Fprintf(&query, "(%d,NULL,%d)", row, row)
			}
		}
		if _, err := engine.Execute(ctx, query.String()); err != nil {
			t.Fatal(err)
		}
	}
	query := `SELECT COUNT(*), SUM(value) FROM metrics WHERE tenant_id IS NULL`
	engine.experimentalProjections, engine.batchAggregates = false, false
	want := projectionExecute(t, engine, query)
	engine.experimentalProjections, engine.batchAggregates = true, true
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	got := projectionExecute(t, engine, query)
	if !reflect.DeepEqual(got.Rows, want.Rows) || got.Execution == nil || got.Execution.Path != "kcol-batch" ||
		got.Execution.RowsSkipped != columnar.BatchRows || got.Execution.RowsFromMetadata != columnar.BatchRows ||
		got.Execution.RowsScanned != 0 {
		t.Fatalf("range-clustered NULL result=%+v stats=%+v want=%+v", got.Rows, got.Execution, want.Rows)
	}
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyticsHashPartitionPrunesWhenRangeCannot(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "hash.kitdb"), Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL) PARTITION BY HASH (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	const rows = 2 * columnarChunkRows
	// Every chunk spans min=1,max=100, so tenant_id=50 cannot be rejected by
	// range metadata. Its hash bucket is absent from both chunk bitmaps.
	insertPartitionRows(t, engine, rows, func(row int) int {
		if row&1 == 0 {
			return 1
		}
		return 100
	})
	query := `SELECT COUNT(*) FROM metrics WHERE tenant_id = 50`
	engine.experimentalProjections, engine.batchAggregates = false, false
	want := projectionExecute(t, engine, query)
	engine.experimentalProjections, engine.batchAggregates = true, true
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	got := projectionExecute(t, engine, query)
	if !reflect.DeepEqual(got.Rows, want.Rows) || got.Execution == nil || got.Execution.Path != "kcol-batch" {
		t.Fatalf("hash result: got=%+v stats=%+v want=%+v", got.Rows, got.Execution, want.Rows)
	}
	if got.Execution.ChunksSkipped != 2 || got.Execution.ChunksScanned != 0 ||
		got.Execution.RowsSkipped != rows || got.Execution.BatchesSkipped != 16 || got.Execution.RowsScanned != 0 {
		t.Fatalf("hash chunk pruning: %+v", got.Execution)
	}
}

func TestAnalyticsHashPartitionDirectoryPrunesBlocksBeforeHeaders(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "hash-blocks.kitdb"), Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL) PARTITION BY HASH (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	insertPartitionRows(t, engine, columnarChunkRows, func(row int) int {
		if row < 7*columnar.BatchRows {
			return 1
		}
		return 100
	})
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	result := projectionExecute(t, engine, `SELECT COUNT(*) FROM metrics WHERE tenant_id = 100`)
	if result.Execution == nil || result.Execution.Path != "kcol-batch" ||
		result.Execution.ChunksScanned != 1 || result.Execution.RowsSkipped != 7*columnar.BatchRows ||
		result.Execution.BatchesSkipped != 7 || result.Execution.RowsFromMetadata != columnar.BatchRows ||
		result.Execution.ColumnarBlockHeadersRead != 1 || result.Execution.ColumnarPayloadsRead != 0 ||
		result.Execution.ColumnarBlocksPruned != 7 || result.Execution.ColumnarRowsPruned != 7*columnar.BatchRows {
		t.Fatalf("hash block directory result=%+v stats=%+v", result.Rows, result.Execution)
	}
}

func insertPartitionRows(t testing.TB, engine *Engine, rows int, partition func(int) int) {
	t.Helper()
	ctx := context.Background()
	for start := 0; start < rows; start += 256 {
		var query strings.Builder
		query.WriteString(`INSERT INTO metrics (id,tenant_id,value) VALUES `)
		for row := start; row < min(start+256, rows); row++ {
			if row != start {
				query.WriteByte(',')
			}
			fmt.Fprintf(&query, "(%d,%d,%d)", row, partition(row), row%97)
		}
		if _, err := engine.Execute(ctx, query.String()); err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkAnalyticsRangePartitionPruning(b *testing.B) {
	for _, test := range []struct {
		name      string
		partition string
	}{
		{name: "block-statistics-only"},
		{name: "range-chunk-pruning", partition: ` PARTITION BY RANGE (tenant_id)`},
	} {
		b.Run(test.name, func(b *testing.B) {
			engine, err := OpenWithOptions(filepath.Join(b.TempDir(), "benchmark.kitdb"), Options{ExperimentalProjections: true})
			if err != nil {
				b.Fatal(err)
			}
			defer engine.Close()
			ctx := context.Background()
			create := `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL)` + test.partition
			if _, err := engine.Execute(ctx, create); err != nil {
				b.Fatal(err)
			}
			const chunks = 16
			const rows = chunks * columnarChunkRows
			for start := 0; start < rows; start += 256 {
				var query strings.Builder
				query.WriteString(`INSERT INTO metrics (id,tenant_id,value) VALUES `)
				for row := start; row < min(start+256, rows); row++ {
					if row != start {
						query.WriteByte(',')
					}
					fmt.Fprintf(&query, "(%d,%d,%d)", row, row/columnarChunkRows*100, row%97)
				}
				if _, err := engine.Execute(ctx, query.String()); err != nil {
					b.Fatal(err)
				}
			}
			// Keep the benchmark on immutable KROW segments. Otherwise every
			// read transaction also measures cloning the deliberately bounded
			// uncheckpointed overlay, which is unrelated to KCOL routing.
			if _, err := engine.Checkpoint(); err != nil {
				b.Fatal(err)
			}
			if _, err := engine.RefreshAnalytics(ctx); err != nil {
				b.Fatal(err)
			}
			query := `SELECT COUNT(*), SUM(value) FROM metrics WHERE tenant_id >= 1500`
			result, err := engine.Execute(ctx, query)
			if err != nil || result.Execution == nil || result.Execution.Path != "kcol-batch" {
				b.Fatalf("warmup: result=%+v error=%v", result, err)
			}
			skipped := result.Execution.ChunksSkipped
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := engine.Execute(ctx, query); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(skipped), "chunks_skipped")
		})
	}
}

func BenchmarkAnalyticsRangeBlockClustering(b *testing.B) {
	for _, test := range []struct {
		name      string
		partition string
	}{
		{name: "mixed-blocks"},
		{name: "range-clustered-blocks", partition: ` PARTITION BY RANGE (tenant_id)`},
	} {
		b.Run(test.name, func(b *testing.B) {
			engine, err := OpenWithOptions(filepath.Join(b.TempDir(), "benchmark.kitdb"), Options{ExperimentalProjections: true})
			if err != nil {
				b.Fatal(err)
			}
			defer engine.Close()
			ctx := context.Background()
			create := `CREATE TABLE metrics (id INTEGER PRIMARY KEY, tenant_id INTEGER NOT NULL, value INTEGER NOT NULL)` + test.partition
			if _, err := engine.Execute(ctx, create); err != nil {
				b.Fatal(err)
			}
			const chunks = 16
			insertPartitionRows(b, engine, chunks*columnarChunkRows, func(row int) int { return row % 8 })
			if _, err := engine.Checkpoint(); err != nil {
				b.Fatal(err)
			}
			if _, err := engine.RefreshAnalytics(ctx); err != nil {
				b.Fatal(err)
			}
			query := `SELECT COUNT(*), SUM(value) FROM metrics WHERE tenant_id = 7`
			result, err := engine.Execute(ctx, query)
			if err != nil || result.Execution == nil || result.Execution.Path != "kcol-batch" {
				b.Fatalf("warmup: result=%+v error=%v", result, err)
			}
			rowsSkipped, rowsMetadata := result.Execution.RowsSkipped, result.Execution.RowsFromMetadata
			blocksPruned, headersRead := result.Execution.ColumnarBlocksPruned, result.Execution.ColumnarBlockHeadersRead
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := engine.Execute(ctx, query); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(blocksPruned), "blocks_pruned")
			b.ReportMetric(float64(headersRead), "headers_read")
			b.ReportMetric(float64(rowsSkipped), "rows_skipped")
			b.ReportMetric(float64(rowsMetadata), "rows_metadata")
		})
	}
}
