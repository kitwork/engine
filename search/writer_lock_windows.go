//go:build windows

package search

import (
	"errors"
	"syscall"
)

const (
	windowsSharingViolation syscall.Errno = 32
	windowsLockViolation    syscall.Errno = 33
)

type writerLockHandle struct {
	handle syscall.Handle
}

func lockWriterFile(path string) (*writerLockHandle, error) {
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(
		pointer,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0,
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		if errors.Is(err, windowsSharingViolation) || errors.Is(err, windowsLockViolation) {
			return nil, writerLockError(err)
		}
		return nil, err
	}
	return &writerLockHandle{handle: handle}, nil
}

func (lock *writerLockHandle) close() error {
	if lock == nil || lock.handle == 0 || lock.handle == syscall.InvalidHandle {
		return nil
	}
	err := syscall.CloseHandle(lock.handle)
	lock.handle = syscall.InvalidHandle
	return err
}
