package relational

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/kitdb/managed"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// OpenNativeSQL exposes this Engine through database/sql without opening a
// socket or taking ownership of the engine. Closing the returned pool drains
// logical connections; the Engine owner must close the Engine separately.
func OpenNativeSQL(engine *Engine) (*sql.DB, error) {
	if engine == nil {
		return nil, fmt.Errorf("kitdb native SQL: engine is nil")
	}
	engine.mu.RLock()
	err := engine.readyLocked()
	engine.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(nativeSQLConnector{engine: engine}), nil
}

type nativeSQLConnector struct{ engine *Engine }

func (connector nativeSQLConnector) Connect(context.Context) (driver.Conn, error) {
	if connector.engine == nil {
		return nil, fmt.Errorf("kitdb native SQL: engine is nil")
	}
	return &nativeSQLConnection{engine: connector.engine}, nil
}

func (connector nativeSQLConnector) Driver() driver.Driver {
	return nativeSQLDriver{}
}

type nativeNodeSQLConnector struct {
	adapter     string
	node        *PostgresNode
	database    string
	readOnly    bool
	permissions *managed.DatabasePermissions
	account     *managed.UserInfo
}

func (connector nativeNodeSQLConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if connector.node == nil {
		return nil, fmt.Errorf("kitdb native node: node is nil")
	}
	databases, err := connector.node.discoverDatabases()
	if err != nil {
		return nil, err
	}
	database, found := findPostgresNodeDatabase(databases, connector.database)
	if !found {
		return nil, fmt.Errorf("kitdb native node: database %q does not exist", connector.database)
	}
	acquireContext, cancel := context.WithTimeout(ctx, connector.node.databaseAcquireTimeout)
	defer cancel()
	entry, err := connector.node.acquireEngine(acquireContext, database)
	if err != nil {
		return nil, err
	}
	trace := func(source string, parameterCount int) {
		if connector.node.trace != nil {
			connector.node.trace(database.name, source, parameterCount)
		}
	}
	account := managed.UserInfo{Name: connector.node.user, Admin: true}
	if connector.account != nil {
		account = *connector.account
	}
	localPolicy, err := entry.engine.localRolePolicy(account, database.name)
	if err != nil {
		_ = connector.node.releaseEngine(entry)
		return nil, err
	}
	connection := &nativeSQLConnection{
		localPolicy: localPolicy,
		engine:      entry.engine, readOnly: connector.node.readOnly || connector.readOnly, trace: trace,
		permissions: connector.permissions,
		accountCommand: func(ctx context.Context, source string, values []any, inTransaction bool) (Result, bool, error) {
			return connector.node.accountCommand(ctx, account, source, values, inTransaction, false)
		},
		release: func() error { return connector.node.releaseEngine(entry) },
	}
	if localPolicy != nil {
		connection.readOnly = connector.node.readOnly
	}
	connection.catalogQuery = func(ctx context.Context, source string, values []any) (Result, bool, error) {
		if isLocalRoleCatalogQuery(source) {
			return connector.node.localRoleCatalogQuery(ctx, account, databaseCatalogScope{engine: entry.engine, database: database.name, active: connection.transaction, permissions: connection.permissions, local: connection.localPolicy, readOnly: connection.readOnly}, source, values)
		}
		return connector.node.accountCatalogQuery(account, source, values)
	}
	adapter := connector.adapter
	connection.localRoleCommand = func(ctx context.Context, source string, inTx bool) (Result, bool, error) {
		return connector.node.localRoleCommand(ctx, entry.engine, database.name, account, source, inTx, false)
	}
	connection.sessionCommand = func(ctx context.Context, source string, values []any, inTransaction bool) (Result, bool, error) {
		return connector.node.sessionCommand(ctx, account, connection.record, source, values, inTransaction, false)
	}
	if adapter == "" {
		adapter = "native"
	}
	_, err = connector.node.registerSession(account, database.name, adapter, connection.Close, func(record *nodeSessionRecord) {
		connection.record = record
		connection.node = connector.node
	})
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

func (connector nativeNodeSQLConnector) Driver() driver.Driver { return nativeSQLDriver{} }

type nativeSQLDriver struct{}

func (nativeSQLDriver) Open(string) (driver.Conn, error) {
	return nil, fmt.Errorf("kitdb native SQL: use relational.OpenNativeSQL")
}

type nativeSQLConnection struct {
	localPolicy      *localRolePolicy
	localRoleCommand func(context.Context, string, bool) (Result, bool, error)
	sessionCommand   func(context.Context, string, []any, bool) (Result, bool, error)
	record           *nodeSessionRecord
	node             *PostgresNode
	mu               sync.Mutex
	engine           *Engine
	transaction      *Transaction
	sequences        sequenceSession
	readOnly         bool
	permissions      *managed.DatabasePermissions
	catalogQuery     func(context.Context, string, []any) (Result, bool, error)
	accountCommand   func(context.Context, string, []any, bool) (Result, bool, error)
	trace            func(source string, parameterCount int)
	release          func() error
	closed           bool
	closeDone        chan struct{}
	closeErr         error
}

func (connection *nativeSQLConnection) Prepare(query string) (driver.Stmt, error) {
	return connection.PrepareContext(context.Background(), query)
}

func (connection *nativeSQLConnection) PrepareContext(_ context.Context, query string) (driver.Stmt, error) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.closed {
		return nil, driver.ErrBadConn
	}
	return &nativeSQLStatement{connection: connection, query: query}, nil
}

