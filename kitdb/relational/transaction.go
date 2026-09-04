package relational

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	maximumTransactionOperations  = 100_000
	maximumTransactionBytes       = 32 << 20
	maximumSnapshotCaptureRetries = 4
)

var ErrTransactionConflict = errors.New("kitdb: relational transaction conflict")

type TransactionOptions struct {
	ReadOnly bool
}

type transactionMutation struct {
	value   []byte
	deleted bool
}

type transactionOperation struct {
	key      []byte
	value    []byte
	deleted  bool
	byteSize int
}

// Savepoint is an in-memory boundary inside one relational transaction. It is
// valid only for the transaction that created it.
type Savepoint struct {
	transaction *Transaction
	operations  int
	bytes       int
}

// Transaction is KitDB's standalone relational transaction. It reads one
// immutable kernel snapshot and overlays a bounded write-set. Commit is
// optimistic and publishes the complete write-set as one kernel transaction.
type Transaction struct {
	mu        sync.Mutex
	engine    *Engine
	snapshot  *kitdbengine.Snapshot
	catalog   kitdbengine.CatalogSnapshot
	base      uint64
	readOnly  bool
	sequences *sequenceSession

	operations []transactionOperation
	overlay    map[string]transactionMutation
	bytes      int
	done       bool
	released   bool
}

// BeginTransaction captures a consistent catalog and data snapshot. The
// transaction must be committed or rolled back so its snapshot can be freed.
func (engine *Engine) BeginTransaction(ctx context.Context, options TransactionOptions) (*Transaction, error) {
	if ctx == nil {
		return nil, fmt.Errorf("kitdb: transaction context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	engine.mu.RLock()
	if err := engine.readyLocked(); err != nil {
		engine.mu.RUnlock()
		return nil, err
	}

	// Relational commits use writeMu too, so all three observations share one
	// boundary. The version read must stay inside the gate: otherwise a DDL
	// commit after Snapshot can make an already-consistent pair look invalid.
	// Direct kernel frontends do not share writeMu, so retry a bounded number of
	// times if one of them publishes catalog bytes during the capture.
	for attempt := 0; attempt < maximumSnapshotCaptureRetries; attempt++ {
		engine.writeMu.Lock()
		catalog, err := engine.database.Catalog()
		var snapshot *kitdbengine.Snapshot
		if err == nil {
			snapshot, err = engine.database.Snapshot()
		}
		var version kitdbengine.CatalogVersion
		if err == nil {
			version, err = engine.database.CatalogVersion()
		}
		engine.writeMu.Unlock()
		if err != nil {
			if snapshot != nil {
				_ = snapshot.Close()
			}
			engine.mu.RUnlock()
			return nil, err
		}
		if snapshot.Transaction() >= catalog.Transaction && version.Revision == catalog.Revision {
			return &Transaction{
				engine: engine, snapshot: snapshot, catalog: catalog,
				base: snapshot.Transaction(), readOnly: options.ReadOnly,
				overlay: make(map[string]transactionMutation),
			}, nil
		}
		_ = snapshot.Close()
		if err := ctx.Err(); err != nil {
			engine.mu.RUnlock()
			return nil, err
		}
	}
	engine.mu.RUnlock()
	return nil, fmt.Errorf("kitdb: catalog and data snapshot boundaries disagree after %d attempts", maximumSnapshotCaptureRetries)
}

func (transaction *Transaction) BaseTransaction() uint64 {
	if transaction == nil {
		return 0
	}
	return transaction.base
}

func (transaction *Transaction) ReadOnly() bool {
	if transaction == nil {
		return true
	}
	return transaction.readOnly
}

// Execute runs one data statement against this transaction. DDL is kept out
// until catalog and record mutations share one fully specified SQL contract.
func (transaction *Transaction) Execute(ctx context.Context, source string, parameters ...any) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("kitdb: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	statement, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return Result{}, err
	}
	return transaction.executeParsed(ctx, statement, parameters)
}

// ExecutePlan runs one typed data statement against this transaction without
// rendering and reparsing SQL. DDL remains excluded by executeParsed.
func (transaction *Transaction) ExecutePlan(
	ctx context.Context,
	plan *kitdbsql.ParsedStatement,
	parameters ...any,
) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("kitdb: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if plan == nil {
		return Result{}, fmt.Errorf("kitdb: execution plan is nil")
	}
	return transaction.executeParsed(ctx, *plan, parameters)
}

