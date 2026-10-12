//go:build !kitdbfault

package checkpointfault

import "io"

// Reach is compiled to a no-op in ordinary builds.
func Reach(path, point string) {}

func Error(path, point string) error { return nil }

func WriteError(path, point string, file io.Writer, data []byte, offset int64) error {
	return nil
}
