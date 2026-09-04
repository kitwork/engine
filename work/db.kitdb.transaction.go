package work

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"

	kitdbengine "github.com/kitwork/engine/kitdb"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

const (
	kitDBRecordTransactionOperationLimit = 100_000
	kitDBRecordTransactionByteLimit      = 32 << 20
)

type kitDBRecordMutation struct {
	value   []byte
	deleted bool
}

type kitDBRecordOperation struct {
	key      []byte
	value    []byte
	deleted  bool
	byteSize int
}

type kitDBRecordSavepoint struct {
	operations int
	bytes      int
}

// kitDBRecordTransaction is the one relational transaction mechanism used by
// the ORM and remote SQL profile. It reads one immutable kernel snapshot and
// overlays its own writes. Commit is optimistic: the per-file writer gate
// verifies that the source transaction has not advanced before publishing one
// ordinary kernel transaction.
type kitDBRecordTransaction struct {
	mu            sync.Mutex
	managed       *managedKitDB
	snapshot      *kitdbengine.Snapshot
	live          *kitdbengine.DB
	base          uint64
	catalogRev    string
	operations    []kitDBRecordOperation
	overlay       map[string]kitDBRecordMutation
	bytes         int
	wakeMigration bool
	wakeIndex     bool
	writeLocked   bool
	done          bool
}

type kitDBSnapshotReader struct {
	snapshot *kitdbengine.Snapshot
}

type kitDBRecordReader interface {
	kitDBReader
	Scan(kitdbengine.RangeOptions, func(key, encoded []byte) (bool, error)) error
}

func beginKitDBRecordTransaction(
	tenant *Tenant,
	scope *requestscope.Scope,
	databaseName string,
	lockWriter bool,
) (*kitDBRecordTransaction, error) {
	return beginKitDBRecordTransactionWithCheckpoint(
		tenant, scope, databaseName, lockWriter, false, checkpointKitDBBeforeWrite,
	)
}

func beginKitDBBulkRecordTransaction(
	tenant *Tenant,
	scope *requestscope.Scope,
	databaseName string,
	lockWriter bool,
) (*kitDBRecordTransaction, error) {
	return beginKitDBRecordTransactionWithCheckpoint(
		tenant, scope, databaseName, true, true, checkpointKitDBBeforeBulkWrite,
	)
}

func beginKitDBRecordTransactionWithCheckpoint(
	tenant *Tenant,
	scope *requestscope.Scope,
	databaseName string,
	lockWriter bool,
	liveReader bool,
	checkpoint func(*managedKitDB) error,
) (*kitDBRecordTransaction, error) {
	managed, err := kitDBForRequest(tenant, databaseName, scope).database()
	if err != nil {
		return nil, err
	}

	if checkpoint == nil {
		managed.Release()
		return nil, fmt.Errorf("kitdb: checkpoint policy is unavailable")
	}
	if err := checkpoint(managed); err != nil {
		managed.Release()
		return nil, err
	}
	managed.writeMu.Lock()
	version, err := managed.database.CatalogVersion()
	if err != nil {
		managed.writeMu.Unlock()
		managed.Release()
		return nil, err
	}
	var snapshot *kitdbengine.Snapshot
	base := version.Transaction
	var live *kitdbengine.DB
	if liveReader {
		base, err = managed.database.LastTransaction()
		live = managed.database
	} else {
		snapshot, err = managed.database.Snapshot()
		if err == nil {
			base = snapshot.Transaction()
		}
	}
	if err != nil {
		managed.writeMu.Unlock()
		managed.Release()
		return nil, err
	}
	if !lockWriter {
		managed.writeMu.Unlock()
	}
	return &kitDBRecordTransaction{
		managed: managed, snapshot: snapshot, live: live, base: base, catalogRev: version.Revision,
		overlay: make(map[string]kitDBRecordMutation), writeLocked: lockWriter,
	}, nil
}

