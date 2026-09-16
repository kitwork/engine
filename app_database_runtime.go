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
	"strconv"
	"strings"
	"sync"

	hostdatabase "github.com/kitwork/engine/database"
	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/managed"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/relational"
)

type appDatabaseRuntime struct {
	cancel           context.CancelFunc
	listener         net.Listener
	serveDone        chan struct{}
	file             *relational.Engine
	node             *relational.PostgresNode
	nativeDatabase   *sql.DB
	nativeRoot       *appDatabaseNativeRoot
	unregisterNative func()
	alias            string
	user             string
	password         string
	kitSQL           bool
	concurrency      int
	closeOnce        sync.Once
	closeErr         error
}

type appDatabaseNativeRoot struct {
	node        *relational.PostgresNode
	concurrency int

	mu        sync.Mutex
	databases map[string]*sql.DB
	closed    bool
}

func (root *appDatabaseNativeRoot) resolve(alias string) (*sql.DB, bool, error) {
	if root == nil || root.node == nil {
		return nil, false, nil
	}
	names, err := root.node.Databases()
	if err != nil {
		return nil, false, err
	}
	var name string
	// Databases returns the virtual maintenance database first. It has no user
	// structs and is deliberately absent from the native application surface.
	for _, candidate := range names[1:] {
		if strings.EqualFold(candidate, strings.TrimSpace(alias)) {
			name = candidate
			break
		}
	}
	if name == "" {
		return nil, false, nil
	}
	key := strings.ToLower(name)
	root.mu.Lock()
	if root.closed {
		root.mu.Unlock()
		return nil, false, fmt.Errorf("KitDB managed root is closed")
	}
	if database := root.databases[key]; database != nil {
		root.mu.Unlock()
		return database, true, nil
	}
	root.mu.Unlock()

	database, err := root.node.OpenNativeDatabase(name)
	if err != nil {
		return nil, false, err
	}
	if root.concurrency > 0 {
		database.SetMaxOpenConns(root.concurrency)
	}
	// A cold logical database must not retain a node lease merely because one
	// tenant queried it once. The node itself decides whether its idle engine
	// remains warm or becomes an LRU eviction candidate.
	database.SetMaxIdleConns(0)

	root.mu.Lock()
	if root.closed {
		root.mu.Unlock()
		_ = database.Close()
		return nil, false, fmt.Errorf("KitDB managed root is closed")
	}
	if current := root.databases[key]; current != nil {
		root.mu.Unlock()
		_ = database.Close()
		return current, true, nil
	}
	root.databases[key] = database
	root.mu.Unlock()
	return database, true, nil
}

func (root *appDatabaseNativeRoot) Close() error {
	if root == nil {
		return nil
	}
	root.mu.Lock()
	if root.closed {
		root.mu.Unlock()
		return nil
	}
	root.closed = true
	databases := make([]*sql.DB, 0, len(root.databases))
	for _, database := range root.databases {
		databases = append(databases, database)
	}
	root.databases = nil
	root.mu.Unlock()
	var closeErr error
	for _, database := range databases {
		closeErr = errors.Join(closeErr, database.Close())
	}
	return closeErr
}

