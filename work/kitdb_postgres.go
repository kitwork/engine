package work

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

// KitDBPostgresOptions configures the local PostgreSQL wire profile. An empty
// Database serves the tenant's token-scoped KitDB node through the maintenance
// database; a non-empty Database preserves the original single-database mode.
// Database accepts either the declared storage name or its suffixless
// PostgreSQL name.
type KitDBPostgresOptions struct {
	Database                  string
	MaintenanceDatabase       string
	User                      string
	TraceQuery                func(database, source string, parameterCount int)
	MaxConnections            int
	MaxConcurrentCopies       int
	MaxConcurrentCopiesPerKey int
	MaxQueuedCopies           int
	MaxQueuedCopiesPerKey     int
	MaxMessageBytes           int
	MaxCopyBytes              int64
	IdleTimeout               time.Duration
	QueryTimeout              time.Duration
	CopyTimeout               time.Duration
	TransactionTimeout        time.Duration
	CopyMetrics               *pgwire.CopyMetrics
}

// ServeKitDBPostgres serves one tenant through PostgreSQL protocol 3.0. The
// underlying pgwire profile rejects non-loopback listeners until TLS and SCRAM
// are implemented.
func (tenant *Tenant) ServeKitDBPostgres(
	ctx context.Context,
	listener net.Listener,
	options KitDBPostgresOptions,
) error {
	if tenant == nil {
		return fmt.Errorf("kitdb postgres: tenant is nil")
	}
	if err := pgwire.ValidateCleartextListener(listener); err != nil {
		return err
	}
	user := strings.TrimSpace(options.User)
	if user == "" {
		user = "kitdb"
	}
	maintenanceDatabase := strings.TrimSpace(options.MaintenanceDatabase)
	if maintenanceDatabase == "" {
		maintenanceDatabase = "kitdb"
	}
	maintenanceDatabase = kitDBPostgresLogicalDatabaseName(maintenanceDatabase)
	authenticator := &kitDBPostgresAuthenticator{
		tenant: tenant, database: strings.TrimSpace(options.Database),
		maintenanceDatabase: maintenanceDatabase, user: user, traceQuery: options.TraceQuery,
		transactionTimeout: kitDBPostgresTransactionTimeout(options.TransactionTimeout),
	}
	if err := authenticator.restoreKitDBNodeCatalog(ctx); err != nil {
		return fmt.Errorf("kitdb postgres: restore node database catalog: %w", err)
	}
	return (pgwire.Server{
		Authenticator:             authenticator,
		MaxConnections:            options.MaxConnections,
		MaxConcurrentCopies:       options.MaxConcurrentCopies,
		MaxConcurrentCopiesPerKey: options.MaxConcurrentCopiesPerKey,
		MaxQueuedCopies:           options.MaxQueuedCopies,
		MaxQueuedCopiesPerKey:     options.MaxQueuedCopiesPerKey,
		MaxMessageBytes:           options.MaxMessageBytes,
		MaxCopyBytes:              options.MaxCopyBytes,
		IdleTimeout:               options.IdleTimeout,
		QueryTimeout:              options.QueryTimeout,
		CopyTimeout:               options.CopyTimeout,
		CopyMetrics:               options.CopyMetrics,
	}).Serve(ctx, listener)
}

type kitDBPostgresAuthenticator struct {
	tenant              *Tenant
	database            string
	maintenanceDatabase string
	user                string
	traceQuery          func(database, source string, parameterCount int)
	transactionTimeout  time.Duration
	sessionMu           sync.Mutex
	sessions            map[*kitDBPostgresSession]struct{}
}

type kitDBPostgresDatabase struct {
	name        string
	storageName string
	config      serveConfig
}

// kitDBPostgresLogicalDatabaseName keeps the storage suffix private to KitDB.
// PostgreSQL clients operate on logical database names, just as they do not
// include PostgreSQL's physical storage representation in a connection string.
func kitDBPostgresLogicalDatabaseName(name string) string {
	name = strings.TrimSpace(name)
	const suffix = ".kitdb"
	if len(name) > len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix) {
		return name[:len(name)-len(suffix)]
	}
	return name
}

