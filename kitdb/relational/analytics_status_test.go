package relational

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/kitwork/engine/kitdb/columnar"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func TestAnalyticsStatusTracksRangeGenerationAndCanceledRefresh(t *testing.T) {
	engine := incrementalProjectionDatabase(t, columnarChunkRows+7)
	ctx := context.Background()
	projectionExecute(t, engine, `ALTER TABLE products SET PARTITION BY RANGE (id)`)

	missing, err := engine.AnalyticsStatus(ctx, "products")
	if err != nil {
		t.Fatal(err)
	}
	if missing.Status != "missing" || missing.Fresh || missing.QueryPath != "krow-batch" ||
		missing.ExpectedLayout != "kcol-v3+chunk-v6-range" || missing.PartitionStrategy != "range" ||
		missing.PartitionField != "id" || missing.CurrentTransaction == 0 {
		t.Fatalf("missing status = %+v", missing)
	}

	first, err := engine.RefreshAnalytics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := engine.AnalyticsStatus(ctx, "public.products")
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != "ready" || !ready.Fresh || ready.QueryPath != "kcol-batch" ||
		ready.SourceTransaction != ready.CurrentTransaction || ready.Rows != columnarChunkRows+7 ||
		ready.Chunks != 2 || ready.ChunkVersion != columnarRangeChunkVersion ||
		ready.SnapshotGeneration == 0 || ready.FileBytes == 0 || ready.LiveBytes == 0 {
		t.Fatalf("ready status = %+v", ready)
	}
	columns, err := engine.Describe(ctx, `PRAGMA analytics_status(products)`)
	if err != nil || !reflect.DeepEqual(columns, analyticsStatusColumns) {
		t.Fatalf("PRAGMA description = %#v, %v", columns, err)
	}
	result, err := engine.Execute(ctx, `PRAGMA analytics_status('products')`)
	if err != nil {
		t.Fatal(err)
	}
	if result.CommandTag != "SELECT 1" || len(result.Rows) != 1 || len(result.Rows[0]) != len(analyticsStatusColumns) ||
		result.Rows[0][1] != "ready" || result.Rows[0][2] != true || result.Rows[0][5] != "kcol-batch" ||
		result.Rows[0][6] != "kcol-v3+chunk-v6-range" || result.Rows[0][13] != int64(columnarRangeChunkVersion) {
		t.Fatalf("PRAGMA result = %+v", result)
	}
	transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	transactionStatus, err := transaction.Execute(ctx, `PRAGMA analytics_status(products)`)
	if err != nil || len(transactionStatus.Rows) != 1 || transactionStatus.Rows[0][1] != "ready" {
		t.Fatalf("transaction PRAGMA = %+v, %v", transactionStatus, err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}

	projectionExecute(t, engine, `UPDATE products SET price = 4444 WHERE id = 1`)
	query := `SELECT SUM(price), COUNT(*) FROM products`
	expected := projectionExecute(t, engine, query)
	stale, err := engine.AnalyticsStatus(ctx, "products")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Status != "stale" || stale.Fresh || stale.QueryPath != "krow-batch" ||
		stale.SourceTransaction >= stale.CurrentTransaction || stale.SnapshotGeneration != ready.SnapshotGeneration {
		t.Fatalf("stale status = %+v", stale)
	}

	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelContext := columnarAppendCancelContext{
		Context: base, cancel: cancel, path: first.AnalyticsFile, size: first.AnalyticsFileBytes,
	}
	if _, err := engine.RefreshAnalytics(cancelContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("range append cancellation = %v", err)
	}
	afterCancel, err := engine.AnalyticsStatus(ctx, "products")
	if err != nil {
		t.Fatal(err)
	}
	if afterCancel.Status != "stale" || afterCancel.SnapshotGeneration != ready.SnapshotGeneration ||
		afterCancel.SourceTransaction != ready.SourceTransaction {
		t.Fatalf("canceled refresh status = %+v", afterCancel)
	}
	if got := projectionExecute(t, engine, query); got.Execution == nil || got.Execution.Path != "krow-batch" ||
		!reflect.DeepEqual(got.Rows, expected.Rows) {
		t.Fatalf("fallback after cancellation = %+v", got)
	}

	if _, err := engine.RefreshAnalytics(ctx); err != nil {
		t.Fatal(err)
	}
	refreshed, err := engine.AnalyticsStatus(ctx, "products")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != "ready" || !refreshed.Fresh || refreshed.QueryPath != "kcol-batch" ||
		refreshed.SnapshotGeneration <= ready.SnapshotGeneration ||
		refreshed.SourceTransaction != refreshed.CurrentTransaction {
		t.Fatalf("refreshed status = %+v", refreshed)
	}
	assertCurrentProjection(t, engine, query, expected)
}

func TestAnalyticsStatusReportsDisabledWithoutOpeningSidecar(t *testing.T) {
	engine := projectionTestDatabase(t, 4)
	engine.experimentalProjections = false
	status, err := engine.AnalyticsStatus(context.Background(), "products")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "disabled" || status.Enabled || !status.Supported || status.Fresh ||
		status.QueryPath != "krow-batch" || status.FileBytes != 0 {
		t.Fatalf("disabled status = %+v", status)
	}
}

func TestRangeClusterBuilderHasOneChunkMemoryCeiling(t *testing.T) {
	fields := make([]columnar.Field, columnar.MaximumColumns)
	schema := kitdbsql.Schema{Fields: make([]kitdbsql.Field, columnar.MaximumColumns)}
	for index := range fields {
		kind, schemaKind := columnar.Integer, "bigint"
		switch index % 3 {
		case 1:
			kind, schemaKind = columnar.Float, "float"
		case 2:
			kind, schemaKind = columnar.Boolean, "bool"
		}
		fields[index] = columnar.Field{Tag: uint32(index + 1), Kind: kind}
		schema.Fields[index] = kitdbsql.Field{Tag: uint32(index + 1), Kind: schemaKind}
	}
	decoder, err := newBatchDecoderCapacity(schema, fields, columnarChunkRows)
	if err != nil {
		t.Fatal(err)
	}
	clusterer, err := newRangeChunkClusterer(fields, 0)
	if err != nil {
		t.Fatal(err)
	}
	bytes := cap(clusterer.order) * strconv.IntSize / 8
	for _, batch := range []*columnar.Batch{decoder.batch, clusterer.output} {
		for _, vector := range batch.Columns {
			bytes += cap(vector.Valid)
			bytes += 8 * (cap(vector.Integers) + cap(vector.Floats))
		}
	}
	if bytes > 11<<20 {
		t.Fatalf("range builder backing arrays = %d bytes, ceiling = %d", bytes, 11<<20)
	}
	if cap(clusterer.order) != columnarChunkRows || len(decoder.batch.Columns[0].Valid) != columnarChunkRows ||
		len(clusterer.output.Columns[0].Valid) != columnar.BatchRows {
		t.Fatalf("unexpected builder shape: order=%d source=%d output=%d", cap(clusterer.order), len(decoder.batch.Columns[0].Valid), len(clusterer.output.Columns[0].Valid))
	}
	if _, err := newBatchDecoderCapacity(schema, fields, columnarChunkRows+1); err == nil {
		t.Fatal("range decoder accepted more than one chunk")
	}
}
