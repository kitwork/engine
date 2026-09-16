package relational

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type PostgresOptions struct {
	Database string
	User     string
	Password string
	ReadOnly bool
	Trace    func(source string, parameterCount int)
}

type PostgresServerOptions struct {
	PostgresOptions
	MaxConcurrentQueries       int
	MaxConcurrentQueriesPerKey int
	MaxQueuedQueries           int
	MaxQueuedQueriesPerKey     int
	MaxConnections             int
	MaxConcurrentCopies        int
	MaxConcurrentCopiesPerKey  int
	MaxQueuedCopies            int
	MaxQueuedCopiesPerKey      int
	MaxMessageBytes            int
	MaxCopyBytes               int64
	IdleTimeout                time.Duration
	QueryTimeout               time.Duration
	CopyTimeout                time.Duration
	QueryMetrics               *pgwire.QueryMetrics
	CopyMetrics                *pgwire.CopyMetrics
}

type postgresAuthenticator struct {
	engine   *Engine
	database string
	user     string
	password string
	readOnly bool
	trace    func(source string, parameterCount int)
}

type postgresSession struct {
	sequences              sequenceSession
	authenticator          *postgresAuthenticator
	closeOnce              sync.Once
	mu                     sync.Mutex
	transaction            *Transaction
	failed                 bool
	node                   *PostgresNode
	maintenance            bool
	maintenanceTransaction bool
	onClose                func() error
}

// PostgresAuthenticator creates a protocol adapter over this standalone
// engine. The password profile is intentionally loopback-only because pgwire
// does not yet claim TLS or SCRAM.
func (engine *Engine) PostgresAuthenticator(options PostgresOptions) (pgwire.Authenticator, error) {
	if engine == nil {
		return nil, fmt.Errorf("kitdb postgres: engine is nil")
	}
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return nil, err
	}
	user := strings.TrimSpace(options.User)
	if user == "" {
		user = "kitdb"
	}
	password := options.Password
	if password == "" {
		return nil, fmt.Errorf("kitdb postgres: password is required")
	}
	database := strings.TrimSpace(options.Database)
	if database == "" {
		database = strings.TrimSuffix(filepath.Base(engine.database.Path()), filepath.Ext(engine.database.Path()))
	}
	if database == "" || strings.ContainsAny(database, "\x00/\\") {
		return nil, fmt.Errorf("kitdb postgres: invalid logical database name %q", database)
	}
	return &postgresAuthenticator{
		engine: engine, database: database, user: user, password: password,
		readOnly: options.ReadOnly, trace: options.Trace,
	}, nil
}

func (engine *Engine) ServePostgres(
	ctx context.Context,
	listener net.Listener,
	options PostgresServerOptions,
) error {
	authenticator, err := engine.PostgresAuthenticator(options.PostgresOptions)
	if err != nil {
		return err
	}
	return (pgwire.Server{
		Authenticator:              authenticator,
		MaxConnections:             options.MaxConnections,
		MaxConcurrentQueries:       options.MaxConcurrentQueries,
		MaxConcurrentQueriesPerKey: options.MaxConcurrentQueriesPerKey,
		MaxQueuedQueries:           options.MaxQueuedQueries,
		MaxQueuedQueriesPerKey:     options.MaxQueuedQueriesPerKey,
		MaxConcurrentCopies:        options.MaxConcurrentCopies,
		MaxConcurrentCopiesPerKey:  options.MaxConcurrentCopiesPerKey,
		MaxQueuedCopies:            options.MaxQueuedCopies,
		MaxQueuedCopiesPerKey:      options.MaxQueuedCopiesPerKey,
		MaxMessageBytes:            options.MaxMessageBytes,
		MaxCopyBytes:               options.MaxCopyBytes,
		IdleTimeout:                options.IdleTimeout,
		QueryTimeout:               options.QueryTimeout,
		CopyTimeout:                options.CopyTimeout,
		QueryMetrics:               options.QueryMetrics,
		CopyMetrics:                options.CopyMetrics,
	}).Serve(ctx, listener)
}

func (authenticator *postgresAuthenticator) Authenticate(
	ctx context.Context,
	startup pgwire.Startup,
	password string,
) (pgwire.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, pgwire.NewError("57014", "connection canceled")
	}
	if subtle.ConstantTimeCompare([]byte(startup.User()), []byte(authenticator.user)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(authenticator.password)) != 1 {
		return nil, pgwire.NewError("28P01", "password authentication failed for KitDB user")
	}
	requested := strings.TrimSpace(startup.Database())
	if requested != "" && !strings.EqualFold(strings.TrimSuffix(requested, ".kitdb"), authenticator.database) {
		return nil, pgwire.NewError(
			"3D000", fmt.Sprintf("KitDB database %q is not served by this listener", requested),
		)
	}
	return &postgresSession{authenticator: authenticator}, nil
}