func (authenticator *kitDBPostgresAuthenticator) databases() ([]kitDBPostgresDatabase, error) {
	entries := listServes(authenticator.tenant, "kitdb")
	databases := make([]kitDBPostgresDatabase, 0, len(entries))
	storageByName := make(map[string]string, len(entries))
	for _, entry := range entries {
		_, config, available := resolveServe(authenticator.tenant, entry.name)
		if !available || config.database == nil {
			continue
		}
		name := kitDBPostgresServeLogicalName(entry.name, config)
		if name == "" {
			return nil, fmt.Errorf("KitDB storage name %q has no PostgreSQL database name", entry.name)
		}
		if previous, found := storageByName[name]; found && previous != entry.name {
			return nil, fmt.Errorf(
				"KitDB storage names %q and %q both map to PostgreSQL database %q",
				previous,
				entry.name,
				name,
			)
		}
		storageByName[name] = entry.name
		databases = append(databases, kitDBPostgresDatabase{
			name: name, storageName: entry.name, config: config,
		})
	}
	return databases, nil
}

func kitDBPostgresServeLogicalName(storageName string, config serveConfig) string {
	if name := strings.TrimSpace(config.logicalName); name != "" {
		return name
	}
	return kitDBPostgresLogicalDatabaseName(storageName)
}

func findKitDBPostgresDatabase(
	databases []kitDBPostgresDatabase,
	requested string,
) (kitDBPostgresDatabase, bool) {
	requested = kitDBPostgresLogicalDatabaseName(requested)
	for _, database := range databases {
		if database.name == requested {
			return database, true
		}
	}
	return kitDBPostgresDatabase{}, false
}

func (authenticator *kitDBPostgresAuthenticator) Authenticate(
	ctx context.Context,
	startup pgwire.Startup,
	password string,
) (pgwire.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, pgwire.NewError("57014", "connection canceled")
	}
	if subtle.ConstantTimeCompare([]byte(startup.User()), []byte(authenticator.user)) != 1 {
		return nil, pgwire.NewError("28P01", "password authentication failed for KitDB user")
	}
	requested := strings.TrimSpace(startup.Database())
	databases, err := authenticator.databases()
	if err != nil {
		return nil, pgwire.NewError("3D000", err.Error())
	}
	if authenticator.database != "" {
		selected, found := findKitDBPostgresDatabase(databases, authenticator.database)
		if !found {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("KitDB database %q does not exist", authenticator.database))
		}
		if requested != "" && kitDBPostgresLogicalDatabaseName(requested) != selected.name {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("KitDB database %q is not served by this listener", requested))
		}
		if selected.config.engine != "kitdb" || selected.config.database == nil {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("KitDB database %q does not exist", selected.name))
		}
		if subtle.ConstantTimeCompare([]byte(password), []byte(selected.config.token)) != 1 {
			return nil, pgwire.NewError("28P01", "password authentication failed for KitDB user")
		}
		return authenticator.registerSession(&kitDBPostgresSession{
			tenant: authenticator.tenant, database: selected.config.database,
			databaseName: selected.name, storageName: selected.storageName,
			databaseNames: []string{selected.name},
			databases:     []kitDBPostgresDatabase{selected},
			user:          authenticator.user, readonly: selected.config.access != "readwrite",
			traceQuery: authenticator.traceQuery, transactionTimeout: authenticator.transactionTimeout,
		})
	}

	for _, database := range databases {
		if database.name == authenticator.maintenanceDatabase {
			return nil, pgwire.NewError(
				"3D000",
				fmt.Sprintf("KitDB database %q conflicts with the PostgreSQL maintenance database", database.storageName),
			)
		}
	}
	authorized := authenticator.authorizedDatabases(password, databases)
	if len(authorized) == 0 {
		return nil, pgwire.NewError("28P01", "password authentication failed for KitDB user")
	}
	if requested == "" {
		requested = authenticator.maintenanceDatabase
	}
	databaseNames := make([]string, 1, len(authorized)+1)
	databaseNames[0] = authenticator.maintenanceDatabase
	for _, entry := range authorized {
		databaseNames = append(databaseNames, entry.name)
	}
	if kitDBPostgresLogicalDatabaseName(requested) == authenticator.maintenanceDatabase {
		return authenticator.registerSession(&kitDBPostgresSession{
			tenant: authenticator.tenant, databaseName: authenticator.maintenanceDatabase,
			databaseNames: databaseNames, databases: authorized,
			user: authenticator.user, readonly: true, nodeMode: true,
			maintenance: true, traceQuery: authenticator.traceQuery,
			transactionTimeout: authenticator.transactionTimeout,
		})
	}
	selected, found := findKitDBPostgresDatabase(authorized, requested)
	if !found || selected.config.engine != "kitdb" || selected.config.database == nil {
		return nil, pgwire.NewError("3D000", fmt.Sprintf("KitDB database %q does not exist", requested))
	}
	return authenticator.registerSession(&kitDBPostgresSession{
		tenant: authenticator.tenant, database: selected.config.database,
		databaseName: selected.name, storageName: selected.storageName,
		databaseNames: databaseNames,
		databases:     authorized, nodeMode: true,
		user: authenticator.user, readonly: selected.config.access != "readwrite",
		traceQuery: authenticator.traceQuery, transactionTimeout: authenticator.transactionTimeout,
	})
}