// Describe resolves result metadata against the transaction's captured
// catalog without executing or acquiring another engine lifecycle lock.
func (transaction *Transaction) Describe(ctx context.Context, source string) ([]Column, error) {
	if ctx == nil {
		return nil, fmt.Errorf("kitdb: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	statement, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return nil, err
	}
	if err := transaction.ready(); err != nil {
		return nil, err
	}
	if statement.Kind == kitdbsql.StatementPragma {
		return describePragma(statement.Pragma)
	}
	if columns, handled, err := describeSequenceSelect(statement.Select); handled {
		return columns, err
	}
	if err := resolveStatementFunctions(&statement, transaction.catalog); err != nil {
		return nil, err
	}
	switch statement.Kind {
	case kitdbsql.StatementSelect:
		return describeSelectFromCatalog(transaction.catalog, statement.Select, nil)
	case kitdbsql.StatementInsert:
		return transaction.describeReturning(statement.Insert.Table, statement.Insert.Returning)
	case kitdbsql.StatementUpdate:
		return transaction.describeReturning(statement.Update.Table, statement.Update.Returning)
	case kitdbsql.StatementDelete:
		return transaction.describeReturning(statement.Delete.Table, statement.Delete.Returning)
	case kitdbsql.StatementExplain:
		return explainColumns(), nil
	default:
		return nil, fmt.Errorf("kitdb SQL: statement has no row description")
	}
}

func (transaction *Transaction) describeReturning(table string, returning []kitdbsql.Projection) ([]Column, error) {
	if len(returning) == 0 {
		return nil, nil
	}
	schema, err := transaction.schema(table)
	if err != nil {
		return nil, err
	}
	columns, _, err := bindProjection(schema, returning)
	return columns, err
}

func (transaction *Transaction) tables() ([]Table, error) {
	if err := transaction.ready(); err != nil {
		return nil, err
	}
	tables := make([]Table, 0, len(transaction.catalog.Structs))
	for _, entry := range transaction.catalog.Structs {
		schema, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return nil, err
		}
		count, analyzed, err := readTableCount(transaction, schema)
		if err != nil {
			return nil, err
		}
		tables = append(tables, Table{
			Name: schema.Name, ID: schema.ID, Hash: schema.Hash, Fields: len(schema.Fields),
			Rows: count, Analyzed: analyzed,
		})
	}
	return tables, nil
}

func (transaction *Transaction) executeParsed(
	ctx context.Context,
	statement kitdbsql.ParsedStatement,
	parameters []any,
) (Result, error) {
	if err := transaction.ready(); err != nil {
		return Result{}, err
	}
	if transaction.readOnly && !standaloneStatementReadOnly(statement.Kind) {
		return Result{}, fmt.Errorf("kitdb: transaction is read-only")
	}
	if _, handled, err := describeSequenceSelect(statement.Select); handled {
		if err != nil {
			return Result{}, err
		}
		transaction.mu.Lock()
		if transaction.sequences == nil {
			transaction.sequences = &sequenceSession{}
		}
		sequences := transaction.sequences
		transaction.mu.Unlock()
		return sequences.execute(ctx, transaction.engine.database, statement.Select, parameters, transaction.readOnly)
	}
	if err := resolveStatementFunctions(&statement, transaction.catalog); err != nil {
		return Result{}, err
	}
	write := statement.Kind == kitdbsql.StatementInsert || statement.Kind == kitdbsql.StatementUpdate ||
		statement.Kind == kitdbsql.StatementDelete
	var savepoint Savepoint
	if write {
		var err error
		savepoint, err = transaction.Savepoint()
		if err != nil {
			return Result{}, err
		}
	}
	var result Result
	var err error
	switch statement.Kind {
	case kitdbsql.StatementInsert:
		result, err = transaction.executeInsert(ctx, statement.Insert, parameters)
	case kitdbsql.StatementSelect:
		result, err = transaction.executeReadSelect(ctx, statement.Select, parameters)
	case kitdbsql.StatementUpdate:
		result, err = transaction.executeUpdate(ctx, statement.Update, parameters)
	case kitdbsql.StatementDelete:
		result, err = transaction.executeDelete(ctx, statement.Delete, parameters)
	case kitdbsql.StatementExplain:
		if statement.ExplainAnalyze {
			result, err = transaction.executeExplainAnalyze(ctx, statement.Explain, parameters)
		} else {
			result, err = transaction.executeExplain(ctx, statement.Explain, parameters)
		}
	case kitdbsql.StatementPragma:
		result, err = transaction.executePragma(ctx, statement.Pragma)
	default:
		err = fmt.Errorf("kitdb SQL: schema statements cannot run inside a data transaction")
	}
	if err != nil && write {
		_ = transaction.RollbackTo(savepoint)
	}
	return result, err
}

