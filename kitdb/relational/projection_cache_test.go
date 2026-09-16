package relational

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/kitwork/engine/internal/snapshotfile"
	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestProjectionReaderCacheHitsAndInvalidatesAtPublication(t *testing.T) {
	engine := projectionTestDatabase(t, 128)
	engine.searchReaderCacheBytes = 16 << 20
	ctx := context.Background()
	if _, err := engine.RefreshProjections(ctx); err != nil {
		t.Fatal(err)
	}

	aggregate := `SELECT SUM(price), AVG(rating) FROM products WHERE price >= 25`
	first := projectionExecute(t, engine, aggregate)
	if first.Execution == nil || first.Execution.Path != "kcol-batch" ||
		first.Execution.ProjectionCacheMisses != 1 || first.Execution.ProjectionCacheHits != 0 {
		t.Fatalf("first analytics cache access = %+v", first.Execution)
	}
	second := projectionExecute(t, engine, aggregate)
	if second.Execution == nil || second.Execution.ProjectionCacheHits != 1 ||
		second.Execution.ProjectionCacheMisses != 0 {
		t.Fatalf("warm analytics cache access = %+v", second.Execution)
	}

	searchQuery := `SELECT id, name, _score FROM products WHERE * SEARCH 'blue widget' LIMIT 5`
	searchFirst := projectionExecute(t, engine, searchQuery)
	if searchFirst.Execution == nil || searchFirst.Execution.Path != "search-snapshot" ||
		searchFirst.Execution.ProjectionCacheMisses != 1 || searchFirst.Execution.ProjectionCacheHits != 0 ||
		searchFirst.Execution.SearchReaderCacheMisses != 1 || searchFirst.Execution.SearchReaderCacheHits != 0 {
		t.Fatalf("first search cache access = %+v", searchFirst.Execution)
	}
	searchSecond := projectionExecute(t, engine, searchQuery)
	if searchSecond.Execution == nil || searchSecond.Execution.ProjectionCacheHits != 1 ||
		searchSecond.Execution.ProjectionCacheMisses != 0 ||
		searchSecond.Execution.SearchReaderCacheHits != 1 || searchSecond.Execution.SearchReaderCacheMisses != 0 {
		t.Fatalf("warm search cache access = %+v", searchSecond.Execution)
	}
	if got := projectionCacheEntries(engine); got != 2 {
		t.Fatalf("warm projection cache entries = %d, want 2", got)
	}

	projectionExecute(t, engine, `UPDATE products SET price = 999 WHERE id = 1`)
	stale := projectionExecute(t, engine, aggregate)
	if stale.Execution == nil || stale.Execution.Path != "krow-batch" ||
		stale.Execution.ProjectionCacheMisses != 1 || stale.Execution.Fallback == "" {
		t.Fatalf("stale analytics cache access = %+v", stale.Execution)
	}
	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	afterPublish := projectionExecute(t, engine, aggregate)
	if afterPublish.Execution == nil || afterPublish.Execution.Path != "kcol-batch" ||
		afterPublish.Execution.ProjectionCacheMisses != 1 || afterPublish.Execution.ProjectionCacheHits != 0 {
		t.Fatalf("first access after publication = %+v", afterPublish.Execution)
	}
	warmAgain := projectionExecute(t, engine, aggregate)
	if warmAgain.Execution == nil || warmAgain.Execution.ProjectionCacheHits != 1 ||
		warmAgain.Execution.ProjectionCacheMisses != 0 {
		t.Fatalf("warm access after publication = %+v", warmAgain.Execution)
	}
	if _, err := engine.RefreshProjections(ctx); err != nil {
		t.Fatal(err)
	}
	searchAfterPublish := projectionExecute(t, engine, searchQuery)
	if searchAfterPublish.Execution == nil || searchAfterPublish.Execution.Path != "search-snapshot" ||
		searchAfterPublish.Execution.ProjectionCacheMisses != 1 || searchAfterPublish.Execution.ProjectionCacheHits != 0 ||
		searchAfterPublish.Execution.SearchReaderCacheMisses != 1 {
		t.Fatalf("first search access after publication = %+v", searchAfterPublish.Execution)
	}
	searchWarmAgain := projectionExecute(t, engine, searchQuery)
	if searchWarmAgain.Execution == nil || searchWarmAgain.Execution.ProjectionCacheHits != 1 ||
		searchWarmAgain.Execution.ProjectionCacheMisses != 0 || searchWarmAgain.Execution.SearchReaderCacheHits != 1 {
		t.Fatalf("warm search access after publication = %+v", searchWarmAgain.Execution)
	}
	analyticsStillWarm := projectionExecute(t, engine, aggregate)
	if analyticsStillWarm.Execution == nil || analyticsStillWarm.Execution.ProjectionCacheHits != 1 ||
		analyticsStillWarm.Execution.ProjectionCacheMisses != 0 {
		t.Fatalf("search publication invalidated analytics cache = %+v", analyticsStillWarm.Execution)
	}
	resident := engine.ProjectionCacheStats()
	if resident.Entries != 2 || resident.ActiveLeases != 0 || resident.DirectoryBytes <= 0 ||
		resident.SearchReaders != 1 || resident.SearchFileHandles != expectedProjectionSearchReadHandles() ||
		resident.SearchReaderResidentBytes <= 0 ||
		resident.SearchReaderCapacityBytes < resident.SearchReaderResidentBytes {
		t.Fatalf("projection cache residency = %+v", resident)
	}
	trimmed, err := engine.TrimProjectionCache()
	if err != nil {
		t.Fatal(err)
	}
	if trimmed.Entries != resident.Entries || trimmed.DirectoryBytes != resident.DirectoryBytes ||
		trimmed.SearchReaders != resident.SearchReaders ||
		trimmed.SearchFileHandles != resident.SearchFileHandles ||
		trimmed.SearchReaderResidentBytes != resident.SearchReaderResidentBytes ||
		trimmed.SearchReaderCapacityBytes != resident.SearchReaderCapacityBytes {
		t.Fatalf("projection cache trim = %+v, residency was %+v", trimmed, resident)
	}
	if afterTrim := engine.ProjectionCacheStats(); afterTrim != (ProjectionCacheStats{}) {
		t.Fatalf("projection cache after trim = %+v", afterTrim)
	}
	analyticsAfterTrim := projectionExecute(t, engine, aggregate)
	if analyticsAfterTrim.Execution == nil || analyticsAfterTrim.Execution.Path != "kcol-batch" ||
		analyticsAfterTrim.Execution.ProjectionCacheMisses != 1 {
		t.Fatalf("analytics access after trim = %+v", analyticsAfterTrim.Execution)
	}
	searchAfterTrim := projectionExecute(t, engine, searchQuery)
	if searchAfterTrim.Execution == nil || searchAfterTrim.Execution.Path != "search-snapshot" ||
		searchAfterTrim.Execution.ProjectionCacheMisses != 1 ||
		searchAfterTrim.Execution.SearchReaderCacheMisses != 1 {
		t.Fatalf("search access after trim = %+v", searchAfterTrim.Execution)
	}

	analyticsPath := engine.Path() + ".analytics"
	searchPath := engine.Path() + ".search"
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if got := projectionCacheEntries(engine); got != 0 {
		t.Fatalf("closed projection cache entries = %d, want 0", got)
	}
	// This also proves Windows handles owned by the cache were released.
	for _, path := range []string{analyticsPath, searchPath} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove closed projection %s: %v", path, err)
		}
	}
}