func (authenticator *kitDBPostgresAuthenticator) registerSession(
	session *kitDBPostgresSession,
) (pgwire.Session, error) {
	if session == nil {
		return nil, pgwire.NewError("08006", "KitDB PostgreSQL session is unavailable")
	}
	authenticator.sessionMu.Lock()
	defer authenticator.sessionMu.Unlock()
	if session.nodeMode && !session.maintenance {
		_, config, found := resolveServe(authenticator.tenant, session.storageName)
		if !found || config.database == nil || config.engine != "kitdb" {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("KitDB database %q does not exist", session.databaseName))
		}
		if kitDBPostgresServeLogicalName(session.storageName, config) != session.databaseName {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("KitDB database %q was renamed", session.databaseName))
		}
	}
	if authenticator.sessions == nil {
		authenticator.sessions = make(map[*kitDBPostgresSession]struct{})
	}
	session.authenticator = authenticator
	authenticator.sessions[session] = struct{}{}
	return session, nil
}

func (authenticator *kitDBPostgresAuthenticator) unregisterSession(session *kitDBPostgresSession) {
	if authenticator == nil || session == nil {
		return
	}
	authenticator.sessionMu.Lock()
	delete(authenticator.sessions, session)
	authenticator.sessionMu.Unlock()
}

func (authenticator *kitDBPostgresAuthenticator) databaseSessionsLocked(name string) int {
	count := 0
	for session := range authenticator.sessions {
		if !session.maintenance && session.databaseName == name {
			count++
		}
	}
	return count
}

func (authenticator *kitDBPostgresAuthenticator) authorizedDatabases(
	password string,
	databases []kitDBPostgresDatabase,
) []kitDBPostgresDatabase {
	authorized := make([]kitDBPostgresDatabase, 0, len(databases))
	for _, database := range databases {
		if subtle.ConstantTimeCompare([]byte(password), []byte(database.config.token)) == 1 {
			authorized = append(authorized, database)
		}
	}
	return authorized
}

type kitDBPostgresSession struct {
	tenant             *Tenant
	database           *dbProxy
	databaseName       string
	storageName        string
	databaseNames      []string
	databases          []kitDBPostgresDatabase
	user               string
	readonly           bool
	maintenance        bool
	nodeMode           bool
	traceQuery         func(database, source string, parameterCount int)
	transactionTimeout time.Duration
	transactionMu      sync.Mutex
	transaction        *kitDBPostgresTransaction
	authenticator      *kitDBPostgresAuthenticator
	closeOnce          sync.Once
	closeErr           error
}

var _ pgwire.CopyAdmissionSession = (*kitDBPostgresSession)(nil)

type kitDBPostgresTransaction struct {
	mode          kitDBPostgresTransactionMode
	database      *dbProxy
	record        *kitDBRecordTransaction
	scope         *requestscope.Scope
	context       context.Context
	cancel        context.CancelFunc
	timer         *time.Timer
	requestOpen   bool
	readOnly      bool
	statements    int
	ddlSource     string
	ddlParameters []pgwire.Parameter
	failed        bool
	expired       bool
}

func (session *kitDBPostgresSession) Close() error {
	session.closeOnce.Do(func() {
		session.transactionMu.Lock()
		transaction := session.transaction
		session.transaction = nil
		session.closeErr = session.releaseKitDBPostgresTransactionLocked(transaction, false, nil)
		session.transactionMu.Unlock()
		if session.authenticator != nil {
			session.authenticator.unregisterSession(session)
		}
	})
	return session.closeErr
}

func (session *kitDBPostgresSession) TransactionStatus() byte {
	session.transactionMu.Lock()
	defer session.transactionMu.Unlock()
	if session.transaction == nil {
		return pgwire.TransactionIdle
	}
	if session.transaction.failed {
		return pgwire.TransactionFailed
	}
	return pgwire.TransactionActive
}