func (connection *nativeSQLConnection) Close() error {
	if connection.record != nil {
		connection.record.cancel()
	}
	connection.mu.Lock()
	if connection.closed {
		done := connection.closeDone
		connection.mu.Unlock()
		<-done
		connection.mu.Lock()
		err := connection.closeErr
		connection.mu.Unlock()
		return err
	}
	connection.closed = true
	connection.closeDone = make(chan struct{})
	var closeErr error
	if connection.transaction != nil {
		closeErr = connection.transaction.Rollback()
		connection.transaction = nil
	}
	connection.engine = nil
	release := connection.release
	connection.release = nil
	connection.mu.Unlock()
	if release != nil {
		closeErr = errors.Join(closeErr, release())
	}
	if connection.node != nil {
		connection.node.unregisterSession(connection.record)
	}
	connection.mu.Lock()
	connection.closeErr = closeErr
	close(connection.closeDone)
	connection.mu.Unlock()
	return closeErr
}

func (connection *nativeSQLConnection) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}

func (connection *nativeSQLConnection) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	ctx, cleanup := connection.record.operationContext(ctx)
	defer cleanup()
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.closed || connection.engine == nil {
		return nil, driver.ErrBadConn
	}
	if connection.transaction != nil {
		return nil, fmt.Errorf("kitdb native SQL: transaction already active")
	}
	switch sql.IsolationLevel(options.Isolation) {
	case sql.LevelDefault, sql.LevelRepeatableRead, sql.LevelSerializable:
	default:
		return nil, fmt.Errorf("kitdb native SQL: unsupported isolation level %d", options.Isolation)
	}
	transaction, err := connection.engine.BeginTransaction(ctx, TransactionOptions{
		ReadOnly: connection.readOnly || options.ReadOnly,
	})
	if err != nil {
		return nil, err
	}
	connection.transaction = transaction
	if connection.record != nil {
		connection.record.state.Store(1)
	}
	return &nativeSQLTransaction{connection: connection, transaction: transaction}, nil
}

func (connection *nativeSQLConnection) Ping(ctx context.Context) error {
	_, err := connection.execute(ctx, `SELECT 1`, nil)
	return err
}

func (connection *nativeSQLConnection) ResetSession(context.Context) error {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.closed {
		return driver.ErrBadConn
	}
	if connection.transaction != nil {
		_ = connection.transaction.Rollback()
		connection.transaction = nil
		if connection.record != nil {
			connection.record.state.Store(0)
		}
	}
	return nil
}

func (connection *nativeSQLConnection) IsValid() bool {
	connection.mu.Lock()
	valid := !connection.closed && connection.engine != nil
	connection.mu.Unlock()
	return valid
}

