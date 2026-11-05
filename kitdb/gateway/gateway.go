// Package gateway serves one database made of SQLite files on the PostgreSQL wire port, read-only
// (KITWORK_KITDB.md question 13.5). A Session is a pgwire.Session on one SQLite connection of its own: the main
// database in memory, every schema file of the database ATTACHed read-only (mode=ro) under its schema name, public
// first, then PRAGMA query_only=1, so SQLite itself refuses any write.
//
// What a user may read is decided before a statement runs, from the statement's own program: the session asks SQLite
// to EXPLAIN it with the same parameters and reads every opcode that opens a b-tree. OpenRead and ReopenIdx name the
// database (p3, mapped through PRAGMA database_list) and the root page (p2, mapped through that schema's
// sqlite_schema; an index maps to its table). Views, subqueries and common table expressions are already expanded in
// that program, so the tables a view reads are the tables checked. VOpen (a virtual table or a table-valued function)
// is refused except json_each and json_tree. An opcode that writes (OpenWrite, a write transaction, Clear, …) is
// refused as a write. Access.CanSelect answers for each table the program opens.
package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
	sqlite "modernc.org/sqlite" // also the "sqlite" database/sql driver
)

// Target is one database on the port: its schema files, public first.
type Target struct {
	Name    string            // as the client named it ("demo.kitwork.io/application")
	Schemas map[string]string // schema name → SQLite file ("public" → …/application/kitwork.db, "billing" → …/billing.db)
}

// Access is what the session's user may read; the gateway asks it for every table a statement opens.
type Access interface {
	CanSelect(ctx context.Context, schema, table string) (bool, error)
	// Command runs an account statement (handled=false when it is not one); nil for no account statements.
	Command(ctx context.Context, statement string) (tag string, handled bool, err error)
	// Current reports whether the session may go on (false once its user's stamp changed: the session ends).
	Current(ctx context.Context) (bool, error)
}

// Columns gives a column's declared kind where the file alone does not say it: a decimal's precision and scale (the
// file stores the integer × 10^scale), a boolean stored 0 or 1. The kinds are the engine's own ("decimal", "bool",
// "json", "datetime", "date", "kitid", "text", …); a kind the gateway does not know leaves the value its own type.
// nil knows none.
type Columns interface {
	Column(schema, table, column string) (kind string, precision, scale int, found bool)
}

// Options bounds what one statement may return, names the session's user and receives what fails.
type Options struct {
	MaxRows  int   // 10000 when zero or less
	MaxBytes int64 // 10 << 20 when zero or less
	// MaxTemporaryBytes is the most bytes the process's SQLite temporary files may hold while a statement of the
	// session runs (temporary.go), PostgreSQL's temp_file_limit: 2 << 30 when zero or less.
	MaxTemporaryBytes int64
	// User is the session's user: what current_user answers and the owner the catalog shows.
	User string
	// Log, when set, is told each statement that fails and its error, as PostgreSQL logs a failed statement.
	Log func(statement string, err error)
	// Search answers SEARCH (search.go); nil when the port has no full-text index.
	Search SearchProvider
}

const (
	// DefaultMaxRows is the most rows a statement returns when Options.MaxRows is not set.
	DefaultMaxRows = 10000
	// DefaultMaxBytes is the most bytes of values a statement returns when Options.MaxBytes is not set.
	DefaultMaxBytes = int64(10 << 20)
	// MaxSchemas is how many files SQLite attaches to one connection (SQLITE_MAX_ATTACHED).
	MaxSchemas = 10

	// ServerVersion is what SHOW server_version answers.
	ServerVersion = "16.0 (Kitwork SQLite)"
)

var plainIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// SQLite's limit identifiers (sqlite3_limit) the session lowers.
const (
	limitLength   = 0 // SQLITE_LIMIT_LENGTH: the largest string or blob
	limitAttached = 7 // SQLITE_LIMIT_ATTACHED: how many files may be attached
)

// busyTimeout is how long a statement waits for a lock another connection holds.
const busyTimeout = 5 * time.Second