func (session *kitDBPostgresSession) CopyAdmission() pgwire.CopyAdmission {
	key := session.databaseName
	if session.tenant != nil && session.tenant.entity != nil {
		key = session.tenant.appID() + "\x00" + key
	}
	return pgwire.CopyAdmission{Key: key, Weight: 1}
}

func (session *kitDBPostgresSession) Execute(
	ctx context.Context,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, error) {
	if session.nodeMode && !session.maintenance {
		if _, config, found := resolveServe(session.tenant, session.storageName); !found || config.database == nil || config.engine != "kitdb" {
			return pgwire.Result{}, pgwire.NewError("3D000", fmt.Sprintf("KitDB database %q does not exist", session.databaseName))
		}
	}
	if session.traceQuery != nil {
		session.traceQuery(session.databaseName, source, len(parameters))
	}
	if result, handled, err := session.transactionQuery(ctx, source, parameters); handled {
		return result, err
	}
	return session.executeAutocommit(ctx, source, parameters)
}

func (session *kitDBPostgresSession) executeAutocommit(
	ctx context.Context,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, error) {
	if result, handled, err := session.compatibilityQuery(source); handled {
		return result, err
	}
	if result, handled, err := session.catalogQuery(source, parameters); handled {
		return result, err
	}
	bindings, err := kitDBPostgresBindings(parameters)
	if err != nil {
		return pgwire.Result{}, err
	}
	statement, parseErr := parseKitSQL(source, bindings)
	if parseErr == nil {
		switch statement.kind {
		case "create_database":
			return session.executeKitDBPostgresCreateDatabase(ctx, statement.createDatabase)
		case "drop_database":
			return session.executeKitDBPostgresDropDatabase(ctx, statement.dropDatabase)
		case "alter_rename_database":
			return session.executeKitDBPostgresRenameDatabase(ctx, statement.renameDatabase)
		}
	}
	if session.maintenance || session.database == nil {
		if parseErr != nil {
			return pgwire.Result{}, kitDBPostgresError(parseErr)
		}
		return pgwire.Result{}, pgwire.NewError(
			"3D000",
			"KitDB maintenance database has no user structs; reconnect with a database from pg_database",
		)
	}
	if !session.tenant.beginRequest() {
		return pgwire.Result{}, pgwire.NewError("57P01", "KitDB tenant is shutting down")
	}
	defer session.tenant.endRequest()

	lease, err := session.tenant.generationLease()
	if err != nil {
		return pgwire.Result{}, pgwire.NewError("57P01", err.Error())
	}
	request := (&http.Request{}).WithContext(ctx)
	scope := requestscope.New(session.tenant, nil, request)
	if lease != nil && !scope.AddCleanup(lease.Release) {
		lease.Release()
		scope.Close()
		return pgwire.Result{}, pgwire.NewError("57P01", "KitDB request scope is unavailable")
	}
	defer scope.Close()

	result, err := executeKitDBRemoteSQL(ctx, scope, session.database, source, bindings, session.readonly)
	if err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	return kitDBPostgresResult(source, result), nil
}

func (session *kitDBPostgresSession) transactionQuery(
	ctx context.Context,
	source string,
	parameters []pgwire.Parameter,
) (pgwire.Result, bool, error) {
	return session.executeKitDBPostgresTransactionQuery(ctx, source, parameters)
}

func cloneKitDBPostgresParameters(parameters []pgwire.Parameter) []pgwire.Parameter {
	cloned := make([]pgwire.Parameter, len(parameters))
	for index, parameter := range parameters {
		cloned[index] = parameter
		cloned[index].Data = append([]byte(nil), parameter.Data...)
	}
	return cloned
}