func (transaction *Transaction) executeReadSelect(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
) (Result, error) {
	return transaction.executeReadSelectWithObservation(ctx, plan, parameters, false)
}

func (transaction *Transaction) executeReadSelectObserved(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
) (Result, error) {
	return transaction.executeReadSelectWithObservation(ctx, plan, parameters, true)
}

func (transaction *Transaction) executeReadSelectWithObservation(
	ctx context.Context,
	plan *kitdbsql.SelectStatement,
	parameters []any,
	observe bool,
) (Result, error) {
	budget := newMaterializationBudget(transaction.engine.maximumResultRows)
	var working *materializationWorkingSet
	if selectUsesMaterialization(plan) {
		working = newMaterializationWorkingSet(budget)
		defer working.close()
	}
	result, err := transaction.executeSelectInScope(ctx, plan, parameters, observe, nil, budget, working)
	if err != nil || !observe || budget.rows == 0 {
		return result, err
	}
	if result.Execution == nil {
		result.Execution = &ExecutionStats{Path: "materialization"}
	}
	result.Execution.RowsMaterialized = uint64(budget.rows)
	result.Execution.MaterializationDirectRows = uint64(budget.directRows)
	result.Execution.MaterializationBytes = uint64(budget.bytes)
	result.Execution.MaterializationPeakBytes = uint64(budget.peakBytes)
	return result, nil
}

