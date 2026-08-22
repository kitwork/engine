//go:build windows

package kitdb

// Go's os package cannot open a Windows directory for FlushFileBuffers.
func syncDirectory(string) error { return nil }