func (session *kitDBPostgresSession) compatibilityQuery(source string) (pgwire.Result, bool, error) {
	normalized := normalizeKitDBPostgresSQL(source)
	switch normalized {
	case "select version()", "select pg_catalog.version()":
		return kitDBPostgresTextResult("version", "KitDB PostgreSQL wire profile 0.1"), true, nil
	case "select current_database()", "select pg_catalog.current_database()":
		return kitDBPostgresTextResult("current_database", session.databaseName), true, nil
	case "select current_schema()", "select pg_catalog.current_schema()":
		return kitDBPostgresTextResult("current_schema", "public"), true, nil
	case "select current_user", "select session_user", "select current_user, session_user":
		if normalized == "select current_user, session_user" {
			return pgwire.Result{
				Columns: []pgwire.Column{
					{Name: "current_user", DataTypeOID: pgwire.OIDText, DataTypeSize: -1},
					{Name: "session_user", DataTypeOID: pgwire.OIDText, DataTypeSize: -1},
				},
				Rows:       [][]pgwire.Field{{{Data: []byte(session.user)}, {Data: []byte(session.user)}}},
				CommandTag: "SELECT 1",
			}, true, nil
		}
		return kitDBPostgresTextResult(strings.TrimPrefix(normalized, "select "), session.user), true, nil
	case "show server_version":
		return kitDBPostgresTextResult("server_version", "16.0"), true, nil
	case "show server_version_num":
		return kitDBPostgresTextResult("server_version_num", "160000"), true, nil
	case "show transaction_isolation":
		return kitDBPostgresTextResult("transaction_isolation", "repeatable read"), true, nil
	case "show transaction_read_only":
		value := "off"
		if session.readonly {
			value = "on"
		}
		return kitDBPostgresTextResult("transaction_read_only", value), true, nil
	case "show standard_conforming_strings":
		return kitDBPostgresTextResult("standard_conforming_strings", "on"), true, nil
	case "show client_encoding":
		return kitDBPostgresTextResult("client_encoding", "UTF8"), true, nil
	case "show timezone":
		return kitDBPostgresTextResult("TimeZone", "UTC"), true, nil
	case "show search_path":
		return kitDBPostgresTextResult("search_path", `"$user", public`), true, nil
	case "discard all":
		return pgwire.Result{CommandTag: "DISCARD ALL"}, true, nil
	}
	if strings.HasPrefix(normalized, "set ") {
		if kitDBPostgresSessionSetting(normalized) {
			return pgwire.Result{CommandTag: "SET"}, true, nil
		}
		return pgwire.Result{}, true, pgwire.NewError("0A000", "unsupported PostgreSQL session setting")
	}
	if strings.HasPrefix(normalized, "select pg_catalog.set_config(") ||
		strings.HasPrefix(normalized, "select set_config(") {
		return kitDBPostgresTextResult("set_config", ""), true, nil
	}
	if strings.HasPrefix(normalized, "select current_setting('server_version_num')") {
		return kitDBPostgresTextResult("current_setting", "160000"), true, nil
	}
	if strings.HasPrefix(normalized, "select current_setting('server_version')") {
		return kitDBPostgresTextResult("current_setting", "16.0"), true, nil
	}
	return pgwire.Result{}, false, nil
}

func normalizeKitDBPostgresSQL(source string) string {
	source = strings.TrimSpace(source)
	source = strings.TrimSuffix(source, ";")
	return strings.ToLower(strings.Join(strings.Fields(source), " "))
}

func kitDBPostgresSessionSetting(normalized string) bool {
	for _, name := range []string{
		"application_name", "client_encoding", "datestyle", "extra_float_digits",
		"standard_conforming_strings", "timezone",
	} {
		if strings.HasPrefix(normalized, "set "+name+" ") ||
			strings.HasPrefix(normalized, "set "+name+"=") {
			return true
		}
	}
	return false
}

func kitDBPostgresTextResult(name, text string) pgwire.Result {
	return pgwire.Result{
		Columns:    []pgwire.Column{{Name: name, DataTypeOID: pgwire.OIDText, DataTypeSize: -1}},
		Rows:       [][]pgwire.Field{{{Data: []byte(text)}}},
		CommandTag: "SELECT 1",
	}
}