func (connection *nativeSQLConnection) ExecContext(
	ctx context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	result, err := connection.execute(ctx, query, nativeSQLArguments(arguments))
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(result.Affected), nil
}

func (connection *nativeSQLConnection) QueryContext(
	ctx context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Rows, error) {
	ctx, cleanup := connection.record.operationContext(ctx)
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()
	values := nativeSQLArguments(arguments)
	connection.mu.Lock()
	if connection.closed || connection.engine == nil {
		connection.mu.Unlock()
		return nil, driver.ErrBadConn
	}
	if connection.trace != nil {
		connection.trace(kitdbsql.AccountTraceSource(query), len(values))
	}
	if connection.sessionCommand != nil {
		if result, handled, err := connection.sessionCommand(ctx, query, values, connection.transaction != nil); handled {
			connection.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return connection.materializedRows(result)
		}
	}
	if connection.localRoleCommand != nil {
		if _, handled, _ := kitdbsql.ParseLocalRoleStatement(query); handled && len(values) != 0 {
			connection.mu.Unlock()
			return nil, fmt.Errorf("local role statements do not accept value parameters")
		}
		if result, handled, err := connection.localRoleCommand(ctx, query, connection.transaction != nil); handled {
			connection.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return connection.materializedRows(result)
		}
	}
	if connection.accountCommand != nil {
		if result, handled, err := connection.accountCommand(ctx, query, values, connection.transaction != nil); handled {
			connection.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return connection.materializedRows(result)
		}
	}
	if connection.catalogQuery != nil {
		if result, handled, err := connection.catalogQuery(ctx, query, values); handled {
			connection.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return connection.materializedRows(result)
		}
	}
	if connection.localPolicy != nil {
		if result, handled, err := connection.engine.executeLocalStatement(ctx, connection.localPolicy, connection.transaction, query, values, false, connection.readOnly); handled {
			connection.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return connection.materializedRows(result)
		}
	}
	if err := checkDatabaseStatement(connection.permissions, query); err != nil {
		connection.mu.Unlock()
		return nil, err
	}
	if connection.transaction != nil {
		result, err := connection.transaction.Execute(ctx, query, values...)
		connection.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return connection.materializedRows(result)
	}
	stream, handled, err := connection.engine.openSelectRowStream(ctx, query, values)
	if handled {
		connection.mu.Unlock()
		if err != nil {
			return nil, err
		}
		rows := newNativeSQLStreamRows(stream)
		if connection.record != nil {
			rows.lifetime = connection.record.ctx
			stop := context.AfterFunc(connection.record.ctx, func() { _ = stream.Close() })
			finish := cleanup
			rows.cleanup = func() { stop(); finish() }
		} else {
			rows.cleanup = cleanup
		}
		cleanup = nil
		return rows, nil
	}
	result, err := connection.engine.executeWithSequences(
		ctx, query, values, &connection.sequences, connection.readOnly,
	)
	connection.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return connection.materializedRows(result)
}

func (connection *nativeSQLConnection) execute(ctx context.Context, query string, arguments []any) (Result, error) {
	ctx, cleanup := connection.record.operationContext(ctx)
	defer cleanup()
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.closed || connection.engine == nil {
		return Result{}, driver.ErrBadConn
	}
	if connection.trace != nil {
		connection.trace(kitdbsql.AccountTraceSource(query), len(arguments))
	}
	if connection.sessionCommand != nil {
		if result, handled, err := connection.sessionCommand(ctx, query, arguments, connection.transaction != nil); handled {
			return result, err
		}
	}
	if connection.localRoleCommand != nil {
		if _, handled, _ := kitdbsql.ParseLocalRoleStatement(query); handled && len(arguments) != 0 {
			return Result{}, fmt.Errorf("local role statements do not accept value parameters")
		}
		if result, handled, err := connection.localRoleCommand(ctx, query, connection.transaction != nil); handled {
			return result, err
		}
	}
	if connection.accountCommand != nil {
		if result, handled, err := connection.accountCommand(ctx, query, arguments, connection.transaction != nil); handled {
			return result, err
		}
	}
	if connection.catalogQuery != nil {
		if result, handled, err := connection.catalogQuery(ctx, query, arguments); handled {
			return result, err
		}
	}
	if connection.localPolicy != nil {
		if result, handled, err := connection.engine.executeLocalStatement(ctx, connection.localPolicy, connection.transaction, query, arguments, false, connection.readOnly); handled {
			return result, err
		}
	}
	if err := checkDatabaseStatement(connection.permissions, query); err != nil {
		return Result{}, err
	}
	if connection.transaction != nil {
		return connection.transaction.Execute(ctx, query, arguments...)
	}
	return connection.engine.executeWithSequences(ctx, query, arguments, &connection.sequences, connection.readOnly)
}

