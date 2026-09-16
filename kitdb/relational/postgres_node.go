package relational

import (
	"container/list"
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/kitdb/managed"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/pgwire"
)

const (
	DefaultMaximumDiscoveredDatabases          = 4_096
	MaximumDiscoveredDatabases                 = 100_000
	DefaultDatabaseAcquireTimeout              = 10 * time.Second
	MaximumDatabaseAcquireTimeout              = 10 * time.Minute
	DefaultMaximumIdleProjectionDatabases      = 8
	MaximumIdleProjectionDatabases             = 4_096
	DefaultMaximumIdleProjectionDirectoryBytes = int64(32 << 20)
	MaximumIdleProjectionDirectoryBytes        = int64(1 << 40)
	DefaultMaximumIdleProjectionReaderBytes    = int64(256 << 20)
	MaximumIdleProjectionReaderBytes           = int64(1 << 40)
)

// PostgresNodeOptions configures one standalone PostgreSQL endpoint over the
// Root. A .catalog database selects managed folder mode; otherwise legacy flat
// discovery is retained. User databases open lazily through ManagerLimits.
type PostgresNodeOptions struct {
	Root                       string
	MaintenanceDatabase        string
	User                       string
	Password                   string
	ReadOnly                   bool
	MaximumDiscoveredDatabases int
	DatabaseAcquireTimeout     time.Duration
	// WarmDatabases names a bounded set of logical databases whose idle
	// relational engine, page cache, and projection readers should survive LRU
	// pressure. Names never become file paths or durable database metadata.
	WarmDatabases []string
	// WarmProjectionOpenPolicy applies only when a configured warm database is
	// first selected. The default is validate when projections are enabled, so
	// corrupt sidecars cannot become long-lived node residents unnoticed.
	WarmProjectionOpenPolicy ProjectionOpenPolicy
	// MaximumIdleProjectionDatabases, MaximumIdleProjectionDirectoryBytes,
	// and MaximumIdleProjectionReaderBytes bound non-active KCOL/search readers
	// across the node. Reader bytes include container directories plus reserved
	// packed-search reader memory. Zero values select bounded defaults.
	MaximumIdleProjectionDatabases      int
	MaximumIdleProjectionDirectoryBytes int64
	MaximumIdleProjectionReaderBytes    int64
	ManagerLimits                       kitdbnode.Limits
	Relational                          Options
	Trace                               func(database, source string, parameterCount int)
}

// PostgresNodeStats is a path-free process-local view of relational ownership.
// Reader bytes cover retained container directories and deterministic search
// reader structures. They exclude payload pages, transient query allocations,
// allocator overhead, and operating-system page-cache residency.
type PostgresNodeStats struct {
	Closed                              bool
	ManagedEngines                      int
	OpeningEngines                      int
	ActiveEngines                       int
	IdleEngines                         int
	ConfiguredWarmDatabases             int
	WarmEngines                         int
	WarmIdleEngines                     int
	Sessions                            int
	ProjectionCachedEngines             int
	ProjectionCacheEntries              int
	ProjectionActiveLeases              int
	ProjectionDirectoryBytes            int64
	ProjectionSearchReaders             int
	ProjectionSearchFileHandles         int
	ProjectionReaderResidentBytes       int64
	ProjectionReaderCapacityBytes       int64
	ProjectionCacheTrims                uint64
	ProjectionDirectoryBytesTrimmed     int64
	ProjectionReaderBytesTrimmed        int64
	MaximumIdleProjectionDatabases      int
	MaximumIdleProjectionDirectoryBytes int64
	MaximumIdleProjectionReaderBytes    int64
	Manager                             kitdbnode.Stats
}

type postgresNodeDatabase struct {
	name       string
	path       string
	registered *managed.Database
}

type postgresNodeEngineEntry struct {
	database                 postgresNodeDatabase
	engine                   *Engine
	ready                    chan struct{}
	opening                  bool
	warm                     bool
	sessions                 int
	idle                     *list.Element
	projectionEntries        int
	projectionDirectoryBytes int64
	projectionReaderBytes    int64
	err                      error
}