func kitDBPostgresParameter(parameter pgwire.Parameter) (value.Value, error) {
	if parameter.Null {
		return value.NULL, nil
	}
	if parameter.Format == 0 {
		text := string(parameter.Data)
		switch parameter.OID {
		case pgwire.OIDBool:
			parsed, err := strconv.ParseBool(text)
			if err != nil {
				if text == "t" || text == "1" {
					return value.TRUE, nil
				}
				if text == "f" || text == "0" {
					return value.FALSE, nil
				}
				return value.Value{}, pgwire.NewError("22P02", "invalid boolean parameter")
			}
			return value.New(parsed), nil
		case pgwire.OIDInt2, pgwire.OIDInt4, pgwire.OIDInt8:
			bits := 64
			if parameter.OID == pgwire.OIDInt2 {
				bits = 16
			} else if parameter.OID == pgwire.OIDInt4 {
				bits = 32
			}
			parsed, err := strconv.ParseInt(text, 10, bits)
			if err != nil {
				return value.Value{}, pgwire.NewError("22P02", "invalid integer parameter")
			}
			return value.New(parsed), nil
		case pgwire.OIDFloat4, pgwire.OIDFloat8:
			parsed, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return value.Value{}, pgwire.NewError("22P02", "invalid floating-point parameter")
			}
			return value.New(parsed), nil
		case pgwire.OIDBytea:
			if strings.HasPrefix(text, `\x`) {
				decoded, err := hex.DecodeString(text[2:])
				if err != nil {
					return value.Value{}, pgwire.NewError("22P02", "invalid bytea parameter")
				}
				return value.New(decoded), nil
			}
			return value.New(parameter.Data), nil
		default:
			return value.New(text), nil
		}
	}

	switch parameter.OID {
	case pgwire.OIDBool:
		if len(parameter.Data) != 1 {
			return value.Value{}, pgwire.NewError("22P03", "invalid binary boolean parameter")
		}
		return value.New(parameter.Data[0] != 0), nil
	case pgwire.OIDInt2:
		if len(parameter.Data) != 2 {
			return value.Value{}, pgwire.NewError("22P03", "invalid binary int2 parameter")
		}
		return value.New(int16(binary.BigEndian.Uint16(parameter.Data))), nil
	case pgwire.OIDInt4:
		if len(parameter.Data) != 4 {
			return value.Value{}, pgwire.NewError("22P03", "invalid binary int4 parameter")
		}
		return value.New(int32(binary.BigEndian.Uint32(parameter.Data))), nil
	case pgwire.OIDInt8:
		if len(parameter.Data) != 8 {
			return value.Value{}, pgwire.NewError("22P03", "invalid binary int8 parameter")
		}
		return value.New(int64(binary.BigEndian.Uint64(parameter.Data))), nil
	case pgwire.OIDFloat4:
		if len(parameter.Data) != 4 {
			return value.Value{}, pgwire.NewError("22P03", "invalid binary float4 parameter")
		}
		return value.New(float64(math.Float32frombits(binary.BigEndian.Uint32(parameter.Data)))), nil
	case pgwire.OIDFloat8:
		if len(parameter.Data) != 8 {
			return value.Value{}, pgwire.NewError("22P03", "invalid binary float8 parameter")
		}
		return value.New(math.Float64frombits(binary.BigEndian.Uint64(parameter.Data))), nil
	case pgwire.OIDText, pgwire.OIDJSON, pgwire.OIDJSONB, pgwire.OIDNumeric:
		return value.New(string(parameter.Data)), nil
	case pgwire.OIDBytea:
		return value.New(append([]byte(nil), parameter.Data...)), nil
	default:
		return value.Value{}, pgwire.NewError("0A000", "unsupported binary PostgreSQL parameter type")
	}
}

func kitDBPostgresResult(source string, result kitDBRemoteResult) pgwire.Result {
	converted := pgwire.Result{
		Columns:    make([]pgwire.Column, len(result.columns)),
		Rows:       make([][]pgwire.Field, len(result.rows)),
		CommandTag: kitDBPostgresCommandTag(source, result),
	}
	for index, column := range result.columns {
		converted.Columns[index] = kitDBPostgresColumn(column)
	}
	for rowIndex, row := range result.rows {
		converted.Rows[rowIndex] = make([]pgwire.Field, len(row))
		for columnIndex, item := range row {
			kind := "text"
			if columnIndex < len(result.columns) {
				kind = result.columns[columnIndex].kind
			}
			converted.Rows[rowIndex][columnIndex] = kitDBPostgresField(item, kind)
		}
	}
	return converted
}

func kitDBPostgresColumn(column kitDBRemoteColumn) pgwire.Column {
	result := pgwire.Column{Name: column.name, DataTypeOID: pgwire.OIDText, DataTypeSize: -1}
	// Kitwork struct() still authors Schema IR v2 UUID aliases. Do not advertise
	// native UUID until that adapter can persist and enforce the v7 exact marker.
	if column.kind == "uuid" {
		return result
	}
	if typeInfo, found := kitdbsql.LookupKind(column.kind); found {
		result.DataTypeOID = typeInfo.Result.OID
		result.DataTypeSize = typeInfo.Result.Size
	}
	return result
}

