package relational

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kitwork/engine/internal/snapshotfile"
	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func incrementalProjectionDatabase(t testing.TB, rows int) *Engine {
	t.Helper()
	initial := projectionTestDatabase(t, rows)
	path := initial.Path()
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err := OpenWithOptions(path, Options{ExperimentalProjections: true, Kernel: kitdbengine.OpenOptions{RetainHistory: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	return engine
}

func readProjectionManifest(t testing.TB, path string) projectionManifest {
	t.Helper()
	file, err := snapshotfile.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var manifest projectionManifest
	if err := file.Metadata(&manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func projectionSchema(t testing.TB, engine *Engine, name string) kitdbsql.Schema {
	t.Helper()
	catalog, err := engine.database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := schemaFromCatalog(catalog, name)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func assertCurrentProjection(t testing.TB, engine *Engine, query string, before Result) {
	t.Helper()
	got := projectionExecute(t, engine, query)
	if got.Execution == nil || got.Execution.Path != "kcol-batch" || !reflect.DeepEqual(got.Rows, before.Rows) {
		t.Fatalf("columnar result %+v, expected rows %+v", got, before.Rows)
	}
}

func TestColumnarIncrementalReusesUnchangedChunks(t *testing.T) {
	const rows = 2*columnarChunkRows + 17
	engine := incrementalProjectionDatabase(t, rows)
	ctx := context.Background()
	first, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.AnalyticsSourceRows != rows || first.AnalyticsBuiltChunks != 3 || first.AnalyticsReusedRows != 0 {
		t.Fatalf("initial chunk build: %+v", first)
	}
	unchanged, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.AnalyticsSourceRows != 0 || unchanged.AnalyticsReusedRows != rows || unchanged.AnalyticsReusedChunks != 3 || unchanged.AnalyticsBuiltChunks != 0 {
		t.Fatalf("unchanged refresh scanned source: %+v", unchanged)
	}
	schema := projectionSchema(t, engine, "products")
	old := readProjectionManifest(t, first.AnalyticsFile).Tables[schema.ID]
	key, err := rowKey(schema, map[string]any{"id": int64(1)}, old.Generation)
	if err != nil {
		t.Fatal(err)
	}
	dirty := sort.Search(len(old.Chunks), func(i int) bool { return bytes.Compare(old.Chunks[i].End, key) > 0 })
	projectionExecute(t, engine, `UPDATE products SET price = 900 WHERE id = 1`)
	query := `SELECT SUM(price), COUNT(price), COUNT(*) FROM products`
	expected := projectionExecute(t, engine, query)
	updated, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AnalyticsSourceRows != old.Chunks[dirty].Rows || updated.AnalyticsReusedRows != rows-old.Chunks[dirty].Rows || updated.AnalyticsReusedChunks != 2 || updated.AnalyticsBuiltChunks != 1 || updated.AnalyticsCopiedBytes != 0 || updated.AnalyticsReferencedBytes == 0 || updated.AnalyticsPublication != "append" || updated.AnalyticsFallback != "" {
		t.Fatalf("single-row update rebuilt unrelated chunks: %+v", updated)
	}
	assertCurrentProjection(t, engine, query, expected)
	t.Logf("rows=%d source_rows=%d reused_rows=%d referenced_bytes=%d written_bytes=%d", rows, updated.AnalyticsSourceRows, updated.AnalyticsReusedRows, updated.AnalyticsReferencedBytes, updated.AnalyticsWrittenBytes)
}

func TestColumnarIncrementalInsertDeleteAndEmptyRanges(t *testing.T) {
	engine := incrementalProjectionDatabase(t, columnarChunkRows+4)
	ctx := context.Background()
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	query := `SELECT SUM(price), COUNT(price), MIN(price), MAX(price), COUNT(*) FROM products`
	for _, sql := range []string{
		`INSERT INTO products (id, name, price) VALUES (-10, 'before', 1000)`,
		`INSERT INTO products (id, name, price) VALUES (999999, 'after', 2000)`,
		`DELETE FROM products WHERE id = 2`,
		`UPDATE products SET price = NULL WHERE id = 3`,
	} {
		projectionExecute(t, engine, sql)
		expected := projectionExecute(t, engine, query)
		report, err := engine.RefreshAnalytics(ctx)
		if err != nil || report.AnalyticsFallback != "" || report.AnalyticsReusedChunks == 0 {
			t.Fatalf("incremental %s: %+v %v", sql, report, err)
		}
		assertCurrentProjection(t, engine, query, expected)
	}
	empty := incrementalProjectionDatabase(t, 3)
	if _, err := empty.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`DELETE FROM products WHERE id >= 0`,
		`INSERT INTO products (id, name, price) VALUES (1, 'returned', 7)`,
	} {
		projectionExecute(t, empty, sql)
		expected := projectionExecute(t, empty, query)
		report, err := empty.RefreshAnalytics(ctx)
		if err != nil || report.AnalyticsFallback != "" {
			t.Fatalf("empty range rebuild: %+v %v", report, err)
		}
		assertCurrentProjection(t, empty, query, expected)
	}
}

func TestColumnarIncrementalSplitsChunkAndReusesEmptyRange(t *testing.T) {
	engine := incrementalProjectionDatabase(t, 0)
	ctx := context.Background()
	for start := 0; start < columnarChunkRows+8; start += 128 {
		var insert strings.Builder
		insert.WriteString("INSERT INTO products (id, name, price) VALUES ")
		for i := start; i < min(start+128, columnarChunkRows+8); i++ {
			if i > start {
				insert.WriteByte(',')
			}
			fmt.Fprintf(&insert, "(%d, 'even', %d)", i*2, i)
		}
		projectionExecute(t, engine, insert.String())
	}
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	query := `SELECT COUNT(*), SUM(price), MIN(price), MAX(price) FROM products`
	for _, step := range []struct {
		sql           string
		source, reuse uint64
		built, copied int
	}{
		{`INSERT INTO products (id, name, price) VALUES (1, 'odd', 999)`, columnarChunkRows + 1, 8, 2, 1},
		{`DELETE FROM products WHERE id >= 0 AND id < 16382`, 0, 9, 1, 2},
		{`INSERT INTO products (id, name, price) VALUES (1, 'returned', 1000)`, 1, 9, 1, 2},
	} {
		projectionExecute(t, engine, step.sql)
		expected := projectionExecute(t, engine, query)
		report, err := engine.RefreshAnalytics(ctx)
		if err != nil || report.AnalyticsFallback != "" || report.AnalyticsSourceRows != step.source || report.AnalyticsReusedRows != step.reuse || report.AnalyticsBuiltChunks != step.built || report.AnalyticsReusedChunks != step.copied {
			t.Fatalf("chunk split/empty interval after %s: %+v %v", step.sql, report, err)
		}
		assertCurrentProjection(t, engine, query, expected)
	}
}

func TestColumnarIncrementalReuseSurvivesRestart(t *testing.T) {
	engine := incrementalProjectionDatabase(t, columnarChunkRows+8)
	ctx := context.Background()
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	projectionExecute(t, engine, `UPDATE products SET price = 444 WHERE id = 1`)
	query := `SELECT COUNT(*), SUM(price) FROM products`
	expected := projectionExecute(t, engine, query)
	path := engine.Path()
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(path, Options{ExperimentalProjections: true, Kernel: kitdbengine.OpenOptions{RetainHistory: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	report, err := reopened.RefreshAnalytics(ctx)
	if err != nil || report.AnalyticsFallback != "" || report.AnalyticsSourceRows != columnarChunkRows || report.AnalyticsReusedRows != 8 {
		t.Fatalf("restarted incremental refresh: %+v %v", report, err)
	}
	assertCurrentProjection(t, reopened, query, expected)
}

func TestColumnarIncrementalKeyMoveDirtiesBothRanges(t *testing.T) {
	engine := incrementalProjectionDatabase(t, 2*columnarChunkRows+17)
	ctx := context.Background()
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Standalone UPDATE does not change primary keys. A delete/insert in one
	// transaction exercises the same two-key history coverage without that API.
	for _, query := range []string{
		`DELETE FROM products WHERE id = 1`,
		`INSERT INTO products (id, name, price) VALUES (999999, 'moved', 1234)`,
	} {
		if _, err := tx.Execute(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	query := `SELECT COUNT(*), SUM(price), SUM(id) FROM products`
	expected := projectionExecute(t, engine, query)
	report, err := engine.RefreshAnalytics(ctx)
	if err != nil || report.AnalyticsFallback != "" || report.AnalyticsSourceRows != columnarChunkRows+17 || report.AnalyticsReusedRows != columnarChunkRows || report.AnalyticsBuiltChunks != 2 || report.AnalyticsReusedChunks != 1 {
		t.Fatalf("two-key mutation coverage: %+v %v", report, err)
	}
	assertCurrentProjection(t, engine, query, expected)
}

func TestColumnarIncrementalProcessExitDuringBuild(t *testing.T) {
	const childPath = "KITDB_COLUMNAR_CRASH_TEST_PATH"
	if path := os.Getenv(childPath); path != "" {
		engine, err := OpenWithOptions(path, Options{ExperimentalProjections: true, Kernel: kitdbengine.OpenOptions{RetainHistory: true}})
		if err != nil {
			t.Fatal(err)
		}
		defer engine.Close()
		// Exit without deferred cleanup only after the new container has data.
		stat, err := os.Stat(path + ".analytics")
		if err != nil {
			t.Fatal(err)
		}
		ctx := columnarExitContext{Context: context.Background(), directory: filepath.Dir(path), appendPath: path + ".analytics", appendSize: stat.Size()}
		if _, err := engine.RefreshAnalytics(ctx); err != nil {
			t.Fatal(err)
		}
		t.Fatal("refresh completed without exercising process exit")
	}
	engine := incrementalProjectionDatabase(t, columnarChunkRows+8)
	ctx := context.Background()
	projectionExecute(t, engine, `ALTER TABLE products SET PARTITION BY RANGE (id)`)
	first, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if table := readProjectionManifest(t, first.AnalyticsFile).Tables[projectionSchema(t, engine, "products").ID]; table.ChunkVersion != columnarRangeChunkVersion {
		t.Fatalf("hard-exit fixture chunk version = %d", table.ChunkVersion)
	}
	published, err := os.ReadFile(first.AnalyticsFile)
	if err != nil {
		t.Fatal(err)
	}
	projectionExecute(t, engine, `UPDATE products SET price = 777 WHERE id = 1`)
	query := `SELECT COUNT(*), SUM(price) FROM products`
	expected := projectionExecute(t, engine, query)
	path := engine.Path()
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestColumnarIncrementalProcessExitDuringBuild$", "-test.timeout=30s")
	child.Env = append(os.Environ(), childPath+"="+path)
	output, err := child.CombinedOutput()
	if child.ProcessState == nil || child.ProcessState.ExitCode() != 73 {
		t.Fatalf("child did not exit during staging: %v\n%s", err, output)
	}
	after, err := os.ReadFile(first.AnalyticsFile)
	if err != nil || len(after) < len(published) || !bytes.Equal(after[:len(published)], published) {
		t.Fatalf("process exit changed published projection: %v", err)
	}
	reopened, err := OpenWithOptions(path, Options{ExperimentalProjections: true, Kernel: kitdbengine.OpenOptions{RetainHistory: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	latest := projectionExecute(t, reopened, query)
	if latest.Execution == nil || latest.Execution.Path != "krow-batch" || !reflect.DeepEqual(latest.Rows, expected.Rows) {
		t.Fatalf("source recovery after process exit: %+v", latest)
	}
	report, err := reopened.RefreshAnalytics(ctx)
	if err != nil || report.AnalyticsFallback != "" || report.AnalyticsSourceRows != columnarChunkRows || report.AnalyticsReusedRows != 8 {
		t.Fatalf("refresh after process exit: %+v %v", report, err)
	}
	assertCurrentProjection(t, reopened, query, expected)
}

type columnarExitContext struct {
	context.Context
	directory  string
	appendPath string
	appendSize int64
}

func (ctx columnarExitContext) Err() error {
	if ctx.appendPath != "" {
		if stat, err := os.Stat(ctx.appendPath); err == nil && stat.Size() > ctx.appendSize {
			os.Exit(73)
		}
	}
	entries, err := os.ReadDir(ctx.directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".projection-") {
			info, err := entry.Info()
			if err == nil && info.Size() > 4096 {
				os.Exit(73)
			}
		}
	}
	return ctx.Context.Err()
}

func TestColumnarIncrementalCatalogChangesKeepUnchangedTables(t *testing.T) {
	engine := incrementalProjectionDatabase(t, 32)
	ctx := context.Background()
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	projectionExecute(t, engine, `CREATE TABLE clicks (id INTEGER PRIMARY KEY, visits INTEGER)`)
	projectionExecute(t, engine, `INSERT INTO clicks VALUES (1, 10)`)
	query := `SELECT SUM(price), COUNT(price) FROM products`
	expected := projectionExecute(t, engine, query)
	report, err := engine.RefreshAnalytics(ctx)
	if err != nil || report.AnalyticsSourceRows != 1 || report.AnalyticsReusedRows != 32 || report.AnalyticsTables != 2 {
		t.Fatalf("new table invalidated unchanged table: %+v %v", report, err)
	}
	assertCurrentProjection(t, engine, query, expected)
	projectionExecute(t, engine, `ALTER TABLE products ADD COLUMN extra INTEGER`)
	report, err = engine.RefreshAnalytics(ctx)
	if err != nil || report.AnalyticsSourceRows != 32 || report.AnalyticsReusedRows != 1 {
		t.Fatalf("schema change reuse: %+v %v", report, err)
	}
	projectionExecute(t, engine, `DROP TABLE clicks`)
	report, err = engine.RefreshAnalytics(ctx)
	if err != nil || report.AnalyticsTables != 1 || report.AnalyticsSourceRows != 0 || report.AnalyticsReusedRows != 32 {
		t.Fatalf("dropped table remains in projection: %+v %v", report, err)
	}
}

func TestColumnarIncrementalFallsBackWithoutHistoryProof(t *testing.T) {
	for _, mode := range []string{"disabled", "pruned"} {
		t.Run(mode, func(t *testing.T) {
			var engine *Engine
			if mode == "disabled" {
				engine = projectionTestDatabase(t, 32)
			} else {
				engine = incrementalProjectionDatabase(t, 32)
			}
			ctx := context.Background()
			if _, err := engine.RefreshAnalytics(ctx); err != nil {
				t.Fatal(err)
			}
			projectionExecute(t, engine, `UPDATE products SET price = 555 WHERE id = 1`)
			if mode == "pruned" {
				boundary, err := engine.database.Checkpoint()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := engine.database.PruneHistory(ctx, boundary); err != nil {
					t.Fatal(err)
				}
			}
			query := `SELECT SUM(price), COUNT(price) FROM products`
			expected := projectionExecute(t, engine, query)
			report, err := engine.RefreshAnalytics(ctx)
			if err != nil || report.AnalyticsSourceRows != 32 || report.AnalyticsReusedRows != 0 || !strings.Contains(report.AnalyticsFallback, "history") {
				t.Fatalf("unproven reuse: %+v %v", report, err)
			}
			assertCurrentProjection(t, engine, query, expected)
		})
	}
}

func TestColumnarIncrementalIgnoresCommitsAfterCapturedSnapshot(t *testing.T) {
	engine := incrementalProjectionDatabase(t, 32)
	ctx := context.Background()
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	projectionExecute(t, engine, `UPDATE products SET price = 111 WHERE id = 1`)
	query := `SELECT SUM(price), COUNT(price) FROM products`
	expected := projectionExecute(t, engine, query)
	tx, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	projectionExecute(t, engine, `UPDATE products SET price = 222 WHERE id = 2`)
	latest := projectionExecute(t, engine, query)
	var report ProjectionReport
	if err := engine.refreshColumnarSnapshot(ctx, tx, &report); err != nil {
		t.Fatal(err)
	}
	manifest := readProjectionManifest(t, engine.Path()+".analytics")
	if manifest.Cursor != tx.snapshot.HistoryCursor() {
		t.Fatalf("published a newer cursor: %+v", manifest.Cursor)
	}
	oldResult, err := tx.Execute(ctx, query)
	if err != nil || oldResult.Execution == nil || oldResult.Execution.Path != "kcol-batch" || !reflect.DeepEqual(oldResult.Rows, expected.Rows) {
		t.Fatalf("captured snapshot: %+v %v", oldResult, err)
	}
	current := projectionExecute(t, engine, query)
	if current.Execution == nil || current.Execution.Path != "krow-batch" || !reflect.DeepEqual(current.Rows, latest.Rows) {
		t.Fatalf("future write lost: %+v", current)
	}
}

func TestColumnarIncrementalCorruptReuseRebuildsFromSource(t *testing.T) {
	engine := incrementalProjectionDatabase(t, columnarChunkRows+17)
	ctx := context.Background()
	first, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	query := `SELECT SUM(price), COUNT(price) FROM products`
	expected := projectionExecute(t, engine, query)
	schema := projectionSchema(t, engine, "products")
	manifest := readProjectionManifest(t, first.AnalyticsFile)
	reader, err := snapshotfile.Open(first.AnalyticsFile)
	if err != nil {
		t.Fatal(err)
	}
	section, err := reader.Section(schema.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, base, _ := section.Outer()
	reader.Close()
	chunk := manifest.Tables[schema.ID].Chunks[1]
	file, err := os.OpenFile(first.AnalyticsFile, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	offset := base + chunk.Offset + chunk.Length - 1
	if _, err := file.ReadAt(data[:], offset); err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if _, err := file.WriteAt(data[:], offset); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := engine.RefreshAnalytics(ctx)
	if err != nil || report.AnalyticsReusedRows != 0 || report.AnalyticsSourceRows != columnarChunkRows+17 || !strings.Contains(report.AnalyticsFallback, "verification") {
		t.Fatalf("corrupt chunk published: %+v %v", report, err)
	}
	assertCurrentProjection(t, engine, query, expected)
}

func TestColumnarIncrementalCopyDoesNotRetryDestinationFailure(t *testing.T) {
	engine := incrementalProjectionDatabase(t, 32)
	report, err := engine.RefreshAnalytics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	schema := projectionSchema(t, engine, "products")
	chunk := readProjectionManifest(t, report.AnalyticsFile).Tables[schema.ID].Chunks[0]
	file, err := snapshotfile.Open(report.AnalyticsFile)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	section, err := file.Section(schema.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := columnar.Open(section)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []error{io.ErrClosedPipe, io.ErrShortWrite} {
		writer := &columnarCountingWriter{output: columnarFaultWriter{failure}}
		_, err := copyColumnarChunk(context.Background(), reader, writer, chunk)
		if !errors.Is(err, failure) || errors.Is(err, errColumnarReuse) {
			t.Fatalf("destination failure misclassified for retry: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := copyColumnarChunk(ctx, reader, &columnarCountingWriter{output: io.Discard}, chunk); !errors.Is(err, context.Canceled) || errors.Is(err, errColumnarReuse) {
		t.Fatalf("cancellation misclassified for retry: %v", err)
	}
	chunk.Rows++
	if _, err := copyColumnarChunk(context.Background(), reader, &columnarCountingWriter{output: io.Discard}, chunk); !errors.Is(err, errColumnarReuse) {
		t.Fatalf("bad copied row count did not request a rebuild: %v", err)
	}
}

type columnarFaultWriter struct{ err error }

func (writer columnarFaultWriter) Write(data []byte) (int, error) {
	if writer.err == io.ErrShortWrite {
		return len(data) - 1, nil
	}
	return 0, writer.err
}

func rewriteColumnarManifest(t *testing.T, path string, mutate func(*projectionManifest)) {
	t.Helper()
	reader, err := snapshotfile.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var manifest projectionManifest
	if err := reader.Metadata(&manifest); err != nil {
		t.Fatal(err)
	}
	writer, err := snapshotfile.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for id := range manifest.Tables {
		section, err := reader.Section(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Add(id, func(output io.Writer) error { return snapshotfile.Copy(context.Background(), output, section) }); err != nil {
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

func TestColumnarIncrementalRejectsLegacyAndInvalidChunkMetadata(t *testing.T) {
	for _, mode := range []string{"legacy", "gap", "cursor"} {
		t.Run(mode, func(t *testing.T) {
			engine := incrementalProjectionDatabase(t, 32)
			ctx := context.Background()
			first, err := engine.RefreshAnalytics(ctx)
			if err != nil {
				t.Fatal(err)
			}
			rewriteColumnarManifest(t, first.AnalyticsFile, func(manifest *projectionManifest) {
				for id, table := range manifest.Tables {
					switch mode {
					case "legacy":
						table.ChunkVersion, table.Chunks = 0, nil
					case "gap":
						table.Chunks[0].Start = append(table.Chunks[0].Start, 0)
					case "cursor":
						manifest.Cursor.Checksum ^= 1
					}
					manifest.Tables[id] = table
				}
			})
			projectionExecute(t, engine, `UPDATE products SET price = 333 WHERE id = 1`)
			query := `SELECT SUM(price), COUNT(price) FROM products`
			expected := projectionExecute(t, engine, query)
			report, err := engine.RefreshAnalytics(ctx)
			if err != nil || report.AnalyticsSourceRows != 32 || report.AnalyticsReusedRows != 0 || report.AnalyticsFallback == "" {
				t.Fatalf("invalid reuse metadata accepted: %+v %v", report, err)
			}
			assertCurrentProjection(t, engine, query, expected)
		})
	}
}

func BenchmarkColumnarIncrementalRefresh(b *testing.B) {
	for _, incremental := range []bool{false, true} {
		name := "full"
		if incremental {
			name = "history-chunks"
		}
		b.Run(name, func(b *testing.B) {
			var engine *Engine
			if incremental {
				engine = incrementalProjectionDatabase(b, 4*columnarChunkRows)
			} else {
				engine = projectionTestDatabase(b, 4*columnarChunkRows)
			}
			ctx := context.Background()
			if _, err := engine.RefreshAnalytics(ctx); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			var report ProjectionReport
			iteration := 0
			var written, referenced, copied int64
			compactions := 0
			for b.Loop() {
				iteration++
				if _, err := engine.Execute(ctx, fmt.Sprintf("UPDATE products SET price = %d WHERE id = 1", 100000+iteration)); err != nil {
					b.Fatal(err)
				}
				var err error
				report, err = engine.RefreshAnalytics(ctx)
				if err != nil {
					b.Fatal(err)
				}
				written += report.AnalyticsWrittenBytes
				referenced += report.AnalyticsReferencedBytes
				copied += report.AnalyticsCopiedBytes
				if report.AnalyticsPublication == "compact" {
					compactions++
				}
			}
			b.ReportMetric(float64(report.AnalyticsSourceRows), "source-rows/op")
			b.ReportMetric(float64(report.AnalyticsReusedRows), "reused-rows/op")
			b.ReportMetric(float64(copied)/float64(iteration), "copied-bytes/op")
			b.ReportMetric(float64(written)/float64(iteration), "written-bytes/op")
			b.ReportMetric(float64(referenced)/float64(iteration), "referenced-bytes/op")
			b.ReportMetric(float64(compactions)/float64(iteration), "compactions/op")
			if incremental && (report.AnalyticsSourceRows != columnarChunkRows || report.AnalyticsReusedRows != 3*columnarChunkRows) {
				b.Fatalf("incremental path not used: %+v", report)
			}
		})
	}
}
