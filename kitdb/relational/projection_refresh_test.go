package relational

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestColumnarRefreshLeavesSearchAbsentOrLegacy(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "absent"
		if legacy {
			name = "legacy-directory"
		}
		t.Run(name, func(t *testing.T) {
			engine := projectionTestDatabase(t, 32)
			searchPath := engine.Path() + ".search"
			marker := filepath.Join(searchPath, "owner")
			if legacy {
				if err := os.Mkdir(searchPath, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(marker, []byte("legacy search"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			query := `SELECT SUM(price), COUNT(price) FROM products`
			before := projectionExecute(t, engine, query)
			report, err := engine.RefreshAnalytics(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if report.AnalyticsFile != engine.Path()+".analytics" || report.AnalyticsTables != 1 || report.SearchFile != "" || report.SearchTables != 0 {
				t.Fatalf("analytics-only report: %+v", report)
			}
			got := projectionExecute(t, engine, query)
			if got.Execution == nil || got.Execution.Path != "kcol-batch" || !reflect.DeepEqual(got.Rows, before.Rows) {
				t.Fatalf("columnar query: %+v, expected %+v", got, before)
			}
			if legacy {
				contents, err := os.ReadFile(marker)
				if err != nil || string(contents) != "legacy search" {
					t.Fatalf("legacy search changed: %q %v", contents, err)
				}
			} else if _, err := os.Lstat(searchPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("columnar refresh created search: %v", err)
			}
		})
	}
}

func TestColumnarRefreshAfterWritesPreservesIndependentSearch(t *testing.T) {
	engine := projectionTestDatabase(t, 32)
	ctx := context.Background()
	first, err := engine.RefreshProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	searchBefore, err := os.ReadFile(first.SearchFile)
	if err != nil {
		t.Fatal(err)
	}
	columnarBefore, err := os.ReadFile(first.AnalyticsFile)
	if err != nil {
		t.Fatal(err)
	}
	query := `SELECT SUM(price), COUNT(price), COUNT(*) FROM products`
	before := projectionExecute(t, engine, query)
	old, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	for _, sql := range []string{
		`INSERT INTO products (id, name, price) VALUES (100, 'blue new widget', 1000)`,
		`UPDATE products SET price = 70 WHERE id = 1`,
		`DELETE FROM products WHERE id = 2`,
	} {
		projectionExecute(t, engine, sql)
		unchanged, err := os.ReadFile(first.AnalyticsFile)
		if err != nil || !reflect.DeepEqual(unchanged, columnarBefore) {
			t.Fatalf("ordinary write rewrote columnar: %s: %v", sql, err)
		}
		current := projectionExecute(t, engine, query)
		if current.Execution == nil || current.Execution.Path != "krow-batch" || current.Execution.Fallback == "" {
			t.Fatalf("stale projection did not fall back after %s: %+v", sql, current)
		}
	}
	current := projectionExecute(t, engine, query)
	if reflect.DeepEqual(current.Rows, before.Rows) {
		t.Fatal("writes did not change aggregate")
	}
	oldResult, err := old.Execute(ctx, query)
	if err != nil || oldResult.Execution == nil || oldResult.Execution.Path != "kcol-batch" || !reflect.DeepEqual(oldResult.Rows, before.Rows) {
		t.Fatalf("old snapshot before refresh: %+v %v", oldResult, err)
	}
	refreshed, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Transaction <= first.Transaction || refreshed.SearchFile != "" || refreshed.SearchTables != 0 {
		t.Fatalf("independent refresh report: %+v", refreshed)
	}
	got := projectionExecute(t, engine, query)
	if got.Execution == nil || got.Execution.Path != "kcol-batch" || !reflect.DeepEqual(got.Rows, current.Rows) {
		t.Fatalf("refreshed columnar: %+v, expected %+v", got, current)
	}
	searchAfter, err := os.ReadFile(first.SearchFile)
	if err != nil || !reflect.DeepEqual(searchAfter, searchBefore) {
		t.Fatalf("columnar refresh changed search file: %v", err)
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM products WHERE * SEARCH 'blue' LIMIT 5`); err == nil || !strings.Contains(err.Error(), "RefreshProjections") {
		t.Fatalf("columnar refresh incorrectly advanced search watermark: %v", err)
	}
	oldResult, err = old.Execute(ctx, query)
	if err != nil || oldResult.Execution == nil || oldResult.Execution.Path != "krow-batch" || !reflect.DeepEqual(oldResult.Rows, before.Rows) {
		t.Fatalf("old snapshot after refresh: %+v %v", oldResult, err)
	}
	if err := old.Rollback(); err != nil {
		t.Fatal(err)
	}
	path := engine.Path()
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got = projectionExecute(t, reopened, query)
	if got.Execution == nil || got.Execution.Path != "kcol-batch" || !reflect.DeepEqual(got.Rows, current.Rows) {
		t.Fatalf("reopened columnar: %+v, expected %+v", got, current)
	}
}

func TestColumnarRefreshCancellationKeepsPublishedFile(t *testing.T) {
	for _, mode := range []string{"canceled", "queued", "mid-build"} {
		t.Run(mode, func(t *testing.T) {
			engine := projectionTestDatabase(t, 1200)
			if _, err := engine.RefreshAnalytics(context.Background()); err != nil {
				t.Fatal(err)
			}
			if mode == "mid-build" {
				projectionExecute(t, engine, `UPDATE products SET price = 999 WHERE id = 1`)
			}
			before, err := os.ReadFile(engine.Path() + ".analytics")
			if err != nil {
				t.Fatal(err)
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			var ctx context.Context = base
			if mode == "mid-build" {
				ctx = &columnarBuildCancelContext{Context: base, cancel: cancel, directory: filepath.Dir(engine.Path())}
			} else {
				cancel()
				if mode == "queued" {
					engine.projectionBuilds <- struct{}{}
					defer func() { <-engine.projectionBuilds }()
				}
			}
			if _, err := engine.RefreshAnalytics(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled refresh: %v", err)
			}
			after, err := os.ReadFile(engine.Path() + ".analytics")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("cancellation replaced published columnar: %v", err)
			}
			files, err := os.ReadDir(filepath.Dir(engine.Path()))
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range files {
				if strings.HasPrefix(file.Name(), ".projection-") || strings.HasPrefix(file.Name(), ".search-build-") {
					t.Fatalf("cancellation leaked staging file %s", file.Name())
				}
			}
		})
	}
}

type columnarBuildCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	directory string
}

func (ctx *columnarBuildCancelContext) Err() error {
	if err := ctx.Context.Err(); err != nil {
		return err
	}
	// Cancel after staged column data exists, not after an incidental call count.
	files, _ := os.ReadDir(ctx.directory)
	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".projection-") {
			if stat, err := file.Info(); err == nil && stat.Size() > 4096 {
				ctx.cancel()
				break
			}
		}
	}
	return ctx.Context.Err()
}

func TestColumnarRefreshSharesBuildAdmission(t *testing.T) {
	engine := projectionTestDatabase(t, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := engine.RefreshProjections(ctx); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 4)
	for worker := range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 3 {
				var err error
				switch worker {
				case 0:
					_, err = engine.RefreshProjections(ctx)
				case 1:
					_, err = engine.RefreshAnalytics(ctx)
				case 2:
					_, err = engine.Execute(ctx, `SELECT SUM(price) FROM products`)
				case 3:
					_, err = engine.Execute(ctx, `SELECT id FROM products WHERE * SEARCH 'blue' LIMIT 10`)
				}
				if err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestColumnarRefreshRejectsNonRegularTarget(t *testing.T) {
	engine := projectionTestDatabase(t, 1)
	path := engine.Path() + ".analytics"
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RefreshAnalytics(context.Background()); err == nil {
		t.Fatal("columnar refresh accepted a directory")
	}
	if stat, err := os.Stat(path); err != nil || !stat.IsDir() {
		t.Fatalf("columnar directory was replaced: %v", err)
	}
}
