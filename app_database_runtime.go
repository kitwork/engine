package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kitwork/engine/kitdb/analytics"
	"github.com/kitwork/engine/kitdb/catalog"
	"github.com/kitwork/engine/kitdb/gateway"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/work"
)

// app.database(path, { port, user, password }) is the host's database port (KITWORK_KITDB.md câu 13.5, 30/10/2026):
// the PostgreSQL wire on SQLite files, read-only until 05/11/2026; an account granted INSERT, UPDATE or DELETE now
// writes, through the writer the engine's own statements write through (work.OpenWriter). A client names its database the way the catalogue of a cluster
// does: a folder of the host's .database/ (`shopping`), or `<domain>/<database>` of an app (`demo.kitwork.io/
// application`, the folder application/ of the .database/ of the app that has the domain). The database's schemas
// are the .db files of its folder: kitwork.db is public, billing.db is billing. Each .database/ is one cluster with
// its own accounts, roles and grants in .catalog/kitwork.db (kitdb/catalog), managed by SQL over the port;
// user/password of app.database is the admin of every cluster. SCRAM-SHA-256; loopback only until TLS.

// appDatabaseRuntime is the port while the host runs.
type appDatabaseRuntime struct {
	cancel    context.CancelFunc
	listener  net.Listener
	serveDone chan struct{}
	gateway   *appDatabaseGateway
	closeOnce sync.Once
	closeErr  error
}

// appDatabaseApps is where the host's apps are, to find the app of a domain.
type appDatabaseApps struct {
	root   string
	layout work.RootLayout
}

func startAppDatabaseRuntimes(parent context.Context, configs []AppDatabaseConfig, apps appDatabaseApps) ([]*appDatabaseRuntime, <-chan error, error) {
	errorsChannel := make(chan error, max(1, len(configs)))
	runtimes := make([]*appDatabaseRuntime, 0, len(configs))
	for _, config := range configs {
		runtime, err := startAppDatabaseRuntime(parent, config, apps, errorsChannel)
		if err != nil {
			for index := len(runtimes) - 1; index >= 0; index-- {
				_ = runtimes[index].Close()
			}
			return nil, nil, err
		}
		runtimes = append(runtimes, runtime)
	}
	return runtimes, errorsChannel, nil
}

// validateAppDatabaseConfigs checks what app.database names before anything opens: its path is a folder, or nothing
// yet (the host's .database/ is made at the start).
func validateAppDatabaseConfigs(configs []AppDatabaseConfig) error {
	for _, config := range configs {
		info, err := os.Stat(config.Path)
		if err == nil && !info.IsDir() {
			return fmt.Errorf("app.database %q: path is a file; the port serves the host's .database/ folder", config.Path)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("app.database %q: %w", config.Path, err)
		}
	}
	return nil
}

func startAppDatabaseRuntime(parent context.Context, config AppDatabaseConfig, apps appDatabaseApps, errorsChannel chan<- error) (*appDatabaseRuntime, error) {
	if parent == nil {
		return nil, fmt.Errorf("app.database %q: parent context is nil", config.Path)
	}
	if err := validateAppDatabaseConfigs([]AppDatabaseConfig{config}); err != nil {
		return nil, err
	}
	host, err := filepath.Abs(config.Path)
	if err != nil {
		return nil, fmt.Errorf("app.database %q: resolve path: %w", config.Path, err)
	}
	if err := os.MkdirAll(host, 0o700); err != nil {
		return nil, fmt.Errorf("app.database %q: %w", config.Path, err)
	}
	if apps.root != "" {
		if absolute, err := filepath.Abs(apps.root); err == nil {
			apps.root = absolute
		}
	}
	// options.Analytics: the column copies the port's aggregates read (kitdb/analytics), decided by the engine, every
	// table's of a file in one file beside it (<file>.analytics)
	port := &appDatabaseGateway{
		host: host, apps: apps,
		admin: catalog.Admin{User: config.User, Password: config.Password},
		options: gateway.Options{MaxRows: 10_000, MaxBytes: 10 << 20, Analytics: analytics.NewRegistry(),
			Writers: appDatabaseWriters{}},
		catalogs: map[string]*catalog.Catalog{},
	}
	ctx, cancel := context.WithCancel(parent)
	runtime := &appDatabaseRuntime{cancel: cancel, gateway: port}
	if port.search, err = startAppDatabaseSearch(ctx, host, config.Search); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	address := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	runtime.listener, err = net.Listen("tcp", address)
	if err != nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("app.database %q: listen on %s: %w", config.Path, address, err)
	}
	if err := pgwire.ValidateCleartextListener(runtime.listener); err != nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("app.database %q: %w (the port has no TLS yet: host must be 127.0.0.1 or ::1)", config.Path, err)
	}
	server := pgwire.Server{Authenticator: port}
	if config.Concurrency > 0 {
		server.MaxConcurrentQueries = config.Concurrency
		server.MaxConcurrentQueriesPerKey = config.Concurrency
		server.MaxConnections = max(16, config.Concurrency*4)
	}
	listener := runtime.listener
	runtime.serveDone = make(chan struct{})
	go func() {
		defer close(runtime.serveDone)
		serveErr := server.Serve(ctx, listener)
		if serveErr != nil && ctx.Err() == nil && !errors.Is(serveErr, net.ErrClosed) {
			select {
			case errorsChannel <- fmt.Errorf("app.database %q stopped: %w", config.Path, serveErr):
			default:
			}
		}
	}()
	slog.Info("Database port started (PostgreSQL wire, SQLite, read-only)", "path", host, "address", runtime.listener.Addr())
	return runtime, nil
}