// PostgresNode owns bounded, lazily opened relational engines for one database
// directory. It is independent from Kitwork Tenant, VM, routing and app state.
type PostgresNode struct {
	root                                string
	maintenanceDatabase                 string
	user                                string
	password                            string
	readOnly                            bool
	maximumDiscovered                   int
	manager                             *kitdbnode.Manager
	catalog                             *managed.Root
	managerMaximumOpen                  int
	managerMaximumCache                 int64
	databaseCacheBytes                  int64
	databaseAcquireTimeout              time.Duration
	warmDatabases                       map[string]struct{}
	warmProjectionOpenPolicy            ProjectionOpenPolicy
	maximumIdleProjectionDatabases      int
	maximumIdleProjectionDirectoryBytes int64
	maximumIdleProjectionReaderBytes    int64
	relational                          Options
	maximumResults                      int
	maximumMutations                    int
	trace                               func(database, source string, parameterCount int)

	mu                              sync.Mutex
	engines                         map[string]*postgresNodeEngineEntry
	idle                            list.List
	notify                          chan struct{}
	closed                          bool
	closeDone                       chan struct{}
	closeErr                        error
	projectionCacheTrims            uint64
	projectionDirectoryBytesTrimmed int64
	projectionReaderBytesTrimmed    int64
}

// OpenPostgresNode opens only .catalog in managed mode. User database handles
// remain lazy. A managed node holds the catalog's exclusive lock until Close.
func OpenPostgresNode(options PostgresNodeOptions) (*PostgresNode, error) {
	root := strings.TrimSpace(options.Root)
	if root == "" {
		return nil, fmt.Errorf("kitdb postgres node: root is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("kitdb postgres node: resolve root: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("kitdb postgres node: inspect root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("kitdb postgres node: root %q is not a directory", absoluteRoot)
	}

	maintenance := strings.TrimSpace(options.MaintenanceDatabase)
	if maintenance == "" {
		maintenance = "kitdb"
	}
	maintenance = postgresLogicalDatabaseName(maintenance)
	if err := validatePostgresLogicalDatabaseName(maintenance); err != nil {
		return nil, fmt.Errorf("kitdb postgres node: maintenance database: %w", err)
	}
	user := strings.TrimSpace(options.User)
	if user == "" {
		user = "kitdb"
	}
	if options.Password == "" {
		return nil, fmt.Errorf("kitdb postgres node: password is required")
	}
	maximumDiscovered := options.MaximumDiscoveredDatabases
	if maximumDiscovered == 0 {
		maximumDiscovered = DefaultMaximumDiscoveredDatabases
	}
	if maximumDiscovered < 1 || maximumDiscovered > MaximumDiscoveredDatabases {
		return nil, fmt.Errorf(
			"kitdb postgres node: maximum discovered databases must be between 1 and %d",
			MaximumDiscoveredDatabases,
		)
	}
	acquireTimeout := options.DatabaseAcquireTimeout
	if acquireTimeout == 0 {
		acquireTimeout = DefaultDatabaseAcquireTimeout
	}
	if acquireTimeout < time.Millisecond || acquireTimeout > MaximumDatabaseAcquireTimeout {
		return nil, fmt.Errorf(
			"kitdb postgres node: database acquire timeout must be between 1ms and %s",
			MaximumDatabaseAcquireTimeout,
		)
	}
	if strings.TrimSpace(options.Relational.SearchNamespace) != "" {
		return nil, fmt.Errorf("kitdb postgres node: a shared search namespace is unsafe; leave it empty for per-database derivation")
	}
	maximumResults, maximumMutations, err := normalizeRelationalBounds(options.Relational)
	if err != nil {
		return nil, err
	}
	manager, err := kitdbnode.NewManager(options.ManagerLimits)
	if err != nil {
		return nil, err
	}
	managerStats := manager.Stats()
	warmDatabases, err := normalizePostgresNodeWarmDatabases(options.WarmDatabases, maintenance)
	if err != nil {
		_ = manager.Close()
		return nil, err
	}
	warmProjectionOpenPolicy := options.WarmProjectionOpenPolicy
	if warmProjectionOpenPolicy == ProjectionOpenDefault && options.Relational.ExperimentalProjections && len(warmDatabases) != 0 {
		warmProjectionOpenPolicy = ProjectionOpenValidate
	}
	warmProjectionOpenPolicy, err = normalizeProjectionOpenPolicy(warmProjectionOpenPolicy)
	if err != nil {
		_ = manager.Close()
		return nil, fmt.Errorf("kitdb postgres node: warm projection open policy: %w", err)
	}
	if warmProjectionOpenPolicy != ProjectionOpenLazy && !options.Relational.ExperimentalProjections {
		_ = manager.Close()
		return nil, fmt.Errorf("kitdb postgres node: warm projection open policy requires experimental projections")
	}
	maximumIdleProjectionDatabases, maximumIdleProjectionDirectoryBytes,
		maximumIdleProjectionReaderBytes, err :=
		normalizePostgresNodeProjectionResidency(
			options.MaximumIdleProjectionDatabases,
			options.MaximumIdleProjectionDirectoryBytes,
			options.MaximumIdleProjectionReaderBytes,
			managerStats.MaxOpenDatabases,
			len(warmDatabases),
			options.Relational.SearchReaderCacheBytes,
		)
	if err != nil {
		_ = manager.Close()
		return nil, err
	}
	databaseCacheBytes := options.Relational.Kernel.PageCacheBytes
	switch {
	case databaseCacheBytes == 0:
		databaseCacheBytes = managerStats.DefaultPageCacheBytes
	case databaseCacheBytes < 0:
		databaseCacheBytes = 0
	}
	node := &PostgresNode{
		root: absoluteRoot, maintenanceDatabase: maintenance,
		user: user, password: options.Password, readOnly: options.ReadOnly,
		maximumDiscovered: maximumDiscovered, manager: manager,
		managerMaximumOpen:                  managerStats.MaxOpenDatabases,
		managerMaximumCache:                 managerStats.MaxPageCacheBytes,
		databaseCacheBytes:                  databaseCacheBytes,
		databaseAcquireTimeout:              acquireTimeout,
		warmDatabases:                       warmDatabases,
		warmProjectionOpenPolicy:            warmProjectionOpenPolicy,
		maximumIdleProjectionDatabases:      maximumIdleProjectionDatabases,
		maximumIdleProjectionDirectoryBytes: maximumIdleProjectionDirectoryBytes,
		maximumIdleProjectionReaderBytes:    maximumIdleProjectionReaderBytes,
		relational:                          options.Relational,
		maximumResults:                      maximumResults, maximumMutations: maximumMutations,
		trace:   options.Trace,
		engines: make(map[string]*postgresNodeEngineEntry),
		notify:  make(chan struct{}), closeDone: make(chan struct{}),
	}
	managedRoot, err := managed.Present(absoluteRoot)
	if err != nil {
		_ = manager.Close()
		return nil, err
	}
	if managedRoot {
		node.catalog, err = managed.Open(absoluteRoot)
		if err != nil {
			_ = manager.Close()
			return nil, err
		}
	}
	databases, err := node.discoverDatabases()
	if err != nil {
		_ = node.Close()
		return nil, err
	}
	available := make(map[string]struct{}, len(databases))
	for _, database := range databases {
		available[strings.ToLower(database.name)] = struct{}{}
	}
	for name := range warmDatabases {
		if _, found := available[name]; !found {
			_ = node.Close()
			return nil, fmt.Errorf("kitdb postgres node: warm database %q does not exist", name)
		}
	}
	return node, nil
}

func normalizePostgresNodeWarmDatabases(names []string, maintenance string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(names))
	for _, source := range names {
		name := postgresLogicalDatabaseName(source)
		if err := validatePostgresLogicalDatabaseName(name); err != nil {
			return nil, fmt.Errorf("kitdb postgres node: warm database: %w", err)
		}
		if strings.EqualFold(name, maintenance) {
			return nil, fmt.Errorf("kitdb postgres node: maintenance database %q cannot be warmed", name)
		}
		key := strings.ToLower(name)
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("kitdb postgres node: warm database %q is repeated", name)
		}
		result[key] = struct{}{}
	}
	return result, nil
}

