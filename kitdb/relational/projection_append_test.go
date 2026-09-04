package relational

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/snapshotfile"
)

func TestColumnarAppendWritesOnlyChangedChunkAndDirectory(t *testing.T) {
	engine := incrementalProjectionDatabase(t, 4*columnarChunkRows)
	ctx := context.Background()
	first, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first.AnalyticsFile)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := engine.RefreshAnalytics(ctx)
	if err != nil || unchanged.AnalyticsPublication != "unchanged" || unchanged.AnalyticsWrittenBytes != 0 || unchanged.AnalyticsCopiedBytes != 0 {
		t.Fatalf("unchanged refresh wrote data: %+v %v", unchanged, err)
	}
	identical, err := os.ReadFile(first.AnalyticsFile)
	if err != nil || !bytes.Equal(before, identical) {
		t.Fatalf("unchanged file changed: %v", err)
	}
	projectionExecute(t, engine, `UPDATE products SET price = 3333 WHERE id = 1`)
	query := `SELECT COUNT(*), SUM(price), AVG(rating), MIN(id), MAX(id) FROM products`
	expected := projectionExecute(t, engine, query)
	report, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(first.AnalyticsFile)
	if err != nil {
		t.Fatal(err)
	}
	if report.AnalyticsPublication != "append" || report.AnalyticsCopiedBytes != 0 || report.AnalyticsSourceRows != columnarChunkRows || report.AnalyticsReusedRows != 3*columnarChunkRows || report.AnalyticsReferencedBytes == 0 || report.AnalyticsWrittenBytes >= first.AnalyticsWrittenBytes/2 || report.AnalyticsFileBytes != int64(len(after)) {
		t.Fatalf("append did not avoid full-file writes: first=%+v, next=%+v", first, report)
	}
	// Only the two reserved root pages can change within the old file prefix.
	if len(after) <= len(before) || !bytes.Equal(before[8192:], after[8192:len(before)]) {
		t.Fatal("append rewrote old immutable bytes")
	}
	assertCurrentProjection(t, engine, query, expected)
	t.Logf("rows=%d full_written=%d append_written=%d referenced=%d copied=%d file=%d", 4*columnarChunkRows, first.AnalyticsWrittenBytes, report.AnalyticsWrittenBytes, report.AnalyticsReferencedBytes, report.AnalyticsCopiedBytes, report.AnalyticsFileBytes)
}

