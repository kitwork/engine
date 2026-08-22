package kitdb

import (
	"errors"
	"fmt"
)

const writerLockSuffix = ".lock"

type writerLock struct {
	handle *writerLockHandle
}

func acquireWriterLock(databasePath string) (*writerLock, error) {
	path := databasePath + writerLockSuffix
	handle, err := lockWriterFile(path)
	if err != nil {
		return nil, fmt.Errorf("kitdb: lock writer for %q: %w", databasePath, err)
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
		return fmt.Errorf("kitdb: close writer lock: %w", err)
	}
	return nil
}

func writerLockError(err error) error {
	if err == nil {
		return nil
	}
	return errors.Join(ErrWriterLocked, err)
}
