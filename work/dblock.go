package work

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kitwork/engine/database"
)

// Single-instance guard for local file databases.
//
// A turso (or sqlite) file database is SINGLE-PROCESS: its write-ahead log is not shared across
// separate OS processes the way one might expect, so two kitwork instances opening the same file
// silently diverge — a row written by one is invisible to the other ("created a link, reloaded, it's
// gone"), and worse, a migration/rebuild running in that split-brain state can wipe existing rows.
// This guard takes an exclusive, process-scoped OS lock on "<db>.lock" the first time this process
// opens a given file, so a SECOND kitwork process opening the same file fails LOUDLY and early instead
// of corrupting data. The OS releases the lock when the process exits (even on crash), so there is no
// stale-lock problem.
//
// Scope: this stops a second kitwork PROCESS. It cannot stop an external SQLite tool from opening the
// raw .db directly (turso itself must be able to open that file, so we cannot lock it exclusively) —
// opening the live file with a db manager while the server runs remains unsafe by its own nature.

var (
	dbLocksMu sync.Mutex
	dbLocks   = map[string]*dbLockHandle{} // absolute db path -> held OS lock (process-global, never re-taken)
)

// lockFilePath maps a database's absolute path to its lock file, kept in the OS temp dir (named by a
// hash of the db path) rather than beside the db. That keeps .data/ clean and, crucially, never leaves
// an open handle inside a directory a caller may want to remove (e.g. a test's temp dir). The name is
// deterministic, so a second process derives the SAME lock file for the same db.
func lockFilePath(dbPath string) string {
	sum := sha256.Sum256([]byte(dbPath))
	return filepath.Join(os.TempDir(), "kitwork-dblock-"+hex.EncodeToString(sum[:8])+".lock")
}

// lockableDBPath returns the absolute file path for a single-process file backend (sqlite/turso) that
// should be guarded, and false for :memory: or network backends (postgres/mysql) that need no lock.
func lockableDBPath(config *database.Config) (string, bool) {
	if config == nil {
		return "", false
	}
	kind := strings.ToLower(config.Type)
	if kind != "sqlite" && kind != "sqlite3" && kind != "turso" {
		return "", false
	}
	path := config.Name
	if path == "" {
		path = config.Host
	}
	if path == "" || strings.Contains(path, ":memory:") {
		return "", false
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = filepath.Clean(abs)
	}
	return path, true
}

// acquireDBLock takes the exclusive lock for dbPath. Idempotent per process: a path this process
// already holds returns nil (so re-opening across hot-reloads is fine). A path held by ANOTHER process
// returns an actionable error.
func acquireDBLock(dbPath string) error {
	dbLocksMu.Lock()
	defer dbLocksMu.Unlock()
	if _, held := dbLocks[dbPath]; held {
		return nil
	}
	handle, err := lockFile(lockFilePath(dbPath))
	if err != nil {
		return fmt.Errorf(
			"database %q is already open by another process — turso/sqlite file databases are single-process; "+
				"stop the other kitwork instance (and close any db tool holding the file), then retry",
			filepath.Base(dbPath),
		)
	}
	dbLocks[dbPath] = handle
	return nil
}

// releaseDBLock drops a held lock (used on clean shutdown; the OS also releases on process exit).
func releaseDBLock(dbPath string) {
	dbLocksMu.Lock()
	defer dbLocksMu.Unlock()
	if handle, held := dbLocks[dbPath]; held {
		handle.close()
		delete(dbLocks, dbPath)
	}
}
