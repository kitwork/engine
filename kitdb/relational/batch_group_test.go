package relational

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/snapshotfile"
	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestBatchGroupMatchesScalar(t *testing.T) {
	e := projectionTestDatabase(t, 2507)
	queries := []string{
		`SELECT price, COUNT(*) AS n, COUNT(rating) AS rated, SUM(rating) AS total, AVG(rating) AS mean, MIN(rating) AS lo, MAX(rating) AS hi FROM products GROUP BY price ORDER BY price`,
		`SELECT enabled, COUNT(*) AS n, SUM(price) AS total, MIN(enabled), MAX(enabled) FROM products GROUP BY enabled ORDER BY total DESC`,
		`SELECT price, enabled, COUNT(*) AS n FROM products WHERE rating >= 1 AND rating < 4 GROUP BY price,enabled ORDER BY price DESC,enabled LIMIT 17 OFFSET 3`,
		`SELECT enabled AS flag, COUNT(*) AS n, SUM(price) AS total FROM products GROUP BY enabled HAVING n > 1 AND total > 100 ORDER BY total DESC LIMIT 1`,
		`SELECT price FROM products GROUP BY price ORDER BY price`,
		`SELECT enabled, COUNT(*) FROM products GROUP BY enabled,price ORDER BY enabled`,
		`SELECT price, COUNT(*) FROM products WHERE price IS NULL GROUP BY price ORDER BY price`,
		`SELECT price, COUNT(*) FROM products WHERE price < 0 GROUP BY price`,
		`SELECT price, COUNT(*) FROM products GROUP BY price LIMIT 0`,
		`SELECT price, COUNT(*) FROM products GROUP BY price,price ORDER BY price`,
	}
	e.batchAggregates, e.experimentalProjections = false, false
	wants := make([]Result, len(queries))
	for i, q := range queries {
		wants[i] = projectionExecute(t, e, q)
	}
	e.batchAggregates = true
	for _, mode := range []string{"krow-batch", "kcol-batch"} {
		if mode == "kcol-batch" {
			e.experimentalProjections = true
			if _, err := e.RefreshAnalytics(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		for i, q := range queries {
			got := projectionExecute(t, e, q)
			if got.Execution == nil || got.Execution.Path != mode || !reflect.DeepEqual(got.Rows, wants[i].Rows) || !reflect.DeepEqual(got.Columns, wants[i].Columns) {
				t.Fatalf("%s: %s\ngot=%+v stats=%+v\nwant=%+v", mode, q, got, got.Execution, wants[i])
			}
			if got.Execution.RowsScanned+got.Execution.RowsSkipped+got.Execution.RowsFromMetadata != 2507 ||
				got.Execution.Batches+got.Execution.BatchesSkipped+got.Execution.BatchesFromMetadata != 3 {
				t.Fatalf("wrong scan counters: %+v", got.Execution)
			}
		}
	}
}

func TestBatchGroupNullsEmptyAndParameters(t *testing.T) {
	e := projectionTestDatabase(t, 0)
	query := `SELECT price,enabled,COUNT(*) AS n,COUNT(rating) AS rated,SUM(rating) AS total FROM products GROUP BY price,enabled ORDER BY price,enabled`
	if r := projectionExecute(t, e, query); len(r.Rows) != 0 || r.Execution == nil || r.Execution.Groups != 0 {
		t.Fatalf("empty grouped input must have no groups: %+v", r)
	}
	projectionExecute(t, e, `INSERT INTO products (id,price,enabled,rating) VALUES (1,NULL,NULL,NULL),(2,NULL,NULL,NULL),(3,0,NULL,NULL),(4,0,false,NULL),(5,-3,true,1.5),(6,-3,true,2.5)`)
	want := [][]any{{nil, nil, int64(2), int64(0), nil}, {int64(-3), true, int64(2), int64(2), float64(4)}, {int64(0), nil, int64(1), int64(0), nil}, {int64(0), false, int64(1), int64(0), nil}}
	for _, projected := range []bool{false, true} {
		e.experimentalProjections = projected
		if projected {
			if _, err := e.RefreshAnalytics(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		got := projectionExecute(t, e, query)
		if !reflect.DeepEqual(got.Rows, want) || got.Execution.Groups != 4 {
			t.Fatalf("null/zero/false group identities: %+v", got)
		}
		r, err := e.Execute(context.Background(), `SELECT price, COUNT(*) AS n, SUM(rating) AS total FROM products WHERE price >= $1 GROUP BY price HAVING n >= $2 AND total IS NOT NULL ORDER BY total DESC`, int64(-3), int64(2))
		if err != nil || !reflect.DeepEqual(r.Rows, [][]any{{int64(-3), int64(2), float64(4)}}) {
			t.Fatalf("parameters/HAVING: %+v %v", r, err)
		}
		for _, bad := range []string{
			`SELECT price,COUNT(*) AS n FROM products GROUP BY price HAVING missing > 1`,
			`SELECT price,COUNT(*) AS n FROM products GROUP BY price ORDER BY missing`,
			`SELECT price,COUNT(*) AS n FROM products GROUP BY price HAVING n > $1`,
		} {
			if _, err := e.Execute(context.Background(), bad); err == nil {
				t.Fatalf("invalid grouped query accepted: %s", bad)
			}
		}
	}
}

func TestBatchGroupFallbackShapes(t *testing.T) {
	e := projectionTestDatabase(t, 97)
	queries := []string{
		`SELECT name,COUNT(*),SUM(price) FROM products GROUP BY name ORDER BY name`,
		`SELECT rating,COUNT(*) FROM products GROUP BY rating ORDER BY rating`,
		`SELECT enabled,MIN(name) FROM products GROUP BY enabled ORDER BY enabled`,
		`SELECT enabled,COUNT(*) FROM products WHERE name LIKE 'blue%' GROUP BY enabled ORDER BY enabled`,
		`SELECT enabled,COUNT(*) FROM products WHERE price < 5 OR price > 90 GROUP BY enabled ORDER BY enabled`,
		`SELECT enabled,COUNT(*) FROM products WHERE id >= 10 AND id < 20 GROUP BY enabled ORDER BY enabled`,
	}
	for _, q := range queries {
		e.batchAggregates, e.experimentalProjections = false, false
		want := projectionExecute(t, e, q)
		e.batchAggregates, e.experimentalProjections = true, true
		got := projectionExecute(t, e, q)
		if got.Execution != nil || !reflect.DeepEqual(got.Rows, want.Rows) {
			t.Fatalf("unsupported shape did not keep scalar/index semantics: %s %+v", q, got)
		}
	}
}

func TestBatchGroupLegacyScalarRepresentations(t *testing.T) {
	e := projectionTestDatabase(t, 0)
	projectionExecute(t, e, `INSERT INTO products (id,price,enabled) VALUES (1,7,true),(2,7,true)`)
	catalog, err := e.database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := schemaFromCatalog(catalog, "products")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i, values := range []map[string]any{
		{"id": int64(1), "price": float64(7), "enabled": int64(1)},
		{"id": int64(2), "price": int64(7), "enabled": true},
	} {
		key, err := rowKey(schema, map[string]any{"id": int64(i + 1)}, 0)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeRow(schema, values, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Put(key, encoded); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	query := `SELECT price,enabled,COUNT(*) FROM products GROUP BY price,enabled`
	for _, mode := range []string{"scalar", "batch", "columnar"} {
		e.batchAggregates = mode != "scalar"
		e.experimentalProjections = mode == "columnar"
		if mode == "columnar" {
			if _, err := e.RefreshAnalytics(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		got := projectionExecute(t, e, query)
		if !reflect.DeepEqual(got.Rows, [][]any{{int64(7), true, int64(2)}}) {
			t.Fatalf("%s split equivalent logical values: %v", mode, got.Rows)
		}
	}
}

func TestBatchGroupFreshnessAndWriteOverlay(t *testing.T) {
	e := projectionTestDatabase(t, 2100)
	ctx := context.Background()
	query := `SELECT price,enabled,COUNT(*) AS n,SUM(rating) AS total FROM products GROUP BY price,enabled ORDER BY price,enabled`
	if _, err := e.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	before := projectionExecute(t, e, query)
	old, err := e.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Rollback()
	projectionExecute(t, e, `UPDATE products SET price=-7,enabled=false WHERE id=1`)
	projectionExecute(t, e, `DELETE FROM products WHERE id=2`)
	projectionExecute(t, e, `INSERT INTO products (id,price,rating,enabled) VALUES (3000,3,8.5,true)`)
	oldResult, err := old.Execute(ctx, query)
	if err != nil || oldResult.Execution == nil || oldResult.Execution.Path != "kcol-batch" || !reflect.DeepEqual(oldResult.Rows, before.Rows) {
		t.Fatalf("old snapshot: %+v %v", oldResult, err)
	}
	e.batchAggregates, e.experimentalProjections = false, false
	want := projectionExecute(t, e, query)
	e.batchAggregates, e.experimentalProjections = true, true
	got := projectionExecute(t, e, query)
	if got.Execution.Path != "krow-batch" || got.Execution.Fallback == "" || !reflect.DeepEqual(got.Rows, want.Rows) {
		t.Fatalf("stale projection: %+v", got)
	}
	writer, err := e.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.Execute(ctx, `UPDATE products SET price=9 WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	e.batchAggregates, e.experimentalProjections = false, false
	wantWrite, err := writer.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	e.batchAggregates, e.experimentalProjections = true, true
	gotWrite, err := writer.Execute(ctx, query)
	if err != nil || gotWrite.Execution != nil || !reflect.DeepEqual(gotWrite.Rows, wantWrite.Rows) || reflect.DeepEqual(gotWrite.Rows, got.Rows) {
		t.Fatalf("uncommitted write ignored: %+v %v", gotWrite, err)
	}
	if err := writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	got = projectionExecute(t, e, query)
	if got.Execution.Path != "kcol-batch" || !reflect.DeepEqual(got.Rows, want.Rows) {
		t.Fatalf("refreshed group: %+v", got)
	}
	oldResult, err = old.Execute(ctx, query)
	if err != nil || oldResult.Execution.Path != "krow-batch" || !reflect.DeepEqual(oldResult.Rows, before.Rows) {
		t.Fatalf("new sidecar mixed into old snapshot: %+v %v", oldResult, err)
	}
	if err := old.Rollback(); err != nil {
		t.Fatal(err)
	}
	path := e.Path()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(path, Options{ExperimentalProjections: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got = projectionExecute(t, reopened, query)
	if got.Execution.Path != "kcol-batch" || !reflect.DeepEqual(got.Rows, want.Rows) {
		t.Fatalf("reopened groups: %+v", got)
	}
}

func TestBatchGroupDiscardsPartialCorruptProjection(t *testing.T) {
	e := projectionTestDatabase(t, 2100)
	if _, err := e.RefreshAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}
	query := `SELECT price,COUNT(*) AS n,SUM(rating) AS total FROM products GROUP BY price ORDER BY price`
	want := projectionExecute(t, e, query)
	file, err := snapshotfile.Open(e.Path() + ".analytics")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := e.database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := schemaFromCatalog(catalog, "products")
	if err != nil {
		t.Fatal(err)
	}
	section, err := file.Section(schema.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, base, length := section.Outer()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(e.Path()+".analytics", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	// In this initial contiguous section price precedes rating and enabled.
	lastRows := int64(2100 % columnar.BatchRows)
	offset := base + length - 3*lastRows*9 + lastRows
	var value [1]byte
	if _, err := f.ReadAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	value[0] ^= 0x40
	if _, err := f.WriteAt(value[:], offset); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got := projectionExecute(t, e, query)
	if !reflect.DeepEqual(got.Rows, want.Rows) || got.Execution.Path != "krow-batch" || got.Execution.Fallback == "" || got.Execution.RowsScanned != 2100 || got.Execution.Groups != want.Execution.Groups {
		t.Fatalf("partial groups survived fallback: stats=%+v", got.Execution)
	}
}

func TestBatchGroupBudgetsAndAdmission(t *testing.T) {
	e := projectionTestDatabase(t, 2100)
	if _, err := e.RefreshAnalytics(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.maximumResultRows = 2
	for _, projected := range []bool{false, true} {
		e.experimentalProjections = projected
		// LIMIT/HAVING must not hide groups encountered during the full scan.
		for _, suffix := range []string{" LIMIT 1", " HAVING n > 10000"} {
			r, err := e.Execute(context.Background(), `SELECT price,COUNT(*) AS n FROM products GROUP BY price`+suffix)
			if err == nil || !strings.Contains(err.Error(), "2-group limit") || len(r.Rows) != 0 {
				t.Fatalf("group budget: %+v %v", r, err)
			}
		}
	}
	e.maximumResultRows = DefaultMaximumResultRows
	var wide strings.Builder
	wide.WriteString("SELECT id")
	for i := range 128 {
		fmt.Fprintf(&wide, ",COUNT(*) AS n%d", i)
	}
	wide.WriteString(" FROM products GROUP BY id LIMIT 1")
	if r, err := e.Execute(context.Background(), wide.String()); err == nil || !strings.Contains(err.Error(), "state budget") || len(r.Rows) != 0 {
		t.Fatalf("wide group state was not bounded: %v", err)
	}
	ctx := context.Background()
	tx, err := e.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	schema, err := tx.schema("products")
	if err != nil {
		t.Fatal(err)
	}
	fields := columnarFields(schema)
	sentinel := errors.New("aggregate consumer error")
	reset := false
	_, err = tx.scanAggregateBatches(ctx, schema, 0, fields, nil, nil, func(*columnar.Batch) error { return sentinel }, func() { reset = true }, false)
	if !errors.Is(err, sentinel) || reset {
		t.Fatalf("consumer error retried on KROW: %v reset=%t", err, reset)
	}
	canceled, cancel := context.WithCancel(ctx)
	_, err = tx.scanAggregateBatches(canceled, schema, 0, fields, nil, nil, func(*columnar.Batch) error { cancel(); return nil }, func() { reset = true }, false)
	cancel()
	if !errors.Is(err, context.Canceled) || reset {
		t.Fatalf("canceled group retried: %v reset=%t", err, reset)
	}
	for range cap(e.projectionQueries) {
		e.projectionQueries <- struct{}{}
	}
	waitCtx, stop := context.WithTimeout(ctx, 10*time.Millisecond)
	_, err = e.Execute(waitCtx, `SELECT price,COUNT(*) FROM products GROUP BY price`)
	stop()
	for range cap(e.projectionQueries) {
		<-e.projectionQueries
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission ignored cancellation: %v", err)
	}
}

func TestBatchGroupKeyOwnershipAllocationAndByteBudget(t *testing.T) {
	fields := []columnar.Field{{Tag: 1, Kind: columnar.Integer}, {Tag: 2, Kind: columnar.Boolean}}
	batch, err := columnar.NewBatch(fields)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []aggregateBinding{{field: &kitdbsql.Field{Kind: "integer"}}, {function: "count"}}
	groups := newBatchGroups([]int{0, 1}, bindings, 10, nil)
	positions := []int{0, -1}
	batch.Rows = 1
	for i := range batch.Columns {
		batch.Columns[i].Valid[0] = 1
	}
	for _, value := range []int64{-9007199254740991, 9007199254740991, 0, -9007199254740991} {
		batch.Columns[0].Integers[0] = value
		if _, err := groups.resolve(batch, 0, positions); err != nil {
			t.Fatal(err)
		}
	}
	if len(groups.groups) != 3 || groups.groups[0].values[0] != int64(-9007199254740991) {
		t.Fatal("group keys/values retained reusable scratch")
	}
	if allocations := testing.AllocsPerRun(1000, func() {
		if _, err := groups.resolve(batch, 0, positions); err != nil {
			t.Fatal(err)
		}
	}); allocations != 0 {
		t.Fatalf("existing group lookup allocated %v times", allocations)
	}
	budget := newMaterializationBudget(10)
	working := newMaterializationWorkingSet(budget)
	bounded := newBatchGroups([]int{0, 1}, bindings, 10, working)
	budget.maximumBytes = bounded.entryCost - 1
	batch.Columns[0].Integers[0] = 42
	if _, err := bounded.resolve(batch, 0, positions); err == nil ||
		!strings.Contains(err.Error(), "query memory budget while buffering GROUP BY batch state") {
		t.Fatalf("query-wide batch group budget error = %v", err)
	}
	if len(bounded.groups) != 0 || working.bytes != 0 || budget.workingBytes != 0 {
		t.Fatalf(
			"failed batch admission retained groups=%d working=%d budget=%d",
			len(bounded.groups), working.bytes, budget.workingBytes,
		)
	}
	groups.reset()
	// Exercise the exact admission boundary without allocating 16 MiB in a unit test.
	groups.entryCost = maximumBatchGroupBytes/2 - 2*9*len(groups.positions)
	for i := range 3 {
		batch.Columns[0].Integers[0] = int64(i)
		_, err := groups.resolve(batch, 0, positions)
		if i < 2 && err != nil || i == 2 && (err == nil || !strings.Contains(err.Error(), "state budget")) {
			t.Fatalf("byte boundary i=%d: %v", i, err)
		}
	}
}

func TestBatchGroupConcurrentReadersAndRefresh(t *testing.T) {
	e := projectionTestDatabase(t, 2100)
	ctx := context.Background()
	if _, err := e.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	query := `SELECT enabled,COUNT(*),SUM(price) FROM products GROUP BY enabled ORDER BY enabled`
	want := projectionExecute(t, e, query)
	var wait sync.WaitGroup
	errors := make(chan error, 5)
	for worker := range 5 {
		wait.Go(func() {
			for range 5 {
				if worker == 0 {
					if _, err := e.RefreshAnalytics(ctx); err != nil {
						errors <- err
						return
					}
					continue
				}
				got, err := e.Execute(ctx, query)
				if err != nil || got.Execution == nil || got.Execution.Path != "kcol-batch" || !reflect.DeepEqual(got.Rows, want.Rows) {
					errors <- fmt.Errorf("concurrent group mismatch: %v", err)
					return
				}
			}
		})
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