func normalizePostgresNodeProjectionResidency(
	maximumDatabases int,
	maximumDirectoryBytes int64,
	maximumReaderBytes int64,
	maximumOpen int,
	warmDatabases int,
	searchReaderCacheBytes int64,
) (int, int64, int64, error) {
	explicitDatabases := maximumDatabases != 0
	if maximumDatabases == 0 {
		maximumDatabases = min(DefaultMaximumIdleProjectionDatabases, maximumOpen)
		if warmDatabases > maximumDatabases {
			maximumDatabases = warmDatabases
		}
	}
	if maximumDatabases < 1 || maximumDatabases > MaximumIdleProjectionDatabases ||
		maximumDatabases > maximumOpen {
		return 0, 0, 0, fmt.Errorf(
			"kitdb postgres node: maximum idle projection databases must be between 1 and MaxOpenDatabases (up to %d)",
			MaximumIdleProjectionDatabases,
		)
	}
	if explicitDatabases && warmDatabases > maximumDatabases {
		return 0, 0, 0, fmt.Errorf(
			"kitdb postgres node: %d warm databases exceed the idle projection database limit %d",
			warmDatabases, maximumDatabases,
		)
	}

	maximumDirectoryPerEngine := int64(2 * maximumCachedProjectionDirectoryBytes)
	minimumWarmDirectoryBytes := int64(warmDatabases) * maximumDirectoryPerEngine
	if maximumDirectoryBytes == 0 {
		maximumDirectoryBytes = min(
			DefaultMaximumIdleProjectionDirectoryBytes,
			int64(maximumDatabases)*maximumDirectoryPerEngine,
		)
		if maximumDirectoryBytes < minimumWarmDirectoryBytes {
			maximumDirectoryBytes = minimumWarmDirectoryBytes
		}
	}
	if maximumDirectoryBytes < 1 || maximumDirectoryBytes > MaximumIdleProjectionDirectoryBytes {
		return 0, 0, 0, fmt.Errorf(
			"kitdb postgres node: maximum idle projection directory bytes must be between 1 and %d",
			MaximumIdleProjectionDirectoryBytes,
		)
	}
	if maximumDirectoryBytes < minimumWarmDirectoryBytes {
		return 0, 0, 0, fmt.Errorf(
			"kitdb postgres node: warm databases require a projection directory budget of at least %d bytes",
			minimumWarmDirectoryBytes,
		)
	}

	maximumReaderPerEngine := maximumDirectoryPerEngine + searchReaderCacheBytes
	minimumWarmReaderBytes := int64(warmDatabases) * maximumReaderPerEngine
	if maximumReaderBytes == 0 {
		maximumReaderBytes = min(
			DefaultMaximumIdleProjectionReaderBytes,
			int64(maximumDatabases)*maximumReaderPerEngine,
		)
		if maximumReaderBytes < minimumWarmReaderBytes {
			maximumReaderBytes = minimumWarmReaderBytes
		}
	}
	if maximumReaderBytes < 1 || maximumReaderBytes > MaximumIdleProjectionReaderBytes {
		return 0, 0, 0, fmt.Errorf(
			"kitdb postgres node: maximum idle projection reader bytes must be between 1 and %d",
			MaximumIdleProjectionReaderBytes,
		)
	}
	if maximumReaderBytes < minimumWarmReaderBytes {
		return 0, 0, 0, fmt.Errorf(
			"kitdb postgres node: warm databases require a projection reader budget of at least %d bytes",
			minimumWarmReaderBytes,
		)
	}
	return maximumDatabases, maximumDirectoryBytes, maximumReaderBytes, nil
}