// Session is one client's read-only connection to a Target. It implements pgwire.Session, pgwire.DescribeSession
// and pgwire.TransactionStatusSession.
type Session struct {
	mutex   sync.Mutex
	target  Target
	access  Access
	columns Columns
	options Options

	database   *sql.DB
	connection *sql.Conn

	// order is the schema names in the order they were attached; databases maps SQLite's database index (p3 of
	// an opcode) to a schema name; schemas holds each schema's root pages.
	order     []string
	databases map[int64]string
	schemas   map[string]*schemaMap
	// tableFunctions maps the address SQLite prints in p4 of VOpen for json_each and json_tree on this connection.
	tableFunctions map[string]string
	sqliteVersion  string

	// catalogVersions is each schema's schema_version when the catalog (catalog.go) was last filled; nil before.
	catalogVersions []int64
	// oids keeps the OID the catalog gave each table, index and constraint, for the session's life.
	oids map[string]uint32
	// tables is every table and view of the schemas, by lower-case schema then name, read with the catalog for the
	// decimal rewrite (decimal.go); decimalColumns counts their decimal columns of a positive scale.
	tables         map[string]map[string]*decimalTable
	decimalColumns int

	transaction byte  // pgwire.TransactionIdle, TransactionActive or TransactionFailed
	ended       error // set once Access.Current said the session may not go on
	closed      bool

	statementMutex  sync.Mutex
	cancelStatement context.CancelFunc
}

var (
	_ pgwire.Session                  = (*Session)(nil)
	_ pgwire.DescribeSession          = (*Session)(nil)
	_ pgwire.TransactionStatusSession = (*Session)(nil)
	_ pgwire.QueryAdmissionSession    = (*Session)(nil)
)

// QueryAdmission is the share of the port's statements running at once that this session waits in: its user's, as
// PostgreSQL bounds a role. Every session waited in one share before (04/11/2026), so one account's long statements,
// a recursive CTE that reads no table and runs until the port's QueryTimeout, held the port for every other account.
func (session *Session) QueryAdmission() pgwire.QueryAdmission {
	return pgwire.QueryAdmission{Key: "user " + session.options.User, Weight: 1}
}

// Open attaches every schema file of target read-only to one new SQLite connection. A missing file is an error naming
// the database; nothing is ever created.
func Open(ctx context.Context, target Target, access Access, columns Columns, options Options) (*Session, error) {
	if access == nil {
		return nil, fmt.Errorf("gateway: access is nil")
	}
	if options.MaxRows <= 0 {
		options.MaxRows = DefaultMaxRows
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = DefaultMaxBytes
	}
	if options.MaxTemporaryBytes <= 0 {
		options.MaxTemporaryBytes = DefaultMaxTemporaryBytes
	}
	order, err := schemaOrder(target)
	if err != nil {
		return nil, err
	}
	files := make(map[string]string, len(order))
	for _, name := range order {
		path, err := filepath.Abs(target.Schemas[name])
		if err != nil {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("database %q: schema %q has no usable file", target.Name, name))
		}
		information, err := os.Stat(path)
		if err != nil || !information.Mode().IsRegular() {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("database %q does not exist: its schema %q has no file", target.Name, name))
		}
		files[name] = path
	}

	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("gateway: open SQLite: %w", err)
	}
	database.SetMaxOpenConns(1)
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("gateway: open SQLite: %w", err)
	}
	session := &Session{
		target:         target,
		access:         access,
		columns:        columns,
		options:        options,
		database:       database,
		connection:     connection,
		order:          order,
		databases:      map[int64]string{},
		schemas:        map[string]*schemaMap{},
		tableFunctions: map[string]string{},
		oids:           map[string]uint32{},
		transaction:    pgwire.TransactionIdle,
	}
	if err := session.attach(ctx, files); err != nil {
		_ = session.release()
		return nil, err
	}
	return session, nil
}

// schemaOrder checks the schema names of target and orders them: public first, then by name.
func schemaOrder(target Target) ([]string, error) {
	if len(target.Schemas) == 0 {
		return nil, pgwire.NewError("3D000", fmt.Sprintf("database %q does not exist: it has no schema file", target.Name))
	}
	if len(target.Schemas) > MaxSchemas {
		return nil, pgwire.NewError("54000", fmt.Sprintf("database %q has %d schemas: SQLite attaches at most %d files to one connection",
			target.Name, len(target.Schemas), MaxSchemas))
	}
	order := make([]string, 0, len(target.Schemas))
	seen := map[string]string{}
	for name := range target.Schemas {
		if !plainIdentifier.MatchString(name) {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("database %q: schema name %q is not a plain identifier", target.Name, name))
		}
		folded := strings.ToLower(name)
		if folded == "main" || folded == "temp" {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("database %q: schema name %q is reserved by SQLite", target.Name, name))
		}
		if other, exists := seen[folded]; exists {
			return nil, pgwire.NewError("3D000", fmt.Sprintf("database %q: schemas %q and %q differ only by case", target.Name, other, name))
		}
		seen[folded] = name
		order = append(order, name)
	}
	sort.Slice(order, func(left, right int) bool {
		if (order[left] == "public") != (order[right] == "public") {
			return order[left] == "public"
		}
		return order[left] < order[right]
	})
	return order, nil
}

