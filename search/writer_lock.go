package search

import (
	"errors"
	"fmt"
	"path/filepath"
)

const writerLockFilename = ".kitwork-search.writer.lock"

type writerLock struct {
	handle *writerLockHandle
}

func acquireWriterLock(directory string) (*writerLock, error) {
	path := filepath.Join(directory, writerLockFilename)
	handle, err := lockWriterFile(path)
	if err != nil {
		return nil, fmt.Errorf("search: lock index writer for %q: %w", directory, err)
	}
	return &writerLock{handle: handle}, nil
}

func (lock *writerLock) close() error {
	if lock == nil || lock.handle == nil {
		return nil
	}
	err := lock.handle.close()
	lock.handle = nil
	if err != nil {
		return fmt.Errorf("search: close writer lock: %w", err)
	}
	return nil
}

func writerLockError(err error) error {
	if err == nil {
		return nil
	}
	return errors.Join(ErrWriterLocked, err)
}
