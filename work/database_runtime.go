package work

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/database"
)

func (t *Tenant) databaseManager() *app.DatabaseManager {
	if t == nil || t.appRuntime == nil {
		return nil
	}
	return t.appRuntime.Databases()
}

func databaseConnectionKey(config *database.Config) string {
	if config == nil {
		return ""
	}
	kind := strings.ToLower(config.Type)
	path := config.Name
	if path == "" {
		path = config.Host
	}
	if kind == "sqlite" || kind == "sqlite3" {
		if path != ":memory:" {
			if absolute, err := filepath.Abs(path); err == nil {
				path = filepath.Clean(absolute)
			}
		}
		return "sqlite:" + path
	}
	if dsn, err := config.BuildDSN(); err == nil {
		return kind + ":" + dsn
	}
	return fmt.Sprintf("%s:%s:%s:%d:%s", kind, config.Alias, config.Host, config.Port, config.Name)
}

func (t *Tenant) openDatabase(config *database.Config) (*sql.DB, error) {
	manager := t.databaseManager()
	if manager == nil {
		return nil, fmt.Errorf("app database manager is unavailable")
	}
	key := databaseConnectionKey(config)
	connect := config.Connect
	lockPath, guarded := lockableDBPath(config)
	// A local file backend is single-process: take the single-instance lock BEFORE opening it, so a
	// second kitwork process fails loudly here instead of silently diverging (see dblock.go). Wrapped
	// inside the connector so it runs exactly once per file (manager.Open calls it only on a cache miss).
	if guarded {
		connect = func() (*sql.DB, error) {
			if err := acquireDBLock(lockPath); err != nil {
				return nil, err
			}
			return config.Connect()
		}
	}
	conn, err := manager.Open(key, connect)
	if err == nil && guarded {
		// Release the lock when this app's connections are torn down (Runtime.Close / hot-reload).
		manager.SetCleanup(key, func() { releaseDBLock(lockPath) })
	}
	return conn, err
}

func (t *Tenant) lookupDatabase(config *database.Config) *sql.DB {
	manager := t.databaseManager()
	if manager == nil {
		return nil
	}
	return manager.Lookup(databaseConnectionKey(config))
}
