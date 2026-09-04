package relational

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestIndexOnlyAggregateMatchesKROWForEqualityAndRange(t *testing.T) {
	engine := openCoveringAggregateTestEngine(t)
	defer engine.Close()
	ctx := context.Background()
	query := `
		SELECT COUNT(*) AS rows, COUNT(score) AS scored,
			SUM(score) AS total, AVG(score) AS average,
			MIN(score) AS minimum, MAX(score) AS maximum
		FROM metrics
		WHERE tenant = 'a' AND score >= 10
	`

	planned, err := engine.Execute(ctx, "EXPLAIN "+query)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Rows) != 1 || planned.Rows[0][1] != "index-only aggregate" ||
		!strings.Contains(planned.Rows[0][2].(string), "index=metrics_tenant_score_id_idx") ||
		!strings.Contains(planned.Rows[0][2].(string), "equality_prefix=1") ||
		!strings.Contains(planned.Rows[0][2].(string), "range=score") {
		t.Fatalf("covering aggregate plan = %#v", planned.Rows)
	}

	covered, err := engine.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(2), int64(2), int64(30), "15", int64(10), int64(20)}}
	if !reflect.DeepEqual(covered.Rows, want) || covered.Execution == nil ||
		covered.Execution.Path != "index-only-aggregate" || covered.Execution.IndexEntriesScanned != 2 ||
		covered.Execution.RowsMatched != 2 || covered.Execution.RowsScanned != 0 ||
		covered.Execution.PointLookups != 0 {
		t.Fatalf("covering aggregate = %+v, want rows %#v", covered, want)
	}

	schema, index := coveringAggregateTestIndex(t, engine)
	markTestIndexUnready(t, engine, schema, index)
	baseline, err := engine.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(covered.Rows, baseline.Rows) {
		t.Fatalf("covering rows = %#v, KROW rows = %#v", covered.Rows, baseline.Rows)
	}
	if baseline.Execution != nil && baseline.Execution.Path == "index-only-aggregate" {
		t.Fatalf("unready index still selected: %+v", baseline.Execution)
	}
}

func TestIndexOnlyAggregatePreservesNULLAndGroupSemantics(t *testing.T) {
	engine := openCoveringAggregateTestEngine(t)
	defer engine.Close()
	ctx := context.Background()
	query := `
		SELECT tenant, COUNT(*) AS rows, COUNT(score) AS scored,
			SUM(score) AS total, AVG(score) AS average,
			MIN(score) AS minimum, MAX(score) AS maximum
		FROM metrics
		WHERE tenant >= 'a' AND tenant <= 'b'
		GROUP BY tenant
		ORDER BY tenant
	`
	got, err := engine.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{
		{"a", int64(3), int64(2), int64(30), "15", int64(10), int64(20)},
		{"b", int64(2), int64(1), int64(5), "5", int64(5), int64(5)},
	}
	if !reflect.DeepEqual(got.Rows, want) || got.Execution == nil ||
		got.Execution.Path != "index-only-aggregate" || got.Execution.RowsScanned != 0 ||
		got.Execution.IndexEntriesScanned != 5 || got.Execution.RowsMatched != 5 ||
		got.Execution.Groups != 2 {
		t.Fatalf("NULL/group covering aggregate = %+v, want rows %#v", got, want)
	}

	analyzed, err := engine.Execute(ctx, "EXPLAIN ANALYZE "+query)
	if err != nil {
		t.Fatal(err)
	}
	if analyzed.Execution == nil || analyzed.Execution.Path != "index-only-aggregate" ||
		!explainDetailContains(analyzed, "rows", "scanned=0 matched=5 groups=2") ||
		!explainDetailContains(analyzed, "index", "entries_scanned=5 point_lookups=0") {
		t.Fatalf("EXPLAIN ANALYZE covering aggregate = %+v", analyzed)
	}
}