func startAppDatabaseRuntimes(
	parent context.Context,
	configs []AppDatabaseConfig,
) ([]*appDatabaseRuntime, <-chan error, error) {
	errorsChannel := make(chan error, max(1, len(configs)))
	runtimes := make([]*appDatabaseRuntime, 0, len(configs))
	for _, config := range configs {
		runtime, err := startAppDatabaseRuntime(parent, config, errorsChannel)
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

func startAppDatabaseRuntime(
	parent context.Context,
	config AppDatabaseConfig,
	errorsChannel chan<- error,
) (*appDatabaseRuntime, error) {
	if parent == nil {
		return nil, fmt.Errorf("app.database %q: parent context is nil", config.Path)
	}
	absolute, err := filepath.Abs(config.Path)
	if err != nil {
		return nil, fmt.Errorf("app.database %q: resolve path: %w", config.Path, err)
	}
	directory, err := appDatabaseDirectoryTarget(config.Path, absolute)
	if err != nil {
		return nil, err
	}
	if directory && config.Warm && len(config.WarmDatabases) == 0 {
		return nil, fmt.Errorf("app.database %q: warm: true is only valid for one file; name warm databases for a managed root", config.Path)
	}
	if directory && config.Alias != "" {
		return nil, fmt.Errorf("app.database %q: alias requires one KitDB file; managed roots expose databases by their own names", config.Path)
	}
	ctx, cancel := context.WithCancel(parent)
	runtime := &appDatabaseRuntime{
		cancel: cancel, user: config.User, password: config.Password,
		kitSQL: config.KitSQL, concurrency: config.Concurrency,
	}
	options := relational.Options{
		ExperimentalProjections: true,
		BatchAggregates:         true,
		QueryCache:              config.Cache,
		Kernel: kitdbengine.OpenOptions{
			PageCacheBytes: config.MemoryBytes,
		},
	}
	if directory {
		if err := prepareAppDatabaseRoot(absolute); err != nil {
			cancel()
			return nil, fmt.Errorf("app.database %q: %w", config.Path, err)
		}
		limits := kitdbnode.Limits{}
		if config.MemoryBytes > 0 {
			limits.MaxPageCacheBytes = config.MemoryBytes
			limits.DefaultPageCacheBytes = min(config.MemoryBytes, int64(16<<20))
		}
		password := config.Password
		if password == "" {
			password = "embedded-only"
		}
		runtime.node, err = relational.OpenPostgresNode(relational.PostgresNodeOptions{
			Root: absolute, User: config.User, Password: password,
			WarmDatabases:            config.WarmDatabases,
			WarmProjectionOpenPolicy: relational.ProjectionOpenValidate,
			ManagerLimits:            limits,
			Relational:               options,
		})
	} else {
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			cancel()
			return nil, fmt.Errorf("app.database %q: create parent: %w", config.Path, err)
		}
		if config.WarmDatabases != nil {
			cancel()
			return nil, fmt.Errorf("app.database %q: named warm databases require a directory target", config.Path)
		}
		if config.Warm {
			options.ProjectionOpenPolicy = relational.ProjectionOpenValidate
		}
		runtime.file, err = relational.OpenWithContext(ctx, absolute, options)
	}
	if err != nil {
		cancel()
		return nil, fmt.Errorf("app.database %q: open: %w", config.Path, err)
	}
	if runtime.file != nil {
		runtime.alias = config.Alias
		if runtime.alias == "" {
			runtime.alias = "default"
		}
		if _, exists := hostdatabase.Configs[runtime.alias]; exists {
			_ = runtime.Close()
			return nil, fmt.Errorf("app.database %q: alias %q conflicts with an external database declaration", config.Path, runtime.alias)
		}
		runtime.nativeDatabase, err = relational.OpenNativeSQL(runtime.file)
		if err != nil {
			_ = runtime.Close()
			return nil, fmt.Errorf("app.database %q: open native connection: %w", config.Path, err)
		}
		if config.Concurrency > 0 {
			runtime.nativeDatabase.SetMaxOpenConns(config.Concurrency)
			runtime.nativeDatabase.SetMaxIdleConns(config.Concurrency)
		}
		runtime.unregisterNative, err = hostdatabase.RegisterOwned(runtime.alias, runtime.nativeDatabase)
		if err != nil {
			_ = runtime.Close()
			return nil, fmt.Errorf("app.database %q: publish native connection: %w", config.Path, err)
		}
	} else if runtime.node != nil {
		runtime.nativeRoot = &appDatabaseNativeRoot{
			node: runtime.node, concurrency: config.Concurrency,
			databases: make(map[string]*sql.DB),
		}
		runtime.unregisterNative, err = hostdatabase.RegisterOwnedResolver(runtime.nativeRoot.resolve)
		if err != nil {
			_ = runtime.Close()
			return nil, fmt.Errorf("app.database %q: publish native database root: %w", config.Path, err)
		}
	}
	if config.Port == 0 {
		if directory {
			slog.Info("KitDB managed root opened for native access", "path", absolute)
		} else {
			slog.Info("KitDB opened for native access", "path", absolute, "alias", runtime.alias)
		}
		return runtime, nil
	}
	address := net.JoinHostPort(config.Host, strconv.Itoa(config.Port))
	runtime.listener, err = net.Listen("tcp", address)
	if err != nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("app.database %q: listen on %s: %w", config.Path, address, err)
	}
	serverOptions := relational.PostgresServerOptions{}
	if config.Concurrency > 0 {
		serverOptions.MaxConcurrentQueries = config.Concurrency
		serverOptions.MaxConcurrentQueriesPerKey = config.Concurrency
		serverOptions.MaxConnections = max(16, config.Concurrency*4)
	}
	listener := runtime.listener
	runtime.serveDone = make(chan struct{})
	go func() {
		defer close(runtime.serveDone)
		var serveErr error
		if runtime.node != nil {
			serveErr = runtime.node.ServePostgres(ctx, listener, serverOptions)
		} else {
			serverOptions.PostgresOptions = relational.PostgresOptions{
				User: config.User, Password: config.Password,
			}
			serveErr = runtime.file.ServePostgres(ctx, listener, serverOptions)
		}
		if serveErr != nil && ctx.Err() == nil && !errors.Is(serveErr, net.ErrClosed) {
			select {
			case errorsChannel <- fmt.Errorf("app.database %q stopped: %w", config.Path, serveErr):
			default:
			}
		}
	}()
	slog.Info("KitDB PostgreSQL listener started", "path", absolute, "alias", runtime.alias, "address", runtime.listener.Addr(), "managed", directory)
	return runtime, nil
}

func appDatabaseDirectoryTarget(source, absolute string) (bool, error) {
	if strings.HasSuffix(source, "/") || strings.HasSuffix(source, "\\") || filepath.Base(filepath.Clean(source)) == ".kitdb" {
		return true, nil
	}
	info, err := os.Stat(absolute)
	if err == nil {
		return info.IsDir(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("app.database %q: inspect path: %w", source, err)
}

func prepareAppDatabaseRoot(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		_, err = managed.Init(path)
		return err
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("managed root is not a directory")
	}
	present, err := managed.Present(path)
	if err != nil || present {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		_, err = managed.Init(path)
		return err
	}
	// Keep legacy flat roots readable. They can be adopted explicitly later;
	// silently creating .catalog beside existing data would change authority.
	return nil
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
		if runtime.unregisterNative != nil {
			runtime.unregisterNative()
		}
		if runtime.nativeDatabase != nil {
			runtime.closeErr = errors.Join(runtime.closeErr, runtime.nativeDatabase.Close())
		}
		if runtime.nativeRoot != nil {
			runtime.closeErr = errors.Join(runtime.closeErr, runtime.nativeRoot.Close())
		}
		if runtime.node != nil {
			runtime.closeErr = errors.Join(runtime.closeErr, runtime.node.Close())
		}
		if runtime.file != nil {
			runtime.closeErr = errors.Join(runtime.closeErr, runtime.file.Close())
		}
	})
	return runtime.closeErr
}
