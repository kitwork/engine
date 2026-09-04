//go:build !windows

package snapshotfile

import "os"

func syncParent(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
