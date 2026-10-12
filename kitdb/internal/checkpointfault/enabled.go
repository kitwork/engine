//go:build kitdbfault

// Package checkpointfault provides process-exit boundaries and storage I/O
// failures only in tagged fault-lab builds, not production configuration.
package checkpointfault

import "os"

func Reach(path, point string) {
	if os.Getenv("KITDB_CHECKPOINT_FAULT_PATH") != path || os.Getenv("KITDB_CHECKPOINT_FAULT_POINT") != point {
		return
	}
	file, err := os.OpenFile(path+".fault-reached", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(74)
	}
	if _, err := file.WriteString(point); err != nil {
		os.Exit(74)
	}
	if err := file.Sync(); err != nil {
		os.Exit(74)
	}
	os.Exit(73) // No defers, handle close, manager drain or checkpoint cleanup.
}
