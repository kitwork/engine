package relational

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/snapshotfile"
)

func projectionTestDatabase(t testing.TB, rows int) *Engine {
	t.Helper()
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "data.kitdb"), Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT SEARCHABLE, price INTEGER, rating FLOAT, enabled BOOLEAN, payload TEXT)`); err != nil {
		t.Fatal(err)
	}
	for start := 0; start < rows; start += 128 {
		var sql strings.Builder
		sql.WriteString("INSERT INTO products (id,name,price,rating,enabled,payload) VALUES ")
		for i := start; i < min(start+128, rows); i++ {
			if i != start {
				sql.WriteByte(',')
			}
			price, rating := fmt.Sprint(i%100), fmt.Sprintf("%.2f", float64(i%20)/4)
			if i%17 == 0 {
				price = "NULL"
			}
			if i%19 == 0 {
				rating = "NULL"
			}
			fmt.Fprintf(&sql, "(%d,'blue widget %d',%s,%s,%t,'%s')", i, i%11, price, rating, i%2 == 0, strings.Repeat("description ", 24))
		}
		if _, err := engine.Execute(ctx, sql.String()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	return engine
}

func projectionExecute(t testing.TB, engine *Engine, query string) Result {
	t.Helper()
	r, err := engine.Execute(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return r
}

func TestProjectionSnapshotsMatchRowsAndReopen(t *testing.T) {
	engine := projectionTestDatabase(t, 2507)
	ctx := context.Background()
	projectionExecute(t, engine, `CREATE TABLE clicks (id INTEGER PRIMARY KEY, visits INTEGER)`)
	projectionExecute(t, engine, `INSERT INTO clicks VALUES (1,42)`)
	queries := []string{
		`SELECT COUNT(*), COUNT(price), SUM(price), AVG(rating), MIN(price), MAX(rating) FROM products`,
		`SELECT SUM(price), AVG(rating), COUNT(*) FROM products WHERE price >= 25 AND price < 75 AND enabled = true`,
		`SELECT COUNT(*), SUM(price) FROM products WHERE price IS NULL`,
		`SELECT COUNT(*), SUM(price), AVG(rating) FROM products WHERE rating IS NOT NULL AND price != 30`,
		`SELECT SUM(price), MIN(price), MAX(price), AVG(rating), COUNT(price) FROM products WHERE price < 0`,
		`SELECT MIN(enabled), MAX(enabled), COUNT(enabled) FROM products`,
		`SELECT SUM(price) FROM products LIMIT 0`,
		`SELECT SUM(visits) FROM clicks`,
	}
	expected := make([]Result, len(queries))
	engine.batchAggregates, engine.experimentalProjections = false, false
	for i, query := range queries {
		expected[i] = projectionExecute(t, engine, query)
	}
	engine.batchAggregates = true
	for i, query := range queries {
		got := projectionExecute(t, engine, query)
		if !reflect.DeepEqual(got.Rows, expected[i].Rows) || got.Execution == nil || got.Execution.Path != "krow-batch" {
			t.Fatalf("row batch %s: %+v, want %+v", query, got, expected[i])
		}
	}
	engine.experimentalProjections = true
	report, err := engine.RefreshProjections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.AnalyticsTables != 2 || report.SearchTables != 1 {
		t.Fatal(report)
	}
	for _, path := range []string{report.AnalyticsFile, report.SearchFile} {
		stat, err := os.Stat(path)
		if err != nil || !stat.Mode().IsRegular() {
			t.Fatalf("not one file: %s %v", path, err)
		}
	}
	for i, query := range queries {
		got := projectionExecute(t, engine, query)
		if !reflect.DeepEqual(got.Rows, expected[i].Rows) || got.Execution == nil || got.Execution.Path != "kcol-batch" {
			t.Fatalf("columnar %s: %+v (%+v), want %+v", query, got, got.Execution, expected[i])
		}
	}
	searchQuery := `SELECT id, name, _score, _snippet FROM products WHERE * SEARCH 'blue widget' AND price >= 50 ORDER BY _score DESC LIMIT 10`
	before := projectionExecute(t, engine, searchQuery)
	if len(before.Rows) != 10 || before.Execution.Path != "search-snapshot" || !strings.Contains(before.Rows[0][3].(string), "<b>") {
		t.Fatalf("search: %+v", before)
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
	after := projectionExecute(t, reopened, searchQuery)
	if !reflect.DeepEqual(before.Rows, after.Rows) {
		t.Fatalf("reopen search mismatch")
	}
	if got := projectionExecute(t, reopened, queries[0]); !reflect.DeepEqual(got.Rows, expected[0].Rows) || got.Execution.Path != "kcol-batch" {
		t.Fatalf("reopen columnar: %+v", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".projection-") {
			t.Fatalf("temporary build artifact survived: %s", entry.Name())
		}
	}
}

func TestProjectionSnapshotFreshnessCorruptionAndCancellation(t *testing.T) {
	engine := projectionTestDatabase(t, 2100)
	ctx := context.Background()
	if _, err := engine.RefreshProjections(ctx); err != nil {
		t.Fatal(err)
	}
	query := `SELECT SUM(price), COUNT(price), AVG(rating) FROM products`
	want := projectionExecute(t, engine, query)
	// Corrupt a later data block: partial sums must be discarded before fallback.
	file, err := snapshotfile.Open(engine.Path() + ".analytics")
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := engine.database.Catalog()
	schema, _ := schemaFromCatalog(catalog, "products")
	section, err := file.Section(schema.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, base, length := section.Outer()
	_ = file.Close()
	f, err := os.OpenFile(engine.Path()+".analytics", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Last column is bool; corrupt the selected price column in the final group.
	lastGroupRows := int64(2100 % 1024)
	offset := base + length - 3*lastGroupRows*9 + lastGroupRows
	var byteValue [1]byte
	if _, err := f.ReadAt(byteValue[:], offset); err != nil {
		t.Fatal(err)
	}
	byteValue[0] ^= 0x40
	if _, err := f.WriteAt(byteValue[:], offset); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got := projectionExecute(t, engine, query)
	if got.Execution.Path != "krow-batch" || got.Execution.Fallback == "" || !reflect.DeepEqual(got.Rows, want.Rows) {
		t.Fatalf("corruption fallback: %+v %+v", got, got.Execution)
	}
	if _, err := engine.RefreshProjections(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	projectionExecute(t, engine, `UPDATE products SET price = 7 WHERE id = 1`)
	projectionExecute(t, engine, `DELETE FROM products WHERE id = 2`)
	oldResult, err := old.Execute(ctx, query)
	if err != nil || oldResult.Execution.Path != "kcol-batch" || !reflect.DeepEqual(oldResult.Rows, want.Rows) {
		t.Fatalf("old snapshot: %+v %v", oldResult, err)
	}
	current := projectionExecute(t, engine, query)
	if current.Execution.Path != "krow-batch" {
		t.Fatal("stale columnar accepted")
	}
	if _, err := engine.Execute(ctx, `SELECT id FROM products WHERE * SEARCH 'blue' LIMIT 5`); err == nil || !strings.Contains(err.Error(), "RefreshProjections") {
		t.Fatalf("stale search: %v", err)
	}
	writeTx, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeTx.Execute(ctx, `UPDATE products SET price = 1 WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	txResult, err := writeTx.Execute(ctx, query)
	if err != nil || txResult.Execution != nil {
		t.Fatalf("write overlay bypassed: %+v %v", txResult, err)
	}
	writeTx.Rollback()
	before, err := os.ReadFile(engine.Path() + ".search")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := engine.RefreshProjections(canceled); err == nil {
		t.Fatal("canceled refresh succeeded")
	}
	after, _ := os.ReadFile(engine.Path() + ".search")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("canceled refresh changed published file")
	}
	if _, err := engine.RefreshProjections(ctx); err != nil {
		t.Fatal(err)
	}
	if got := projectionExecute(t, engine, query); got.Execution.Path != "kcol-batch" || !reflect.DeepEqual(got.Rows, current.Rows) {
		t.Fatalf("refresh: %+v", got)
	}
	if err := old.Rollback(); err != nil {
		t.Fatal(err)
	}
	projectionExecute(t, engine, `ALTER TABLE products ADD COLUMN extra INTEGER`)
	if got := projectionExecute(t, engine, query); got.Execution.Path != "krow-batch" {
		t.Fatal("DDL did not invalidate snapshot")
	}
}

