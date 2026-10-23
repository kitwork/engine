//go:build !windows

package file

import (
	"io/fs"
	"time"
)

// createdAt is zero: the standard library reads no time of creation here (Linux keeps it only through
// statx), so FileInfo.createdAt is null, as mục 6 allows.
func createdAt(fs.FileInfo) time.Time { return time.Time{} }