// fileURI is the SQLite URI that opens path read-only; '%', '?' and '#' are escaped so the path stays a path.
func fileURI(path string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(path))
	return "file:" + escaped + "?mode=ro"
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (session *Session) attach(ctx context.Context, files map[string]string) error {
	// A file another connection holds a lock on (the engine writing it, a reader recovering its WAL index) is waited
	// for, as PostgreSQL waits for a lock, not refused at once.
	if _, err := session.connection.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", busyTimeout.Milliseconds())); err != nil {
		return fmt.Errorf("gateway: busy timeout: %w", err)
	}
	for _, name := range session.order {
		uri := fileURI(files[name])
		if _, err := session.connection.ExecContext(ctx, "ATTACH ? AS "+quoteIdentifier(name), uri); err != nil {
			// SQLite's message names the file: the client is told the schema, never the path.
			message := strings.NewReplacer(uri, "its file", files[name], "its file").Replace(cleanMessage(err.Error()))
			return pgwire.NewError("3D000", fmt.Sprintf("database %q: schema %q cannot be opened read-only: %s",
				session.target.Name, name, message))
		}
		session.schemas[name] = &schemaMap{}
	}
	// No statement can attach another file.
	if _, err := sqlite.Limit(session.connection, limitAttached, len(session.order)); err != nil {
		return fmt.Errorf("gateway: SQLite attach limit: %w", err)
	}
	// The catalog's TEMP tables, filled before the first statement (catalog.go).
	if err := session.createCatalog(ctx); err != nil {
		return err
	}
	// Views and triggers in the files may not call functions or virtual tables with side effects; then nothing
	// the session runs may write.
	for _, pragma := range []string{"PRAGMA trusted_schema=0", "PRAGMA query_only=1"} {
		if _, err := session.connection.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("gateway: %s: %w", pragma, err)
		}
	}
	rows, err := session.connection.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return fmt.Errorf("gateway: database list: %w", err)
	}
	for rows.Next() {
		var index int64
		var name, file string
		if err := rows.Scan(&index, &name, &file); err != nil {
			_ = rows.Close()
			return fmt.Errorf("gateway: database list: %w", err)
		}
		if _, attached := session.schemas[name]; attached {
			session.databases[index] = name
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("gateway: database list: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("gateway: database list: %w", err)
	}
	if len(session.databases) != len(session.order) {
		return fmt.Errorf("gateway: SQLite lists %d of the %d attached schemas", len(session.databases), len(session.order))
	}
	for _, function := range []string{"json_each", "json_tree"} {
		operations, err := session.explain(ctx, "SELECT * FROM "+function+"('[]')", nil)
		if err != nil {
			return fmt.Errorf("gateway: %s: %w", function, err)
		}
		for _, operation := range operations {
			if operation.opcode == "VOpen" {
				session.tableFunctions[operation.p4] = function
			}
		}
	}
	if len(session.tableFunctions) != 2 {
		return fmt.Errorf("gateway: json_each and json_tree are not two distinct virtual tables on this connection")
	}
	if err := session.connection.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&session.sqliteVersion); err != nil {
		return fmt.Errorf("gateway: SQLite version: %w", err)
	}
	prepareTemporaryFiles(ctx)
	// Last, once the session's own setup is read: no single value may outgrow what a whole result may hold. SQLite
	// refuses it while building it, before it is copied out.
	if _, err := sqlite.Limit(session.connection, limitLength, int(min(session.options.MaxBytes, sqliteMaxLength))); err != nil {
		return fmt.Errorf("gateway: SQLite length limit: %w", err)
	}
	return nil
}

// release closes the SQLite connection.
func (session *Session) release() error {
	var err error
	if session.connection != nil {
		err = session.connection.Close()
		session.connection = nil
	}
	if session.database != nil {
		if closeErr := session.database.Close(); err == nil {
			err = closeErr
		}
		session.database = nil
	}
	return err
}