type nativeSQLTransaction struct {
	connection  *nativeSQLConnection
	transaction *Transaction
}

func (transaction *nativeSQLTransaction) Commit() error {
	return transaction.finish(true)
}

func (transaction *nativeSQLTransaction) Rollback() error {
	return transaction.finish(false)
}

func (transaction *nativeSQLTransaction) finish(commit bool) error {
	if transaction == nil || transaction.connection == nil || transaction.transaction == nil {
		return sql.ErrTxDone
	}
	connection := transaction.connection
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.transaction != transaction.transaction {
		return sql.ErrTxDone
	}
	connection.transaction = nil
	if connection.record != nil {
		connection.record.state.Store(0)
	}
	if commit {
		ctx, cleanup := connection.record.operationContext(context.Background())
		defer cleanup()
		_, err := transaction.transaction.Commit(ctx)
		return err
	}
	return transaction.transaction.Rollback()
}

type nativeSQLStatement struct {
	connection *nativeSQLConnection
	query      string
}

func (statement *nativeSQLStatement) Close() error  { return nil }
func (statement *nativeSQLStatement) NumInput() int { return -1 }

func (statement *nativeSQLStatement) Exec(arguments []driver.Value) (driver.Result, error) {
	return statement.ExecContext(context.Background(), nativeSQLNamedArguments(arguments))
}

func (statement *nativeSQLStatement) Query(arguments []driver.Value) (driver.Rows, error) {
	return statement.QueryContext(context.Background(), nativeSQLNamedArguments(arguments))
}

func (statement *nativeSQLStatement) ExecContext(
	ctx context.Context,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	return statement.connection.ExecContext(ctx, statement.query, arguments)
}

func (statement *nativeSQLStatement) QueryContext(
	ctx context.Context,
	arguments []driver.NamedValue,
) (driver.Rows, error) {
	return statement.connection.QueryContext(ctx, statement.query, arguments)
}

type nativeSQLRows struct {
	lifetime context.Context
	columns  []Column
	rows     [][]any
	index    int
}

type nativeSQLStreamRows struct {
	lifetime context.Context
	cleanup  func()
	columns  []Column
	stream   atomic.Pointer[selectRowStream]
}

func newNativeSQLStreamRows(stream *selectRowStream) *nativeSQLStreamRows {
	rows := &nativeSQLStreamRows{columns: stream.Columns()}
	rows.stream.Store(stream)
	return rows
}

func (rows *nativeSQLStreamRows) Columns() []string {
	columns := make([]string, len(rows.columns))
	for index, column := range rows.columns {
		columns[index] = column.Name
	}
	return columns
}

func (rows *nativeSQLStreamRows) Close() error {
	stream := rows.stream.Swap(nil)
	if stream == nil {
		return nil
	}
	if rows.cleanup != nil {
		rows.cleanup()
	}
	return stream.Close()
}

func (rows *nativeSQLStreamRows) Next(destination []driver.Value) error {
	if rows.lifetime != nil && rows.lifetime.Err() != nil {
		return rows.lifetime.Err()
	}
	stream := rows.stream.Load()
	if stream == nil {
		return io.EOF
	}
	row, err := stream.Next()
	if err != nil {
		return err
	}
	if len(row) != len(rows.columns) {
		return fmt.Errorf("kitdb native SQL: row width does not match result columns")
	}
	for index, item := range row {
		converted, err := nativeSQLValue(item)
		if err != nil {
			return err
		}
		destination[index] = converted
	}
	return nil
}

