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

// prepareTemporaryFiles gives the process's SQLite temporary files a directory of their own, once. A directory it
// cannot make or set leaves the files where SQLite puts them, unmeasured.
func prepareTemporaryFiles(ctx context.Context) {
	temporaryDirectory.once.Do(func() {
		parent := os.TempDir()
		own := "kitwork-sqlite-" + strconv.Itoa(os.Getpid())
		if entries, err := os.ReadDir(parent); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && strings.HasPrefix(entry.Name(), "kitwork-sqlite-") && entry.Name() != own {
					_ = os.Remove(filepath.Join(parent, entry.Name())) // an empty one, left by a process gone
				}
			}
		}
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
