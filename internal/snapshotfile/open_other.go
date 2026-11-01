//go:build !windows

package snapshotfile

import "os"

// openRenamable opens path for reading; POSIX already lets an open file be renamed.
func openRenamable(path string) (*os.File, error) { return os.Open(path) }