func TestColumnarAppendCompactsAfterBoundedGarbage(t *testing.T) {
	engine := incrementalProjectionDatabase(t, 2*columnarChunkRows)
	ctx := context.Background()
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	query := `SELECT SUM(price), COUNT(*) FROM products`
	compacted := false
	for i := 0; i < 12; i++ {
		projectionExecute(t, engine, fmt.Sprintf("UPDATE products SET price = %d WHERE id = 1", 5000+i))
		expected := projectionExecute(t, engine, query)
		report, err := engine.RefreshAnalytics(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertCurrentProjection(t, engine, query, expected)
		if report.AnalyticsPublication == "compact" {
			if report.AnalyticsObsoleteBytes != 0 || report.AnalyticsCopiedBytes == 0 || report.AnalyticsReferencedBytes != 0 {
				t.Fatal(report)
			}
			compacted = true
			break
		}
	}
	if !compacted {
		t.Fatal("repeated updates grew the file without compaction")
	}
}

func TestColumnarAppendUpgradesLegacyContainer(t *testing.T) {
	engine := incrementalProjectionDatabase(t, columnarChunkRows+7)
	ctx := context.Background()
	first, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rewriteColumnarManifest(t, first.AnalyticsFile, func(*projectionManifest) {})
	file, err := snapshotfile.Open(first.AnalyticsFile)
	if err != nil {
		t.Fatal(err)
	}
	if file.GenerationInfo().Number != 0 {
		t.Fatal("fixture did not create v1 container")
	}
	file.Close()
	query := `SELECT SUM(price), COUNT(*) FROM products`
	expected := projectionExecute(t, engine, query)
	report, err := engine.RefreshAnalytics(ctx)
	if err != nil || report.AnalyticsPublication != "rewrite" || report.AnalyticsSourceRows != 0 || report.AnalyticsCopiedBytes == 0 {
		t.Fatalf("legacy upgrade: %+v %v", report, err)
	}
	file, err = snapshotfile.Open(first.AnalyticsFile)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if file.GenerationInfo().Number == 0 {
		t.Fatal("legacy file was not upgraded")
	}
	assertCurrentProjection(t, engine, query, expected)
}

type columnarAppendCancelContext struct {
	context.Context
	cancel context.CancelFunc
	path   string
	size   int64
}

func (ctx columnarAppendCancelContext) Err() error {
	if err := ctx.Context.Err(); err != nil {
		return err
	}
	if info, err := os.Stat(ctx.path); err == nil && info.Size() > ctx.size {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestColumnarAppendCancellationKeepsPublishedGeneration(t *testing.T) {
	engine := incrementalProjectionDatabase(t, columnarChunkRows+7)
	first, err := engine.RefreshAnalytics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old := readProjectionManifest(t, first.AnalyticsFile)
	projectionExecute(t, engine, `UPDATE products SET price = 4444 WHERE id = 1`)
	query := `SELECT SUM(price), COUNT(*) FROM products`
	expected := projectionExecute(t, engine, query)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := columnarAppendCancelContext{Context: base, cancel: cancel, path: first.AnalyticsFile, size: first.AnalyticsFileBytes}
	if _, err := engine.RefreshAnalytics(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("append cancellation: %v", err)
	}
	if got := readProjectionManifest(t, first.AnalyticsFile); !reflect.DeepEqual(got, old) {
		t.Fatal("canceled append advanced manifest")
	}
	if got := projectionExecute(t, engine, query); got.Execution.Path != "krow-batch" || !reflect.DeepEqual(got.Rows, expected.Rows) {
		t.Fatal(got)
	}
	if _, err := engine.RefreshAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCurrentProjection(t, engine, query, expected)
}

func TestColumnarAppendMultiGenerationDifferential(t *testing.T) {
	engine := incrementalProjectionDatabase(t, 2*columnarChunkRows+13)
	ctx := context.Background()
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		`SELECT SUM(price), AVG(rating), COUNT(*), COUNT(price) FROM products`,
		`SELECT SUM(price), COUNT(*) FROM products WHERE price >= 25 AND enabled = true`,
		`SELECT MIN(id), MAX(id), SUM(price) FROM products WHERE price IS NULL`,
	}
	for step, statement := range []string{
		`UPDATE products SET price = 1000 WHERE id = 1`,
		`UPDATE products SET rating = 99.5 WHERE id = 12000`,
		`DELETE FROM products WHERE id = 8193`,
		`INSERT INTO products (id,name,price) VALUES (999999,'last',555)`,
		`UPDATE products SET price = NULL WHERE id = 16385`,
		`DELETE FROM products WHERE id = 999999`,
		`INSERT INTO products (id,name,price) VALUES (-100,'first',777)`,
		`UPDATE products SET enabled = true WHERE id = 12000`,
	} {
		projectionExecute(t, engine, statement)
		expected := make([]Result, len(queries))
		for i, query := range queries {
			expected[i] = projectionExecute(t, engine, query)
		}
		report, err := engine.RefreshAnalytics(ctx)
		if err != nil || report.AnalyticsFallback != "" {
			t.Fatalf("step %d: %+v %v", step, report, err)
		}
		for i, query := range queries {
			assertCurrentProjection(t, engine, query, expected[i])
		}
	}
}

func TestColumnarAppendRecoveredRootDoesNotServeStaleSQL(t *testing.T) {
	engine := incrementalProjectionDatabase(t, columnarChunkRows+7)
	ctx := context.Background()
	first, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectionExecute(t, engine, `UPDATE products SET price = 9999 WHERE id = 1`)
	query := `SELECT SUM(price), COUNT(*) FROM products`
	expected := projectionExecute(t, engine, query)
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(first.AnalyticsFile, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if _, err := file.ReadAt(data[:], 4095); err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if _, err := file.WriteAt(data[:], 4095); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readProjectionManifest(t, first.AnalyticsFile); got.Cursor.Transaction != first.Transaction {
		t.Fatal("did not recover old root")
	}
	got := projectionExecute(t, engine, query)
	if got.Execution.Path != "krow-batch" || !reflect.DeepEqual(got.Rows, expected.Rows) {
		t.Fatalf("recovered root served stale SQL: %+v", got)
	}
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	assertCurrentProjection(t, engine, query, expected)
}

func TestColumnarAppendConcurrentWritesRefreshAndQueries(t *testing.T) {
	engine := incrementalProjectionDatabase(t, 128)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	query := `SELECT COUNT(*), SUM(price) FROM products`
	initial := projectionExecute(t, engine, query)
	sum, ok := initial.Rows[0][1].(int64)
	if !ok {
		t.Fatalf("unexpected SUM type: %+v", initial.Rows)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 2)
	workers.Add(2)
	go func() {
		defer workers.Done()
		for i := 0; i < 12; i++ {
			if _, err := engine.Execute(ctx, fmt.Sprintf("UPDATE products SET price = %d WHERE id = 1", 1000+i)); err != nil {
				failures <- err
				return
			}
			if _, err := engine.RefreshAnalytics(ctx); err != nil {
				failures <- err
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 60; i++ {
			result, err := engine.Execute(ctx, query)
			if err != nil {
				failures <- err
				return
			}
			if len(result.Rows) != 1 || len(result.Rows[0]) != 2 || result.Rows[0][0] != int64(128) {
				failures <- fmt.Errorf("partial count: %+v", result)
				return
			}
			got, ok := result.Rows[0][1].(int64)
			if !ok || (got != sum && (got < sum+999 || got > sum+1010)) {
				failures <- fmt.Errorf("mixed snapshot sum: %+v", result)
				return
			}
		}
	}()
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
