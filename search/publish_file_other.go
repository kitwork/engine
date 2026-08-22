//go:build !windows

package search

import "os"

func publishFile(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}