func (rows *nativeSQLStreamRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(rows.columns) {
		return ""
	}
	return rows.columns[index].Kind
}

func newNativeSQLRows(result Result) (*nativeSQLRows, error) {
	for _, row := range result.Rows {
		if len(row) != len(result.Columns) {
			return nil, fmt.Errorf("kitdb native SQL: row width does not match result columns")
		}
	}
	return &nativeSQLRows{columns: result.Columns, rows: result.Rows}, nil
}

func (connection *nativeSQLConnection) materializedRows(result Result) (*nativeSQLRows, error) {
	rows, err := newNativeSQLRows(result)
	if err == nil && connection.record != nil {
		rows.lifetime = connection.record.ctx
	}
	return rows, err
}

func (rows *nativeSQLRows) Columns() []string {
	columns := make([]string, len(rows.columns))
	for index, column := range rows.columns {
		columns[index] = column.Name
	}
	return columns
}

func (rows *nativeSQLRows) Close() error {
	rows.rows = nil
	return nil
}

func (rows *nativeSQLRows) Next(destination []driver.Value) error {
	if rows.lifetime != nil && rows.lifetime.Err() != nil {
		return rows.lifetime.Err()
	}
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	row := rows.rows[rows.index]
	rows.index++
	for index, item := range row {
		converted, err := nativeSQLValue(item)
		if err != nil {
			return err
		}
		destination[index] = converted
	}
	return nil
}

func (rows *nativeSQLRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(rows.columns) {
		return ""
	}
	return rows.columns[index].Kind
}

func nativeSQLArguments(arguments []driver.NamedValue) []any {
	values := make([]any, len(arguments))
	for index, argument := range arguments {
		values[index] = argument.Value
	}
	return values
}

func nativeSQLNamedArguments(arguments []driver.Value) []driver.NamedValue {
	values := make([]driver.NamedValue, len(arguments))
	for index, argument := range arguments {
		values[index] = driver.NamedValue{Ordinal: index + 1, Value: argument}
	}
	return values
}

func nativeSQLValue(item any) (driver.Value, error) {
	switch value := item.(type) {
	case nil, bool, int64, float64, string, []byte, time.Time:
		return value, nil
	case exactTemporal:
		return value.text, nil
	case time.Duration:
		return value.String(), nil
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case uint:
		if uint64(value) > math.MaxInt64 {
			return nil, fmt.Errorf("kitdb native SQL: unsigned integer exceeds BIGINT")
		}
		return int64(value), nil
	case uint8:
		return int64(value), nil
	case uint16:
		return int64(value), nil
	case uint32:
		return int64(value), nil
	case uint64:
		if value > math.MaxInt64 {
			return nil, fmt.Errorf("kitdb native SQL: unsigned integer exceeds BIGINT")
		}
		return int64(value), nil
	case float32:
		return float64(value), nil
	case map[string]any, []any:
		encoded, err := json.Marshal(value)
		return encoded, err
	default:
		return nil, fmt.Errorf("kitdb native SQL: unsupported result value %T", item)
	}
}

var (
	_ driver.Connector        = nativeSQLConnector{}
	_ driver.Connector        = nativeNodeSQLConnector{}
	_ driver.Conn             = (*nativeSQLConnection)(nil)
	_ driver.ConnBeginTx      = (*nativeSQLConnection)(nil)
	_ driver.ExecerContext    = (*nativeSQLConnection)(nil)
	_ driver.QueryerContext   = (*nativeSQLConnection)(nil)
	_ driver.Pinger           = (*nativeSQLConnection)(nil)
	_ driver.SessionResetter  = (*nativeSQLConnection)(nil)
	_ driver.Validator        = (*nativeSQLConnection)(nil)
	_ driver.StmtExecContext  = (*nativeSQLStatement)(nil)
	_ driver.StmtQueryContext = (*nativeSQLStatement)(nil)
	_ driver.Rows             = (*nativeSQLRows)(nil)
)
