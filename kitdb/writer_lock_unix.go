//go:build !windows

package kitdb

import (
	"errors"
	"os"
	"syscall"
)

type writerLockHandle struct {
	file *os.File
}

func lockWriterFile(path string) (*writerLockHandle, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, writerLockError(err)
		}
		return nil, err
	}
	return &writerLockHandle{file: file}, nil
}

func (lock *writerLockHandle) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}
