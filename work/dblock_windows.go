//go:build windows

package work

import "syscall"

// dbLockHandle holds the exclusive Windows file handle for the lifetime of the lock.
type dbLockHandle struct{ h syscall.Handle }

// lockFile opens the lock file with dwShareMode = 0 (no sharing): a second CreateFile on the same path
// — from any process — fails with ERROR_SHARING_VIOLATION. That IS the lock. Closing the handle (or the
// process exiting) releases it.
func lockFile(path string) (*dbLockHandle, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(
		ptr,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // exclusive: deny other opens
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return &dbLockHandle{h: h}, nil
}

func (l *dbLockHandle) close() {
	if l != nil && l.h != 0 && l.h != syscall.InvalidHandle {
		_ = syscall.CloseHandle(l.h)
		l.h = syscall.InvalidHandle
	}
}