func TestProjectionSnapshotConcurrentRefreshAndQueries(t *testing.T) {
	engine := projectionTestDatabase(t, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := engine.RefreshProjections(ctx); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errors := make(chan error, 4)
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for range 3 {
				var err error
				if worker == 0 {
					_, err = engine.RefreshProjections(ctx)
				} else if worker == 1 {
					_, err = engine.Execute(ctx, `SELECT id FROM products WHERE * SEARCH 'blue' LIMIT 10`)
				} else {
					_, err = engine.Execute(ctx, `SELECT SUM(price) FROM products`)
				}
				if err != nil {
					errors <- err
					return
				}
			}
		}(worker)
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestProjectionDoesNotReplaceLegacyDirectory(t *testing.T) {
	engine := projectionTestDatabase(t, 1)
	path := engine.Path() + ".search"
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RefreshProjections(context.Background()); err == nil {
		t.Fatal("legacy directory replaced")
	}
	if stat, err := os.Stat(path); err != nil || !stat.IsDir() {
		t.Fatal("legacy directory lost")
	}
}

func TestProjectionQueuedRefreshCancellation(t *testing.T) {
	engine := projectionTestDatabase(t, 1)
	engine.projectionBuilds <- struct{}{}
	defer func() { <-engine.projectionBuilds }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.RefreshProjections(ctx); err != context.Canceled {
		t.Fatalf("queued refresh ignored cancellation: %v", err)
	}
}

func BenchmarkProjectionAggregate(b *testing.B) {
	engine := projectionTestDatabase(b, 100000)
	if _, err := engine.RefreshProjections(context.Background()); err != nil {
		b.Fatal(err)
	}
	query := `SELECT SUM(price), AVG(rating), COUNT(*) FROM products WHERE price >= 25 AND price < 75 AND enabled = true`
	engine.batchAggregates, engine.experimentalProjections = false, false
	expected := projectionExecute(b, engine, query)
	for _, mode := range []string{"krow-scalar", "krow-batch", "kcol-batch"} {
		engine.batchAggregates = mode != "krow-scalar"
		engine.experimentalProjections = mode == "kcol-batch"
		b.Run(mode, func(b *testing.B) {
			check := projectionExecute(b, engine, query)
			if !reflect.DeepEqual(check.Rows, expected.Rows) {
				b.Fatalf("different answer: %+v, expected %+v", check.Rows, expected.Rows)
			}
			b.ReportAllocs()
			for b.Loop() {
				result, err := engine.Execute(context.Background(), query)
				if err != nil || len(result.Rows) != 1 {
					b.Fatalf("query: %+v %v", result, err)
				}
				if mode != "krow-scalar" && (result.Execution == nil || result.Execution.Path != mode) {
					b.Fatalf("wrong execution path: %+v", result.Execution)
				}
			}
		})
	}
}

type projectionCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (ctx *projectionCancelContext) Err() error {
	ctx.remaining--
	if ctx.remaining <= 0 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestProjectionCanceledMidBuildKeepsPublishedSearch(t *testing.T) {
	engine := projectionTestDatabase(t, 1200)
	if _, err := engine.RefreshProjections(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(engine.Path() + ".search")
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &projectionCancelContext{Context: base, cancel: cancel, remaining: 30}
	if _, err := engine.RefreshProjections(ctx); err == nil {
		t.Fatal("mid-build cancellation ignored")
	}
	after, err := os.ReadFile(engine.Path() + ".search")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("mid-build cancellation replaced committed search")
	}
	files, err := os.ReadDir(filepath.Dir(engine.Path()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".search-build-") || strings.HasPrefix(file.Name(), ".projection-") {
			t.Fatalf("leaked canceled artifact %s", file.Name())
		}
	}
}

func TestProjectionUnsupportedQueriesKeepScalarSemantics(t *testing.T) {
	for _, count := range []int{0, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			engine := projectionTestDatabase(t, count)
			if _, err := engine.RefreshProjections(context.Background()); err != nil {
				t.Fatal(err)
			}
			queries := []string{
				`SELECT SUM(price), COUNT(price), AVG(rating) FROM products`,
				`SELECT rating, SUM(price) FROM products GROUP BY rating ORDER BY rating`,
				`SELECT SUM(price) FROM products WHERE price > 50 OR enabled = true`,
				`SELECT COUNT(name), MIN(name) FROM products`,
				`SELECT SUM(price) FROM products WHERE id = 5`,
			}
			for i, query := range queries {
				engine.batchAggregates, engine.experimentalProjections = false, false
				want := projectionExecute(t, engine, query)
				engine.batchAggregates, engine.experimentalProjections = true, true
				got := projectionExecute(t, engine, query)
				if !reflect.DeepEqual(want.Rows, got.Rows) {
					t.Fatalf("fallback %s: %+v, expected %+v", query, got, want)
				}
				if i > 0 && got.Execution != nil {
					t.Fatalf("unsupported query used batch: %s", query)
				}
			}
		})
	}
}