func TestSearchReaderCacheBypassesInsufficientBudget(t *testing.T) {
	engine := projectionTestDatabase(t, 64)
	engine.searchReaderCacheBytes = 1
	if _, err := engine.RefreshProjections(context.Background()); err != nil {
		t.Fatal(err)
	}
	query := `SELECT id, name, _score FROM products WHERE * SEARCH 'blue widget' LIMIT 5`
	for attempt := range 2 {
		result := projectionExecute(t, engine, query)
		if result.Execution == nil || result.Execution.SearchReaderCacheMisses != 1 ||
			result.Execution.SearchReaderCacheBypasses != 1 || result.Execution.SearchReaderCacheHits != 0 {
			t.Fatalf("uncached search %d = %+v", attempt, result.Execution)
		}
	}
	resident := engine.ProjectionCacheStats()
	if resident.SearchReaders != 0 ||
		resident.SearchFileHandles != expectedProjectionSearchReadHandles() ||
		resident.SearchReaderResidentBytes != 0 ||
		resident.SearchReaderCapacityBytes != 0 {
		t.Fatalf("insufficient budget retained a search reader: %+v", resident)
	}
	trimmed, err := engine.TrimProjectionCache()
	if err != nil {
		t.Fatal(err)
	}
	if trimmed.SearchReaders != 0 || trimmed.SearchFileHandles != resident.SearchFileHandles ||
		engine.ProjectionCacheStats() != (ProjectionCacheStats{}) {
		t.Fatalf("insufficient-budget trim = %+v", trimmed)
	}
}

