// Package relational provides KitDB's standalone catalog, record and SQL
// execution profile. It depends on the KitDB kernel, never on Kitwork Tenant,
// VM, work, routing or application packages.
package relational

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/search"
)

const (
	DefaultMaximumResultRows   = 10_000
	MaximumResultRows          = 100_000
	DefaultMaximumMutationRows = 10_000
	MaximumMutationRows        = 10_000
)

func standaloneStatementReadOnly(kind kitdbsql.StatementKind) bool {
	return kind == kitdbsql.StatementSelect || kind == kitdbsql.StatementExplain ||
		kind == kitdbsql.StatementPragma
}

type Options struct {
	// ExperimentalProjections enables read-only .analytics and .search snapshots.
	// RefreshProjections is explicit; this is not an incremental index mode.
	ExperimentalProjections bool
	BatchAggregates         bool
	Kernel                  kitdbengine.OpenOptions
	MaximumResultRows       int
	MaximumMutationRows     int
	SearchRoot              string
	SearchNamespace         string
	SearchManager           search.ManagerOptions
	MaximumSearchResults    int
	MaximumSearchCandidates int
	SearchForegroundWait    time.Duration
	// SearchReaderCacheBytes reserves process memory for immutable packed
	// search readers. Zero disables reader residency; queries still work by
	// opening and closing a bounded reader per execution.
	SearchReaderCacheBytes int64
	// ProjectionOpenPolicy optionally rejects invalid or non-ready immutable
	// projection state before this Engine becomes visible to callers.
	ProjectionOpenPolicy ProjectionOpenPolicy
}

type Engine struct {
	projectionMu            sync.RWMutex
	projectionCache         projectionReaderCache
	projectionBuilds        chan struct{}
	projectionQueries       chan struct{}
	experimentalProjections bool
	batchAggregates         bool
	mu                      sync.RWMutex
	writeMu                 sync.Mutex
	database                *kitdbengine.DB
	closeDatabase           func() error
	maximumResultRows       int
	maximumMutationRows     int
	searchRoot              string
	searchNamespace         string
	searchManagerOptions    search.ManagerOptions
	maximumSearchResults    int
	maximumSearchCandidates int
	searchForegroundWait    time.Duration
	searchReaderCacheBytes  int64
	searchContext           context.Context
	searchCancel            context.CancelFunc
	searchMu                sync.Mutex
	searchManager           *search.Manager
	searchStates            map[string]*relationalSearchState
	closed                  bool
}

type Column struct {
	Name          string
	Kind          string
	Precision     int
	Scale         int
	TimePrecision *int
	TextLength    *int
	ExactUUID     bool
}

func columnForField(name string, field kitdbsql.Field) Column {
	return Column{
		Name: name, Kind: field.Kind, Precision: field.Precision, Scale: field.Scale,
		TimePrecision: field.TimePrecision, TextLength: field.TextLength, ExactUUID: field.ExactUUID,
	}
}

type Result struct {
	Execution  *ExecutionStats
	Columns    []Column
	Rows       [][]any
	Affected   int64
	CommandTag string

	materializationWorkingAccounted bool
}

type Table struct {
	Name     string
	ID       string
	Hash     string
	Fields   int
	Rows     uint64
	Analyzed bool
}

func Open(path string) (*Engine, error) {
	return OpenWithContext(context.Background(), path, Options{})
}

func OpenWithOptions(path string, options Options) (*Engine, error) {
	return OpenWithContext(context.Background(), path, options)
}

// OpenWithContext opens a standalone relational Engine and applies any
// projection admission policy before publishing it to the caller. The kernel
// remains the sole canonical durability owner.
func OpenWithContext(ctx context.Context, path string, options Options) (*Engine, error) {
	if ctx == nil {
		return nil, fmt.Errorf("kitdb: open context is nil")
	}
	maximum, maximumMutations, err := normalizeRelationalBounds(options)
	if err != nil {
		return nil, err
	}
	database, err := kitdbengine.OpenWithOptions(path, options.Kernel)
	if err != nil {
		return nil, err
	}
	engine, err := newEngineWithDatabase(
		ctx, database, options, maximum, maximumMutations, database.Close,
	)
	if err != nil {
		return nil, errors.Join(err, database.Close())
	}
	return engine, nil
}