func (session *postgresSession) Close() error {
	if session == nil {
		return nil
	}
	var closeErr error
	session.closeOnce.Do(func() {
		session.mu.Lock()
		transaction := session.transaction
		session.transaction = nil
		session.failed = false
		session.maintenanceTransaction = false
		session.mu.Unlock()
		if transaction != nil {
			closeErr = transaction.Rollback()
		}
		if session.onClose != nil {
			closeErr = errors.Join(closeErr, session.onClose())
		}
	})
	return closeErr
}

func (session *postgresSession) TransactionStatus() byte {
	if session == nil {
		return pgwire.TransactionIdle
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.maintenanceTransaction {
		if session.failed {
			return pgwire.TransactionFailed
		}
		return pgwire.TransactionActive
	}
	if session.transaction == nil {
		return pgwire.TransactionIdle
	}
	if session.failed {
		return pgwire.TransactionFailed
	}
	return pgwire.TransactionActive
}

func (session *postgresSession) CopyAdmission() pgwire.CopyAdmission {
	return pgwire.CopyAdmission{Key: session.authenticator.database, Weight: 1}
}

func (session *postgresSession) QueryAdmission() pgwire.QueryAdmission {
	return pgwire.QueryAdmission{Key: session.authenticator.database, Weight: 1}
}

func (session *postgresSession) Describe(
	ctx context.Context,
	source string,
	parameters []pgwire.Parameter,
) ([]pgwire.Column, error) {
	if session == nil || session.authenticator == nil ||
		(session.authenticator.engine == nil && !session.maintenance) {
		return nil, pgwire.NewError("08003", "KitDB session is closed")
	}
	if session.maintenance {
		return session.describeMaintenance(source, parameters)
	}
	if isSavepointSQL(source) {
		_, err := kitdbsql.ParseStatement(source)
		return nil, postgresError(err)
	}
	session.mu.Lock()
	transaction := session.transaction
	failed := session.failed
	session.mu.Unlock()
	if transaction != nil {
		if failed {
			return nil, pgwire.NewError("25P02", "current KitDB transaction is aborted; issue ROLLBACK")
		}
		if result, handled, err := session.compatibilityQuery(source); handled {
			if err != nil {
				return nil, err
			}
			return append([]pgwire.Column(nil), result.Columns...), nil
		}
		if result, handled, err := session.catalogQueryForTransaction(transaction, source, parameters, true); handled {
			if err != nil {
				return nil, err
			}
			return append([]pgwire.Column(nil), result.Columns...), nil
		}
		columns, err := transaction.Describe(ctx, source)
		if err != nil {
			return nil, postgresError(err)
		}
		result := make([]pgwire.Column, len(columns))
		for index, column := range columns {
			result[index] = postgresColumn(column)
		}
		return result, nil
	}
	if result, handled, err := session.compatibilityQuery(source); handled {
		if err != nil {
			return nil, err
		}
		return append([]pgwire.Column(nil), result.Columns...), nil
	}
	if result, handled, err := session.catalogQuery(source, parameters, true); handled {
		if err != nil {
			return nil, err
		}
		return append([]pgwire.Column(nil), result.Columns...), nil
	}
	columns, err := session.authenticator.engine.Describe(ctx, source)
	if err != nil {
		return nil, postgresError(err)
	}
	result := make([]pgwire.Column, len(columns))
	for index, column := range columns {
		result[index] = postgresColumn(column)
	}
	return result, nil
}

func (session *postgresSession) Execute(
	ctx context.Context,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, error) {
	if session == nil || session.authenticator == nil ||
		(session.authenticator.engine == nil && !session.maintenance) {
		return pgwire.Result{}, pgwire.NewError("08003", "KitDB session is closed")
	}
	if session.maintenance {
		return session.executeMaintenance(ctx, source, parameters)
	}
	if session.authenticator.trace != nil {
		session.authenticator.trace(source, len(parameters))
	}
	if normalizePostgresSQL(source) == "discard all" {
		session.mu.Lock()
		defer session.mu.Unlock()
		if session.transaction != nil {
			session.failed = true
			return pgwire.Result{}, pgwire.NewError("25001", "DISCARD ALL cannot run inside a transaction")
		}
		if len(parameters) != 0 {
			return pgwire.Result{}, pgwire.NewError("42601", "DISCARD ALL does not accept parameters")
		}
		session.sequences.reset()
		return pgwire.Result{CommandTag: "DISCARD ALL"}, nil
	}
	if result, handled, err := session.transactionExecute(ctx, source, parameters); handled {
		return result, err
	}
	if result, handled, err := session.compatibilityQuery(source); handled {
		return result, err
	}
	if result, handled, err := session.catalogQuery(source, parameters, false); handled {
		return result, err
	}
	bound, err := postgresParameters(parameters)
	if err != nil {
		return pgwire.Result{}, err
	}
	statement, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return pgwire.Result{}, postgresError(err)
	}
	if session.authenticator.readOnly && !standaloneStatementReadOnly(statement.Kind) {
		return pgwire.Result{}, pgwire.NewError("25006", "database is read-only")
	}
	result, err := session.authenticator.engine.executeWithSequences(ctx, source, bound, &session.sequences, session.authenticator.readOnly)
	if err != nil {
		return pgwire.Result{}, postgresError(err)
	}
	return postgresResult(result), nil
}

