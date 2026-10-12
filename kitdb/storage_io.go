package kitdb

import (
	"io"
	"os"

	"github.com/kitwork/engine/kitdb/internal/checkpointfault"
)

// Ordinary builds compile the fault checks away. Tagged fault-lab builds
// exercise the same callers and error handling with real partial file writes.
func storageWrite(path, point string, file io.Writer, data []byte) (int, error) {
	if err := checkpointfault.WriteError(path, point, file, data, -1); err != nil {
		return 0, err
	}
	return writeAll(file, data)
}

func storageWriteAt(path, point string, file *os.File, data []byte, offset int64) error {
	if err := checkpointfault.WriteError(path, point, file, data, offset); err != nil {
		return err
	}
	return writeAllAt(file, data, offset)
}

func storageSync(path, point string, file interface{ Sync() error }) error {
	if err := checkpointfault.Error(path, point); err != nil {
		return err
	}
	return file.Sync()
}

func storageReplace(path, point, staging, target string) error {
	if err := checkpointfault.Error(path, point); err != nil {
		return err
	}
	return replaceFile(staging, target)
}

func storageSyncDirectory(path, point, directory string) error {
	if err := checkpointfault.Error(path, point); err != nil {
		return err
	}
	return syncDirectory(directory)
}