// ServePostgres serves the node through the same bounded pgwire transport as a
// single relational Engine. Authentication and routing remain node-owned.
func (node *PostgresNode) ServePostgres(
	ctx context.Context,
	listener net.Listener,
	options PostgresServerOptions,
) error {
	if node == nil {
		return fmt.Errorf("kitdb postgres node: node is nil")
	}
	return (pgwire.Server{
		Authenticator:              node,
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

// Databases returns the current bounded discovery snapshot, including the
// virtual maintenance database first.
func (node *PostgresNode) Databases() ([]string, error) {
	if node == nil {
		return nil, fmt.Errorf("kitdb postgres node: node is nil")
	}
	databases, err := node.discoverDatabases()
	if err != nil {
		return nil, err
	}
	result := make([]string, 1, len(databases)+1)
	result[0] = node.maintenanceDatabase
	for _, database := range databases {
		result = append(result, database.name)
	}
	return result, nil
}

// OpenNativeDatabase returns a lazy database/sql pool for one database owned
// by this node. Each physical connection acquires an ordinary node engine
// lease and releases it when database/sql retires that connection.
func (node *PostgresNode) OpenNativeDatabase(name string) (*sql.DB, error) {
	if node == nil {
		return nil, fmt.Errorf("kitdb native node: node is nil")
	}
	name = postgresLogicalDatabaseName(name)
	if err := validatePostgresLogicalDatabaseName(name); err != nil {
		return nil, fmt.Errorf("kitdb native node: %w", err)
	}
	if strings.EqualFold(name, node.maintenanceDatabase) {
		return nil, fmt.Errorf("kitdb native node: maintenance database %q has no user structs", name)
	}
	databases, err := node.discoverDatabases()
	if err != nil {
		return nil, err
	}
	database, found := findPostgresNodeDatabase(databases, name)
	if !found {
		return nil, fmt.Errorf("kitdb native node: database %q does not exist", name)
	}
	return sql.OpenDB(nativeNodeSQLConnector{node: node, database: database.name}), nil
}

func (node *PostgresNode) Authenticate(
	ctx context.Context,
	startup pgwire.Startup,
	password string,
) (pgwire.Session, error) {
	if node == nil {
		return nil, pgwire.NewError("57P01", "KitDB PostgreSQL node is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, pgwire.NewError("57014", "connection canceled")
	}
	if subtle.ConstantTimeCompare([]byte(startup.User()), []byte(node.user)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(node.password)) != 1 {
		return nil, pgwire.NewError("28P01", "password authentication failed for KitDB user")
	}
	databases, err := node.discoverDatabases()
	if err != nil {
		return nil, pgwire.NewError("58030", err.Error())
	}
	requested := postgresLogicalDatabaseName(startup.Database())
	if requested == "" {
		requested = node.maintenanceDatabase
	}
	if strings.EqualFold(requested, node.maintenanceDatabase) {
		return &postgresSession{
			authenticator: &postgresAuthenticator{
				database: node.maintenanceDatabase, user: node.user,
				readOnly: true,
			},
			node: node, maintenance: true,
		}, nil
	}
	database, found := findPostgresNodeDatabase(databases, requested)
	if !found {
		return nil, pgwire.NewError("3D000", fmt.Sprintf("KitDB database %q does not exist", requested))
	}
	acquireContext, cancel := context.WithTimeout(ctx, node.databaseAcquireTimeout)
	defer cancel()
	entry, err := node.acquireEngine(acquireContext, database)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, kitdbnode.ErrWarmCapacity) {
			return nil, pgwire.NewError("53300", "KitDB database capacity is busy; retry the connection")
		}
		return nil, pgwire.NewError("55000", err.Error())
	}
	trace := func(source string, parameterCount int) {
		if node.trace != nil {
			node.trace(database.name, source, parameterCount)
		}
	}
	return &postgresSession{
		authenticator: &postgresAuthenticator{
			engine: entry.engine, database: database.name,
			user: node.user, password: node.password,
			readOnly: node.readOnly, trace: trace,
		},
		node: node,
		onClose: func() error {
			return node.releaseEngine(entry)
		},
	}, nil
}

func (node *PostgresNode) discoverDatabases() ([]postgresNodeDatabase, error) {
	node.mu.Lock()
	closed := node.closed
	node.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("kitdb postgres node: node is closed")
	}
	if node.catalog != nil {
		catalog := node.catalog.Catalog()
		if len(catalog.Databases) > node.maximumDiscovered {
			return nil, fmt.Errorf("kitdb postgres node: registered database count exceeds %d", node.maximumDiscovered)
		}
		result := make([]postgresNodeDatabase, 0, len(catalog.Databases))
		for _, database := range catalog.Databases {
			if strings.EqualFold(database.Name, node.maintenanceDatabase) {
				return nil, fmt.Errorf("kitdb postgres node: registered database %q conflicts with maintenance database", database.Name)
			}
			result = append(result, postgresNodeDatabase{
				name:       database.Name,
				path:       filepath.Join(node.root, database.Directory, "data.kitdb"),
				registered: &database,
			})
		}
		return result, nil
	}
	entries, err := os.ReadDir(node.root)
	if err != nil {
		return nil, fmt.Errorf("kitdb postgres node: read root: %w", err)
	}
	result := make([]postgresNodeDatabase, 0, min(len(entries), node.maximumDiscovered))
	seen := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".kitdb") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("kitdb postgres node: inspect %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		logical := postgresLogicalDatabaseName(name)
		if err := validatePostgresLogicalDatabaseName(logical); err != nil {
			return nil, fmt.Errorf("kitdb postgres node: file %q: %w", name, err)
		}
		if strings.EqualFold(logical, node.maintenanceDatabase) {
			return nil, fmt.Errorf(
				"kitdb postgres node: file %q conflicts with maintenance database %q",
				name, node.maintenanceDatabase,
			)
		}
		key := strings.ToLower(logical)
		if previous, found := seen[key]; found {
			return nil, fmt.Errorf(
				"kitdb postgres node: files %q and %q have the same logical database name",
				previous, name,
			)
		}
		seen[key] = name
		result = append(result, postgresNodeDatabase{
			name: logical,
			path: filepath.Join(node.root, name),
		})
		if len(result) > node.maximumDiscovered {
			return nil, fmt.Errorf(
				"kitdb postgres node: discovered database count exceeds %d",
				node.maximumDiscovered,
			)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].name) < strings.ToLower(result[right].name)
	})
	return result, nil
}