// Close ends the session: a running statement is canceled and the SQLite connection closed.
func (session *Session) Close() error {
	session.statementMutex.Lock()
	if session.cancelStatement != nil {
		session.cancelStatement()
	}
	session.statementMutex.Unlock()
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.closed {
		return nil
	}
	session.closed = true
	return session.release()
}

// TransactionStatus is PostgreSQL's ReadyForQuery status: idle, in a transaction block, or in a failed one.
func (session *Session) TransactionStatus() byte {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return session.transaction
}

// enter takes the session for one statement and checks Access.Current first; leave releases it.
func (session *Session) enter(ctx context.Context) (context.Context, func(), error) {
	session.mutex.Lock()
	if session.closed {
		session.mutex.Unlock()
		return nil, nil, pgwire.NewError("08003", "the session is closed")
	}
	if session.ended != nil {
		err := session.ended
		session.mutex.Unlock()
		return nil, nil, err
	}
	statementContext, cancelCause := context.WithCancelCause(ctx)
	cancel := func() { cancelCause(nil) }
	session.statementMutex.Lock()
	session.cancelStatement = cancel
	session.statementMutex.Unlock()
	unwatch := temporaryFiles.watch(session.options.MaxTemporaryBytes, cancelCause)
	leave := func() {
		unwatch()
		session.statementMutex.Lock()
		session.cancelStatement = nil
		session.statementMutex.Unlock()
		cancel()
		session.mutex.Unlock()
	}
	current, err := session.access.Current(statementContext)
	if err != nil {
		leave()
		return nil, nil, protocolError(err, "checking the session's account")
	}
	if !current {
		// The user's password, grants or roles changed: this session ends and its user signs in again.
		session.ended = pgwire.NewError("57P01",
			"terminating connection because the account of this session changed (password, grants or roles): sign in again")
		_ = session.release()
		err := session.ended
		leave()
		return nil, nil, err
	}
	return statementContext, leave, nil
}

// Execute runs one statement.
func (session *Session) Execute(ctx context.Context, query string, parameters []pgwire.Parameter) (result pgwire.Result, err error) {
	defer session.logFailure(query, &err)
	ctx, leave, err := session.enter(ctx)
	if err != nil {
		return pgwire.Result{}, err
	}
	defer leave()
	defer func() { err = temporaryCause(ctx, err) }() // before leave cancels the statement's context
	parsed, err := parseStatement(session.translator().translate(query))
	if err != nil {
		session.fail()
		return pgwire.Result{}, err
	}
	if session.transaction == pgwire.TransactionFailed && parsed.kind != statementCommit && parsed.kind != statementRollback {
		return pgwire.Result{}, pgwire.NewError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
	}
	result, err = session.execute(ctx, parsed, parameters)
	if err != nil && parsed.kind != statementCommit && parsed.kind != statementRollback {
		session.fail()
	}
	return result, err
}

// Describe gives the columns a read statement returns without running it.
func (session *Session) Describe(ctx context.Context, query string, parameters []pgwire.Parameter) (columns []pgwire.Column, err error) {
	defer session.logFailure(query, &err)
	ctx, leave, err := session.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer leave()
	defer func() { err = temporaryCause(ctx, err) }() // before leave cancels the statement's context
	parsed, err := parseStatement(session.translator().translate(query))
	if err != nil {
		session.fail()
		return nil, err
	}
	if session.transaction == pgwire.TransactionFailed {
		return nil, pgwire.NewError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
	}
	switch parsed.kind {
	case statementRead, statementExplain:
		switch err = oldSearch(parsed.tokens); {
		case err != nil:
		case hasMatch(parsed.tokens):
			_, columns, err = session.matchRead(ctx, parsed, parameters, true)
		default:
			_, columns, err = session.read(ctx, parsed, parameters, true)
		}
	case statementVersion, statementShow:
		var result pgwire.Result
		result, err = session.execute(ctx, parsed, parameters)
		columns = result.Columns
	case statementRefused, statementAccount:
		err = readOnlyError(parsed.verb)
	}
	if err != nil {
		session.fail()
		return nil, err
	}
	return columns, nil
}

// logFailure tells Options.Log a statement that failed.
func (session *Session) logFailure(query string, err *error) {
	if *err != nil && session.options.Log != nil {
		session.options.Log(query, *err)
	}
}

// fail marks an open transaction block failed, as PostgreSQL does after an error inside one.
func (session *Session) fail() {
	if session.transaction == pgwire.TransactionActive {
		session.transaction = pgwire.TransactionFailed
	}
}

