package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// On Linux SQLite removes the name of a temporary file (etilqs_*) as soon as it opens it, so no directory lists it;
// the file stays reachable through the process's descriptor of it, /proc/self/fd/<n>, whose link reads
// "<directory>/etilqs_… (deleted)". Those are the process's own files whatever directory SQLite chose.

const temporarySupported = true

func prepareTemporaryFiles(context.Context) {}

// sqliteTemporaryBytes is the size of the process's SQLite temporary files.
func sqliteTemporaryBytes() (int64, bool) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	var total int64
	for _, entry := range entries {
		link := filepath.Join("/proc/self/fd", entry.Name())
		target, err := os.Readlink(link)
		if err != nil || !strings.HasPrefix(filepath.Base(strings.TrimSuffix(target, " (deleted)")), "etilqs_") {
			continue
		}
		if info, err := os.Stat(link); err == nil { // the file the descriptor holds, named or not
			total += info.Size()
		}
	}
	return total, true
}
