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
	"time"
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
	node     *PostgresNode
	database string
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
	return &nativeSQLConnection{
		engine: entry.engine, readOnly: connector.node.readOnly, trace: trace,
		release: func() error { return connector.node.releaseEngine(entry) },
	}, nil
}

func (connector nativeNodeSQLConnector) Driver() driver.Driver { return nativeSQLDriver{} }

type nativeSQLDriver struct{}

func (nativeSQLDriver) Open(string) (driver.Conn, error) {
	return nil, fmt.Errorf("kitdb native SQL: use relational.OpenNativeSQL")
}

type nativeSQLConnection struct {
	mu          sync.Mutex
	engine      *Engine
	transaction *Transaction
	sequences   sequenceSession
	readOnly    bool
	trace       func(source string, parameterCount int)
	release     func() error
	closed      bool
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
	connection.mu.Lock()
	if connection.closed {
		connection.mu.Unlock()
		return nil
	}
	connection.closed = true
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
	return closeErr
}

func (connection *nativeSQLConnection) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}

func (connection *nativeSQLConnection) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
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
	result, err := connection.execute(ctx, query, nativeSQLArguments(arguments))
	if err != nil {
		return nil, err
	}
	return newNativeSQLRows(result)
}

func (connection *nativeSQLConnection) execute(ctx context.Context, query string, arguments []any) (Result, error) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.closed || connection.engine == nil {
		return Result{}, driver.ErrBadConn
	}
	if connection.trace != nil {
		connection.trace(query, len(arguments))
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
	if commit {
		_, err := transaction.transaction.Commit(context.Background())
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
	columns []Column
	rows    [][]any
	index   int
}

func newNativeSQLRows(result Result) (*nativeSQLRows, error) {
	for _, row := range result.Rows {
		if len(row) != len(result.Columns) {
			return nil, fmt.Errorf("kitdb native SQL: row width does not match result columns")
		}
	}
	return &nativeSQLRows{columns: result.Columns, rows: result.Rows}, nil
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