func (runtime *appDatabaseRuntime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.closeOnce.Do(func() {
		runtime.cancel()
		if runtime.listener != nil {
			runtime.closeErr = errors.Join(runtime.closeErr, runtime.listener.Close())
		}
		if runtime.serveDone != nil {
			<-runtime.serveDone
		}
		if runtime.gateway != nil {
			runtime.closeErr = errors.Join(runtime.closeErr, runtime.gateway.Close())
		}
	})
	return runtime.closeErr
}

// appDatabaseGateway finds the database a client names, its cluster's catalog, and opens its session.
type appDatabaseGateway struct {
	host    string // the host's .database/, a cluster of its own
	apps    appDatabaseApps
	admin   catalog.Admin
	options gateway.Options
	// search holds the full-text indexes of app.database's search (app_database_search.go).
	search *appDatabaseSearch

	mu       sync.Mutex
	catalogs map[string]*catalog.Catalog // by the cluster's folder
	closed   bool
}

// appDatabaseTarget is one database as a client named it.
type appDatabaseTarget struct {
	name    string // as the catalog names it: lower case
	cluster string // its .database/
	folder  string // its own folder in the cluster
}

// appDatabasePart is one part of a database's name: a domain or a folder, never a path out of where it is looked for.
var appDatabasePart = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// resolve finds the database a client names, or why there is none.
func (g *appDatabaseGateway) resolve(name string) (appDatabaseTarget, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	domain, database, ofApp := strings.Cut(name, "/")
	if !ofApp {
		database, domain = domain, ""
	}
	if !appDatabasePart.MatchString(database) || strings.Contains(database, "..") || (ofApp && (!appDatabasePart.MatchString(domain) || strings.Contains(domain, ".."))) {
		return appDatabaseTarget{}, fmt.Errorf("database %q does not exist: a database is <folder> of the host's .database/ or <domain>/<folder> of an app", name)
	}
	cluster := g.host
	if ofApp {
		app, err := g.appOf(domain)
		if err != nil {
			return appDatabaseTarget{}, err
		}
		cluster = filepath.Join(app, ".database")
	}
	folder := filepath.Join(cluster, database)
	if info, err := os.Stat(filepath.Join(folder, "kitwork.db")); err != nil || !info.Mode().IsRegular() {
		return appDatabaseTarget{}, fmt.Errorf("database %q does not exist: no %s", name, filepath.ToSlash(filepath.Join(database, "kitwork.db")))
	}
	return appDatabaseTarget{name: name, cluster: cluster, folder: folder}, nil
}

// appOf is the folder of the app that has domain.
func (g *appDatabaseGateway) appOf(domain string) (string, error) {
	missing := fmt.Errorf("database of %q does not exist: no app has that domain", domain)
	if g.apps.root == "" {
		return "", missing
	}
	if g.apps.layout.IsSingleApp() {
		if g.apps.layout == work.RootLayoutMultiDomain {
			if info, err := os.Stat(filepath.Join(g.apps.root, domain)); err != nil || !info.IsDir() {
				return "", missing
			}
		}
		return g.apps.root, nil
	}
	identity, err := work.ResolveIdentityForLayout(g.apps.root, domain, g.apps.layout)
	if err != nil || identity == "" {
		return "", missing
	}
	return filepath.Join(g.apps.root, identity), nil
}