func TestIndexOnlyAggregateUsesFullCoveringIndexAndNULLPrefix(t *testing.T) {
	engine := openCoveringAggregateTestEngine(t)
	defer engine.Close()
	ctx := context.Background()

	full, err := engine.Execute(ctx, `
		SELECT tenant, COUNT(score) AS scored, SUM(score) AS total
		FROM metrics GROUP BY tenant ORDER BY tenant
	`)
	if err != nil {
		t.Fatal(err)
	}
	wantFull := [][]any{
		{"a", int64(2), int64(30)},
		{"b", int64(1), int64(5)},
		{"c", int64(1), int64(100)},
	}
	if !reflect.DeepEqual(full.Rows, wantFull) || full.Execution == nil ||
		full.Execution.Path != "index-only-aggregate" || full.Execution.RowsScanned != 0 ||
		full.Execution.IndexEntriesScanned != 6 {
		t.Fatalf("full covering aggregate = %+v, want rows %#v", full, wantFull)
	}

	nulls, err := engine.Execute(ctx, `
		SELECT COUNT(*) AS rows, COUNT(score) AS scored
		FROM metrics WHERE tenant = 'a' AND score IS NULL
	`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(nulls.Rows, [][]any{{int64(1), int64(0)}}) || nulls.Execution == nil ||
		nulls.Execution.Path != "index-only-aggregate" || nulls.Execution.IndexEntriesScanned != 1 ||
		nulls.Execution.RowsMatched != 1 {
		t.Fatalf("NULL-prefix covering aggregate = %+v", nulls)
	}
}

func TestIndexOnlyAggregateKeepsBIGINTExact(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "covering-bigint.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE ledger (tenant TEXT NOT NULL, id BIGINT NOT NULL, amount BIGINT, PRIMARY KEY (tenant,id))`,
		`CREATE INDEX ledger_tenant_amount_id_idx ON ledger (tenant,amount,id)`,
		`INSERT INTO ledger (tenant,id,amount) VALUES
			('a',1,9007199254740993), ('a',2,9007199254740995), ('b',1,1)`,
	} {
		if _, err := engine.Execute(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	got, err := engine.Execute(ctx, `
		SELECT SUM(amount) AS total, AVG(amount) AS average, MIN(amount), MAX(amount)
		FROM ledger WHERE tenant = 'a'
	`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{"18014398509481988", "9007199254740994", int64(9007199254740993), int64(9007199254740995)}}
	if !reflect.DeepEqual(got.Rows, want) || got.Execution == nil ||
		got.Execution.Path != "index-only-aggregate" || got.Execution.IndexEntriesScanned != 2 {
		t.Fatalf("exact BIGINT covering aggregate = %+v, want rows %#v", got, want)
	}
}

func TestIndexOnlyAggregateUsesKROWBooleanRepresentation(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "covering-boolean.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE flags (tenant TEXT NOT NULL, id BIGINT NOT NULL, active BOOLEAN, PRIMARY KEY (tenant,id))`,
		`CREATE INDEX flags_tenant_active_id_idx ON flags (tenant,active,id)`,
		`INSERT INTO flags (tenant,id,active) VALUES ('a',1,TRUE), ('a',2,FALSE), ('a',3,NULL), ('b',1,TRUE)`,
	} {
		if _, err := engine.Execute(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	got, err := engine.Execute(ctx, `
		SELECT COUNT(*) AS rows, COUNT(active) AS present, MIN(active), MAX(active)
		FROM flags WHERE tenant = 'a' AND active IS NOT NULL
	`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(2), int64(2), false, true}}
	if !reflect.DeepEqual(got.Rows, want) || got.Execution == nil ||
		got.Execution.Path != "index-only-aggregate" || got.Execution.RowsMatched != 2 {
		t.Fatalf("boolean covering aggregate = %+v, want rows %#v", got, want)
	}

	trueOnly, err := engine.Execute(ctx, `SELECT COUNT(*) FROM flags WHERE tenant = 'a' AND active = TRUE`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(trueOnly.Rows, [][]any{{int64(1)}}) || trueOnly.Execution == nil ||
		trueOnly.Execution.Path != "index-only-aggregate" || trueOnly.Execution.IndexEntriesScanned != 1 {
		t.Fatalf("boolean equality covering aggregate = %+v", trueOnly)
	}
}

func TestIndexOnlyAggregateReadsTransactionOverlay(t *testing.T) {
	engine := openCoveringAggregateTestEngine(t)
	defer engine.Close()
	ctx := context.Background()
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Execute(ctx, `INSERT INTO metrics (tenant,id,score,payload) VALUES ('a',4,30,'new')`); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Execute(ctx, `DELETE FROM metrics WHERE tenant = 'a' AND id = 2`); err != nil {
		t.Fatal(err)
	}
	got, err := transaction.Execute(ctx, `
		SELECT COUNT(*) AS rows, SUM(score) AS total, MIN(score) AS minimum, MAX(score) AS maximum
		FROM metrics WHERE tenant = 'a'
	`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(3), int64(50), int64(20), int64(30)}}
	if !reflect.DeepEqual(got.Rows, want) || got.Execution == nil ||
		got.Execution.Path != "index-only-aggregate" || got.Execution.RowsScanned != 0 ||
		got.Execution.RowsMatched != 3 {
		t.Fatalf("overlay covering aggregate = %+v, want rows %#v", got, want)
	}
}

func TestIndexOnlyAggregateFallsBackWhenIndexDoesNotCoverPredicateOrValue(t *testing.T) {
	engine := openCoveringAggregateTestEngine(t)
	defer engine.Close()
	ctx := context.Background()
	queries := []string{
		`SELECT COUNT(*) FROM metrics WHERE tenant = 'a' AND payload = 'ten'`,
		`SELECT MIN(payload) FROM metrics WHERE tenant = 'a'`,
	}
	for _, query := range queries {
		planned, err := engine.Execute(ctx, "EXPLAIN "+query)
		if err != nil {
			t.Fatal(err)
		}
		if len(planned.Rows) != 1 || planned.Rows[0][1] == "index-only aggregate" {
			t.Fatalf("non-covering query selected index-only path: %#v", planned.Rows)
		}
		if _, err := engine.Execute(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIndexOnlyAggregateUsesBoundedScanWithCoveredResidual(t *testing.T) {
	engine := openCoveringAggregateTestEngine(t)
	defer engine.Close()
	ctx := context.Background()
	query := `
		SELECT COUNT(*) AS rows, SUM(score) AS total
		FROM metrics
		WHERE tenant = 'a' AND score IN (10, 20)
	`
	planned, err := engine.Execute(ctx, "EXPLAIN "+query)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Rows) != 1 || planned.Rows[0][1] != "index-only aggregate" ||
		!strings.Contains(planned.Rows[0][2].(string), "equality_prefix=1") {
		t.Fatalf("covered residual plan = %#v", planned.Rows)
	}
	got, err := engine.Execute(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(2), int64(30)}}
	if !reflect.DeepEqual(got.Rows, want) || got.Execution == nil ||
		got.Execution.Path != "index-only-aggregate" || got.Execution.IndexEntriesScanned != 3 ||
		got.Execution.RowsMatched != 2 || got.Execution.RowsScanned != 0 {
		t.Fatalf("covered residual aggregate = %+v, want rows %#v", got, want)
	}
}

func openCoveringAggregateTestEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := Open(filepath.Join(t.TempDir(), "covering-aggregate.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	statements := []string{
		`CREATE TABLE metrics (
			tenant TEXT NOT NULL,
			id BIGINT NOT NULL,
			score INTEGER,
			payload TEXT NOT NULL,
			PRIMARY KEY (tenant,id)
		)`,
		`CREATE INDEX metrics_tenant_score_id_idx ON metrics (tenant,score,id)`,
		`INSERT INTO metrics (tenant,id,score,payload) VALUES
			('a',1,NULL,'none'), ('a',2,10,'ten'), ('a',3,20,'twenty'),
			('b',1,NULL,'none'), ('b',2,5,'five'), ('c',1,100,'hundred')`,
	}
	for _, statement := range statements {
		if _, err := engine.Execute(ctx, statement); err != nil {
			engine.Close()
			t.Fatal(err)
		}
	}
	if _, err := engine.Checkpoint(); err != nil {
		engine.Close()
		t.Fatal(err)
	}
	return engine
}

func coveringAggregateTestIndex(t *testing.T, engine *Engine) (kitdbsql.Schema, secondaryIndex) {
	t.Helper()
	engine.mu.RLock()
	schema, err := engine.schemaLocked("metrics")
	engine.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	indexes, err := collectSecondaryIndexes(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		if index.name == "metrics_tenant_score_id_idx" {
			return schema, index
		}
	}
	t.Fatal("covering aggregate index was not declared")
	return kitdbsql.Schema{}, secondaryIndex{}
}