// Attach creates a relational facade over an already-open KitDB kernel handle.
// The caller retains ownership of database and must close the facade before
// closing the kernel handle. This is the direct embedded path used by hosts
// that already own file lifecycle, leases and resource accounting.
func Attach(database *kitdbengine.DB, options Options) (*Engine, error) {
	return AttachWithContext(context.Background(), database, options)
}

// AttachWithContext creates a relational facade over a caller-owned kernel and
// applies projection admission without taking ownership of the kernel handle.
func AttachWithContext(ctx context.Context, database *kitdbengine.DB, options Options) (*Engine, error) {
	if ctx == nil {
		return nil, fmt.Errorf("kitdb: attach context is nil")
	}
	maximum, maximumMutations, err := normalizeRelationalBounds(options)
	if err != nil {
		return nil, err
	}
	return newEngineWithDatabase(
		ctx,
		database,
		options,
		maximum,
		maximumMutations,
		func() error { return nil },
	)
}

func normalizeRelationalBounds(options Options) (int, int, error) {
	policy, err := normalizeProjectionOpenPolicy(options.ProjectionOpenPolicy)
	if err != nil {
		return 0, 0, err
	}
	if policy != ProjectionOpenLazy && !options.ExperimentalProjections {
		return 0, 0, fmt.Errorf("kitdb: projection open policy requires experimental projections")
	}
	if options.SearchReaderCacheBytes < 0 || options.SearchReaderCacheBytes > MaximumSearchReaderCacheBytes {
		return 0, 0, fmt.Errorf(
			"kitdb: search reader cache bytes must be between 0 and %d", MaximumSearchReaderCacheBytes,
		)
	}
	if options.SearchReaderCacheBytes != 0 && !options.ExperimentalProjections {
		return 0, 0, fmt.Errorf("kitdb: search reader cache requires experimental projections")
	}
	if options.ExperimentalProjections && options.SearchRoot != "" {
		return 0, 0, fmt.Errorf("kitdb: experimental file projections cannot use a legacy search-root directory")
	}
	maximum := options.MaximumResultRows
	if maximum == 0 {
		maximum = DefaultMaximumResultRows
	}
	if maximum < 1 || maximum > MaximumResultRows {
		return 0, 0, fmt.Errorf("kitdb: maximum result rows must be between 1 and %d", MaximumResultRows)
	}
	maximumMutations := options.MaximumMutationRows
	if maximumMutations == 0 {
		maximumMutations = DefaultMaximumMutationRows
	}
	if maximumMutations < 1 || maximumMutations > MaximumMutationRows {
		return 0, 0, fmt.Errorf("kitdb: maximum mutation rows must be between 1 and %d", MaximumMutationRows)
	}
	return maximum, maximumMutations, nil
}

