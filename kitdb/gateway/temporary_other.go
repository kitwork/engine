//go:build !windows && !linux

package gateway

import "context"

// Elsewhere (macOS, the BSDs) SQLite removes a temporary file's name as soon as it opens it and the process has no
// /proc to find it by: the files are not measured and no statement is stopped for them.

const temporarySupported = false

func prepareTemporaryFiles(context.Context) {}

func sqliteTemporaryBytes() (int64, bool) { return 0, false }