func (session *postgresSession) transactionExecute(
	ctx context.Context,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, bool, error) {
	normalized := normalizePostgresSQL(source)
	if isSavepointSQL(source) {
		session.mu.Lock()
		defer session.mu.Unlock()
		statement, err := kitdbsql.ParseStatement(source)
		if err != nil || len(parameters) != 0 {
			if session.transaction != nil {
				session.failed = true
			}
			if err == nil {
				err = pgwire.NewError("42601", "savepoints do not accept parameters")
			}
			return pgwire.Result{}, true, postgresError(err)
		}
		if session.transaction == nil {
			return pgwire.Result{}, true, postgresError(errSavepointTransaction)
		}
		if session.failed && statement.Savepoint.Action != "rollback" {
			return pgwire.Result{}, true, pgwire.NewError("25P02", "current KitDB transaction is aborted; issue ROLLBACK TO SAVEPOINT or ROLLBACK")
		}
		result, err := session.transaction.executeParsed(ctx, statement, nil)
		if err != nil {
			session.failed = true
		} else if statement.Savepoint.Action == "rollback" {
			session.failed = false
		}
		return postgresResult(result), true, postgresError(err)
	}
	if begin, readOnly, err := parsePostgresBegin(normalized); begin {
		if err != nil {
			return pgwire.Result{}, true, err
		}
		if len(parameters) != 0 {
			return pgwire.Result{}, true, pgwire.NewError("42601", "BEGIN does not accept parameters")
		}
		session.mu.Lock()
		defer session.mu.Unlock()
		if session.transaction != nil {
			return pgwire.Result{}, true, pgwire.NewError("25001", "a KitDB transaction is already active")
		}
		transaction, err := session.authenticator.engine.BeginTransaction(
			ctx,
			TransactionOptions{ReadOnly: session.authenticator.readOnly || readOnly},
		)
		if err != nil {
			return pgwire.Result{}, true, postgresError(err)
		}
		transaction.sequences = &session.sequences
		session.transaction = transaction
		session.failed = false
		return pgwire.Result{CommandTag: "BEGIN"}, true, nil
	}

	switch normalized {
	case "rollback", "rollback transaction", "rollback work":
		if len(parameters) != 0 {
			return pgwire.Result{}, true, pgwire.NewError("42601", "ROLLBACK does not accept parameters")
		}
		session.mu.Lock()
		transaction := session.transaction
		session.transaction = nil
		session.failed = false
		session.mu.Unlock()
		if transaction != nil {
			_ = transaction.Rollback()
		}
		return pgwire.Result{CommandTag: "ROLLBACK"}, true, nil
	case "commit", "commit transaction", "commit work", "end", "end transaction", "end work":
		if len(parameters) != 0 {
			return pgwire.Result{}, true, pgwire.NewError("42601", "COMMIT does not accept parameters")
		}
		session.mu.Lock()
		transaction := session.transaction
		failed := session.failed
		session.transaction = nil
		session.failed = false
		session.mu.Unlock()
		if transaction == nil {
			return pgwire.Result{CommandTag: "COMMIT"}, true, nil
		}
		if failed {
			_ = transaction.Rollback()
			return pgwire.Result{CommandTag: "ROLLBACK"}, true, nil
		}
		if _, err := transaction.Commit(ctx); err != nil {
			return pgwire.Result{}, true, postgresError(err)
		}
		return pgwire.Result{CommandTag: "COMMIT"}, true, nil
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	transaction := session.transaction
	if transaction == nil {
		return pgwire.Result{}, false, nil
	}
	if session.failed {
		return pgwire.Result{}, true, pgwire.NewError(
			"25P02", "current KitDB transaction is aborted; issue ROLLBACK",
		)
	}
	if normalized == "show transaction_read_only" {
		value := "off"
		if transaction.ReadOnly() {
			value = "on"
		}
		return postgresTextResult("transaction_read_only", value), true, nil
	}
	if result, handled, err := session.compatibilityQuery(source); handled {
		if err != nil {
			session.failed = true
		}
		return result, true, err
	}
	if result, handled, err := session.catalogQueryForTransaction(transaction, source, parameters, false); handled {
		if err != nil {
			session.failed = true
		}
		return result, true, err
	}
	bound, err := postgresParameters(parameters)
	if err != nil {
		session.failed = true
		return pgwire.Result{}, true, err
	}
	statement, err := kitdbsql.ParseStatement(source)
	if err != nil {
		session.failed = true
		return pgwire.Result{}, true, postgresError(err)
	}
	if transaction.ReadOnly() && !standaloneStatementReadOnly(statement.Kind) {
		session.failed = true
		return pgwire.Result{}, true, pgwire.NewError("25006", "transaction is read-only")
	}
	result, err := transaction.executeParsed(ctx, statement, bound)
	if err != nil {
		session.failed = true
		return pgwire.Result{}, true, postgresError(err)
	}
	return postgresResult(result), true, nil
}

func parsePostgresBegin(normalized string) (bool, bool, error) {
	var options string
	switch {
	case normalized == "begin" || strings.HasPrefix(normalized, "begin "):
		options = strings.TrimSpace(strings.TrimPrefix(normalized, "begin"))
		options = strings.TrimSpace(strings.TrimPrefix(options, "transaction"))
	case normalized == "start transaction" || strings.HasPrefix(normalized, "start transaction "):
		options = strings.TrimSpace(strings.TrimPrefix(normalized, "start transaction"))
	default:
		return false, false, nil
	}
	if options == "" || options == "read write" {
		return true, false, nil
	}
	if options == "read only" {
		return true, true, nil
	}
	readOnly := false
	if strings.HasSuffix(options, " read only") {
		readOnly = true
		options = strings.TrimSpace(strings.TrimSuffix(options, " read only"))
	} else if strings.HasSuffix(options, " read write") {
		options = strings.TrimSpace(strings.TrimSuffix(options, " read write"))
	}
	switch options {
	case "isolation level read uncommitted", "isolation level read committed",
		"isolation level repeatable read", "isolation level serializable":
		return true, readOnly, nil
	default:
		return true, false, pgwire.NewError("0A000", "unsupported KitDB transaction option")
	}
}

func postgresParameters(parameters []pgwire.Parameter) ([]any, error) {
	bound := make([]any, len(parameters))
	for index, parameter := range parameters {
		item, err := decodePostgresParameter(parameter)
		if err != nil {
			return nil, err
		}
		bound[index] = item
	}
	return bound, nil
}

func (session *postgresSession) compatibilityQuery(source string) (pgwire.Result, bool, error) {
	normalized := normalizePostgresSQL(source)
	switch normalized {
	case "select version()", "select pg_catalog.version()":
		return postgresTextResult("version", "KitDB standalone PostgreSQL profile 0.1"), true, nil
	case "select current_database()", "select pg_catalog.current_database()":
		return postgresTextResult("current_database", session.authenticator.database), true, nil
	case "select current_schema()", "select pg_catalog.current_schema()":
		return postgresTextResult("current_schema", "public"), true, nil
	case "select current_user", "select session_user":
		return postgresTextResult(strings.TrimPrefix(normalized, "select "), session.authenticator.user), true, nil
	case "select 1":
		return pgwire.Result{
			Columns: []pgwire.Column{{Name: "?column?", DataTypeOID: pgwire.OIDInt8, DataTypeSize: 8}},
			Rows:    [][]pgwire.Field{{{Data: []byte("1")}}}, CommandTag: "SELECT 1",
		}, true, nil
	case "show server_version":
		return postgresTextResult("server_version", "16.0"), true, nil
	case "show server_version_num":
		return postgresTextResult("server_version_num", "160000"), true, nil
	case "show transaction_isolation":
		return postgresTextResult("transaction_isolation", "read committed"), true, nil
	case "show transaction_read_only":
		value := "off"
		if session.authenticator.readOnly {
			value = "on"
		}
		return postgresTextResult("transaction_read_only", value), true, nil
	case "show standard_conforming_strings":
		return postgresTextResult("standard_conforming_strings", "on"), true, nil
	case "show client_encoding":
		return postgresTextResult("client_encoding", "UTF8"), true, nil
	case "show timezone":
		return postgresTextResult("TimeZone", "UTC"), true, nil
	case "show search_path":
		return postgresTextResult("search_path", `"$user", public`), true, nil
	case "discard all":
		return pgwire.Result{CommandTag: "DISCARD ALL"}, true, nil
	}
	if strings.HasPrefix(normalized, "set ") {
		return pgwire.Result{CommandTag: "SET"}, true, nil
	}
	return pgwire.Result{}, false, nil
}

func (session *postgresSession) catalogQuery(source string, parameters []pgwire.Parameter, describe bool) (pgwire.Result, bool, error) {
	if !isPostgresCatalogQuery(source) {
		return pgwire.Result{}, false, nil
	}
	catalog, err := session.authenticator.engine.postgresCatalogSnapshot(session.authenticator.database)
	if err != nil {
		return pgwire.Result{}, true, postgresError(err)
	}
	if err := session.attachNodeDatabases(&catalog); err != nil {
		return pgwire.Result{}, true, err
	}
	catalog.user = session.authenticator.user
	source, err = bindFunctionCatalogParameters(source, parameters, describe)
	if err != nil {
		return pgwire.Result{}, true, err
	}
	return executePostgresCatalogQuery(source, catalog)
}

func (session *postgresSession) catalogQueryForTransaction(
	transaction *Transaction,
	source string,
	parameters []pgwire.Parameter,
	describe bool,
) (pgwire.Result, bool, error) {
	if !isPostgresCatalogQuery(source) {
		return pgwire.Result{}, false, nil
	}
	catalog, err := transaction.postgresCatalogSnapshot(session.authenticator.database)
	if err != nil {
		return pgwire.Result{}, true, postgresError(err)
	}
	if err := session.attachNodeDatabases(&catalog); err != nil {
		return pgwire.Result{}, true, err
	}
	catalog.user = session.authenticator.user
	source, err = bindFunctionCatalogParameters(source, parameters, describe)
	if err != nil {
		return pgwire.Result{}, true, err
	}
	return executePostgresCatalogQuery(source, catalog)
}

func (session *postgresSession) describeMaintenance(
	source string,
	parameters []pgwire.Parameter,
) ([]pgwire.Column, error) {
	if len(parameters) != 0 && !isPostgresCatalogQuery(source) {
		return nil, pgwire.NewError("42601", "KitDB maintenance statement does not accept parameters")
	}
	if result, handled, err := session.compatibilityQuery(source); handled {
		if err != nil {
			return nil, err
		}
		return append([]pgwire.Column(nil), result.Columns...), nil
	}
	if result, handled, err := session.maintenanceCatalogQuery(source); handled {
		if err != nil {
			return nil, err
		}
		return append([]pgwire.Column(nil), result.Columns...), nil
	}
	normalized := normalizePostgresSQL(source)
	if begin, _, err := parsePostgresBegin(normalized); begin {
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
	if normalized == "commit" || normalized == "rollback" ||
		strings.HasPrefix(normalized, "set ") || normalized == "discard all" {
		return nil, nil
	}
	return nil, pgwire.NewError(
		"3D000",
		"KitDB maintenance database has no user tables; reconnect with a database from pg_database",
	)
}

func (session *postgresSession) executeMaintenance(
	ctx context.Context,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, error) {
	if err := ctx.Err(); err != nil {
		return pgwire.Result{}, pgwire.NewError("57014", "KitDB query canceled or timed out")
	}
	if session.node != nil && session.node.trace != nil {
		session.node.trace(session.authenticator.database, source, len(parameters))
	}
	normalized := normalizePostgresSQL(source)
	if begin, _, err := parsePostgresBegin(normalized); begin {
		if err != nil {
			return pgwire.Result{}, err
		}
		if len(parameters) != 0 {
			return pgwire.Result{}, pgwire.NewError("42601", "BEGIN does not accept parameters")
		}
		session.mu.Lock()
		session.maintenanceTransaction = true
		session.failed = false
		session.mu.Unlock()
		return pgwire.Result{CommandTag: "BEGIN"}, nil
	}
	switch normalized {
	case "rollback", "rollback transaction", "rollback work":
		if len(parameters) != 0 {
			return pgwire.Result{}, pgwire.NewError("42601", "ROLLBACK does not accept parameters")
		}
		session.mu.Lock()
		session.maintenanceTransaction = false
		session.failed = false
		session.mu.Unlock()
		return pgwire.Result{CommandTag: "ROLLBACK"}, nil
	case "commit", "commit transaction", "commit work", "end", "end transaction", "end work":
		if len(parameters) != 0 {
			return pgwire.Result{}, pgwire.NewError("42601", "COMMIT does not accept parameters")
		}
		session.mu.Lock()
		session.maintenanceTransaction = false
		session.failed = false
		session.mu.Unlock()
		return pgwire.Result{CommandTag: "COMMIT"}, nil
	}
	if result, handled, err := session.compatibilityQuery(source); handled {
		return result, err
	}
	if result, handled, err := session.maintenanceCatalogQuery(source); handled {
		return result, err
	}
	return pgwire.Result{}, pgwire.NewError(
		"3D000",
		"KitDB maintenance database has no user tables; reconnect with a database from pg_database",
	)
}

func (session *postgresSession) maintenanceCatalogQuery(source string) (pgwire.Result, bool, error) {
	if !isPostgresCatalogQuery(source) {
		return pgwire.Result{}, false, nil
	}
	names := []string{session.authenticator.database}
	if session.node != nil {
		var err error
		names, err = session.node.Databases()
		if err != nil {
			return pgwire.Result{}, true, pgwire.NewError("58030", err.Error())
		}
	}
	return executePostgresCatalogQuery(source, postgresCatalogSnapshot{
		database:  session.authenticator.database,
		databases: names,
	})
}

func (session *postgresSession) attachNodeDatabases(catalog *postgresCatalogSnapshot) error {
	if session == nil || session.node == nil || catalog == nil {
		return nil
	}
	names, err := session.node.Databases()
	if err != nil {
		return pgwire.NewError("58030", err.Error())
	}
	catalog.databases = names
	return nil
}

func normalizePostgresSQL(source string) string {
	source = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(source), ";"))
	return strings.ToLower(strings.Join(strings.Fields(source), " "))
}