func TestSearchReaderCacheSingleFlightsConcurrentOpen(t *testing.T) {
	engine := projectionTestDatabase(t, 256)
	engine.searchReaderCacheBytes = 16 << 20
	if _, err := engine.RefreshProjections(context.Background()); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	start := make(chan struct{})
	results := make(chan Result, workers)
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := engine.Execute(
				context.Background(),
				`SELECT id, name, _score FROM products WHERE * SEARCH 'blue widget' LIMIT 5`,
			)
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	var hits, misses, bypasses uint64
	for result := range results {
		if result.Execution == nil {
			t.Fatal("concurrent search omitted execution stats")
		}
		hits += result.Execution.SearchReaderCacheHits
		misses += result.Execution.SearchReaderCacheMisses
		bypasses += result.Execution.SearchReaderCacheBypasses
	}
	if misses != 1 || hits != workers-1 || bypasses != 0 {
		t.Fatalf("concurrent search cache hits=%d misses=%d bypasses=%d", hits, misses, bypasses)
	}
	if resident := engine.ProjectionCacheStats(); resident.SearchReaders != 1 ||
		resident.SearchFileHandles != expectedProjectionSearchReadHandles() {
		t.Fatalf("concurrent search retained %+v", resident)
	}
}

func TestSearchReaderCacheOptionsAreExplicitAndBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.kitdb")
	if engine, err := OpenWithOptions(path, Options{SearchReaderCacheBytes: 1}); err == nil {
		_ = engine.Close()
		t.Fatal("search reader cache succeeded without experimental projections")
	}
	if engine, err := OpenWithOptions(path, Options{
		ExperimentalProjections: true,
		SearchReaderCacheBytes:  MaximumSearchReaderCacheBytes + 1,
	}); err == nil {
		_ = engine.Close()
		t.Fatal("oversized search reader cache succeeded")
	}
}

func TestProjectionReaderCacheBypassesOversizedDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.analytics")
	schema := kitdbsql.Schema{ID: "events-id", Name: "events", Hash: "events-hash"}
	cursor := kitdbengine.HistoryCursor{DatabaseID: "database-id", Transaction: 7, Checksum: 9}
	manifest := projectionManifest{
		Version: 1,
		Kind:    "columnar",
		Cursor:  cursor,
		Catalog: "catalog-revision",
		Tables: map[string]projectionTable{
			schema.ID: {
				Hash: schema.Hash, Generation: 3, Epoch: 4,
				BlockDirectory: make([]byte, maximumCachedProjectionDirectoryBytes+1),
			},
		},
	}
	writer, err := snapshotfile.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Add(schema.ID, func(output io.Writer) error {
		_, err := output.Write([]byte{0})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Publish(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}

	engine := &Engine{}
	for attempt := range 2 {
		engine.projectionMu.RLock()
		lease, err := engine.acquireProjection(
			path, "columnar", cursor, manifest.Catalog, schema, 3, 4,
		)
		if err != nil {
			engine.projectionMu.RUnlock()
			t.Fatal(err)
		}
		if !lease.access.miss || !lease.access.bypass || lease.access.hit {
			engine.projectionMu.RUnlock()
			t.Fatalf("oversized access %d = %+v", attempt, lease.access)
		}
		if err := lease.close(); err != nil {
			engine.projectionMu.RUnlock()
			t.Fatal(err)
		}
		engine.projectionMu.RUnlock()
	}
	if got := projectionCacheEntries(engine); got != 0 {
		t.Fatalf("oversized directory retained %d cache entries", got)
	}
}

func BenchmarkProjectionReaderCache(b *testing.B) {
	path := filepath.Join(b.TempDir(), "cache.analytics")
	schema := kitdbsql.Schema{ID: "events-id", Name: "events", Hash: "events-hash"}
	cursor := kitdbengine.HistoryCursor{DatabaseID: "database-id", Transaction: 7, Checksum: 9}
	manifest := projectionManifest{
		Version: 1,
		Kind:    "columnar",
		Cursor:  cursor,
		Catalog: "catalog-revision",
		Tables: map[string]projectionTable{
			schema.ID: {
				Hash: schema.Hash, Generation: 3, Epoch: 4,
				BlockDirectory: make([]byte, 768<<10),
			},
		},
	}
	writer, err := snapshotfile.CreateGeneration(path)
	if err != nil {
		b.Fatal(err)
	}
	if err := writer.Add(schema.ID, func(output io.Writer) error {
		_, err := output.Write([]byte{0})
		return err
	}); err != nil {
		b.Fatal(err)
	}
	if err := writer.Publish(context.Background(), manifest); err != nil {
		b.Fatal(err)
	}
	defer writer.Close()

	engine := &Engine{}
	b.Cleanup(func() {
		engine.projectionMu.Lock()
		defer engine.projectionMu.Unlock()
		if err := engine.projectionCache.close(); err != nil {
			b.Error(err)
		}
	})
	acquire := func() {
		engine.projectionMu.RLock()
		lease, err := engine.acquireProjection(
			path, "columnar", cursor, manifest.Catalog, schema, 3, 4,
		)
		if err == nil {
			err = lease.close()
		}
		engine.projectionMu.RUnlock()
		if err != nil {
			b.Fatal(err)
		}
	}
	acquire()

	b.Run("warm-lease", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			acquire()
		}
	})
	b.Run("cold-open-and-decode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			engine.projectionMu.Lock()
			err := engine.projectionCache.invalidate("columnar")
			engine.projectionMu.Unlock()
			if err != nil {
				b.Fatal(err)
			}
			acquire()
		}
	})
}

func BenchmarkPackedSearchReaderCache(b *testing.B) {
	for _, benchmark := range []struct {
		name   string
		budget int64
	}{
		{name: "uncached", budget: 0},
		{name: "resident", budget: 16 << 20},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			engine := projectionTestDatabase(b, 4096)
			engine.searchReaderCacheBytes = benchmark.budget
			if _, err := engine.RefreshProjections(context.Background()); err != nil {
				b.Fatal(err)
			}
			query := `SELECT id, name, _score FROM products WHERE * SEARCH 'blue widget' LIMIT 20`
			if _, err := engine.Execute(context.Background(), query); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := engine.Execute(context.Background(), query); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func projectionCacheEntries(engine *Engine) int {
	engine.projectionCache.mu.Lock()
	defer engine.projectionCache.mu.Unlock()
	return len(engine.projectionCache.entries)
}

func expectedProjectionSearchReadHandles() int {
	if runtime.GOOS == "windows" {
		return 2
	}
	return 1
}