func findPostgresNodeDatabase(
	databases []postgresNodeDatabase,
	requested string,
) (postgresNodeDatabase, bool) {
	for _, database := range databases {
		if strings.EqualFold(database.name, requested) {
			return database, true
		}
	}
	return postgresNodeDatabase{}, false
}

func postgresLogicalDatabaseName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > len(".kitdb") && strings.EqualFold(filepath.Ext(name), ".kitdb") {
		return name[:len(name)-len(".kitdb")]
	}
	return name
}

func validatePostgresLogicalDatabaseName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "\x00/\\") {
		return fmt.Errorf("invalid logical database name %q", name)
	}
	return nil
}

func (node *PostgresNode) acquireEngine(
	ctx context.Context,
	database postgresNodeDatabase,
) (*postgresNodeEngineEntry, error) {
	if database.registered != nil {
		path, err := node.catalog.DatabasePath(*database.registered)
		if err != nil {
			return nil, err
		}
		database.path = path
	}
	key := strings.ToLower(database.name)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		node.mu.Lock()
		if node.closed {
			node.mu.Unlock()
			return nil, fmt.Errorf("kitdb postgres node: node is closed")
		}
		if current := node.engines[key]; current != nil {
			if current.opening {
				ready := current.ready
				node.mu.Unlock()
				select {
				case <-ready:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				if current.err != nil {
					return nil, current.err
				}
				continue
			}
			if !samePostgresNodePath(current.database.path, database.path) {
				if current.sessions != 0 {
					node.mu.Unlock()
					return nil, fmt.Errorf("kitdb postgres node: database %q changed path while in use", database.name)
				}
				node.removeIdleLocked(current)
				delete(node.engines, key)
				node.signalLocked()
				node.mu.Unlock()
				if err := node.closeIdleEngine(current.engine); err != nil {
					return nil, err
				}
				continue
			}
			node.removeIdleLocked(current)
			current.sessions++
			node.mu.Unlock()
			return current, nil
		}

		managerStats := node.manager.Stats()
		atCapacity := len(node.engines) >= node.managerMaximumOpen ||
			managerStats.ReservedPageCacheBytes+node.databaseCacheBytes > node.managerMaximumCache
		if atCapacity {
			victim := node.oldestIdleLocked()
			if victim == nil {
				if node.onlyWarmEnginesLocked() {
					node.mu.Unlock()
					return nil, kitdbnode.ErrWarmCapacity
				}
				notify := node.notify
				node.mu.Unlock()
				select {
				case <-notify:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				continue
			}
			node.removeIdleLocked(victim)
			delete(node.engines, strings.ToLower(victim.database.name))
			node.signalLocked()
			node.mu.Unlock()
			if err := node.closeIdleEngine(victim.engine); err != nil {
				return nil, err
			}
			continue
		}

		_, warm := node.warmDatabases[key]
		entry := &postgresNodeEngineEntry{
			database: database, ready: make(chan struct{}), opening: true,
			warm: warm, sessions: 1,
		}
		node.engines[key] = entry
		node.mu.Unlock()

		lease, err := node.manager.Acquire(ctx, database.path, node.relational.Kernel)
		if err != nil {
			node.failEngineOpen(key, entry, err)
			return nil, err
		}
		if database.registered != nil && lease.DB().ID() != database.registered.DatabaseID {
			err := fmt.Errorf("kitdb postgres node: database %q identity does not match .catalog", database.name)
			_ = lease.Release()
			_ = node.trimManagerIdle()
			node.failEngineOpen(key, entry, err)
			return nil, err
		}
		relationalOptions := node.relational
		if warm {
			relationalOptions.ProjectionOpenPolicy = stricterProjectionOpenPolicy(
				relationalOptions.ProjectionOpenPolicy, node.warmProjectionOpenPolicy,
			)
		}
		engine, err := newEngineWithDatabase(
			ctx, lease.DB(), relationalOptions, node.maximumResults, node.maximumMutations, lease.Release,
		)
		if err != nil {
			_ = lease.Release()
			_ = node.trimManagerIdle()
			node.failEngineOpen(key, entry, err)
			return nil, err
		}

		node.mu.Lock()
		if node.closed || node.engines[key] != entry {
			node.mu.Unlock()
			_ = engine.Close()
			return nil, fmt.Errorf("kitdb postgres node: node closed while opening %q", database.name)
		}
		entry.engine = engine
		entry.opening = false
		close(entry.ready)
		node.signalLocked()
		node.mu.Unlock()
		return entry, nil
	}
}

func (node *PostgresNode) failEngineOpen(
	key string,
	entry *postgresNodeEngineEntry,
	openErr error,
) {
	node.mu.Lock()
	entry.err = openErr
	entry.opening = false
	if node.engines[key] == entry {
		delete(node.engines, key)
	}
	close(entry.ready)
	node.signalLocked()
	node.mu.Unlock()
}

func (node *PostgresNode) releaseEngine(entry *postgresNodeEngineEntry) error {
	if node == nil || entry == nil {
		return nil
	}
	node.mu.Lock()
	current := node.engines[strings.ToLower(entry.database.name)]
	if current != entry || entry.sessions == 0 {
		node.mu.Unlock()
		return nil
	}
	entry.sessions--
	if entry.sessions == 0 && !entry.opening {
		projection := entry.engine.ProjectionCacheStats()
		entry.projectionEntries = projection.Entries
		entry.projectionDirectoryBytes = projection.DirectoryBytes
		entry.projectionReaderBytes = projection.DirectoryBytes + projection.SearchReaderCapacityBytes
		entry.idle = node.idle.PushFront(entry)
	}
	trimErr := node.enforceIdleProjectionResidencyLocked()
	node.signalLocked()
	node.mu.Unlock()
	return trimErr
}

func (node *PostgresNode) oldestIdleLocked() *postgresNodeEngineEntry {
	for element := node.idle.Back(); element != nil; element = element.Prev() {
		entry := element.Value.(*postgresNodeEngineEntry)
		if !entry.warm {
			return entry
		}
	}
	return nil
}

func (node *PostgresNode) onlyWarmEnginesLocked() bool {
	if len(node.engines) == 0 {
		return false
	}
	for _, entry := range node.engines {
		if !entry.warm {
			return false
		}
	}
	return true
}

func (node *PostgresNode) enforceIdleProjectionResidencyLocked() error {
	var cachedDatabases int
	var directoryBytes int64
	var readerBytes int64
	for element := node.idle.Front(); element != nil; element = element.Next() {
		entry := element.Value.(*postgresNodeEngineEntry)
		if entry.projectionEntries == 0 {
			continue
		}
		cachedDatabases++
		directoryBytes += entry.projectionDirectoryBytes
		readerBytes += entry.projectionReaderBytes
	}

	var result error
	for cachedDatabases > node.maximumIdleProjectionDatabases ||
		directoryBytes > node.maximumIdleProjectionDirectoryBytes ||
		readerBytes > node.maximumIdleProjectionReaderBytes {
		var victim *postgresNodeEngineEntry
		for element := node.idle.Back(); element != nil; element = element.Prev() {
			candidate := element.Value.(*postgresNodeEngineEntry)
			if !candidate.warm && candidate.projectionEntries != 0 {
				victim = candidate
				break
			}
		}
		if victim == nil {
			return errors.Join(result, fmt.Errorf(
				"kitdb postgres node: warm projection readers exceed the configured idle budget",
			))
		}
		beforeEntries := victim.projectionEntries
		beforeDirectoryBytes := victim.projectionDirectoryBytes
		beforeReaderBytes := victim.projectionReaderBytes
		trimmed, err := victim.engine.TrimProjectionCache()
		if err != nil {
			return errors.Join(result, err)
		}
		victim.projectionEntries = 0
		victim.projectionDirectoryBytes = 0
		victim.projectionReaderBytes = 0
		cachedDatabases--
		directoryBytes -= beforeDirectoryBytes
		readerBytes -= beforeReaderBytes
		node.projectionCacheTrims++
		node.projectionDirectoryBytesTrimmed += trimmed.DirectoryBytes
		trimmedReaderBytes := trimmed.DirectoryBytes + trimmed.SearchReaderCapacityBytes
		node.projectionReaderBytesTrimmed += trimmedReaderBytes
		if trimmed.Entries != beforeEntries || trimmed.DirectoryBytes != beforeDirectoryBytes ||
			trimmedReaderBytes != beforeReaderBytes {
			result = errors.Join(result, fmt.Errorf(
				"kitdb postgres node: projection residency changed during idle trim",
			))
		}
	}
	return result
}

func (node *PostgresNode) removeIdleLocked(entry *postgresNodeEngineEntry) {
	if entry == nil || entry.idle == nil {
		return
	}
	node.idle.Remove(entry.idle)
	entry.idle = nil
}

// Stats returns bounded process-local node ownership without exposing database
// paths or logical names. Active engines may change immediately after the
// snapshot; each projection cache is inspected under its own lock.
func (node *PostgresNode) Stats() PostgresNodeStats {
	if node == nil {
		return PostgresNodeStats{Closed: true}
	}
	type engineSnapshot struct {
		engine   *Engine
		opening  bool
		warm     bool
		sessions int
	}
	node.mu.Lock()
	engines := make([]engineSnapshot, 0, len(node.engines))
	stats := PostgresNodeStats{
		Closed:                              node.closed,
		ManagedEngines:                      len(node.engines),
		ConfiguredWarmDatabases:             len(node.warmDatabases),
		ProjectionCacheTrims:                node.projectionCacheTrims,
		ProjectionDirectoryBytesTrimmed:     node.projectionDirectoryBytesTrimmed,
		ProjectionReaderBytesTrimmed:        node.projectionReaderBytesTrimmed,
		MaximumIdleProjectionDatabases:      node.maximumIdleProjectionDatabases,
		MaximumIdleProjectionDirectoryBytes: node.maximumIdleProjectionDirectoryBytes,
		MaximumIdleProjectionReaderBytes:    node.maximumIdleProjectionReaderBytes,
	}
	for _, entry := range node.engines {
		engines = append(engines, engineSnapshot{
			engine: entry.engine, opening: entry.opening,
			warm: entry.warm, sessions: entry.sessions,
		})
		stats.Sessions += entry.sessions
		if entry.opening {
			stats.OpeningEngines++
		} else if entry.sessions == 0 {
			stats.IdleEngines++
		} else {
			stats.ActiveEngines++
		}
		if entry.warm {
			stats.WarmEngines++
			if !entry.opening && entry.sessions == 0 {
				stats.WarmIdleEngines++
			}
		}
	}
	node.mu.Unlock()

	for _, entry := range engines {
		if entry.opening || entry.engine == nil {
			continue
		}
		projection := entry.engine.ProjectionCacheStats()
		if projection.Entries != 0 {
			stats.ProjectionCachedEngines++
		}
		stats.ProjectionCacheEntries += projection.Entries
		stats.ProjectionActiveLeases += projection.ActiveLeases
		stats.ProjectionDirectoryBytes += projection.DirectoryBytes
		stats.ProjectionSearchReaders += projection.SearchReaders
		stats.ProjectionSearchFileHandles += projection.SearchFileHandles
		stats.ProjectionReaderResidentBytes += projection.DirectoryBytes + projection.SearchReaderResidentBytes
		stats.ProjectionReaderCapacityBytes += projection.DirectoryBytes + projection.SearchReaderCapacityBytes
	}
	stats.Manager = node.manager.Stats()
	return stats
}

func (node *PostgresNode) signalLocked() {
	close(node.notify)
	node.notify = make(chan struct{})
}

func samePostgresNodePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	return strings.EqualFold(left, right)
}