func decodePostgresParameter(parameter pgwire.Parameter) (any, error) {
	if parameter.Null {
		return nil, nil
	}
	if parameter.Format == 0 {
		text := string(parameter.Data)
		switch parameter.OID {
		case pgwire.OIDBool:
			value, err := booleanValue(text)
			if err != nil {
				return nil, pgwire.NewError("22P02", "invalid boolean parameter")
			}
			return value, nil
		case pgwire.OIDInt2, pgwire.OIDInt4, pgwire.OIDInt8:
			bits := 64
			if parameter.OID == pgwire.OIDInt2 {
				bits = 16
			} else if parameter.OID == pgwire.OIDInt4 {
				bits = 32
			}
			value, err := strconv.ParseInt(text, 10, bits)
			if err != nil {
				return nil, pgwire.NewError("22P02", "invalid integer parameter")
			}
			return value, nil
		case pgwire.OIDOID:
			value, err := strconv.ParseUint(text, 10, 32)
			if err != nil {
				return nil, pgwire.NewError("22P02", "invalid oid parameter")
			}
			return uint32(value), nil
		case pgwire.OIDFloat4, pgwire.OIDFloat8:
			value, err := strconv.ParseFloat(text, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, pgwire.NewError("22P02", "invalid floating-point parameter")
			}
			return value, nil
		case pgwire.OIDBytea:
			if strings.HasPrefix(text, `\x`) {
				decoded, err := hex.DecodeString(text[2:])
				if err != nil {
					return nil, pgwire.NewError("22P02", "invalid bytea parameter")
				}
				return decoded, nil
			}
			return append([]byte(nil), parameter.Data...), nil
		case pgwire.OIDUUID:
			canonical, err := kitdbsql.CanonicalUUID(text)
			if err != nil {
				return nil, pgwire.NewError("22P02", "invalid UUID parameter")
			}
			return canonical, nil
		default:
			return text, nil
		}
	}
	switch parameter.OID {
	case pgwire.OIDBool:
		if len(parameter.Data) != 1 {
			return nil, pgwire.NewError("22P03", "invalid binary boolean parameter")
		}
		return parameter.Data[0] != 0, nil
	case pgwire.OIDInt2:
		if len(parameter.Data) != 2 {
			return nil, pgwire.NewError("22P03", "invalid binary int2 parameter")
		}
		return int64(int16(binary.BigEndian.Uint16(parameter.Data))), nil
	case pgwire.OIDInt4:
		if len(parameter.Data) != 4 {
			return nil, pgwire.NewError("22P03", "invalid binary int4 parameter")
		}
		return int64(int32(binary.BigEndian.Uint32(parameter.Data))), nil
	case pgwire.OIDInt8:
		if len(parameter.Data) != 8 {
			return nil, pgwire.NewError("22P03", "invalid binary int8 parameter")
		}
		return int64(binary.BigEndian.Uint64(parameter.Data)), nil
	case pgwire.OIDFloat4:
		if len(parameter.Data) != 4 {
			return nil, pgwire.NewError("22P03", "invalid binary float4 parameter")
		}
		return float64(math.Float32frombits(binary.BigEndian.Uint32(parameter.Data))), nil
	case pgwire.OIDFloat8:
		if len(parameter.Data) != 8 {
			return nil, pgwire.NewError("22P03", "invalid binary float8 parameter")
		}
		return math.Float64frombits(binary.BigEndian.Uint64(parameter.Data)), nil
	case pgwire.OIDBytea:
		return append([]byte(nil), parameter.Data...), nil
	case pgwire.OIDNumeric:
		text, err := pgwire.DecodeNumericBinary(parameter.Data)
		if err != nil {
			return nil, pgwire.NewError("22P03", "invalid binary numeric parameter")
		}
		return text, nil
	case pgwire.OIDDate:
		text, err := pgwire.DecodeDateBinary(parameter.Data)
		if err != nil {
			return nil, pgwire.NewError("22P03", "invalid binary date parameter")
		}
		return text, nil
	case pgwire.OIDTime:
		text, err := pgwire.DecodeTimeBinary(parameter.Data)
		if err != nil {
			return nil, pgwire.NewError("22P03", "invalid binary time parameter")
		}
		return text, nil
	case pgwire.OIDTimestamp, pgwire.OIDTimestampTZ:
		text, err := pgwire.DecodeTimestampBinary(parameter.Data, parameter.OID == pgwire.OIDTimestampTZ)
		if err != nil {
			return nil, pgwire.NewError("22P03", "invalid binary timestamp parameter")
		}
		return text, nil
	case pgwire.OIDInterval:
		text, err := pgwire.DecodeIntervalBinary(parameter.Data)
		if err != nil {
			return nil, pgwire.NewError("22P03", "invalid binary interval parameter")
		}
		return text, nil
	case pgwire.OIDUUID:
		text, err := pgwire.DecodeUUIDBinary(parameter.Data)
		if err != nil {
			return nil, pgwire.NewError("22P03", "invalid binary UUID parameter")
		}
		return text, nil
	case pgwire.OIDText, pgwire.OIDVarchar, pgwire.OIDBPChar, pgwire.OIDJSON, pgwire.OIDJSONB, 0:
		return string(parameter.Data), nil
	default:
		return nil, pgwire.NewError("0A000", "unsupported binary PostgreSQL parameter type")
	}
}

