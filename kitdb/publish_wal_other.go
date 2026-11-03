//go:build !windows

package kitdb

import "os"

func createWALRotationStaging(directory, pattern string) (*os.File, error) {
	return os.CreateTemp(directory, pattern)
}

func publishWALFile(staging, target string) (bool, error) {
	err := replaceFile(staging, target)
	return err == nil, err
}