// catalogOf is the catalog of a cluster, opened once. The accounts of an old KitDB catalog (.catalog/data.kitdb) were
// brought in by the catalog's first opening until 05/11/2026, when the KROW reader left the engine (KITWORK_KITDB.md
// câu 13.4); an engine from before that day still imports them.
func (g *appDatabaseGateway) catalogOf(ctx context.Context, cluster string) (*catalog.Catalog, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, fmt.Errorf("the database port is closing")
	}
	if current := g.catalogs[cluster]; current != nil {
		return current, nil
	}
	opened, err := catalog.Open(ctx, cluster, g.admin)
	if err != nil {
		return nil, err
	}
	if legacy := filepath.Join(cluster, catalog.Directory, "data.kitdb"); fileExists(legacy) {
		slog.Warn("Database port: the accounts of the old KitDB catalog are no longer imported; an engine from before 05/11/2026 imports them",
			"catalog", legacy)
	}
	g.catalogs[cluster] = opened
	return opened, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// Authenticate is the cleartext exchange, which the port does not offer: it authenticates with SCRAM-SHA-256.
func (g *appDatabaseGateway) Authenticate(context.Context, pgwire.Startup, string) (pgwire.Session, error) {
	return nil, pgwire.NewError("28000", "this server authenticates with SCRAM-SHA-256")
}

// SCRAMVerifier is the verifier of the startup's user in the cluster of the database it names. For a database that
// does not exist only the host's admin is known (it is told the database does not exist once authenticated); anyone
// else fails as a wrong password does.
func (g *appDatabaseGateway) SCRAMVerifier(ctx context.Context, startup pgwire.Startup) (pgwire.SCRAMVerifier, bool, error) {
	cluster := g.host
	if target, err := g.resolve(startup.Database()); err == nil {
		cluster = target.cluster
	} else if startup.User() != g.admin.User {
		return pgwire.SCRAMVerifier{}, false, nil
	}
	accounts, err := g.catalogOf(ctx, cluster)
	if err != nil {
		slog.Warn("Database port: catalog unavailable", "cluster", cluster, "error", err)
		return pgwire.SCRAMVerifier{}, false, pgwire.NewError("58000", "the catalog of this database is unavailable")
	}
	return accounts.Verifier(ctx, startup.User())
}

// OpenSession opens the session of an authenticated user on the database it names.
func (g *appDatabaseGateway) OpenSession(ctx context.Context, startup pgwire.Startup) (pgwire.Session, error) {
	target, err := g.resolve(startup.Database())
	if err != nil {
		return nil, pgwire.NewError("3D000", err.Error())
	}
	accounts, err := g.catalogOf(ctx, target.cluster)
	if err != nil {
		return nil, pgwire.NewError("58000", "the catalog of this database is unavailable")
	}
	user := startup.User()
	access := &appDatabaseAccess{catalog: accounts, user: user, database: target.name, admin: accounts.IsAdmin(user)}
	if !access.admin {
		allowed, err := accounts.CanConnect(ctx, user, target.name)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, pgwire.NewError("42501", fmt.Sprintf("permission denied for database %q", target.name))
		}
	}
	if access.stamp, err = accounts.Stamp(ctx, user); err != nil {
		return nil, err
	}
	schemas, err := appDatabaseSchemas(target.folder)
	if err != nil {
		return nil, pgwire.NewError("3D000", fmt.Sprintf("database %q: %v", target.name, err))
	}
	columns, err := readAppDatabaseColumns(ctx, schemas)
	if err != nil {
		return nil, pgwire.NewError("58000", fmt.Sprintf("database %q: %v", target.name, err))
	}
	options := g.options
	options.User = user
	options.Search = appDatabaseSearchProvider{database: target.name, schemas: schemas}
	options.Log = func(statement string, err error) { logAppDatabaseFailure(target.name, user, statement, err) }
	session, err := gateway.Open(ctx, gateway.Target{Name: target.name, Schemas: schemas}, access, columns, options)
	if err != nil {
		return nil, err
	}
	// The tables whose .searchable() columns ask for an index get one (app_database_search.go).
	g.search.discover(target.name, schemas, columns)
	return session, nil
}

// logAppDatabaseFailure prints a statement the port refused or could not run, with its error, as PostgreSQL logs a
// failed statement: what a database tool asks that the port does not answer shows where the host's other lines do
// ([Queue], [Schedule]).
func logAppDatabaseFailure(database, user, statement string, err error) {
	text := []rune(strings.Join(strings.Fields(statement), " "))
	if len(text) > 2000 {
		text = append(text[:2000], []rune(" …")...)
	}
	fmt.Printf("[database] %s (%s): %v — %s\n", database, user, err, string(text))
}

// Close closes every cluster's catalog.
func (g *appDatabaseGateway) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	var closeErr error
	for _, opened := range g.catalogs {
		closeErr = errors.Join(closeErr, opened.Close())
	}
	g.catalogs = nil
	if g.options.Analytics != nil {
		closeErr = errors.Join(closeErr, g.options.Analytics.Close())
	}
	return errors.Join(closeErr, g.search.Close())
}

