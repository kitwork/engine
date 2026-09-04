package relational

import (
	"container/list"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/pgwire"
)

const (
	DefaultMaximumDiscoveredDatabases = 4_096
	MaximumDiscoveredDatabases        = 100_000
	DefaultDatabaseAcquireTimeout     = 10 * time.Second
	MaximumDatabaseAcquireTimeout     = 10 * time.Minute
)

// PostgresNodeOptions configures one standalone PostgreSQL endpoint over the
// non-hidden .kitdb files immediately below Root. Files are discovered without
// opening them; selected databases are opened lazily through ManagerLimits.
type PostgresNodeOptions struct {
	Root                       string
	MaintenanceDatabase        string
	User                       string
	Password                   string
	ReadOnly                   bool
	MaximumDiscoveredDatabases int
	DatabaseAcquireTimeout     time.Duration
	ManagerLimits              kitdbnode.Limits
	Relational                 Options
	Trace                      func(database, source string, parameterCount int)
}

type postgresNodeDatabase struct {
	name string
	path string
}

type postgresNodeEngineEntry struct {
	database postgresNodeDatabase
	engine   *Engine
	ready    chan struct{}
	opening  bool
	sessions int
	idle     *list.Element
	err      error
}

// PostgresNode owns bounded, lazily opened relational engines for one database
// directory. It is independent from Kitwork Tenant, VM, routing and app state.
type PostgresNode struct {
	root                   string
	maintenanceDatabase    string
	user                   string
	password               string
	readOnly               bool
	maximumDiscovered      int
	manager                *kitdbnode.Manager
	managerMaximumOpen     int
	managerMaximumCache    int64
	databaseCacheBytes     int64
	databaseAcquireTimeout time.Duration
	relational             Options
	maximumResults         int
	maximumMutations       int
	trace                  func(database, source string, parameterCount int)

	mu        sync.Mutex
	engines   map[string]*postgresNodeEngineEntry
	idle      list.List
	notify    chan struct{}
	closed    bool
	closeDone chan struct{}
	closeErr  error
}

// OpenPostgresNode creates a metadata-only node. It does not open any .kitdb
// file until an authenticated session selects that logical database.
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
		managerMaximumOpen:     managerStats.MaxOpenDatabases,
		managerMaximumCache:    managerStats.MaxPageCacheBytes,
		databaseCacheBytes:     databaseCacheBytes,
		databaseAcquireTimeout: acquireTimeout,
		relational:             options.Relational,
		maximumResults:         maximumResults, maximumMutations: maximumMutations,
		trace:   options.Trace,
		engines: make(map[string]*postgresNodeEngineEntry),
		notify:  make(chan struct{}), closeDone: make(chan struct{}),
	}
	if _, err := node.discoverDatabases(); err != nil {
		_ = manager.Close()
		return nil, err
	}
	return node, nil
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
		Authenticator:             node,
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
		if errors.Is(err, context.DeadlineExceeded) {
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

		entry := &postgresNodeEngineEntry{
			database: database, ready: make(chan struct{}), opening: true, sessions: 1,
		}
		node.engines[key] = entry
		node.mu.Unlock()

		lease, err := node.manager.Acquire(ctx, database.path, node.relational.Kernel)
		if err != nil {
			node.failEngineOpen(key, entry, err)
			return nil, err
		}
		engine, err := newEngineWithDatabase(
			lease.DB(), node.relational, node.maximumResults, node.maximumMutations, lease.Release,
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
	defer node.mu.Unlock()
	current := node.engines[strings.ToLower(entry.database.name)]
	if current != entry || entry.sessions == 0 {
		return nil
	}
	entry.sessions--
	if entry.sessions == 0 && !entry.opening {
		entry.idle = node.idle.PushFront(entry)
	}
	node.signalLocked()
	return nil
}

func (node *PostgresNode) oldestIdleLocked() *postgresNodeEngineEntry {
	oldest := node.idle.Back()
	if oldest == nil {
		return nil
	}
	return oldest.Value.(*postgresNodeEngineEntry)
}

func (node *PostgresNode) removeIdleLocked(entry *postgresNodeEngineEntry) {
	if entry == nil || entry.idle == nil {
		return
	}
	node.idle.Remove(entry.idle)
	entry.idle = nil
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
	node.mu.Lock()
	node.closeErr = closeErr
	close(node.closeDone)
	node.mu.Unlock()
	return closeErr
}

var _ pgwire.Authenticator = (*PostgresNode)(nil)