func postgresResult(result Result) pgwire.Result {
	converted := pgwire.Result{
		Columns: make([]pgwire.Column, len(result.Columns)),
		Rows:    make([][]pgwire.Field, len(result.Rows)), CommandTag: result.CommandTag,
	}
	for index, column := range result.Columns {
		converted.Columns[index] = postgresColumn(column)
	}
	for rowIndex, row := range result.Rows {
		converted.Rows[rowIndex] = make([]pgwire.Field, len(row))
		for columnIndex, item := range row {
			column := Column{Kind: "text"}
			if columnIndex < len(result.Columns) {
				column = result.Columns[columnIndex]
			}
			converted.Rows[rowIndex][columnIndex] = postgresField(item, column)
		}
	}
	if converted.CommandTag == "" {
		converted.CommandTag = "OK"
	}
	return converted
}

func postgresColumn(column Column) pgwire.Column {
	result := pgwire.Column{Name: column.Name, DataTypeOID: pgwire.OIDText, DataTypeSize: -1, TypeModifier: -1}
	if postgres, found := postgresResultTypeForColumn(column); found {
		result.DataTypeOID, result.DataTypeSize = postgres.OID, postgres.Size
	}
	if column.Kind == "decimal" && column.Precision != 0 {
		result.TypeModifier = postgresNumericTypeModifier(column.Precision, column.Scale)
	}
	if column.TimePrecision != nil && (column.Kind == "time" || column.Kind == "timestamp" || column.Kind == "timestamptz") {
		result.TypeModifier = int32(*column.TimePrecision)
	}
	if column.TextLength != nil && (column.Kind == "varchar" || column.Kind == "char") {
		result.TypeModifier = postgresTextTypeModifier(*column.TextLength)
	}
	return result
}