// newEngineWithDatabase binds the relational layer to an already-owned kernel
// handle. closeDatabase transfers the caller's ownership into Engine.Close.
func newEngineWithDatabase(
	ctx context.Context,
	database *kitdbengine.DB,
	options Options,
	maximum int,
	maximumMutations int,
	closeDatabase func() error,
) (*Engine, error) {
	if ctx == nil {
		return nil, fmt.Errorf("kitdb: managed database context is nil")
	}
	if database == nil {
		return nil, fmt.Errorf("kitdb: managed database is nil")
	}
	if closeDatabase == nil {
		return nil, fmt.Errorf("kitdb: managed database release is nil")
	}
	searchConfiguration, err := normalizeRelationalSearchOptions(
		database.Path(), database.ID(), maximum, options,
	)
	if err != nil {
		return nil, err
	}
	searchContext, searchCancel := context.WithCancel(context.Background())
	engine := &Engine{
		projectionQueries:       make(chan struct{}, 2),
		projectionBuilds:        make(chan struct{}, 1),
		experimentalProjections: options.ExperimentalProjections,
		batchAggregates:         options.BatchAggregates || options.ExperimentalProjections,
		database:                database, closeDatabase: closeDatabase, maximumResultRows: maximum,
		maximumMutationRows: maximumMutations,
		searchRoot:          searchConfiguration.root, searchNamespace: searchConfiguration.namespace,
		searchManagerOptions:    searchConfiguration.manager,
		maximumSearchResults:    searchConfiguration.maximumResults,
		maximumSearchCandidates: searchConfiguration.maximumCandidates,
		searchForegroundWait:    searchConfiguration.foregroundWait,
		searchReaderCacheBytes:  options.SearchReaderCacheBytes,
		searchContext:           searchContext, searchCancel: searchCancel,
		searchStates: make(map[string]*relationalSearchState),
	}
	if err := engine.enforceProjectionOpenPolicy(ctx, options.ProjectionOpenPolicy); err != nil {
		searchCancel()
		return nil, fmt.Errorf("kitdb: projection preflight: %w", err)
	}
	return engine, nil
}

func (engine *Engine) Path() string {
	if engine == nil {
		return ""
	}
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if engine.database == nil {
		return ""
	}
	return engine.database.Path()
}

// Describe returns the stable result columns of one SELECT without reading
// rows. PostgreSQL prepared-statement discovery and embedded clients can use it
// without turning metadata inspection into query execution.
func (engine *Engine) Describe(ctx context.Context, source string) ([]Column, error) {
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
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return nil, err
	}
	if statement.Kind == kitdbsql.StatementPragma {
		return describePragma(statement.Pragma)
	}
	if columns, handled, err := describeSequenceSelect(statement.Select); handled {
		return columns, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return nil, err
	}
	if err := resolveStatementFunctions(&statement, catalog); err != nil {
		return nil, err
	}
	switch statement.Kind {
	case kitdbsql.StatementSelect:
		return describeSelectFromCatalog(catalog, statement.Select, nil)
	case kitdbsql.StatementExplain:
		return explainColumns(), nil
	case kitdbsql.StatementInsert:
		return engine.describeReturningLocked(statement.Insert.Table, statement.Insert.Returning)
	case kitdbsql.StatementUpdate:
		return engine.describeReturningLocked(statement.Update.Table, statement.Update.Returning)
	case kitdbsql.StatementDelete:
		return engine.describeReturningLocked(statement.Delete.Table, statement.Delete.Returning)
	default:
		return nil, fmt.Errorf("kitdb SQL: statement has no row description")
	}
}

func (engine *Engine) describeSelectLocked(plan *kitdbsql.SelectStatement) ([]Column, error) {
	catalog, err := engine.database.Catalog()
	if err != nil {
		return nil, err
	}
	return describeSelectFromCatalog(catalog, plan, nil)
}

func (engine *Engine) describeReturningLocked(
	table string,
	returning []kitdbsql.Projection,
) ([]Column, error) {
	if len(returning) == 0 {
		return nil, nil
	}
	schema, err := engine.schemaLocked(table)
	if err != nil {
		return nil, err
	}
	columns, _, err := bindProjection(schema, returning)
	return columns, err
}

func (engine *Engine) Close() error {
	if engine == nil {
		return nil
	}
	if engine.searchCancel != nil {
		engine.searchCancel()
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil
	}
	engine.closed = true
	engine.searchMu.Lock()
	manager := engine.searchManager
	engine.searchManager = nil
	engine.searchMu.Unlock()
	var managerErr error
	if manager != nil {
		managerErr = manager.Close()
	}
	engine.projectionMu.Lock()
	projectionErr := engine.projectionCache.close()
	engine.projectionMu.Unlock()
	if engine.database == nil {
		return errors.Join(managerErr, projectionErr)
	}
	closeDatabase := engine.closeDatabase
	engine.closeDatabase = nil
	engine.database = nil
	if closeDatabase == nil {
		return errors.Join(managerErr, projectionErr)
	}
	return errors.Join(managerErr, projectionErr, closeDatabase())
}