func (session *Session) execute(ctx context.Context, parsed statement, parameters []pgwire.Parameter) (pgwire.Result, error) {
	switch parsed.kind {
	case statementEmpty:
		return pgwire.Result{}, nil
	case statementRead, statementExplain:
		if err := oldSearch(parsed.tokens); err != nil {
			return pgwire.Result{}, err
		}
		if hasMatch(parsed.tokens) {
			result, _, err := session.matchRead(ctx, parsed, parameters, false)
			return result, err
		}
		result, _, err := session.read(ctx, parsed, parameters, false)
		return result, err
	case statementVersion:
		version := "PostgreSQL 16.0 (Kitwork, SQLite " + session.sqliteVersion + ")"
		return textResult("version", version, "SELECT 1"), nil
	case statementShow:
		return show(parsed)
	case statementSet:
		return set(parsed)
	case statementBegin:
		return session.begin(ctx, parsed)
	case statementCommit:
		return session.finish(parsed, false)
	case statementRollback:
		return session.finish(parsed, true)
	case statementAccount:
		tag, handled, err := session.access.Command(ctx, strings.TrimSpace(parsed.text[parsed.tokens[0].start:]))
		if err != nil {
			return pgwire.Result{}, protocolError(err, "running the account statement")
		}
		if handled {
			return pgwire.Result{CommandTag: tag}, nil
		}
		return pgwire.Result{}, readOnlyError(parsed.verb)
	default:
		return pgwire.Result{}, readOnlyError(parsed.verb)
	}
}

// begin opens one read transaction on the session's connection: every later statement of the block reads the
// snapshot its first statement took.
func (session *Session) begin(ctx context.Context, parsed statement) (pgwire.Result, error) {
	all := words(parsed.tokens)
	tag := "BEGIN"
	if all[0] == "START" {
		if len(all) < 2 || all[1] != "TRANSACTION" {
			return pgwire.Result{}, syntaxError("START needs TRANSACTION")
		}
		tag = "START TRANSACTION"
	}
	// BEGIN READ WRITE, what drivers and database tools send for any transaction not marked read-only (lib/pq for a
	// plain Begin), begins the same read transaction: a write inside it is refused as anywhere else. IMMEDIATE and
	// EXCLUSIVE, which take SQLite's write lock, are refused.
	for _, word := range all {
		if word == "IMMEDIATE" || word == "EXCLUSIVE" {
			return pgwire.Result{}, pgwire.NewError("25006", "cannot begin a read-write transaction: this database is read-only")
		}
	}
	if session.transaction != pgwire.TransactionIdle {
		return pgwire.Result{CommandTag: tag}, nil // PostgreSQL warns and stays in the block
	}
	// The block reads the catalog of the moment it begins.
	if err := session.refreshCatalog(ctx); err != nil {
		return pgwire.Result{}, err
	}
	if _, err := session.connection.ExecContext(ctx, "BEGIN"); err != nil {
		return pgwire.Result{}, sqliteError(err)
	}
	session.transaction = pgwire.TransactionActive
	return pgwire.Result{CommandTag: tag}, nil
}

// finish ends a transaction block. COMMIT of a failed block rolls it back, as PostgreSQL does.
func (session *Session) finish(parsed statement, rollback bool) (pgwire.Result, error) {
	for _, word := range words(parsed.tokens) {
		if word == "TO" {
			return pgwire.Result{}, pgwire.NewError("0A000", "savepoints are not supported")
		}
		if word == "PREPARED" {
			return pgwire.Result{}, pgwire.NewError("0A000", "prepared transactions are not supported")
		}
	}
	tag := "COMMIT"
	if rollback || session.transaction == pgwire.TransactionFailed {
		tag = "ROLLBACK"
	}
	if session.transaction == pgwire.TransactionIdle {
		return pgwire.Result{CommandTag: tag}, nil // PostgreSQL warns there is no transaction in progress
	}
	session.transaction = pgwire.TransactionIdle
	// A read transaction has nothing to keep: both end it. SQLite may already have ended it after an interrupt.
	if _, err := session.connection.ExecContext(context.Background(), "ROLLBACK"); err != nil &&
		!strings.Contains(err.Error(), "no transaction is active") {
		return pgwire.Result{}, sqliteError(err)
	}
	return pgwire.Result{CommandTag: tag}, nil
}