func postgresNumericTypeModifier(precision, scale int) int32 {
	if precision < 1 || scale < 0 || scale > precision {
		return -1
	}
	return int32((precision<<16)|scale) + 4
}

func postgresTextTypeModifier(length int) int32 {
	if length < 1 || length > kitdbsql.MaximumTextLength {
		return -1
	}
	return int32(length + 4)
}

func postgresField(item any, column Column) pgwire.Field {
	if item == nil {
		return pgwire.Field{Null: true}
	}
	if column.Kind == "bool" {
		truth, _ := booleanValue(item)
		if truth {
			return pgwire.Field{Data: []byte("t")}
		}
		return pgwire.Field{Data: []byte("f")}
	}
	if column.Kind == "blob" {
		if data, ok := item.([]byte); ok {
			encoded := make([]byte, 2+hex.EncodedLen(len(data)))
			copy(encoded, `\x`)
			hex.Encode(encoded[2:], data)
			return pgwire.Field{Data: encoded}
		}
	}
	switch current := item.(type) {
	case string:
		if column.Kind == "decimal" && column.Precision != 0 {
			current = postgresDecimalText(current, column.Scale)
		}
		current = postgresTemporalText(column.Kind, current)
		return pgwire.Field{Data: []byte(current)}
	case exactTemporal:
		return pgwire.Field{Data: []byte(postgresTemporalText(column.Kind, current.text))}
	case []byte:
		return pgwire.Field{Data: bytesToPostgres(current)}
	case bool:
		return pgwire.Field{Data: []byte(strconv.FormatBool(current))}
	case time.Time:
		return pgwire.Field{Data: []byte(current.UTC().Format(time.RFC3339Nano))}
	case time.Duration:
		return pgwire.Field{Data: []byte(current.String())}
	case map[string]any, []any:
		encoded, _ := json.Marshal(current)
		return pgwire.Field{Data: encoded}
	default:
		return pgwire.Field{Data: []byte(fmt.Sprint(item))}
	}
}