// appDatabaseSchema is a schema file's name: kitwork.db is public, <name>.db the schema <name>.
var appDatabaseSchema = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// appDatabaseSchemas are the schemas of a database: each .db file of its folder.
func appDatabaseSchemas(folder string) (map[string]string, error) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	schemas := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".db") {
			continue
		}
		schema := strings.TrimSuffix(name, ".db")
		if name == "kitwork.db" {
			schema = "public"
		} else if !appDatabaseSchema.MatchString(schema) || schema == "public" || schema == "main" || schema == "temp" {
			continue
		}
		schemas[schema] = filepath.Join(folder, name)
	}
	if schemas["public"] == "" {
		return nil, fmt.Errorf("no kitwork.db")
	}
	return schemas, nil
}

// appDatabaseAccess is what one session's user may do, asked of its cluster's catalog.
type appDatabaseAccess struct {
	catalog  *catalog.Catalog
	user     string
	database string
	admin    bool
	stamp    uint64
}

func (access *appDatabaseAccess) CanSelect(ctx context.Context, schema, table string) (bool, error) {
	if access.admin {
		return true, nil
	}
	return access.catalog.CanSelect(ctx, access.user, access.database, schema, table)
}

func (access *appDatabaseAccess) CanModify(ctx context.Context, schema, table, privilege string) (bool, error) {
	if access.admin {
		return true, nil
	}
	return access.catalog.Can(ctx, access.user, access.database, schema, table, privilege)
}

func (access *appDatabaseAccess) Admin(context.Context) (bool, error) { return access.admin, nil }

// appDatabaseWriters opens, for a session that writes, the writer of a file the engine's statements write through.
type appDatabaseWriters struct{}

func (appDatabaseWriters) Open(path string) (gateway.Writer, error) {
	database, release, err := work.OpenWriter(path)
	if err != nil {
		return nil, err
	}
	return appDatabaseWriter{Database: database, release: release}, nil
}

type appDatabaseWriter struct {
	*kitdbsqlite.Database
	release func()
}

func (writer appDatabaseWriter) Close() error {
	writer.release()
	return nil
}

func (access *appDatabaseAccess) Command(ctx context.Context, statement string) (string, bool, error) {
	return access.catalog.Command(ctx, access.user, access.database, statement)
}

// Current is false once the user's password, roles or grants changed since the session opened: it ends.
func (access *appDatabaseAccess) Current(ctx context.Context) (bool, error) {
	stamp, err := access.catalog.Stamp(ctx, access.user)
	return err == nil && stamp == access.stamp, err
}

// appDatabaseColumns is what kitwork_columns of each schema file says of its columns (db.sqlite_columns.go in work).
type appDatabaseColumns map[string]appDatabaseColumn

type appDatabaseColumn struct {
	schema, table, column string // as the file writes them
	kind                  string
	precision, scale      int
	weight                int // a .searchable() column's search weight, 0 for any other
}

func (columns appDatabaseColumns) Column(schema, table, column string) (string, int, int, bool) {
	found, ok := columns[strings.ToLower(schema+"."+table+"."+column)]
	return found.kind, found.precision, found.scale, ok
}

// readAppDatabaseColumns reads kitwork_columns of every schema file, read-only; a file without it says nothing.
func readAppDatabaseColumns(ctx context.Context, schemas map[string]string) (appDatabaseColumns, error) {
	columns := appDatabaseColumns{}
	names := make([]string, 0, len(schemas))
	for schema := range schemas {
		names = append(names, schema)
	}
	sort.Strings(names)
	for _, schema := range names {
		if err := readAppDatabaseColumnsOf(ctx, schema, schemas[schema], columns); err != nil {
			return nil, err
		}
	}
	return columns, nil
}

func readAppDatabaseColumnsOf(ctx context.Context, schema, path string, columns appDatabaseColumns) error {
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer file.Close()
	var held int
	if err := file.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'kitwork_columns'`).Scan(&held); err != nil || held == 0 {
		return err
	}
	// search_weight came on 31/10/2026: a file written before has none, and no column of it is searchable.
	weight := "0"
	if err := file.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('kitwork_columns') WHERE name = 'search_weight'`).Scan(&held); err != nil {
		return err
	}
	if held > 0 {
		weight = "search_weight"
	}
	rows, err := file.QueryContext(ctx, `SELECT table_name, column_name, kind, precision, scale, `+weight+` FROM kitwork_columns`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		found := appDatabaseColumn{schema: schema}
		if err := rows.Scan(&found.table, &found.column, &found.kind, &found.precision, &found.scale, &found.weight); err != nil {
			return err
		}
		columns[strings.ToLower(schema+"."+found.table+"."+found.column)] = found
	}
	return rows.Err()
}