// textResult is a one-row, one-column text result.
func textResult(name, value, tag string) pgwire.Result {
	return pgwire.Result{
		Columns:    []pgwire.Column{{Name: name, DataTypeOID: pgwire.OIDText}},
		Rows:       [][]pgwire.Field{{{Data: []byte(value)}}},
		CommandTag: tag,
	}
}

// settings are what SHOW answers: PostgreSQL's name of each setting and its fixed value.
var settings = map[string][2]string{
	"SERVER_VERSION":                {"server_version", ServerVersion},
	"SERVER_VERSION_NUM":            {"server_version_num", "160000"},
	"SERVER_ENCODING":               {"server_encoding", "UTF8"},
	"CLIENT_ENCODING":               {"client_encoding", "UTF8"},
	"STANDARD_CONFORMING_STRINGS":   {"standard_conforming_strings", "on"},
	"TIMEZONE":                      {"TimeZone", "UTC"},
	"DATESTYLE":                     {"DateStyle", "ISO, MDY"},
	"INTERVALSTYLE":                 {"IntervalStyle", "postgres"},
	"INTEGER_DATETIMES":             {"integer_datetimes", "on"},
	"TRANSACTION_ISOLATION":         {"transaction_isolation", "repeatable read"},
	"DEFAULT_TRANSACTION_ISOLATION": {"default_transaction_isolation", "repeatable read"},
	"TRANSACTION_READ_ONLY":         {"transaction_read_only", "on"},
	"DEFAULT_TRANSACTION_READ_ONLY": {"default_transaction_read_only", "on"},
	"SEARCH_PATH":                   {"search_path", "public"},
	"IS_SUPERUSER":                  {"is_superuser", "off"},
}

func show(parsed statement) (pgwire.Result, error) {
	all := words(parsed.tokens)[1:]
	name := strings.Join(all, " ")
	switch name {
	case "TIME ZONE":
		name = "TIMEZONE"
	case "TRANSACTION ISOLATION LEVEL":
		name = "TRANSACTION_ISOLATION"
	case "ALL":
		names := make([]string, 0, len(settings))
		for key := range settings {
			names = append(names, key)
		}
		sort.Strings(names)
		result := pgwire.Result{
			Columns:    []pgwire.Column{{Name: "name", DataTypeOID: pgwire.OIDText}, {Name: "setting", DataTypeOID: pgwire.OIDText}},
			CommandTag: "SHOW",
		}
		for _, key := range names {
			setting := settings[key]
			result.Rows = append(result.Rows, []pgwire.Field{{Data: []byte(setting[0])}, {Data: []byte(setting[1])}})
		}
		return result, nil
	}
	setting, known := settings[name]
	if !known {
		return pgwire.Result{}, pgwire.NewError("42704", fmt.Sprintf("unrecognized configuration parameter %q", strings.ToLower(name)))
	}
	return textResult(setting[0], setting[1], "SHOW"), nil
}

// set accepts and ignores the settings clients send on connecting (search_path, client_encoding, application_name,
// DateStyle, extra_float_digits, TimeZone, …); asking for writes is refused, and a role cannot be switched.
func set(parsed statement) (pgwire.Result, error) {
	all := words(parsed.tokens)
	tag := all[0]
	for index, word := range all {
		if word == "WRITE" && index > 0 && all[index-1] == "READ" {
			return pgwire.Result{}, pgwire.NewError("25006", "cannot set a read-write transaction: this database is read-only")
		}
	}
	rest := all[1:]
	if len(rest) > 0 && (rest[0] == "SESSION" || rest[0] == "LOCAL") {
		rest = rest[1:]
	}
	if len(rest) > 0 && (rest[0] == "ROLE" || rest[0] == "AUTHORIZATION") {
		return pgwire.Result{}, pgwire.NewError("0A000", "SET ROLE and SET SESSION AUTHORIZATION are not supported: sign in as that user")
	}
	if len(rest) >= 3 && (rest[0] == "DEFAULT_TRANSACTION_READ_ONLY" || rest[0] == "TRANSACTION_READ_ONLY") &&
		(rest[1] == "TO" || rest[1] == "=") {
		switch rest[2] {
		case "OFF", "FALSE", "0", "NO":
			return pgwire.Result{}, pgwire.NewError("25006", "cannot set "+strings.ToLower(rest[0])+" off: this database is read-only")
		}
	}
	return pgwire.Result{CommandTag: tag}, nil
}