func (table *SchemaTable) kitDBWriteTransaction() (*kitDBRecordTransaction, bool, error) {
	if table.transaction != nil {
		return table.transaction, false, nil
	}
	transaction, err := beginKitDBRecordTransaction(table.tenant, table.scope, table.dbName, true)
	if err != nil {
		return nil, false, err
	}
	if state, found, stateErr := loadKitDBRowMigrationState(transaction, table.definition); stateErr != nil {
		_ = transaction.Rollback()
		return nil, false, stateErr
	} else if found && state.PrimaryKeyChange &&
		(state.Phase == kitDBRowMigrationPhaseRows || state.Phase == kitDBRowMigrationPhaseVerify) {
		_ = transaction.Rollback()
		return nil, false, fmt.Errorf(
			"kitdb: writes to struct %q are paused while its primary key is rebuilt; reads remain available and PRAGMA migration_status(%q) reports durable progress",
			table.definition.Name, table.definition.Name,
		)
	}
	if transaction.base != table.readyTransaction {
		if err := validateKitDBWriteDefinition(transaction.managed.database, table.definition); err != nil {
			_ = transaction.Rollback()
			return nil, false, err
		}
		inactive, err := loadKitDBInactiveIndexes(transaction, table.definition)
		if err != nil {
			_ = transaction.Rollback()
			return nil, false, err
		}
		generations, writes, epoch, err := loadKitDBIndexLayout(transaction, table.definition)
		if err != nil {
			_ = transaction.Rollback()
			return nil, false, err
		}
		table.inactiveIndexes = inactive
		table.indexGenerations = generations
		table.writeIndexes = writes
		table.indexEpoch = epoch
		generation, rowWrites, rowEpoch, err := loadKitDBRowLayout(transaction, table.definition)
		if err != nil {
			_ = transaction.Rollback()
			return nil, false, err
		}
		table.rowGeneration = generation
		table.writeRows = rowWrites
		table.rowEpoch = rowEpoch
		table.readyTransaction = transaction.base
	}
	if len(table.inactiveIndexes) != 0 {
		transaction.wakeSecondaryIndex()
	}
	return transaction, true, nil
}

func (transaction *kitDBRecordTransaction) Savepoint() kitDBRecordSavepoint {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	return kitDBRecordSavepoint{operations: len(transaction.operations), bytes: transaction.bytes}
}

func (transaction *kitDBRecordTransaction) RollbackTo(savepoint kitDBRecordSavepoint) {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done || savepoint.operations < 0 || savepoint.operations > len(transaction.operations) {
		return
	}
	transaction.operations = transaction.operations[:savepoint.operations]
	transaction.bytes = savepoint.bytes
	transaction.rebuildOverlayLocked()
}

func (transaction *kitDBRecordTransaction) Put(key, encoded []byte) error {
	return transaction.add(kitDBRecordOperation{
		key: key, value: encoded, byteSize: len(key) + len(encoded),
	})
}

func (transaction *kitDBRecordTransaction) Delete(key []byte) error {
	return transaction.add(kitDBRecordOperation{
		key: key, deleted: true, byteSize: len(key),
	})
}

func (transaction *kitDBRecordTransaction) hasPendingMutation(key []byte) bool {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done {
		return false
	}
	_, found := transaction.overlay[string(key)]
	return found
}

func (transaction *kitDBRecordTransaction) add(operation kitDBRecordOperation) error {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done {
		return fmt.Errorf("kitdb: record transaction is closed")
	}
	if len(operation.key) == 0 {
		return kitdbengine.ErrEmptyKey
	}
	if len(transaction.operations) >= kitDBRecordTransactionOperationLimit ||
		operation.byteSize > kitDBRecordTransactionByteLimit-transaction.bytes {
		return fmt.Errorf(
			"kitdb: record transaction exceeds %d operations or %d bytes",
			kitDBRecordTransactionOperationLimit,
			kitDBRecordTransactionByteLimit,
		)
	}
	operation.key = bytes.Clone(operation.key)
	operation.value = bytes.Clone(operation.value)
	transaction.operations = append(transaction.operations, operation)
	transaction.bytes += operation.byteSize
	transaction.overlay[string(operation.key)] = kitDBRecordMutation{
		value: operation.value, deleted: operation.deleted,
	}
	return nil
}

func (transaction *kitDBRecordTransaction) wakeRowMigration() {
	transaction.mu.Lock()
	if !transaction.done {
		transaction.wakeMigration = true
	}
	transaction.mu.Unlock()
}

