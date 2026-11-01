//go:build windows

package sqlite

import (
	"errors"
	"syscall"
)

const (
	windowsSharingViolation syscall.Errno = 32
	windowsLockViolation    syscall.Errno = 33
)

// fileLock is held by an open handle with no sharing: the operating system
// releases it when the process exits or crashes.
type fileLock struct {
	handle syscall.Handle
}

func lockFile(path string) (*fileLock, error) {
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(pointer, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windowsSharingViolation) || errors.Is(err, windowsLockViolation) {
			return nil, errors.Join(ErrLocked, err)
		}
		return nil, err
	}
	return &fileLock{handle: handle}, nil
}

func (lock *fileLock) close() error {
	if lock == nil || lock.handle == 0 || lock.handle == syscall.InvalidHandle {
		return nil
	}
	err := syscall.CloseHandle(lock.handle)
	lock.handle = syscall.InvalidHandle
	return err
}