func (node *PostgresNode) closeIdleEngine(engine *Engine) error {
	if engine == nil {
		return nil
	}
	return errors.Join(engine.Close(), node.trimManagerIdle())
}

func (node *PostgresNode) trimManagerIdle() error {
	ctx, cancel := context.WithTimeout(context.Background(), node.databaseAcquireTimeout)
	defer cancel()
	_, err := node.manager.TrimIdle(ctx, 0)
	return err
}

// Close drains every cached relational engine and then the shared handle
// manager. pgwire should stop and close its sessions before this call.
func (node *PostgresNode) Close() error {
	if node == nil {
		return nil
	}
	node.mu.Lock()
	if node.closed {
		done := node.closeDone
		node.mu.Unlock()
		<-done
		node.mu.Lock()
		err := node.closeErr
		node.mu.Unlock()
		return err
	}
	node.closed = true
	engines := make([]*Engine, 0, len(node.engines))
	for _, entry := range node.engines {
		if entry.engine != nil {
			engines = append(engines, entry.engine)
		}
	}
	node.engines = make(map[string]*postgresNodeEngineEntry)
	node.idle.Init()
	node.signalLocked()
	node.mu.Unlock()

	var closeErr error
	for _, engine := range engines {
		closeErr = errors.Join(closeErr, engine.Close())
	}
	closeErr = errors.Join(closeErr, node.manager.Close())
	if node.catalog != nil {
		closeErr = errors.Join(closeErr, node.catalog.Close())
	}
	node.mu.Lock()
	node.closeErr = closeErr
	close(node.closeDone)
	node.mu.Unlock()
	return closeErr
}

var _ pgwire.Authenticator = (*PostgresNode)(nil)