func (engine *Engine) Checkpoint() (uint64, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return 0, err
	}
	return engine.database.Checkpoint()
}

func (engine *Engine) Tables() ([]Table, error) {
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return nil, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return nil, err
	}
	tables := make([]Table, 0, len(catalog.Structs))
	for _, entry := range catalog.Structs {
		schema, err := decodeCatalogSchema(entry.Definition)
		if err != nil {
			return nil, err
		}
		count, analyzed, err := readTableCount(engine.database, schema)
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

// Execute parses, binds and runs one statement against the same catalog and
// row encoding consumed by Kitwork. The initial standalone profile is narrow
// by design; unsupported syntax fails before touching storage.
func (engine *Engine) Execute(ctx context.Context, source string, parameters ...any) (Result, error) {
	return engine.executeWithSequences(ctx, source, parameters, nil, false)
}

// ExecutePlan binds and runs a typed statement without rendering or parsing
// SQL text. A frontend must provide a fresh plan for each concurrent call;
// binding may attach snapshot-local function metadata to expression nodes.
func (engine *Engine) ExecutePlan(
	ctx context.Context,
	plan *kitdbsql.ParsedStatement,
	parameters ...any,
) (Result, error) {
	return engine.executePlanWithSequences(ctx, plan, parameters, nil, false)
}

func (engine *Engine) executeWithSequences(ctx context.Context, source string, parameters []any, sequences *sequenceSession, readOnly bool) (Result, error) {
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
	return engine.executePreparedPlanWithSequences(ctx, &statement, parameters, sequences, readOnly)
}

func (engine *Engine) executePlanWithSequences(
	ctx context.Context,
	plan *kitdbsql.ParsedStatement,
	parameters []any,
	sequences *sequenceSession,
	readOnly bool,
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
	return engine.executePreparedPlanWithSequences(ctx, plan, parameters, sequences, readOnly)
}

func (engine *Engine) executePreparedPlanWithSequences(
	ctx context.Context,
	plan *kitdbsql.ParsedStatement,
	parameters []any,
	sequences *sequenceSession,
	readOnly bool,
) (Result, error) {
	statement := *plan
	if readOnly && !standaloneStatementReadOnly(statement.Kind) {
		return Result{}, fmt.Errorf("kitdb: database is read-only")
	}
	if _, handled, err := describeSequenceSelect(statement.Select); handled {
		if err != nil {
			return Result{}, err
		}
		engine.mu.RLock()
		defer engine.mu.RUnlock()
		if err := engine.readyLocked(); err != nil {
			return Result{}, err
		}
		if sequences == nil {
			sequences = &sequenceSession{}
		}
		return sequences.execute(ctx, engine.database, statement.Select, parameters, readOnly)
	}
	if statement.CreateSequence != nil || statement.DropSequence != nil || statement.AlterSequence != nil {
		return engine.executeSequenceDDL(ctx, statement)
	}
	switch statement.Kind {
	case kitdbsql.StatementCreate:
		if statement.CreateFunction != nil {
			return engine.executeFunctionDDL(ctx, statement.CreateFunction, nil)
		}
		if statement.CreateTable != nil {
			return engine.executeCreateTable(ctx, statement.CreateTable)
		}
		return engine.executeCreateIndex(ctx, statement.CreateIndex)
	case kitdbsql.StatementDrop:
		if statement.DropFunction != nil {
			return engine.executeFunctionDDL(ctx, nil, statement.DropFunction)
		}
		if statement.DropTable != nil {
			return engine.executeDropTable(ctx, statement.DropTable)
		}
		return engine.executeDropIndex(ctx, statement.DropIndex)
	case kitdbsql.StatementAnalyze:
		return engine.executeAnalyze(ctx, statement.Analyze)
	case kitdbsql.StatementReindex:
		return engine.executeReindex(ctx, statement.Reindex)
	case kitdbsql.StatementPragma:
		transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
		if err != nil {
			return Result{}, err
		}
		result, executeErr := transaction.executePragma(ctx, statement.Pragma)
		_ = transaction.Rollback()
		return result, executeErr
	case kitdbsql.StatementAlter:
		return engine.executeAlterTable(ctx, statement.AlterTable)
	case kitdbsql.StatementExplain:
		if statement.Explain.Search != nil {
			engine.mu.RLock()
			if err := engine.readyLocked(); err != nil {
				engine.mu.RUnlock()
				return Result{}, err
			}
			catalog, err := engine.database.Catalog()
			engine.mu.RUnlock()
			if err != nil {
				return Result{}, err
			}
			if err := resolveStatementFunctions(&statement, catalog); err != nil {
				return Result{}, err
			}
			if statement.ExplainAnalyze {
				return engine.executeSearchExplainAnalyze(ctx, statement.Explain, parameters)
			}
			return engine.executeSearchExplain(ctx, statement.Explain, parameters)
		}
		transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
		if err != nil {
			return Result{}, err
		}
		result, executeErr := transaction.executeParsed(ctx, statement, parameters)
		_ = transaction.Rollback()
		return result, executeErr
	case kitdbsql.StatementSelect:
		if statement.Select.Search != nil {
			engine.mu.RLock()
			if err := engine.readyLocked(); err != nil {
				engine.mu.RUnlock()
				return Result{}, err
			}
			catalog, err := engine.database.Catalog()
			engine.mu.RUnlock()
			if err != nil {
				return Result{}, err
			}
			if err := resolveStatementFunctions(&statement, catalog); err != nil {
				return Result{}, err
			}
			return engine.executeSearchSelect(ctx, statement.Select, parameters)
		}
		transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
		if err != nil {
			return Result{}, err
		}
		result, executeErr := transaction.executeParsed(ctx, statement, parameters)
		_ = transaction.Rollback()
		return result, executeErr
	case kitdbsql.StatementInsert, kitdbsql.StatementUpdate, kitdbsql.StatementDelete:
		if sequences == nil {
			sequences = &sequenceSession{}
		}
		readOnly := statement.Kind == kitdbsql.StatementSelect
		for attempt := 0; attempt < 3; attempt++ {
			transaction, err := engine.BeginTransaction(ctx, TransactionOptions{ReadOnly: readOnly})
			if err != nil {
				return Result{}, err
			}
			transaction.sequences = sequences
			result, executeErr := transaction.executeParsed(ctx, statement, parameters)
			if executeErr != nil {
				_ = transaction.Rollback()
				return Result{}, executeErr
			}
			if readOnly {
				_ = transaction.Rollback()
				return result, nil
			}
			_, commitErr := transaction.Commit(ctx)
			if commitErr == nil {
				return result, nil
			}
			if !errors.Is(commitErr, ErrTransactionConflict) || attempt == 2 {
				return Result{}, commitErr
			}
		}
		return Result{}, fmt.Errorf("kitdb: autocommit retry exhausted")
	default:
		return Result{}, fmt.Errorf("kitdb SQL: unsupported standalone statement")
	}
}

func (engine *Engine) readyLocked() error {
	if engine == nil || engine.closed || engine.database == nil {
		return fmt.Errorf("kitdb: relational engine is closed")
	}
	return nil
}

func (engine *Engine) executeCreateTable(ctx context.Context, plan *kitdbsql.CreateTableStatement) (Result, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.readyLocked(); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	catalog, err := engine.database.Catalog()
	if err != nil {
		return Result{}, err
	}
	for _, entry := range catalog.Structs {
		if strings.EqualFold(entry.Name, plan.Name) {
			if plan.IfNotExists {
				return Result{CommandTag: "CREATE TABLE"}, nil
			}
			return Result{}, fmt.Errorf("kitdb SQL: table %q already exists as %q", plan.Name, entry.Name)
		}
	}
	transaction, err := engine.database.Begin()
	if err != nil {
		return Result{}, err
	}
	defer transaction.Rollback()
	plan, catalog, err = prepareCreateSequences(transaction, plan, catalog)
	if err != nil {
		return Result{}, err
	}
	schema, encoded, err := schemaFromCreate(plan, catalog)
	if err != nil {
		return Result{}, err
	}
	for _, entry := range catalog.Structs {
		if entry.ID == schema.ID {
			return Result{}, fmt.Errorf("kitdb: schema identity collision between %q and %q", entry.Name, schema.Name)
		}
	}
	if err := transaction.DefineStruct(encoded); err != nil {
		_ = transaction.Rollback()
		return Result{}, err
	}
	if err := markEmptyTableIndexesReady(transaction, schema); err != nil {
		_ = transaction.Rollback()
		return Result{}, err
	}
	if err := markEmptyTableCount(transaction, schema); err != nil {
		_ = transaction.Rollback()
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = transaction.Rollback()
		return Result{}, err
	}
	if _, err := transaction.CommitSequenceChanges(); err != nil {
		return Result{}, err
	}
	return Result{CommandTag: "CREATE TABLE"}, nil
}

func (transaction *Transaction) executeInsert(ctx context.Context, plan *kitdbsql.InsertStatement, parameters []any) (Result, error) {
	if err := transaction.ready(); err != nil {
		return Result{}, err
	}
	schema, err := transaction.schema(plan.Table)
	if err != nil {
		return Result{}, err
	}
	if err := standaloneWriteSupported(schema); err != nil {
		return Result{}, err
	}
	generation, err := activeRowGeneration(transaction, schema)
	if err != nil {
		return Result{}, err
	}
	if generation != 0 {
		return Result{}, fmt.Errorf(
			"kitdb: standalone writes to migrated row generation %d are not enabled yet", generation,
		)
	}
	rows, err := transaction.bindInsertRows(ctx, schema, plan, parameters)
	if err != nil {
		return Result{}, err
	}
	if len(rows) == 0 {
		return Result{}, fmt.Errorf("kitdb SQL: INSERT has no rows")
	}
	staged := make(map[string]struct{}, len(rows)*2)
	for index, row := range rows {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		key, err := rowKey(schema, row, 0)
		if err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d: %w", index+1, err)
		}
		if _, duplicate := staged[string(key)]; duplicate {
			return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d repeats a primary key", index+1)
		}
		if _, found, err := transaction.Get(key); err != nil {
			return Result{}, err
		} else if found {
			return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d violates primary key", index+1)
		}
		staged[string(key)] = struct{}{}
		if err := validateRowChecks(schema, row); err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d: %w", index+1, err)
		}
		unique, err := uniqueEntries(schema, row, key)
		if err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d: %w", index+1, err)
		}
		for _, entry := range unique {
			if _, duplicate := staged[string(entry.key)]; duplicate {
				return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d violates a unique constraint", index+1)
			}
			if _, found, err := transaction.Get(entry.key); err != nil {
				return Result{}, err
			} else if found {
				return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d violates a unique constraint", index+1)
			}
			staged[string(entry.key)] = struct{}{}
		}
		encoded, err := encodeRow(schema, row, nil)
		if err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d: %w", index+1, err)
		}
		if err := transaction.Put(key, encoded); err != nil {
			return Result{}, err
		}
		for _, entry := range unique {
			if err := transaction.Put(entry.key, entry.value); err != nil {
				return Result{}, err
			}
		}
		secondary, err := secondaryIndexEntries(schema, row, key)
		if err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d: %w", index+1, err)
		}
		for _, entry := range secondary {
			if err := transaction.Put(entry.key, entry.value); err != nil {
				return Result{}, err
			}
		}
	}
	for index, row := range rows {
		if err := transaction.validateRowForeignKeys(schema, row); err != nil {
			return Result{}, fmt.Errorf("kitdb SQL: INSERT row %d: %w", index+1, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := adjustTableCount(transaction, schema, int64(len(rows))); err != nil {
		return Result{}, err
	}
	result := Result{Affected: int64(len(rows)), CommandTag: fmt.Sprintf("INSERT 0 %d", len(rows))}
	if len(plan.Returning) != 0 {
		columns, names, err := bindProjection(schema, plan.Returning)
		if err != nil {
			return Result{}, err
		}
		result.Columns = columns
		result.Rows = make([][]any, len(rows))
		for index, row := range rows {
			result.Rows[index] = projectRow(schema, row, names)
		}
	}
	return result, nil
}

func (engine *Engine) schemaLocked(requested string) (kitdbsql.Schema, error) {
	catalog, err := engine.database.Catalog()
	if err != nil {
		return kitdbsql.Schema{}, err
	}
	return schemaFromCatalog(catalog, requested)
}

func schemaFromCatalog(catalog kitdbengine.CatalogSnapshot, requested string) (kitdbsql.Schema, error) {
	var match *kitdbengine.CatalogStruct
	for index := range catalog.Structs {
		entry := &catalog.Structs[index]
		if entry.Name == requested {
			match = entry
			break
		}
		if strings.EqualFold(entry.Name, requested) {
			if match != nil {
				return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: ambiguous table %q", requested)
			}
			match = entry
		}
	}
	if match == nil {
		return kitdbsql.Schema{}, fmt.Errorf("kitdb SQL: no such table: %s", requested)
	}
	return decodeCatalogSchema(match.Definition)
}

func standaloneWriteSupported(schema kitdbsql.Schema) error {
	for _, constraint := range relationalForeignConstraints(schema) {
		for _, action := range []string{
			normalizeReferentialAction(constraint.OnDelete),
			normalizeReferentialAction(constraint.OnUpdate),
		} {
			if action != "no action" && action != "restrict" {
				return fmt.Errorf(
					"kitdb: table %q foreign key %q uses unsupported action %q",
					schema.Name, constraint.Name, action,
				)
			}
		}
	}
	return nil
}

func (transaction *Transaction) bindInsertRows(ctx context.Context, schema kitdbsql.Schema, plan *kitdbsql.InsertStatement, parameters []any) ([]map[string]any, error) {
	if plan == nil {
		return nil, fmt.Errorf("kitdb SQL: invalid INSERT plan")
	}
	columns := append([]string(nil), plan.Columns...)
	if len(columns) == 0 && len(plan.Rows) != 0 {
		if len(plan.Rows[0]) != len(schema.Fields) {
			return nil, fmt.Errorf(
				"kitdb SQL: INSERT row has %d values but table %q has %d fields",
				len(plan.Rows[0]), schema.Name, len(schema.Fields),
			)
		}
		columns = make([]string, len(schema.Fields))
		for index, field := range schema.Fields {
			columns[index] = field.Name
		}
	}
	canonical := make([]string, len(columns))
	fields := make([]kitdbsql.Field, len(columns))
	seen := make(map[string]bool, len(columns))
	for index, requested := range columns {
		name, field, found := schema.FieldByName(unqualifiedColumn(requested))
		if !found {
			return nil, fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, requested)
		}
		canonical[index] = name
		if seen[name] {
			return nil, fmt.Errorf("kitdb SQL: duplicate INSERT field %q", name)
		}
		seen[name], fields[index] = true, field
	}
	count := len(plan.Rows)
	if plan.DefaultRows != 0 {
		count = plan.DefaultRows
	}
	rows := make([]map[string]any, 0, count)
	now := time.Now().UTC()
	for rowIndex := 0; rowIndex < count; rowIndex++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		provided := make(map[string]any, len(canonical))
		if plan.DefaultRows == 0 {
			for columnIndex, literal := range plan.Rows[rowIndex] {
				field := fields[columnIndex]
				if literal.Kind == kitdbsql.LiteralDefault {
					continue
				}
				if field.Sequence != nil && (field.Sequence.Mode == "always" || field.Sequence.Mode == "by_default") {
					if plan.Overriding == "user" {
						continue
					}
					if field.Sequence.Mode == "always" && plan.Overriding != "system" {
						return nil, fmt.Errorf("kitdb SQL: GENERATED ALWAYS field %q requires DEFAULT or OVERRIDING SYSTEM VALUE", field.Name)
					}
				}
				item, err := resolveLiteral(literal, parameters)
				if err != nil {
					return nil, fmt.Errorf("kitdb SQL: INSERT row %d: %w", rowIndex+1, err)
				}
				provided[canonical[columnIndex]] = item
			}
		}
		for _, field := range schema.Fields {
			if _, providedField := provided[field.Name]; providedField || field.Sequence == nil {
				continue
			}
			item, err := transaction.fieldDefault(ctx, field, now)
			if err != nil {
				return nil, err
			}
			provided[field.Name] = item
		}
		row, err := fillRow(schema, provided, now)
		if err != nil {
			return nil, fmt.Errorf("kitdb SQL: INSERT row %d: %w", rowIndex+1, err)
		}
		if err := validateRequiredFields(schema, row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

type indexEntry struct {
	key   []byte
	value []byte
}

func uniqueEntries(schema kitdbsql.Schema, row map[string]any, logicalRowKey []byte) ([]indexEntry, error) {
	entries := make([]indexEntry, 0)
	for _, field := range schema.Fields {
		if field.Primary || !field.Unique {
			continue
		}
		key, applicable, err := uniqueKey(schema, field.ID, []any{row[field.Name]})
		if err != nil {
			return nil, fmt.Errorf("field %q unique key: %w", field.Name, err)
		}
		if applicable {
			entries = append(entries, indexEntry{key: key, value: bytes.Clone(logicalRowKey)})
		}
	}
	for _, constraint := range schema.UniqueConstraints {
		values := make([]any, len(constraint.Fields))
		for index, tag := range constraint.Fields {
			field, found := fieldByTag(schema, tag)
			if !found {
				return nil, fmt.Errorf("constraint %q references missing field tag %d", constraint.Name, tag)
			}
			values[index] = row[field.Name]
		}
		key, applicable, err := uniqueKey(schema, constraint.ID, values)
		if err != nil {
			return nil, fmt.Errorf("constraint %q unique key: %w", constraint.Name, err)
		}
		if applicable {
			entries = append(entries, indexEntry{key: key, value: bytes.Clone(logicalRowKey)})
		}
	}
	sort.Slice(entries, func(left, right int) bool { return bytes.Compare(entries[left].key, entries[right].key) < 0 })
	return entries, nil
}

func bindProjection(schema kitdbsql.Schema, projections []kitdbsql.Projection) ([]Column, []string, error) {
	if len(projections) == 1 && projections[0].All {
		columns := make([]Column, len(schema.Fields))
		names := make([]string, len(schema.Fields))
		for index, field := range schema.Fields {
			columns[index], names[index] = columnForField(field.Name, field), field.Name
		}
		return columns, names, nil
	}
	columns := make([]Column, len(projections))
	names := make([]string, len(projections))
	for index, projection := range projections {
		if projection.All || projection.Count {
			return nil, nil, fmt.Errorf("kitdb SQL: invalid row projection")
		}
		canonical, field, found := schema.FieldByName(unqualifiedColumn(projection.Name))
		if !found {
			return nil, nil, fmt.Errorf("kitdb SQL: table %q has no field %q", schema.Name, projection.Name)
		}
		label := projection.Alias
		if label == "" {
			label = canonical
		}
		columns[index], names[index] = columnForField(label, field), canonical
	}
	return columns, names, nil
}

func projectRow(schema kitdbsql.Schema, row map[string]any, names []string) []any {
	result := make([]any, len(names))
	for index, name := range names {
		_, field, _ := schema.FieldByName(name)
		result[index] = readField(field, row[name])
	}
	return result
}
