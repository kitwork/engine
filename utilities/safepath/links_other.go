//go:build !windows

package safepath

import "path/filepath"

// resolveLinks is the path with every symbolic link on the way followed.
func resolveLinks(name string) (string, error) { return filepath.EvalSymlinks(name) }