func (transaction *kitDBRecordTransaction) wakeSecondaryIndex() {
	transaction.mu.Lock()
	if !transaction.done {
		transaction.wakeIndex = true
	}
	transaction.mu.Unlock()
}

func (transaction *kitDBRecordTransaction) Get(key []byte) ([]byte, bool, error) {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done || transaction.snapshot == nil && transaction.live == nil {
		return nil, false, fmt.Errorf("kitdb: record transaction is closed")
	}
	if mutation, found := transaction.overlay[string(key)]; found {
		if mutation.deleted {
			return nil, false, nil
		}
		return bytes.Clone(mutation.value), true, nil
	}
	if transaction.snapshot != nil {
		return transaction.snapshot.Get(key)
	}
	return transaction.live.Get(key)
}

func (transaction *kitDBRecordTransaction) Scan(
	options kitdbengine.RangeOptions,
	visit func(key, encoded []byte) (bool, error),
) error {
	transaction.mu.Lock()
	if transaction.done || transaction.snapshot == nil {
		bulk := transaction.live != nil
		transaction.mu.Unlock()
		if bulk {
			return fmt.Errorf("kitdb: bulk record transaction does not support range scans")
		}
		return fmt.Errorf("kitdb: record transaction is closed")
	}
	snapshot := transaction.snapshot
	overlay := make(map[string]kitDBRecordMutation, len(transaction.overlay))
	for key, mutation := range transaction.overlay {
		overlay[key] = mutation
	}
	transaction.mu.Unlock()

	overlayKeys := make([]string, 0, len(overlay))
	for key := range overlay {
		if kitDBRecordKeyInRange([]byte(key), options) {
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

	snapshotReady := cursor.Next()
	overlayIndex := 0
	emitted := 0
	for snapshotReady || overlayIndex < len(overlayKeys) {
		if options.Limit > 0 && emitted >= options.Limit {
			break
		}

		var key, encoded []byte
		useSnapshot := false
		useOverlay := false
		switch {
		case !snapshotReady:
			useOverlay = true
		case overlayIndex >= len(overlayKeys):
			useSnapshot = true
		default:
			comparison := bytes.Compare(cursor.Key(), []byte(overlayKeys[overlayIndex]))
			if options.Reverse {
				useSnapshot = comparison >= 0
				useOverlay = comparison <= 0
			} else {
				useSnapshot = comparison <= 0
				useOverlay = comparison >= 0
			}
		}

		if useOverlay {
			overlayKey := overlayKeys[overlayIndex]
			mutation := overlay[overlayKey]
			overlayIndex++
			if !mutation.deleted {
				key = []byte(overlayKey)
				encoded = mutation.value
			}
		}
		if useSnapshot {
			if !useOverlay {
				key = cursor.Key()
				encoded = cursor.Value()
			}
			snapshotReady = cursor.Next()
		}
		if len(key) == 0 {
			continue
		}
		emitted++
		stop, err := visit(bytes.Clone(key), bytes.Clone(encoded))
		if err != nil || stop {
			return err
		}
	}
	return cursor.Err()
}

func (transaction *kitDBRecordTransaction) Commit() (uint64, error) {
	return transaction.CommitContext(nil)
}

// CommitContext adds a caller-owned cancellation boundary to the transaction's
// lifetime context. Once kernel publication starts, durability still completes
// atomically; cancellation is checked before that irreversible boundary.
func (transaction *kitDBRecordTransaction) CommitContext(ctx context.Context) (uint64, error) {
	transaction.mu.Lock()
	if transaction.done {
		transaction.mu.Unlock()
		return 0, fmt.Errorf("kitdb: record transaction is closed")
	}
	operations := append([]kitDBRecordOperation(nil), transaction.operations...)
	managed := transaction.managed
	base := transaction.base
	catalogRevision := transaction.catalogRev
	wakeMigration := transaction.wakeMigration
	wakeIndex := transaction.wakeIndex
	transaction.done = true
	transaction.mu.Unlock()
	defer transaction.release()

	if managed == nil || managed.database == nil {
		return 0, fmt.Errorf("kitdb: managed record transaction is unavailable")
	}
	if err := contextErrorEither(managed.context, ctx); err != nil {
		return 0, err
	}
	if !transaction.writeLocked {
		if len(operations) != 0 {
			if err := checkpointKitDBBeforeWrite(managed); err != nil {
				return 0, err
			}
		}
		managed.writeMu.Lock()
		defer managed.writeMu.Unlock()
	}
	if err := contextErrorEither(managed.context, ctx); err != nil {
		return 0, err
	}
	version, err := managed.database.CatalogVersion()
	if err != nil {
		return 0, err
	}
	if version.Revision != catalogRevision {
		return 0, &kitDBTransactionConflictError{base: base, current: version.Transaction}
	}
	if len(operations) == 0 {
		return base, nil
	}
	current, err := managed.database.LastTransaction()
	if err != nil {
		return 0, err
	}
	if current != base {
		return 0, &kitDBTransactionConflictError{base: base, current: current}
	}
	kernel, err := managed.database.Begin()
	if err != nil {
		return 0, err
	}
	for _, operation := range operations {
		if err := contextErrorEither(managed.context, ctx); err != nil {
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
	if err := contextErrorEither(managed.context, ctx); err != nil {
		_ = kernel.Rollback()
		return 0, err
	}
	committed, err := kernel.Commit()
	if err == nil && wakeMigration {
		_, _ = scheduleKitDBRowMigration(managed)
	}
	if err == nil && wakeIndex {
		_, _ = scheduleKitDBSecondaryIndex(managed)
	}
	return committed, err
}

func (transaction *kitDBRecordTransaction) Rollback() error {
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

func (transaction *kitDBRecordTransaction) release() {
	transaction.mu.Lock()
	snapshot := transaction.snapshot
	managed := transaction.managed
	transaction.snapshot = nil
	transaction.live = nil
	transaction.managed = nil
	transaction.operations = nil
	transaction.overlay = nil
	transaction.mu.Unlock()
	if snapshot != nil {
		_ = snapshot.Close()
	}
	if managed != nil {
		if transaction.writeLocked {
			managed.writeMu.Unlock()
		}
		managed.Release()
	}
}

func (transaction *kitDBRecordTransaction) rebuildOverlayLocked() {
	transaction.overlay = make(map[string]kitDBRecordMutation, len(transaction.operations))
	for _, operation := range transaction.operations {
		transaction.overlay[string(operation.key)] = kitDBRecordMutation{
			value: operation.value, deleted: operation.deleted,
		}
	}
}

func (reader kitDBSnapshotReader) Get(key []byte) ([]byte, bool, error) {
	return reader.snapshot.Get(key)
}

func (reader kitDBSnapshotReader) Scan(
	options kitdbengine.RangeOptions,
	visit func(key, encoded []byte) (bool, error),
) error {
	cursor, err := reader.snapshot.Cursor(options)
	if err != nil {
		return err
	}
	defer cursor.Close()
	for cursor.Next() {
		stop, err := visit(cursor.Key(), cursor.Value())
		if err != nil || stop {
			return err
		}
	}
	return cursor.Err()
}

func kitDBRecordKeyInRange(key []byte, options kitdbengine.RangeOptions) bool {
	if len(options.Start) != 0 && bytes.Compare(key, options.Start) < 0 {
		return false
	}
	if len(options.End) != 0 && bytes.Compare(key, options.End) >= 0 {
		return false
	}
	return len(options.Prefix) == 0 || bytes.HasPrefix(key, options.Prefix)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func contextErrorEither(first, second context.Context) error {
	if err := contextError(first); err != nil {
		return err
	}
	return contextError(second)
}

func (proxy *dbProxy) prepareKitDBRecordTransaction() error {
	return proxy.prepareKitDBRecordTransactionWithPolicy(false)
}

func (proxy *dbProxy) prepareKitDBRecordTransactionWithPolicy(bulk bool) error {
	if proxy == nil || proxy.engine != "kitdb" {
		return fmt.Errorf("db.transaction is available only for KitDB")
	}
	if proxy.transaction != nil {
		return fmt.Errorf("kitdb: nested transactions are not supported")
	}
	if err := proxy.refreshKitDBCatalogDefinitions(); err != nil {
		return err
	}
	tables, definitions := proxy.schemaSnapshot()
	if len(definitions) == 0 {
		return nil
	}
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	definition := definitions[names[0]]
	table := &SchemaTable{
		tenant: proxy.tenant, scope: proxy.scope, engine: "kitdb", dbName: proxy.dbName,
		migrate: proxy.migrate, table: definition.Name, columns: definition.columns,
		siblings: tables, definition: definition, definitions: definitions,
	}
	if bulk {
		return table.ensureKitDBStructWithCheckpoint(checkpointKitDBBeforeBulkWrite)
	}
	return table.ensureKitDBStruct()
}

func (proxy *dbProxy) beginKitDBRecordTransaction(scope *requestscope.Scope) (*dbProxy, *kitDBRecordTransaction, error) {
	return proxy.beginKitDBRecordTransactionWithPolicy(scope, false)
}

func (proxy *dbProxy) beginKitDBBulkRecordTransaction(scope *requestscope.Scope) (*dbProxy, *kitDBRecordTransaction, error) {
	return proxy.beginKitDBRecordTransactionWithPolicy(scope, true)
}

func (proxy *dbProxy) beginKitDBRecordTransactionWithPolicy(
	scope *requestscope.Scope,
	bulk bool,
) (*dbProxy, *kitDBRecordTransaction, error) {
	if err := proxy.prepareKitDBRecordTransactionWithPolicy(bulk); err != nil {
		return nil, nil, err
	}
	if scope == nil {
		scope = proxy.scope
	}
	for attempt := 0; attempt < 3; attempt++ {
		var transaction *kitDBRecordTransaction
		var err error
		if bulk {
			transaction, err = beginKitDBBulkRecordTransaction(proxy.tenant, scope, proxy.dbName, false)
		} else {
			transaction, err = beginKitDBRecordTransaction(proxy.tenant, scope, proxy.dbName, false)
		}
		if err != nil {
			return nil, nil, err
		}
		tables, definitions, catalogTx, catalogRevision := proxy.schemaSnapshotWithCatalog()
		if catalogRevision != transaction.catalogRev {
			_ = transaction.Rollback()
			if err := proxy.refreshKitDBCatalogDefinitions(); err != nil {
				return nil, nil, err
			}
			continue
		}
		names := make([]string, 0, len(definitions))
		for name := range definitions {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := validateKitDBWriteDefinition(transaction.managed.database, definitions[name]); err != nil {
				_ = transaction.Rollback()
				return nil, nil, err
			}
		}
		transactionProxy := &dbProxy{
			tenant: proxy.tenant, scope: scope, engine: "kitdb", dbName: proxy.dbName,
			migrate: proxy.migrate, tables: tables, structs: definitions,
			declaredTables: append([]string(nil), proxy.declaredTables...), transaction: transaction,
			catalogTx: catalogTx, catalogRev: catalogRevision,
		}
		transactionProxy.catalogOnce.Do(func() {})
		return transactionProxy, transaction, nil
	}
	return nil, nil, fmt.Errorf("kitdb: catalog changed repeatedly while beginning transaction; retry")
}

func (proxy *dbProxy) kitDBTransaction(args ...value.Value) value.Value {
	if proxy == nil || proxy.engine != "kitdb" {
		return value.Value{K: value.Invalid, V: "db.transaction is available only for KitDB"}
	}
	if len(args) != 1 || !args[0].IsCallable() {
		return value.Value{K: value.Invalid, V: "db.transaction expects one callback"}
	}
	lambda, ok := args[0].V.(*value.Lambda)
	if !ok {
		return value.Value{K: value.Invalid, V: "db.transaction callback must be a Kitwork function"}
	}
	transactionProxy, transaction, err := proxy.beginKitDBRecordTransaction(proxy.scope)
	if err != nil {
		return kitDBError(err)
	}
	result := (tenantLambdaExecutor{tenant: proxy.tenant, requestScope: proxy.scope}).ExecuteLambda(
		lambda,
		[]value.Value{{K: value.Proxy, V: transactionProxy}},
	)
	if result.K == value.Invalid {
		_ = transaction.Rollback()
		return result
	}
	if _, err := transaction.Commit(); err != nil {
		return kitDBError(err)
	}
	return result
}
