package gateway

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// On Windows SQLite makes its temporary files (etilqs_*, deleted when closed, listed while open) in
// sqlite3_temp_directory, else in the directory GetTempPath names, which other programs share: a browser keeps
// SQLite files too. So the process gets a directory of its own, kitwork-sqlite-<pid> in that one, set once through
// PRAGMA temp_store_directory, which SQLite guards with a mutex wherever it reads or sets it: the temporary files
// every connection of the process makes from then on are there, and only they are. The pragma runs on a connection of
// its own: setting it closes the TEMP database of the connection that runs it, which holds a session's catalog.

const temporarySupported = true

var temporaryDirectory struct {
	once sync.Once
	path string
}

// prepareTemporaryFiles gives the process's SQLite temporary files a directory of their own, once, and makes it again
// when it is gone (another program cleaning the temporary directory). A directory it cannot make or set leaves the
// files where SQLite puts them, unmeasured.
func prepareTemporaryFiles(ctx context.Context) {
	if path := temporaryDirectory.path; path != "" {
		if _, err := os.Stat(path); err != nil {
			_ = os.MkdirAll(path, 0o700)
		}
	}
	temporaryDirectory.once.Do(func() {
		parent := os.TempDir()
		own := "kitwork-sqlite-" + strconv.Itoa(os.Getpid())
		removeStaleTemporaryDirectories(parent, own, processRunning)
		path := filepath.Join(parent, own)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return
		}
		memory, err := sql.Open("sqlite", "file::memory:")
		if err != nil {
			return
		}
		defer memory.Close()
		if _, err := memory.ExecContext(ctx, "PRAGMA temp_store_directory = "+quoteLiteral(path)); err != nil {
			return
		}
		temporaryDirectory.path = path
	})
}

// removeStaleTemporaryDirectories removes the empty kitwork-sqlite-<pid> directories of processes no longer running.
// The directory of a process that runs is left alone, empty or not: it is empty most of the time, between the statements
// that spill, and SQLite cannot make a temporary file once it is gone ("unable to open database file"; until
// 05/11/2026 every empty one was removed, so two engines on one machine broke each other's sorts).
func removeStaleTemporaryDirectories(parent, own string, running func(int) bool) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, "kitwork-sqlite-") || name == own {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimPrefix(name, "kitwork-sqlite-"))
		if err != nil || pid <= 0 || running(pid) {
			continue
		}
		_ = os.Remove(filepath.Join(parent, name)) // an empty one, left by a process gone
	}
}

// processRunning reports whether a process of that id runs (one the user may open).
func processRunning(pid int) bool {
	process, err := os.FindProcess(pid) // OpenProcess on Windows: an error when no such process runs
	if err != nil {
		return false
	}
	_ = process.Release()
	return true
}

// sqliteTemporaryBytes is the size of the process's SQLite temporary files.
func sqliteTemporaryBytes() (int64, bool) {
	path := temporaryDirectory.path
	if path == "" {
		return 0, false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, false
	}
	var total int64
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "etilqs_") {
			continue
		}
		// the size the directory lists can lag behind a file being written; the file's own is current
		if info, err := os.Stat(filepath.Join(path, entry.Name())); err == nil {
			total += info.Size()
		}
	}
	return total, true
}
