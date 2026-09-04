package relational

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/search"
)

func TestPackSearchProjectionAdoptsCurrentLegacyIndex(t *testing.T) {
	path := legacySearchTestDatabase(t, false)
	ctx := context.Background()
	engine, err := OpenWithOptions(path, Options{
		ExperimentalProjections: true,
		SearchReaderCacheBytes:  8 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := engine.PackSearchProjection(ctx)
	if err != nil {
		_ = engine.Close()
		t.Fatal(err)
	}
	if report.Transaction == 0 || report.SearchFile != path+".search" ||
		report.SearchTables != 1 || report.SearchDocuments != 3 ||
		report.SearchSegments == 0 || report.SearchSourceBytes == 0 ||
		report.SearchFileBytes == 0 || report.CanonicalRowsScanned != 0 ||
		report.Publication != "packed-legacy-index" {
		_ = engine.Close()
		t.Fatalf("pack report = %+v", report)
	}
	if stat, err := os.Stat(report.SearchFile); err != nil || !stat.Mode().IsRegular() {
		_ = engine.Close()
		t.Fatalf("packed file = %v, %v", stat, err)
	}
	if stat, err := os.Stat(filepath.Join(filepath.Dir(path), "search")); err != nil || !stat.IsDir() {
		_ = engine.Close()
		t.Fatalf("legacy root changed = %v, %v", stat, err)
	}
	status, err := engine.PreflightProjections(ctx)
	if err != nil {
		_ = engine.Close()
		t.Fatal(err)
	}
	if len(status.Search) != 1 || status.Search[0].Status != "ready" ||
		status.Search[0].QueryPath != "search-snapshot" ||
		status.Search[0].Documents != 3 || status.Search[0].ReaderCapacityBytes <= 0 {
		_ = engine.Close()
		t.Fatalf("packed status = %+v", status)
	}
	query := `SELECT id, name, _score FROM products WHERE * SEARCH 'ban phim logitech' ORDER BY _score DESC LIMIT 10`
	result := projectionExecute(t, engine, query)
	if result.Execution == nil || result.Execution.Path != "search-snapshot" ||
		len(result.Rows) != 1 || result.Rows[0][0] != int64(1) {
		_ = engine.Close()
		t.Fatalf("packed query = %+v", result)
	}
	before, err := os.ReadFile(report.SearchFile)
	if err != nil {
		_ = engine.Close()
		t.Fatal(err)
	}
	projectionExecute(t, engine, `INSERT INTO products VALUES (4, 'ban phim logitech moi', 400)`)
	if _, err := engine.PackSearchProjection(ctx); err == nil || !strings.Contains(err.Error(), "not at source transaction") {
		_ = engine.Close()
		t.Fatalf("stale legacy index packed = %v", err)
	}
	after, err := os.ReadFile(report.SearchFile)
	if err != nil || !reflect.DeepEqual(after, before) {
		_ = engine.Close()
		t.Fatalf("failed pack changed published file: %v", err)
	}
	if _, err := engine.Execute(ctx, query); err == nil || !strings.Contains(err.Error(), "RefreshProjections") {
		_ = engine.Close()
		t.Fatalf("stale packed search served = %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	moved := report.SearchFile + ".moved"
	if err := os.Rename(report.SearchFile, moved); err != nil {
		t.Fatalf("packed reader handle survived close: %v", err)
	}
	if err := os.Rename(moved, report.SearchFile); err != nil {
		t.Fatal(err)
	}
}

func TestPackSearchProjectionRejectsCurrentTombstones(t *testing.T) {
	path := legacySearchTestDatabase(t, true)
	ctx := context.Background()
	legacy, err := OpenWithOptions(path, Options{
		Kernel:        kitdbengine.OpenOptions{RetainHistory: true},
		SearchManager: search.ManagerOptions{DisableAutoCompact: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	projectionExecute(t, legacy, `DELETE FROM products WHERE id = 2`)
	projectionExecute(t, legacy, `SELECT id FROM products WHERE * SEARCH 'logitech' LIMIT 10`)
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	engine, err := OpenWithOptions(path, Options{
		ExperimentalProjections: true,
		Kernel:                  kitdbengine.OpenOptions{RetainHistory: true},
		SearchManager:           search.ManagerOptions{DisableAutoCompact: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.PackSearchProjection(ctx); err == nil || !strings.Contains(err.Error(), "tombstones") {
		t.Fatalf("tombstoned legacy index packed = %v", err)
	}
	if _, err := os.Stat(path + ".search"); !os.IsNotExist(err) {
		t.Fatalf("failed tombstone pack published a file: %v", err)
	}
}

func TestPackSearchProjectionHonorsQueuedCancellation(t *testing.T) {
	path := legacySearchTestDatabase(t, false)
	engine, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	engine.projectionBuilds <- struct{}{}
	defer func() { <-engine.projectionBuilds }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.PackSearchProjection(ctx); err != context.Canceled {
		t.Fatalf("queued pack ignored cancellation: %v", err)
	}
}

func legacySearchTestDatabase(t *testing.T, retainHistory bool) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application", ".data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "shop.kitdb")
	engine, err := OpenWithOptions(path, Options{
		Kernel: kitdbengine.OpenOptions{RetainHistory: retainHistory},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, query := range []string{
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT SEARCHABLE, price INTEGER)`,
		`INSERT INTO products VALUES (1, 'bàn phím logitech', 100), (2, 'chuột logitech', 200), (3, 'bàn phím cơ', 300)`,
		`SELECT id FROM products WHERE * SEARCH 'logitech' LIMIT 10`,
	} {
		if _, err := engine.Execute(ctx, query); err != nil {
			_ = engine.Close()
			t.Fatalf("%s: %v", query, err)
		}
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