func postgresTemporalText(kind, source string) string {
	switch kind {
	case "timestamp":
		return strings.Replace(source, "T", " ", 1)
	case "timestamptz":
		text := strings.Replace(source, "T", " ", 1)
		if strings.HasSuffix(text, "Z") {
			text = strings.TrimSuffix(text, "Z") + "+00"
		}
		return text
	default:
		return source
	}
}

func postgresDecimalText(source string, scale int) string {
	if scale < 0 {
		return source
	}
	negative := strings.HasPrefix(source, "-")
	unsigned := strings.TrimPrefix(source, "-")
	integer, fraction := decimalParts(unsigned)
	if len(fraction) > scale {
		return source
	}
	if scale == 0 {
		return source
	}
	text := integer + "." + fraction + strings.Repeat("0", scale-len(fraction))
	if negative {
		text = "-" + text
	}
	return text
}

func bytesToPostgres(data []byte) []byte {
	encoded := make([]byte, 2+hex.EncodedLen(len(data)))
	copy(encoded, `\x`)
	hex.Encode(encoded[2:], data)
	return encoded
}

func postgresTextResult(name, text string) pgwire.Result {
	return pgwire.Result{
		Columns: []pgwire.Column{{Name: name, DataTypeOID: pgwire.OIDText, DataTypeSize: -1}},
		Rows:    [][]pgwire.Field{{{Data: []byte(text)}}}, CommandTag: "SELECT 1",
	}
}