func kitDBPostgresField(item value.Value, kind string) pgwire.Field {
	if item.IsNil() {
		return pgwire.Field{Null: true}
	}
	if kind == "bool" {
		truth := item.K == value.Bool && item.N != 0
		if item.K == value.Number {
			truth = item.N != 0
		}
		if truth {
			return pgwire.Field{Data: []byte("t")}
		}
		return pgwire.Field{Data: []byte("f")}
	}
	if kind == "blob" || item.K == value.Bytes {
		data := item.Bytes()
		encoded := make([]byte, 2+hex.EncodedLen(len(data)))
		copy(encoded, `\x`)
		hex.Encode(encoded[2:], data)
		return pgwire.Field{Data: encoded}
	}
	return pgwire.Field{Data: []byte(item.Text())}
}

func kitDBPostgresCommandTag(source string, result kitDBRemoteResult) string {
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(source)))
	if len(fields) == 0 {
		return "OK"
	}
	switch fields[0] {
	case "SELECT", "EXPLAIN", "PRAGMA", "SHOW":
		return fmt.Sprintf("SELECT %d", len(result.rows))
	case "INSERT":
		return fmt.Sprintf("INSERT 0 %d", result.affected)
	case "UPDATE":
		return fmt.Sprintf("UPDATE %d", result.affected)
	case "DELETE":
		return fmt.Sprintf("DELETE %d", result.affected)
	case "CREATE":
		if len(fields) > 1 && fields[1] == "DATABASE" {
			return "CREATE DATABASE"
		}
		if len(fields) > 1 && fields[1] == "INDEX" {
			return "CREATE INDEX"
		}
		return "CREATE TABLE"
	case "ALTER":
		if len(fields) > 1 && fields[1] == "DATABASE" {
			return "ALTER DATABASE"
		}
		if len(fields) > 1 && fields[1] == "INDEX" {
			return "ALTER INDEX"
		}
		return "ALTER TABLE"
	case "DROP":
		if len(fields) > 1 && fields[1] == "INDEX" {
			return "DROP INDEX"
		}
		return "DROP TABLE"
	default:
		return fields[0]
	}
}

func kitDBPostgresError(err error) error {
	if err == nil {
		return nil
	}
	var protocolErr *pgwire.Error
	if errors.As(err, &protocolErr) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return pgwire.NewError("57014", "KitDB query canceled or timed out")
	}
	message := err.Error()
	lower := strings.ToLower(message)
	code := "XX000"
	switch {
	case strings.Contains(lower, "transaction conflict"):
		code = "40001"
	case strings.Contains(lower, "record transaction exceeds") ||
		strings.Contains(lower, "transaction exceeds"):
		code = "54000"
	case strings.Contains(lower, "read-only"):
		code = "25006"
	case strings.Contains(lower, "no such table") || strings.Contains(lower, "no struct"):
		code = "42P01"
	case strings.Contains(lower, "no such index"):
		code = "42704"
	case strings.Contains(lower, "no such constraint"):
		code = "42704"
	case strings.Contains(lower, "constraint") && strings.Contains(lower, "already exists"):
		code = "42710"
	case strings.Contains(lower, "table") && strings.Contains(lower, "already exists"):
		code = "42P07"
	case strings.Contains(lower, "no such column") || strings.Contains(lower, "has no field"):
		code = "42703"
	case strings.Contains(lower, "depends on") || strings.Contains(lower, "because constraint"):
		code = "2BP01"
	case strings.Contains(lower, "source-declared"):
		code = "0A000"
	case strings.Contains(lower, "index") && strings.Contains(lower, "already exists"):
		code = "42P07"
	case strings.Contains(lower, "unique"):
		code = "23505"
	case strings.Contains(lower, "foreign key") || strings.Contains(lower, "reference"):
		code = "23503"
	case strings.Contains(lower, "not null"):
		code = "23502"
	case strings.Contains(lower, "check constraint") || strings.Contains(lower, "choice"):
		code = "23514"
	case strings.Contains(lower, "expected") || strings.Contains(lower, "unexpected") ||
		strings.Contains(lower, "unterminated") || strings.Contains(lower, "unsupported character") ||
		strings.Contains(lower, "multiple statements") || strings.Contains(lower, "only select"):
		code = "42601"
	case strings.Contains(lower, "not supported"):
		code = "0A000"
	}
	return pgwire.NewError(code, message)
}