func (transaction *Transaction) Savepoint() (Savepoint, error) {
	if transaction == nil {
		return Savepoint{}, fmt.Errorf("kitdb: relational transaction is closed")
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done {
		return Savepoint{}, fmt.Errorf("kitdb: relational transaction is closed")
	}
	return Savepoint{
		transaction: transaction,
		operations:  len(transaction.operations),
		bytes:       transaction.bytes,
	}, nil
}

func (transaction *Transaction) RollbackTo(savepoint Savepoint) error {
	if transaction == nil {
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done {
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	if savepoint.transaction != transaction || savepoint.operations < 0 ||
		savepoint.operations > len(transaction.operations) {
		return fmt.Errorf("kitdb: savepoint does not belong to this transaction")
	}
	transaction.operations = transaction.operations[:savepoint.operations]
	transaction.bytes = savepoint.bytes
	transaction.rebuildOverlayLocked()
	return nil
}

func (transaction *Transaction) Get(key []byte) ([]byte, bool, error) {
	if transaction == nil {
		return nil, false, fmt.Errorf("kitdb: relational transaction is closed")
	}
	transaction.mu.Lock()
	if transaction.done || transaction.snapshot == nil {
		transaction.mu.Unlock()
		return nil, false, fmt.Errorf("kitdb: relational transaction is closed")
	}
	if mutation, found := transaction.overlay[string(key)]; found {
		transaction.mu.Unlock()
		if mutation.deleted {
			return nil, false, nil
		}
		return bytes.Clone(mutation.value), true, nil
	}
	snapshot := transaction.snapshot
	transaction.mu.Unlock()
	return snapshot.Get(key)
}

func (transaction *Transaction) getWithStats(
	key []byte,
	stats *kitdbengine.CursorStats,
) ([]byte, bool, error) {
	if stats == nil {
		return transaction.Get(key)
	}
	if transaction == nil {
		return nil, false, fmt.Errorf("kitdb: relational transaction is closed")
	}
	transaction.mu.Lock()
	if transaction.done || transaction.snapshot == nil {
		transaction.mu.Unlock()
		return nil, false, fmt.Errorf("kitdb: relational transaction is closed")
	}
	if mutation, found := transaction.overlay[string(key)]; found {
		transaction.mu.Unlock()
		stats.OverlayEntriesVisited++
		if mutation.deleted {
			return nil, false, nil
		}
		return bytes.Clone(mutation.value), true, nil
	}
	snapshot := transaction.snapshot
	transaction.mu.Unlock()
	value, found, observed, err := snapshot.GetWithStats(key)
	stats.Add(observed)
	return value, found, err
}

func (transaction *Transaction) Scan(
	options kitdbengine.RangeOptions,
	visit func(key, encoded []byte) (bool, error),
) error {
	return transaction.scan(options, nil, visit)
}

func (transaction *Transaction) scan(
	options kitdbengine.RangeOptions,
	stats *kitdbengine.CursorStats,
	visit func(key, encoded []byte) (bool, error),
) error {
	if transaction == nil {
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	transaction.mu.Lock()
	if transaction.done || transaction.snapshot == nil {
		transaction.mu.Unlock()
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	snapshot := transaction.snapshot
	overlay := make(map[string]transactionMutation, len(transaction.overlay))
	for key, mutation := range transaction.overlay {
		overlay[key] = transactionMutation{value: bytes.Clone(mutation.value), deleted: mutation.deleted}
	}
	transaction.mu.Unlock()

	overlayKeys := make([]string, 0, len(overlay))
	for key := range overlay {
		if recordKeyInRange([]byte(key), options) {
			overlayKeys = append(overlayKeys, key)
		}
	}
	sort.Strings(overlayKeys)
	if options.Reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(overlayKeys)))
	}

	snapshotOptions := options
	snapshotOptions.Limit = 0
	cursor, err := snapshot.Cursor(snapshotOptions)
	if err != nil {
		return err
	}
	defer cursor.Close()
	if stats != nil {
		defer func() {
			stats.Add(cursor.Stats())
		}()
	}

	snapshotReady := cursor.Next()
	overlayIndex := 0
	emitted := 0
	for snapshotReady || overlayIndex < len(overlayKeys) {
		if options.Limit > 0 && emitted >= options.Limit {
			break
		}
		var key, encoded []byte
		useSnapshot, useOverlay := false, false
		switch {
		case !snapshotReady:
			useOverlay = true
		case overlayIndex >= len(overlayKeys):
			useSnapshot = true
		default:
			comparison := bytes.Compare(cursor.Key(), []byte(overlayKeys[overlayIndex]))
			if options.Reverse {
				useSnapshot, useOverlay = comparison >= 0, comparison <= 0
			} else {
				useSnapshot, useOverlay = comparison <= 0, comparison >= 0
			}
		}
		if useOverlay {
			overlayKey := overlayKeys[overlayIndex]
			mutation := overlay[overlayKey]
			overlayIndex++
			if stats != nil {
				stats.OverlayEntriesVisited++
			}
			if !mutation.deleted {
				key, encoded = []byte(overlayKey), mutation.value
			}
		}
		if useSnapshot {
			if !useOverlay {
				key, encoded = cursor.Key(), cursor.Value()
			}
			snapshotReady = cursor.Next()
		}
		if len(key) == 0 {
			continue
		}
		emitted++
		stop, visitErr := visit(bytes.Clone(key), bytes.Clone(encoded))
		if visitErr != nil || stop {
			return visitErr
		}
	}
	return cursor.Err()
}

func (transaction *Transaction) scanKeys(
	options kitdbengine.RangeOptions,
	stats *kitdbengine.CursorStats,
	visit func(key []byte) (bool, error),
) error {
	if transaction == nil {
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	transaction.mu.Lock()
	if transaction.done || transaction.snapshot == nil {
		transaction.mu.Unlock()
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	snapshot := transaction.snapshot
	hasOverlay := len(transaction.overlay) != 0
	transaction.mu.Unlock()

	if hasOverlay {
		return transaction.scan(options, stats, func(key, _ []byte) (bool, error) {
			return visit(key)
		})
	}
	observed, err := snapshot.ScanKeys(options, visit)
	if stats != nil {
		stats.Add(observed)
	}
	return err
}

func (transaction *Transaction) Put(key, value []byte) error {
	return transaction.add(transactionOperation{
		key: key, value: value, byteSize: len(key) + len(value),
	})
}

func (transaction *Transaction) Delete(key []byte) error {
	return transaction.add(transactionOperation{key: key, deleted: true, byteSize: len(key)})
}

func (transaction *Transaction) add(operation transactionOperation) error {
	if transaction == nil {
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done {
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	if transaction.readOnly {
		return fmt.Errorf("kitdb: transaction is read-only")
	}
	if len(operation.key) == 0 {
		return kitdbengine.ErrEmptyKey
	}
	if len(transaction.operations) >= maximumTransactionOperations ||
		operation.byteSize > maximumTransactionBytes-transaction.bytes {
		return fmt.Errorf(
			"kitdb: relational transaction exceeds %d operations or %d bytes",
			maximumTransactionOperations, maximumTransactionBytes,
		)
	}
	operation.key = bytes.Clone(operation.key)
	operation.value = bytes.Clone(operation.value)
	transaction.operations = append(transaction.operations, operation)
	transaction.bytes += operation.byteSize
	transaction.overlay[string(operation.key)] = transactionMutation{
		value: operation.value, deleted: operation.deleted,
	}
	return nil
}

func (transaction *Transaction) Commit(ctx context.Context) (uint64, error) {
	if transaction == nil {
		return 0, fmt.Errorf("kitdb: relational transaction is closed")
	}
	if ctx == nil {
		return 0, fmt.Errorf("kitdb: commit context is nil")
	}
	transaction.mu.Lock()
	if transaction.done {
		transaction.mu.Unlock()
		return 0, fmt.Errorf("kitdb: relational transaction is closed")
	}
	operations := append([]transactionOperation(nil), transaction.operations...)
	engine := transaction.engine
	base := transaction.base
	catalogRevision := transaction.catalog.Revision
	transaction.done = true
	transaction.mu.Unlock()
	defer transaction.release()

	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(operations) == 0 {
		return base, nil
	}
	engine.writeMu.Lock()
	defer engine.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	version, err := engine.database.CatalogVersion()
	if err != nil {
		return 0, err
	}
	if version.Revision != catalogRevision {
		return 0, fmt.Errorf("%w: catalog changed after transaction %d", ErrTransactionConflict, base)
	}
	kernel, err := engine.database.Begin()
	if err != nil {
		return 0, err
	}
	for _, operation := range operations {
		if err := ctx.Err(); err != nil {
			_ = kernel.Rollback()
			return 0, err
		}
		if operation.deleted {
			err = kernel.Delete(operation.key)
		} else {
			err = kernel.Put(operation.key, operation.value)
		}
		if err != nil {
			_ = kernel.Rollback()
			return 0, err
		}
	}
	if err := ctx.Err(); err != nil {
		_ = kernel.Rollback()
		return 0, err
	}
	committed, err := kernel.CommitIfUnchanged(base)
	if errors.Is(err, kitdbengine.ErrTransactionConflict) {
		return committed, errors.Join(ErrTransactionConflict, err)
	}
	return committed, err
}

func (transaction *Transaction) Rollback() error {
	if transaction == nil {
		return nil
	}
	transaction.mu.Lock()
	if transaction.done {
		transaction.mu.Unlock()
		return nil
	}
	transaction.done = true
	transaction.mu.Unlock()
	transaction.release()
	return nil
}

func (transaction *Transaction) ready() error {
	if transaction == nil {
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done || transaction.snapshot == nil || transaction.engine == nil {
		return fmt.Errorf("kitdb: relational transaction is closed")
	}
	return nil
}

func (transaction *Transaction) release() {
	transaction.mu.Lock()
	if transaction.released {
		transaction.mu.Unlock()
		return
	}
	transaction.released = true
	snapshot := transaction.snapshot
	engine := transaction.engine
	transaction.snapshot = nil
	transaction.engine = nil
	transaction.operations = nil
	transaction.overlay = nil
	transaction.mu.Unlock()
	if snapshot != nil {
		_ = snapshot.Close()
	}
	if engine != nil {
		engine.mu.RUnlock()
	}
}

func (transaction *Transaction) rebuildOverlayLocked() {
	transaction.overlay = make(map[string]transactionMutation, len(transaction.operations))
	for _, operation := range transaction.operations {
		transaction.overlay[string(operation.key)] = transactionMutation{
			value: operation.value, deleted: operation.deleted,
		}
	}
}

func (transaction *Transaction) schema(requested string) (kitdbsql.Schema, error) {
	if err := transaction.ready(); err != nil {
		return kitdbsql.Schema{}, err
	}
	return schemaFromCatalog(transaction.catalog, requested)
}

func recordKeyInRange(key []byte, options kitdbengine.RangeOptions) bool {
	if len(options.Start) != 0 && bytes.Compare(key, options.Start) < 0 {
		return false
	}
	if len(options.End) != 0 && bytes.Compare(key, options.End) >= 0 {
		return false
	}
	return len(options.Prefix) == 0 || bytes.HasPrefix(key, options.Prefix)
}