func postgresError(err error) error {
	if err == nil {
		return nil
	}
	var protocolError *pgwire.Error
	if errors.As(err, &protocolError) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return pgwire.NewError("57014", "KitDB query canceled or timed out")
	}
	lower := strings.ToLower(err.Error())
	code := "XX000"
	switch {
	case errors.Is(err, ErrReferentialActionUnsupported):
		code = "0A000"
	case errors.Is(err, ErrForeignKeyViolation):
		code = "23503"
	case errors.Is(err, ErrForeignKeyCheckLimit):
		code = "54000"
	case errors.Is(err, errSavepointMissing):
		code = "3B001"
	case errors.Is(err, errSavepointTransaction):
		code = "25P01"
	case errors.Is(err, errUpsertCardinality):
		code = "21000"
	case errors.Is(err, ErrTransactionConflict):
		code = "40001"
	case errors.Is(err, kitdbengine.ErrSequenceNotFound):
		code = "42P01"
	case errors.Is(err, kitdbengine.ErrSequenceLimit):
		code = "2200H"
	case strings.Contains(lower, "not defined") && strings.Contains(lower, "session"):
		code = "55000"
	case strings.Contains(lower, "no such table"):
		code = "42P01"
	case strings.Contains(lower, "already exists"):
		code = "42P07"
	case strings.Contains(lower, "no field"):
		code = "42703"
	case strings.Contains(lower, "primary key") || strings.Contains(lower, "unique constraint"):
		code = "23505"
	case strings.Contains(lower, "cannot be null"):
		code = "23502"
	case strings.Contains(lower, "expects") || strings.Contains(lower, "invalid"):
		code = "22P02"
	case strings.Contains(lower, "not enabled yet") || strings.Contains(lower, "currently supports"):
		code = "0A000"
	case strings.Contains(lower, "read-only"):
		code = "25006"
	}
	return pgwire.NewError(code, err.Error())
}

var (
	_ pgwire.Authenticator            = (*postgresAuthenticator)(nil)
	_ pgwire.Session                  = (*postgresSession)(nil)
	_ pgwire.DescribeSession          = (*postgresSession)(nil)
	_ pgwire.TransactionStatusSession = (*postgresSession)(nil)
	_ pgwire.CopyAdmissionSession     = (*postgresSession)(nil)
)
