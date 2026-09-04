package work

import (
	"context"
	"fmt"
	"math"

	"github.com/kitwork/engine/value"
)

const (
	kitDBCreateManyDefaultBatch = 256
	kitDBCreateManyMaximumBatch = 256
	kitDBCreateManyRowLimit     = 10_000

	kitDBCreateManyFailedCode  = "KITDB_CREATE_MANY_FAILED"
	kitDBCreateManyPartialCode = "KITDB_CREATE_MANY_PARTIAL"
)

// CreateMany ingests a bounded array through ordinary relational
// transactions. Each committed batch is independently durable; a failed batch
// is rolled back and reports the first safe resume offset as `next`.
//
//	db.products.createMany(rows, { batch: 128 })
//
// Inside db.transaction(), the call remains part of the caller's transaction
// and therefore accepts at most one batch.
func (t *SchemaTable) CreateMany(args ...value.Value) value.Value {
	if v, ok := t.ready(); !ok {
		return v
	}
	if t.engine != "kitdb" {
		return value.Value{K: value.Invalid, V: "db.createMany: available only for KitDB"}
	}
	rows, batch, err := parseKitDBCreateManyArguments(args)
	if err != nil {
		return value.Value{K: value.Invalid, V: "db.createMany: " + err.Error()}
	}
	ctx := context.Background()
	if t.scope != nil {
		ctx = t.scope.Context()
	}
	return t.createManyKitDB(ctx, rows, batch)
}

func parseKitDBCreateManyArguments(args []value.Value) ([]value.Value, int, error) {
	if len(args) == 0 || len(args) > 2 || args[0].K != value.Array {
		return nil, 0, fmt.Errorf("expects an array and optional { batch } object")
	}
	rows := args[0].Array()
	if len(rows) > kitDBCreateManyRowLimit {
		return nil, 0, fmt.Errorf("accepts at most %d rows per call", kitDBCreateManyRowLimit)
	}
	for index, row := range rows {
		if row.K != value.Map {
			return nil, 0, fmt.Errorf("row %d must be an object", index)
		}
	}

	batch := kitDBCreateManyDefaultBatch
	if len(args) == 1 {
		return rows, batch, nil
	}
	if args[1].K != value.Map {
		return nil, 0, fmt.Errorf("options must be an object containing batch")
	}
	options := args[1].Map()
	for name := range options {
		if name != "batch" {
			return nil, 0, fmt.Errorf("unknown option %q; use batch", name)
		}
	}
	if raw, found := options["batch"]; found {
		if raw.K != value.Number || math.IsNaN(raw.N) || math.IsInf(raw.N, 0) || raw.N != math.Trunc(raw.N) {
			return nil, 0, fmt.Errorf("batch must be an integer")
		}
		batch = int(raw.N)
		if batch < 1 || batch > kitDBCreateManyMaximumBatch {
			return nil, 0, fmt.Errorf("batch must be between 1 and %d", kitDBCreateManyMaximumBatch)
		}
	}
	return rows, batch, nil
}

func (t *SchemaTable) createManyKitDB(
	ctx context.Context,
	rows []value.Value,
	batch int,
) value.Value {
	if len(rows) == 0 {
		return kitDBCreateManyProgress(0, 0, 0, true, -1)
	}
	if t.transaction != nil {
		if len(rows) > batch {
			return kitDBCreateManyFailure(
				0, 0, 0, 0,
				fmt.Errorf("inside db.transaction() accepts at most one batch of %d rows", batch),
			)
		}
		return t.createManyKitDBTransaction(ctx, rows, 0, 0, t.transaction, false)
	}

	inserted := 0
	batches := 0
	for inserted < len(rows) {
		if err := contextError(ctx); err != nil {
			return kitDBCreateManyFailure(inserted, batches, inserted, inserted, err)
		}
		end := min(inserted+batch, len(rows))
		transaction, owned, err := t.kitDBWriteTransaction()
		if err != nil {
			return kitDBCreateManyFailure(inserted, batches, inserted, inserted, err)
		}
		result := t.createManyKitDBTransaction(
			ctx, rows[inserted:end], inserted, batches, transaction, owned,
		)
		if result.IsError {
			return result
		}
		inserted = end
		batches++
	}
	return kitDBCreateManyProgress(inserted, batches, inserted, true, -1)
}

func (t *SchemaTable) createManyKitDBTransaction(
	ctx context.Context,
	rows []value.Value,
	start int,
	batches int,
	transaction *kitDBRecordTransaction,
	owned bool,
) value.Value {
	savepoint := transaction.Savepoint()
	batchTable := *t
	batchTable.transaction = transaction
	rollback := func() {
		if owned {
			_ = transaction.Rollback()
			return
		}
		transaction.RollbackTo(savepoint)
	}

	for offset, item := range rows {
		if err := contextError(ctx); err != nil {
			rollback()
			return kitDBCreateManyFailure(start, batches, start, start+offset, err)
		}
		row, message := fillRow(batchTable.columns, item.Map())
		if message != "" {
			rollback()
			return kitDBCreateManyFailure(
				start, batches, start, start+offset,
				fmt.Errorf("row %d: table %q %s", start+offset, batchTable.table, message),
			)
		}
		if err := batchTable.writeKitDBRowInTransaction(transaction, row); err != nil {
			rollback()
			return kitDBCreateManyFailure(
				start, batches, start, start+offset,
				fmt.Errorf("row %d: %w", start+offset, err),
			)
		}
	}
	if err := batchTable.invalidateKitDBStatistics(transaction); err != nil {
		rollback()
		return kitDBCreateManyFailure(start, batches, start, start, err)
	}
	if owned {
		if _, err := transaction.CommitContext(ctx); err != nil {
			return kitDBCreateManyFailure(start, batches, start, start, err)
		}
		t.markKitDBStatisticsStale()
		return kitDBCreateManyProgress(start+len(rows), batches+1, start+len(rows), true, -1)
	}
	return kitDBCreateManyProgress(len(rows), 1, len(rows), true, -1)
}

func kitDBCreateManyProgress(inserted, batches, next int, complete bool, failedRow int) value.Value {
	progress := map[string]value.Value{
		"inserted": value.New(inserted),
		"batches":  value.New(batches),
		"next":     value.New(next),
		"complete": value.New(complete),
	}
	if failedRow >= 0 {
		progress["failedRow"] = value.New(failedRow)
	}
	return value.New(progress)
}

func kitDBCreateManyFailure(
	inserted, batches, next, failedRow int,
	err error,
) value.Value {
	result := kitDBCreateManyProgress(inserted, batches, next, false, failedRow)
	code := kitDBCreateManyFailedCode
	if inserted > 0 {
		code = kitDBCreateManyPartialCode
	}
	message := fmt.Sprintf(
		"kitdb: createMany stopped at row %d after %d durable rows: %v",
		failedRow, inserted, err,
	)
	return value.WithFailure(result, code, message)
}
